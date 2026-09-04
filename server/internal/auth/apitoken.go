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

// APITokenTTL is the lifetime of a newly issued token. Agents are
// long-lived workers; the bound exists so an unrevoked leaked token
// expires on its own.
const APITokenTTL = 10 * 365 * 24 * time.Hour

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

// ValidateAPIToken verifies a bearer API token against the token table
// and the account it belongs to. A token is valid while unexpired,
// unrevoked, and its account active — account suspension kills every
// token of it. Returns "" when the token is not a live API token.
//
// The last-use stamp refreshes at most once per hour per token, so
// steady validation costs one indexed read and (rarely) a single-row
// write — a swarm poll loop stays cheap.
func (s *Service) ValidateAPIToken(ctx context.Context, token string) (string, bool) {
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	var accountID string
	if err := s.pool.QueryRow(ctx, `
		select t.account_id
		from api_tokens t
		join accounts a on a.id = t.account_id and a.status = 'active'
		where t.token_hash = $1
		  and t.revoked_at is null
		  and (t.expires_at is null or t.expires_at > now())`,
		hash).Scan(&accountID); err != nil {
		return "", false
	}
	if _, err := s.pool.Exec(ctx, `
		update api_tokens
		set last_used_at = now()
		where token_hash = $1
		  and (last_used_at is null or last_used_at < now() - interval '1 hour')`,
		hash); err != nil {
		s.log.Warn("api token last-use stamp failed", "error", err)
	}
	return accountID, true
}
