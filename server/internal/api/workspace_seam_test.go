// workspace_seam_test.go — the workspace administration contract over
// in-memory fakes (wave 2): the rename, the invitation machine (email
// batch, role mapping, lifecycle, the token-hash discipline), member
// suspension (session + token revocation), and the tenant resolution
// rules at the door.
package api

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// memberRowVals is one canned workspace_members row in memberColumns'
// order (8 values).
func memberRowVals(id, role, status, accountID, workspaceID string, teamIDs ...string) []any {
	now := time.Now()
	if teamIDs == nil {
		teamIDs = []string{}
	}
	return []any{id, role, status, accountID, workspaceID, now, now, teamIDs}
}

// inviteRowVals is one canned invitations row in inviteColumns' order.
// The lifecycle instants dereference to time.Time values: the scan
// targets are **time.Time, and a nil pointer in the value set is the
// fake's way of binding SQL NULL.
func inviteRowVals(id, workspaceID, email, role string, teamIDs []string, consumed, revoked *time.Time) []any {
	now := time.Now()
	conv := func(v *time.Time) any {
		if v == nil {
			return nil
		}
		return *v
	}
	return []any{id, workspaceID, email, role, teamIDs, now, conv(consumed), conv(revoked), now}
}

// ---------------------------------------------------------------------
// Pure seams.

// TestEmailPattern pins the invite address rule: exactly one @, a dot in
// the domain, no whitespace.
func TestEmailPattern(t *testing.T) {
	valid := []string{"a@b.co", "first.last+tag@sub.domain.org", "a_1@x-1.io"}
	invalid := []string{"a@b", "a b@c.d", "@b.co", "a@.co", "a@@b.co", "a@b.c d", " "}
	for _, e := range valid {
		if !emailPattern.MatchString(e) {
			t.Errorf("%q must be a valid address", e)
		}
	}
	for _, e := range invalid {
		if emailPattern.MatchString(e) {
			t.Errorf("%q must be rejected", e)
		}
	}
}

// TestInviteTokenHash pins the hash discipline: 256 bits of entropy,
// 64 hex chars, never the token itself.
func TestInviteTokenHash(t *testing.T) {
	h1, err := newInviteTokenHash()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(h1) {
		t.Fatalf("token hash = %q, want 64 lowercase hex chars", h1)
	}
	h2, _ := newInviteTokenHash()
	if h1 == h2 {
		t.Fatal("two token hashes must differ (random 256-bit entropy)")
	}
}

// TestInviteDataLifecycle pins the client Invite shape: the status
// derives from the lifecycle instants, updatedAt is the latest event,
// and an unset expiry falls back to the creation instant.
func TestInviteDataLifecycle(t *testing.T) {
	a := apiForTests(t, &fakePool{t: t})
	created := time.Now().Add(-time.Hour)
	row := inviteRow{ID: "inv1", WorkspaceID: "ws1", Email: "a@x.co", Role: "admin",
		TeamIDs: []string{"t1"}, CreatedAt: created}
	d := a.inviteData(row, "A")
	if d["status"] != "INVITED" || d["role"] != "ADMIN" {
		t.Fatalf("fresh invite = %v / %v, want INVITED/ADMIN", d["status"], d["role"])
	}
	if d["emailId"] != "a@x.co" || d["expiresAt"] != created.Format(iso) {
		t.Fatalf("invite = %+v, want the email and a creation-derived expiry", d)
	}
	if _, ok := d["teamIds"].([]string); !ok {
		t.Fatalf("teamIds = %v, want an array (never null)", d["teamIds"])
	}
	// Consumed → ACCEPTED, updatedAt advances.
	cons := row
	when := created.Add(time.Hour)
	cons.ConsumedAt = &when
	cons.RevokedAt = nil
	d2 := a.inviteData(cons, "A")
	if d2["status"] != "ACCEPTED" || d2["updatedAt"] != when.Format(iso) {
		t.Fatalf("consumed = %v / %v, want ACCEPTED and the consumption instant", d2["status"], d2["updatedAt"])
	}
	// Revoked → DECLINED.
	rev := row
	rev.RevokedAt = &when
	d3 := a.inviteData(rev, "A")
	if d3["status"] != "DECLINED" {
		t.Fatalf("revoked = %v, want DECLINED", d3["status"])
	}
}

// TestWorkspaceData pins the client Workspace shape: preferences is
// omitted (the client union has no undefined), actionsEnabled is false.
func TestWorkspaceData(t *testing.T) {
	a := apiForTests(t, &fakePool{t: t})
	now := time.Now()
	d := a.workspaceData(workspaceRow{ID: "ws1", Slug: "acme", Name: "Acme", CreatedAt: now, UpdatedAt: now})
	if d["slug"] != "acme" || d["actionsEnabled"] != false {
		t.Fatalf("workspace data = %+v", d)
	}
	if _, present := d["preferences"]; present {
		t.Fatal("the preferences key must be absent (client union: no undefined)")
	}
}

// ---------------------------------------------------------------------
// resolveWorkspaceForWrite and member lookups.

// TestResolveWorkspaceForWrite pins the write-target rule: an explicit
// id must be a workspace the principal actively belongs to; otherwise a
// principal with exactly one active workspace resolves to it, and
// anything ambiguous or unauthorized is false (404 upstream).
func TestResolveWorkspaceForWrite(t *testing.T) {
	p := humanPrincipal("u1")
	mk := func(roleErr, countErr error, count int, wsID any) *fakePool {
		t.Helper()
		rules := []fakeRule{
			{frag: "select role from workspace_members", rowVals: []any{"admin"}, rowErr: roleErr},
		}
		// The single-workspace lookup (the implicit path) returns the id
		// and the count over the account's active memberships.
		if countErr == nil && count >= 0 && wsID != nil {
			val := wsID
			rules = append(rules, fakeRule{frag: "count(*) over ()", rowVals: []any{val, count}})
		} else if countErr != nil {
			rules = append(rules, fakeRule{frag: "count(*) over ()", rowErr: countErr})
		}
		return &fakePool{t: t, rules: rules}
	}
	cases := []struct {
		name     string
		pool     *fakePool
		explicit string
		want     string
	}{
		{"explicit and a member", mk(nil, nil, 0, nil), "ws1", "ws1"},
		{"explicit, not a member", mk(pgx.ErrNoRows, nil, 0, nil), "ws2", ""},
		{"implicit, exactly one", mk(nil, nil, 1, any("ws1")), "", "ws1"},
		{"implicit, several is ambiguous", mk(nil, nil, 2, any("ws1")), "", ""},
		{"implicit, none", mk(nil, pgx.ErrNoRows, 0, nil), "", ""},
	}
	for _, c := range cases {
		ws, ok := apiForTests(t, c.pool).resolveWorkspaceForWrite(context0(), p, c.explicit)
		if (ok && ws != c.want) || (!ok && c.want != "") {
			t.Errorf("%s: got (%q, %v), want %q", c.name, ws, ok, c.want)
		}
	}
}

// context0 is the tests' request context.
func context0() context.Context { return context.Background() }

// ---------------------------------------------------------------------
// handleUpdateWorkspace (rename).

func TestUpdateWorkspaceContract(t *testing.T) {
	id := "11111111-1111-1111-1111-111111111111"
	body := `{"name":"  New Name  ","workspaceId":"` + id + `"}`
	wsRow := []any{id, "acme", "Acme", time.Now(), time.Now()}

	t.Run("an oversized name 422s", func(t *testing.T) {
		long := `{"name":"` + strings.Repeat("x", 101) + `","workspaceId":"` + id + `"}`
		a := apiForTests(t, &fakePool{t: t})
		wantError(t, record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/workspaces", long), a.handleUpdateWorkspace),
			422, "name must be 1-100 chars")
	})
	t.Run("a non-admin 404s", func(t *testing.T) {
		pool := &fakePool{t: t, rules: []fakeRule{
			{frag: "select role from workspace_members", rowVals: []any{"member"}},
			{frag: "from workspaces where id = $1", rowVals: wsRow},
		}}
		a := apiForTests(t, pool)
		wantError(t, record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/workspaces", body), a.handleUpdateWorkspace),
			404, "not found")
	})
	t.Run("an admin renames and refreshes", func(t *testing.T) {
		pool := &fakePool{t: t, rules: []fakeRule{
			{frag: "select role from workspace_members", rowVals: []any{"admin"}},
			{frag: "from workspaces where id = $1", rowVals: wsRow},
		}}
		now := time.Now()
		pool.txs = []*fakeTx{{t: t, rules: []fakeRule{
			{frag: "insert into sync_sequences", rowVals: []any{int64(31)}},
			{frag: "from workspaces where id = $1", rowVals: []any{id, "acme", "New Name", now, now}},
		}}}
		a := apiForTests(t, pool)
		rec := checkStatus(t, record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/workspaces", body), a.handleUpdateWorkspace), 200)
		tx := pool.txs[0]
		upd := txSQL(tx, "update workspaces set name")
		if len(upd) != 1 || upd[0].args[1] != "New Name" {
			t.Fatalf("rename = %+v, want the trimmed name", upd)
		}
		var out struct {
			Name string `json:"name"`
		}
		decodeBody(t, rec, &out)
		if out.Name != "New Name" {
			t.Fatalf("response = %+v, want the renamed workspace", out)
		}
	})
}

// ---------------------------------------------------------------------
// Invitation batch: the validation machine (everything before the tx).

// invitePool wires the pool reads of the invite handler's pre-tx phase.
func invitePool(t *testing.T, role string, teamCount int) *fakePool {
	t.Helper()
	return &fakePool{t: t, rules: []fakeRule{
		{frag: "select role from workspace_members", rowVals: []any{role}},
		{frag: "select name from workspaces where id = $1", rowVals: []any{"Acme"}},
		{frag: "id = any", rowVals: []any{teamCount}},
	}}
}

func TestInviteUsersValidation(t *testing.T) {
	id := "11111111-1111-1111-1111-111111111111"
	team := "22222222-2222-2222-2222-222222222222"
	ws := `"workspaceId":"` + id + `"`

	t.Run("a non-admin 404s", func(t *testing.T) {
		a := apiForTests(t, invitePool(t, "member", 1))
		body := `{"emailIds":"a@x.co",` + ws + `}`
		wantError(t, record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/workspaces/invite_users", body), a.handleInviteUsers),
			404, "not found")
	})
	t.Run("OWNER is not an invitable role", func(t *testing.T) {
		a := apiForTests(t, invitePool(t, "admin", 1))
		body := `{"emailIds":"a@x.co","role":"OWNER",` + ws + `}`
		wantError(t, record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/workspaces/invite_users", body), a.handleInviteUsers),
			422, "role must be ADMIN or USER")
	})
	t.Run("a non-uuid team 422s", func(t *testing.T) {
		a := apiForTests(t, invitePool(t, "admin", 1))
		body := `{"emailIds":"a@x.co","teamIds":["not-a-uuid"],` + ws + `}`
		wantError(t, record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/workspaces/invite_users", body), a.handleInviteUsers),
			422, "teamIds must be uuids")
	})
	t.Run("a team outside the workspace 422s", func(t *testing.T) {
		a := apiForTests(t, invitePool(t, "admin", 0)) // the count must equal the requested set
		body := `{"emailIds":"a@x.co","teamIds":["` + team + `"],` + ws + `}`
		wantError(t, record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/workspaces/invite_users", body), a.handleInviteUsers),
			422, "teamIds must belong to the workspace")
	})
	t.Run("a malformed address 422s with the offender", func(t *testing.T) {
		a := apiForTests(t, invitePool(t, "admin", 1))
		body := `{"emailIds":"a b@c.co",` + ws + `}`
		wantError(t, record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/workspaces/invite_users", body), a.handleInviteUsers),
			422, "invalid email address: a b@c.co")
	})
	t.Run("all-malformed input 422s", func(t *testing.T) {
		a := apiForTests(t, invitePool(t, "admin", 1))
		body := `{"emailIds":" ,,,",` + ws + `}`
		wantError(t, record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/workspaces/invite_users", body), a.handleInviteUsers),
			422, "at least one valid email is required")
	})
	t.Run("a batch above 50 422s", func(t *testing.T) {
		var emails []string
		for i := 1; i <= 51; i++ {
			emails = append(emails, "user"+strconv.Itoa(i)+"@x.co")
		}
		a := apiForTests(t, invitePool(t, "admin", 1))
		body := `{"emailIds":"` + strings.Join(emails, ",") + `",` + ws + `}`
		wantError(t, record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/workspaces/invite_users", body), a.handleInviteUsers),
			422, "at most 50 emails per invite batch")
	})
}

// ---------------------------------------------------------------------
// The invitation lifecycle, direct over the fake tx.

func TestEnsureAccountTx(t *testing.T) {
	a := apiForTests(t, &fakePool{t: t})
	ctx := context0()

	t.Run("a new account materializes", func(t *testing.T) {
		tx := &fakeTx{t: t, rules: []fakeRule{
			{frag: "insert into accounts (email, name)", rowVals: []any{"new1"}},
		}}
		id, err := a.ensureAccountTx(ctx, tx, "eve@x.co", "eve")
		if err != nil || id != "new1" {
			t.Fatalf("id = %q err = %v, want the new account", id, err)
		}
	})
	t.Run("an existing account resolves (no-rows conflict)", func(t *testing.T) {
		tx := &fakeTx{t: t, rules: []fakeRule{
			{frag: "insert into accounts (email, name)", rowErr: pgx.ErrNoRows},
			{frag: "select id, kind from accounts where email", rowVals: []any{"old1", "human"}},
		}}
		id, err := a.ensureAccountTx(ctx, tx, "eve@x.co", "eve")
		if err != nil || id != "old1" {
			t.Fatalf("id = %q err = %v, want the existing account", id, err)
		}
	})
	t.Run("an agent identity is refused", func(t *testing.T) {
		tx := &fakeTx{t: t, rules: []fakeRule{
			{frag: "insert into accounts (email, name)", rowErr: pgx.ErrNoRows},
			{frag: "select id, kind from accounts where email", rowVals: []any{"ag1", "agent"}},
		}}
		_, err := a.ensureAccountTx(ctx, tx, "vega@converge.dev", "vega")
		if !errors.Is(err, errInviteAgent) {
			t.Fatalf("err = %v, want errInviteAgent (agents join through the agent API)", err)
		}
	})
}

// TestInviteMembershipTx pins the membership lifecycle: a stranger gets
// an invited seat, an active member is refused (a role decision, not an
// invitation), a suspended member is refused, and a prior invitee gets
// a refreshed role.
func TestInviteMembershipTx(t *testing.T) {
	a := apiForTests(t, &fakePool{t: t})
	ctx := context0()
	teams := []string{"t1"}

	memberRule := func(err error, status string) fakeRule {
		r := fakeRule{frag: "select id, status from workspace_members", rowErr: err}
		if err == nil {
			r.rowVals = []any{"m1", status}
		}
		return r
	}
	t.Run("a stranger is seated as invited", func(t *testing.T) {
		tx := &fakeTx{t: t, rules: []fakeRule{memberRule(pgx.ErrNoRows, "")}}
		status, err := a.inviteMembershipTx(ctx, tx, "ws1", "u9", "member", teams)
		if err != nil || status != "INVITED" {
			t.Fatalf("status = %q err = %v, want INVITED", status, err)
		}
		if !hasSQL(tx, "insert into workspace_members") || !hasSQL(tx, "insert into team_members") {
			t.Fatalf("writes = %v, want the seat and the team grant", tx.execs)
		}
	})
	t.Run("an active member is refused", func(t *testing.T) {
		tx := &fakeTx{t: t, rules: []fakeRule{memberRule(nil, "active")}}
		if _, err := a.inviteMembershipTx(ctx, tx, "ws1", "u2", "member", nil); !errors.Is(err, errAlreadyMember) {
			t.Fatalf("err = %v, want errAlreadyMember", err)
		}
		if hasSQL(tx, "insert into workspace_members") {
			t.Fatal("an active member must not be re-seated")
		}
	})
	t.Run("a suspended member is refused", func(t *testing.T) {
		tx := &fakeTx{t: t, rules: []fakeRule{memberRule(nil, "suspended")}}
		if _, err := a.inviteMembershipTx(ctx, tx, "ws1", "u3", "member", nil); !errors.Is(err, errMemberSuspended) {
			t.Fatalf("err = %v, want errMemberSuspended", err)
		}
	})
	t.Run("a prior invitee gets a refreshed role", func(t *testing.T) {
		tx := &fakeTx{t: t, rules: []fakeRule{memberRule(nil, "invited")}}
		status, err := a.inviteMembershipTx(ctx, tx, "ws1", "u4", "admin", teams)
		if err != nil || status != "INVITED" {
			t.Fatalf("status = %q err = %v, want INVITED", status, err)
		}
		upd := txSQL(tx, "update workspace_members set role")
		if len(upd) != 1 || upd[0].args[1] != "admin" {
			t.Fatalf("role refresh = %+v, want admin", upd)
		}
	})
}

// ---------------------------------------------------------------------
// handleSuspendMember.

// suspendPool wires the pool reads of the suspend handler.
func suspendPool(t *testing.T, role string, targetVals []any) *fakePool {
	t.Helper()
	return &fakePool{t: t, rules: []fakeRule{
		{frag: "select role from workspace_members", rowVals: []any{role}},
		{frag: "from workspace_members wm where", rowVals: targetVals},
	}}
}

func TestSuspendMemberContract(t *testing.T) {
	id := "33333333-3333-3333-3333-333333333333"
	body := `{"userId":"` + id + `","workspaceId":"ws1"}`
	suspended := memberRowVals("m1", "member", "suspended", id, "ws1")
	loaded := memberRowVals("m1", "member", "active", id, "ws1", "t1")

	// suspendTx's reload rule carries the POST-change membership: the
	// fake has no state, so each subtest scripts the row the re-read
	// must return (the response is built from it).
	suspendTx := func(t *testing.T, reloaded []any) *fakeTx {
		t.Helper()
		return &fakeTx{t: t, rules: []fakeRule{
			{frag: "from workspace_members wm where", rowVals: reloaded},
			{frag: "insert into sync_sequences", rowVals: []any{int64(32)}},
		}}
	}

	t.Run("a non-admin 404s", func(t *testing.T) {
		a := apiForTests(t, suspendPool(t, "member", loaded))
		wantError(t, record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/workspaces/suspend", body), a.handleSuspendMember),
			404, "not found")
	})
	t.Run("the owner cannot be suspended", func(t *testing.T) {
		target := memberRowVals("m9", "owner", "active", id, "ws1")
		a := apiForTests(t, suspendPool(t, "admin", target))
		wantError(t, record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/workspaces/suspend", body), a.handleSuspendMember),
			422, "the workspace owner cannot be suspended")
	})
	t.Run("an invitee is not a member", func(t *testing.T) {
		target := memberRowVals("m2", "member", "invited", id, "ws1")
		a := apiForTests(t, suspendPool(t, "admin", target))
		wantError(t, record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/workspaces/suspend", body), a.handleSuspendMember),
			422, "member is invited, not a member")
	})
	t.Run("suspending revokes sessions and tokens", func(t *testing.T) {
		pool := suspendPool(t, "admin", loaded)
		pool.txs = []*fakeTx{suspendTx(t, suspended)}
		a := apiForTests(t, pool)
		rec := checkStatus(t, record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/workspaces/suspend", body), a.handleSuspendMember), 200)
		tx := pool.txs[0]
		upd := txSQL(tx, "update workspace_members set status")
		if len(upd) != 1 || upd[0].args[1] != "suspended" {
			t.Fatalf("status flip = %+v, want suspended", upd)
		}
		if !hasSQL(tx, "update sessions set revoked_at") {
			t.Fatal("suspension must revoke the member's sessions (spec 07)")
		}
		if !hasSQL(tx, "update api_tokens set revoked_at") {
			t.Fatal("suspension must revoke agent tokens (a suspended agent 401s)")
		}
		audit := txSQL(tx, "insert into audit_events")
		if len(audit) != 1 || audit[0].args[2] != "member.suspended" {
			t.Fatalf("audit = %+v, want the member.suspended action", audit)
		}
		var out struct {
			Status string `json:"status"`
			Role   string `json:"role"`
		}
		decodeBody(t, rec, &out)
		if out.Status != "SUSPENDED" || out.Role != "USER" {
			t.Fatalf("response = %+v, want SUSPENDED/USER", out)
		}
	})
	t.Run("reactivating never revokes", func(t *testing.T) {
		pool := suspendPool(t, "admin", suspended)
		pool.txs = []*fakeTx{suspendTx(t, loaded)} // the re-read sees the member active again
		a := apiForTests(t, pool)
		rec := checkStatus(t, record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/workspaces/suspend", body), a.handleSuspendMember), 200)
		tx := pool.txs[0]
		upd := txSQL(tx, "update workspace_members set status")
		if upd[0].args[1] != "active" {
			t.Fatalf("status flip = %v, want active", upd[0].args[1])
		}
		if hasSQL(tx, "update sessions") || hasSQL(tx, "update api_tokens") {
			t.Fatal("reactivation must not revoke credentials (the admin rotates instead)")
		}
		var out struct {
			Status string `json:"status"`
		}
		decodeBody(t, rec, &out)
		if out.Status != "ACTIVE" {
			t.Fatalf("response = %+v, want ACTIVE", out)
		}
	})
}

// ---------------------------------------------------------------------
// handleInviteAction (the invitee's side).

func TestInviteActionContract(t *testing.T) {
	inviteID := "44444444-4444-4444-4444-444444444444"
	email := "eve@x.co"
	p := humanPrincipal("u7")
	p.Email = email
	body := `{"inviteId":"` + inviteID + `","accept":true}`

	fresh := inviteRowVals(inviteID, "ws1", email, "member", []string{"t1"}, nil, nil)

	t.Run("a processed invite acknowledges idempotently", func(t *testing.T) {
		when := time.Now()
		row := inviteRowVals(inviteID, "ws1", email, "member", []string{"t1"}, &when, nil) // consumed
		pool := &fakePool{t: t, rules: []fakeRule{
			{frag: "from invitations i where", rowVals: row},
		}}
		a := apiForTests(t, pool)
		rec := checkStatus(t, record(t, a, requestFor(t, p, "POST", "http://x/api/v1/workspaces/invite_action", body), a.handleInviteAction), 200)
		var out struct {
			Status string `json:"status"`
		}
		decodeBody(t, rec, &out)
		if out.Status != "ACCEPTED" {
			t.Fatalf("response = %v, want ACCEPTED (the client reloads on it)", out.Status)
		}
		if pool.begins != 0 {
			t.Fatal("a processed invite must not open a transaction")
		}
	})
	t.Run("accepting materializes the membership", func(t *testing.T) {
		pool := &fakePool{t: t, rules: []fakeRule{
			{frag: "from invitations i where", rowVals: fresh},
		}}
		pool.txs = []*fakeTx{{t: t, rules: []fakeRule{
			{frag: "insert into workspace_members (workspace_id, account_id", rowVals: []any{"m7"}},
			{frag: "from workspace_members wm where", rowVals: memberRowVals("m7", "member", "active", "u7", "ws1", "t1")},
			{frag: "insert into sync_sequences", rowVals: []any{int64(33)}},
		}}}
		a := apiForTests(t, pool)
		rec := checkStatus(t, record(t, a, requestFor(t, p, "POST", "http://x/api/v1/workspaces/invite_action", body), a.handleInviteAction), 200)
		tx := pool.txs[0]
		if !hasSQL(tx, "update invitations set consumed_at") {
			t.Fatal("acceptance must consume the invite (one live invite per workspace+email)")
		}
		audit := txSQL(tx, "insert into audit_events")
		if len(audit) != 1 || audit[0].args[2] != "invite.accepted" {
			t.Fatalf("audit = %+v, want the invite.accepted action", audit)
		}
		var out struct {
			Status string `json:"status"`
		}
		decodeBody(t, rec, &out)
		if out.Status != "ACCEPTED" {
			t.Fatalf("response = %v, want ACCEPTED", out.Status)
		}
	})
	t.Run("declining revokes and clears the placeholder", func(t *testing.T) {
		pool := &fakePool{t: t, rules: []fakeRule{
			{frag: "from invitations i where", rowVals: fresh},
		}}
		pool.txs = []*fakeTx{{t: t, rules: []fakeRule{
			{frag: "status = 'invited'", rowVals: []any{"m7"}},
			{frag: "insert into sync_sequences", rowVals: []any{int64(34)}},
		}}}
		a := apiForTests(t, pool)
		decline := `{"inviteId":"` + inviteID + `","accept":false}`
		rec := checkStatus(t, record(t, a, requestFor(t, p, "POST", "http://x/api/v1/workspaces/invite_action", decline), a.handleInviteAction), 200)
		tx := pool.txs[0]
		if !hasSQL(tx, "delete from workspace_members") {
			t.Fatal("declining must remove the placeholder membership")
		}
		if !hasSQL(tx, "update invitations set revoked_at") {
			t.Fatal("declining must revoke the invite")
		}
		var out struct {
			Status string `json:"status"`
		}
		decodeBody(t, rec, &out)
		if out.Status != "DECLINED" {
			t.Fatalf("response = %v, want DECLINED", out.Status)
		}
	})
	t.Run("declining without a placeholder keeps the membership", func(t *testing.T) {
		pool := &fakePool{t: t, rules: []fakeRule{
			{frag: "from invitations i where", rowVals: fresh},
		}}
		pool.txs = []*fakeTx{{t: t, rules: []fakeRule{
			{frag: "status = 'invited'", rowErr: pgx.ErrNoRows},
			{frag: "insert into sync_sequences", rowVals: []any{int64(35)}},
		}}}
		a := apiForTests(t, pool)
		decline := `{"inviteId":"` + inviteID + `","accept":false}`
		rec := checkStatus(t, record(t, a, requestFor(t, p, "POST", "http://x/api/v1/workspaces/invite_action", decline), a.handleInviteAction), 200)
		tx := pool.txs[0]
		if hasSQL(tx, "delete from workspace_members") {
			t.Fatal("an active/suspended member who declines a stale invite keeps their membership")
		}
		if !hasSQL(tx, "update invitations set revoked_at") {
			t.Fatal("the invite itself is still revoked")
		}
		var out struct {
			Status string `json:"status"`
		}
		decodeBody(t, rec, &out)
		if out.Status != "DECLINED" {
			t.Fatalf("response = %v, want DECLINED", out.Status)
		}
	})
}
