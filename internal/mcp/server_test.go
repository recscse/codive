package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/recscse/devctx/internal/db"
)

func setupTestDB(t *testing.T) (string, func()) {
	tempDir, err := os.MkdirTemp("", "ctxd_mcp_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(tempDir, ".devctx", "index.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}

	if err := db.InitSchema(database); err != nil {
		t.Fatalf("failed to init schema: %v", err)
	}

	ctx := context.Background()
	now := time.Now().UTC()

	// Seed files
	sampleFiles := []db.FileRecord{
		{
			Path:         "main.go",
			Language:     "Go",
			SizeBytes:    100,
			ContentHash:  "hash1",
			LastModified: now,
			LastIndexed:  now,
		},
	}
	if err := db.SaveFiles(ctx, database, sampleFiles); err != nil {
		t.Fatalf("failed to seed files: %v", err)
	}

	// Seed symbols
	sampleSymbols := []db.SymbolRecord{
		{
			FilePath:   "main.go",
			Name:       "StartServer",
			Kind:       "function",
			Signature:  "func StartServer() error",
			LineNumber: 10,
		},
	}
	if err := db.SaveSymbols(ctx, database, sampleSymbols); err != nil {
		t.Fatalf("failed to seed symbols: %v", err)
	}

	// Seed FTS
	ftsData := map[string]string{
		"main.go": "package main\n\nfunc StartServer() error {\n\treturn nil\n}\n\nfunc Run() {\n\tStartServer()\n}\n",
	}
	if err := db.SaveFTS(ctx, database, ftsData); err != nil {
		t.Fatalf("failed to seed fts: %v", err)
	}

	// Write file on disk
	os.WriteFile(filepath.Join(tempDir, "main.go"), []byte(ftsData["main.go"]), 0644)

	database.Close()

	cleanup := func() {
		os.RemoveAll(tempDir)
	}
	return tempDir, cleanup
}

func TestMCPServer(t *testing.T) {
	tempDir, cleanup := setupTestDB(t)
	defer cleanup()

	dbPath := filepath.Join(tempDir, ".devctx", "index.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer database.Close()

	server := NewServer(tempDir, database)

	// 1. Test initialize
	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n"
	var out bytes.Buffer
	if err := server.Serve(strings.NewReader(initReq), &out); err != nil {
		t.Fatalf("serve failed: %v", err)
	}

	var initResp JSONRPCResponse
	if err := json.Unmarshal(out.Bytes(), &initResp); err != nil {
		t.Fatalf("failed to unmarshal init response: %v", err)
	}
	if initResp.Error != nil {
		t.Fatalf("unexpected init error: %v", initResp.Error)
	}

	// 2. Test tools/list (should now return 10 tools)
	out.Reset()
	listReq := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
	if err := server.Serve(strings.NewReader(listReq), &out); err != nil {
		t.Fatalf("serve failed: %v", err)
	}

	var listResp struct {
		Result struct {
			Tools []Tool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &listResp); err != nil {
		t.Fatalf("failed to unmarshal tools/list response: %v", err)
	}
	if len(listResp.Result.Tools) != 14 {
		t.Errorf("expected 14 tools, got %d", len(listResp.Result.Tools))
	}

	// Test save_decision and get_decisions
	out.Reset()
	saveReq := `{"jsonrpc":"2.0","id":15,"method":"tools/call","params":{"name":"save_decision","arguments":{"topic":"database","summary":"Use WAL mode always"}}}` + "\n"
	if err := server.Serve(strings.NewReader(saveReq), &out); err != nil {
		t.Fatalf("serve failed: %v", err)
	}
	if !strings.Contains(out.String(), "Recorded architectural decision") {
		t.Errorf("expected save_decision success, got %s", out.String())
	}

	out.Reset()
	getDecReq := `{"jsonrpc":"2.0","id":16,"method":"tools/call","params":{"name":"get_decisions","arguments":{"topic":"database"}}}` + "\n"
	if err := server.Serve(strings.NewReader(getDecReq), &out); err != nil {
		t.Fatalf("serve failed: %v", err)
	}
	if !strings.Contains(out.String(), "Use WAL mode always") {
		t.Errorf("expected get_decisions to find decision, got %s", out.String())
	}

	// Test get_file_skeleton
	out.Reset()
	skelReq := `{"jsonrpc":"2.0","id":20,"method":"tools/call","params":{"name":"get_file_skeleton","arguments":{"path":"main.go"}}}` + "\n"
	if err := server.Serve(strings.NewReader(skelReq), &out); err != nil {
		t.Fatalf("serve failed: %v", err)
	}
	if !strings.Contains(out.String(), "File Skeleton") {
		t.Errorf("expected get_file_skeleton output, got %s", out.String())
	}

	// 3. Test tools/call get_repo_map
	out.Reset()
	mapReq := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_repo_map","arguments":{"max_depth":2}}}` + "\n"
	if err := server.Serve(strings.NewReader(mapReq), &out); err != nil {
		t.Fatalf("serve failed: %v", err)
	}
	if !strings.Contains(out.String(), "StartServer") {
		t.Errorf("expected repo map to contain StartServer, got %s", out.String())
	}

	// 4. Test tools/call find_symbol
	out.Reset()
	findReq := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"find_symbol","arguments":{"query":"StartServer"}}}` + "\n"
	if err := server.Serve(strings.NewReader(findReq), &out); err != nil {
		t.Fatalf("serve failed: %v", err)
	}
	if !strings.Contains(out.String(), "StartServer") {
		t.Errorf("expected symbol search to find StartServer, got %s", out.String())
	}

	// 5. Test tools/call find_references
	out.Reset()
	refReq := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"find_references","arguments":{"symbol":"StartServer"}}}` + "\n"
	if err := server.Serve(strings.NewReader(refReq), &out); err != nil {
		t.Fatalf("serve failed: %v", err)
	}
	if !strings.Contains(out.String(), "StartServer") {
		t.Errorf("expected find_references to find call sites, got %s", out.String())
	}

	// 6. Test tools/call search_code
	out.Reset()
	searchReq := `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"search_code","arguments":{"query":"StartServer"}}}` + "\n"
	if err := server.Serve(strings.NewReader(searchReq), &out); err != nil {
		t.Fatalf("serve failed: %v", err)
	}
	if !strings.Contains(out.String(), "main.go") {
		t.Errorf("expected code search to match main.go, got %s", out.String())
	}

	// 7. Test tools/call read_file_context
	out.Reset()
	readReq := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"read_file_context","arguments":{"path":"main.go"}}}` + "\n"
	if err := server.Serve(strings.NewReader(readReq), &out); err != nil {
		t.Fatalf("serve failed: %v", err)
	}
	if !strings.Contains(out.String(), "package main") {
		t.Errorf("expected read_file_context to return file content, got %s", out.String())
	}
}
