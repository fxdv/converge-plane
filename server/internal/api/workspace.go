// workspace.go — M5: workspace administration — rename, preferences,
// invitations, and member suspension.
//
// Permission model (docs/spec/07):
//   - update workspace name: WO/WA
//   - create/revoke invite: WO/WA; accept/decline: the invitee
//   - suspend/reactivate member: WO/WA except the Owner
package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// workspaceRow is a workspaces-table row with everything the client
// Workspace shape needs.
type workspaceRow struct {
	ID, Slug, Name       string
	CreatedAt, UpdatedAt time.Time
}

// workspaceData serializes a workspace row in the exact shape of the
// client's Workspace model (preferences is a union with undefined on
// the client, so the key is omitted rather than null).
func (a *API) workspaceData(r workspaceRow) map[string]any {
	return map[string]any{
		"id":             r.ID,
		"slug":           r.Slug,
		"name":           r.Name,
		"createdAt":      r.CreatedAt.Format(iso),
		"updatedAt":      r.UpdatedAt.Format(iso),
		"actionsEnabled": false,
	}
}

// handleUpdateWorkspace implements POST /api/v1/workspaces (workspace
// rename; the client sends only {name}).
func (a *API) handleUpdateWorkspace(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	var req struct {
		Name        string  `json:"name"`
		WorkspaceID *string `json:"workspaceId"`
	}
	if err := jsonDecode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if len(name) < 1 || len(name) > 100 {
		writeError(w, http.StatusUnprocessableEntity, "name must be 1-100 chars")
		return
	}
	workspaceID, ok := a.resolveWorkspaceForWrite(ctx, p, strval(req.WorkspaceID))
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	role, _ := a.workspaceRole(ctx, p, workspaceID)
	if !adminRole(role) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	var row workspaceRow
	err := a.pool.QueryRow(ctx,
		"select id, slug, name, created_at, updated_at from workspaces where id = $1",
		workspaceID).Scan(&row.ID, &row.Slug, &row.Name, &row.CreatedAt, &row.UpdatedAt)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		"update workspaces set name = $2, version = version + 1, updated_at = now() where id = $1",
		row.ID, name); err != nil {
		a.internalError(w, err)
		return
	}
	row.Name = name
	rec, err := a.emitChange(ctx, tx, row.ID, "Workspace", row.ID, "UPDATE", nil)
	if err != nil {
		a.internalError(w, err)
		return
	}
	row.UpdatedAt = time.Now()
	if err := a.refreshOutboxTx(ctx, tx, row.ID, &rec, a.workspaceData(row)); err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	writeJSON(w, http.StatusOK, a.workspaceData(row))
}

// handleUpdateWorkspacePreferences implements POST /api/v1/workspaces/preferences.
// The client's DTO is an empty class (no fields), so this is an honest
// no-op acknowledgement; workspace preferences are not a v1 concept.
func (a *API) handleUpdateWorkspacePreferences(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	var req struct {
		WorkspaceID *string `json:"workspaceId"`
	}
	_ = jsonDecode(r, &req)
	if _, ok := a.resolveWorkspaceForWrite(ctx, p, strval(req.WorkspaceID)); !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

// inviteRow is an invitations-table row for the client Invite shape.
// ConsumedAt/RevokedAt are nullable lifecycle instants (pointers);
// ExpiresAt is NOT NULL in the schema but still scans through a pointer
// (always set) to keep the nil checks uniform.
type inviteRow struct {
	ID, WorkspaceID, Email, Role string
	TeamIDs                      []string
	ExpiresAt, ConsumedAt        *time.Time
	RevokedAt                    *time.Time
	CreatedAt                    time.Time
}

const inviteColumns = `
	i.id, i.workspace_id, i.email, i.role, i.team_ids,
	i.expires_at, i.consumed_at, i.revoked_at, i.created_at`

func scanInviteRow(s scanner, r *inviteRow) error {
	return s.Scan(&r.ID, &r.WorkspaceID, &r.Email, &r.Role, &r.TeamIDs,
		&r.ExpiresAt, &r.ConsumedAt, &r.RevokedAt, &r.CreatedAt)
}

// inviteData serializes the client Invite entity (packages/types invite
// .entity.ts). Dates come back as ISO strings, the wire convention.
func (a *API) inviteData(r inviteRow, fullName string) map[string]any {
	updated := r.CreatedAt
	if r.ConsumedAt != nil && r.ConsumedAt.After(updated) {
		updated = *r.ConsumedAt
	}
	if r.RevokedAt != nil && r.RevokedAt.After(updated) {
		updated = *r.RevokedAt
	}
	status := "INVITED"
	switch {
	case r.ConsumedAt != nil:
		status = "ACCEPTED"
	case r.RevokedAt != nil:
		status = "DECLINED"
	}
	expires := r.CreatedAt
	if r.ExpiresAt != nil {
		expires = *r.ExpiresAt
	}
	teamIDs := r.TeamIDs
	if teamIDs == nil {
		teamIDs = []string{}
	}
	return map[string]any{
		"id":          r.ID,
		"createdAt":   r.CreatedAt.Format(iso),
		"updatedAt":   updated.Format(iso),
		"deleted":     nil,
		"sentAt":      r.CreatedAt.Format(iso),
		"expiresAt":   expires.Format(iso),
		"emailId":     r.Email,
		"fullName":    fullName,
		"workspaceId": r.WorkspaceID,
		"status":      status,
		"teamIds":     teamIDs,
		"role":        clientRole(r.Role),
	}
}

// newInviteTokenHash returns the sha256 of a random 256-bit token. The
// token itself is never stored or exposed (v1 has no email provider);
// only its hash exists in the database, matching the session-token
// treatment in the auth package.
func newInviteTokenHash() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// maxInviteEmails bounds a single invite batch (the client's textarea is
// unbounded; this keeps one request to one transaction of sane size).
const maxInviteEmails = 50

// handleInviteUsers implements POST /api/v1/workspaces/invite_users
// (workspace admin only). Per email: an account is materialized when
// missing (invites precede sign-in), a workspace_members row is created
// or re-invited with status 'invited', the requested teams are granted,
// and one live invitation per workspace+email is upserted (30-day
// expiry). The client's member list updates live through the
// UsersOnWorkspaces records emitted per affected membership.
func (a *API) handleInviteUsers(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	var req struct {
		EmailIDs    string   `json:"emailIds"`
		TeamIDs     []string `json:"teamIds"`
		Role        string   `json:"role"`
		WorkspaceID *string  `json:"workspaceId"`
	}
	if err := jsonDecode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	workspaceID, ok := a.resolveWorkspaceForWrite(ctx, p, strval(req.WorkspaceID))
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	role, _ := a.workspaceRole(ctx, p, workspaceID)
	if !adminRole(role) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	// The client's RoleEnum sends ADMIN|USER; the schema stores
	// admin|member. BOT/AGENT cannot be invited (no machine sign-in).
	memberRole := "member"
	switch strings.ToUpper(req.Role) {
	case "ADMIN":
		memberRole = "admin"
	case "USER", "MEMBER", "":
		memberRole = "member"
	default:
		writeError(w, http.StatusUnprocessableEntity, "role must be ADMIN or USER")
		return
	}
	for _, teamID := range req.TeamIDs {
		if !isUUID(teamID) {
			writeError(w, http.StatusUnprocessableEntity, "teamIds must be uuids")
			return
		}
	}
	// Every requested team must belong to the resolved workspace.
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
	seen := make(map[string]bool)
	var emails []string
	for _, part := range strings.Split(req.EmailIDs, ",") {
		email := strings.ToLower(strings.TrimSpace(part))
		if email == "" {
			continue
		}
		if !emailPattern.MatchString(email) {
			writeError(w, http.StatusUnprocessableEntity, "invalid email address: "+email)
			return
		}
		if seen[email] {
			continue
		}
		seen[email] = true
		emails = append(emails, email)
	}
	if len(emails) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "at least one valid email is required")
		return
	}
	if len(emails) > maxInviteEmails {
		writeError(w, http.StatusUnprocessableEntity, "at most 50 emails per invite batch")
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	results := make(map[string]string, len(emails))
	var records []syncActionRecord
	for _, email := range emails {
		local := strings.SplitN(email, "@", 2)[0]
		accountID, err := a.ensureAccountTx(ctx, tx, email, local)
		if err != nil {
			a.internalError(w, err)
			return
		}
		status, err := a.inviteMembershipTx(ctx, tx, workspaceID, accountID, memberRole, req.TeamIDs)
		if err != nil {
			if errors.Is(err, errAlreadyMember) || errors.Is(err, errMemberSuspended) {
				writeError(w, http.StatusUnprocessableEntity, err.Error())
				return
			}
			a.internalError(w, err)
			return
		}
		if err := a.upsertInviteTx(ctx, tx, p.AccountID, workspaceID, email, memberRole, req.TeamIDs); err != nil {
			a.internalError(w, err)
			return
		}
		if err := a.auditTx(ctx, tx, workspaceID, p.AccountID, "invite.created", "Invitation", ""); err != nil {
			a.internalError(w, err)
			return
		}
		rec, fresh, err := a.emitMemberChangeTx(ctx, tx, workspaceID, accountID)
		if err != nil {
			a.internalError(w, err)
			return
		}
		if err := a.refreshOutboxTx(ctx, tx, workspaceID, &rec, a.memberData(fresh)); err != nil {
			a.internalError(w, err)
			return
		}
		records = append(records, rec)
		results[email] = status
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	for i := range records {
		a.broadcastRecord(records[i])
	}
	writeJSON(w, http.StatusCreated, results)
}

// inviteMembershipTx failures that are client-explainable validation
// errors (not 500s): the batch is rejected with the message as-is.
var (
	errAlreadyMember   = errors.New("already a member")
	errMemberSuspended = errors.New("is suspended and cannot be re-invited")
)

// ensureAccountTx returns the account id for an invited email, creating
// the account when it does not exist yet (name from the local part, the
// same derivation the magic-link sign-in uses).
func (a *API) ensureAccountTx(ctx context.Context, tx pgx.Tx, email, name string) (string, error) {
	var id *string
	err := tx.QueryRow(ctx, `
		insert into accounts (email, name) values ($1, $2)
		on conflict (email) do nothing
		returning id`, email, name).Scan(&id)
	if err != nil {
		return "", err
	}
	if id != nil {
		return *id, nil
	}
	err = tx.QueryRow(ctx, "select id from accounts where email = $1", email).Scan(&id)
	if err != nil {
		return "", err
	}
	return *id, nil
}

// inviteMembershipTx creates or re-invites the workspace membership for
// an invited email and grants the requested teams (without overriding an
// existing manager role). It rejects active and suspended members:
// re-inviting a member is a role decision, not an invitation.
func (a *API) inviteMembershipTx(ctx context.Context, tx pgx.Tx, workspaceID, accountID, memberRole string, teamIDs []string) (string, error) {
	var (
		memberID string
		status   string
	)
	err := tx.QueryRow(ctx,
		"select id, status from workspace_members where workspace_id = $1 and account_id = $2",
		workspaceID, accountID).Scan(&memberID, &status)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		_, err = tx.Exec(ctx, `
			insert into workspace_members (workspace_id, account_id, role, status)
			values ($1, $2, $3, 'invited')`,
			workspaceID, accountID, memberRole)
		if err != nil {
			return "", err
		}
		status = "invited"
	case err != nil:
		return "", err
	case status == "active":
		return "", errAlreadyMember
	case status == "suspended":
		return "", errMemberSuspended
	default: // invited: re-invite refreshes role only
		if _, err := tx.Exec(ctx,
			"update workspace_members set role = $2, updated_at = now() where id = $1",
			memberID, memberRole); err != nil {
			return "", err
		}
	}
	for _, teamID := range teamIDs {
		if _, err := tx.Exec(ctx, `
			insert into team_members (team_id, account_id, role)
			values ($1, $2, 'member')
			on conflict (team_id, account_id) do nothing`, teamID, accountID); err != nil {
			return "", err
		}
	}
	return strings.ToUpper(status), nil
}

// upsertInviteTx replaces the live invitation for workspace+email (or
// inserts a new one when the previous was consumed or revoked): fresh
// token hash, role, team grants, and 30-day expiry.
func (a *API) upsertInviteTx(ctx context.Context, tx pgx.Tx, inviterID, workspaceID, email, memberRole string, teamIDs []string) error {
	tokenHash, err := newInviteTokenHash()
	if err != nil {
		return err
	}
	// pgx encodes a Go []string as a Postgres array literal, which the
	// uuid[] column accepts. An empty slice binds as '{}' (the column
	// default), never null.
	teamArr := teamIDs
	if teamArr == nil {
		teamArr = []string{}
	}
	_, err = tx.Exec(ctx, `
		insert into invitations (workspace_id, email, role, token_hash, invited_by, team_ids, expires_at)
		values ($1, $2, $3, $4, $5, $6, now() + interval '30 days')
		on conflict (workspace_id, email)
		where consumed_at is null and revoked_at is null
		do update set
			role = excluded.role,
			token_hash = excluded.token_hash,
			team_ids = excluded.team_ids,
			invited_by = excluded.invited_by,
			expires_at = excluded.expires_at,
			consumed_at = null,
			revoked_at = null`,
		workspaceID, email, memberRole, tokenHash, inviterID, teamArr)
	return err
}

// handleInviteAction implements POST /api/v1/workspaces/invite_action
// for the invitee: the invite must name the principal's email. Accept
// activates the membership (or creates it) and consumes the invite;
// decline revokes it and removes the placeholder membership. The
// response is the client Invite shape, which the client's accepts flow
// reloads the app on.
func (a *API) handleInviteAction(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	var req struct {
		InviteID string `json:"inviteId"`
		Accept   *bool  `json:"accept"`
	}
	if err := jsonDecode(r, &req); err != nil || !isUUID(req.InviteID) || req.Accept == nil {
		writeError(w, http.StatusBadRequest, "inviteId and accept are required")
		return
	}
	var inv inviteRow
	err := scanInviteRow(a.pool.QueryRow(ctx,
		"select "+inviteColumns+" from invitations i where i.id = $1 and i.email = $2",
		req.InviteID, p.Email), &inv)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if inv.ConsumedAt != nil || inv.RevokedAt != nil {
		// Already processed: idempotent acknowledgement with the final
		// state (the client reloads on ACCEPTED).
		writeJSON(w, http.StatusOK, a.inviteData(inv, p.Fullname))
		return
	}
	if !*req.Accept {
		a.declineInvite(w, r, p, &inv)
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Materialize the membership as active. The membership role was set
	// from the invite at invite time; the upsert only transitions the
	// lifecycle state.
	var memberID string
	// The role subselect degrades to the column default for a brand-new
	// membership: an explicitly bound NULL would violate the NOT NULL
	// constraint (defaults do not apply to explicit NULLs).
	err = tx.QueryRow(ctx, `
		insert into workspace_members (workspace_id, account_id, role, status, joined_at)
		values ($1, $2, coalesce((select role from workspace_members where workspace_id = $1 and account_id = $2), 'member'), 'active', now())
		on conflict (workspace_id, account_id)
		do update set status = 'active', joined_at = now(), updated_at = now()
		returning id`, inv.WorkspaceID, p.AccountID).Scan(&memberID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	for _, teamID := range inv.TeamIDs {
		if _, err := tx.Exec(ctx, `
			insert into team_members (team_id, account_id, role)
			values ($1, $2, 'member')
			on conflict (team_id, account_id) do nothing`, teamID, p.AccountID); err != nil {
			a.internalError(w, err)
			return
		}
	}
	if _, err := tx.Exec(ctx,
		"update invitations set consumed_at = now() where id = $1", inv.ID); err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.auditTx(ctx, tx, inv.WorkspaceID, p.AccountID, "invite.accepted", "Invitation", inv.ID); err != nil {
		a.internalError(w, err)
		return
	}
	rec, fresh, err := a.emitMemberChangeTx(ctx, tx, inv.WorkspaceID, p.AccountID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.refreshOutboxTx(ctx, tx, inv.WorkspaceID, &rec, a.memberData(fresh)); err != nil {
		a.internalError(w, err)
		return
	}
	now := time.Now()
	inv.ConsumedAt = &now
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	writeJSON(w, http.StatusOK, a.inviteData(inv, p.Fullname))
}

// declineInvite revokes the invite and removes the placeholder
// membership (only when it is still in the invited state: an active or
// suspended member who declines a stale invite keeps their membership).
func (a *API) declineInvite(w http.ResponseWriter, r *http.Request, p *Principal, inv *inviteRow) {
	ctx := r.Context()
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var memberID string
	err = tx.QueryRow(ctx,
		"select id from workspace_members where workspace_id = $1 and account_id = $2 and status = 'invited'",
		inv.WorkspaceID, p.AccountID).Scan(&memberID)
	var memberRec *syncActionRecord
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// No placeholder to clean up; the invite alone is revoked.
	case err != nil:
		a.internalError(w, err)
		return
	default:
		if _, err := tx.Exec(ctx,
			"delete from workspace_members where id = $1", memberID); err != nil {
			a.internalError(w, err)
			return
		}
		rec, err := a.emitChange(ctx, tx, inv.WorkspaceID, "UsersOnWorkspaces", memberID, "DELETE", map[string]any{"id": memberID})
		if err != nil {
			a.internalError(w, err)
			return
		}
		memberRec = &rec
	}
	if _, err := tx.Exec(ctx,
		"update invitations set revoked_at = now() where id = $1", inv.ID); err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.auditTx(ctx, tx, inv.WorkspaceID, p.AccountID, "invite.declined", "Invitation", inv.ID); err != nil {
		a.internalError(w, err)
		return
	}
	now := time.Now()
	inv.RevokedAt = &now
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	if memberRec != nil {
		a.broadcastRecord(*memberRec)
	}
	writeJSON(w, http.StatusOK, a.inviteData(*inv, p.Fullname))
}

// handleSuspendMember implements POST /api/v1/workspaces/suspend
// (workspace admin; the workspace Owner cannot be suspended). The
// toggle flips active<->suspended; suspension revokes the member's
// sessions (docs/spec/07), and the member's next page load lands on the
// suspended screen (GET /users reports the membership state).
func (a *API) handleSuspendMember(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	var req struct {
		UserID      string  `json:"userId"`
		WorkspaceID *string `json:"workspaceId"`
	}
	if err := jsonDecode(r, &req); err != nil || !isUUID(req.UserID) {
		writeError(w, http.StatusBadRequest, "userId is required")
		return
	}
	workspaceID, ok := a.resolveWorkspaceForWrite(ctx, p, strval(req.WorkspaceID))
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	role, _ := a.workspaceRole(ctx, p, workspaceID)
	if !adminRole(role) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	target, err := a.memberByAccount(ctx, workspaceID, req.UserID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if strings.EqualFold(target.Role, "owner") {
		writeError(w, http.StatusUnprocessableEntity, "the workspace owner cannot be suspended")
		return
	}
	suspending := false
	switch strings.ToLower(target.Status) {
	case "active":
		suspending = true
	case "suspended":
		// reactivate
	default:
		writeError(w, http.StatusUnprocessableEntity, "member is invited, not a member")
		return
	}
	nextStatus := "suspended"
	if !suspending {
		nextStatus = "active"
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		"update workspace_members set status = $2, updated_at = now() where id = $1",
		target.ID, nextStatus); err != nil {
		a.internalError(w, err)
		return
	}
	if suspending {
		// Spec: suspension revokes sessions so the member's next
		// request is rejected at the session middleware.
		if _, err := tx.Exec(ctx, `
			update sessions set revoked_at = now()
			where account_id = $1 and revoked_at is null`, req.UserID); err != nil {
			a.internalError(w, err)
			return
		}
	}
	action := "member.suspended"
	if !suspending {
		action = "member.reactivated"
	}
	if err := a.auditTx(ctx, tx, workspaceID, p.AccountID, action, "UsersOnWorkspaces", target.ID); err != nil {
		a.internalError(w, err)
		return
	}
	rec, fresh, err := a.emitMemberChangeTx(ctx, tx, workspaceID, req.UserID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.refreshOutboxTx(ctx, tx, workspaceID, &rec, a.memberData(fresh)); err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	writeJSON(w, http.StatusOK, a.memberData(fresh))
}
