package routing

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MarcoVanLill/ForgeGate/internal/providers"
	"github.com/MarcoVanLill/ForgeGate/internal/storage"
	"github.com/google/uuid"
)

type flakyProvider struct {
	name     string
	failures int
	class    providers.ErrorClass
	calls    int
}

func (p *flakyProvider) Name() string { return p.name }
func (p *flakyProvider) Complete(context.Context, providers.Request) (providers.Response, error) {
	p.calls++
	if p.calls <= p.failures {
		return providers.Response{}, &providers.Error{Class: p.class, Provider: p.name, Retryable: p.class == providers.ErrorTransient}
	}
	return providers.Response{Text: "ok"}, nil
}
func (p *flakyProvider) Stream(context.Context, providers.Request) (<-chan providers.Chunk, <-chan error) {
	return make(chan providers.Chunk), make(chan error)
}
func (p *flakyProvider) Health(context.Context) providers.Health {
	return providers.Health{Healthy: true}
}

type staticSource struct{ configs []storage.ProviderConfig }

func (s staticSource) ProviderConfigs(context.Context, uuid.UUID, string) ([]storage.ProviderConfig, error) {
	return s.configs, nil
}
func TestFallback(t *testing.T) {
	source := staticSource{[]storage.ProviderConfig{{Name: "primary", Kind: "fake", Timeout: time.Second, MaxRetries: 0, MaxConcurrency: 1, Settings: json.RawMessage(`{}`)}, {Name: "secondary", Kind: "fake", Timeout: time.Second, MaxRetries: 0, MaxConcurrency: 1, Settings: json.RawMessage(`{}`)}}}
	engine := New(source, func(c storage.ProviderConfig) (providers.Provider, error) {
		fail := map[string]providers.ErrorClass{}
		if c.Name == "primary" {
			fail["fallback"] = providers.ErrorUnavailable
		}
		return providers.NewFake(providers.FakeConfig{ProviderName: c.Name, FailModels: fail}), nil
	}, nil)
	result, err := engine.Complete(context.Background(), uuid.New(), providers.Request{Model: "fallback", Input: "hello"})
	if err != nil {
		t.Fatalf("Complete() error=%v", err)
	}
	if result.Provider != "secondary" || result.Fallbacks != 1 {
		t.Fatalf("result=%#v", result)
	}
}
func TestCircuitOpens(t *testing.T) {
	source := staticSource{[]storage.ProviderConfig{{Name: "broken", Timeout: time.Second, MaxRetries: 0, MaxConcurrency: 1}}}
	engine := New(source, func(c storage.ProviderConfig) (providers.Provider, error) {
		return providers.NewFake(providers.FakeConfig{ProviderName: c.Name, FailModels: map[string]providers.ErrorClass{"fail": providers.ErrorUnavailable}}), nil
	}, nil)
	tenantID := uuid.New()
	for i := 0; i < 3; i++ {
		_, _ = engine.Complete(context.Background(), tenantID, providers.Request{Model: "fail"})
	}
	state, _ := engine.getState(tenantID, source.configs[0])
	if !state.circuit.open() {
		t.Fatal("circuit is closed, want open")
	}
}

func TestRetriesOnlySafeErrors(t *testing.T) {
	tests := []struct {
		name      string
		class     providers.ErrorClass
		retryable bool
		wantCalls int
		wantErr   bool
	}{
		{name: "transient is retried", class: providers.ErrorTransient, retryable: true, wantCalls: 2},
		{name: "invalid is not retried", class: providers.ErrorInvalid, retryable: false, wantCalls: 1, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &flakyProvider{name: "test", failures: 1, class: tt.class}
			source := staticSource{[]storage.ProviderConfig{{Name: "test", Timeout: time.Second, MaxRetries: 2, MaxConcurrency: 1}}}
			engine := New(source, func(storage.ProviderConfig) (providers.Provider, error) { return provider, nil }, nil)
			engine.sleep = func(context.Context, time.Duration) error { return nil }
			_, err := engine.Complete(context.Background(), uuid.New(), providers.Request{})
			if (err != nil) != tt.wantErr {
				t.Fatalf("error=%v wantErr=%v", err, tt.wantErr)
			}
			if provider.calls != tt.wantCalls {
				t.Fatalf("calls=%d want=%d", provider.calls, tt.wantCalls)
			}
		})
	}
}
