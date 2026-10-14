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
