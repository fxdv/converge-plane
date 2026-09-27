package api

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCleanRunText(t *testing.T) {
	cases := []struct {
		name, in  string
		max       int
		multiline bool
		want      string
	}{
		{"trims", "  step one \n", 100, true, "step one"},
		{"keeps newlines and tabs when multiline", "a\n\tb", 100, true, "a\n\tb"},
		{"drops NUL and CR", "a\x00b\r\nc", 100, true, "ab\nc"},
		{"single line turns controls into spaces", "a\nb\tc", 100, false, "a b c"},
		{"drops bidi overrides", "safe\u202Etxt.exe\u2066", 100, false, "safetxt.exe"},
		{"repairs invalid UTF-8", "a\xffb", 100, false, "a\uFFFDb"},
		{"caps runes with an ellipsis", strings.Repeat("é", 10), 5, false, "éééé…"},
		{"blank is empty", " \x00\r ", 100, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := cleanRunText(c.in, c.max, c.multiline)
			if got != c.want {
				t.Fatalf("cleanRunText(%q) = %q, want %q", c.in, got, c.want)
			}
			if utf8.RuneCountInString(got) > c.max {
				t.Fatalf("result has %d runes, cap %d", utf8.RuneCountInString(got), c.max)
			}
		})
	}
}

func TestValidEvidenceURL(t *testing.T) {
	good := []string{
		"https://github.com/o/r/pull/12",
		"http://ci.internal:8080/job/42",
		"https://example.com/a?b=c#d",
	}
	bad := []string{
		"",
		"javascript:alert(1)",
		"data:text/html,<script>",
		"//github.com/o/r",
		"/relative/path",
		"https://user:pass@github.com/o/r",
		"https://github.com/o r",
		"https://github.com/\nX-Injected: 1",
		"ftp://files.example.com/x",
		"https://" + strings.Repeat("a", runEvidenceURLMax),
	}
	for _, u := range good {
		if !validEvidenceURL(u) {
			t.Errorf("validEvidenceURL(%q) = false, want true", u)
		}
	}
	for _, u := range bad {
		if validEvidenceURL(u) {
			t.Errorf("validEvidenceURL(%q) = true, want false", u)
		}
	}
}

func TestRunReportNormalize(t *testing.T) {
	ptr := func(s string) *string { return &s }
	i64 := func(n int64) *int64 { return &n }
	events := func(n int) []runEventIn {
		out := make([]runEventIn, n)
		for i := range out {
			out[i] = runEventIn{Kind: "step", Message: "x"}
		}
		return out
	}
	cases := []struct {
		name string
		rp   runReport
		want string
	}{
		{"empty is fine", runReport{}, ""},
		{"blank model", runReport{Model: ptr(" ")}, "model must be 1-100 characters"},
		{"negative total", runReport{Totals: &runTotals{CostMicros: i64(-1)}}, "totals must be between 0 and 1000000000000"},
		{"huge total", runReport{Totals: &runTotals{InputTokens: i64(runTotalMax + 1)}}, "totals must be between 0 and 1000000000000"},
		{"too many events", runReport{Events: events(runEventsPerReport + 1)}, "a report carries at most 50 events"},
		{"unknown event kind", runReport{Events: []runEventIn{{Kind: "shell", Message: "rm -rf"}}}, "event kind must be step, tool, note, or error"},
		{"blank event", runReport{Events: []runEventIn{{Kind: "note", Message: "\x00 "}}}, "event message must not be empty"},
		{"unknown evidence kind", runReport{Evidence: []runEvidence{{Kind: "issue", URL: "https://x.io"}}}, "evidence kind must be pull_request, commit, ci_run, deployment, or link"},
		{"bad outcome", runReport{Outcome: ptr("merged")}, "outcome must be done, failed, blocked, or partial"},
		{"good report", runReport{
			Model: ptr("claude"), Totals: &runTotals{InputTokens: i64(10), CostMicros: i64(0)},
			Events: events(runEventsPerReport), Outcome: ptr("done"),
			Evidence: []runEvidence{{Kind: "pull_request", URL: "https://github.com/o/r/pull/1", Title: ptr("  ")}},
		}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rp := c.rp
			if got := rp.normalize(); got != c.want {
				t.Fatalf("normalize() = %q, want %q", got, c.want)
			}
		})
	}

	rp := runReport{Summary: ptr(" \r "), Evidence: []runEvidence{{Kind: "link", URL: "https://x.io", Title: ptr(" \t ")}}}
	if msg := rp.normalize(); msg != "" || rp.Summary != nil || rp.Evidence[0].Title != nil {
		t.Fatalf("blank summary and title must become absent: %q %v %v", msg, rp.Summary, rp.Evidence[0].Title)
	}
	if !(&runReport{ClaimID: "x"}).empty() || (&runReport{Outcome: ptr("done")}).empty() {
		t.Fatal("empty() must ignore the claim id and see any report field")
	}
}

func TestRunDataShape(t *testing.T) {
	d := runData(runRow{ID: "r", IssueID: "i", AgentID: "a"})
	for _, k := range []string{"claimId", "endedAt", "endReason", "outcome", "summary", "model"} {
		v, ok := d[k]
		if !ok || v != nil {
			t.Fatalf("%s = %v (present %v), want an explicit null", k, v, ok)
		}
	}
	if ev, ok := d["evidence"].([]runEvidence); !ok || ev == nil || len(ev) != 0 {
		t.Fatalf("evidence = %#v, want an empty array, never null", d["evidence"])
	}
}
