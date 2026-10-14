package providers

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestFakeProviderContract(t *testing.T) {
	provider := NewFake(FakeConfig{ProviderName: "fake-test"})
	request := Request{Model: "fake/echo", Input: "hello world"}
	response, err := provider.Complete(context.Background(), request)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if !strings.Contains(response.Text, request.Input) || response.Usage.TotalTokens == 0 {
		t.Fatalf("Complete() = %#v", response)
	}
	chunks, errs := provider.Stream(context.Background(), request)
	var output strings.Builder
	var done bool
	for chunk := range chunks {
		output.WriteString(chunk.Text)
		done = done || chunk.Done
	}
	if err := <-errs; err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if !done || output.String() != response.Text {
		t.Fatalf("Stream() output = %q, done = %v", output.String(), done)
	}
	if !provider.Health(context.Background()).Healthy {
		t.Fatal("Health() = unhealthy")
	}
}
func TestFakeProviderHonorsDeadline(t *testing.T) {
	provider := NewFake(FakeConfig{Latency: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	_, err := provider.Complete(ctx, Request{})
	providerErr, ok := AsError(err)
	if !ok || providerErr.Class != ErrorTimeout {
		t.Fatalf("Complete() error = %#v", err)
	}
}
