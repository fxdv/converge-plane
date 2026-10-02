package auth

import (
	"context"
	"testing"
)

func TestSharedAuthLimitHoldsAcrossProcesses(t *testing.T) {
	s, pool, _ := dbService(t)
	other := NewService(pool, s.cfg, s.log, nil)
	key := "ip:203.0.113.9"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from auth_rate_buckets where bucket_key = $1`, key)
	})
	ctx := context.Background()
	allowed, used := s.sharedAuth(ctx, key, 0.0001, 1)
	if !used || !allowed {
		t.Fatalf("first spend = %v used %v", allowed, used)
	}
	allowed, used = other.sharedAuth(ctx, key, 0.0001, 1)
	if !used || allowed {
		t.Fatalf("second process spend = %v used %v, want refused", allowed, used)
	}
}
