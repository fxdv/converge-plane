// wire_builders.go — the sync engine's wire builders
// (docs/spec/product-spec.tex, ch. 5): the exact map shapes the web
// client's stores expect, plus the small serialization helpers they
// share. Pinned by internal/api/wire_contract_test.go and
// web/src/store/__tests__/wire-contract.test.ts — the two sides of the
// contract must stay in step.
// spec cs:api:wire

package api

import (
	"encoding/json"
	"strconv"
	"time"
)

// issueData serializes an issue row in the exact shape of the client's
// Issue model. Bootstrap and mutation responses share it so both speak
// the identical vocabulary. (stateId is a required string in the client
// model; an issue without a status serializes as empty, never null.)
func (a *API) issueData(r issueRow) map[string]any {
	// v1.1: both denormalized arrays must always be arrays, never null
	// (a JSON null would crash a strict model).
	relations := r.RelationRaw
	if len(relations) == 0 {
		relations = []byte(`[]`)
	}
	projectIds := r.ProjectIds
	if projectIds == nil {
		projectIds = []string{}
	}
	// projectId (the legacy singular wire field) derives from the
	// membership — one source of truth, two projections: v1 is a single
	// project, so the first element is the answer.
	var projectID any = nil
	if len(projectIds) > 0 {
		projectID = projectIds[0]
	}
	return map[string]any{
		"id":                 r.ID,
		"createdAt":          r.CreatedAt.Format(iso),
		"updatedAt":          r.UpdatedAt.Format(iso),
		"title":              r.Title,
		"number":             r.Number,
		"description":        descriptionForClient(r.DescRaw),
		"priority":           r.Priority,
		"dueDate":            nil,
		"sortOrder":          r.SortOrder,
		"estimate":           0,
		"teamId":             r.TeamID,
		"createdById":        nullOrEmpty(strval(r.CreatedByID)),
		"assigneeId":         nullOrEmpty(strval(r.AssigneeID)),
		"labelIds":           r.LabelIDs,
		"parentId":           nullOrEmpty(strval(r.ParentID)),
		"stateId":            strval(r.StatusID),
		"subscriberIds":      []string{},
		"cycleId":            nil,
		"projectId":          projectID,
		"projectIds":         projectIds,
		"projectMilestoneId": nil,
		"sourceMetadata":     nil,
		"children":           r.Children,
		"agentPaused":        r.AgentPaused,
		"relations":          relations,
	}
}

func nullOrEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// artifactData serializes a swarm document (SWR-56) in the exact shape
// of the client's IssueArtifact model. The body is plain text (a fenced
// markdown/JSON document the client renders preformatted — never as
// rich text), so there is no jsonb envelope; sourceMetadata is present
// (null) because the client field is union(string, null) without
// undefined.
func artifactData(id, title, body, authorID, issueID string, createdAt, updatedAt time.Time) map[string]any {
	return map[string]any{
		"id":             id,
		"createdAt":      createdAt.Format(iso),
		"updatedAt":      updatedAt.Format(iso),
		"userId":         authorID,
		"issueId":        issueID,
		"title":          title,
		"body":           body,
		"sourceMetadata": nil,
	}
}

// historyData maps a generic activity/audit row to the client's
// IssueHistory shape (pure, so the wire contract is unit-testable;
// collectHistory feeds it the issue_history columns). Only
// status/assignee/priority/labels transitions are user-visible in v1;
// summary carries the handoff note (D1) — null on every other row.
func historyData(id string, createdAt, updatedAt time.Time, actorID *string, issueID, action, field string, from, to, summary *string) map[string]any {
	// Every from/to field is union(..., null) without undefined in the
	// client model, so all of them must be present (null when unset).
	data := map[string]any{
		"id":        id,
		"createdAt": createdAt.Format(iso),
		"updatedAt": updatedAt.Format(iso),
		"userId":    nullOrEmpty(strval(actorID)),
		"issueId":   nullOrEmpty(issueID),
		// D1: the client history model needs the action to tell a
		// handoff from a pause (both carry a summary).
		"action":          action,
		"addedLabelIds":   []string{},
		"removedLabelIds": []string{},
		"fromPriority":    nil,
		"toPriority":      nil,
		"fromStateId":     nil,
		"toStateId":       nil,
		"fromEstimate":    nil,
		"toEstimate":      nil,
		"fromAssigneeId":  nil,
		"toAssigneeId":    nil,
		"fromParentId":    nil,
		"toParentId":      nil,
		"relationChanges": nil,
		"sourceMetadata":  nil,
		"summary":         nullOrEmpty(strval(summary)),
	}
	switch field {
	case "status":
		data["fromStateId"] = nullOrEmpty(strval(from))
		data["toStateId"] = nullOrEmpty(strval(to))
	case "labels":
		// from/to carry JSON arrays of label ids.
		if f := strval(from); f != "" {
			var arr []string
			if json.Unmarshal([]byte(f), &arr) == nil {
				data["removedLabelIds"] = arr
			}
		}
		if t := strval(to); t != "" {
			var arr []string
			if json.Unmarshal([]byte(t), &arr) == nil {
				data["addedLabelIds"] = arr
			}
		}
	case "parent":
		data["fromParentId"] = nullOrEmpty(strval(from))
		data["toParentId"] = nullOrEmpty(strval(to))
	case "relation", "relation_deleted":
		// The timeline's RelatedActivity entry: the client renders
		// from relationChanges (added vs removed) with the related
		// issue's identifier.
		data["relationChanges"] = map[string]any{
			"isDeleted":      action == "relation_deleted",
			"issueId":        nullOrEmpty(issueID),
			"relatedIssueId": nullOrEmpty(strval(from)),
			"type":           strval(to),
		}
	case "assignee":
		data["fromAssigneeId"] = nullOrEmpty(strval(from))
		data["toAssigneeId"] = nullOrEmpty(strval(to))
	case "priority":
		if f := strval(from); f != "" {
			data["fromPriority"], _ = strconv.Atoi(f)
		}
		if t := strval(to); t != "" {
			data["toPriority"], _ = strconv.Atoi(t)
		}
	}
	if action == "created" {
		data["toStateId"] = nullOrEmpty(strval(to))
	}
	return data
}

func strval(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// wireAction maps the internal outbox action to the client's
// SyncActionRecord action vocabulary (const enum Action = 'I'|'U'|'D',
// data-loader.ts). The client's save-data handlers switch on exactly
// these values; anything else is silently dropped, which would make
// mutations invisible in the UI. The database keeps the descriptive
// CREATE/UPDATE/DELETE names for operators.
func wireAction(action string) string {
	switch action {
	case "CREATE":
		return "I"
	case "UPDATE":
		return "U"
	case "DELETE":
		return "D"
	}
	return action
}
