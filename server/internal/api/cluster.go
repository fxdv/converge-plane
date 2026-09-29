// One board across more than one API process.
// spec cs:arch:seams
//
// Realtime stays a hint: the outbox row is the record. After a local
// publish this process notifies the others, and each one reloads that
// row before publishing to its own subscribers. A notice from this
// process is ignored, so a record is not delivered twice here.
//
// The runtime, the claim sweeper, the GitHub poller, and outbound
// webhook delivery run on the process that holds the advisory lock.
// The others keep serving HTTP and the stream.

package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"converge/internal/broadcast"
)

const runtimeLeaderLock int64 = 480021

func newInstanceID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "local"
	}
	return hex.EncodeToString(b[:])
}

func (a *API) startServices(ctx context.Context) {
	a.svcMu.Lock()
	defer a.svcMu.Unlock()
	if a.servicesOn {
		return
	}
	a.servicesOn = true
	select {
	case <-a.runtime.done:
		a.runtime = newAgentRuntime(a)
	default:
	}
	a.runtime.Start(ctx)
	a.sweeperDone = make(chan struct{})
	go a.runClaimSweeper(ctx, a.sweeperDone)
	a.webhooks = newWebhookDispatcher(a)
	a.webhookDone = make(chan struct{})
	go a.webhooks.run(ctx, a.webhookDone)
	if len(a.cfg.GitHubRepos) > 0 {
		a.githubDone = make(chan struct{})
		go newGitHubPoller(a).run(ctx, a.githubDone)
		a.log.Info("tracking GitHub pull requests",
			"repos", strings.Join(a.cfg.GitHubRepos, ","),
			"authenticated", a.cfg.GitHubToken != "",
			"interval", a.cfg.GitHubPollInterval,
			"auto_done", a.cfg.GitHubAutoDone)
	}
}

func (a *API) stopServices() {
	a.svcMu.Lock()
	defer a.svcMu.Unlock()
	if !a.servicesOn {
		return
	}
	a.servicesOn = false
	a.runtime.Stop()
	if a.sweeperDone != nil {
		close(a.sweeperDone)
		a.sweeperDone = nil
	}
	if a.githubDone != nil {
		close(a.githubDone)
		a.githubDone = nil
	}
	if a.webhookDone != nil {
		close(a.webhookDone)
		a.webhookDone = nil
	}
}

// leadRuntime holds the swarm lock for this process. A pool that is not
// Postgres (the seam tests) starts the services immediately.
func (a *API) leadRuntime(ctx context.Context) {
	p, ok := a.pool.(*pgxpool.Pool)
	if !ok {
		a.startServices(ctx)
		return
	}
	for {
		if ctx.Err() != nil {
			return
		}
		conn, err := pgx.Connect(ctx, p.Config().ConnString())
		if err != nil {
			if !a.sleepLeader(ctx, 2*time.Second) {
				return
			}
			continue
		}
		var got bool
		err = conn.QueryRow(ctx, `select pg_try_advisory_lock($1::bigint)`, runtimeLeaderLock).Scan(&got)
		if err != nil || !got {
			_ = conn.Close(context.Background())
			if !a.sleepLeader(ctx, 2*time.Second) {
				return
			}
			continue
		}
		a.log.Info("runtime leader")
		a.startServices(ctx)
		a.holdLeader(ctx, conn)
		_ = conn.Close(context.Background())
		a.stopServices()
		if ctx.Err() != nil || !a.leaderOpen() {
			return
		}
	}
}

func (a *API) leaderOpen() bool {
	a.svcMu.Lock()
	defer a.svcMu.Unlock()
	return a.leaderStop != nil
}

func (a *API) sleepLeader(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-a.leaderStop:
		return false
	case <-t.C:
		return a.leaderOpen()
	}
}

func (a *API) holdLeader(ctx context.Context, conn *pgx.Conn) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.leaderStop:
			return
		case <-t.C:
			if err := conn.Ping(ctx); err != nil {
				a.log.Warn("runtime leader connection lost", "err", err)
				return
			}
		}
	}
}

func (a *API) notifyFanout(rec syncActionRecord) {
	if a.instanceID == "" || rec.WorkspaceID == "" || rec.SequenceID == "" {
		return
	}
	p, ok := a.pool.(*pgxpool.Pool)
	if !ok {
		return
	}
	payload := a.instanceID + " " + rec.WorkspaceID + " " + rec.SequenceID
	if len(payload) > 7000 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := p.Exec(ctx, `select pg_notify('converge_fanout', $1)`, payload); err != nil {
		a.log.Debug("fanout notify", "err", err)
	}
}

func (a *API) listenFanout(ctx context.Context) {
	p, ok := a.pool.(*pgxpool.Pool)
	if !ok {
		return
	}
	for a.leaderOpen() && ctx.Err() == nil {
		conn, err := pgx.Connect(ctx, p.Config().ConnString())
		if err != nil {
			if !a.sleepLeader(ctx, 2*time.Second) {
				return
			}
			continue
		}
		if _, err := conn.Exec(ctx, `listen converge_fanout`); err != nil {
			_ = conn.Close(context.Background())
			if !a.sleepLeader(ctx, 2*time.Second) {
				return
			}
			continue
		}
		if _, err := conn.Exec(ctx, `listen converge_session`); err != nil {
			_ = conn.Close(context.Background())
			continue
		}
		a.readNotices(ctx, conn)
		_ = conn.Close(context.Background())
	}
}

func (a *API) readNotices(ctx context.Context, conn *pgx.Conn) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.fanoutDone:
			return
		default:
		}
		wait, cancel := context.WithTimeout(ctx, 30*time.Second)
		n, err := conn.WaitForNotification(wait)
		cancel()
		if err != nil {
			if ctx.Err() != nil || !a.leaderOpen() {
				return
			}
			if wait.Err() != nil {
				continue
			}
			return
		}
		a.applyNotice(n.Payload)
	}
}

func (a *API) applyNotice(payload string) {
	parts := strings.Split(payload, " ")
	if len(parts) == 1 {
		if a.auth != nil && parts[0] != "" {
			a.auth.NoteRevoked(parts[0])
		}
		return
	}
	if len(parts) != 3 || parts[0] == "" || parts[0] == a.instanceID {
		return
	}
	a.publishRemote(parts[1], parts[2])
}

func (a *API) publishRemote(workspaceID, sequence string) {
	p, ok := a.pool.(*pgxpool.Pool)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var model, modelID, action string
	var data []byte
	var seq int64
	err := p.QueryRow(ctx, `
		select model_name, coalesce(model_id::text, ''), action, data, sequence_id
		from sync_outbox
		where workspace_id = $1 and sequence_id = $2::bigint`,
		workspaceID, sequence).Scan(&model, &modelID, &action, &data, &seq)
	if err != nil {
		return
	}
	rec := syncActionRecord{
		Data:        data,
		ModelName:   model,
		ModelID:     modelID,
		Action:      wireAction(action),
		WorkspaceID: workspaceID,
		SequenceID:  strconv.FormatInt(seq, 10),
	}
	if model == notifModel {
		var body map[string]any
		if json.Unmarshal(data, &body) == nil {
			rec.recipient, _ = body["recipientId"].(string)
		}
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return
	}
	a.bcast.Publish(workspaceID, broadcast.Event{Data: raw, Seq: rec.SequenceID, To: rec.recipient})
}
