package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/graph"
)

func TestMCPExtensions(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer database.Close()

	ctx := context.Background()
	_ = db.ApplyIndexChanges(ctx, database, db.IndexChanges{
		Files: []db.FileRecord{{Path: "app.go", Language: "Go"}},
		Symbols: []db.SymbolRecord{{
			FilePath:   "app.go",
			Name:       "StartServer",
			Kind:       "function",
			LineNumber: 12,
		}},
	})
	_, _ = graph.BuildGraph(ctx, database, tempDir)

	server := NewServer(tempDir, database, "test-version")
	defer server.Close()

	// Test get_codebase_graph
	var out strings.Builder
	graphReq := `{"jsonrpc":"2.0","id":101,"method":"tools/call","params":{"name":"get_codebase_graph","arguments":{"limit":100}}}` + "\n"
	if err := server.Serve(strings.NewReader(graphReq), &out); err != nil {
		t.Fatalf("get_codebase_graph call failed: %v", err)
	}

	var graphResp JSONRPCResponse
	if err := json.Unmarshal([]byte(out.String()), &graphResp); err != nil {
		t.Fatalf("failed to unmarshal graph response: %v", err)
	}
	if graphResp.Error != nil {
		t.Fatalf("get_codebase_graph returned error: %s", graphResp.Error.Message)
	}

	// Test get_execution_flow
	out.Reset()
	flowReq := `{"jsonrpc":"2.0","id":102,"method":"tools/call","params":{"name":"get_execution_flow","arguments":{"node_id":"sym:app.go:StartServer:12"}}}` + "\n"
	if err := server.Serve(strings.NewReader(flowReq), &out); err != nil {
		t.Fatalf("get_execution_flow call failed: %v", err)
	}

	var flowResp JSONRPCResponse
	if err := json.Unmarshal([]byte(out.String()), &flowResp); err != nil {
		t.Fatalf("failed to unmarshal flow response: %v", err)
	}
	if flowResp.Error != nil {
		t.Fatalf("get_execution_flow returned error: %s", flowResp.Error.Message)
	}
}
