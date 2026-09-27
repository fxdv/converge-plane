// notifications_db_test.go — the patch triggers against a real
// database: an assignment tells the new assignee, and a move to a
// terminal state tells the issue's people, both from one patch path.
// Gated on CONVERGE_TEST_DATABASE_URL.
package api

import (
	"context"
	"testing"

	"converge/internal/auth"
)

func TestIssuePatchNotifies(t *testing.T) {
	f := newWorkFixture(t)
	ctx := context.Background()
	peer := testUUID()
	f.exec(`insert into accounts (id, email, name, kind) values ($1, $2, 'Peer', 'human')`, peer, peer[:8]+"@work.test")
	f.accounts = append(f.accounts, peer)
	f.exec(`insert into workspace_members (workspace_id, account_id, role, status, joined_at) values ($1, $2, 'member', 'active', now())`,
		f.ws, peer)
	owner := &Principal{AccountID: f.owner, Fullname: "Owner", Kind: auth.AccountKindHuman}

	patch := func(issueID string, req issueRequest) {
		t.Helper()
		tx, err := f.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		row, err := f.a.issueByIDTx(ctx, tx, issueID)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := f.a.applyIssuePatchTx(ctx, tx, owner, f.ws, row, req); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	inbox := func(issueID string) []string {
		t.Helper()
		rows, err := f.pool.Query(ctx, `select type from notifications
			where issue_id = $1 and account_id = $2 order by created_at`, issueID, peer)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		}
		return out
	}

	issue := f.issue(f.t1, f.todo, "")
	patch(issue, issueRequest{AssigneeID: &peer})
	patch(issue, issueRequest{StateID: &f.done})
	if got := inbox(issue); len(got) != 2 || got[0] != notifAssigned || got[1] != notifClosed {
		t.Fatalf("peer inbox = %v, want [assigned closed]", got)
	}
	// Re-sending the current values is not a change and tells no one.
	patch(issue, issueRequest{AssigneeID: &peer, StateID: &f.done})
	if got := inbox(issue); len(got) != 2 {
		t.Fatalf("a no-op patch notified: %v", got)
	}
}
