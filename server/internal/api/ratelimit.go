// ratelimit.go — per-account token-bucket rate limiting (M6 hardening).
// spec cs:arch:seams
//
// A swarm of agents is an intentional flood vector the single-instance
// API must absorb without degradation. The limiter bounds each account
// (human or agent) independently, so one misbehaving agent cannot
// starve its human teammates or the rest of the swarm.
//
// The shared bucket lives in rate_buckets (migration 0026), so two API
// processes spend the same budget. The in-memory bucket is the fallback
// when that table cannot be read, and the only bucket unit tests see.
package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type tokenBucket struct {
	tokens float64
	last   time.Time
}

// accountUsage is one account's rolling request counter, the swarm
// panel's token-burn reporting (D2). It uses the same 24h economics
// window as the quiet guards (guardWindow in handoff.go) so the panel's
// statistics and the guards' windows cannot drift apart. The window
// resets on first use after expiry: a long-idle account's counter is
// exact at the cost of at most one window of drift at the reset.
type accountUsage struct {
	count       int64
	windowStart time.Time
}

// accountRateLimiter bounds the request rate per account id.
type accountRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	usage   map[string]*accountUsage
	rps     float64
	burst   float64
}

func newAccountRateLimiter(rps float64, burst int64) *accountRateLimiter {
	return &accountRateLimiter{
		buckets: make(map[string]*tokenBucket),
		usage:   make(map[string]*accountUsage),
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

// recordUsage counts one authenticated request toward the account's
// rolling 24h total. It is the swarm panel's token-burn proxy (D2):
// deliberate in-memory state on the same single-instance seam as the
// token buckets (see the file header) — a shared-broker multi-instance
// deployment moves both behind the broker.
func (l *accountRateLimiter) recordUsage(accountID string) {
	if l == nil || accountID == "" {
		return
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	u, ok := l.usage[accountID]
	if !ok || now.Sub(u.windowStart) >= guardWindow {
		l.usage[accountID] = &accountUsage{count: 1, windowStart: now}
		return
	}
	u.count++
}

// usageCount returns the account's rolling 24h request total (the
// swarm panel's requests24h field); 0 when the window has expired.
func (l *accountRateLimiter) usageCount(accountID string) int64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	u, ok := l.usage[accountID]
	if !ok || time.Since(u.windowStart) >= guardWindow {
		return 0
	}
	return u.count
}

// rateLimitGuard applies the limiter to authenticated v1 traffic. It runs
// inside the session middleware, after the principal is resolved, so
// unauthenticated probes pay nothing but the middleware itself. Every
// authenticated request — allowed or throttled — counts toward the
// account's usage total: a throttled request reached the API either way.
func (a *API) rateLimitGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Value(mcpDispatchKey) != nil {
			// An MCP tool call's inner request: the MCP request paid.
			next.ServeHTTP(w, r)
			return
		}
		if p := PrincipalFromContext(r.Context()); p != nil {
			a.limiter.recordUsage(p.AccountID)
			if !a.allowAccount(r.Context(), p.AccountID) {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":"rate limit exceeded; slow down"}`))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// sharedBucketSQL refills one account's bucket and spends a token when
// one is available. The first row (no previous bucket) is allowed and
// stores a full burst. A later call is allowed only when the refilled
// bucket held at least one token.
const sharedBucketSQL = `
with prev as (
  select tokens, updated_at from rate_buckets where account_id = $1::uuid
),
up as (
  insert into rate_buckets as b (account_id, tokens, updated_at)
  values (
    $1::uuid,
    case
      when not exists (select 1 from prev) then $2::float8
      else (
        select case
          when least($3::float8, tokens + $4::float8 * extract(epoch from (clock_timestamp() - updated_at))) >= 1
          then least($3::float8, tokens + $4::float8 * extract(epoch from (clock_timestamp() - updated_at))) - 1
          else least($3::float8, tokens + $4::float8 * extract(epoch from (clock_timestamp() - updated_at)))
        end
        from prev
      )
    end,
    clock_timestamp()
  )
  on conflict (account_id) do update
  set tokens = excluded.tokens, updated_at = excluded.updated_at
  returning account_id
)
select
  (select account_id from up) is not null
  and (
    not exists (select 1 from prev)
    or coalesce((
      select least($3::float8, tokens + $4::float8 * extract(epoch from (clock_timestamp() - updated_at))) >= 1
      from prev
    ), false)
  )`

// allowAccount spends one token from the shared bucket when Postgres can
// answer, and from this process's bucket when it cannot.
func (a *API) allowAccount(ctx context.Context, accountID string) bool {
	if a == nil || a.limiter == nil || a.limiter.rps <= 0 {
		return true
	}
	if allowed, ok := a.sharedAllow(ctx, accountID); ok {
		return allowed
	}
	return a.limiter.allow(accountID)
}

func (a *API) sharedAllow(ctx context.Context, accountID string) (bool, bool) {
	p, ok := a.pool.(*pgxpool.Pool)
	if !ok || accountID == "" {
		return false, false
	}
	qctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	var allowed bool
	err := p.QueryRow(qctx, sharedBucketSQL,
		accountID, a.limiter.burst, a.limiter.burst, a.limiter.rps).Scan(&allowed)
	if err != nil {
		return false, false
	}
	return allowed, true
}
