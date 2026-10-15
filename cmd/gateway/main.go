package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/MarcoVanLill/ForgeGate/internal/app"
	"github.com/MarcoVanLill/ForgeGate/internal/config"
	"github.com/MarcoVanLill/ForgeGate/internal/gateway"
	"github.com/MarcoVanLill/ForgeGate/internal/jobs"
	"github.com/MarcoVanLill/ForgeGate/internal/ratelimit"
	"github.com/MarcoVanLill/ForgeGate/internal/routing"
	"github.com/MarcoVanLill/ForgeGate/internal/storage"
	"github.com/MarcoVanLill/ForgeGate/internal/telemetry"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		if err := migrate(); err != nil {
			slog.Error("migration failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		slog.Error("gateway stopped", "error", err)
		os.Exit(1)
	}
}

func migrate() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	store, err := storage.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	return store.Migrate(ctx)
}
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := app.Logger(cfg.LogLevel)
	slog.SetDefault(logger)
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdownTracing, err := telemetry.InitTracing(rootCtx, "forgegate-gateway", cfg.OTelEndpoint)
	if err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTracing(ctx)
	}()
	store, err := storage.Open(rootCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	if cfg.MigrateOnStart {
		if err := store.Migrate(rootCtx); err != nil {
			return err
		}
	}
	redisClient := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword})
	defer redisClient.Close()
	if err := redisotel.InstrumentTracing(redisClient); err != nil {
		return err
	}
	if err := redisClient.Ping(rootCtx).Err(); err != nil {
		return err
	}
	natsConn, err := nats.Connect(cfg.NATSURL, nats.Name("forgegate-gateway"), nats.MaxReconnects(-1), nats.ReconnectWait(time.Second))
	if err != nil {
		return err
	}
	defer natsConn.Close()
	js, err := natsConn.JetStream()
	if err != nil {
		return err
	}
	if err := jobs.EnsureStream(js); err != nil {
		return err
	}
	metrics := telemetry.NewMetrics()
	router := routing.New(store, app.ProviderFactory(cfg), metrics)
	publisher := jobs.NewPublisher(store, js, logger, cfg.OutboxPollInterval)
	go func() { _ = publisher.Run(rootCtx) }()
	api := gateway.New(gateway.Dependencies{Store: store, Router: router, Limiter: ratelimit.New(redisClient), Redis: redisClient, NATS: natsConn, Metrics: metrics, Logger: logger, RequestTimeout: cfg.RequestTimeout})
	server := &http.Server{Addr: cfg.HTTPAddr, Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 0, IdleTimeout: 60 * time.Second}
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("gateway listening", "address", cfg.HTTPAddr)
		serverErrors <- server.ListenAndServe()
	}()
	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-rootCtx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	return server.Shutdown(shutdownCtx)
}
