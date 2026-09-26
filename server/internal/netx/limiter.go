package netx

import (
	"sync"
	"time"
)

// maxLimiterKeys bounds a Limiter's memory. Keys on unauthenticated
// routes (IPs, emails) are attacker-chosen, so the map must not grow
// without limit; past the cap, idle keys are swept.
const maxLimiterKeys = 100_000

type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter is an in-memory keyed token bucket: each key refills at rate
// tokens per second up to burst. Single-instance by design, like the
// per-account limiter in the api package.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64
	burst   float64
	now     func() time.Time
}

// NewLimiter returns a limiter allowing burst requests at once and rate
// requests per second sustained, per key.
func NewLimiter(rate float64, burst int) *Limiter {
	return &Limiter{
		buckets: make(map[string]*bucket),
		rate:    rate,
		burst:   float64(burst),
		now:     time.Now,
	}
}

// Allow consumes one token for key and reports whether one was available.
// A nil limiter allows everything.
func (l *Limiter) Allow(key string) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= maxLimiterKeys {
			l.sweep(now)
		}
		l.buckets[key] = &bucket{tokens: l.burst - 1, last: now}
		return true
	}
	b.tokens = min(l.burst, b.tokens+l.rate*now.Sub(b.last).Seconds())
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweep drops buckets that have refilled completely: forgetting them is
// indistinguishable from keeping them.
func (l *Limiter) sweep(now time.Time) {
	for k, b := range l.buckets {
		if b.tokens+l.rate*now.Sub(b.last).Seconds() >= l.burst {
			delete(l.buckets, k)
		}
	}
}
