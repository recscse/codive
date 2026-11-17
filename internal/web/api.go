package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

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

// inlineRebuildMaxFiles is the largest repository whose graph is rebuilt while
// a request waits. Beyond it a rebuild can take minutes, so it runs in the
// background and requests are answered from the previous graph meanwhile.
const inlineRebuildMaxFiles = 2000

// graphRebuilder runs at most one background graph rebuild at a time.
type graphRebuilder struct {
	mu      sync.Mutex
	running bool
}

var rebuilder graphRebuilder

// Checking staleness hashes every indexed file and the overview scans every
// symbol, which is seconds of work on a big repository. Both are cached briefly
// (and dropped when a rebuild finishes) so page loads and clicks stay fast.
const cacheTTL = 30 * time.Second

var cache struct {
	mu         sync.Mutex
	readyAt    time.Time
	overview   *graph.Overview
	overviewAt time.Time
}

func dropCache() {
	cache.mu.Lock()
	cache.readyAt, cache.overview = time.Time{}, nil
	cache.mu.Unlock()
}

func (g *graphRebuilder) busy() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.running
}

// start begins a background rebuild unless one is already running.
func (g *graphRebuilder) start(database *sql.DB, dir string) {
	g.mu.Lock()
	if g.running {
		g.mu.Unlock()
		return
	}
	g.running = true
	g.mu.Unlock()
	go func() {
		defer func() {
			g.mu.Lock()
			g.running = false
			g.mu.Unlock()
			dropCache()
		}()
		if _, err := graph.BuildGraph(context.Background(), database, dir); err != nil {
			slog.Error("Background graph rebuild failed", "error", err)
		}
	}()
}

// graphStatus brings the graph up to date when that is cheap, and otherwise
// reports why the data being served is old or not there yet:
// "ready", "updating" (serving the previous graph) or "building" (no graph yet).
func graphStatus(ctx context.Context, database *sql.DB, dir string) (string, error) {
	cache.mu.Lock()
	fresh := time.Since(cache.readyAt) < cacheTTL
	cache.mu.Unlock()
	if fresh && !rebuilder.busy() {
		return "ready", nil
	}
	stale, err := graph.IsStale(ctx, database)
	if err != nil {
		return "", err
	}
	if !stale {
		if rebuilder.busy() {
			return "updating", nil
		}
		cache.mu.Lock()
		cache.readyAt = time.Now()
		cache.mu.Unlock()
		return "ready", nil
	}
	var files int
	_ = database.QueryRowContext(ctx, "SELECT COUNT(*) FROM files;").Scan(&files)
	if files <= inlineRebuildMaxFiles && !rebuilder.busy() {
		if err := graph.EnsureFresh(ctx, database, dir); err != nil {
			return "", err
		}
		dropCache()
		return "ready", nil
	}
	rebuilder.start(database, dir)
	var nodes int
	_ = database.QueryRowContext(ctx, "SELECT COUNT(*) FROM graph_nodes;").Scan(&nodes)
	if nodes == 0 {
		return "building", nil
	}
	return "updating", nil
}

// explorerGraph is the graph plus whether it is current.
type explorerGraph struct {
	*graph.GraphSnapshot
	Status string `json:"status"`
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// handleExplorerGraph handles GET /api/explorer/graph?limit=20000
func handleExplorerGraph(database *sql.DB, targetDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 20000
		if lStr := r.URL.Query().Get("limit"); lStr != "" {
			if l, err := strconv.Atoi(lStr); err == nil && l > 0 && l <= 200000 {
				limit = l
			}
		}

		ctx := r.Context()
		status, err := graphStatus(ctx, database, targetDir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		snapshot, err := graph.LoadGraph(ctx, database, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, explorerGraph{GraphSnapshot: snapshot, Status: status})
	}
}

// handleExplorerOverview handles GET /api/explorer/overview: a plain summary of the repository.
func handleExplorerOverview(database *sql.DB, targetDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		status, err := graphStatus(ctx, database, targetDir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		cache.mu.Lock()
		ov := cache.overview
		if ov != nil && time.Since(cache.overviewAt) > cacheTTL*4 {
			ov = nil
		}
		cache.mu.Unlock()
		if ov == nil {
			if ov, err = graph.BuildOverview(ctx, database); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			cache.mu.Lock()
			cache.overview, cache.overviewAt = ov, time.Now()
			cache.mu.Unlock()
		}
		writeJSON(w, struct {
			*graph.Overview
			Status string `json:"status"`
		}{ov, status})
	}
}

// handleExplorerSearch handles GET /api/explorer/search?q=...
func handleExplorerSearch(database *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodes, err := graph.SearchNodes(r.Context(), database, r.URL.Query().Get("q"), 40)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"nodes": nodes})
	}
}

// handleExplorerNode handles GET /api/explorer/node?id=...: one node with its
// direct links, for pages about items that weren't in the loaded graph.
func handleExplorerNode(database *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "missing 'id' query parameter", http.StatusBadRequest)
			return
		}
		snap, err := graph.NodeNeighborhood(r.Context(), database, id)
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "node not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, snap)
	}
}
