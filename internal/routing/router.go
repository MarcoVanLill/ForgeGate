package routing

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/MarcoVanLill/ForgeGate/internal/providers"
	"github.com/MarcoVanLill/ForgeGate/internal/storage"
	"github.com/google/uuid"
)

var ErrSaturated = errors.New("provider concurrency limit reached")

type ConfigSource interface {
	ProviderConfigs(context.Context, uuid.UUID, string) ([]storage.ProviderConfig, error)
}
type Factory func(storage.ProviderConfig) (providers.Provider, error)
type Observer interface {
	ProviderCall(provider, outcome string, duration time.Duration)
	ProviderFallback(from, to string)
	CircuitState(provider, state string)
}
type noopObserver struct{}

func (noopObserver) ProviderCall(string, string, time.Duration) {}
func (noopObserver) ProviderFallback(string, string)            {}
func (noopObserver) CircuitState(string, string)                {}

type state struct {
	provider  providers.Provider
	semaphore chan struct{}
	circuit   *circuit
}
type Engine struct {
	source   ConfigSource
	factory  Factory
	observer Observer
	mu       sync.Mutex
	states   map[string]*state
	sleep    func(context.Context, time.Duration) error
}

func New(source ConfigSource, factory Factory, observer Observer) *Engine {
	if observer == nil {
		observer = noopObserver{}
	}
	return &Engine{source: source, factory: factory, observer: observer, states: make(map[string]*state), sleep: sleepContext}
}

type Result struct {
	Response  providers.Response
	Provider  string
	Fallbacks int
}

func (e *Engine) Complete(ctx context.Context, tenantID uuid.UUID, request providers.Request) (Result, error) {
	configs, err := e.source.ProviderConfigs(ctx, tenantID, request.Model)
	if err != nil {
		return Result{}, err
	}
	if len(configs) == 0 {
		return Result{}, &providers.Error{Class: providers.ErrorUnavailable, Message: "no provider route is configured"}
	}
	var lastErr error
	previous := ""
	for index, cfg := range configs {
		if previous != "" {
			e.observer.ProviderFallback(previous, cfg.Name)
		}
		providerState, err := e.getState(tenantID, cfg)
		if err != nil {
			return Result{}, err
		}
		if !providerState.circuit.allow() {
			e.observer.CircuitState(cfg.Name, "open")
			lastErr = &providers.Error{Class: providers.ErrorUnavailable, Provider: cfg.Name, Retryable: true, Message: "provider circuit is open"}
			previous = cfg.Name
			continue
		}
		response, err := e.tryComplete(ctx, providerState, cfg, request)
		if err == nil {
			providerState.circuit.success()
			e.observer.CircuitState(cfg.Name, "closed")
			return Result{Response: response, Provider: cfg.Name, Fallbacks: index}, nil
		}
		lastErr = err
		providerState.circuit.failure()
		if providerState.circuit.open() {
			e.observer.CircuitState(cfg.Name, "open")
		}
		previous = cfg.Name
	}
	return Result{}, lastErr
}
func (e *Engine) tryComplete(ctx context.Context, s *state, cfg storage.ProviderConfig, request providers.Request) (providers.Response, error) {
	if !acquire(s.semaphore) {
		return providers.Response{}, &providers.Error{Class: providers.ErrorUnavailable, Provider: cfg.Name, Retryable: true, Message: ErrSaturated.Error(), Cause: ErrSaturated}
	}
	defer release(s.semaphore)
	var lastErr error
	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
		started := time.Now()
		response, err := s.provider.Complete(attemptCtx, request)
		cancel()
		outcome := "success"
		if err != nil {
			outcome = "failure"
		}
		e.observer.ProviderCall(cfg.Name, outcome, time.Since(started))
		if err == nil {
			return response, nil
		}
		lastErr = err
		providerErr, ok := providers.AsError(err)
		if !ok || !providerErr.Retryable || attempt == cfg.MaxRetries {
			return providers.Response{}, err
		}
		delay := time.Duration(25*(1<<attempt)+(len(cfg.Name)+attempt*13)%25) * time.Millisecond
		if err := e.sleep(ctx, delay); err != nil {
			return providers.Response{}, err
		}
	}
	return providers.Response{}, lastErr
}

type StreamChunk struct {
	providers.Chunk
	Provider  string
	Fallbacks int
}

func (e *Engine) Stream(ctx context.Context, tenantID uuid.UUID, request providers.Request) (<-chan StreamChunk, <-chan error) {
	out := make(chan StreamChunk)
	errs := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errs)
		configs, err := e.source.ProviderConfigs(ctx, tenantID, request.Model)
		if err != nil {
			errs <- err
			return
		}
		if len(configs) == 0 {
			errs <- &providers.Error{Class: providers.ErrorUnavailable, Message: "no provider route is configured"}
			return
		}
		var lastErr error
		previous := ""
		for index, cfg := range configs {
			if previous != "" {
				e.observer.ProviderFallback(previous, cfg.Name)
			}
			s, err := e.getState(tenantID, cfg)
			if err != nil {
				errs <- err
				return
			}
			if !s.circuit.allow() {
				lastErr = &providers.Error{Class: providers.ErrorUnavailable, Provider: cfg.Name, Retryable: true, Message: "provider circuit is open"}
				previous = cfg.Name
				continue
			}
			if !acquire(s.semaphore) {
				lastErr = &providers.Error{Class: providers.ErrorUnavailable, Provider: cfg.Name, Retryable: true, Message: ErrSaturated.Error(), Cause: ErrSaturated}
				previous = cfg.Name
				continue
			}
			attemptCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
			chunks, providerErrs := s.provider.Stream(attemptCtx, request)
			first, firstErr, ok := firstStreamEvent(attemptCtx, chunks, providerErrs)
			if !ok {
				cancel()
				release(s.semaphore)
				lastErr = firstErr
				s.circuit.failure()
				previous = cfg.Name
				continue
			}
			out <- StreamChunk{Chunk: first, Provider: cfg.Name, Fallbacks: index}
			for chunk := range chunks {
				out <- StreamChunk{Chunk: chunk, Provider: cfg.Name, Fallbacks: index}
			}
			streamErr := <-providerErrs
			cancel()
			release(s.semaphore)
			if streamErr != nil {
				s.circuit.failure()
				errs <- streamErr
				return
			}
			s.circuit.success()
			return
		}
		errs <- lastErr
	}()
	return out, errs
}
func firstStreamEvent(ctx context.Context, chunks <-chan providers.Chunk, errs <-chan error) (providers.Chunk, error, bool) {
	select {
	case <-ctx.Done():
		return providers.Chunk{}, ctx.Err(), false
	case err, open := <-errs:
		if open && err != nil {
			return providers.Chunk{}, err, false
		}
		return providers.Chunk{}, &providers.Error{Class: providers.ErrorUnavailable, Retryable: true, Message: "provider stream ended before data"}, false
	case chunk, open := <-chunks:
		if !open {
			return providers.Chunk{}, &providers.Error{Class: providers.ErrorUnavailable, Retryable: true, Message: "provider stream ended before data"}, false
		}
		return chunk, nil, true
	}
}
func (e *Engine) getState(tenantID uuid.UUID, cfg storage.ProviderConfig) (*state, error) {
	key := tenantID.String() + ":" + cfg.Name
	e.mu.Lock()
	defer e.mu.Unlock()
	if existing := e.states[key]; existing != nil {
		return existing, nil
	}
	provider, err := e.factory(cfg)
	if err != nil {
		return nil, fmt.Errorf("create provider %s: %w", cfg.Name, err)
	}
	created := &state{provider: provider, semaphore: make(chan struct{}, cfg.MaxConcurrency), circuit: newCircuit(3, 15*time.Second)}
	e.states[key] = created
	return created, nil
}
func acquire(semaphore chan struct{}) bool {
	select {
	case semaphore <- struct{}{}:
		return true
	default:
		return false
	}
}
func release(semaphore chan struct{}) { <-semaphore }
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
