// webhooks.go — signed outbound webhooks (Phase 2).
// spec cs:agents:webhooks
//
// An admin registers an HTTPS endpoint. When an issue changes, a run is
// reported, or a pull request's verified state changes, the server
// queues a small JSON event and a dispatcher POSTs it. The body is
// signed with the endpoint's secret:
//
//	X-Converge-Timestamp: unix seconds
//	X-Converge-Signature: sha256=<hex HMAC-SHA256 of timestamp + "." + body>
//
// The secret is returned once, at creation or rotation, and is never
// logged. Redirects are not followed. The URL is checked again at
// delivery time, so a name that later points at a private address is
// not fetched.
package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

const (
	webhookMaxEndpoints = 10
	webhookMaxAttempts  = 8
	webhookTimeout      = 10 * time.Second
	webhookBatch        = 10
)

func (a *API) enqueueWebhookTx(ctx context.Context, tx pgx.Tx, workspaceID, event string, data map[string]any) error {
	if a.webhooks == nil {
		return nil
	}
	var n int
	if err := tx.QueryRow(ctx, `
		select count(*) from webhook_endpoints where workspace_id = $1 and enabled`, workspaceID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		insert into webhook_events (workspace_id, event, payload) values ($1, $2, $3::jsonb)`,
		workspaceID, event, string(payload))
	return err
}

func (a *API) handleCreateWebhook(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	workspaceID := chi.URLParam(r, "id")
	if !a.webhookAdmin(ctx, w, p, workspaceID) {
		return
	}
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := webhookURLAllowed(ctx, req.URL, a.cfg.DevMode); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	var n int
	if err := a.pool.QueryRow(ctx, `select count(*) from webhook_endpoints where workspace_id = $1`, workspaceID).Scan(&n); err != nil {
		a.internalError(w, err)
		return
	}
	if n >= webhookMaxEndpoints {
		writeError(w, http.StatusUnprocessableEntity, "a workspace keeps at most 10 webhook endpoints")
		return
	}
	secret, err := newWebhookSecret()
	if err != nil {
		a.internalError(w, err)
		return
	}
	var id string
	var created time.Time
	err = a.pool.QueryRow(ctx, `
		insert into webhook_endpoints (workspace_id, url, secret) values ($1, $2, $3)
		returning id, created_at`, workspaceID, req.URL, secret).Scan(&id, &created)
	if err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": id, "url": req.URL, "secret": secret, "enabled": true,
		"createdAt": created.UTC().Format(time.RFC3339),
	})
}

func (a *API) handleListWebhooks(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	workspaceID := chi.URLParam(r, "id")
	if !a.webhookAdmin(ctx, w, p, workspaceID) {
		return
	}
	rows, err := a.pool.Query(ctx, `
		select id, url, enabled, created_at from webhook_endpoints
		where workspace_id = $1 order by created_at`, workspaceID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, rawURL string
		var enabled bool
		var created time.Time
		if err := rows.Scan(&id, &rawURL, &enabled, &created); err != nil {
			a.internalError(w, err)
			return
		}
		out = append(out, map[string]any{
			"id": id, "url": rawURL, "enabled": enabled,
			"createdAt": created.UTC().Format(time.RFC3339),
		})
	}
	if err := rows.Err(); err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleListWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	workspaceID := chi.URLParam(r, "id")
	if !a.webhookAdmin(ctx, w, p, workspaceID) {
		return
	}
	rows, err := a.pool.Query(ctx, `
		select id, event, attempts, last_error, delivered_at, next_attempt_at, created_at
		from webhook_events
		where workspace_id = $1
		order by created_at desc
		limit 20`, workspaceID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var (
			id, event     string
			attempts      int
			last          *string
			next, created time.Time
			deliveredAt   *time.Time
		)
		if err := rows.Scan(&id, &event, &attempts, &last, &deliveredAt, &next, &created); err != nil {
			a.internalError(w, err)
			return
		}
		row := map[string]any{
			"id": id, "event": event, "attempts": attempts,
			"lastError": nil, "deliveredAt": nil,
			"nextAttemptAt": next.UTC().Format(time.RFC3339),
			"createdAt":     created.UTC().Format(time.RFC3339),
		}
		if last != nil {
			row["lastError"] = *last
		}
		if deliveredAt != nil {
			row["deliveredAt"] = deliveredAt.UTC().Format(time.RFC3339)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleRetryWebhookDelivery(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	workspaceID := chi.URLParam(r, "id")
	eventID := chi.URLParam(r, "eventId")
	if !a.webhookAdmin(ctx, w, p, workspaceID) {
		return
	}
	tag, err := a.pool.Exec(ctx, `
		update webhook_events
		set next_attempt_at = now(), delivered_at = null, attempts = 0, last_error = null
		where id = $1 and workspace_id = $2`, eventID, workspaceID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	workspaceID := chi.URLParam(r, "id")
	endpointID := chi.URLParam(r, "endpointId")
	if !a.webhookAdmin(ctx, w, p, workspaceID) {
		return
	}
	tag, err := a.pool.Exec(ctx, `
		delete from webhook_endpoints where id = $1 and workspace_id = $2`, endpointID, workspaceID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleRotateWebhookSecret(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	workspaceID := chi.URLParam(r, "id")
	endpointID := chi.URLParam(r, "endpointId")
	if !a.webhookAdmin(ctx, w, p, workspaceID) {
		return
	}
	secret, err := newWebhookSecret()
	if err != nil {
		a.internalError(w, err)
		return
	}
	tag, err := a.pool.Exec(ctx, `
		update webhook_endpoints set secret = $3, updated_at = now()
		where id = $1 and workspace_id = $2`, endpointID, workspaceID, secret)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": endpointID, "secret": secret})
}

func (a *API) webhookAdmin(ctx context.Context, w http.ResponseWriter, p *Principal, workspaceID string) bool {
	if !isUUID(workspaceID) || agentActor(p) {
		writeError(w, http.StatusNotFound, "not found")
		return false
	}
	role, ok := a.workspaceRole(ctx, p, workspaceID)
	if !ok || !adminRole(role) {
		writeError(w, http.StatusNotFound, "not found")
		return false
	}
	return true
}

func newWebhookSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func webhookURLAllowed(ctx context.Context, raw string, dev bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return errors.New("webhook url must be an https URL without credentials")
	}
	if u.Scheme != "https" && !(dev && u.Scheme == "http") {
		return errors.New("webhook url must be https")
	}
	host := u.Hostname()
	if ip, err := netip.ParseAddr(host); err == nil {
		if !webhookIPAllowed(ip, dev) {
			return errors.New("webhook url must not point at a private address")
		}
		return nil
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return errors.New("webhook url host did not resolve")
	}
	for _, ip := range ips {
		if !webhookIPAllowed(ip, dev) {
			return errors.New("webhook url must not point at a private address")
		}
	}
	return nil
}

func webhookIPAllowed(ip netip.Addr, dev bool) bool {
	if dev && ip.IsLoopback() {
		return true
	}
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast()
}

func signWebhook(secret string, ts int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(strconv.FormatInt(ts, 10)))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

type webhookDispatcher struct {
	a      *API
	client *http.Client
}

func newWebhookDispatcher(a *API) *webhookDispatcher {
	return &webhookDispatcher{a: a, client: &http.Client{
		Timeout: webhookTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

func (d *webhookDispatcher) run(ctx context.Context, done <-chan struct{}) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-tick.C:
			d.deliverDue(ctx)
		}
	}
}

// closeDone lets StopRuntime close the channel without double-close if
// run already returned. The caller owns close.
func closeDone(<-chan struct{}) {}

func (d *webhookDispatcher) deliverDue(ctx context.Context) {
	rows, err := d.a.pool.Query(ctx, `
		select id, workspace_id, event, payload, attempts
		from webhook_events
		where delivered_at is null and next_attempt_at <= now() and attempts < $1
		order by next_attempt_at
		limit $2`, webhookMaxAttempts, webhookBatch)
	if err != nil {
		return
	}
	defer rows.Close()
	type due struct {
		id, workspaceID, event string
		payload                []byte
		attempts               int
	}
	var batch []due
	for rows.Next() {
		var e due
		if err := rows.Scan(&e.id, &e.workspaceID, &e.event, &e.payload, &e.attempts); err != nil {
			return
		}
		batch = append(batch, e)
	}
	if err := rows.Err(); err != nil {
		return
	}
	for _, e := range batch {
		d.deliverOne(ctx, e.id, e.workspaceID, e.event, e.payload, e.attempts)
	}
}

func (d *webhookDispatcher) deliverOne(ctx context.Context, eventID, workspaceID, event string, payload []byte, attempts int) {
	rows, err := d.a.pool.Query(ctx, `
		select id, url, secret from webhook_endpoints
		where workspace_id = $1 and enabled`, workspaceID)
	if err != nil {
		return
	}
	defer rows.Close()
	type ep struct{ id, url, secret string }
	var eps []ep
	for rows.Next() {
		var e ep
		if err := rows.Scan(&e.id, &e.url, &e.secret); err != nil {
			return
		}
		eps = append(eps, e)
	}
	if err := rows.Err(); err != nil {
		return
	}
	body, _ := json.Marshal(map[string]any{
		"id": eventID, "event": event, "data": json.RawMessage(payload),
	})
	tag, err := d.a.pool.Exec(ctx, `
		update webhook_events set next_attempt_at = now() + interval '2 minutes'
		where id = $1 and delivered_at is null and next_attempt_at <= now()`, eventID)
	if err != nil || tag.RowsAffected() == 0 {
		return
	}
	failed := false
	var last string
	for _, e := range eps {
		if err := webhookURLAllowed(ctx, e.url, d.a.cfg.DevMode); err != nil {
			failed, last = true, "url rejected"
			continue
		}
		ts := time.Now().Unix()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(body))
		if err != nil {
			failed, last = true, "request"
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "Converge-Webhooks")
		req.Header.Set("X-Converge-Event", event)
		req.Header.Set("X-Converge-Timestamp", strconv.FormatInt(ts, 10))
		req.Header.Set("X-Converge-Signature", signWebhook(e.secret, ts, body))
		res, err := d.client.Do(req)
		if err != nil {
			failed, last = true, "delivery failed"
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
		res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			failed, last = true, "endpoint returned "+strconv.Itoa(res.StatusCode)
		}
	}
	if failed {
		wait := time.Duration(1<<min(attempts, 6)) * time.Minute
		_, _ = d.a.pool.Exec(ctx, `
			update webhook_events
			set attempts = attempts + 1, next_attempt_at = now() + $2::int * interval '1 second', last_error = $3
			where id = $1`, eventID, int(wait.Seconds()), truncateRunes(last, 200))
		return
	}
	_, _ = d.a.pool.Exec(ctx, `
		update webhook_events set delivered_at = now(), attempts = attempts + 1, last_error = null
		where id = $1`, eventID)
}
