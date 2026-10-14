package providers

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type FakeConfig struct {
	ProviderName string
	Latency      time.Duration
	FailModels   map[string]ErrorClass
}
type Fake struct {
	name       string
	latency    time.Duration
	failModels map[string]ErrorClass
}

func NewFake(cfg FakeConfig) *Fake {
	if cfg.ProviderName == "" {
		cfg.ProviderName = "fake"
	}
	return &Fake{name: cfg.ProviderName, latency: cfg.Latency, failModels: cfg.FailModels}
}
func (f *Fake) Name() string { return f.name }
func (f *Fake) Complete(ctx context.Context, req Request) (Response, error) {
	if err := f.wait(ctx); err != nil {
		return Response{}, err
	}
	if class, ok := f.failModels[req.Model]; ok {
		return Response{}, &Error{Class: class, Provider: f.name, Retryable: isRetryable(class), Message: fmt.Sprintf("%s injected %s failure", f.name, class)}
	}
	text := "ForgeGate fake response: " + req.Input
	return Response{Text: text, Usage: tokenUsage(req.Input, text)}, nil
}
func (f *Fake) Stream(ctx context.Context, req Request) (<-chan Chunk, <-chan error) {
	chunks := make(chan Chunk)
	errs := make(chan error, 1)
	go func() {
		defer close(chunks)
		defer close(errs)
		response, err := f.Complete(ctx, req)
		if err != nil {
			errs <- err
			return
		}
		words := strings.Fields(response.Text)
		for i, word := range words {
			sep := " "
			if i == len(words)-1 {
				sep = ""
			}
			select {
			case <-ctx.Done():
				errs <- &Error{Class: ErrorTimeout, Provider: f.name, Retryable: true, Message: "stream cancelled", Cause: ctx.Err()}
				return
			case chunks <- Chunk{Text: word + sep}:
			}
		}
		chunks <- Chunk{Done: true, Usage: &response.Usage}
	}()
	return chunks, errs
}
func (f *Fake) Health(context.Context) Health {
	return Health{Healthy: true, Detail: "deterministic in-process provider"}
}
func (f *Fake) wait(ctx context.Context) error {
	if f.latency <= 0 {
		return nil
	}
	timer := time.NewTimer(f.latency)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return &Error{Class: ErrorTimeout, Provider: f.name, Retryable: true, Message: "provider deadline exceeded", Cause: ctx.Err()}
	}
}
func tokenUsage(input, output string) Usage {
	in := len(strings.Fields(input))
	out := len(strings.Fields(output))
	return Usage{InputTokens: in, OutputTokens: out, TotalTokens: in + out}
}
func isRetryable(class ErrorClass) bool {
	return class == ErrorRateLimited || class == ErrorTransient || class == ErrorTimeout || class == ErrorUnavailable
}
