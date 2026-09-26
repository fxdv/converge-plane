package netx

import (
	"strconv"
	"testing"
	"time"
)

func TestLimiterBurstAndRefill(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewLimiter(1, 3)
	l.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if !l.Allow("k") {
			t.Fatalf("request %d within burst denied", i+1)
		}
	}
	if l.Allow("k") {
		t.Fatal("request past burst allowed")
	}
	if !l.Allow("other") {
		t.Fatal("keys must be independent")
	}
	now = now.Add(time.Second)
	if !l.Allow("k") {
		t.Fatal("token not refilled after 1s at 1 rps")
	}
	if l.Allow("k") {
		t.Fatal("refill exceeded rate")
	}
}

func TestLimiterSweepsIdleKeysAtCap(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewLimiter(1, 1)
	l.now = func() time.Time { return now }
	for i := 0; i < maxLimiterKeys; i++ {
		l.buckets[strconv.Itoa(i)] = &bucket{tokens: 0, last: now}
	}
	now = now.Add(time.Hour)
	l.Allow("fresh")
	if len(l.buckets) != 1 {
		t.Fatalf("idle keys not swept at the cap: %d remain", len(l.buckets))
	}
}

func TestNilLimiterAllows(t *testing.T) {
	var l *Limiter
	if !l.Allow("x") {
		t.Fatal("nil limiter must allow")
	}
}
