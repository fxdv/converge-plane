// sync.go — the sync engine: bootstrap, delta, and the stream
// dispatch (docs/spec/product-spec.tex, ch. 5). The per-model
// collectors live in collectors.go, the wire builders in
// wire_builders.go, and the outbox seams in outbox.go.
// spec cs:arch:sync
// spec cs:api:wire

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

// syncActionRecord matches the web client's SyncActionRecord.
type syncActionRecord struct {
	Data        json.RawMessage `json:"data"`
	ModelName   string          `json:"modelName"`
	ModelID     string          `json:"modelId"`
	Action      string          `json:"action"`
	WorkspaceID string          `json:"workspaceId"`
	SequenceID  string          `json:"sequenceId"`
}

// syncResponse matches the web client's BootstrapResponse.
type syncResponse struct {
	SyncActions    []syncActionRecord `json:"syncActions"`
	LastSequenceID string             `json:"lastSequenceId"`
	// Stale marks the delta as incomplete: the client's watermark is
	// older than the outbox's retained window, so some records were
	// trimmed away before delivery. The client must fall back to a
	// full bootstrap to reconstruct its object set.
	Stale bool `json:"stale,omitempty"`
}

// handleSync serves both /sync_actions/bootstrap and /sync_actions/delta.
//
// Tenant rule: the workspaceId parameter must be a workspace the
// principal actively belongs to; otherwise the response is a plain 404
// (no existence leak). Records are serialized in the exact shapes the
// web client's stores expect.
//
// Bootstrap returns the full tenant object set; delta returns outbox
// records newer than the client's lastSequenceId watermark (true gap
// recovery for M2: everything changed while the client was away).
func (a *API) handleSync(w http.ResponseWriter, r *http.Request) {
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

	// The server's current watermark for the workspace: the cursor
	// reported back to the client so it can advance past what was
	// delivered. Never echo the client's own (stale) cursor.
	var serverSeq int64
	if err := a.pool.QueryRow(r.Context(),
		"select coalesce((select last_sequence from sync_sequences where workspace_id = $1), 0)",
		workspaceID).Scan(&serverSeq); err != nil {
		serverSeq = 0
	}

	// Non-nil: the client contract is an array, and a Go nil slice
	// marshals as JSON null, which crashes the client's iteration.
	records := []syncActionRecord{}
	stale := false
	if strings.HasSuffix(r.URL.Path, "/delta") {
		// Delta: outbox records newer than the client's watermark, filtered
		// to the models it asked for. The client upserts/removes by model
		// id, so delivery is idempotent.
		afterSeq := serverSeq
		if v := q.Get("lastSequenceId"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				afterSeq = n
			}
		}
		var err error
		records, err = a.collectOutbox(r.Context(), workspaceID, afterSeq, q.Get("modelNames"))
		if err != nil {
			a.internalError(w, err)
			return
		}
		// The outbox is trimmed per workspace; if the client's cursor
		// predates the oldest retained record, the delta cannot be
		// complete. An empty outbox with a watermark behind it is stale
		// for the same reason, as is a zero cursor: a client that holds
		// no watermark cannot be satisfied by a forward-only delta, so it
		// must fall back to a full bootstrap.
		var oldest *int64
		if err := a.pool.QueryRow(r.Context(),
			"select min(sequence_id) from sync_outbox where workspace_id = $1",
			workspaceID).Scan(&oldest); err == nil {
			if oldest == nil || *oldest > afterSeq {
				stale = true
			}
		}
	} else {
		// Bootstrap: the client passes a comma-separated MODELS list.
		// Unknown or not-yet-shipped models simply yield no records.
		//
		// Collectors run concurrently: each is one independent indexed
		// query, so wall time is the slowest collector rather than the
		// sum of all of them (the sequential loop made a 9-model
		// bootstrap pay 9 serial round-trips). Record order is not
		// significant: the client upserts by model id and takes the
		// watermark from lastSequenceId.
		names := make([]string, 0, 8)
		seen := make(map[string]bool, 8)
		for _, name := range strings.Split(q.Get("modelNames"), ",") {
			name = strings.TrimSpace(name)
			if name != "" && !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
		byModel := make([][]syncActionRecord, len(names))
		var g errgroup.Group
		for i, name := range names {
			g.Go(func() error {
				recs, err := a.collectModel(r.Context(), name, workspaceID)
				if err != nil {
					return err
				}
				byModel[i] = recs
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			a.internalError(w, err)
			return
		}
		for _, recs := range byModel {
			records = append(records, recs...)
		}
	}

	writeJSON(w, http.StatusOK, syncResponse{
		SyncActions:    records,
		LastSequenceID: strconv.FormatInt(serverSeq, 10),
		Stale:          stale,
	})
}

// collectModel serializes one model for a tenant-scoped workspace.
func (a *API) collectModel(ctx context.Context, model, workspaceID string) ([]syncActionRecord, error) {
	seq := 0
	emit := func(id string, data any) (syncActionRecord, error) {
		seq++
		raw, err := json.Marshal(data)
		if err != nil {
			return syncActionRecord{}, err
		}
		return syncActionRecord{
			Data:        raw,
			ModelName:   model,
			ModelID:     id,
			Action:      "I",
			WorkspaceID: workspaceID,
			SequenceID:  strconv.Itoa(seq),
		}, nil
	}

	switch model {
	case "Workspace":
		return a.collectWorkspace(ctx, workspaceID, emit)
	case "UsersOnWorkspaces":
		return a.collectMembers(ctx, workspaceID, emit)
	case "Team":
		return a.collectTeams(ctx, workspaceID, emit)
	case "Workflow":
		return a.collectWorkflows(ctx, workspaceID, emit)
	case "Label":
		return a.collectLabels(ctx, workspaceID, emit)
	case "Issue":
		return a.collectIssues(ctx, workspaceID, emit)
	case "Project":
		return a.collectProjects(ctx, workspaceID, emit)
	case "IssueComment":
		return a.collectComments(ctx, workspaceID, emit)
	case "IssueRelation":
		return a.collectIssueRelations(ctx, workspaceID, emit)
	case "IssueHistory":
		return a.collectHistory(ctx, workspaceID, emit)
	case ModelSwarmActivity:
		return a.collectSwarmActivity(ctx, workspaceID, emit)
	case "View":
		return a.collectViews(ctx, workspaceID, emit)
	default:
		return nil, nil
	}
}

// emitFn is the record emitter bound to one model + workspace.
type emitFn func(id string, data any) (syncActionRecord, error)

const iso = time.RFC3339

// clientRole maps internal workspace roles to the client's role enum.
// The client model accepts only ADMIN | USER | BOT | AGENT (mirroring the
// backend Role enum); "owner" is a backend-only concept that serializes
// as ADMIN. Sending anything else (e.g. "OWNER") crashes the
// mobx-state-tree validation in the browser.
func clientRole(role string) string {
	switch strings.ToLower(role) {
	case "owner", "admin":
		return "ADMIN"
	case "agent":
		return "AGENT"
	default:
		return "USER"
	}
}
