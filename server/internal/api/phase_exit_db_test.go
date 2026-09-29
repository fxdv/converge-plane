package api

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"converge/internal/auth"
)

func TestGreenCheckIsEvidence(t *testing.T) {
	f := newWorkFixture(t)
	ctx := context.Background()
	issue := f.issue(f.t1, f.todo, "")
	agent := &Principal{AccountID: f.ext1, Kind: auth.AccountKindAgent, Fullname: "Agent"}
	f.exec(`insert into issue_pull_requests (workspace_id, issue_id, repo, number, state, ci_state)
		values ($1, $2, 'acme/app', 9, 'open', 'success')`, f.ws, issue)
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	row, err := f.a.issueByIDTx(ctx, tx, issue)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = f.a.applyIssuePatchTx(ctx, tx, agent, f.ws, row, issueRequest{StateID: &f.done})
	if err != nil {
		t.Fatalf("a green check the server stored should be proof: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	blocked := f.issue(f.t1, f.todo, "")
	f.exec(`insert into issue_pull_requests (workspace_id, issue_id, repo, number, state, ci_state)
		values ($1, $2, 'acme/app', 10, 'open', 'failure')`, f.ws, blocked)
	tx, err = f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	row, err = f.a.issueByIDTx(ctx, tx, blocked)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = f.a.applyIssuePatchTx(ctx, tx, agent, f.ws, row, issueRequest{StateID: &f.done})
	if err != errDoneNeedsEvidence {
		t.Fatalf("a failed check = %v, want the evidence refusal", err)
	}
}

func TestPhase4Exit(t *testing.T) {
	f := newWorkFixture(t)
	ctx := context.Background()
	issue := f.issue(f.t1, f.todo, f.ext1)
	work := f.claim(f.ext1, issue, http.StatusOK)
	f.report(f.ext1, issue, `{"claimId":"`+work.Claim.ID+`","totals":{"costMicros":2500},"events":[{"kind":"note","message":"opened the pull request"}]}`, http.StatusOK)
	f.exec(`insert into issue_pull_requests (workspace_id, issue_id, repo, number, state)
		values ($1, $2, 'acme/app', 42, 'merged')`, f.ws, issue)

	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := f.a.completeOnMergeTx(ctx, tx, pullRow{WorkspaceID: f.ws, IssueID: issue}, pullRef{Repo: "acme/app", Number: 42}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	var category string
	var cost int
	var steps int
	if err := f.pool.QueryRow(ctx, `
		select s.category,
		       coalesce((select sum(cost_micros) from agent_runs where issue_id = $1), 0),
		       coalesce((select count(*) from agent_run_events e join agent_runs r on r.id = e.run_id where r.issue_id = $1), 0)
		from issues i
		join workflow_statuses s on s.id = i.status_id
		where i.id = $1`, issue).Scan(&category, &cost, &steps); err != nil {
		t.Fatal(err)
	}
	if category != "COMPLETED" || cost != 2500 || steps != 1 {
		t.Fatalf("exit = category %s cost %d steps %d, want COMPLETED 2500 1", category, cost, steps)
	}
}

func TestSharedRateBucket(t *testing.T) {
	f := newWorkFixture(t)
	f.a.limiter = newAccountRateLimiter(0.0001, 1)
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `delete from rate_buckets where account_id = $1`, f.owner)
	})
	allowed := func() bool {
		t.Helper()
		ok, used := f.a.sharedAllow(ctx, f.owner)
		if !used {
			t.Fatal("shared bucket was not read")
		}
		return ok
	}
	if !allowed() || !allowed() || allowed() {
		t.Fatal("burst of 1 should allow the first two spends and refuse the third")
	}
}

func TestOneRuntimeLeader(t *testing.T) {
	dbURL := os.Getenv("CONVERGE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("CONVERGE_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	a, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(ctx)
	b, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(ctx)
	var gotA, gotB bool
	if err := a.QueryRow(ctx, `select pg_try_advisory_lock($1::bigint)`, runtimeLeaderLock).Scan(&gotA); err != nil {
		t.Fatal(err)
	}
	if err := b.QueryRow(ctx, `select pg_try_advisory_lock($1::bigint)`, runtimeLeaderLock).Scan(&gotB); err != nil {
		t.Fatal(err)
	}
	if !gotA || gotB {
		t.Fatalf("locks = %v %v, want the first process only", gotA, gotB)
	}
	if err := a.Close(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := b.QueryRow(ctx, `select pg_try_advisory_lock($1::bigint)`, runtimeLeaderLock).Scan(&gotB); err != nil {
			t.Fatal(err)
		}
		if gotB || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !gotB {
		t.Fatal("the lock did not pass to the second process")
	}
}

func TestFanoutReloadsOutbox(t *testing.T) {
	f := newWorkFixture(t)
	f.a.instanceID = "this-process"
	f.exec(`insert into sync_outbox (workspace_id, sequence_id, model_name, action, data)
		values ($1, 7, 'Issue', 'UPDATE', '{"id":"x"}')`, f.ws)
	ch, cancel := f.a.bcast.Subscribe(f.ws)
	defer cancel()
	f.a.applyNotice("this-process " + f.ws + " 7")
	select {
	case ev := <-ch:
		t.Fatalf("own notice was published: %s", ev.Data)
	default:
	}
	f.a.applyNotice("other-process " + f.ws + " 7")
	select {
	case ev := <-ch:
		if ev.Seq != "7" {
			t.Fatalf("seq = %s", ev.Seq)
		}
	case <-time.After(time.Second):
		t.Fatal("the other process's notice did not reload the outbox row")
	}
}
