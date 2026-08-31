// Realtime stream (M2, spec R-8): the web client's former socket.io
// connection replaced by an SSE endpoint delivering the same
// SyncActionRecord JSON the socket used to carry.
package api

import (
	"fmt"
	"net/http"
	"time"
)

const ssePingInterval = 15 * time.Second

// handleStream implements GET /api/v1/sync_actions/stream?workspaceId=...
//
// EventSource connects cross-origin (web :3000 -> api :3001), so the
// response carries explicit CORS headers; the session cookies are
// SameSite=Lax but localhost:3000 -> localhost:3001 is same-site (site
// excludes the port), so the browser sends them.
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

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering
	if a.cfg.WebOrigin != "" {
		w.Header().Set("Access-Control-Allow-Origin", a.cfg.WebOrigin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()

	ch, cancel := a.bcast.Subscribe(workspaceID)
	defer cancel()

	a.log.Debug("realtime subscriber attached", "workspace", workspaceID,
		"account", p.AccountID, "subscribers", a.bcast.SubscriberCount(workspaceID))

	ping := time.NewTicker(ssePingInterval)
	defer ping.Stop()

	for {
		select {
		case payload, ok := <-ch:
			if !ok {
				return
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
				return
			}
			fl.Flush()
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
