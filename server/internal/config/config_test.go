package config

import (
	"testing"
	"time"
)

// allKeys is every variable Load reads; tests blank it first so the
// ambient environment (CI, a dogfood shell with CONVERGE_* exported) can
// never leak into a case.
var allKeys = []string{
	"CONVERGE_HTTP_ADDR", "CONVERGE_DATABASE_URL", "CONVERGE_PUBLIC_URL",
	"CONVERGE_WEB_ORIGIN", "CONVERGE_LOG_LEVEL", "CONVERGE_HTTP_TIMEOUT",
	"CONVERGE_DB_MIN_CONNS", "CONVERGE_DB_MAX_CONNS", "CONVERGE_SECURE_COOKIES",
	"CONVERGE_SESSION_SECRET", "CONVERGE_ACCESS_TOKEN_TTL",
	"CONVERGE_REFRESH_TOKEN_TTL", "CONVERGE_RATE_LIMIT_RPS",
	"CONVERGE_RATE_LIMIT_BURST", "CONVERGE_DEV_MODE", "CONVERGE_RUNTIME",
	"CONVERGE_RUNTIME_TOPOLOGY", "CONVERGE_RUNTIME_TICK",
	"CONVERGE_SWARM_REVIEW_INTERVAL", "CONVERGE_LLM", "CONVERGE_LLM_URLS",
	"CONVERGE_LLM_MODEL", "CONVERGE_LLM_TIMEOUT", "CONVERGE_LLM_MAX_TOKENS",
	"CONVERGE_SMTP_HOST", "CONVERGE_SMTP_PORT", "CONVERGE_SMTP_USER",
	"CONVERGE_SMTP_PASS", "CONVERGE_SMTP_FROM", "CONVERGE_SMTP_TLS",
}

// loadWith blanks every recognized variable, applies vars, and loads.
func loadWith(t *testing.T, vars map[string]string) (Config, error) {
	t.Helper()
	for _, k := range allKeys {
		t.Setenv(k, "")
	}
	for k, v := range vars {
		t.Setenv(k, v)
	}
	return Load()
}

func loadOK(t *testing.T, vars map[string]string) Config {
	t.Helper()
	cfg, err := loadWith(t, vars)
	if err != nil {
		t.Fatalf("Load with %v: %v", vars, err)
	}
	return cfg
}

func loadErr(t *testing.T, vars map[string]string, wantSub string) {
	t.Helper()
	_, err := loadWith(t, vars)
	if err == nil {
		t.Fatalf("Load with %v succeeded, want error containing %q", vars, wantSub)
	}
	if !contains(err.Error(), wantSub) {
		t.Fatalf("error %q does not contain %q", err, wantSub)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// The minimal deployment: only the required DSN. Everything else must
// take its documented default.
func TestLoadDefaults(t *testing.T) {
	cfg := loadOK(t, map[string]string{"CONVERGE_DATABASE_URL": "postgresql://x"})
	cases := []struct {
		got  any
		want any
		what string
	}{
		{cfg.HTTPAddr, ":3001", "HTTPAddr"},
		{cfg.PublicURL, "http://localhost:3001", "PublicURL"},
		{cfg.WebOrigin, "http://localhost:3000", "WebOrigin"},
		{cfg.LogLevel, "info", "LogLevel"},
		{cfg.SessionCookieName, "sAccessToken", "SessionCookieName"},
		{cfg.SessionSecret, devSessionSecret, "SessionSecret dev fallback"},
		{cfg.AccessTokenTTL, time.Hour, "AccessTokenTTL"},
		{cfg.RefreshTokenTTL, 30 * 24 * time.Hour, "RefreshTokenTTL"},
		{cfg.CodeTTL, 15 * time.Minute, "CodeTTL"},
		{cfg.HTTPTimeout, 30 * time.Second, "HTTPTimeout"},
		{cfg.DBMinConns, int32(1), "DBMinConns"},
		{cfg.DBMaxConns, int32(20), "DBMaxConns"},
		{cfg.RuntimeEnabled, true, "RuntimeEnabled"},
		{cfg.RuntimeTopology, "foreman", "RuntimeTopology"},
		{cfg.RuntimeTick, 5 * time.Second, "RuntimeTick"},
		{cfg.SwarmReviewInterval, 30 * time.Minute, "SwarmReviewInterval"},
		{cfg.LLMEnabled, false, "LLMEnabled (off by default)"},
		{cfg.LLMModel, "qwen3.8-27b", "LLMModel"},
		{cfg.LLMTimeout, 60 * time.Second, "LLMTimeout"},
		{cfg.LLMMaxTokens, 2048, "LLMMaxTokens"},
		{cfg.RateLimitRPS, float64(10), "RateLimitRPS"},
		{cfg.RateLimitBurst, int64(50), "RateLimitBurst"},
		{cfg.SMTPPort, 587, "SMTPPort"},
		{cfg.SMTPTLS, "starttls", "SMTPTLS"},
		{cfg.SMTPFrom, "no-reply@converge.local", "SMTPFrom"},
		{cfg.SecureCookies, false, "SecureCookies"},
		{cfg.DevMode, false, "DevMode"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.what, c.got, c.want)
		}
	}
	if len(cfg.LLMURLs) != 0 {
		t.Errorf("LLMURLs = %v, want empty", cfg.LLMURLs)
	}
}

func TestLoadRequiredDatabaseURL(t *testing.T) {
	loadErr(t, map[string]string{}, "CONVERGE_DATABASE_URL is required")
}

func TestLoadTrailingSlashTrimmed(t *testing.T) {
	cfg := loadOK(t, map[string]string{
		"CONVERGE_DATABASE_URL": "postgresql://x",
		"CONVERGE_PUBLIC_URL":   "https://acme.example/",
		"CONVERGE_WEB_ORIGIN":   "https://acme.example/web/",
	})
	if cfg.PublicURL != "https://acme.example" || cfg.WebOrigin != "https://acme.example/web" {
		t.Fatalf("trim: public=%q web=%q", cfg.PublicURL, cfg.WebOrigin)
	}
}

func TestLoadRuntimeSwitches(t *testing.T) {
	for _, off := range []string{"false", "0", "off"} {
		cfg := loadOK(t, map[string]string{"CONVERGE_DATABASE_URL": "x", "CONVERGE_RUNTIME": off})
		if cfg.RuntimeEnabled {
			t.Fatalf("RUNTIME=%q did not disable the runtime", off)
		}
	}
	for _, on := range []string{"true", "1", "on"} {
		cfg := loadOK(t, map[string]string{"CONVERGE_DATABASE_URL": "x", "CONVERGE_RUNTIME": on})
		if !cfg.RuntimeEnabled {
			t.Fatalf("RUNTIME=%q did not enable the runtime", on)
		}
	}
	loadErr(t, map[string]string{"CONVERGE_DATABASE_URL": "x", "CONVERGE_RUNTIME": "maybe"}, "invalid CONVERGE_RUNTIME")
}

func TestLoadTopology(t *testing.T) {
	cfg := loadOK(t, map[string]string{"CONVERGE_DATABASE_URL": "x", "CONVERGE_RUNTIME_TOPOLOGY": "FLAT"})
	if cfg.RuntimeTopology != "flat" {
		t.Fatalf("topology = %q, want flat (case-insensitive)", cfg.RuntimeTopology)
	}
	loadErr(t, map[string]string{"CONVERGE_DATABASE_URL": "x", "CONVERGE_RUNTIME_TOPOLOGY": "mesh"}, "invalid CONVERGE_RUNTIME_TOPOLOGY")
}

func TestLoadDurations(t *testing.T) {
	ok := loadOK(t, map[string]string{
		"CONVERGE_DATABASE_URL":          "x",
		"CONVERGE_RUNTIME_TICK":          "10s",
		"CONVERGE_SWARM_REVIEW_INTERVAL": "0",
		"CONVERGE_LLM_TIMEOUT":           "1s",
		"CONVERGE_ACCESS_TOKEN_TTL":      "5m",
		"CONVERGE_HTTP_TIMEOUT":          "10s",
	})
	if ok.RuntimeTick != 10*time.Second || ok.SwarmReviewInterval != 0 ||
		ok.LLMTimeout != time.Second || ok.AccessTokenTTL != 5*time.Minute ||
		ok.HTTPTimeout != 10*time.Second {
		t.Fatalf("durations: %+v", ok)
	}
	for k, v := range map[string]string{
		"CONVERGE_RUNTIME_TICK":          "100ms", // < 1s
		"CONVERGE_LLM_TIMEOUT":           "junk",  // unparseable
		"CONVERGE_ACCESS_TOKEN_TTL":      "junk",  // unparseable
		"CONVERGE_REFRESH_TOKEN_TTL":     "junk",  // unparseable
		"CONVERGE_HTTP_TIMEOUT":          "junk",  // unparseable
		"CONVERGE_SWARM_REVIEW_INTERVAL": "500ms", // >0 but < 1s
	} {
		m := map[string]string{"CONVERGE_DATABASE_URL": "x"}
		m[k] = v
		loadErr(t, m, "invalid "+k)
	}
}

func TestLoadLLM(t *testing.T) {
	cfg := loadOK(t, map[string]string{
		"CONVERGE_DATABASE_URL":   "x",
		"CONVERGE_LLM":            "true",
		"CONVERGE_LLM_URLS":       "http://a:8000, http://b:8001,,http://c:8002 ,",
		"CONVERGE_LLM_MAX_TOKENS": "4096",
	})
	if !cfg.LLMEnabled {
		t.Fatal("LLM not enabled")
	}
	want := []string{"http://a:8000", "http://b:8001", "http://c:8002"}
	if len(cfg.LLMURLs) != len(want) {
		t.Fatalf("LLMURLs = %v", cfg.LLMURLs)
	}
	for i := range want {
		if cfg.LLMURLs[i] != want[i] {
			t.Fatalf("LLMURLs[%d] = %q, want %q (trailing/empty entries must drop)", i, cfg.LLMURLs[i], want[i])
		}
	}
	if cfg.LLMMaxTokens != 4096 {
		t.Fatalf("LLMMaxTokens = %d", cfg.LLMMaxTokens)
	}
	for _, v := range []string{"yes", "2"} {
		loadErr(t, map[string]string{"CONVERGE_DATABASE_URL": "x", "CONVERGE_LLM": v}, "invalid CONVERGE_LLM")
	}
	for _, v := range []string{"0", "-3", "junk"} {
		loadErr(t, map[string]string{"CONVERGE_DATABASE_URL": "x", "CONVERGE_LLM_MAX_TOKENS": v}, "invalid CONVERGE_LLM_MAX_TOKENS")
	}
}

func TestLoadPoolBounds(t *testing.T) {
	ok := loadOK(t, map[string]string{
		"CONVERGE_DATABASE_URL": "x",
		"CONVERGE_DB_MIN_CONNS": "0",
		"CONVERGE_DB_MAX_CONNS": "5",
	})
	if ok.DBMinConns != 0 || ok.DBMaxConns != 5 {
		t.Fatalf("pool = %d/%d", ok.DBMinConns, ok.DBMaxConns)
	}
	for min, max := range map[string]string{"5": "3", "0": "0", "-1": "5"} {
		loadErr(t, map[string]string{"CONVERGE_DATABASE_URL": "x", "CONVERGE_DB_MIN_CONNS": min, "CONVERGE_DB_MAX_CONNS": max}, "invalid pool bounds")
	}
	loadErr(t, map[string]string{"CONVERGE_DATABASE_URL": "x", "CONVERGE_DB_MAX_CONNS": "junk"}, "CONVERGE_DB_MAX_CONNS")
}

func TestLoadRateLimit(t *testing.T) {
	ok := loadOK(t, map[string]string{
		"CONVERGE_DATABASE_URL":   "x",
		"CONVERGE_RATE_LIMIT_RPS": "0",
	})
	if ok.RateLimitRPS != 0 {
		t.Fatalf("RPS = %v, want 0 (disabled)", ok.RateLimitRPS)
	}
	loadErr(t, map[string]string{"CONVERGE_DATABASE_URL": "x", "CONVERGE_RATE_LIMIT_RPS": "-1"}, "invalid CONVERGE_RATE_LIMIT_RPS")
	loadErr(t, map[string]string{"CONVERGE_DATABASE_URL": "x", "CONVERGE_RATE_LIMIT_BURST": "-1"}, "invalid CONVERGE_RATE_LIMIT_BURST")
}

func TestLoadSMTP(t *testing.T) {
	ok := loadOK(t, map[string]string{
		"CONVERGE_DATABASE_URL": "x",
		"CONVERGE_SMTP_HOST":    "mail.example",
		"CONVERGE_SMTP_PORT":    "2525",
		"CONVERGE_SMTP_TLS":     "IMPLICIT",
	})
	if ok.SMTPHost != "mail.example" || ok.SMTPPort != 2525 || ok.SMTPTLS != "implicit" {
		t.Fatalf("smtp = %q %d %q", ok.SMTPHost, ok.SMTPPort, ok.SMTPTLS)
	}
	loadErr(t, map[string]string{"CONVERGE_DATABASE_URL": "x", "CONVERGE_SMTP_PORT": "70000"}, "invalid CONVERGE_SMTP_PORT")
	loadErr(t, map[string]string{"CONVERGE_DATABASE_URL": "x", "CONVERGE_SMTP_PORT": "0"}, "invalid CONVERGE_SMTP_PORT")
	loadErr(t, map[string]string{"CONVERGE_DATABASE_URL": "x", "CONVERGE_SMTP_TLS": "plain"}, "invalid CONVERGE_SMTP_TLS")
}

func TestLoadBooleanAndLogLevel(t *testing.T) {
	cfg := loadOK(t, map[string]string{
		"CONVERGE_DATABASE_URL":   "x",
		"CONVERGE_SECURE_COOKIES": "true",
		"CONVERGE_DEV_MODE":       "true",
		"CONVERGE_LOG_LEVEL":      "debug",
	})
	if !cfg.SecureCookies || !cfg.DevMode || cfg.LogLevel != "debug" {
		t.Fatalf("flags: %+v", cfg)
	}
	// "1" must NOT enable SecureCookies: only the literal "true" does.
	cfg = loadOK(t, map[string]string{"CONVERGE_DATABASE_URL": "x", "CONVERGE_SECURE_COOKIES": "1"})
	if cfg.SecureCookies {
		t.Fatal(`SecureCookies set by "1"; the contract is the literal "true"`)
	}
	loadErr(t, map[string]string{"CONVERGE_DATABASE_URL": "x", "CONVERGE_LOG_LEVEL": "trace"}, "invalid CONVERGE_LOG_LEVEL")
}

func TestOrDevSecret(t *testing.T) {
	if got := orDevSecret(""); got != devSessionSecret {
		t.Fatalf("orDevSecret(\"\") = %q", got)
	}
	if got := orDevSecret("real"); got != "real" {
		t.Fatalf("orDevSecret(real) = %q", got)
	}
}

func TestInt32FromEnv(t *testing.T) {
	t.Setenv("X_TEST_INT", "")
	if n, err := int32FromEnv("X_TEST_INT", 7); err != nil || n != 7 {
		t.Fatalf("unset: %d %v", n, err)
	}
	t.Setenv("X_TEST_INT", "42")
	if n, err := int32FromEnv("X_TEST_INT", 7); err != nil || n != 42 {
		t.Fatalf("set: %d %v", n, err)
	}
	t.Setenv("X_TEST_INT", "junk")
	if _, err := int32FromEnv("X_TEST_INT", 7); err == nil {
		t.Fatal("junk accepted")
	}
}
