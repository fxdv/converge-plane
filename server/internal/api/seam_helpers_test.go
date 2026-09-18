// seam_helpers_test.go — the shared in-process fixtures for the handler
// seam tests (wave 2). A handler is a pure function of (principal,
// request, pool) once the pool is the in-memory fake, so it is driven
// with an httptest recorder and no network: every test in this file's
// family runs on a machine whose sockets are all it is owed.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"converge/internal/auth"
	"converge/internal/broadcast"
	"converge/internal/config"
)

// apiForTests assembles the API over an in-memory pool fake. The
// runtime is deliberately absent: wakeIssueOwner and the spend counters
// are nil-receiver safe, and the handlers under test must not depend on
// the runtime's goroutines to answer a request.
func apiForTests(t *testing.T, pool *fakePool) *API {
	t.Helper()
	return &API{
		pool:  pool,
		log:   discardLogger(),
		cfg:   config.Config{},
		bcast: broadcast.New(),
	}
}

// humanPrincipal is the default caller for the seam tests.
func humanPrincipal(id string) *Principal {
	return &Principal{AccountID: id, Email: id + "@example.com", Fullname: "Human", Kind: auth.AccountKindHuman}
}

// agentPrincipal is a swarm member (the pause-guard's other branch).
func agentPrincipal(id string) *Principal {
	return &Principal{AccountID: id, Email: id + "@converge.dev", Fullname: "agent", Kind: auth.AccountKindAgent}
}

// requestFor builds the in-process request: rawBody is the request body
// verbatim (a JSON string for the contract tests, or a malformed string
// for the decode-rejection cases); principal rides the context, exactly
// as the session middleware places it; urlParams is a flat
// name-value name-value list of chi route parameters.
func requestFor(t *testing.T, principal *Principal, method, path, rawBody string, urlParams ...string) *http.Request {
	t.Helper()
	if len(urlParams)%2 != 0 {
		t.Fatal("requestFor: urlParams must be name-value pairs")
	}
	req := httptest.NewRequest(method, path, strings.NewReader(rawBody))
	req.Header.Set("Content-Type", "application/json")
	if principal != nil {
		req = req.WithContext(WithPrincipal(req.Context(), principal))
	}
	if len(urlParams) > 0 {
		rc := chi.NewRouteContext()
		for i := 0; i < len(urlParams); i += 2 {
			rc.URLParams.Add(urlParams[i], urlParams[i+1])
			// chi populates r.PathValue after route matching; the direct
			// seam call skips the router, so set the standard value too
			// (the swarm/metrics/agent routes read it, not chi's params).
			req.SetPathValue(urlParams[i], urlParams[i+1])
		}
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
	}
	return req
}

// record runs the handler against a fresh recorder and returns it.
func record(t *testing.T, a *API, req *http.Request, h func(http.ResponseWriter, *http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// checkStatus asserts the response code and returns the recorder.
func checkStatus(t *testing.T, rec *httptest.ResponseRecorder, code int) *httptest.ResponseRecorder {
	t.Helper()
	if rec.Code != code {
		t.Fatalf("status = %d (body %s), want %d", rec.Code, rec.Body.String(), code)
	}
	return rec
}

// decodeBody unmarshals the recorder's JSON body into v.
func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
}

// errorMessage extracts the {"error": ...} message of an error response.
func errorMessage(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Error string `json:"error"`
	}
	decodeBody(t, rec, &e)
	return e.Error
}

// wantError asserts the status and the exact error message (the messages
// are the client-visible contract: the error screen prints them verbatim).
func wantError(t *testing.T, rec *httptest.ResponseRecorder, code int, msg string) {
	t.Helper()
	checkStatus(t, rec, code)
	if got := errorMessage(t, rec); got != msg {
		t.Fatalf("error = %q, want %q", got, msg)
	}
}

// txSQL returns every SQL statement the transaction issued — both the
// Exec and the QueryRow channels (an "insert … returning id" rides the
// QueryRow channel, a bare "update" the Exec channel; the seam contract
// is the statement and its args, not which channel pgx routed it on).
func txSQL(tx *fakeTx, frag string) []fakeExecCall {
	all := append(append([]fakeExecCall(nil), tx.execs...), tx.rows...)
	var out []fakeExecCall
	for _, e := range all {
		if frag == "" || strings.Contains(e.sql, frag) {
			out = append(out, e)
		}
	}
	return out
}

// hasSQL reports whether the transaction issued a statement containing
// frag — the positive form of the write contract.
func hasSQL(tx *fakeTx, frag string) bool {
	return len(txSQL(tx, frag)) > 0
}

// poolExecutedQuery reports whether the pool saw a QueryRow containing
// frag (the read-seam contract: the wake, the guards, the lookups).
func poolExecutedQuery(pool *fakePool, frag string) bool {
	for _, q := range pool.poolQueries() {
		if strings.Contains(q.sql, frag) {
			return true
		}
	}
	return false
}
