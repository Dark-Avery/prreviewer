package configs

import (
	"fmt"
	"os"
)

type Config struct {
	DatabaseURL string
	HTTPAddr    string
}

const defaultHTTPAddr = ":8080"

// Load reads configuration from environment variables.
// Required: DATABASE_URL, HTTP_ADDR (defaults to :8080 if empty).
func Load() (*Config, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = defaultHTTPAddr
	}
	return &Config{
		DatabaseURL: dbURL,
		HTTPAddr:    addr,
	}, nil
}
