package main

import (
	"flag"
	"testing"

	"github.com/OneLastStop529/taskforge/pkg/taskforge"
)

func TestCLIConfig_DefaultsToRedisBackends(t *testing.T) {
	t.Setenv("TASKFORGE_REDIS_ADDR", "redis.internal:6380")
	t.Setenv("TASKFORGE_REDIS_USERNAME", "worker")
	t.Setenv("TASKFORGE_REDIS_PASSWORD", "secret")
	t.Setenv("TASKFORGE_REDIS_DB", "7")

	cfg := cliConfig()

	if cfg.BrokerBackend != taskforge.BackendRedis {
		t.Fatalf("expected redis broker backend, got %q", cfg.BrokerBackend)
	}
	if cfg.ResultBackend != taskforge.BackendRedis {
		t.Fatalf("expected redis result backend, got %q", cfg.ResultBackend)
	}
	if cfg.DLQBackend != taskforge.BackendRedis {
		t.Fatalf("expected redis dlq backend, got %q", cfg.DLQBackend)
	}
	if cfg.IdempotencyBackend != taskforge.BackendRedis {
		t.Fatalf("expected redis idempotency backend, got %q", cfg.IdempotencyBackend)
	}
	if cfg.Redis.Addr != "redis.internal:6380" {
		t.Fatalf("expected redis addr override, got %q", cfg.Redis.Addr)
	}
	if cfg.Redis.Username != "worker" {
		t.Fatalf("expected redis username override, got %q", cfg.Redis.Username)
	}
	if cfg.Redis.Password != "secret" {
		t.Fatalf("expected redis password override, got %q", cfg.Redis.Password)
	}
	if cfg.Redis.DB != 7 {
		t.Fatalf("expected redis db override, got %d", cfg.Redis.DB)
	}
}

func TestBindBackendFlags_DefaultsToRedis(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	flags := bindBackendFlags(fs, true)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("Parse: %v", err)
	}

	cfg := flags.apply(taskforge.DefaultConfig())

	if cfg.BrokerBackend != taskforge.BackendRedis {
		t.Fatalf("expected redis broker backend, got %q", cfg.BrokerBackend)
	}
	if cfg.ResultBackend != taskforge.BackendRedis {
		t.Fatalf("expected redis result backend, got %q", cfg.ResultBackend)
	}
	if cfg.DLQBackend != "" {
		t.Fatalf("expected DLQ backend flag default to inherit result backend, got %q", cfg.DLQBackend)
	}
	if cfg.IdempotencyBackend != "" {
		t.Fatalf("expected idempotency backend flag default to inherit result backend, got %q", cfg.IdempotencyBackend)
	}
}

func TestBindBackendFlags_AllowMemoryOverride(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	flags := bindBackendFlags(fs, true)
	if err := fs.Parse([]string{
		"-broker-backend", "memory",
		"-result-backend", "memory",
		"-dlq-backend", "memory",
		"-idempotency-backend", "memory",
		"-redis-addr", "127.0.0.1:6390",
		"-redis-db", "3",
	}); err != nil {
		t.Fatalf("Parse: %v", err)
	}

	cfg := flags.apply(cliConfig())

	if cfg.BrokerBackend != taskforge.BackendMemory {
		t.Fatalf("expected memory broker backend, got %q", cfg.BrokerBackend)
	}
	if cfg.ResultBackend != taskforge.BackendMemory {
		t.Fatalf("expected memory result backend, got %q", cfg.ResultBackend)
	}
	if cfg.DLQBackend != taskforge.BackendMemory {
		t.Fatalf("expected memory dlq backend, got %q", cfg.DLQBackend)
	}
	if cfg.IdempotencyBackend != taskforge.BackendMemory {
		t.Fatalf("expected memory idempotency backend, got %q", cfg.IdempotencyBackend)
	}
	if cfg.Redis.Addr != "127.0.0.1:6390" {
		t.Fatalf("expected redis addr override, got %q", cfg.Redis.Addr)
	}
	if cfg.Redis.DB != 3 {
		t.Fatalf("expected redis db override, got %d", cfg.Redis.DB)
	}
}
