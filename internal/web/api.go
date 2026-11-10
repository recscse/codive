package web

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/recscse/codive/internal/git"
	"github.com/recscse/codive/internal/graph"
)

// handleExplorerFlow handles GET /api/explorer/flow?node_id=...&depth=6
func handleExplorerFlow(database *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.URL.Query().Get("node_id")
		if nodeID == "" {
			http.Error(w, "missing 'node_id' query parameter", http.StatusBadRequest)
			return
		}

		maxDepth := 6
		if dStr := r.URL.Query().Get("depth"); dStr != "" {
			if d, err := strconv.Atoi(dStr); err == nil && d > 0 && d <= 20 {
				maxDepth = d
			}
		}

		ctx := r.Context()
		flow, err := graph.TraverseExecutionFlow(ctx, database, nodeID, maxDepth)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(flow)
	}
}

// handleExplorerImpact handles GET /api/explorer/impact?node_id=...
func handleExplorerImpact(database *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.URL.Query().Get("node_id")
		if nodeID == "" {
			http.Error(w, "missing 'node_id' query parameter", http.StatusBadRequest)
			return
		}

		ctx := r.Context()
		report, err := graph.AnalyzeImpact(ctx, database, nodeID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(report)
	}
}

// handleExplorerChanges handles GET /api/explorer/changes: uncommitted files and
// the symbols they touch, for answering "what does my current work affect?".
func handleExplorerChanges(database *sql.DB, targetDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := git.GetGitChanges(r.Context(), targetDir, database)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// The index itself isn't the user's work.
		kept := result.Files[:0]
		for _, f := range result.Files {
			if strings.HasPrefix(f.Path, ".codive/") {
				result.TotalChanged--
				continue
			}
			kept = append(kept, f)
		}
		result.Files = kept

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	}
}

// handleExplorerGraph handles GET /api/explorer/graph?limit=3000
func handleExplorerGraph(database *sql.DB, targetDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 3500
		if lStr := r.URL.Query().Get("limit"); lStr != "" {
			if l, err := strconv.Atoi(lStr); err == nil && l > 0 && l <= 200000 {
				limit = l
			}
		}

		ctx := r.Context()
		if err := graph.EnsureFresh(ctx, database, targetDir); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		snapshot, err := graph.LoadGraph(ctx, database, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snapshot)
	}
}
