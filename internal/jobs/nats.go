package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/MarcoVanLill/ForgeGate/internal/providers"
	"github.com/MarcoVanLill/ForgeGate/internal/routing"
	"github.com/MarcoVanLill/ForgeGate/internal/storage"
	"github.com/MarcoVanLill/ForgeGate/internal/telemetry"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
)

const (
	StreamName   = "FORGEGATE_JOBS"
	RunSubject   = "forgegate.jobs.run"
	DLQSubject   = "forgegate.jobs.dlq"
	ConsumerName = "forgegate-worker"
)

func EnsureStream(js nats.JetStreamContext) error {
	if _, err := js.StreamInfo(StreamName); err == nil {
		return nil
	} else if !errors.Is(err, nats.ErrStreamNotFound) {
		return fmt.Errorf("inspect job stream: %w", err)
	}
	_, err := js.AddStream(&nats.StreamConfig{Name: StreamName, Subjects: []string{RunSubject, DLQSubject}, Retention: nats.LimitsPolicy, Storage: nats.FileStorage, Replicas: 1, MaxAge: 7 * 24 * time.Hour, Discard: nats.DiscardOld})
	if err != nil {
		return fmt.Errorf("create job stream: %w", err)
	}
	return nil
}

type outboxStore interface {
	ClaimOutbox(context.Context, int) ([]storage.OutboxEvent, error)
	MarkOutboxPublished(context.Context, uuid.UUID) error
	MarkOutboxFailed(context.Context, uuid.UUID, string, int) error
}
type Publisher struct {
	store    outboxStore
	js       nats.JetStreamContext
	logger   *slog.Logger
	interval time.Duration
}

func NewPublisher(store outboxStore, js nats.JetStreamContext, logger *slog.Logger, interval time.Duration) *Publisher {
	return &Publisher{store: store, js: js, logger: logger, interval: interval}
}
func (p *Publisher) Run(ctx context.Context) error {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		if err := p.flush(ctx); err != nil && ctx.Err() == nil {
			p.logger.Error("outbox flush failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
func (p *Publisher) flush(ctx context.Context) error {
	events, err := p.store.ClaimOutbox(ctx, 50)
	if err != nil {
		return err
	}
	for _, event := range events {
		msg := nats.NewMsg(RunSubject)
		msg.Data = event.Payload
		msg.Header.Set(nats.MsgIdHdr, event.ID.String())
		otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(msg.Header))
		if _, err := p.js.PublishMsg(msg, nats.Context(ctx)); err != nil {
			_ = p.store.MarkOutboxFailed(ctx, event.ID, err.Error(), event.Attempts)
			continue
		}
		if err := p.store.MarkOutboxPublished(ctx, event.ID); err != nil {
			return fmt.Errorf("mark outbox published: %w", err)
		}
	}
	return nil
}

type workerStore interface {
	ClaimJob(context.Context, uuid.UUID) (storage.Job, bool, error)
	CompleteJob(context.Context, uuid.UUID, string, providers.Response) error
	FailJob(context.Context, uuid.UUID, string, string, bool) error
	RecordRequest(context.Context, storage.RequestRecord) error
}
type Worker struct {
	store   workerStore
	router  *routing.Engine
	js      nats.JetStreamContext
	metrics *telemetry.Metrics
	logger  *slog.Logger
	timeout time.Duration
}

func NewWorker(store workerStore, router *routing.Engine, js nats.JetStreamContext, metrics *telemetry.Metrics, logger *slog.Logger, timeout time.Duration) *Worker {
	return &Worker{store: store, router: router, js: js, metrics: metrics, logger: logger, timeout: timeout}
}
func (w *Worker) Run(ctx context.Context) error {
	sub, err := w.js.PullSubscribe(RunSubject, ConsumerName, nats.BindStream(StreamName), nats.ManualAck(), nats.AckExplicit(), nats.MaxDeliver(20), nats.AckWait(45*time.Second))
	if err != nil {
		return fmt.Errorf("create durable job consumer: %w", err)
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		messages, err := sub.Fetch(8, nats.MaxWait(time.Second))
		if errors.Is(err, nats.ErrTimeout) {
			w.observeQueue(sub)
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("fetch jobs: %w", err)
		}
		for _, message := range messages {
			w.process(ctx, message)
		}
		w.observeQueue(sub)
	}
}
func (w *Worker) observeQueue(sub *nats.Subscription) {
	if info, err := sub.ConsumerInfo(); err == nil {
		w.metrics.QueueDepth(float64(info.NumPending + uint64(info.NumAckPending)))
	}
}
func (w *Worker) process(parent context.Context, message *nats.Msg) {
	started := time.Now()
	carrier := propagation.HeaderCarrier(message.Header)
	parent = otel.GetTextMapPropagator().Extract(parent, carrier)
	ctx, span := otel.Tracer("forgegate/jobs").Start(parent, "job.process")
	defer span.End()
	var envelope struct {
		JobID uuid.UUID `json:"job_id"`
	}
	if err := json.Unmarshal(message.Data, &envelope); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "invalid message")
		_ = w.publishDLQ(ctx, message.Data, "invalid_message", err.Error())
		_ = message.Ack()
		w.metrics.Job("dead_letter", time.Since(started))
		return
	}
	span.SetAttributes(attribute.String("job.id", envelope.JobID.String()))
	job, claimed, err := w.store.ClaimJob(ctx, envelope.JobID)
	if errors.Is(err, storage.ErrNotFound) {
		_ = w.publishDLQ(ctx, message.Data, "job_not_found", err.Error())
		_ = message.Ack()
		w.metrics.Job("dead_letter", time.Since(started))
		return
	}
	if err != nil {
		_ = message.NakWithDelay(time.Second)
		return
	}
	if !claimed {
		switch job.Status {
		case "completed", "failed", "cancelled":
			_ = message.Ack()
			w.metrics.Job("duplicate", time.Since(started))
		default:
			_ = message.NakWithDelay(2 * time.Second)
		}
		return
	}
	requestCtx, cancel := context.WithTimeout(ctx, w.timeout)
	result, routeErr := w.router.Complete(requestCtx, job.TenantID, job.Request)
	cancel()
	record := storage.RequestRecord{ID: uuid.New(), TenantID: job.TenantID, RequestID: "job:" + job.ID.String(), Model: job.Request.Model, Input: job.Request.Input, Outcome: "error", StatusCode: 502, Latency: time.Since(started)}
	if routeErr == nil {
		record.Provider = result.Provider
		record.Outcome = "success"
		record.StatusCode = 200
		record.Usage = result.Response.Usage
		if err := w.store.CompleteJob(ctx, job.ID, result.Provider, result.Response); err != nil {
			w.logger.Error("complete job", "job_id", job.ID, "error", err)
			_ = message.NakWithDelay(time.Second)
			return
		}
		_ = w.store.RecordRequest(ctx, record)
		_ = message.Ack()
		w.metrics.Job("completed", time.Since(started))
		return
	}
	providerErr, retryable := providers.AsError(routeErr)
	retry := retryable && providerErr.Retryable && job.Attempts < job.MaxAttempts
	code := "provider_error"
	if retryable {
		code = string(providerErr.Class)
	}
	if err := w.store.FailJob(ctx, job.ID, code, routeErr.Error(), retry); err != nil {
		_ = message.NakWithDelay(time.Second)
		return
	}
	record.ErrorCode = code
	_ = w.store.RecordRequest(ctx, record)
	if retry {
		delay := time.Duration(1<<min(job.Attempts, 6)) * time.Second
		_ = message.NakWithDelay(delay)
		w.metrics.Job("retry", time.Since(started))
		return
	}
	_ = w.publishDLQ(ctx, message.Data, code, routeErr.Error())
	_ = message.Ack()
	w.metrics.Job("dead_letter", time.Since(started))
}
func (w *Worker) publishDLQ(ctx context.Context, payload []byte, code, message string) error {
	msg := nats.NewMsg(DLQSubject)
	msg.Data = payload
	msg.Header.Set("ForgeGate-Error-Code", code)
	msg.Header.Set("ForgeGate-Error", truncate(message, 500))
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(msg.Header))
	_, err := w.js.PublishMsg(msg, nats.Context(ctx))
	return err
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
