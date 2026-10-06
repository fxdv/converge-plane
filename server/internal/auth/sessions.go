// Server-side session state.
//
// Access tokens stay stateless: HMAC-signed, short-lived, checked without
// a query. The refresh token is the long-lived credential, so every use is
// checked against its sessions row and rotated:
//
//   - sign-in inserts the row holding the refresh token's hash;
//   - refresh swaps in the next token's hash under a row lock and keeps
//     the one it replaced for refreshReuseGrace, so two tabs refreshing
//     with the same cookie are not mistaken for theft;
//   - any older token is a replayed copy: the session is revoked, which
//     signs out both the thief and the victim;
//   - sign-out revokes the row.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
)

// refreshReuseGrace is how long the refresh token a rotation replaced is
// still honoured, without being rotated again.
const refreshReuseGrace = 30 * time.Second

// sessionRetention is how long expired and revoked rows are kept for the
// audit trail before sign-in prunes them.
const sessionRetention = 30 * 24 * time.Hour

var (
	// errSessionInvalid: the session is unknown, expired, revoked, or its
	// account is no longer active.
	errSessionInvalid = errors.New("session invalid")
	// errSessionReused: a superseded refresh token was presented; the
	// session has been revoked.
	errSessionReused = errors.New("refresh token reused")
)

// startSession creates the sessions row for a sign-in and mints its
// tokens.
func (s *Service) startSession(ctx context.Context, accountID, ip, userAgent string) (SessionMaterial, error) {
	sid, err := newUUID()
	if err != nil {
		return SessionMaterial{}, err
	}
	m := s.issueSession(accountID, sid)
	if _, err := s.pool.Exec(ctx, `
		insert into sessions (id, account_id, token_hash, expires_at, user_agent, ip)
		values ($1, $2, $3, $4, $5, $6)`,
		sid, accountID, hashValue(m.RefreshToken), m.RefreshExpiry, userAgent, inetOrNil(ip)); err != nil {
		return SessionMaterial{}, fmt.Errorf("insert session: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `
		delete from sessions
		where account_id = $1
		  and least(coalesce(revoked_at, expires_at), expires_at) < now() - $2::interval`,
		accountID, intervalSQL(sessionRetention)); err != nil {
		s.log.Warn("prune sessions", "account_id", accountID, "error", err)
	}
	return m, nil
}

// refreshSession redeems a verified refresh token. rotated is false when
// the token was the one a concurrent refresh just replaced: the caller
// then renews the access material only.
func (s *Service) refreshSession(ctx context.Context, c tokenClaims, token, ip, userAgent string) (m SessionMaterial, rotated bool, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SessionMaterial{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current, previous string
	var inGrace, live bool
	err = tx.QueryRow(ctx, `
		select s.token_hash, coalesce(s.prev_token_hash, ''),
		       coalesce(s.rotated_at > now() - $3::interval, false),
		       s.revoked_at is null and s.expires_at > now() and a.status = 'active'
		from sessions s join accounts a on a.id = s.account_id
		where s.id = $1 and s.account_id = $2
		for update of s`,
		c.Session, c.Account, intervalSQL(refreshReuseGrace)).Scan(&current, &previous, &inGrace, &live)
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionMaterial{}, false, errSessionInvalid
	}
	if err != nil {
		return SessionMaterial{}, false, err
	}
	if !live {
		return SessionMaterial{}, false, errSessionInvalid
	}

	presented := hashValue(token)
	switch {
	case presented == current:
		m = s.issueSession(c.Account, c.Session)
		if _, err := tx.Exec(ctx, `
			update sessions
			set token_hash = $2, prev_token_hash = $3, rotated_at = now(),
			    last_seen_at = now(), expires_at = $4, ip = $5, user_agent = $6
			where id = $1`,
			c.Session, hashValue(m.RefreshToken), current, m.RefreshExpiry, inetOrNil(ip), userAgent); err != nil {
			return SessionMaterial{}, false, err
		}
		rotated = true
	case presented == previous && inGrace:
		m = s.issueSession(c.Account, c.Session)
		if _, err := tx.Exec(ctx,
			`update sessions set last_seen_at = now() where id = $1`, c.Session); err != nil {
			return SessionMaterial{}, false, err
		}
	default:
		if _, err := tx.Exec(ctx, `
			update sessions set revoked_at = now(), revoked_reason = 'reuse'
			where id = $1`, c.Session); err != nil {
			return SessionMaterial{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return SessionMaterial{}, false, err
		}
		s.announceRevoked(ctx, c.Session)
		return SessionMaterial{}, false, errSessionReused
	}
	if err := tx.Commit(ctx); err != nil {
		return SessionMaterial{}, false, err
	}
	return m, rotated, nil
}

// revokeSession ends a session: its refresh token stops working and, on
// this instance, so do its access tokens.
func (s *Service) revokeSession(ctx context.Context, c tokenClaims, reason string) error {
	s.announceRevoked(ctx, c.Session)
	_, err := s.pool.Exec(ctx, `
		update sessions set revoked_at = now(), revoked_reason = $3
		where id = $1 and account_id = $2 and revoked_at is null`,
		c.Session, c.Account, reason)
	return err
}

// RevokeCachedSessions drops access tokens for these sessions on this
// process and tells the others. The caller has already set revoked_at.
func (s *Service) RevokeCachedSessions(ctx context.Context, sessionIDs []string) {
	if s == nil {
		return
	}
	for _, id := range sessionIDs {
		s.announceRevoked(ctx, id)
	}
}

func (s *Service) announceRevoked(ctx context.Context, sessionID string) {
	s.NoteRevoked(sessionID)
	if s.pool == nil {
		return
	}
	_, _ = s.pool.Exec(ctx, `select pg_notify('converge_session', $1)`, sessionID)
}

// inetOrNil keeps an unparseable client address out of the inet column.
func inetOrNil(ip string) any {
	if addr, err := netip.ParseAddr(ip); err == nil {
		return addr.String()
	}
	return nil
}
