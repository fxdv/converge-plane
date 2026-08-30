// Package httpx assembles the HTTP server: routing, middleware, and the
// operator endpoints. Domain modules mount their routes via NewRouter.
package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// Dependencies carries the collaborators the server needs.
type Dependencies struct {
	Logger    *slog.Logger
	Version   string
	PublicURL string
	WebOrigin string
	// Ready reports database readiness for the /readyz probe.
	Ready func(ctx context.Context) error
	// MountApp registers application routes (auth + API) on the base
	// router after the operator routes. Middleware registered via Use
	// applies to everything mounted afterwards.
	MountApp func(r chi.Router)
}

// Server is the configured HTTP server.
type Server struct {
	srv *http.Server
	log *slog.Logger
}

type contextKey string

const requestIDKey contextKey = "request-id"

// New builds the HTTP server with the operator router mounted.
func New(d Dependencies) *Server {
	r := NewRouter()
	r.Use(
		Recoverer(d.Logger),
		RequestID(),
		RequestLogger(d.Logger),
		CORS(d.WebOrigin),
	)

	if d.MountApp != nil {
		d.MountApp(r)
	}

	r.MethodFunc("GET", "/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.MethodFunc("GET", "/readyz", func(w http.ResponseWriter, r *http.Request) {
		if d.Ready != nil {
			if err := d.Ready(r.Context()); err != nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{
					"status": "unavailable",
					"reason": "database not ready",
				})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	r.MethodFunc("GET", "/version", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"name":    "converge",
			"version": d.Version,
		})
	})

	srv := &http.Server{
		Addr:              "", // set in Run
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return &Server{srv: srv, log: d.Logger}
}

// NewRouter returns the base router. Domain modules import httpx and mount
// their routes on a router built by this function.
func NewRouter() chi.Router {
	return chi.NewRouter()
}

// Run starts the server and blocks until ctx is cancelled, then performs a
// graceful shutdown of in-flight requests.
func (s *Server) Run(ctx context.Context, addr string) error {
	s.srv.Addr = addr

	errCh := make(chan error, 1)
	go func() {
		s.log.Info("http server listening", "addr", addr)
		if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		s.log.Info("shutting down http server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := s.srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		return nil
	case err := <-errCh:
		return err
	}
}

// ---- middleware ---------------------------------------------------------

// Recoverer converts panics into 500 responses and logs them.
func Recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("panic recovered",
						"panic", fmt.Sprintf("%v", rec),
						"path", r.URL.Path,
						"request_id", RequestIDFromContext(r.Context()),
					)
					writeJSON(w, http.StatusInternalServerError, map[string]string{
						"error": "internal error",
					})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// RequestID ensures every request has a correlation ID. It honours an
// incoming X-Request-Id header and echoes it back.
func RequestID() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := strings.TrimSpace(r.Header.Get("X-Request-Id"))
			if id == "" {
				id = newRequestID()
			}
			w.Header().Set("X-Request-Id", id)
			next.ServeHTTP(w, r.WithContext(withRequestID(r.Context(), id)))
		})
	}
}

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func withRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFromContext returns the correlation ID set by RequestID, or "".
func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}

// RequestLogger emits one structured log line per request.
func RequestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			log.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", RequestIDFromContext(r.Context()),
				"remote_addr", clientIP(r),
			)
		})
	}
}

// CORS allows the web client origin to call the API with credentials.
// Self-hosted deployments commonly run web and API on different origins;
// in single-domain layouts the origin never matches and no CORS headers
// are emitted.
func CORS(webOrigin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" || origin != webOrigin {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-Id, If-Match, Idempotency-Key")
			w.Header().Set("Access-Control-Max-Age", "86400")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ---- helpers ------------------------------------------------------------

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush implements http.Flusher for the SSE streams to come.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// clientIP resolves the client IP, trusting X-Forwarded-For only for local
// proxy connections.
func clientIP(r *http.Request) string {
	connIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		connIP = r.RemoteAddr
	}
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" && isLocal(connIP) {
		if first := strings.TrimSpace(strings.Split(fwd, ",")[0]); first != "" {
			return first
		}
	}
	return connIP
}

func isLocal(ip string) bool {
	return ip == "127.0.0.1" || ip == "::1" ||
		strings.HasPrefix(ip, "10.") || strings.HasPrefix(ip, "192.168.") || strings.HasPrefix(ip, "172.16.")
}
