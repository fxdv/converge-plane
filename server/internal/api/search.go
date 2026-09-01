// search.go — M5: cross-team issue search.
//
// Ranking is trigram distance (the pg_trgm <-> operator, backed by the
// GIN index on search_text) with a recall floor: a substring match (ilike)
// always wins, so a short query that hits a word deep in a long
// description is never ranked below an exact hit.
package api

import (
	"net/http"
	"strconv"
	"strings"
)

// searchLimitCap bounds one search: the trigram scan is index-assisted
// but unbounded result sets would let a broad query page through the
// whole tenant. The client asks for 10.
const searchLimitCap = 50

// handleSearch implements GET /api/v1/search?query=&workspaceId=&limit=
// for active workspace members. An empty query yields an empty array (the
// client renders an empty suggestion list, not an error).
func (a *API) handleSearch(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	q := r.URL.Query()
	workspaceID := q.Get("workspaceId")
	query := strings.TrimSpace(q.Get("query"))
	if workspaceID == "" || !a.memberOf(r.Context(), p, workspaceID) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if query == "" {
		writeJSON(w, http.StatusOK, []map[string]any{})
		return
	}
	limit := 10
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > searchLimitCap {
		limit = searchLimitCap
	}

	rows, err := a.pool.Query(r.Context(), `
		select `+issueColumns+`
		from issues i
		join teams t on t.id = i.team_id
		where t.workspace_id = $1 and i.status = 'active'
		  and (i.search_text % $2 or i.search_text ilike '%' || $2 || '%')
		order by i.search_text <-> $2
		limit $3`, workspaceID, query, limit)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var row issueRow
		if err := rows.Scan(&row.ID, &row.TeamID, &row.Number, &row.Priority, &row.SortOrder,
			&row.Title, &row.DescRaw, &row.Status, &row.CreatedAt, &row.UpdatedAt,
			&row.CreatedByID, &row.AssigneeID, &row.ParentID, &row.StatusID,
			&row.LabelIDs, &row.Children); err != nil {
			a.internalError(w, err)
			return
		}
		out = append(out, a.issueData(row))
	}
	if err := rows.Err(); err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
