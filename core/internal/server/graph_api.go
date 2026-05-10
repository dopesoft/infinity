package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// /api/memory/graph — returns the top N most-connected graph nodes plus
// every edge that connects two nodes in that set. Designed for the Memory
// tab's KnowledgeGraphPanel: the client renders a force-directed layout
// against this snapshot.
//
// Query params:
//
//	limit         — node cap (default 80, max 500)
//	type          — filter to nodes of this type (optional)
//	include_stale — include stale nodes (default false)

type graphNodeDTO struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Degree   int    `json:"degree"`
	Stale    bool   `json:"stale"`
	Metadata any    `json:"metadata,omitempty"`
}

type graphEdgeDTO struct {
	ID         string  `json:"id"`
	Source     string  `json:"source"`
	Target     string  `json:"target"`
	Type       string  `json:"type"`
	Confidence float64 `json:"confidence"`
}

type graphResponse struct {
	Nodes       []graphNodeDTO `json:"nodes"`
	Edges       []graphEdgeDTO `json:"edges"`
	TotalNodes  int            `json:"total_nodes"`
	TotalEdges  int            `json:"total_edges"`
	NodeTypes   []string       `json:"node_types"`
}

func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		writeJSON(w, http.StatusOK, graphResponse{Nodes: []graphNodeDTO{}, Edges: []graphEdgeDTO{}})
		return
	}

	q := r.URL.Query()
	limit := 80
	if v, _ := strconv.Atoi(q.Get("limit")); v > 0 {
		limit = v
	}
	if limit > 500 {
		limit = 500
	}
	typeFilter := strings.TrimSpace(q.Get("type"))
	includeStale := q.Get("include_stale") == "1" || q.Get("include_stale") == "true"

	ctx := r.Context()

	// Top nodes by edge-degree. Cheaper than fetching everything and sorting
	// in Go for large graphs, and naturally surfaces the interesting hubs.
	nodeQuery := `
		WITH degree AS (
		  SELECT id, COUNT(*) AS d FROM (
		    SELECT source_id AS id FROM mem_graph_edges
		    UNION ALL
		    SELECT target_id AS id FROM mem_graph_edges
		  ) e GROUP BY id
		)
		SELECT n.id::text, n.type, n.name, COALESCE(d.d, 0)::int, COALESCE(n.stale_flag, false), COALESCE(n.metadata, '{}'::jsonb)
		FROM mem_graph_nodes n
		LEFT JOIN degree d ON d.id = n.id
		WHERE ($1 = '' OR n.type = $1)
		  AND ($2 OR COALESCE(n.stale_flag, false) = false)
		ORDER BY COALESCE(d.d, 0) DESC, n.created_at DESC
		LIMIT $3
	`
	rows, err := s.pool.Query(ctx, nodeQuery, typeFilter, includeStale, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	nodes := []graphNodeDTO{}
	idSet := map[string]struct{}{}
	for rows.Next() {
		var n graphNodeDTO
		var meta []byte
		if err := rows.Scan(&n.ID, &n.Type, &n.Name, &n.Degree, &n.Stale, &meta); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if len(meta) > 0 && string(meta) != "{}" {
			var m any
			if json.Unmarshal(meta, &m) == nil {
				n.Metadata = m
			}
		}
		nodes = append(nodes, n)
		idSet[n.ID] = struct{}{}
	}

	// Edges where both endpoints are in our visible node set.
	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	edges := []graphEdgeDTO{}
	if len(ids) > 0 {
		edgeRows, err := s.pool.Query(ctx, `
			SELECT id::text, source_id::text, target_id::text, edge_type, COALESCE(confidence, 1.0)
			FROM mem_graph_edges
			WHERE source_id = ANY($1::uuid[])
			  AND target_id = ANY($1::uuid[])
		`, ids)
		if err == nil {
			defer edgeRows.Close()
			for edgeRows.Next() {
				var e graphEdgeDTO
				if err := edgeRows.Scan(&e.ID, &e.Source, &e.Target, &e.Type, &e.Confidence); err == nil {
					edges = append(edges, e)
				}
			}
		}
	}

	// Totals + type list for the UI filter chips.
	var totalNodes, totalEdges int
	_ = s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM mem_graph_nodes`).Scan(&totalNodes)
	_ = s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM mem_graph_edges`).Scan(&totalEdges)

	types := []string{}
	if typeRows, err := s.pool.Query(ctx, `SELECT DISTINCT type FROM mem_graph_nodes ORDER BY type`); err == nil {
		defer typeRows.Close()
		for typeRows.Next() {
			var t string
			if err := typeRows.Scan(&t); err == nil {
				types = append(types, t)
			}
		}
	}

	writeJSON(w, http.StatusOK, graphResponse{
		Nodes:      nodes,
		Edges:      edges,
		TotalNodes: totalNodes,
		TotalEdges: totalEdges,
		NodeTypes:  types,
	})
}

// writeJSON is shared in api.go.
