// agent.go — M6: agents as first-class actors.
// spec cs:agents:identity
// spec cs:agents:tokens
//
// An agent is an account of kind 'agent' that works a workspace through
// a long-lived API token instead of a browser session. The admin surface
// here is create / list / delete plus per-token rotation and revocation;
// every operation is WO/WA-only, runs in one transaction, writes the
// same outbox + audit + broadcast trail as the rest of the API, and
// returns wire data in the client's exact shapes.
//
// Swarms: each agent is a distinct account, so attribution, suspension
// (the existing /workspaces/suspend kill switch) and token revocation
// scale per agent. A suspended agent's tokens are revoked, so its next
// request is a 401 at the middleware rather than a 404 deeper in.
package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"converge/internal/auth"

	"github.com/jackc/pgx/v5"
)

// Client-explainable failures (409/404 with the message as-is), same
// convention as the invite sentinels.
var (
	errAgentExists     = errors.New("an agent with this name already exists in the workspace")
	errAgentSuspended  = errors.New("the agent is suspended; reactivate it first")
	errAgentEmailTaken = errors.New("the reserved agent email is already taken by a human account")
	errTokenNotFound   = errors.New("token not found for this agent")
)

// agentNamePattern admits printable names; the display name is rendered
// verbatim by the client, so it is bounded the same way as team names.
var agentNamePattern = regexp.MustCompile(`^[^\x00-\x1f]{1,64}$`)

// agentEmail derives the reserved mailbox of an agent: eight hex chars
// of the workspace id + the slugified name, on the .local domain
// (RFC 6762: reserved for mDNS, never routed or delivered). Determinism
// makes the accounts.email unique constraint a global de-duplicator:
// the same workspace + name always resolves to the same identity.
func agentEmail(workspaceID, name string) string {
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

// agentResponse is the creation/rotation wire shape the client's
// agent-creation dialog renders (the token plaintext is shown once).
type agentResponse struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Email          string    `json:"email"`
	Kind           string    `json:"kind"`
	Role           string    `json:"role"`
	Status         string    `json:"status"`
	TeamIDs        []string  `json:"teamIds"`
	Token          string    `json:"token,omitempty"`
	TokenPrefix    string    `json:"tokenPrefix"`
	TokenExpiresAt time.Time `json:"tokenExpiresAt"`
}

// handleCreateAgent implements POST /api/v1/workspaces/{id}/agents
// (workspace admin only). One transaction materializes: the agent
// account (kind 'agent', reserved email), its active workspace
// membership (role 'agent'), the requested team memberships, one API
// token, the sync outbox record, and the audit row.
func (a *API) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	var req struct {
		Name    string   `json:"name"`
		TeamIDs []string `json:"teamIds"`
	}
	if err := jsonDecode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if !agentNamePattern.MatchString(name) {
		writeError(w, http.StatusUnprocessableEntity, "name must be 1-64 printable characters")
		return
	}
	for _, teamID := range req.TeamIDs {
		if !isUUID(teamID) {
			writeError(w, http.StatusUnprocessableEntity, "teamIds must be uuids")
			return
		}
	}
	workspaceID := r.PathValue("id")
	if !isUUID(workspaceID) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	role, ok := a.workspaceRole(ctx, p, workspaceID)
	if !ok || !adminRole(role) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if len(req.TeamIDs) > 0 {
		var inWS int
		err := a.pool.QueryRow(ctx,
			"select count(*) from teams where workspace_id = $1 and id = any($2::uuid[])",
			workspaceID, req.TeamIDs).Scan(&inWS)
		if err != nil || inWS != len(req.TeamIDs) {
			writeError(w, http.StatusUnprocessableEntity, "teamIds must belong to the workspace")
			return
		}
	}

	email := agentEmail(workspaceID, name)
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The account: created on first use, re-adopted when an agent was
	// deleted earlier under the same name. The conflict clause only
	// applies to existing agent accounts, so a human who somehow owns
	// the reserved email is never converted (the sign-in flow rejects
	// agent emails; this is the belt to that suspend).
	var accountID *string
	err = tx.QueryRow(ctx, `
		insert into accounts (email, name, kind) values ($1, $2, 'agent')
		on conflict (email) do update
			set name = excluded.name, updated_at = now()
			where accounts.kind = 'agent'
		returning id`, email, name).Scan(&accountID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		a.internalError(w, err)
		return
	}
	if accountID == nil {
		var kind string
		if err := tx.QueryRow(ctx,
			"select id, kind from accounts where email = $1", email).
			Scan(&accountID, &kind); err != nil {
			a.internalError(w, err)
			return
		}
		if kind != "agent" {
			writeError(w, http.StatusConflict, errAgentEmailTaken.Error())
			return
		}
	}

	// A pre-existing membership (any lifecycle state) means the agent
	// already exists for this workspace: the admin's lever is the
	// suspend/reactivate toggle, not a second creation.
	if existing, err := a.memberByAccountTx(ctx, tx, workspaceID, *accountID); err == nil {
		switch existing.Status {
		case "suspended":
			writeError(w, http.StatusConflict, errAgentSuspended.Error())
			return
		default:
			writeError(w, http.StatusConflict, errAgentExists.Error())
			return
		}
	}
	if _, err := tx.Exec(ctx, `
		insert into workspace_members (workspace_id, account_id, role, status, joined_at)
		values ($1, $2, 'agent', 'active', now())`, workspaceID, *accountID); err != nil {
		a.internalError(w, err)
		return
	}
	if len(req.TeamIDs) > 0 {
		if _, err := tx.Exec(ctx, `
			insert into team_members (team_id, account_id, role)
			select unnest($2::uuid[]), $1, 'member'
			on conflict do nothing`, *accountID, req.TeamIDs); err != nil {
			a.internalError(w, err)
			return
		}
	}

	plaintext, hash, err := auth.IssueAPIToken()
	if err != nil {
		a.internalError(w, err)
		return
	}
	tokenExpiry := time.Now().Add(auth.APITokenTTL)
	if _, err := tx.Exec(ctx, `
		insert into api_tokens (account_id, name, token_hash, token_prefix, expires_at, created_by)
		values ($1, $2, $3, $4, $5, $6)`,
		*accountID, "default", hash, auth.APITokenPrefix, tokenExpiry, p.AccountID); err != nil {
		a.internalError(w, err)
		return
	}

	rec, fresh, err := a.emitMemberChangeTx(ctx, tx, workspaceID, *accountID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.refreshOutboxTx(ctx, tx, workspaceID, &rec, a.memberData(fresh)); err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.auditTx(ctx, tx, workspaceID, p.AccountID, "agent.created", "Agent", *accountID); err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)

	teamIDs := req.TeamIDs
	if teamIDs == nil {
		teamIDs = []string{}
	}
	writeJSON(w, http.StatusCreated, agentResponse{
		ID:             *accountID,
		Name:           name,
		Email:          email,
		Kind:           "agent",
		Role:           "AGENT",
		Status:         "ACTIVE",
		TeamIDs:        teamIDs,
		Token:          plaintext,
		TokenPrefix:    auth.APITokenPrefix,
		TokenExpiresAt: tokenExpiry,
	})
}

// agentListEntry is the GET list shape: everything the admin UI needs to
// manage a swarm, without any token material.
type agentListEntry struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Email      string     `json:"email"`
	Status     string     `json:"status"`
	TeamIDs    []string   `json:"teamIds"`
	TokenCount int        `json:"tokenCount"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	CreatedAt  time.Time  `json:"createdAt"`
}

// handleListAgents implements GET /api/v1/workspaces/{id}/agents.
// Readable by any workspace member: agents are already visible through
// the UsersOnWorkspaces sync, so listing adds no information an
// unauthorized principal could not already have.
func (a *API) handleListAgents(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	workspaceID := r.PathValue("id")
	if !isUUID(workspaceID) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if _, ok := a.workspaceRole(ctx, p, workspaceID); !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	rows, err := a.pool.Query(ctx, `
		select a.id, a.name, a.email, wm.status, a.created_at,
		       coalesce((select array_agg(tm.team_id) from team_members tm
		               where tm.account_id = a.id), '{}'),
		       count(t.id) filter (where t.revoked_at is null),
		       max(t.last_used_at)
		from accounts a
		join workspace_members wm on wm.workspace_id = $1 and wm.account_id = a.id
		left join api_tokens t on t.account_id = a.id
		where a.kind = 'agent'
		group by a.id, a.name, a.email, wm.status, a.created_at
		order by a.created_at`, workspaceID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	out := []agentListEntry{}
	for rows.Next() {
		var e agentListEntry
		var status string
		if err := rows.Scan(&e.ID, &e.Name, &e.Email, &status, &e.CreatedAt,
			&e.TeamIDs, &e.TokenCount, &e.LastUsedAt); err != nil {
			rows.Close()
			a.internalError(w, err)
			return
		}
		e.Status = strings.ToUpper(status)
		out = append(out, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// agentTarget resolves the workspace + agent membership shared by the
// token rotation/revocation/delete routes: the principal must be a
// workspace admin and the account must be an agent member of it.
func (a *API) agentTarget(ctx context.Context, p *Principal, workspaceID, accountID string) (memberRow, error) {
	if !isUUID(workspaceID) || !isUUID(accountID) {
		return memberRow{}, errAgentExists
	}
	role, ok := a.workspaceRole(ctx, p, workspaceID)
	if !ok || !adminRole(role) {
		return memberRow{}, errAgentExists
	}
	target, err := a.memberByAccount(ctx, workspaceID, accountID)
	if err != nil {
		return memberRow{}, errAgentExists
	}
	var kind string
	if err := a.pool.QueryRow(ctx, "select kind from accounts where id = $1", accountID).Scan(&kind); err != nil {
		return memberRow{}, errAgentExists
	}
	if kind != "agent" {
		return memberRow{}, errAgentExists
	}
	return target, nil
}

// handleRotateAgentToken implements
// POST /api/v1/workspaces/{id}/agents/{accountId}/token (workspace
// admin only): mints one additional live token for the agent. Older
// tokens keep working until individually revoked, so a rotation never
// drops an agent mid-swarm.
func (a *API) handleRotateAgentToken(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	accountID := r.PathValue("accountId")
	if _, err := a.agentTarget(ctx, p, r.PathValue("id"), accountID); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var req struct {
		Name *string `json:"name"`
	}
	_ = jsonDecode(r, &req)
	label := "default"
	if req.Name != nil {
		label = strings.TrimSpace(*req.Name)
	}
	if label == "" {
		label = "default"
	}
	if len(label) > 64 {
		writeError(w, http.StatusUnprocessableEntity, "token name must be 1-64 characters")
		return
	}

	plaintext, hash, err := auth.IssueAPIToken()
	if err != nil {
		a.internalError(w, err)
		return
	}
	tokenExpiry := time.Now().Add(auth.APITokenTTL)
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var tokenID *string
	err = tx.QueryRow(ctx, `
		insert into api_tokens (account_id, name, token_hash, token_prefix, expires_at, created_by)
		values ($1, $2, $3, $4, $5, $6)
		returning id`,
		accountID, label, hash, auth.APITokenPrefix, tokenExpiry, p.AccountID).Scan(&tokenID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.auditTx(ctx, tx, r.PathValue("id"), p.AccountID, "agent.token.issued", "Agent", accountID); err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"tokenId":        *tokenID,
		"token":          plaintext,
		"tokenPrefix":    auth.APITokenPrefix,
		"tokenExpiresAt": tokenExpiry,
	})
}

// handleRevokeAgentToken implements
// POST /api/v1/workspaces/{id}/agents/{accountId}/token/revoke
// (workspace admin only). With a tokenId it revokes that one token;
// without, every live token of the agent (a soft kill switch: the
// membership survives, the credentials die).
func (a *API) handleRevokeAgentToken(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	workspaceID := r.PathValue("id")
	accountID := r.PathValue("accountId")
	if _, err := a.agentTarget(ctx, p, workspaceID, accountID); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var req struct {
		TokenID *string `json:"tokenId"`
	}
	_ = jsonDecode(r, &req)
	q := `
		update api_tokens set revoked_at = now()
		where account_id = $1 and revoked_at is null`
	args := []any{accountID}
	if req.TokenID != nil && *req.TokenID != "" {
		q += " and id = $2"
		args = append(args, *req.TokenID)
	}
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, q, args...)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if req.TokenID != nil && *req.TokenID != "" && tag.RowsAffected() == 0 {
		// Distinguish "wrong id" from "already revoked" without a
		// second query: the target must exist and belong to the agent.
		var exists int
		if err := tx.QueryRow(ctx,
			"select count(*) from api_tokens where account_id = $1 and id = $2",
			accountID, *req.TokenID).Scan(&exists); err != nil {
			a.internalError(w, err)
			return
		}
		if exists == 0 {
			writeError(w, http.StatusNotFound, errTokenNotFound.Error())
			return
		}
	}
	if err := a.auditTx(ctx, tx, workspaceID, p.AccountID, "agent.token.revoked", "Agent", accountID); err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": int(tag.RowsAffected())})
}

// handleDeleteAgent implements
// DELETE /api/v1/workspaces/{id}/agents/{accountId} (workspace admin
// only). The agent leaves the workspace: its memberships are stripped,
// every token is revoked, and a DELETE sync record keeps the client's
// member list in step. The account row itself stays (dormant) so issue
// assignments and history keep their attribution; re-creation under the
// same name re-adopts it.
func (a *API) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	workspaceID := r.PathValue("id")
	accountID := r.PathValue("accountId")
	target, err := a.agentTarget(ctx, p, workspaceID, accountID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// DELETE records carry only the id (house contract, matching the
	// issue/comment/team/label/view deletions): clients delete by id,
	// and the row is about to be gone, so its payload serves no sync
	// consumer.
	rec, err := a.emitChange(ctx, tx, workspaceID, "UsersOnWorkspaces", target.ID, "DELETE", map[string]any{"id": target.ID})
	if err != nil {
		a.internalError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `
		update api_tokens set revoked_at = now()
		where account_id = $1 and revoked_at is null`, accountID); err != nil {
		a.internalError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `
		delete from team_members
		where account_id = $1
		  and team_id in (select id from teams where workspace_id = $2)`,
		accountID, workspaceID); err != nil {
		a.internalError(w, err)
		return
	}
	if _, err := tx.Exec(ctx,
		"delete from workspace_members where id = $1", target.ID); err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.auditTx(ctx, tx, workspaceID, p.AccountID, "agent.deleted", "Agent", accountID); err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
