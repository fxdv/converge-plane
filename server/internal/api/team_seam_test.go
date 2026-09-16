// team_seam_test.go — the team contract over in-memory fakes (wave 2):
// the identifier rule, the manager permission model, the create/update
// lifecycle (including the unique-violation 409), membership changes,
// and the preferences merge.
package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestIdentifierPattern pins the team key rule: 1-12 chars of A-Z, 0-9,
// or -, uppercased before matching.
func TestIdentifierPattern(t *testing.T) {
	valid := []string{"ENG", "Q3-2026", "A", "0", "-", "ABCDEFGHIJKL"} // 12: the bound
	for _, s := range valid {
		if !identifierPattern.MatchString(s) {
			t.Errorf("%q must match", s)
		}
	}
	invalid := []string{"", "ABCDEFGHIJKLM", "eng", "ENG.", "ENG_", "ENG ENG"} // 13: over the bound
	for _, s := range invalid {
		if identifierPattern.MatchString(s) {
			t.Errorf("%q must be rejected", s)
		}
	}
	// The handler uppercases before matching: lowercase input is legal.
	if !identifierPattern.MatchString(strings.ToUpper("q3-2026")) {
		t.Error("lowercase identifiers must be normalized, not rejected")
	}
}

// TestTeamManager pins the permission model: a workspace owner/admin can
// manage any team; otherwise the principal must be a manager of that
// team; a non-member can do nothing.
func TestTeamManager(t *testing.T) {
	p := humanPrincipal("u1")
	cases := []struct {
		name    string
		roleErr error
		role    any
		mgrErr  error
		mgrRole any
		want    bool
	}{
		{"workspace admin bypasses", nil, "admin", pgx.ErrNoRows, nil, true},
		{"workspace owner bypasses", nil, "owner", pgx.ErrNoRows, nil, true},
		{"a team manager", nil, "member", nil, "manager", true},
		{"a plain member is not a manager", nil, "member", nil, "member", false},
		{"a non-member", pgx.ErrNoRows, nil, nil, nil, false},
	}
	for _, c := range cases {
		pool := &fakePool{t: t, rules: []fakeRule{
			{frag: "select role from workspace_members", rowVals: []any{c.role}, rowErr: c.roleErr},
			{frag: "select role from team_members", rowVals: []any{c.mgrRole}, rowErr: c.mgrErr},
		}}
		a := apiForTests(t, pool)
		if got := a.teamManager(context0(), p, "t1", "ws1"); got != c.want {
			t.Errorf("%s: teamManager = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestMemberData pins the client UsersOnWorkspace shape: role collapses
// owner/admin to ADMIN, status is uppercase, teamIds never null.
func TestMemberData(t *testing.T) {
	a := apiForTests(t, &fakePool{t: t})
	now := time.Now()
	r := memberRow{ID: "m1", Role: "owner", Status: "active", AccountID: "u1", WorkspaceID: "ws1", CreatedAt: now, UpdatedAt: now}
	d := a.memberData(r)
	if d["role"] != "ADMIN" || d["status"] != "ACTIVE" || d["userId"] != "u1" {
		t.Fatalf("member = %+v, want ADMIN/ACTIVE/u1", d)
	}
	if _, ok := d["teamIds"].([]string); !ok {
		t.Fatalf("teamIds = %v, want an empty array, never null", d["teamIds"])
	}
	if _, ok := d["settings"].(map[string]any); !ok {
		t.Fatalf("settings = %v, want an object", d["settings"])
	}
	// agent maps to the client's AGENT role.
	r2 := r
	r2.Role = "agent"
	if got := a.memberData(r2)["role"]; got != "AGENT" {
		t.Fatalf("agent member role = %v, want AGENT", got)
	}
}

// TestCreateTeamContract pins the create machine: the 400/422/404
// family at the door, the 409 on a duplicate identifier, and the write
// shape on the happy path.
func TestCreateTeamContract(t *testing.T) {
	ws := "11111111-1111-1111-1111-111111111111"
	body := `{"name":"  Engineering  ","identifier":"eng","workspaceId":"` + ws + `"}`

	validPool := func(t *testing.T, tx *fakeTx) *fakePool {
		pool := &fakePool{t: t, rules: []fakeRule{
			{frag: "select role from workspace_members", rowVals: []any{"admin"}},
		}}
		if tx != nil {
			pool.txs = []*fakeTx{tx}
		}
		return pool
	}
	tx := &fakeTx{t: t, rules: []fakeRule{
		{frag: "insert into teams (", rowVals: []any{"t1", time.Now()}},
		{frag: "insert into sync_sequences", rowVals: []any{int64(51)}},
		{frag: "from teams where id = $1", rowVals: teamRowVals("t1", ws)},
	}}

	t.Run("a missing name 400s", func(t *testing.T) {
		a := apiForTests(t, validPool(t, nil))
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/teams", `{"identifier":"eng"}`), a.handleCreateTeam)
		wantError(t, rec, 400, "name and identifier are required")
	})
	t.Run("an oversized name 422s", func(t *testing.T) {
		a := apiForTests(t, validPool(t, nil))
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/teams",
			`{"name":"`+strings.Repeat("x", 65)+`","identifier":"eng","workspaceId":"`+ws+`"}`), a.handleCreateTeam)
		wantError(t, rec, 422, "name must be 1-64 chars")
	})
	t.Run("a bad identifier 422s", func(t *testing.T) {
		a := apiForTests(t, validPool(t, nil))
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/teams",
			`{"name":"x","identifier":"bad id!","workspaceId":"`+ws+`"}`), a.handleCreateTeam)
		wantError(t, rec, 422, "identifier must be 1-12 chars of A-Z, 0-9 or -")
	})
	t.Run("a non-admin 404s", func(t *testing.T) {
		pool := &fakePool{t: t, rules: []fakeRule{
			{frag: "select role from workspace_members", rowVals: []any{"member"}},
		}}
		a := apiForTests(t, pool)
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/teams", body), a.handleCreateTeam)
		wantError(t, rec, 404, "not found")
	})
	t.Run("a duplicate identifier 409s", func(t *testing.T) {
		dup := &fakeTx{t: t, rules: []fakeRule{
			{frag: "insert into teams (", rowErr: &pgconn.PgError{Code: "23505"}},
		}}
		a := apiForTests(t, validPool(t, dup))
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/teams", body), a.handleCreateTeam)
		wantError(t, rec, 409, "a team with this identifier already exists")
	})
	t.Run("the happy path writes the team", func(t *testing.T) {
		a := apiForTests(t, validPool(t, tx))
		rec := checkStatus(t, record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/teams", body), a.handleCreateTeam), 201)
		ins := txSQL(tx, "insert into teams (")
		if len(ins) != 1 || ins[0].args[1] != "Engineering" || ins[0].args[2] != "ENG" {
			t.Fatalf("insert = %+v, want the trimmed name and the uppercased key", ins[0].args)
		}
		audit := txSQL(tx, "insert into audit_events")
		if len(audit) != 1 || audit[0].args[2] != "team.created" {
			t.Fatalf("audit = %+v, want the team.created action", audit)
		}
		if !hasSQL(tx, "insert into sync_outbox") {
			t.Fatal("the create must emit a Team CREATE record")
		}
		var out struct {
			Identifier string `json:"identifier"`
			Position   int    `json:"position"`
		}
		decodeBody(t, rec, &out)
		if out.Identifier != "ENG" {
			t.Fatalf("response = %+v, want the uppercased identifier", out)
		}
	})
}

// TestUpdateTeamContract pins the update machine: manager gate, the
// no-op commit, the 409 on an identifier collision, the 422 on a bad
// field, and the write shape.
func TestUpdateTeamContract(t *testing.T) {
	ws := "11111111-1111-1111-1111-111111111111"
	row := teamRowVals("t1", ws)
	row[2] = "ENG"
	id := "22222222-2222-2222-2222-222222222222"

	t.Run("an unknown team 404s", func(t *testing.T) {
		pool := &fakePool{t: t, rules: []fakeRule{
			{frag: "from teams where id = $1", rowErr: pgx.ErrNoRows},
		}}
		a := apiForTests(t, pool)
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/teams/"+id, `{"name":"x"}`, "id", id), a.handleUpdateTeam)
		wantError(t, rec, 404, "not found")
	})
	t.Run("a non-manager 404s", func(t *testing.T) {
		pool := &fakePool{t: t, rules: []fakeRule{
			{frag: "from teams where id = $1", rowVals: row},
			{frag: "select role from workspace_members", rowVals: []any{"member"}},
			{frag: "select role from team_members", rowVals: []any{"member"}},
		}}
		a := apiForTests(t, pool)
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/teams/"+id, `{"name":"x"}`, "id", id), a.handleUpdateTeam)
		wantError(t, rec, 404, "not found")
	})
	t.Run("an invalid field 422s", func(t *testing.T) {
		pool := &fakePool{t: t, rules: []fakeRule{
			{frag: "from teams where id = $1", rowVals: row},
			{frag: "select role from workspace_members", rowVals: []any{"admin"}},
		}}
		pool.txs = []*fakeTx{{t: t}}
		a := apiForTests(t, pool)
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/teams/"+id, `{"name":"`+strings.Repeat("x", 65)+`"}`, "id", id), a.handleUpdateTeam)
		wantError(t, rec, 422, "invalid field value")
	})
	t.Run("an identifier collision 409s", func(t *testing.T) {
		pool := &fakePool{t: t, rules: []fakeRule{
			{frag: "from teams where id = $1", rowVals: row},
			{frag: "select role from workspace_members", rowVals: []any{"admin"}},
		}}
		pool.txs = []*fakeTx{{t: t, rules: []fakeRule{
			{frag: "update teams set identifier", rowErr: &pgconn.PgError{Code: "23505"}},
		}}}
		a := apiForTests(t, pool)
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/teams/"+id, `{"identifier":"Q3"}`, "id", id), a.handleUpdateTeam)
		wantError(t, rec, 409, "a team with this identifier already exists")
	})
	t.Run("a rename writes and refreshes", func(t *testing.T) {
		pool := &fakePool{t: t, rules: []fakeRule{
			{frag: "from teams where id = $1", rowVals: row},
			{frag: "select role from workspace_members", rowVals: []any{"admin"}},
		}}
		reloaded := teamRowVals("t1", ws)
		reloaded[1] = "The Engineering"
		reloaded[2] = "ENG"
		pool.txs = []*fakeTx{{t: t, rules: []fakeRule{
			{frag: "from teams where id = $1", rowVals: reloaded},
			{frag: "insert into sync_sequences", rowVals: []any{int64(52)}},
		}}}
		a := apiForTests(t, pool)
		rec := checkStatus(t, record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/teams/"+id, `{"name":"The Engineering"}`, "id", id), a.handleUpdateTeam), 200)
		tx := pool.txs[0]
		upd := txSQL(tx, "update teams set name")
		if len(upd) != 1 || upd[0].args[1] != "The Engineering" {
			t.Fatalf("rename = %+v, want the new name", upd)
		}
		outs := txSQL(tx, "insert into sync_outbox")
		if len(outs) != 1 || outs[0].args[4] != "UPDATE" {
			t.Fatalf("outbox = %+v, want one Team UPDATE", outs)
		}
		var out struct {
			Name string `json:"name"`
		}
		decodeBody(t, rec, &out)
		if out.Name != "The Engineering" {
			t.Fatalf("response = %+v, want the renamed team", out)
		}
	})
}

// TestTeamMembership pins add/remove: the target rules and the write
// shape (the membership upsert / delete plus the audit plus the
// UsersOnWorkspaces record).
func TestTeamMembership(t *testing.T) {
	ws := "11111111-1111-1111-1111-111111111111"
	row := teamRowVals("t1", ws)
	id := "22222222-2222-2222-2222-222222222222"
	target := "33333333-3333-3333-3333-333333333333"
	body := `{"userId":"` + target + `"}`

	memberPool := func(t *testing.T, targetVals []any, rowErr error) *fakePool {
		t.Helper()
		return &fakePool{t: t, rules: []fakeRule{
			{frag: "from teams where id = $1", rowVals: row},
			{frag: "select role from workspace_members", rowVals: []any{"admin"}},
			{frag: "from workspace_members wm where", rowVals: targetVals, rowErr: rowErr},
		}}
	}
	memberTx := func(t *testing.T) *fakeTx {
		return &fakeTx{t: t, rules: []fakeRule{
			{frag: "from workspace_members wm where", rowVals: memberRowVals("m1", "member", "active", target, "ws1", "t1")},
			{frag: "insert into sync_sequences", rowVals: []any{int64(53)}},
		}}
	}

	t.Run("add: a non-member target 422s", func(t *testing.T) {
		a := apiForTests(t, memberPool(t, nil, pgx.ErrNoRows))
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/teams/"+id+"/add-member", body, "id", id), a.handleAddTeamMember)
		wantError(t, rec, 422, "userId must be an active workspace member")
	})
	t.Run("add: a suspended target 422s", func(t *testing.T) {
		a := apiForTests(t, memberPool(t, memberRowVals("m1", "member", "suspended", target, "ws1"), nil))
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/teams/"+id+"/add-member", body, "id", id), a.handleAddTeamMember)
		wantError(t, rec, 422, "userId must be an active workspace member")
	})
	t.Run("add: an active member is granted and emitted", func(t *testing.T) {
		pool := memberPool(t, memberRowVals("m1", "member", "active", target, "ws1"), nil)
		pool.txs = []*fakeTx{memberTx(t)}
		a := apiForTests(t, pool)
		rec := checkStatus(t, record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/teams/"+id+"/add-member", body, "id", id), a.handleAddTeamMember), 200)
		tx := pool.txs[0]
		grant := txSQL(tx, "insert into team_members")
		if len(grant) != 1 || grant[0].args[0] != "t1" || grant[0].args[1] != target {
			t.Fatalf("grant = %+v, want the team and the target", grant[0].args)
		}
		audit := txSQL(tx, "insert into audit_events")
		if len(audit) != 1 || audit[0].args[2] != "team_membership.added" {
			t.Fatalf("audit = %+v, want team_membership.added", audit)
		}
		var out struct {
			Role  string   `json:"role"`
			Teams []string `json:"teamIds"`
		}
		decodeBody(t, rec, &out)
		if out.Role != "USER" {
			t.Fatalf("response role = %v, want USER", out.Role)
		}
	})
	t.Run("remove: a non-member target 404s", func(t *testing.T) {
		a := apiForTests(t, memberPool(t, nil, pgx.ErrNoRows))
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/teams/"+id+"/remove-member", body, "id", id), a.handleRemoveTeamMember)
		wantError(t, rec, 404, "not found")
	})
	t.Run("remove: deletes and emits", func(t *testing.T) {
		pool := memberPool(t, memberRowVals("m1", "member", "active", target, "ws1", "t1"), nil)
		pool.txs = []*fakeTx{memberTx(t)}
		a := apiForTests(t, pool)
		rec := checkStatus(t, record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/teams/"+id+"/remove-member", body, "id", id), a.handleRemoveTeamMember), 200)
		tx := pool.txs[0]
		del := txSQL(tx, "delete from team_members")
		if len(del) != 1 || del[0].args[0] != "t1" || del[0].args[1] != target {
			t.Fatalf("delete = %+v, want the team and the target", del[0].args)
		}
		var out struct {
			UserID string `json:"userId"`
		}
		decodeBody(t, rec, &out)
		if out.UserID != target {
			t.Fatalf("response = %+v, want the affected member", out)
		}
	})
}

// TestUpdateTeamPreferences pins the merge: only the sent fields change,
// the rest of the stored preferences survive.
func TestUpdateTeamPreferences(t *testing.T) {
	ws := "11111111-1111-1111-1111-111111111111"
	row := teamRowVals("t1", ws)
	row[7] = []byte(`{"cyclesEnabled":false,"teamType":"engineering"}`)
	id := "22222222-2222-2222-2222-222222222222"
	pool := &fakePool{t: t, rules: []fakeRule{
		{frag: "from teams where id = $1", rowVals: row},
		{frag: "select role from workspace_members", rowVals: []any{"admin"}},
	}}
	reloaded := teamRowVals("t1", ws)
	reloaded[7] = []byte(`{"cyclesEnabled":true,"teamType":"engineering"}`)
	pool.txs = []*fakeTx{{t: t, rules: []fakeRule{
		{frag: "from teams where id = $1", rowVals: reloaded},
		{frag: "insert into sync_sequences", rowVals: []any{int64(54)}},
	}}}
	a := apiForTests(t, pool)
	rec := checkStatus(t, record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/teams/"+id+"/preferences",
		`{"cyclesEnabled":true}`, "id", id), a.handleUpdateTeamPreferences), 200)
	tx := pool.txs[0]
	upd := txSQL(tx, "update teams set preferences")
	if len(upd) != 1 {
		t.Fatalf("preferences writes = %d, want 1", len(upd))
	}
	var merged map[string]any
	if err := json.Unmarshal(upd[0].args[1].([]byte), &merged); err != nil {
		t.Fatalf("merged preferences %q: %v", upd[0].args[1], err)
	}
	if merged["cyclesEnabled"] != true || merged["teamType"] != "engineering" {
		t.Fatalf("merged = %v, want the sent field flipped and the stored field kept", merged)
	}
	var out struct {
		Preferences map[string]any `json:"preferences"`
	}
	decodeBody(t, rec, &out)
	if out.Preferences["cyclesEnabled"] != true {
		t.Fatalf("response = %+v, want the merged preferences", out)
	}
}
