// ratelimit.go — per-account token-bucket rate limiting (M6 hardening).
//
// A swarm of agents is an intentional flood vector the single-instance
// API must absorb without degradation. The limiter bounds each account
// (human or agent) independently, so one misbehaving agent cannot
// starve its human teammates or the rest of the swarm.
//
// In-memory on purpose: the single-instance v1 topology means every
// request lands here, and a map of token buckets is the entire state.
// Buckets are created lazily and never evicted; the population is
// bounded by the number of accounts (membership rows), and a 100-agent
// swarm is a few hundred bytes. A shared-broker multi-instance
// deployment would move this behind the same seam.
package api

import (
	"net/http"
	"sync"
	"time"
)

type tokenBucket struct {
	tokens float64
	last   time.Time
}

// accountRateLimiter bounds the request rate per account id.
type accountRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	rps     float64
	burst   float64
}

func newAccountRateLimiter(rps float64, burst int64) *accountRateLimiter {
	return &accountRateLimiter{
		buckets: make(map[string]*tokenBucket),
		rps:     rps,
		burst:   float64(burst),
	}
}

// allow reports whether the account may proceed with another request now.
// Tokens refill continuously at rps up to the burst ceiling; a disabled
// limiter (rps 0) allows everything.
func (l *accountRateLimiter) allow(accountID string) bool {
	if l == nil || l.rps <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[accountID]
	now := time.Now()
	if !ok {
		l.buckets[accountID] = &tokenBucket{tokens: l.burst, last: now}
		return true
	}
	refill := l.rps * now.Sub(b.last).Seconds()
	b.tokens = min(l.burst, b.tokens+refill)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// rateLimitGuard applies the limiter to authenticated v1 traffic. It runs
// inside the session middleware, after the principal is resolved, so
// unauthenticated probes pay nothing but the middleware itself.
func (a *API) rateLimitGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := PrincipalFromContext(r.Context()); p != nil && !a.limiter.allow(p.AccountID) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limit exceeded; slow down"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}
