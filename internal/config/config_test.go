package config

import (
	"testing"
	"time"
)

func TestLoadValidation(t *testing.T) {
	t.Setenv("FORGEGATE_REQUEST_TIMEOUT", "2s")
	t.Setenv("FORGEGATE_MIGRATE_ON_START", "false")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.RequestTimeout != 2*time.Second {
		t.Fatalf("RequestTimeout = %s", cfg.RequestTimeout)
	}
	if cfg.MigrateOnStart {
		t.Fatal("MigrateOnStart = true, want false")
	}
}

func TestLoadRejectsInvalidDuration(t *testing.T) {
	t.Setenv("FORGEGATE_REQUEST_TIMEOUT", "tomorrow")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want validation error")
	}
}
