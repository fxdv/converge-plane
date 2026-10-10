// issue_parent_db_test.go — the parent hierarchy stays a forest: an
// update may not make an issue its own parent or ancestor. Gated on
// CONVERGE_TEST_DATABASE_URL.
package api

import (
	"context"
	"testing"
)

func TestUpdateIssueParentCycle(t *testing.T) {
	f := newWorkFixture(t)
	// grand <- parent <- child; other sits in the sibling team.
	grand := f.issue(f.t1, f.todo, "")
	parent := f.issue(f.t1, f.todo, "")
	child := f.issue(f.t1, f.todo, "")
	other := f.issue(f.t2, f.todo2, "")
	f.exec(`update issues set parent_id = $2 where id = $1`, parent, grand)
	f.exec(`update issues set parent_id = $2 where id = $1`, child, parent)

	setParent := func(id, parentID string) int {
		t.Helper()
		body := `{"parentId":"` + parentID + `"}`
		return f.call(humanPrincipal(f.owner), (*API).handleUpdateIssue, "POST", "/api/v1/issues/"+id, body, "id", id).Code
	}
	parentOf := func(id string) string {
		t.Helper()
		var p *string
		if err := f.pool.QueryRow(context.Background(), `select parent_id::text from issues where id = $1`, id).Scan(&p); err != nil {
			t.Fatalf("read parent: %v", err)
		}
		return strval(p)
	}

	if code := setParent(grand, grand); code != 422 {
		t.Errorf("self-parent: status %d, want 422", code)
	}
	if code := setParent(grand, child); code != 422 {
		t.Errorf("grandchild as parent: status %d, want 422", code)
	}
	if code := setParent(parent, child); code != 422 {
		t.Errorf("child as parent: status %d, want 422", code)
	}
	if got := parentOf(grand); got != "" {
		t.Errorf("refused updates must not write: grand.parent = %q", got)
	}
	// Legal moves still land: across teams, and back to the root.
	if code := setParent(child, other); code != 200 {
		t.Errorf("re-parent across teams: status %d, want 200", code)
	}
	if code := setParent(other, child); code != 422 {
		t.Errorf("cross-team cycle: status %d, want 422", code)
	}
	if code := setParent(child, ""); code != 200 || parentOf(child) != "" {
		t.Errorf("clear parent: status %d, parent %q", code, parentOf(child))
	}
	if code := setParent(grand, child); code != 200 || parentOf(grand) != child {
		t.Errorf("former descendant, now detached: status %d, parent %q", code, parentOf(grand))
	}
}
