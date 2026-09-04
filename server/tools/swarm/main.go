// Command swarm is the M6 demo agent: one process per agent, driven by
// the same v1 API the web client uses (one API token, one workspace).
//
// Each poll cycle the runner re-reads the sync feed, finds the issues
// assigned to it that are not in a terminal state, advances one of them
// to the next workflow state, and leaves a comment explaining the step.
// Launch several of these with different tokens to watch a swarm work
// in the web UI. It is a demonstration of the platform capability
// (agents as first-class actors); a real agent would run its own logic
// here.
//
// The D3 in-process runtime has superseded this as the default driver
// of the demo swarm (the seeded agents now act without external
// processes). This binary remains the reference for an external agent
// runtime: custom logic, its own cadence, the same v1 API.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Environment:
//
//	CONVERGE_API_URL        default http://localhost:3001
//	CONVERGE_WORKSPACE_ID   required
//	CONVERGE_AGENT_TOKEN    required (conv_agent_...)
//	CONVERGE_NAME           default "agent" (log prefix)
//	CONVERGE_POLL_SECONDS   default 8
const modelNames = "Issue,Workflow,UsersOnWorkspaces"

type client struct {
	base   string
	token  string
	name   string
	http   *http.Client
	ctx    context.Context
	cancel context.CancelFunc
}

type issue struct {
	ID        string  `json:"id"`
	Number    int     `json:"number"`
	Title     string  `json:"title"`
	TeamID    string  `json:"teamId"`
	StateID   string  `json:"stateId"`
	Assignee  string  `json:"assigneeId"`
	UpdatedAt string  `json:"updatedAt"`
	SortOrder float64 `json:"sortOrder"`
}

type workflowState struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Position int    `json:"position"`
	Category string `json:"category"`
	TeamID   string `json:"teamId"`
}

type member struct {
	Role    string   `json:"role"`
	TeamIDs []string `json:"teamIds"`
	UserID  string   `json:"userId"`
}

type syncResponse struct {
	SyncActions []struct {
		ModelName string         `json:"modelName"`
		Data      map[string]any `json:"data"`
	} `json:"syncActions"`
}

type userResponse struct {
	ID   string `json:"id"`
	Role string `json:"role"`
}

func main() {
	log.SetFlags(0)
	apiURL := envOr("CONVERGE_API_URL", "http://localhost:3001")
	workspaceID := os.Getenv("CONVERGE_WORKSPACE_ID")
	token := os.Getenv("CONVERGE_AGENT_TOKEN")
	name := envOr("CONVERGE_NAME", "agent")
	if workspaceID == "" || !strings.HasPrefix(token, "conv_agent_") {
		log.Fatal("CONVERGE_WORKSPACE_ID and CONVERGE_AGENT_TOKEN are required")
	}
	poll, err := strconv.Atoi(envOr("CONVERGE_POLL_SECONDS", "8"))
	if err != nil || poll < 1 {
		log.Fatal("CONVERGE_POLL_SECONDS must be a positive integer")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		cancel()
	}()

	c := &client{
		base:   strings.TrimRight(apiURL, "/"),
		token:  token,
		name:   name,
		http:   &http.Client{Timeout: 30 * time.Second},
		ctx:    ctx,
		cancel: cancel,
	}
	log.Printf("%s: running against %s workspace %s (poll %ds)", name, c.base, workspaceID[:8], poll)

	for {
		if err := c.workCycle(workspaceID); err != nil {
			log.Printf("%s: cycle error: %v", name, err)
		}
		select {
		case <-ctx.Done():
			log.Printf("%s: shutting down", name)
			return
		case <-time.After(time.Duration(poll) * time.Second):
		}
	}
}

func (c *client) workCycle(workspaceID string) error {
	self, err := c.get("/api/v1/users")
	if err != nil {
		return fmt.Errorf("self lookup: %w", err)
	}
	var me userResponse
	if err := json.Unmarshal(rawData(self), &me); err != nil {
		return fmt.Errorf("self parse: %w", err)
	}
	bootRaw, err := c.get("/api/v1/sync_actions/bootstrap?workspaceId=" + workspaceID + "&modelNames=" + modelNames)
	if err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	var boot syncResponse
	if err := json.Unmarshal(rawData(bootRaw), &boot); err != nil {
		return fmt.Errorf("bootstrap parse: %w", err)
	}

	var myTeams []string
	issues := []issue{}
	states := map[string][]workflowState{} // teamId -> ordered states
	for _, rec := range boot.SyncActions {
		switch rec.ModelName {
		case "UsersOnWorkspaces":
			var m member
			if err := json.Unmarshal(rawData(rec.Data), &m); err == nil && m.UserID == me.ID {
				myTeams = m.TeamIDs
			}
		case "Issue":
			var i issue
			if json.Unmarshal(rawData(rec.Data), &i) == nil {
				issues = append(issues, i)
			}
		case "Workflow":
			var s workflowState
			if json.Unmarshal(rawData(rec.Data), &s) == nil {
				states[s.TeamID] = append(states[s.TeamID], s)
			}
		}
	}
	for _, list := range states {
		sort.Slice(list, func(a, b int) bool { return list[a].Position < list[b].Position })
	}

	// Work queue: assigned to me, mine team, not terminal.
	var queue []issue
	for _, i := range issues {
		if i.Assignee != me.ID || !inList(myTeams, i.TeamID) {
			continue
		}
		if c.isTerminal(states[i.TeamID], i.StateID) {
			continue
		}
		queue = append(queue, i)
	}
	if len(queue) == 0 {
		return nil
	}
	sort.Slice(queue, func(a, b int) bool { return queue[a].SortOrder < queue[b].SortOrder })
	target := queue[0]
	next := c.nextState(states[target.TeamID], target.StateID)
	if next == nil {
		return nil
	}

	text := fmt.Sprintf("advancing to %s", strings.ToLower(next.Name))
	if err := c.comment(target.ID, fmt.Sprintf("%s: %s", c.name, text)); err != nil {
		return fmt.Errorf("comment on %s: %w", target.Title, err)
	}
	payload, _ := json.Marshal(map[string]any{"stateId": next.ID})
	if _, err := c.post("/api/v1/issues/"+target.ID, payload); err != nil {
		return fmt.Errorf("state change on %s: %w", target.Title, err)
	}
	log.Printf("%s: %s #%d -> %s", c.name, target.Title, target.Number, next.Name)
	return nil
}

// nextState returns the state after the current one in the team's ordered
// workflow; nil when the current state is last or unknown.
func (c *client) nextState(list []workflowState, currentID string) *workflowState {
	for i := range list {
		if list[i].ID == currentID && i+1 < len(list) {
			return &list[i+1]
		}
	}
	return nil
}

func (c *client) isTerminal(list []workflowState, id string) bool {
	for _, s := range list {
		if s.ID == id {
			cat := strings.ToUpper(s.Category)
			return cat == "COMPLETED" || cat == "CANCELED"
		}
	}
	return false
}

// comment posts a single-paragraph rich-text comment (the client's
// description/content document shape).
func (c *client) comment(issueID, text string) error {
	body := map[string]any{
		"body": fmt.Sprintf(
			`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":%s}]}]}`,
			mustJSON(text),
		),
	}
	payload, _ := json.Marshal(body)
	_, err := c.post("/api/v1/issue_comments?issueId="+issueID, payload)
	return err
}

func (c *client) get(path string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s: HTTP %d", http.MethodGet, path, resp.StatusCode)
	}
	var out map[string]any
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

func (c *client) post(path string, payload []byte) (map[string]any, error) {
	req, err := http.NewRequestWithContext(c.ctx, http.MethodPost, c.base+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s: HTTP %d", http.MethodPost, path, resp.StatusCode)
	}
	var out map[string]any
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

// rawData coerces the bootstrap data field (inlined JSON object) to a
// []byte for unmarshaling; tolerates a pre-parsed object too.
func rawData(data map[string]any) []byte {
	b, _ := json.Marshal(data)
	return b
}

func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func inList(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
