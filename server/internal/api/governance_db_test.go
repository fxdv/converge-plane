// governance_db_test.go — an agent cannot mark Done without proof, a
// team spend cap stops a report, and a signed webhook is what an
// endpoint receives.
package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"converge/internal/auth"
)

func TestDoneNeedsEvidence(t *testing.T) {
	f := newWorkFixture(t)
	ctx := context.Background()
	issue := f.issue(f.t1, f.todo, "")
	agent := &Principal{AccountID: f.ext1, Kind: auth.AccountKindAgent, Fullname: "Agent"}
	human := &Principal{AccountID: f.owner, Kind: auth.AccountKindHuman, Fullname: "Owner"}

	patch := func(p *Principal, state string) error {
		t.Helper()
		tx, err := f.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		row, err := f.a.issueByIDTx(ctx, tx, issue)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = f.a.applyIssuePatchTx(ctx, tx, p, f.ws, row, issueRequest{StateID: &state})
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	if err := patch(agent, f.done); err != errDoneNeedsEvidence {
		t.Fatalf("agent close = %v, want the evidence refusal", err)
	}
	if err := patch(human, f.done); err != nil {
		t.Fatal(err)
	}
	if err := patch(human, f.todo); err != nil {
		t.Fatal(err)
	}
	f.exec(`insert into issue_pull_requests (workspace_id, issue_id, repo, number, state)
		values ($1, $2, 'acme/app', 7, 'merged')`, f.ws, issue)
	if err := patch(agent, f.done); err != nil {
		t.Fatalf("merged pull request should be proof: %v", err)
	}
	if err := patch(agent, f.todo); err != nil {
		t.Fatal(err)
	}
	rec := f.call(human, (*API).handleApproveDone, "POST", "/api/v1/issues/"+issue+"/done-approval", "", "id", issue)
	checkStatus(t, rec, http.StatusOK)
	if err := patch(agent, f.done); err != nil {
		t.Fatalf("human approval should be proof: %v", err)
	}
	rec = f.call(agent, (*API).handleApproveDone, "POST", "/api/v1/issues/"+issue+"/done-approval", "", "id", issue)
	checkStatus(t, rec, http.StatusUnprocessableEntity)
}

func TestSpendBudgetStopsReport(t *testing.T) {
	f := newWorkFixture(t)
	f.exec(`update teams set preferences = '{"spendBudgetMicros":1000}' where id = $1`, f.t1)
	issue := f.issue(f.t1, f.todo, f.ext1)
	work := f.claim(f.ext1, issue, http.StatusOK)
	f.report(f.ext1, issue, `{"claimId":"`+work.Claim.ID+`","totals":{"costMicros":1000}}`, http.StatusOK)
	f.report(f.ext1, issue, `{"claimId":"`+work.Claim.ID+`","totals":{"costMicros":1001}}`, http.StatusUnprocessableEntity)
	other := f.issue(f.t1, f.todo, "")
	f.claim(f.ext1, other, http.StatusUnprocessableEntity)
}

func TestSignedWebhookDelivery(t *testing.T) {
	f := newWorkFixture(t)
	f.a.cfg.DevMode = true
	f.a.webhooks = newWebhookDispatcher(f.a)
	var gotSig, gotEvent, gotTS string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEvent = r.Header.Get("X-Converge-Event")
		gotSig = r.Header.Get("X-Converge-Signature")
		gotTS = r.Header.Get("X-Converge-Timestamp")
		gotBody, _ = io.ReadAll(io.LimitReader(r.Body, 1<<16))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	rec := f.call(&Principal{AccountID: f.owner, Kind: auth.AccountKindHuman}, (*API).handleCreateWebhook,
		"POST", "/api/v1/workspaces/"+f.ws+"/webhooks", `{"url":"`+srv.URL+`/hook"}`, "id", f.ws)
	checkStatus(t, rec, http.StatusCreated)
	var created struct {
		Secret string `json:"secret"`
	}
	decodeBody(t, rec, &created)
	if created.Secret == "" || strings.Contains(rec.Body.String(), "secret") && len(created.Secret) < 32 {
		t.Fatal("expected a signing secret")
	}

	ctx := context.Background()
	issue := f.issue(f.t1, f.todo, "")
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	row, err := f.a.issueByIDTx(ctx, tx, issue)
	if err != nil {
		t.Fatal(err)
	}
	title := "renamed"
	if _, _, err := f.a.applyIssuePatchTx(ctx, tx, &Principal{AccountID: f.owner, Kind: auth.AccountKindHuman}, f.ws, row, issueRequest{Title: &title}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	f.a.webhooks.deliverDue(ctx)
	if gotEvent != "issue.updated" {
		t.Fatalf("event = %q body %s", gotEvent, gotBody)
	}
	ts, err := strconv.ParseInt(gotTS, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if gotSig != signWebhook(created.Secret, ts, gotBody) {
		t.Fatalf("signature does not match the secret and body")
	}
}

func TestWebhookURLRejectsPrivateAddresses(t *testing.T) {
	ctx := context.Background()
	if err := webhookURLAllowed(ctx, "https://127.0.0.1/hook", false); err == nil {
		t.Fatal("loopback https must be refused outside dev mode")
	}
	if err := webhookURLAllowed(ctx, "https://10.1.1.1/hook", false); err == nil {
		t.Fatal("private address must be refused")
	}
	if err := webhookURLAllowed(ctx, "http://127.0.0.1/hook", true); err != nil {
		t.Fatalf("dev loopback: %v", err)
	}
}

func TestTraceExportSignature(t *testing.T) {
	f := newWorkFixture(t)
	f.a.cfg.TraceSigningKey = "trace-signing-key-0123456789"
	issue := f.issue(f.t1, f.todo, f.ext1)
	work := f.claim(f.ext1, issue, http.StatusOK)
	f.report(f.ext1, issue, `{"claimId":"`+work.Claim.ID+`","summary":"looked","totals":{"costMicros":10}}`, http.StatusOK)
	rec := f.call(&Principal{AccountID: f.owner, Kind: auth.AccountKindHuman}, (*API).handleTraceExport,
		"GET", "/api/v1/workspaces/"+f.ws+"/trace", "", "id", f.ws)
	checkStatus(t, rec, http.StatusOK)
	if rec.Header().Get("X-Converge-Signature") != signTrace(f.a.cfg.TraceSigningKey, rec.Body.Bytes()) {
		t.Fatalf("signature does not match the body")
	}
	if !strings.Contains(rec.Body.String(), work.Claim.ID) {
		t.Fatalf("export = %s", rec.Body.String())
	}
}
