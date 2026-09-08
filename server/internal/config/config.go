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
	// RuntimeEnabled turns the in-process agent runtime (D3) on or off.
	// On, agents with work act on it: the dispatcher wakes a worker per
	// busy agent, the policy acts, work returns through the handoff
	// protocol. Off, agents exist (M6) but wait for their own runtime.
	RuntimeEnabled bool
	// RuntimeTopology selects the handoff policy the runtime drives:
	// "foreman" (default; one lead agent, the fleet's oldest active
	// agent, dispatches and receives returns) or "flat" (any agent may
	// hand to the least-busy peer). Fleet-level experiment switch until
	// the D4 selector lands.
	RuntimeTopology string
	// RuntimeTick bounds the dispatcher's backstop scan: work the wake
	// fast path missed (a restart, a manual DB edit) is picked up within
	// one tick.
	RuntimeTick time.Duration
	// SwarmReviewInterval bounds the foreman review tick (cs:swarm:review):
	// the standing duty that resolves the swarm's parked queue — a human
	// reply since the last park resumes the swarm; a card that has parked
	// the cycle threshold times without a reply is escalated to the human
	// foreman as their work. Zero disables the tick (the work-cycle
	// circuit breaker still applies on every pause).
	SwarmReviewInterval time.Duration
	// LLMEnabled turns on the LLM decision policy (the D3+ brain): the
	// policy slot is populated by a self-hosted model fleet instead of
	// the deterministic policy. Default OFF: the deterministic policy
	// remains the shipped brain until a fleet is in place; even enabled,
	// every LLM failure falls back to the deterministic policy per
	// decision, so the swarm's cadence never depends on the fleet.
	LLMEnabled bool
	// LLMURLs are the OpenAI-compatible chat endpoints (comma-separated
	// in CONVERGE_LLM_URLS), one per GPU; workspaces hash-shard across
	// them (a workspace's work stays on one instance: warm weights and
	// KV cache).
	LLMURLs []string
	// LLMModel is the model name the endpoints serve (the server's
	// --alias).
	LLMModel string
	// LLMTimeout bounds one decision round-trip; on timeout the
	// deterministic fallback acts.
	LLMTimeout time.Duration
	// LLMMaxTokens bounds the model's output (thinking + answer); the
	// decision JSON must fit inside it.
	LLMMaxTokens int
	// SMTPHost is the SMTP server for transactional mail (workspace
	// invitations). Empty selects the log driver: the full message
	// (including its link) is written to the API log, so a deployment
	// without an email provider stays invite-able.
	SMTPHost string
	// SMTPPort is the SMTP port.
	SMTPPort int
	// SMTPUser and SMTPPass authenticate to the SMTP server when set.
	SMTPUser string
	SMTPPass string
	// SMTPFrom is the From: address on outgoing mail.
	SMTPFrom string
	// SMTPTLS selects the SMTP transport: starttls, off, implicit.
	SMTPTLS string
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
//	CONVERGE_RUNTIME           agent runtime, on by default ("false" disables)
//	CONVERGE_RUNTIME_TOPOLOGY   foreman|flat              (default "foreman")
//	CONVERGE_RUNTIME_TICK       dispatcher backstop tick   (default "5s")
//	CONVERGE_SWARM_REVIEW_INTERVAL
//	                           foreman review tick        (default "30m", "0" disables)
//	CONVERGE_LLM               LLM decision policy, off by default ("true" enables)
//	CONVERGE_LLM_URLS          OpenAI-compatible endpoints, comma-separated (one per GPU)
//	CONVERGE_LLM_MODEL         model name the endpoints serve (default "qwen3.8-27b")
//	CONVERGE_LLM_TIMEOUT       decision round-trip bound (default "60s")
//	CONVERGE_LLM_MAX_TOKENS    model output bound, thinking included (default "2048")
//	CONVERGE_SMTP_HOST         smtp host, "" = log driver (default "")
//	CONVERGE_SMTP_PORT         smtp port               (default "587")
//	CONVERGE_SMTP_USER         smtp auth username      (default "")
//	CONVERGE_SMTP_PASS         smtp auth password      (default "")
//	CONVERGE_SMTP_FROM         from address            (default "no-reply@converge.local")
//	CONVERGE_SMTP_TLS          starttls|off|implicit   (default "starttls")
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
		RuntimeEnabled:    true,
		SessionCookieName: "sAccessToken",
		SecureCookies:     false,
		SessionTTL:        30 * 24 * time.Hour,
		AccessTokenTTL:    time.Hour,
		RefreshTokenTTL:   30 * 24 * time.Hour,
		CodeTTL:           15 * time.Minute,
		RateLimitRPS:      10,
		RateLimitBurst:    50,
		SMTPPort:          587,
		SMTPTLS:           "starttls",
	}

	if v := os.Getenv("CONVERGE_SECURE_COOKIES"); v == "true" {
		cfg.SecureCookies = true
	}
	if v := os.Getenv("CONVERGE_DEV_MODE"); v == "true" {
		cfg.DevMode = true
	}
	switch strings.ToLower(os.Getenv("CONVERGE_RUNTIME")) {
	case "false", "0", "off":
		cfg.RuntimeEnabled = false
	case "true", "1", "on", "":
		cfg.RuntimeEnabled = true
	default:
		return cfg, fmt.Errorf("invalid CONVERGE_RUNTIME %q: expected true or false", os.Getenv("CONVERGE_RUNTIME"))
	}
	switch strings.ToLower(env("CONVERGE_RUNTIME_TOPOLOGY", "foreman")) {
	case "foreman", "flat":
		cfg.RuntimeTopology = strings.ToLower(env("CONVERGE_RUNTIME_TOPOLOGY", "foreman"))
	default:
		return cfg, fmt.Errorf("invalid CONVERGE_RUNTIME_TOPOLOGY %q: expected foreman or flat", os.Getenv("CONVERGE_RUNTIME_TOPOLOGY"))
	}
	cfg.RuntimeTick = 5 * time.Second
	if v := os.Getenv("CONVERGE_RUNTIME_TICK"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Second {
			return cfg, fmt.Errorf("invalid CONVERGE_RUNTIME_TICK %q: expected a duration >= 1s", v)
		}
		cfg.RuntimeTick = d
	}
	// "0" disables the standing review (the pause-side breaker remains).
	cfg.SwarmReviewInterval = 30 * time.Minute
	if v := os.Getenv("CONVERGE_SWARM_REVIEW_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 || (d > 0 && d < time.Second) {
			return cfg, fmt.Errorf("invalid CONVERGE_SWARM_REVIEW_INTERVAL %q: expected a duration, 0 disables", v)
		}
		cfg.SwarmReviewInterval = d
	}
	switch strings.ToLower(os.Getenv("CONVERGE_LLM")) {
	case "true", "1", "on":
		cfg.LLMEnabled = true
	case "false", "0", "off", "":
		cfg.LLMEnabled = false
	default:
		return cfg, fmt.Errorf("invalid CONVERGE_LLM %q: expected true or false", os.Getenv("CONVERGE_LLM"))
	}
	cfg.LLMModel = env("CONVERGE_LLM_MODEL", "qwen3.8-27b")
	if v := os.Getenv("CONVERGE_LLM_URLS"); v != "" {
		for _, u := range strings.Split(v, ",") {
			if u = strings.TrimSpace(u); u != "" {
				cfg.LLMURLs = append(cfg.LLMURLs, u)
			}
		}
	}
	cfg.LLMTimeout = 60 * time.Second
	if v := os.Getenv("CONVERGE_LLM_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Second {
			return cfg, fmt.Errorf("invalid CONVERGE_LLM_TIMEOUT %q: expected a duration >= 1s", v)
		}
		cfg.LLMTimeout = d
	}
	cfg.LLMMaxTokens = 2048
	if v := os.Getenv("CONVERGE_LLM_MAX_TOKENS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return cfg, fmt.Errorf("invalid CONVERGE_LLM_MAX_TOKENS %q: expected an integer >= 1", v)
		}
		cfg.LLMMaxTokens = n
	}
	cfg.SMTPHost = os.Getenv("CONVERGE_SMTP_HOST")
	cfg.SMTPUser = os.Getenv("CONVERGE_SMTP_USER")
	cfg.SMTPPass = os.Getenv("CONVERGE_SMTP_PASS")
	cfg.SMTPFrom = os.Getenv("CONVERGE_SMTP_FROM")
	if v := os.Getenv("CONVERGE_SMTP_PORT"); v != "" {
		port, err := strconv.Atoi(v)
		if err != nil || port < 1 || port > 65535 {
			return cfg, fmt.Errorf("invalid CONVERGE_SMTP_PORT %q: expected a port in 1-65535", v)
		}
		cfg.SMTPPort = port
	}
	if v := os.Getenv("CONVERGE_SMTP_TLS"); v != "" {
		switch strings.ToLower(v) {
		case "starttls", "off", "implicit":
			cfg.SMTPTLS = strings.ToLower(v)
		default:
			return cfg, fmt.Errorf("invalid CONVERGE_SMTP_TLS %q: expected starttls, off or implicit", v)
		}
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
