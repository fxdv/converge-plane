package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func testLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, nil))
}

func TestRecovererPanicsTo500(t *testing.T) {
	var buf bytes.Buffer
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	h := Recoverer(testLogger(&buf))(inner)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if body["error"] != "internal error" {
		t.Fatalf("error = %q — the panic value must never leak to the client", body["error"])
	}
	if !strings.Contains(buf.String(), "boom") {
		t.Fatal("panic not logged")
	}
}

func TestRecovererPassesThrough(t *testing.T) {
	var buf bytes.Buffer
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	h := Recoverer(testLogger(&buf))(inner)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want 418", w.Code)
	}
	if buf.Len() != 0 {
		t.Fatal("no panic, but the logger fired")
	}
}

func TestRequestIDGeneratedAndContext(t *testing.T) {
	var gotCtxID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCtxID = RequestIDFromContext(r.Context())
	})
	h := RequestID()(inner)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	gotHeader := w.Header().Get("X-Request-Id")
	if len(gotHeader) != 16 {
		t.Fatalf("generated request id = %q, want 16 hex chars", gotHeader)
	}
	if gotCtxID != gotHeader {
		t.Fatalf("context id %q != header id %q", gotCtxID, gotHeader)
	}
}

func TestRequestIDHonoursIncoming(t *testing.T) {
	var gotCtxID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCtxID = RequestIDFromContext(r.Context())
	})
	h := RequestID()(inner)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.Header.Set("X-Request-Id", "  caller-id-1  ")
	h.ServeHTTP(w, r)
	if got := w.Header().Get("X-Request-Id"); got != "caller-id-1" {
		t.Fatalf("header = %q, want trimmed caller id", got)
	}
	if gotCtxID != "caller-id-1" {
		t.Fatalf("context id = %q", gotCtxID)
	}
}

func TestRequestIDFromContextEmpty(t *testing.T) {
	if got := RequestIDFromContext(context.Background()); got != "" {
		t.Fatalf("= %q, want empty", got)
	}
}

func TestRequestIDUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := newRequestID()
		if seen[id] {
			t.Fatalf("duplicate request id %q", id)
		}
		seen[id] = true
	}
}

func TestRequestLoggerEmitsOneLine(t *testing.T) {
	var buf bytes.Buffer
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	h := RequestLogger(testLogger(&buf))(inner)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/v1/x", nil))
	out := buf.String()
	if c := strings.Count(out, "http request"); c != 1 {
		t.Fatalf("log lines with 'http request' = %d, want 1:\n%s", c, out)
	}
	for _, want := range []string{"status=201", "method=POST", "path=/api/v1/x", "duration_ms="} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q:\n%s", want, out)
		}
	}
}

func TestCORSMatchingOrigin(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	h := CORS("http://localhost:3000")(inner)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.Header.Set("Origin", "http://localhost:3000")
	h.ServeHTTP(w, r)
	if !called {
		t.Fatal("matching-origin request was not passed through")
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("allow-origin = %q", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("allow-credentials = %q", got)
	}
	if w.Header().Get("Access-Control-Max-Age") == "" {
		t.Fatal("max-age missing")
	}
}

func TestCORSPreFlightShortCircuits(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	h := CORS("http://localhost:3000")(inner)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodOptions, "/x", nil)
	r.Header.Set("Origin", "http://localhost:3000")
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", w.Code)
	}
	if called {
		t.Fatal("preflight reached the handler")
	}
	if w.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Fatal("preflight missing allow-methods")
	}
}

func TestCORSForeignOriginIgnored(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	h := CORS("http://localhost:3000")(inner)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.Header.Set("Origin", "http://evil.example")
	h.ServeHTTP(w, r)
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("foreign origin received CORS headers")
	}
	// No origin at all (same-origin navigation) also gets nothing.
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w2.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("originless request received CORS headers")
	}
}

func TestStatusRecorderCatchesStatusAndFlush(t *testing.T) {
	w := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	rec.WriteHeader(http.StatusConflict)
	rec.Write([]byte("x"))
	rec.Flush()
	if rec.status != http.StatusConflict {
		t.Fatalf("recorded status = %d, want 409", rec.status)
	}
	if w.Code != http.StatusConflict {
		t.Fatalf("underlying writer status = %d", w.Code)
	}
	// A non-Flusher inner writer must not panic.
	plain := &nopFlusher{ResponseWriter: httptest.NewRecorder()}
	p := &statusRecorder{ResponseWriter: plain, status: http.StatusOK}
	p.Flush()
}

type nopFlusher struct{ http.ResponseWriter }

func TestClientIP(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		forwarded  string
		want       string
	}{
		{"plain", "203.0.113.5:443", "", "203.0.113.5"},
		{"xff from public ignored", "203.0.113.5:443", "10.1.2.3", "203.0.113.5"},
		{"xff from loopback trusted", "127.0.0.1:5000", "203.0.113.5, 10.1.2.3", "203.0.113.5"},
		{"xff from v6 loopback trusted", "[::1]:5000", "203.0.113.5", "203.0.113.5"},
		{"xff from 10/8 trusted", "10.0.0.2:8443", "203.0.113.5", "203.0.113.5"},
		{"first of a list wins", "192.168.1.10:80", "  a , b ", "a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/x", nil)
			r.RemoteAddr = c.remoteAddr
			if c.forwarded != "" {
				r.Header.Set("X-Forwarded-For", c.forwarded)
			}
			if got := clientIP(r); got != c.want {
				t.Fatalf("clientIP = %q, want %q", got, c.want)
			}
		})
	}
}

// TestClientIP172Range pins the CURRENT 172/8 trust boundary: only
// 172.16.* is treated as local. The full RFC1918 172.16/12 span
// (172.16.0.0-172.31.255.255) is partially untrusted — 172.20.x falls
// through to the connection IP. The under-trust direction is safe (logs
// record the proxy instead of the client); if this widens, the tests must
// move with it.
func TestClientIP172Range(t *testing.T) {
	for _, trusted := range []string{"127.0.0.1", "::1", "10.1.1.1", "192.168.1.1", "172.16.5.5"} {
		if !isLocal(trusted) {
			t.Fatalf("%s not treated as local", trusted)
		}
	}
	for _, untrusted := range []string{"8.8.8.8", "172.20.5.5", "172.31.255.255"} {
		if isLocal(untrusted) {
			t.Fatalf("%s treated as local (X-Forwarded-For would be trusted)", untrusted)
		}
	}
}

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusAccepted, map[string]string{"ok": "yes"})
	if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d", w.Code)
	}
	var got map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got["ok"] != "yes" {
		t.Fatalf("body = %q, %v", w.Body.String(), err)
	}
}

// TestOperatorRoutes pins the New() surface: healthz/readyz/version and
// the middleware chain around them.
func TestOperatorRoutes(t *testing.T) {
	var buf bytes.Buffer
	s := New(Dependencies{
		Logger:    testLogger(&buf),
		Version:   "9.9.9",
		WebOrigin: "http://localhost:3000",
		Ready:     func(context.Context) error { return nil },
	})
	ts := httptest.NewServer(s.srv.Handler)
	defer ts.Close()

	if body, code := get(t, ts.URL+"/healthz"); code != 200 || !strings.Contains(body, `"status":"ok"`) {
		t.Fatalf("healthz: %d %s", code, body)
	}
	if body, code := get(t, ts.URL+"/readyz"); code != 200 || !strings.Contains(body, `"status":"ready"`) {
		t.Fatalf("readyz: %d %s", code, body)
	}
	if body, code := get(t, ts.URL+"/version"); code != 200 || !strings.Contains(body, "9.9.9") {
		t.Fatalf("version: %d %s", code, body)
	}
	// The RequestID middleware wraps operator routes too.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/healthz", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.Header.Get("X-Request-Id") == "" {
		t.Fatal("operator route missing X-Request-Id")
	}
	// Ready failing -> 503 with a reason, never a stack.
	s2 := New(Dependencies{
		Logger: testLogger(&bytes.Buffer{}),
		Ready:  func(context.Context) error { return fmt.Errorf("db down") },
	})
	ts2 := httptest.NewServer(s2.srv.Handler)
	defer ts2.Close()
	body, code := get(t, ts2.URL+"/readyz")
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "database not ready") {
		t.Fatalf("readyz (down): %d %s", code, body)
	}
}

func get(t *testing.T, url string) (string, int) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return string(b), res.StatusCode
}

func TestNewDefaultsAndMountApp(t *testing.T) {
	var buf bytes.Buffer
	mounted := false
	s := New(Dependencies{
		Logger: testLogger(&buf),
		Ready:  func(context.Context) error { return nil },
		MountApp: func(r chi.Router) {
			r.MethodFunc(http.MethodGet, "/app", func(http.ResponseWriter, *http.Request) {
				mounted = true
			})
		},
	})
	ts := httptest.NewServer(s.srv.Handler)
	defer ts.Close()
	if _, code := get(t, ts.URL+"/app"); code != 200 {
		t.Fatalf("/app: %d", code)
	}
	if !mounted {
		t.Fatal("MountApp route was not reachable")
	}
	// ReadTimeout defaults to 30s when unset.
	if s.srv.ReadTimeout != 30*time.Second {
		t.Fatalf("ReadTimeout = %v, want 30s default", s.srv.ReadTimeout)
	}
	if s.srv.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("ReadHeaderTimeout = %v, want 5s", s.srv.ReadHeaderTimeout)
	}
	if s.srv.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout = %v, want 0 (SSE streams must not be capped)", s.srv.WriteTimeout)
	}
}

var hexID = regexp.MustCompile(`^[0-9a-f]{16}$`)

func TestRequestIDFormat(t *testing.T) {
	for i := 0; i < 100; i++ {
		if !hexID.MatchString(newRequestID()) {
			t.Fatalf("request id %q is not 16 lowercase hex", newRequestID())
		}
	}
}
