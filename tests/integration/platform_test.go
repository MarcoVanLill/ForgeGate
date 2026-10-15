//go:build integration

package integration

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/MarcoVanLill/ForgeGate/internal/jobs"
	"github.com/MarcoVanLill/ForgeGate/internal/providers"
	"github.com/MarcoVanLill/ForgeGate/internal/ratelimit"
	"github.com/MarcoVanLill/ForgeGate/internal/storage"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestPostgresRedisNATSAndIdempotency(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	postgres := start(t, ctx, testcontainers.ContainerRequest{Image: "postgres:16-alpine", ExposedPorts: []string{"5432/tcp"}, Env: map[string]string{"POSTGRES_DB": "forgegate", "POSTGRES_USER": "forgegate", "POSTGRES_PASSWORD": "forgegate"}, WaitingFor: wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(90 * time.Second)})
	redisContainer := start(t, ctx, testcontainers.ContainerRequest{Image: "redis:7-alpine", ExposedPorts: []string{"6379/tcp"}, WaitingFor: wait.ForListeningPort("6379/tcp").WithStartupTimeout(60 * time.Second)})
	natsContainer := start(t, ctx, testcontainers.ContainerRequest{Image: "nats:2.10-alpine", ExposedPorts: []string{"4222/tcp"}, Cmd: []string{"-js", "-m", "8222"}, WaitingFor: wait.ForListeningPort("4222/tcp").WithStartupTimeout(60 * time.Second)})

	postgresURL := endpoint(t, ctx, postgres, "5432/tcp", func(host, port string) string {
		return fmt.Sprintf("postgres://forgegate:forgegate@%s:%s/forgegate?sslmode=disable", host, port)
	})
	redisAddress := endpoint(t, ctx, redisContainer, "6379/tcp", func(host, port string) string { return host + ":" + port })
	natsURL := endpoint(t, ctx, natsContainer, "4222/tcp", func(host, port string) string { return "nats://" + host + ":" + port })

	store, err := storage.Open(ctx, postgresURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	tenant, err := store.Authenticate(ctx, storage.HashAPIKey("fg_demo_local_only"))
	if err != nil {
		t.Fatalf("authenticate seeded key: %v", err)
	}

	redisClient := redis.NewClient(&redis.Options{Addr: redisAddress})
	defer redisClient.Close()
	limiter := ratelimit.New(redisClient)
	for i := 0; i < 2; i++ {
		result, err := limiter.Allow(ctx, tenant.ID, 2)
		if err != nil || !result.Allowed {
			t.Fatalf("rate-limit request %d: result=%#v err=%v", i, result, err)
		}
	}
	result, err := limiter.Allow(ctx, tenant.ID, 2)
	if err != nil || result.Allowed {
		t.Fatalf("expected rate-limit rejection: result=%#v err=%v", result, err)
	}

	connection, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	js, err := connection.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	if err := jobs.EnsureStream(js); err != nil {
		t.Fatal(err)
	}
	subscription, err := js.PullSubscribe(jobs.RunSubject, "integration-worker", nats.BindStream(jobs.StreamName), nats.ManualAck())
	if err != nil {
		t.Fatal(err)
	}
	request := providers.Request{Model: "fake/echo", Input: "integration"}
	created, duplicate, err := store.CreateJob(ctx, tenant, "same-key", request)
	if err != nil || duplicate {
		t.Fatalf("first CreateJob: duplicate=%v err=%v", duplicate, err)
	}
	again, duplicate, err := store.CreateJob(ctx, tenant, "same-key", request)
	if err != nil || !duplicate || again.ID != created.ID {
		t.Fatalf("duplicate CreateJob: job=%s duplicate=%v err=%v", again.ID, duplicate, err)
	}
	publishCtx, publishCancel := context.WithCancel(ctx)
	publisher := jobs.NewPublisher(store, js, slog.New(slog.NewTextHandler(os.Stderr, nil)), 10*time.Millisecond)
	go func() { _ = publisher.Run(publishCtx) }()
	messages, err := subscription.Fetch(1, nats.MaxWait(10*time.Second))
	publishCancel()
	if err != nil || len(messages) != 1 {
		t.Fatalf("fetch outbox message: count=%d err=%v", len(messages), err)
	}
	job, claimed, err := store.ClaimJob(ctx, created.ID)
	if err != nil || !claimed {
		t.Fatalf("ClaimJob: claimed=%v err=%v", claimed, err)
	}
	if err := store.CompleteJob(ctx, job.ID, "fake-primary", providers.Response{Text: "done"}); err != nil {
		t.Fatal(err)
	}
	_, claimed, err = store.ClaimJob(ctx, created.ID)
	if err != nil || claimed {
		t.Fatalf("duplicate ClaimJob: claimed=%v err=%v", claimed, err)
	}
	_ = messages[0].Ack()

	cancellable, _, err := store.CreateJob(ctx, tenant, "cancel-key", request)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := store.CancelJob(ctx, tenant.ID, cancellable.ID)
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("CancelJob: status=%s err=%v", cancelled.Status, err)
	}
}

func start(t *testing.T, ctx context.Context, request testcontainers.ContainerRequest) testcontainers.Container {
	t.Helper()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: request, Started: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = container.Terminate(cleanupCtx)
	})
	return container
}
func endpoint(t *testing.T, ctx context.Context, container testcontainers.Container, port string, format func(string, string) string) string {
	t.Helper()
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := container.MappedPort(ctx, port)
	if err != nil {
		t.Fatal(err)
	}
	return format(host, mapped.Port())
}
