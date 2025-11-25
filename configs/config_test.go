package configs

import (
	"os"
	"testing"
)

func TestLoadSuccess(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("HTTP_ADDR", ":9000")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.DatabaseURL != "postgres://example" || cfg.HTTPAddr != ":9000" {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
}

func TestLoadDefaultAddr(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	_ = os.Unsetenv("HTTP_ADDR")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.HTTPAddr != defaultHTTPAddr {
		t.Fatalf("expected %s, got %s", defaultHTTPAddr, cfg.HTTPAddr)
	}
}

func TestLoadMissingDBURL(t *testing.T) {
	_ = os.Unsetenv("DATABASE_URL")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when DATABASE_URL missing")
	}
}
