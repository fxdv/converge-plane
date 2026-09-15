package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func getenv(key string) string { return os.Getenv(key) }

var wf = []workflowState{
	{ID: "s1", Name: "To Do", Position: 0, Category: "UNSTARTED"},
	{ID: "s2", Name: "In Progress", Position: 1, Category: "STARTED"},
	{ID: "s3", Name: "Done", Position: 2, Category: "COMPLETED"},
}

func TestNextState(t *testing.T) {
	c := &client{}
	if got := c.nextState(wf, "s1"); got == nil || got.ID != "s2" {
		t.Fatalf("nextState(s1) = %v, want s2", got)
	}
	// The terminal state has no successor.
	if got := c.nextState(wf, "s3"); got != nil {
		t.Fatalf("nextState(s3) = %v, want nil", got)
	}
	// An unknown current state is a no-op, not an error.
	if got := c.nextState(wf, "nope"); got != nil {
		t.Fatalf("nextState(nope) = %v, want nil", got)
	}
}

func TestIsTerminal(t *testing.T) {
	c := &client{}
	if !c.isTerminal(wf, "s3") {
		t.Fatal("COMPLETED not terminal")
	}
	list := append(append([]workflowState{}, wf...), workflowState{ID: "s4", Name: "Canceled", Position: 3, Category: "canceled"})
	if !c.isTerminal(list, "s4") {
		t.Fatal("canceled (lowercase category) not terminal")
	}
	if c.isTerminal(wf, "s2") {
		t.Fatal("STARTED reported terminal")
	}
	if c.isTerminal(wf, "unknown") {
		t.Fatal("unknown id reported terminal")
	}
}

func TestMustJSON(t *testing.T) {
	cases := map[string]string{
		`plain`:            `"plain"`,
		`quote " inside`:   `"quote \" inside"`,
		`back \ slash`:     `"back \\ slash"`,
		"new\nline":        `"new\nline"`,
		`"doc"` + "\nmore": `"\"doc\"\nmore"`,
	}
	for in, want := range cases {
		if got := mustJSON(in); got != want {
			t.Fatalf("mustJSON(%q) = %s, want %s", in, got, want)
		}
	}
}

// TestCommentBodyShape pins the ProseMirror document the demo agent posts:
// the v1 comment endpoint parses body as a JSON-encoded document string,
// and mustJSON is the only thing standing between arbitrary text and the
// JSON grammar.
func TestCommentBodyShape(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"c1"}`))
	}))
	defer srv.Close()

	c := &client{base: srv.URL, token: "conv_agent_x", http: &http.Client{}, ctx: context.Background()}
	err := c.comment("issue-123", `He said "stop"`)
	if err != nil {
		t.Fatalf("comment: %v", err)
	}
	if !strings.Contains(gotPath, "/api/v1/issue_comments?issueId=issue-123") {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer conv_agent_x" {
		t.Fatalf("auth = %q", gotAuth)
	}
	// body is a JSON object whose "body" field is itself a JSON document string.
	var outer struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal(gotBody, &outer); err != nil {
		t.Fatalf("outer body not JSON: %v (%s)", err, gotBody)
	}
	var doc struct {
		Type    string `json:"type"`
		Content []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(outer.Body), &doc); err != nil {
		t.Fatalf("inner document not JSON (injection?): %v — %s", err, outer.Body)
	}
	if doc.Type != "doc" || len(doc.Content) != 1 || doc.Content[0].Content[0].Text != `He said "stop"` {
		t.Fatalf("document = %+v", doc)
	}
}

func TestInList(t *testing.T) {
	if !inList([]string{"a", "b"}, "b") {
		t.Fatal("b not found")
	}
	if inList([]string{"a"}, "b") {
		t.Fatal("b found, should not be")
	}
}

func TestEnvOr(t *testing.T) {
	t.Setenv("X_TEST_ENVOR", "")
	if got := envOr("X_TEST_ENVOR", "def"); got != "def" {
		t.Fatalf("unset: %q", got)
	}
	t.Setenv("X_TEST_ENVOR", "val")
	if got := envOr("X_TEST_ENVOR", "def"); got != "val" {
		t.Fatalf("set: %q", got)
	}
}

func TestRawData(t *testing.T) {
	b := rawData(map[string]any{"a": float64(1), "b": "x"})
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("rawData output is not JSON: %v", err)
	}
	if m["b"] != "x" {
		t.Fatalf("m = %v", m)
	}
}
