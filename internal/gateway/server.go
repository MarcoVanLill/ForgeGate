package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/MarcoVanLill/ForgeGate/internal/auth"
	"github.com/MarcoVanLill/ForgeGate/internal/httpx"
	"github.com/MarcoVanLill/ForgeGate/internal/identity"
	"github.com/MarcoVanLill/ForgeGate/internal/providers"
	"github.com/MarcoVanLill/ForgeGate/internal/ratelimit"
	"github.com/MarcoVanLill/ForgeGate/internal/routing"
	"github.com/MarcoVanLill/ForgeGate/internal/storage"
	"github.com/MarcoVanLill/ForgeGate/internal/telemetry"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type Server struct {
	store          *storage.Store
	router         *routing.Engine
	limiter        *ratelimit.Limiter
	redis          *redis.Client
	nats           *nats.Conn
	metrics        *telemetry.Metrics
	logger         *slog.Logger
	requestTimeout time.Duration
	handler        http.Handler
}
type Dependencies struct {
	Store          *storage.Store
	Router         *routing.Engine
	Limiter        *ratelimit.Limiter
	Redis          *redis.Client
	NATS           *nats.Conn
	Metrics        *telemetry.Metrics
	Logger         *slog.Logger
	RequestTimeout time.Duration
}

func New(deps Dependencies) *Server {
	s := &Server{store: deps.Store, router: deps.Router, limiter: deps.Limiter, redis: deps.Redis, nats: deps.NATS, metrics: deps.Metrics, logger: deps.Logger, requestTimeout: deps.RequestTimeout}
	s.handler = s.routes()
	return s
}
func (s *Server) Handler() http.Handler { return s.handler }
func (s *Server) routes() http.Handler {
	r := chi.NewRouter()
	r.Use(s.requestID, s.recoverer, s.observeHTTP)
	r.Get("/health/live", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "live"})
	})
	r.Get("/health/ready", s.ready)
	r.Handle("/metrics", s.metrics.Handler())
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(s.store), s.rateLimit)
		r.Post("/v1/responses", s.createResponse)
		r.Post("/v1/jobs", s.createJob)
		r.Get("/v1/jobs/{id}", s.getJob)
		r.Delete("/v1/jobs/{id}", s.cancelJob)
	})
	return otelhttp.NewHandler(r, "forgegate.http")
}
func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if id == "" || len(id) > 128 {
			id = identity.NewRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(identity.WithRequestID(r.Context(), id)))
	})
}
func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if value := recover(); value != nil {
				s.logger.Error("request panic", "request_id", identity.RequestID(r.Context()), "panic", value, "stack", string(debug.Stack()))
				httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *responseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
func (s *Server) observeHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		wrapped := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(wrapped, r)
		route := chi.RouteContext(r.Context()).RoutePattern()
		if route == "" {
			route = "unmatched"
		}
		s.metrics.ObserveHTTP(route, r.Method, wrapped.status, time.Since(started))
		s.logger.Info("request completed", "request_id", identity.RequestID(r.Context()), "method", r.Method, "route", route, "status", wrapped.status, "duration_ms", time.Since(started).Milliseconds())
	})
}
func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant, _ := auth.Tenant(r.Context())
		result, err := s.limiter.Allow(r.Context(), tenant.ID, tenant.RequestsPerMinute)
		if err != nil {
			s.logger.Error("rate limiter unavailable", "request_id", identity.RequestID(r.Context()), "error", err)
			httpx.Error(w, r, http.StatusServiceUnavailable, "rate_limiter_unavailable", "rate limiting is temporarily unavailable")
			return
		}
		w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(result.Remaining, 10))
		if !result.Allowed {
			s.metrics.RateLimited()
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(result.RetryAfter.Seconds()))))
			httpx.Error(w, r, http.StatusTooManyRequests, "rate_limit_exceeded", "tenant rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	checks := map[string]string{"postgres": "ok", "redis": "ok", "nats": "ok"}
	ready := true
	if err := s.store.Ping(ctx); err != nil {
		checks["postgres"] = err.Error()
		ready = false
	}
	if err := s.redis.Ping(ctx).Err(); err != nil {
		checks["redis"] = err.Error()
		ready = false
	}
	if !s.nats.IsConnected() {
		checks["nats"] = "not connected"
		ready = false
	}
	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	httpx.JSON(w, status, map[string]any{"status": map[bool]string{true: "ready", false: "not_ready"}[ready], "checks": checks})
}

type responseRequest struct {
	Model           string `json:"model"`
	Input           string `json:"input"`
	Stream          bool   `json:"stream"`
	MaxOutputTokens int    `json:"max_output_tokens,omitempty"`
}

func decodeResponseRequest(w http.ResponseWriter, r *http.Request) (responseRequest, bool) {
	var input responseRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "request body must be valid JSON")
		return responseRequest{}, false
	}
	input.Model = strings.TrimSpace(input.Model)
	input.Input = strings.TrimSpace(input.Input)
	if input.Model == "" || input.Input == "" || len(input.Model) > 200 || len(input.Input) > 200000 || input.MaxOutputTokens < 0 {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "model and input are required and limits must be valid")
		return responseRequest{}, false
	}
	return input, true
}
func (s *Server) createResponse(w http.ResponseWriter, r *http.Request) {
	input, ok := decodeResponseRequest(w, r)
	if !ok {
		return
	}
	tenant, _ := auth.Tenant(r.Context())
	if err := s.store.ReserveRequest(r.Context(), tenant, 0); err != nil {
		if errors.Is(err, storage.ErrBudgetExceeded) {
			httpx.Error(w, r, http.StatusPaymentRequired, "budget_exceeded", "tenant monthly budget exceeded")
			return
		}
		httpx.Error(w, r, http.StatusServiceUnavailable, "storage_unavailable", "unable to reserve tenant budget")
		return
	}
	request := providers.Request{Model: input.Model, Input: input.Input, MaxOutputTokens: input.MaxOutputTokens}
	if input.Stream {
		s.streamResponse(w, r, tenant, request)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.requestTimeout)
	defer cancel()
	record := storage.RequestRecord{ID: uuid.New(), TenantID: tenant.ID, RequestID: identity.RequestID(r.Context()), Model: input.Model, Input: input.Input, Outcome: "error", StatusCode: http.StatusBadGateway}
	started := time.Now()
	result, err := s.router.Complete(ctx, tenant.ID, request)
	record.Latency = time.Since(started)
	if err != nil {
		record.ErrorCode = "provider_error"
		s.record(record)
		httpx.Error(w, r, http.StatusBadGateway, "provider_error", "all configured providers failed")
		return
	}
	record.Provider = result.Provider
	record.Outcome = "success"
	record.StatusCode = http.StatusOK
	record.Usage = result.Response.Usage
	s.record(record)
	httpx.JSON(w, http.StatusOK, map[string]any{"id": record.ID, "object": "response", "status": "completed", "model": input.Model, "provider": result.Provider, "fallback_count": result.Fallbacks, "output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": result.Response.Text}}}}, "usage": result.Response.Usage})
}
func (s *Server) streamResponse(w http.ResponseWriter, r *http.Request, tenant storage.Tenant, request providers.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpx.Error(w, r, http.StatusInternalServerError, "streaming_unsupported", "streaming is not supported by this server")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ctx, cancel := context.WithTimeout(r.Context(), s.requestTimeout)
	defer cancel()
	record := storage.RequestRecord{ID: uuid.New(), TenantID: tenant.ID, RequestID: identity.RequestID(r.Context()), Model: request.Model, Input: request.Input, Outcome: "error", StatusCode: http.StatusBadGateway}
	started := time.Now()
	chunks, errs := s.router.Stream(ctx, tenant.ID, request)
	created := false
	for chunk := range chunks {
		if !created {
			record.Provider = chunk.Provider
			writeSSE(w, "response.created", map[string]any{"id": record.ID, "provider": chunk.Provider, "model": request.Model})
			created = true
		}
		if chunk.Text != "" {
			writeSSE(w, "response.output_text.delta", map[string]any{"id": record.ID, "delta": chunk.Text})
		}
		if chunk.Usage != nil {
			record.Usage = *chunk.Usage
		}
		if chunk.Done {
			record.Outcome = "success"
			record.StatusCode = http.StatusOK
			writeSSE(w, "response.completed", map[string]any{"id": record.ID, "usage": record.Usage})
		}
		flusher.Flush()
	}
	if err := <-errs; err != nil {
		record.ErrorCode = "provider_error"
		writeSSE(w, "error", map[string]any{"code": "provider_error", "message": "provider stream failed", "request_id": record.RequestID})
		flusher.Flush()
	}
	record.Latency = time.Since(started)
	s.record(record)
}
func writeSSE(w http.ResponseWriter, event string, value any) {
	payload, _ := json.Marshal(value)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
}
func (s *Server) record(record storage.RequestRecord) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.store.RecordRequest(ctx, record); err != nil {
		s.logger.Error("record request", "request_id", record.RequestID, "error", err)
	}
}
func (s *Server) createJob(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 200 {
		httpx.Error(w, r, http.StatusBadRequest, "idempotency_key_required", "a valid Idempotency-Key header is required")
		return
	}
	input, ok := decodeResponseRequest(w, r)
	if !ok {
		return
	}
	tenant, _ := auth.Tenant(r.Context())
	if err := s.store.ReserveRequest(r.Context(), tenant, 0); err != nil {
		if errors.Is(err, storage.ErrBudgetExceeded) {
			httpx.Error(w, r, http.StatusPaymentRequired, "budget_exceeded", "tenant monthly budget exceeded")
			return
		}
		httpx.Error(w, r, http.StatusServiceUnavailable, "storage_unavailable", "unable to reserve tenant budget")
		return
	}
	job, duplicate, err := s.store.CreateJob(r.Context(), tenant.ID, key, providers.Request{Model: input.Model, Input: input.Input, MaxOutputTokens: input.MaxOutputTokens})
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "job_creation_failed", "unable to create job")
		return
	}
	status := http.StatusAccepted
	if duplicate {
		status = http.StatusOK
	}
	httpx.JSON(w, status, jobView(job, duplicate))
}
func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	tenant, _ := auth.Tenant(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_job_id", "job ID must be a UUID")
		return
	}
	job, err := s.store.GetJob(r.Context(), tenant.ID, id)
	if errors.Is(err, storage.ErrNotFound) {
		httpx.Error(w, r, http.StatusNotFound, "job_not_found", "job was not found")
		return
	}
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "storage_error", "unable to load job")
		return
	}
	httpx.JSON(w, http.StatusOK, jobView(job, false))
}
func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	tenant, _ := auth.Tenant(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_job_id", "job ID must be a UUID")
		return
	}
	job, err := s.store.CancelJob(r.Context(), tenant.ID, id)
	if errors.Is(err, storage.ErrNotFound) {
		httpx.Error(w, r, http.StatusNotFound, "job_not_found", "job was not found")
		return
	}
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "storage_error", "unable to cancel job")
		return
	}
	httpx.JSON(w, http.StatusOK, jobView(job, false))
}
func jobView(job storage.Job, duplicate bool) map[string]any {
	return map[string]any{"id": job.ID, "status": job.Status, "selected_provider": job.SelectedProvider, "result": job.Result, "error_code": job.ErrorCode, "error_message": job.ErrorMessage, "attempts": job.Attempts, "max_attempts": job.MaxAttempts, "cancellation_requested": job.CancellationRequested, "created_at": job.CreatedAt, "updated_at": job.UpdatedAt, "started_at": job.StartedAt, "completed_at": job.CompletedAt, "duplicate": duplicate}
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
