// member.go — M5: the shared workspace-membership row and data builder.
//
// The sync collector (collectMembers) and every membership mutation
// (team membership, invites, suspension) serialize through memberData so
// bootstrap, delta, and mutation responses speak the identical vocabulary
// of the client's UsersOnWorkspace model.
package api

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// memberRow is a workspace_members row with everything the client shape
// needs, including the member's team memberships.
type memberRow struct {
	ID, Role, Status     string
	AccountID            string
	WorkspaceID          string
	TeamIDs              []string
	CreatedAt, UpdatedAt time.Time
}

// memberColumns is the shared SELECT list for membership rows. The team
// ids subquery reads team_members by account (indexed), so the extra
// work per row is one index lookup each.
const memberColumns = `
	wm.id, wm.role, wm.status, wm.account_id, wm.workspace_id,
	wm.created_at, wm.updated_at,
	coalesce((select array_agg(tm.team_id) from team_members tm
	          where tm.account_id = wm.account_id), '{}')`

// scanMemberRow fills a memberRow from a row or rowset cursor.
func scanMemberRow(s scanner, r *memberRow) error {
	return s.Scan(&r.ID, &r.Role, &r.Status, &r.AccountID, &r.WorkspaceID,
		&r.CreatedAt, &r.UpdatedAt, &r.TeamIDs)
}

// memberByAccount loads the account's membership in a workspace (any
// lifecycle state: active, invited, suspended).
func (a *API) memberByAccount(ctx context.Context, workspaceID, accountID string) (memberRow, error) {
	var r memberRow
	return r, scanMemberRow(a.pool.QueryRow(ctx,
		"select "+memberColumns+" from workspace_members wm where wm.workspace_id = $1 and wm.account_id = $2",
		workspaceID, accountID), &r)
}

// memberByAccountTx is memberByAccount inside the caller's transaction.
func (a *API) memberByAccountTx(ctx context.Context, tx pgx.Tx, workspaceID, accountID string) (memberRow, error) {
	var r memberRow
	return r, scanMemberRow(tx.QueryRow(ctx,
		"select "+memberColumns+" from workspace_members wm where wm.workspace_id = $1 and wm.account_id = $2",
		workspaceID, accountID), &r)
}

// memberData serializes a membership row in the exact shape of the
// client's UsersOnWorkspace model. teamIds must be an array (never null)
// and settings an object; role maps owner/admin to the client's ADMIN.
func (a *API) memberData(r memberRow) map[string]any {
	teamIDs := r.TeamIDs
	if teamIDs == nil {
		teamIDs = []string{}
	}
	return map[string]any{
		"id":          r.ID,
		"createdAt":   r.CreatedAt.Format(iso),
		"updatedAt":   r.UpdatedAt.Format(iso),
		"role":        clientRole(r.Role),
		"status":      strings.ToUpper(r.Status),
		"userId":      r.AccountID,
		"workspaceId": r.WorkspaceID,
		"teamIds":     teamIDs,
		"settings":    map[string]any{},
	}
}

// emitMemberChangeTx claims a sync sequence, writes the outbox row for
// the membership inside the caller's transaction, and returns the record
// together with the freshly read membership row. Callers refresh the
// outbox payload with memberData (same shape as the team handlers).
//
// The membership is read once, after the caller's own mutation has
// committed into the same transaction, so the returned row is final.
func (a *API) emitMemberChangeTx(ctx context.Context, tx pgx.Tx, workspaceID, accountID string) (syncActionRecord, memberRow, error) {
	row, err := a.memberByAccountTx(ctx, tx, workspaceID, accountID)
	if err != nil {
		return syncActionRecord{}, memberRow{}, err
	}
	rec, err := a.emitChange(ctx, tx, workspaceID, "UsersOnWorkspaces", row.ID, "UPDATE", nil)
	if err != nil {
		return syncActionRecord{}, row, err
	}
	return rec, row, nil
}
