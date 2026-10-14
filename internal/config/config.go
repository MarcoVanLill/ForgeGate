package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Environment        string
	LogLevel           string
	HTTPAddr           string
	DatabaseURL        string
	RedisAddr          string
	RedisPassword      string
	NATSURL            string
	OTelEndpoint       string
	MigrateOnStart     bool
	ShutdownTimeout    time.Duration
	RequestTimeout     time.Duration
	OutboxPollInterval time.Duration
	OpenAIBaseURL      string
	OpenAIAPIKey       string
	GeminiAPIKey       string
}

func Load() (Config, error) {
	c := Config{
		Environment: env("FORGEGATE_ENV", "development"), LogLevel: env("FORGEGATE_LOG_LEVEL", "info"),
		HTTPAddr:    env("FORGEGATE_HTTP_ADDR", ":8080"),
		DatabaseURL: env("FORGEGATE_DATABASE_URL", "postgres://forgegate:forgegate@localhost:5432/forgegate?sslmode=disable"),
		RedisAddr:   env("FORGEGATE_REDIS_ADDR", "localhost:6379"), RedisPassword: os.Getenv("FORGEGATE_REDIS_PASSWORD"),
		NATSURL: env("FORGEGATE_NATS_URL", "nats://localhost:4222"), OTelEndpoint: os.Getenv("FORGEGATE_OTEL_ENDPOINT"),
		OpenAIBaseURL: env("OPENAI_COMPATIBLE_BASE_URL", "https://api.openai.com"), OpenAIAPIKey: os.Getenv("OPENAI_COMPATIBLE_API_KEY"), GeminiAPIKey: os.Getenv("GEMINI_API_KEY"),
	}
	var err error
	if c.MigrateOnStart, err = boolEnv("FORGEGATE_MIGRATE_ON_START", true); err != nil {
		return Config{}, err
	}
	if c.ShutdownTimeout, err = durationEnv("FORGEGATE_SHUTDOWN_TIMEOUT", 20*time.Second); err != nil {
		return Config{}, err
	}
	if c.RequestTimeout, err = durationEnv("FORGEGATE_REQUEST_TIMEOUT", 30*time.Second); err != nil {
		return Config{}, err
	}
	if c.OutboxPollInterval, err = durationEnv("FORGEGATE_OUTBOX_POLL_INTERVAL", 500*time.Millisecond); err != nil {
		return Config{}, err
	}
	if strings.TrimSpace(c.DatabaseURL) == "" || strings.TrimSpace(c.RedisAddr) == "" || strings.TrimSpace(c.NATSURL) == "" {
		return Config{}, errors.New("database, Redis, and NATS endpoints must be configured")
	}
	if c.RequestTimeout <= 0 || c.ShutdownTimeout <= 0 || c.OutboxPollInterval <= 0 {
		return Config{}, errors.New("timeouts and poll intervals must be positive")
	}
	return c, nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func boolEnv(key string, fallback bool) (bool, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}
func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}
