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
	"github.com/MarcoVanLill/ForgeGate/internal/jobs"
	"github.com/MarcoVanLill/ForgeGate/internal/routing"
	"github.com/MarcoVanLill/ForgeGate/internal/storage"
	"github.com/MarcoVanLill/ForgeGate/internal/telemetry"
	"github.com/nats-io/nats.go"
)

func main() {
	if err := run(); err != nil {
		slog.Error("worker stopped", "error", err)
		os.Exit(1)
	}
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
	shutdownTracing, err := telemetry.InitTracing(rootCtx, "forgegate-worker", cfg.OTelEndpoint)
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
	natsConn, err := nats.Connect(cfg.NATSURL, nats.Name("forgegate-worker"), nats.MaxReconnects(-1), nats.ReconnectWait(time.Second))
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
	worker := jobs.NewWorker(store, router, js, metrics, logger, cfg.RequestTimeout)
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", metrics.Handler())
	metricsMux.HandleFunc("/health/live", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	metricsServer := &http.Server{Addr: cfg.WorkerMetricsAddr, Handler: metricsMux, ReadHeaderTimeout: 5 * time.Second}
	errorsCh := make(chan error, 2)
	go func() { errorsCh <- worker.Run(rootCtx) }()
	go func() {
		logger.Info("worker metrics listening", "address", cfg.WorkerMetricsAddr)
		errorsCh <- metricsServer.ListenAndServe()
	}()
	select {
	case err := <-errorsCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-rootCtx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	return metricsServer.Shutdown(shutdownCtx)
}
