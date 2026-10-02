package auth

import (
	"context"
	"time"

	"converge/internal/netx"
)

// sharedAuthSQL spends one sign-in token. The first sight of a key is
// allowed and stores burst-1, matching the in-memory limiter. A later
// call is allowed only when the refilled bucket held a token.
const sharedAuthSQL = `
with prev as (
  select tokens, updated_at from auth_rate_buckets where bucket_key = $1
),
up as (
  insert into auth_rate_buckets as b (bucket_key, tokens, updated_at)
  values (
    $1,
    case
      when not exists (select 1 from prev) then $2::float8 - 1
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
  on conflict (bucket_key) do update
  set tokens = excluded.tokens, updated_at = excluded.updated_at
  returning bucket_key
)
select
  (select bucket_key from up) is not null
  and (
    not exists (select 1 from prev)
    or coalesce((
      select least($3::float8, tokens + $4::float8 * extract(epoch from (clock_timestamp() - updated_at))) >= 1
      from prev
    ), false)
  )`

// allowAuth reports whether key may proceed. Postgres is asked first so
// every API process spends the same budget. A failed read uses this
// process's own bucket.
func (s *Service) allowAuth(ctx context.Context, key string, memory *netx.Limiter, rate float64, burst int) bool {
	if allowed, ok := s.sharedAuth(ctx, key, rate, burst); ok {
		return allowed
	}
	return memory.Allow(key)
}

func (s *Service) sharedAuth(ctx context.Context, key string, rate float64, burst int) (bool, bool) {
	if s == nil || s.pool == nil || key == "" || len(key) > 300 || burst < 1 {
		return false, false
	}
	qctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	var allowed bool
	err := s.pool.QueryRow(qctx, sharedAuthSQL, key, float64(burst), float64(burst), rate).Scan(&allowed)
	if err != nil {
		return false, false
	}
	return allowed, true
}
