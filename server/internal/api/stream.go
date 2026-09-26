// Realtime stream (M2, spec R-8): the web client's former socket.io
// connection replaced by an SSE endpoint delivering the same
// SyncActionRecord JSON the socket used to carry.
package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"converge/internal/broadcast"
	"converge/internal/metrics"
)

const ssePingInterval = 15 * time.Second

// headReadTimeout bounds the heartbeat's head read: the stream loop waits
// on it, so a slow database must not hold back the events queued behind.
const headReadTimeout = 2 * time.Second

// CollectMetrics reports the realtime fan-out at scrape time.
func (a *API) CollectMetrics(e *metrics.Emitter) {
	e.Gauge("converge_realtime_subscribers", "Open realtime streams across all workspaces.",
		float64(a.bcast.Subscribers()))
}

// handleStream implements GET /api/v1/sync_actions/stream?workspaceId=...
//
// EventSource connects cross-origin (web :3000 -> api :3001), so the
// response carries explicit CORS headers; the session cookies are
// SameSite=Lax but localhost:3000 -> localhost:3001 is same-site (site
// excludes the port), so the browser sends them.
//
// The stream is gap-free per workspace: every committed sequence is
// delivered, in full or — for a record addressed to another account —
// as a bare {"sequenceId","skip":true} marker, so the client's
// contiguous cursor can advance without seeing the record. The heartbeat
// carries the committed head (streamHeartbeat), so a delivery that never
// happened is noticed even when no later record follows it.
func (a *API) handleStream(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	workspaceID := r.URL.Query().Get("workspaceId")
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspaceId is required")
		return
	}
	if _, ok := a.workspaceRole(r.Context(), p, workspaceID); !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		a.internalError(w, fmt.Errorf("streaming unsupported by response writer"))
		return
	}

	// Subscribe before the client learns the stream is open: its
	// reconnect delta runs on open, and anything committed between that
	// delta's snapshot and a later subscription would reach neither.
	ch, cancel := a.bcast.Subscribe(workspaceID)
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering
	if a.cfg.WebOrigin != "" {
		w.Header().Set("Access-Control-Allow-Origin", a.cfg.WebOrigin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
	w.WriteHeader(http.StatusOK)
	// retry: instruct the client to reconnect 2s after a drop (the
	// browser default is implementation-defined); the stream is a hint
	// and the delta reconcile on (re)connect covers any gap.
	fmt.Fprint(w, "retry: 2000\n: connected\n\n")
	fl.Flush()

	a.log.Debug("realtime subscriber attached", "workspace", workspaceID,
		"account", p.AccountID, "subscribers", a.bcast.SubscriberCount(workspaceID))

	ping := time.NewTicker(ssePingInterval)
	defer ping.Stop()

	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				// The broadcaster closed us for falling behind: ending the
				// stream makes the client reconnect and resync from the delta.
				a.log.Info("realtime subscriber disconnected: fell behind",
					"workspace", workspaceID, "account", p.AccountID)
				return
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", streamPayload(ev, p.AccountID)); err != nil {
				return
			}
			fl.Flush()
		case <-ping.C:
			if _, err := fmt.Fprint(w, a.streamHeartbeat(r.Context(), workspaceID)); err != nil {
				return
			}
			fl.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// streamHeartbeat is the keep-alive frame: a `head` event with the
// workspace's committed head, which the client compares with its cursor.
// A failed read degrades to a bare comment that only keeps the
// connection open.
func (a *API) streamHeartbeat(ctx context.Context, workspaceID string) string {
	ctx, cancel := context.WithTimeout(ctx, headReadTimeout)
	defer cancel()
	head, err := a.workspaceHead(ctx, workspaceID)
	if err != nil {
		return ": ping\n\n"
	}
	return fmt.Sprintf("event: head\ndata: {\"sequenceId\":\"%d\"}\n\n", head)
}

// streamPayload is what one subscriber receives for an event: the record
// itself, or only its sequence when it is addressed to someone else.
func streamPayload(ev broadcast.Event, accountID string) []byte {
	if ev.To == "" || ev.To == accountID {
		return ev.Data
	}
	return []byte(`{"sequenceId":` + strconv.Quote(ev.Seq) + `,"skip":true}`)
}
