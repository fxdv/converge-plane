// Package config loads and validates the process configuration from
// environment variables. Circle is configured exclusively via environment;
// there is no file-based configuration.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the validated runtime configuration for the Circle API server.
type Config struct {
	// HTTPAddr is the address the API server listens on.
	HTTPAddr string
	// DatabaseURL is the PostgreSQL connection string. Required.
	DatabaseURL string
	// PublicURL is the externally reachable base URL of the deployment.
	PublicURL string
	// WebOrigin is the origin of the web client, used for CORS in
	// cross-origin self-hosted layouts.
	WebOrigin string
	// LogLevel selects the log level: debug, info, warn, error.
	LogLevel string
	// HTTPTimeout bounds a single API request.
	HTTPTimeout time.Duration
	// DBMinConns and DBMaxConns bound the connection pool.
	DBMinConns int32
	DBMaxConns int32
	// Version is the build version, injected at link time.
	Version string
}

// Load reads the configuration from environment variables.
//
// Recognized variables (all optional unless noted):
//
//	CIRCLE_HTTP_ADDR       listen address          (default ":3001")
//	CIRCLE_DATABASE_URL    postgres DSN            (required)
//	CIRCLE_PUBLIC_URL      public base URL         (default "http://localhost:3001")
//	CIRCLE_WEB_ORIGIN      web origin for CORS     (default "http://localhost:3000")
//	CIRCLE_LOG_LEVEL       debug|info|warn|error  (default "info")
//	CIRCLE_HTTP_TIMEOUT    request timeout, e.g. "30s" (default "30s")
//	CIRCLE_DB_MIN_CONNS    pool min conns          (default "1")
//	CIRCLE_DB_MAX_CONNS    pool max conns          (default "20")
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:    env("CIRCLE_HTTP_ADDR", ":3001"),
		DatabaseURL: os.Getenv("CIRCLE_DATABASE_URL"),
		PublicURL:   strings.TrimRight(env("CIRCLE_PUBLIC_URL", "http://localhost:3001"), "/"),
		WebOrigin:   strings.TrimRight(env("CIRCLE_WEB_ORIGIN", "http://localhost:3000"), "/"),
		LogLevel:    env("CIRCLE_LOG_LEVEL", "info"),
		HTTPTimeout: 30 * time.Second,
		DBMinConns:  1,
		DBMaxConns:  20,
	}

	if cfg.DatabaseURL == "" {
		return cfg, fmt.Errorf("CIRCLE_DATABASE_URL is required")
	}

	if v := os.Getenv("CIRCLE_HTTP_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return cfg, fmt.Errorf("invalid CIRCLE_HTTP_TIMEOUT %q: %w", v, err)
		}
		cfg.HTTPTimeout = d
	}

	var err error
	if cfg.DBMinConns, err = int32FromEnv("CIRCLE_DB_MIN_CONNS", cfg.DBMinConns); err != nil {
		return cfg, err
	}
	if cfg.DBMaxConns, err = int32FromEnv("CIRCLE_DB_MAX_CONNS", cfg.DBMaxConns); err != nil {
		return cfg, err
	}
	if cfg.DBMinConns < 0 || cfg.DBMaxConns <= 0 || cfg.DBMinConns > cfg.DBMaxConns {
		return cfg, fmt.Errorf("invalid pool bounds: min=%d max=%d (require 0 <= min <= max, max > 0)", cfg.DBMinConns, cfg.DBMaxConns)
	}

	switch strings.ToLower(cfg.LogLevel) {
	case "debug", "info", "warn", "error":
	default:
		return cfg, fmt.Errorf("invalid CIRCLE_LOG_LEVEL %q: expected debug, info, warn or error", cfg.LogLevel)
	}

	return cfg, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func int32FromEnv(key string, fallback int32) (int32, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", key, v, err)
	}
	return int32(n), nil
}
