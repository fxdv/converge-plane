// artifact_test.go — SWR-56: the swarm's artifact channel. The tests
// pin the seam from the model output to the sync feed: the fence (the
// document survives, the injection budget does not), the apply (the
// document persists, the issue does not move), and the wire shape (the
// client's IssueArtifact model, pinned on the server side — the two
// sides of the contract stay in step with the web's wire-contract
// suite).
package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// TestArtifactWireShape pins the client contract on the server side:
// the exact key set the web's IssueArtifact model validates, the plain
// text body (no rich-text envelope), and the present-null
// sourceMetadata (the client field is union(string, null) without
// undefined — a missing key would crash the store).
func TestArtifactWireShape(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	d := artifactData("a1", "Audit", "line1\nline2", "ag1", "iss1", now, now)

	wantKeys := []string{"id", "createdAt", "updatedAt", "userId", "issueId", "title", "body", "sourceMetadata"}
	if len(d) != len(wantKeys) {
		t.Fatalf("wire has %d keys, want exactly %d: %v", len(d), len(wantKeys), keys(d))
	}
	for _, k := range wantKeys {
		if _, ok := d[k]; !ok {
			t.Errorf("wire missing key %q (the client model requires it)", k)
		}
	}
	if d["sourceMetadata"] != nil {
		t.Errorf("sourceMetadata must be present-null, got %v", d["sourceMetadata"])
	}
	if d["body"] != "line1\nline2" {
		t.Errorf("the body must be plain text with newlines intact, got %q", d["body"])
	}
	if d["userId"] != "ag1" || d["issueId"] != "iss1" {
		t.Errorf("identity fields: got %v / %v", d["userId"], d["issueId"])
	}
	// The shape must round-trip through JSON the way the outbox stores
	// it (a marshal failure would break the realtime broadcast).
	if _, err := json.Marshal(d); err != nil {
		t.Fatalf("the wire must marshal: %v", err)
	}
}

func keys(d map[string]any) []string {
	out := make([]string, 0, len(d))
	for k := range d {
		out = append(out, k)
	}
	return out
}

// TestApplyActionArtifact pins the apply seam: the document row, the
// timeline breadcrumb (title, not body — the feed stays step-text
// density), and the outbox refresh carrying the full payload. The
// issue must not move: no update-issues write anywhere in the
// transaction. A document is a deliverable, not a transition.
func TestApplyActionArtifact(t *testing.T) {
	a := apiForTests(t, &fakePool{t: t})
	tx := &fakeTx{t: t, rules: []fakeRule{
		{frag: "insert into issue_artifacts", rowVals: []any{"art1"}},
		{frag: "from issue_artifacts where id", rowVals: []any{
			"Audit of the sync engine", "line1\nline2", "ag1", "iss1", time.Now(), time.Now(),
		}},
		{frag: "insert into issue_history", rowVals: []any{"h1"}},
		{frag: "created_at from issue_history", rowVals: []any{time.Now()}},
		{frag: "sync_sequences", rowVals: []any{int64(102)}},
		{frag: "insert into comments", rowVals: []any{"c1"}},
		{frag: "from comments cm where", rowVals: []any{
			"c1", `{"type":"doc"}`, "ag1", "iss1", nil, time.Now(), time.Now(),
		}},
	}}
	w := &agentWorker{workspaceID: "w1", agentID: "ag1", name: "vega"}
	row := issueRow{ID: "iss1", TeamID: "t1", Status: "active"}
	in := ActionInput{
		TeamID: "t1", WorkspaceID: "w1", IssueNumber: 7,
		ActorID: "ag1", ActorName: "vega",
	}

	recs, tripped, reason, err := a.applyActionTx(context.Background(), tx, w, row, in, Action{
		Kind: ActionArtifact, ArtifactTitle: "Audit of the sync engine",
		ArtifactBody: "line1\nline2", Comment: "posted the audit",
	})
	if err != nil {
		t.Fatalf("applyActionTx(artifact): %v", err)
	}
	if tripped {
		t.Errorf("a document must not trip the escalation (tripped=%v, reason=%q)", tripped, reason)
	}

	// No state change: the issue row is never written.
	for _, e := range tx.execs {
		if strings.Contains(e.sql, "update issues") {
			t.Fatalf("the artifact action moved the issue: %s", e.sql)
		}
	}
	// The document: issue, author, title, body in argument order.
	art := findCall(t, tx, "insert into issue_artifacts")
	if len(art.args) != 4 {
		t.Fatalf("artifact insert args = %v, want 4", art.args)
	}
	if art.args[0] != "iss1" || art.args[1] != "ag1" || art.args[2] != "Audit of the sync engine" || art.args[3] != "line1\nline2" {
		t.Errorf("artifact insert = %v", art.args)
	}
	// The breadcrumb: action=artifact, the summary names the document.
	hist := findCall(t, tx, "insert into issue_history")
	if hist.args[4] != "artifact" {
		t.Errorf("history action = %v, want artifact", hist.args[4])
	}
	if !strings.Contains(hist.args[8].(string), "Audit of the sync engine") {
		t.Errorf("history summary must name the document: %v", hist.args[8])
	}
	if strings.Contains(hist.args[8].(string), "line2") {
		t.Errorf("the breadcrumb must not carry the body (feed density): %v", hist.args[8])
	}
	// The broadcast record and the outbox refresh carry the full
	// document payload (JSON: the newlines travel as \n escapes,
	// sourceMetadata is present-null), byte-identical.
	raw := string(recs[0].Data)
	if !strings.Contains(raw, `line1\nline2`) || !strings.Contains(raw, `"sourceMetadata":null`) {
		t.Errorf("the artifact broadcast must carry the full document payload: %s", raw)
	}
	refreshed := 0
	for _, e := range tx.execs {
		if !strings.Contains(e.sql, "update sync_outbox") {
			continue
		}
		// The Exec carries the marshaled bytes (json.Marshal's []byte),
		// not a string: the assertion must type-assert to the real type.
		if got, ok := e.args[2].([]byte); ok && string(got) == raw {
			refreshed++
		}
	}
	if refreshed != 1 {
		t.Errorf("the outbox must be refreshed with the artifact payload exactly once (the pointing comment's refresh is separate), got %d", refreshed)
	}
	// The pointing comment (one short sentence) rides along.
	_ = findCall(t, tx, "insert into comments")
}

// TestApplyActionArtifactEmptyBody pins the guard: an artifact without
// a body is a comment wearing a costume — the cycle fails loud, the
// fallback (and the human) see why.
func TestApplyActionArtifactEmptyBody(t *testing.T) {
	a := apiForTests(t, &fakePool{t: t})
	tx := &fakeTx{t: t, rules: nil}
	w := &agentWorker{workspaceID: "w1", agentID: "ag1", name: "vega"}
	row := issueRow{ID: "iss1", TeamID: "t1", Status: "active"}
	_, _, _, err := a.applyActionTx(context.Background(), tx, w, row, ActionInput{TeamID: "t1"},
		Action{Kind: ActionArtifact, ArtifactTitle: "x"})
	if err == nil || !strings.Contains(err.Error(), "artifact without a body") {
		t.Fatalf("empty-body artifact must fail loud, got %v", err)
	}
}

// TestFenceDocument pins the document fence: newlines and tabs survive
// (a flattened document is meaningless), every other control
// character is stripped (the injection floor), and the cap is
// re-asserted (the storage/display budget).
func TestFenceDocument(t *testing.T) {
	// \r and the ESC/NUL map to spaces; \n and \t survive (the
	// document keeps its line structure).
	got := fenceDocument("line1\r\nline2\taudit\x1b\x00end")
	want := "line1 \nline2\taudit  end"
	if got != want {
		t.Errorf("fenceDocument:\n got %q\nwant %q", got, want)
	}
	// Whitespace alone is not a document: it fences to empty, and the
	// apply/parse guards reject it.
	if got := fenceDocument("  \t\n "); got != "" {
		t.Errorf("a whitespace document must fence to empty, got %q", got)
	}
	// Over the cap: truncated, marked, and inside the 8 KB budget —
	// the marker counts toward the cap, exactly what the table's
	// octet_length check enforces.
	long := strings.Repeat("x", artifactCap+100)
	got = fenceDocument(long)
	if len(got) != artifactCap {
		t.Errorf("a truncated document must fit the budget exactly, got %d bytes", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a truncated document must mark the truncation, got tail %q", got[len(got)-10:])
	}
	// A multibyte document must cut on a rune boundary: the stored
	// body stays valid UTF-8 (a text column rejects anything else).
	cjk := strings.Repeat("日", artifactCap/3+100)
	got = fenceDocument(cjk)
	if len(got) > artifactCap || !utf8.ValidString(got) || !strings.HasSuffix(got, "…") {
		t.Errorf("a multibyte truncation must stay valid and in budget: %d bytes, valid=%v, tail %q", len(got), utf8.ValidString(got), got[len(got)-10:])
	}
}

// TestCapArtifactTitle pins the title bound: the injection fence plus
// the 200-rune title cap, with the truncation marker.
func TestCapArtifactTitle(t *testing.T) {
	if got := capArtifactTitle(strings.Repeat("a", 250)); len([]rune(got)) != artifactTitleCap {
		t.Errorf("title over the cap must fit the cap exactly (the marker counts), got %d runes", len([]rune(got)))
	}
	if got := capArtifactTitle("  Audit \n"); got != "Audit" {
		t.Errorf("the title must be fenced and trimmed, got %q", got)
	}
}

// TestLLMParseArtifact pins the output half of the trust boundary for
// the new kind: a well-formed proposal maps to the runtime action, the
// body fences through the document fence (newlines kept), and an empty
// body is rejected with a diagnostic a prompt-iterator can act on.
func TestLLMParseArtifact(t *testing.T) {
	in := ActionInput{ActorName: "vega", TeamName: "Swarm"}
	content := `{"kind":"artifact","title":"Audit","body":"line1\nline2","comment":"posted it"}`
	act, err := parseLLMAction(in, content)
	if err != nil {
		t.Fatalf("a well-formed artifact must parse: %v", err)
	}
	if act.Kind != ActionArtifact || act.ArtifactTitle != "Audit" || act.ArtifactBody != "line1\nline2" {
		t.Errorf("action = %+v", act)
	}
	if act.Comment != "posted it" {
		t.Errorf("the pointing comment must survive: %q", act.Comment)
	}

	// A title-less document gets the neutral name, not an empty one.
	act, err = parseLLMAction(in, `{"kind":"artifact","body":"x"}`)
	if err != nil {
		t.Fatalf("artifact without a title: %v", err)
	}
	if act.ArtifactTitle != "Document" {
		t.Errorf("title-less document = %q, want the neutral name", act.ArtifactTitle)
	}

	// An empty body is not a document: it must be rejected (a finding
	// that short belongs in a comment or a handoff summary), not
	// persisted as an empty artifact.
	_, err = parseLLMAction(in, `{"kind":"artifact","title":"x","body":"  "}`)
	if err == nil || !strings.Contains(err.Error(), "body is empty") {
		t.Errorf("an empty body must be rejected with a diagnostic, got %v", err)
	}

	// The kind vocabulary is the reply contract: the default error
	// names all four, so a hallucinated fifth is diagnosable.
	_, err = parseLLMAction(in, `{"kind":"teleport"}`)
	if err == nil || !strings.Contains(err.Error(), "artifact") {
		t.Errorf("the unknown-kind error must list the artifact kind: %v", err)
	}
}
