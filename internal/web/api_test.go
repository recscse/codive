package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/graph"
)

func TestExplorerAPI(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer database.Close()

	ctx := t.Context()
	_ = db.ApplyIndexChanges(ctx, database, db.IndexChanges{
		Files: []db.FileRecord{{Path: "main.go", Language: "Go"}},
		Symbols: []db.SymbolRecord{{
			FilePath:   "main.go",
			Name:       "main",
			Kind:       "function",
			LineNumber: 10,
		}},
	})

	_, err = graph.BuildGraph(ctx, database, tempDir)
	if err != nil {
		t.Fatalf("BuildGraph failed: %v", err)
	}

	// 1. Test /api/explorer/graph
	req := httptest.NewRequest("GET", "/api/explorer/graph", nil)
	rec := httptest.NewRecorder()
	handleExplorerGraph(database, tempDir)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from /api/explorer/graph, got %d", rec.Code)
	}
	var snapshot graph.GraphSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("failed to decode graph snapshot: %v", err)
	}
	if snapshot.TotalNodes == 0 {
		t.Errorf("expected nodes in graph snapshot, got 0")
	}

	// 2. Test /api/explorer/flow
	nodeID := "sym:main.go:main:10"
	reqFlow := httptest.NewRequest("GET", "/api/explorer/flow?node_id="+nodeID, nil)
	recFlow := httptest.NewRecorder()
	handleExplorerFlow(database)(recFlow, reqFlow)

	if recFlow.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from /api/explorer/flow, got %d", recFlow.Code)
	}
	var flow graph.ExecutionFlow
	if err := json.Unmarshal(recFlow.Body.Bytes(), &flow); err != nil {
		t.Fatalf("failed to decode execution flow: %v", err)
	}
	if flow.EntryPoint.ID != nodeID {
		t.Errorf("expected flow entry point %s, got %s", nodeID, flow.EntryPoint.ID)
	}

	// 3. Test /api/explorer/impact
	reqImpact := httptest.NewRequest("GET", "/api/explorer/impact?node_id="+nodeID, nil)
	recImpact := httptest.NewRecorder()
	handleExplorerImpact(database)(recImpact, reqImpact)

	if recImpact.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from /api/explorer/impact, got %d", recImpact.Code)
	}
	var impact graph.ImpactReport
	if err := json.Unmarshal(recImpact.Body.Bytes(), &impact); err != nil {
		t.Fatalf("failed to decode impact report: %v", err)
	}
	if impact.TargetNode.ID != nodeID {
		t.Errorf("expected target node %s, got %s", nodeID, impact.TargetNode.ID)
	}
}
