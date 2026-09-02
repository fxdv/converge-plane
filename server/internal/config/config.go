// Package config loads and validates the process configuration from
// environment variables. Converge is configured exclusively via environment;
// there is no file-based configuration.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// devSessionSecret substitutes for CONVERGE_SESSION_SECRET in local
// development. Operators must set a real secret in any real deployment.
const devSessionSecret = "converge-dev-session-secret-do-not-use-in-prod"

// Config is the validated runtime configuration for the Converge API server.
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
	// SessionCookieName is the name of the session cookie.
	SessionCookieName string
	// SecureCookies forces Secure cookies (set true behind TLS in prod).
	SecureCookies bool
	// SessionSecret keys the HMAC signatures inside session tokens.
	// The dev fallback is substituted when unset.
	SessionSecret string
	// SessionTTL bounds the session audit-row lifetime.
	SessionTTL time.Duration
	// AccessTokenTTL bounds the client-visible access token lifetime; the
	// client refreshes as it approaches expiry.
	AccessTokenTTL time.Duration
	// RefreshTokenTTL bounds the refresh token lifetime (the effective
	// sign-in session length).
	RefreshTokenTTL time.Duration
	// CodeTTL bounds a magic-link code lifetime.
	CodeTTL time.Duration
	// DevMode enables local-development conveniences that must never run
	// in production: the create-code response then includes devMagicLink
	// so a deployment without an email provider is still sign-in-able.
	DevMode bool
	// HTTPTimeout bounds a single API request.
	HTTPTimeout time.Duration
	// DBMinConns and DBMaxConns bound the connection pool.
	DBMinConns int32
	DBMaxConns int32
	// RateLimitRPS is the sustained per-account request rate; 0 disables
	// the limiter entirely.
	RateLimitRPS float64
	// RateLimitBurst bounds a short per-account burst above the sustained
	// rate.
	RateLimitBurst int64
	// Version is the build version, injected at link time.
	Version string
}

// Load reads the configuration from environment variables.
//
// Recognized variables (all optional unless noted):
//
//	CONVERGE_HTTP_ADDR         listen address          (default ":3001")
//	CONVERGE_DATABASE_URL      postgres DSN            (required)
//	CONVERGE_PUBLIC_URL        public base URL         (default "http://localhost:3001")
//	CONVERGE_WEB_ORIGIN        web origin for CORS     (default "http://localhost:3000")
//	CONVERGE_LOG_LEVEL         debug|info|warn|error  (default "info")
//	CONVERGE_HTTP_TIMEOUT      request timeout, e.g. "30s" (default "30s")
//	CONVERGE_DB_MIN_CONNS      pool min conns          (default "1")
//	CONVERGE_DB_MAX_CONNS      pool max conns          (default "20")
//	CONVERGE_SECURE_COOKIES    force Secure cookies    (default "false")
//	CONVERGE_SESSION_SECRET    session HMAC key        (dev fallback when unset)
//	CONVERGE_ACCESS_TOKEN_TTL  access token lifetime   (default "1h")
//	CONVERGE_REFRESH_TOKEN_TTL refresh token lifetime  (default "720h")
//	CONVERGE_RATE_LIMIT_RPS    per-account rps, 0=off (default "10")
//	CONVERGE_RATE_LIMIT_BURST  per-account burst      (default "50")
//	CONVERGE_DEV_MODE          true = dev conveniences (magic link in API)
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:          env("CONVERGE_HTTP_ADDR", ":3001"),
		DatabaseURL:       os.Getenv("CONVERGE_DATABASE_URL"),
		PublicURL:         strings.TrimRight(env("CONVERGE_PUBLIC_URL", "http://localhost:3001"), "/"),
		WebOrigin:         strings.TrimRight(env("CONVERGE_WEB_ORIGIN", "http://localhost:3000"), "/"),
		LogLevel:          env("CONVERGE_LOG_LEVEL", "info"),
		HTTPTimeout:       30 * time.Second,
		DBMinConns:        1,
		DBMaxConns:        20,
		SessionCookieName: "sAccessToken",
		SecureCookies:     false,
		SessionTTL:        30 * 24 * time.Hour,
		AccessTokenTTL:    time.Hour,
		RefreshTokenTTL:   30 * 24 * time.Hour,
		CodeTTL:           15 * time.Minute,
		RateLimitRPS:      10,
		RateLimitBurst:    50,
	}

	if v := os.Getenv("CONVERGE_SECURE_COOKIES"); v == "true" {
		cfg.SecureCookies = true
	}
	if v := os.Getenv("CONVERGE_DEV_MODE"); v == "true" {
		cfg.DevMode = true
	}

	if v := os.Getenv("CONVERGE_SESSION_SECRET"); v != "" {
		cfg.SessionSecret = v
	}
	if v := os.Getenv("CONVERGE_ACCESS_TOKEN_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return cfg, fmt.Errorf("invalid CONVERGE_ACCESS_TOKEN_TTL %q: %w", v, err)
		}
		cfg.AccessTokenTTL = d
	}
	if v := os.Getenv("CONVERGE_REFRESH_TOKEN_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return cfg, fmt.Errorf("invalid CONVERGE_REFRESH_TOKEN_TTL %q: %w", v, err)
		}
		cfg.RefreshTokenTTL = d
	}
	cfg.SessionSecret = orDevSecret(cfg.SessionSecret)

	if cfg.DatabaseURL == "" {
		return cfg, fmt.Errorf("CONVERGE_DATABASE_URL is required")
	}

	if v := os.Getenv("CONVERGE_HTTP_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return cfg, fmt.Errorf("invalid CONVERGE_HTTP_TIMEOUT %q: %w", v, err)
		}
		cfg.HTTPTimeout = d
	}

	var err error
	if cfg.DBMinConns, err = int32FromEnv("CONVERGE_DB_MIN_CONNS", cfg.DBMinConns); err != nil {
		return cfg, err
	}
	if cfg.DBMaxConns, err = int32FromEnv("CONVERGE_DB_MAX_CONNS", cfg.DBMaxConns); err != nil {
		return cfg, err
	}
	if cfg.DBMinConns < 0 || cfg.DBMaxConns <= 0 || cfg.DBMinConns > cfg.DBMaxConns {
		return cfg, fmt.Errorf("invalid pool bounds: min=%d max=%d (require 0 <= min <= max, max > 0)", cfg.DBMinConns, cfg.DBMaxConns)
	}

	if v := os.Getenv("CONVERGE_RATE_LIMIT_RPS"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 {
			return cfg, fmt.Errorf("invalid CONVERGE_RATE_LIMIT_RPS %q: number >= 0 (0 disables)", v)
		}
		cfg.RateLimitRPS = f
	}
	if v := os.Getenv("CONVERGE_RATE_LIMIT_BURST"); v != "" {
		b, err := strconv.ParseInt(v, 10, 32)
		if err != nil || b < 0 {
			return cfg, fmt.Errorf("invalid CONVERGE_RATE_LIMIT_BURST %q: integer >= 0", v)
		}
		cfg.RateLimitBurst = int64(b)
	}

	switch strings.ToLower(cfg.LogLevel) {
	case "debug", "info", "warn", "error":
	default:
		return cfg, fmt.Errorf("invalid CONVERGE_LOG_LEVEL %q: expected debug, info, warn or error", cfg.LogLevel)
	}

	return cfg, nil
}

func orDevSecret(secret string) string {
	if secret == "" {
		return devSessionSecret
	}
	return secret
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
