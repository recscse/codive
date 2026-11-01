package graph

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/recscse/codive/internal/db"
)

func TestBuildGraphAndTraversal(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer database.Close()

	ctx := context.Background()

	// 1. Insert dummy files and symbols into SQLite via ApplyIndexChanges
	err = db.ApplyIndexChanges(ctx, database, db.IndexChanges{
		Files: []db.FileRecord{
			{
				Path:     "cmd/api.go",
				Language: "Go",
			},
			{
				Path:     "pkg/service.go",
				Language: "Go",
			},
			{
				Path:     "pkg/service_test.go",
				Language: "Go",
			},
		},
		Symbols: []db.SymbolRecord{
			{
				FilePath:   "cmd/api.go",
				Name:       "HandleRequest",
				Kind:       "function",
				LineNumber: 15,
			},
			{
				FilePath:   "pkg/service.go",
				Name:       "ProcessData",
				Kind:       "function",
				LineNumber: 25,
			},
			{
				FilePath:   "pkg/service_test.go",
				Name:       "TestProcessData",
				Kind:       "function",
				LineNumber: 10,
			},
		},
	})
	if err != nil {
		t.Fatalf("failed to apply test index changes: %v", err)
	}

	// 2. Build graph
	snapshot, err := BuildGraph(ctx, database, tempDir)
	if err != nil {
		t.Fatalf("BuildGraph failed: %v", err)
	}

	if snapshot.TotalNodes == 0 {
		t.Fatalf("expected non-zero total nodes, got %d", snapshot.TotalNodes)
	}

	// 3. Verify LoadGraph
	loaded, err := LoadGraph(ctx, database, 100)
	if err != nil {
		t.Fatalf("LoadGraph failed: %v", err)
	}
	if len(loaded.Nodes) != snapshot.TotalNodes {
		t.Errorf("loaded nodes mismatch: expected %d, got %d", snapshot.TotalNodes, len(loaded.Nodes))
	}

	// 4. Test Execution Flow from HandleRequest
	startID := "sym:cmd/api.go:HandleRequest:15"
	flow, err := TraverseExecutionFlow(ctx, database, startID, 3)
	if err != nil {
		t.Fatalf("TraverseExecutionFlow failed: %v", err)
	}
	if flow.EntryPoint.ID != startID {
		t.Errorf("expected entry point %s, got %s", startID, flow.EntryPoint.ID)
	}
	if len(flow.Steps) == 0 {
		t.Errorf("expected at least 1 step in flow, got 0")
	}

	// 5. Test Impact Analysis for ProcessData
	targetID := "sym:pkg/service.go:ProcessData:25"
	impact, err := AnalyzeImpact(ctx, database, targetID)
	if err != nil {
		t.Fatalf("AnalyzeImpact failed: %v", err)
	}
	if impact.TargetNode.ID != targetID {
		t.Errorf("expected target %s, got %s", targetID, impact.TargetNode.ID)
	}
	if len(impact.TestsToRun) == 0 {
		t.Logf("no explicit tests found via edge (expected in synthetic mock without file on disk)")
	}
}
