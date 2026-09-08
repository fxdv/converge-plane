package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestLLMRepro replays one production decision against the live model
// fleet with the production wire call, then runs the response through
// the real validator. Diagnostic for the floor storm of 2026-09-08
// 00:22-00:28: 7/7 decisions — 2.4-3k total tokens each, i.e. completions
// that burned the full max_tokens=2048 budget on thinking and returned
// an empty answer, so the validator rejected everything.
//
//	go test ./internal/api -run TestLLMRepro -v
//
// Requires the V100 tunnel up (localhost:8000). Skipped unless
// CONVERGE_LLM_REPRO=1 — it is a live-model probe, not a CI gate.
// Expectation after the enable_thinking fix: finish_reason=stop,
// reasoning_content ~0, a JSON proposal, parse ok=true.
func TestLLMRepro(t *testing.T) {
	if os.Getenv("CONVERGE_LLM_REPRO") == "" {
		t.Skip("set CONVERGE_LLM_REPRO=1 to replay against the live model")
	}

	states := []StateRef{
		{ID: "8f0752cc-8f1c-45b5-8ce1-156b56d76266", Name: "Backlog", Position: 0, Category: "BACKLOG"},
		{ID: "cfbe8c81-f25e-4c94-91d9-7feaca80a204", Name: "To Do", Position: 1, Category: "UNSTARTED"},
		{ID: "e82e9a18-cd3b-4080-b045-554df220bf85", Name: "In Progress", Position: 2, Category: "STARTED"},
		{ID: "4ca1432c-c1d4-4925-a445-502692f14ea6", Name: "In Review", Position: 3, Category: "STARTED"},
		{ID: "47e608fe-12fa-4ad8-9c9e-99c23d0865a4", Name: "Human Review", Position: 4, Category: "STARTED"},
		{ID: "e91f00f9-1233-43b8-b8ca-628193bfb7d2", Name: "Done", Position: 5, Category: "COMPLETED"},
		{ID: "73b48d71-9281-40c8-9494-a71934be5a6b", Name: "Canceled", Position: 6, Category: "CANCELED"},
	}
	in := ActionInput{
		IssueNumber:    14,
		TeamName:       "Swarm",
		TeamIdentifier: "SWR",
		TeamID:         "83a78be9-b222-4513-b1c6-c27437569895",
		WorkspaceID:    "7858469c-7a2d-4107-b171-a2dacb7a936a",
		ActorID:        "3c274f78-1c13-4d18-ade8-f9d89fd9dc0d",
		ActorName:      "Alpha",
		// In Review — the state the 00:28:38 decision saw (the floor
		// escalated it to Human Review as its own output).
		CurrentState: &states[3],
		States:       states,
		Fleet: []FleetAgent{
			{AccountID: "3c274f78-1c13-4d18-ade8-f9d89fd9dc0d", Name: "Alpha", OpenCount: 3},
			{AccountID: "bb27bc27-7f25-43f4-8c57-41afe8915a68", Name: "Bravo-1", OpenCount: 0},
			{AccountID: "31da402f-9d3d-4c1d-8c4e-71e31d26e45b", Name: "Bravo-2", OpenCount: 1},
			{AccountID: "d008543e-6a23-49ff-8878-ba341c4dac93", Name: "Bravo-3", OpenCount: 1},
			{AccountID: "b3001773-7f84-4a11-9f2e-6c5512e4ae952f", Name: "Bravo-4", OpenCount: 1},
		},
		RuntimeTopology: "foreman",
		Title:           "Swarm metrics: cost per completed issue, handoff graph, pause rate",
		Description:     "v1.0 scope (foreman-directed, D4): design + integration. The design work is delegated — see the subtask below. When the design lands, the foreman integrates it, codes the implementation (or escalates what a v1.0 swarm cannot do), and reports back here. The card completes when the feature is implemented and verified — the fence is satisfied in stages, not as one giant human-only card.",
		RecentComments:  "Tommy Admin: Foreman delegation — the swarm stops re-parking this card. The design work goes to Bravo-4 (subtask SWR-25, To Do). I hold this card in In Progress: when the design lands, I integrate it, code the implementation, and report back here.\nAlpha: Alpha: advancing issue #14 to In Review",
	}

	// The production request body, verbatim (decide()).
	body, err := json.Marshal(map[string]any{
		"model":                "qwen3.8-27b",
		"messages":             []map[string]string{{"role": "user", "content": llmDecisionPrompt(in)}},
		"max_tokens":           2048,
		"temperature":          0.2,
		"stream":               false,
		"chat_template_kwargs": map[string]any{"enable_thinking": false},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, "http://localhost:8000/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("content-type", "application/json")
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	res, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatalf("model call: %v", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var full struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &full); err != nil {
		head := raw
		if len(head) > 300 {
			head = head[:300]
		}
		t.Fatalf("response: status=%d %v: %s", res.StatusCode, err, head)
	}
	if len(full.Choices) == 0 {
		t.Fatalf("no choices: %s", string(raw[:min(len(raw), 300)]))
	}
	ch := full.Choices[0]
	t.Logf("prompt=%d chars | status=%d finish_reason=%q prompt_tokens=%d completion_tokens=%d",
		len(llmDecisionPrompt(in)), res.StatusCode, ch.FinishReason, full.Usage.PromptTokens, full.Usage.CompletionTokens)
	t.Logf("reasoning_content (%d chars): %s", len(ch.Message.ReasoningContent), fmt.Sprintf("%.300q", ch.Message.ReasoningContent))
	t.Logf("content (%d chars): %s", len(ch.Message.Content), fmt.Sprintf("%.500q", ch.Message.Content))
	if len(ch.Message.Content) > 500 {
		tail := ch.Message.Content
		if len(tail) > 300 {
			tail = tail[len(tail)-300:]
		}
		t.Logf("content tail: %q", tail)
	}
	action, err := parseLLMAction(in, ch.Message.Content)
	if err != nil {
		t.Logf("parseLLMAction: REJECTED — %v", err)
	} else {
		t.Logf("parseLLMAction: ACCEPTED — action=%+v", action)
	}
}
