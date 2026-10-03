// Long-lived bearer credentials for machine actors (M6).
//
// An agent account authenticates with an API token instead of the
// Supertokens-style session protocol: one token per agent, shown exactly
// once, SHA-256-hashed at rest, revocable per token. Web sessions are
// unaffected: tokens carry a distinct prefix, so a bearer value is
// classified in O(1) and the stateless HMAC validation path stays free
// of any database lookup.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Account kinds. Agents are machine accounts: they authenticate with
// API tokens only, can never receive a magic link, and carry the
// 'agent' workspace role (rendered as AGENT on the wire).
const (
	AccountKindHuman = "human"
	AccountKindAgent = "agent"
)

// AgentEmail derives the reserved mailbox of an agent: eight hex chars
// of the workspace id + the slugified name, on the .local domain
// (RFC 6762: reserved for mDNS, never routed or delivered). Determinism
// makes the accounts.email unique constraint a global de-duplicator:
// the same workspace + name always resolves to the same identity. The
// magic-link flow rejects these emails: an agent identity must never
// sign in as a human. Shared by the admin API and the seed, so both
// materialize identical identities.
func AgentEmail(workspaceID, name string) string {
	local := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		default:
			return '_'
		}
	}, strings.ToLower(name))
	if local == "" {
		local = "agent"
	}
	if len(local) > 48 {
		local = local[:48]
	}
	return fmt.Sprintf("ag-%s-%s@converge.local", workspaceID[:8], local)
}

// APITokenPrefix classifies a bearer value as an API token. Everything
// after the prefix is 256 bits of entropy.
const APITokenPrefix = "conv_agent_"

// APITokenTTL is the lifetime of a newly issued token when the caller
// does not name one. A leaked token then dies on its own.
const APITokenTTL = 90 * 24 * time.Hour

// MaxAPITokenTTL is the longest life a caller may request.
const MaxAPITokenTTL = 365 * 24 * time.Hour

// IssueAPIToken mints a token: prefix + 256 bits, returned as plaintext
// with its SHA-256 hash. The plaintext is never persisted.
func IssueAPIToken() (plaintext, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	plaintext = APITokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(plaintext))
	return plaintext, hex.EncodeToString(sum[:]), nil
}

// Token scopes (docs/spec 07). A token stored with no scopes, including
// every token issued before scopes were required, carries the full
// authority of its agent. A token with scopes may call only the routes
// those scopes name, and nothing an unscoped caller could not. New
// issuance requires scopes.
const (
	ScopeIssuesRead    = "issues:read"
	ScopeIssuesWrite   = "issues:write"
	ScopeCommentsRead  = "comments:read"
	ScopeCommentsWrite = "comments:write"
	ScopeWork          = "work"
	ScopeSyncRead      = "sync:read"
)

var knownScopes = map[string]bool{
	ScopeIssuesRead: true, ScopeIssuesWrite: true,
	ScopeCommentsRead: true, ScopeCommentsWrite: true,
	ScopeWork: true, ScopeSyncRead: true,
}

// ValidScope reports whether s is in the scope vocabulary.
func ValidScope(s string) bool { return knownScopes[s] }

// APIToken is the grant a live API token carries. Scopes nil means the
// agent's full authority; TeamIDs nil means every team the agent can
// reach. Both only ever narrow.
type APIToken struct {
	ID        string
	AccountID string
	Scopes    []string
	TeamIDs   []string
}

// Scoped reports whether the token is limited to named scopes.
func (t *APIToken) Scoped() bool { return t != nil && t.Scopes != nil }

// HasScope reports whether the token may act with scope s.
func (t *APIToken) HasScope(s string) bool {
	if !t.Scoped() {
		return true
	}
	for _, have := range t.Scopes {
		if have == s {
			return true
		}
	}
	return false
}

// TeamLimited reports whether the token is limited to named teams.
func (t *APIToken) TeamLimited() bool { return t != nil && t.TeamIDs != nil }

// TeamGranted reports whether the token's grants include teamID.
func (t *APIToken) TeamGranted(teamID string) bool {
	if !t.TeamLimited() {
		return true
	}
	for _, id := range t.TeamIDs {
		if id == teamID {
			return true
		}
	}
	return false
}

// ValidateAPIToken verifies a bearer API token against the token table
// and the account it belongs to. A token is valid while unexpired,
// unrevoked, and its account active — account suspension kills every
// token of it. ok is false when the token is not a live API token.
//
// The last-use stamp refreshes at most once per hour per token, so
// steady validation costs one indexed read and (rarely) a single-row
// write — a swarm poll loop stays cheap.
func (s *Service) ValidateAPIToken(ctx context.Context, token string) (*APIToken, bool) {
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	var t APIToken
	if err := s.pool.QueryRow(ctx, `
		select t.id, t.account_id, t.scopes, t.team_ids::text[]
		from api_tokens t
		join accounts a on a.id = t.account_id and a.status = 'active'
		where t.token_hash = $1
		  and t.revoked_at is null
		  and (t.expires_at is null or t.expires_at > now())`,
		hash).Scan(&t.ID, &t.AccountID, &t.Scopes, &t.TeamIDs); err != nil {
		return nil, false
	}
	if _, err := s.pool.Exec(ctx, `
		update api_tokens
		set last_used_at = now()
		where token_hash = $1
		  and (last_used_at is null or last_used_at < now() - interval '1 hour')`,
		hash); err != nil {
		s.log.Warn("api token last-use stamp failed", "error", err)
	}
	return &t, true
}

// APITokenLive reports whether a token ValidateAPIToken accepted would
// still be accepted: for connections that outlive the request that
// authenticated them. A failed read reports false.
func (s *Service) APITokenLive(ctx context.Context, id string) bool {
	var live bool
	if err := s.pool.QueryRow(ctx, `
		select exists (
			select 1 from api_tokens t
			join accounts a on a.id = t.account_id and a.status = 'active'
			where t.id = $1
			  and t.revoked_at is null
			  and (t.expires_at is null or t.expires_at > now()))`,
		id).Scan(&live); err != nil {
		return false
	}
	return live
}
