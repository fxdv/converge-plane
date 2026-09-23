// notifications.go — the in-app inbox (docs/spec 12, the approved
// trigger matrix from SWR-24).
// spec cs:swarm:inbox
//
// One row is one recipient's one pending (or read) nudge: a pointer to
// the issue's own trace, which is the source of truth. Every trigger
// path — issue patch, comment, handoff, pause, escalation, mention —
// calls notifyIssueTx inside the same transaction as the mutation, so
// the nudge and the change are one atomic fact: a client that falls
// behind the feed loses nothing, and never sees a mutation without it.
//
// Delivery is in-app only, by design: invitations are the only thing
// the mail seam sends, and swarm-scale events (handoffs, pauses, agent
// activity) never mail — the mailbox is protected from swarm noise at
// the source, not by a client filter.
package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"converge/internal/auth"
)

// The trigger kinds (the matrix's vocabulary; the DB CHECK mirrors it).
const (
	notifAssigned   = "assigned"
	notifReassigned = "reassigned"
	notifHandoff    = "handoff"
	notifState      = "state"
	notifComment    = "comment"
	notifPause      = "pause"
	notifMention    = "mention"
	notifClosed     = "closed"
)

// notifRetention is the inbox's horizon (the design: 90 days, then
// purged). Notifications are disposable pointers; the issue's trace is
// the permanent record.
const notifRetention = "interval '90 days'"

// notifModel is the sync model name (the client's MODELS enum mirror).
const notifModel = "Notification"

// notification is the wire shape the client's MST model reads.
// recipientId is the delivery key: the plane's stream and delta are
// per-workspace, so every member receives every tenant notification and
// the client keeps only the rows addressed to its user.
type notification struct {
	ID          string  `json:"id"`
	WorkspaceID string  `json:"workspaceId"`
	IssueID     string  `json:"issueId"`
	IssueNumber int     `json:"issueNumber"`
	Type        string  `json:"type"`
	ActorID     *string `json:"actorId"`
	ActorName   *string `json:"actorName"`
	RecipientID string  `json:"recipientId"`
	CreatedAt   string  `json:"createdAt"`
	ReadAt      *string `json:"readAt"`
}

// dedupKey is the noise guard (the design's rule): (issue, type, actor)
// bucketed by a 24h epoch. A 20-comment thread collapses into one
// pending row (the newest event refreshes it in place); a new day starts
// a new row. The partial unique index makes the collapse race-free —
// concurrent events in one bucket upsert the same row.
func dedupKey(issueID, ntype, actorID string) string {
	return fmt.Sprintf("%s|%s|%s|%d", issueID, ntype, actorID, time.Now().Unix()/86400)
}

// notifyIssueTx upserts one pending notification per recipient inside
// the caller's transaction and emits the sync records. The recipient
// list may contain the actor, agents, and duplicates: the actor is
// never told about their own action, agents have no inbox, and each
// account is notified once. Empty recipients is a no-op.
func (a *API) notifyIssueTx(ctx context.Context, tx pgx.Tx, workspaceID string, p *Principal, row issueRow, ntype string, recipients []string) ([]syncActionRecord, error) {
	recs := make([]syncActionRecord, 0, len(recipients))
	seen := make(map[string]bool, len(recipients))
	for _, rid := range recipients {
		if rid == "" || seen[rid] || rid == p.AccountID {
			continue
		}
		seen[rid] = true
		key := dedupKey(row.ID, ntype, p.AccountID)
		var id string
		var createdAt time.Time
		var readAt *string
		err := tx.QueryRow(ctx, `
			insert into notifications
				(workspace_id, issue_id, account_id, issue_number, type,
				 actor_id, actor_name, dedup_key)
			select $1, $2, $3, $4, $5, $6, $7, $8
			from accounts
			where id = $3 and kind = $9
			on conflict (workspace_id, account_id, dedup_key)
				where read_at is null
			do update
				set created_at = now(),
				    issue_number = excluded.issue_number,
				    actor_id = coalesce(excluded.actor_id, notifications.actor_id),
				    actor_name = coalesce(excluded.actor_name, notifications.actor_name)
			returning id, created_at, read_at`,
			workspaceID, row.ID, rid, row.Number, ntype,
			p.AccountID, p.Fullname, key, auth.AccountKindHuman).Scan(&id, &createdAt, &readAt)
		if errors.Is(err, pgx.ErrNoRows) {
			continue // the recipient is not a human account: no inbox
		}
		if err != nil {
			return nil, err
		}
		data := map[string]any{
			"id":          id,
			"workspaceId": workspaceID,
			"issueId":     row.ID,
			"issueNumber": row.Number,
			"type":        ntype,
			"actorId":     p.AccountID,
			"actorName":   p.Fullname,
			"recipientId": rid,
			"createdAt":   createdAt.UTC().Format(iso),
			"readAt":      readAt,
		}
		rec, err := a.emitChange(ctx, tx, workspaceID, notifModel, id, "CREATE", data)
		if err != nil {
			return nil, err
		}
		recs = append(recs, rec)
	}
	return recs, nil
}

// issueParticipants returns the accounts that have commented on the
// issue (the matrix's "everyone who has commented").
func (a *API) issueParticipants(ctx context.Context, tx pgx.Tx, issueID string) []string {
	rows, err := tx.Query(ctx,
		"select distinct author_id from comments where issue_id = $1", issueID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return out
		}
		out = append(out, id)
	}
	_ = rows.Err()
	return out
}

// notifyIssuePatchTx fires the patch's triggers (the matrix rows that
// ride an issue update): assign / reassign, state change, and the
// terminal "closed" (one notification to the union). Returns the sync
// records for the caller to append to its outbox set.
func (a *API) notifyIssuePatchTx(ctx context.Context, tx pgx.Tx, p *Principal, workspaceID string, row issueRow, req issueRequest) ([]syncActionRecord, error) {
	// Assign / reassign: the new assignee learns of the task; the old
	// assignee learns of the replacement (the actor exclusion inside the
	// notifier keeps a self-assignment quiet).
	if req.AssigneeID != nil && strval(req.AssigneeID) != strval(row.AssigneeID) {
		var recs []syncActionRecord
		if newA := strval(req.AssigneeID); newA != "" {
			r, err := a.notifyIssueTx(ctx, tx, workspaceID, p, row, notifAssigned, []string{newA})
			if err != nil {
				return nil, err
			}
			recs = append(recs, r...)
		}
		if oldA := strval(row.AssigneeID); oldA != "" {
			r, err := a.notifyIssueTx(ctx, tx, workspaceID, p, row, notifReassigned, []string{oldA})
			if err != nil {
				return nil, err
			}
			recs = append(recs, r...)
		}
		if len(recs) > 0 {
			return recs, nil
		}
	}
	// State change: terminal moves get one "closed" to the union of the
	// issue's people; the rest get "state" (assignee + creator).
	if req.StateID != nil && strval(req.StateID) != strval(row.StatusID) {
		var category string
		if err := tx.QueryRow(ctx,
			"select category from workflow_statuses where id = $1", *req.StateID).Scan(&category); err != nil {
			return nil, err
		}
		var recipients []string
		if category == "COMPLETED" || category == "CANCELED" {
			// The union: assignee, creator, and the comment participants.
			set := make(map[string]bool)
			for _, id := range append([]string{strval(row.AssigneeID), strval(row.CreatedByID)}, a.issueParticipants(ctx, tx, row.ID)...) {
				if id != "" {
					set[id] = true
				}
			}
			for id := range set {
				recipients = append(recipients, id)
			}
			sort.Strings(recipients)
			return a.notifyIssueTx(ctx, tx, workspaceID, p, row, notifClosed, recipients)
		}
		return a.notifyIssueTx(ctx, tx, workspaceID, p, row, notifState,
			[]string{strval(row.AssigneeID), strval(row.CreatedByID)})
	}
	return nil, nil
}

// mentionTokens is the mention scanner: an @ handle, then the name
// characters (a dot is admitted for email-style handles; a dash for
// the "bravo-1" form).
var mentionTokens = regexp.MustCompile(`@([A-Za-z0-9][A-Za-z0-9._-]*)`)

// notifyCommentTx fires the comment trigger (assignee + creator + the
// participants — the actor excluded inside the notifier) plus the
// mention trigger for any workspace member the body addresses.
func (a *API) notifyCommentTx(ctx context.Context, tx pgx.Tx, p *Principal, workspaceID string, row issueRow, body string) ([]syncActionRecord, error) {
	var recs []syncActionRecord
	r, err := a.notifyIssueTx(ctx, tx, workspaceID, p, row, notifComment,
		append([]string{strval(row.AssigneeID), strval(row.CreatedByID)}, a.issueParticipants(ctx, tx, row.ID)...))
	if err != nil {
		return nil, err
	}
	recs = append(recs, r...)
	// Mentions: match the body's handles against the workspace's human
	// members, by full name or first name (case-insensitive).
	var tokens []string
	seen := make(map[string]bool)
	for _, m := range mentionTokens.FindAllStringSubmatch(body, -1) {
		t := strings.ToLower(m[1])
		if t != strings.ToLower(p.Fullname) && !seen[t] {
			seen[t] = true
			tokens = append(tokens, t)
		}
	}
	if len(tokens) > 0 {
		rows, err := tx.Query(ctx, `
			select a.id
			from accounts a
			join workspace_members m on m.account_id = a.id
			where m.workspace_id = $1 and a.kind = $2
			  and (lower(a.name) = any($3)
			       or split_part(lower(a.name), ' ', 1) = any($3))`,
			workspaceID, auth.AccountKindHuman, tokens)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var mentioned []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			mentioned = append(mentioned, id)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if len(mentioned) > 0 {
			r, err := a.notifyIssueTx(ctx, tx, workspaceID, p, row, notifMention, mentioned)
			if err != nil {
				return nil, err
			}
			recs = append(recs, r...)
		}
	}
	return recs, nil
}

// ---- the inbox routes -----------------------------------------------------

// handleListNotifications implements GET /api/v1/notifications
// ?workspaceId=...&unreadOnly=true&limit=... — the recipient's own rows
// (the one per-recipient read; the sync feed's records are per-workspace).
func (a *API) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	q := r.URL.Query()
	workspaceID := q.Get("workspaceId")
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspaceId is required")
		return
	}
	if _, ok := a.workspaceRole(r.Context(), p, workspaceID); !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	limit := 200
	if v := q.Get("limit"); v != "" {
		if n, err := fmt.Sscanf(v, "%d", &limit); err != nil || n != 1 {
			writeError(w, http.StatusBadRequest, "limit must be an integer")
			return
		}
		if limit <= 0 || limit > 500 {
			limit = 200
		}
	}
	unreadOnly := q.Get("unreadOnly") == "true"

	sql := `
		select id, issue_id, issue_number, type, actor_id, actor_name, created_at, read_at
		from notifications
		where workspace_id = $1 and account_id = $2` +
		(notifRetentionFilter(unreadOnly) + `
		order by created_at desc
		limit $3`)
	var rows pgx.Rows
	var err error
	if unreadOnly {
		rows, err = a.pool.Query(r.Context(), sql, workspaceID, p.AccountID, limit)
	} else {
		rows, err = a.pool.Query(r.Context(), strings.Replace(sql, " and read_at is null", "", 1), workspaceID, p.AccountID, limit)
	}
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer rows.Close()
	out := make([]notification, 0, limit)
	for rows.Next() {
		var n notification
		var actorID *string
		var createdAt time.Time
		var readAt *string
		if err := rows.Scan(&n.ID, &n.IssueID, &n.IssueNumber, &n.Type, &actorID, &n.ActorName, &createdAt, &readAt); err != nil {
			a.internalError(w, err)
			return
		}
		n.WorkspaceID = workspaceID
		n.ActorID = actorID
		n.RecipientID = p.AccountID
		n.CreatedAt = createdAt.UTC().Format(iso)
		n.ReadAt = readAt
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// notifRetentionFilter is the read-side horizon: the inbox shows the
// retention window (older rows are purged server-side, but a just-purged
// boundary stays bounded by the same constant).
func notifRetentionFilter(unreadOnly bool) string {
	if unreadOnly {
		return " and read_at is null and created_at > now() - " + notifRetention
	}
	return " and created_at > now() - " + notifRetention
}

// handleMarkNotificationRead implements POST /api/v1/notifications/{id}/read.
// The row must be the caller's: a notification is addressed, not shared.
func (a *API) handleMarkNotificationRead(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var n notification
	var createdAt time.Time
	err = tx.QueryRow(ctx, `
		select n.id, n.workspace_id::text, n.issue_id::text, n.issue_number, n.type,
		       a.id::text, a.name, n.created_at, n.read_at, n.account_id::text
		from notifications n
		join accounts a on a.id = n.actor_id
		where n.id = $1 and n.account_id = $2`, id, p.AccountID).Scan(
		&n.ID, &n.WorkspaceID, &n.IssueID, &n.IssueNumber, &n.Type,
		&n.ActorID, &n.ActorName, &createdAt, &n.ReadAt, &n.RecipientID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		a.internalError(w, err)
		return
	}
	n.CreatedAt = createdAt.UTC().Format(iso)
	if n.ReadAt == nil {
		now := time.Now().UTC().Format(iso)
		n.ReadAt = &now
		if _, err := tx.Exec(ctx,
			"update notifications set read_at = now() where id = $1 and account_id = $2",
			id, p.AccountID); err != nil {
			a.internalError(w, err)
			return
		}
	} else {
		// Already read: the op is a no-op that still answers 200 (the
		// client's optimistic badge may race a second tab) — no feed record.
		writeJSON(w, http.StatusOK, a.notificationData(&n))
		return
	}
	rec, err := a.emitChange(ctx, tx, n.WorkspaceID, notifModel, n.ID, "UPDATE", a.notificationData(&n))
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	writeJSON(w, http.StatusOK, a.notificationData(&n))
}

// handleMarkNotificationsRead implements POST /api/v1/notifications/read_all
// ?workspaceId=... — the inbox's clear-all. The sync fan-out is capped
// (the feed carries the newest; the rest settle on the next list fetch).
const readAllBroadcastCap = 100

func (a *API) handleMarkNotificationsRead(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	workspaceID := r.URL.Query().Get("workspaceId")
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspaceId is required")
		return
	}
	if _, ok := a.workspaceRole(ctx, p, workspaceID); !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The rows to update, newest first (the broadcast cap keeps the feed
	// bounded for a long-idle inbox; the DB state is complete regardless).
	rows, err := tx.Query(ctx, `
		select n.id::text, n.issue_id::text, n.issue_number, n.type,
		       a.id::text, a.name, n.created_at, n.account_id::text
		from notifications n
		left join accounts a on a.id = n.actor_id
		where n.workspace_id = $1 and n.account_id = $2 and n.read_at is null
		order by n.created_at desc`, workspaceID, p.AccountID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	var ids []string
	var datas []map[string]any
	for rows.Next() {
		var id, issueID, ntype, actorID, name, accountID string
		var number int
		var createdAt time.Time
		if err := rows.Scan(&id, &issueID, &number, &ntype, &actorID, &name, &createdAt, &accountID); err != nil {
			rows.Close()
			a.internalError(w, err)
			return
		}
		ids = append(ids, id)
		// Every record the plane emits carries the model's full key set
		// (the wire discipline: present or null, never absent — the
		// client's model declares the keys without undefined).
		d := map[string]any{
			"id":          id,
			"workspaceId": workspaceID,
			"issueId":     issueID,
			"issueNumber": number,
			"type":        ntype,
			"actorId":     nullForEmpty(&actorID),
			"actorName":   nullForEmpty(&name),
			"recipientId": accountID,
			"createdAt":   createdAt.UTC().Format(iso),
			"readAt":      time.Now().UTC().Format(iso),
		}
		datas = append(datas, d)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		a.internalError(w, err)
		return
	}
	rows.Close()
	if len(ids) == 0 {
		_ = tx.Commit(ctx)
		writeJSON(w, http.StatusOK, map[string]int{"updated": 0})
		return
	}
	if _, err := tx.Exec(ctx,
		"update notifications set read_at = now() where workspace_id = $1 and account_id = $2 and read_at is null",
		workspaceID, p.AccountID); err != nil {
		a.internalError(w, err)
		return
	}
	recs := make([]syncActionRecord, 0, len(ids))
	for i := range ids {
		rec, err := a.emitChange(ctx, tx, workspaceID, notifModel, ids[i], "UPDATE", datas[i])
		if err != nil {
			a.internalError(w, err)
			return
		}
		recs = append(recs, rec)
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	// The feed gets the newest up to the cap (the rest settle on the next
	// list fetch; the DB state — the badge's truth — is already complete).
	for i := range recs {
		if i < readAllBroadcastCap {
			a.broadcastRecord(recs[i])
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated": len(ids)})
}

// notificationData is the wire record for a notification row read from
// the database (the routes serve it; the collector builds the same
// shape). The pointers ride as null where the row has none: the wire
// discipline is present or null, never absent.
func (a *API) notificationData(n *notification) map[string]any {
	return map[string]any{
		"id":          n.ID,
		"workspaceId": n.WorkspaceID,
		"issueId":     n.IssueID,
		"issueNumber": n.IssueNumber,
		"type":        n.Type,
		"actorId":     n.ActorID,
		"actorName":   n.ActorName,
		"recipientId": n.RecipientID,
		"createdAt":   n.CreatedAt,
		"readAt":      n.ReadAt,
	}
}

// collectNotifications is the bootstrap's inbox slice: the requesting
// account's own rows for the workspace (the only per-recipient collector
// — the sync feed is per-workspace, and the client keeps the rows
// addressed to its user). Bounded by the retention window and the last
// 500 rows: the inbox renders recent history, not a ledger.
const notifBootstrapLimit = 500

func (a *API) collectNotifications(ctx context.Context, workspaceID, accountID string, emit emitFn) ([]syncActionRecord, error) {
	rows, err := a.pool.Query(ctx, `
		select n.id, n.issue_id, n.issue_number, n.type,
		       a.id, a.name, n.created_at, n.read_at
		from notifications n
		left join accounts a on a.id = n.actor_id
		where n.workspace_id = $1 and n.account_id = $2
		  and n.created_at > now() - `+notifRetention+`
		order by n.created_at desc
		limit `+fmt.Sprint(notifBootstrapLimit), workspaceID, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var recs []syncActionRecord
	for rows.Next() {
		var n notification
		var actorID, actorName *string
		var createdAt time.Time
		var readAt *string
		if err := rows.Scan(&n.ID, &n.IssueID, &n.IssueNumber, &n.Type,
			&actorID, &actorName, &createdAt, &readAt); err != nil {
			return nil, err
		}
		n.WorkspaceID = workspaceID
		n.ActorID = actorID
		n.ActorName = actorName
		n.RecipientID = accountID
		n.CreatedAt = createdAt.UTC().Format(iso)
		n.ReadAt = readAt
		rec, err := emit(n.ID, a.notificationData(&n))
		if err != nil {
			return nil, err
		}
		recs = append(recs, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return recs, nil
}

// pruneNotifications is the retention sweeper's body: one bounded
// delete over the retention index — the cost is one index range scan,
// not per request.
func (a *API) pruneNotifications(ctx context.Context) error {
	tag, err := a.pool.Exec(ctx,
		"delete from notifications where created_at < now() - "+notifRetention)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		a.log.Debug("inbox retention sweep", "rows", tag.RowsAffected())
	}
	return nil
}

// inboxRetentionInterval is the sweeper's clock. Notifications are the
// product's only derived table with a time horizon, and its rows are
// disposable pointers, so a coarse interval is the right granularity:
// one 90-day-old range delete every half hour, plus one at boot (work
// aged while the process was down settles on the first pass, the same
// way the runtime picks up its queue).
const inboxRetentionInterval = 30 * time.Minute

// StartInboxRetention launches the retention goroutine on the process
// context; main runs it alongside the agent runtime.
func (a *API) StartInboxRetention(ctx context.Context) {
	go func() {
		if err := a.pruneNotifications(ctx); err != nil {
			a.log.Warn("inbox retention sweep failed", "error", err)
		}
		tick := time.NewTicker(inboxRetentionInterval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if err := a.pruneNotifications(ctx); err != nil {
					a.log.Warn("inbox retention sweep failed", "error", err)
				}
			}
		}
	}()
}
