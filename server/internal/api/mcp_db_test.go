package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"converge/internal/auth"
	"converge/internal/config"
)

// mcpFixture is a work fixture behind the real router, with the real
// session middleware resolving real agent tokens.
type mcpFixture struct {
	*workFixture
	router chi.Router
}

func newMCPFixture(t *testing.T) *mcpFixture {
	f := newWorkFixture(t)
	f.a.auth = auth.NewService(f.pool, config.Config{SessionSecret: "test-secret", AccessTokenTTL: time.Hour}, slog.New(slog.DiscardHandler), nil)
	f.a.limiter = newAccountRateLimiter(0, 0)
	r := chi.NewRouter()
	f.a.Mount(r)
	return &mcpFixture{workFixture: f, router: r}
}

// agentToken creates an external agent in team t1 and returns its id and
// token. scopes is a JSON array, or "" for a full-authority token.
func (f *mcpFixture) agentToken(scopes string) (string, string) {
	f.t.Helper()
	tok := `{"ttlHours":1}`
	if scopes != "" {
		tok = `{"scopes":` + scopes + `,"ttlHours":1}`
	}
	body := `{"name":"mcp-` + testUUID()[:8] + `","teamIds":["` + f.t1 + `"],"driver":"external","token":` + tok + `}`
	rec := f.call(humanPrincipal(f.owner), (*API).handleCreateAgent, "POST", "/x", body, "id", f.ws)
	checkStatus(f.t, rec, 201)
	var created agentResponse
	decodeBody(f.t, rec, &created)
	f.accounts = append(f.accounts, created.ID)
	return created.ID, created.Token
}

func (f *mcpFixture) post(token, body string, headers ...string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest("POST", "http://x/api/v1/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

type rpcReply struct {
	ID     json.RawMessage `json:"id"`
	Result struct {
		ProtocolVersion string `json:"protocolVersion"`
		IsError         bool   `json:"isError"`
		Content         []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"result"`
	Error *rpcError `json:"error"`
}

func (f *mcpFixture) rpc(token, body string, code int) rpcReply {
	f.t.Helper()
	rec := f.post(token, body)
	checkStatus(f.t, rec, code)
	var out rpcReply
	decodeBody(f.t, rec, &out)
	return out
}

func toolCall(name, args string) string {
	return `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
}

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// The official Go SDK client drives a claim from queue to release through
// the endpoint, with a least-privilege token.
func TestMCPInteropOfficialClient(t *testing.T) {
	f := newMCPFixture(t)
	srv := httptest.NewServer(f.router)
	defer srv.Close()
	agent, token := f.agentToken(`["work","issues:write","comments:write"]`)
	work := f.issue(f.t1, f.todo, agent)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "converge-test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   srv.URL + "/api/v1/mcp",
		HTTPClient: &http.Client{Transport: bearerTransport{token}},
		MaxRetries: -1,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()
	if v := cs.InitializeResult().ProtocolVersion; v != mcpProtocolVersions[0] {
		t.Fatalf("negotiated %q, want %q", v, mcpProtocolVersions[0])
	}
	if err := cs.Ping(ctx, nil); err != nil {
		t.Fatalf("ping: %v", err)
	}
	listed, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	var names []string
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	if len(names) != len(mcpTools) || !slices.Contains(names, "claim_issue") || !slices.Contains(names, "release_claim") {
		t.Fatalf("tools = %v", names)
	}

	call := func(name string, args map[string]any, wantError bool) map[string]any {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text := res.Content[0].(*mcp.TextContent).Text
		if res.IsError != wantError {
			t.Fatalf("%s isError=%v: %s", name, res.IsError, text)
		}
		var out map[string]any
		if !wantError {
			if err := json.Unmarshal([]byte(text), &out); err != nil {
				t.Fatalf("%s result is not JSON: %s", name, text)
			}
		} else {
			out = map[string]any{"text": text}
		}
		return out
	}

	queue := call("get_queue", nil, false)
	if !ids(asMaps(queue["assigned"]))[work] {
		t.Fatalf("queue = %v", queue)
	}
	claimed := call("claim_issue", map[string]any{"issueId": work}, false)
	claimID := claimed["claim"].(map[string]any)["id"].(string)
	version := int(claimed["packet"].(map[string]any)["issue"].(map[string]any)["version"].(float64))

	reported := call("report_progress", map[string]any{
		"issueId": work, "claimId": claimID, "model": "claude-x",
		"totals":   map[string]any{"inputTokens": 900, "outputTokens": 100, "costMicros": 3100},
		"events":   []any{map[string]any{"kind": "tool", "message": "go test ./..."}},
		"evidence": []any{map[string]any{"kind": "pull_request", "url": "https://github.com/o/r/pull/9"}},
	}, false)
	if run := reported["run"].(map[string]any); run["costMicros"].(float64) != 3100 || run["eventCount"].(float64) != 1 {
		t.Fatalf("run after report = %v", run)
	}

	updated := call("update_issue", map[string]any{"issueId": work, "version": version, "title": "Fixed via MCP"}, false)
	if updated["version"].(float64) <= float64(version) {
		t.Fatalf("update result = %v; want a newer version", updated)
	}
	stale := call("update_issue", map[string]any{"issueId": work, "version": version, "title": "stale"}, true)
	if !strings.Contains(stale["text"].(string), "HTTP 412") {
		t.Fatalf("stale write = %v; want the 412 surfaced as a tool error", stale)
	}
	call("add_comment", map[string]any{"issueId": work, "body": "PR is up."}, false)

	released := call("release_claim", map[string]any{
		"issueId": work, "claimId": claimID, "outcome": "done", "summary": "Fixed; PR 9.",
		"totals": map[string]any{"inputTokens": 1200, "outputTokens": 150, "costMicros": 4000},
	}, false)
	run := released["run"].(map[string]any)
	if released["released"] != true || run["outcome"] != "done" || run["endReason"] != "released" || run["costMicros"].(float64) != 4000 {
		t.Fatalf("release = %v", released)
	}
	var title string
	if err := f.pool.QueryRow(ctx, `select title from issues where id = $1`, work).Scan(&title); err != nil || title != "Fixed via MCP" {
		t.Fatalf("issue title = %q, %v", title, err)
	}
}

func asMaps(v any) []map[string]any {
	items, _ := v.([]any)
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// A tool call is held to the REST route's policy: the MCP layer adds no
// authority.
func TestMCPToolsKeepTokenScopes(t *testing.T) {
	f := newMCPFixture(t)
	agent, readOnly := f.agentToken(`["issues:read"]`)
	work := f.issue(f.t1, f.todo, agent)

	for _, body := range []string{
		toolCall("get_queue", `{}`),
		toolCall("claim_issue", `{"issueId":"`+work+`"}`),
		toolCall("add_comment", `{"issueId":"`+work+`","body":"hi"}`),
	} {
		out := f.rpc(readOnly, body, 200)
		if !out.Result.IsError || !strings.Contains(out.Result.Content[0].Text, "HTTP 403") {
			t.Fatalf("%s = %+v; want a 403 tool error", body, out.Result)
		}
	}
	if !strings.Contains(f.rpc(readOnly, toolCall("get_queue", `{}`), 200).Result.Content[0].Text, `"requiredScope":"work"`) {
		t.Fatal("the tool error must name the missing scope")
	}

	checkStatus(t, f.post("", `{"jsonrpc":"2.0","id":1,"method":"ping"}`), 401)
	f.exec(`update api_tokens set revoked_at = now() where account_id = $1`, agent)
	checkStatus(t, f.post(readOnly, `{"jsonrpc":"2.0","id":1,"method":"ping"}`), 401)
}

func TestMCPProtocolEdges(t *testing.T) {
	f := newMCPFixture(t)
	agent, token := f.agentToken("")
	work := f.issue(f.t1, f.todo, agent)

	t.Run("version negotiation", func(t *testing.T) {
		init := func(v string) string {
			return `{"jsonrpc":"2.0","id":"a","method":"initialize","params":{"protocolVersion":"` + v + `","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
		}
		if got := f.rpc(token, init("2025-03-26"), 200).Result.ProtocolVersion; got != "2025-03-26" {
			t.Fatalf("a supported version is echoed; got %q", got)
		}
		if got := f.rpc(token, init("2099-01-01"), 200).Result.ProtocolVersion; got != mcpProtocolVersions[0] {
			t.Fatalf("an unknown version is answered with the newest; got %q", got)
		}
		rec := f.post(token, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "MCP-Protocol-Version", "1999-01-01")
		checkStatus(t, rec, 400)
	})

	t.Run("messages", func(t *testing.T) {
		for _, body := range []string{
			`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
			`{"jsonrpc":"2.0","id":3,"result":{}}`,
		} {
			if rec := f.post(token, body); rec.Code != 202 || rec.Body.Len() != 0 {
				t.Fatalf("%s = %d %q; want 202 and no body", body, rec.Code, rec.Body.String())
			}
		}
		cases := []struct {
			body string
			http int
			code int
		}{
			{`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, 400, rpcInvalidRequest},
			{`{"jsonrpc":`, 400, rpcParseError},
			{`{"jsonrpc":"1.0","id":1,"method":"ping"}`, 400, rpcInvalidRequest},
			{`{"jsonrpc":"2.0","id":null,"method":"ping"}`, 400, rpcInvalidRequest},
			{`{"jsonrpc":"2.0","id":{},"method":"ping"}`, 400, rpcInvalidRequest},
			{`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`, 200, rpcMethodNotFound},
			{toolCall("delete_everything", `{}`), 200, rpcInvalidParams},
		}
		for _, c := range cases {
			out := f.rpc(token, c.body, c.http)
			if out.Error == nil || out.Error.Code != c.code {
				t.Fatalf("%s = %+v; want error %d", c.body, out.Error, c.code)
			}
		}
		if out := f.rpc(token, `{"jsonrpc":"2.0","id":"p","method":"ping"}`, 200); out.Error != nil || string(out.ID) != `"p"` {
			t.Fatalf("ping = %+v", out)
		}
	})

	t.Run("arguments", func(t *testing.T) {
		cases := []struct{ name, args, want string }{
			{"claim_issue", `{"issueId":"../../workspaces/` + f.ws + `/agents"}`, "issueId must be the issue's id"},
			{"claim_issue", `{"issueId":"` + work + `","ttl":60}`, "unknown argument(s) ttl; claim_issue takes: issueId, ttlSeconds"},
			{"update_issue", `{"issueId":"` + work + `","version":1}`, "name at least one field to change"},
			{"update_issue", `{"issueId":"` + work + `","version":1.5,"title":"x"}`, "version must be"},
			{"add_comment", `[1,2]`, "arguments must be a JSON object"},
		}
		for _, c := range cases {
			out := f.rpc(token, toolCall(c.name, c.args), 200)
			if !out.Result.IsError || !strings.Contains(out.Result.Content[0].Text, c.want) {
				t.Fatalf("%s %s = %+v; want a tool error containing %q", c.name, c.args, out.Result, c.want)
			}
		}
	})

	t.Run("plain-text descriptions stay text", func(t *testing.T) {
		var version int
		if err := f.pool.QueryRow(context.Background(), `select version from issues where id = $1`, work).Scan(&version); err != nil {
			t.Fatal(err)
		}
		args, _ := json.Marshal(map[string]any{"issueId": work, "version": version, "description": "42"})
		if out := f.rpc(token, toolCall("update_issue", string(args)), 200); out.Result.IsError {
			t.Fatalf("update = %+v", out.Result)
		}
		var kind string
		if err := f.pool.QueryRow(context.Background(), `select jsonb_typeof(description) from issues where id = $1`, work).Scan(&kind); err != nil || kind != "string" {
			t.Fatalf("description stored as %q (%v); want a JSON string", kind, err)
		}
	})

	t.Run("transport doors", func(t *testing.T) {
		_, narrow := f.agentToken(`["work"]`)
		for _, tok := range []string{token, narrow} {
			req := httptest.NewRequest("GET", "http://x/api/v1/mcp", nil)
			req.Header.Set("Authorization", "Bearer "+tok)
			rec := httptest.NewRecorder()
			f.router.ServeHTTP(rec, req)
			checkStatus(t, rec, http.StatusMethodNotAllowed)
		}
		rec := f.post(token, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "Origin", "https://evil.example")
		checkStatus(t, rec, 403)
		big := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"pad":"` + strings.Repeat("x", mcpMaxBody) + `"}}`
		checkStatus(t, f.post(token, big), http.StatusRequestEntityTooLarge)
	})

	t.Run("one call is charged once", func(t *testing.T) {
		// A fresh bucket admits its first request free, then burst more:
		// three calls fit, and would not if the inner requests paid too.
		f.a.limiter = newAccountRateLimiter(0.001, 2)
		defer func() { f.a.limiter = newAccountRateLimiter(0, 0) }()
		for i := range 3 {
			if out := f.rpc(token, toolCall("get_queue", `{}`), 200); out.Result.IsError {
				t.Fatalf("call %d = %+v; the inner request must not be charged", i+1, out.Result)
			}
		}
		checkStatus(t, f.post(token, toolCall("get_queue", `{}`)), http.StatusTooManyRequests)
	})
}
