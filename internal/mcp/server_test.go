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

	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/scanner"
	"github.com/recscse/codive/internal/symbols"
)

func setupTestDB(t *testing.T) (string, func()) {
	tempDir, err := os.MkdirTemp("", "codive_mcp_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(tempDir, ".codive", "index.db")
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

	dbPath := filepath.Join(tempDir, ".codive", "index.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer database.Close()

	server := NewServer(tempDir, database, "v1.1.0-test")

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
	if !strings.Contains(out.String(), "// File: main.go") {
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

// indexForTest builds a real index for dir the same way init does, so file
// records carry genuine sizes, mtimes, and content hashes.
func indexForTest(t *testing.T, dir string) {
	t.Helper()
	res, err := scanner.Scan(dir)
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	database, err := db.Open(filepath.Join(dir, ".codive", "index.db"))
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer database.Close()
	ctx := context.Background()
	if err := db.SaveFiles(ctx, database, res.Files); err != nil {
		t.Fatalf("failed to save files: %v", err)
	}
	fts := make(map[string]string)
	for _, f := range res.Files {
		content, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if err != nil {
			t.Fatalf("failed to read %s: %v", f.Path, err)
		}
		fts[f.Path] = string(content)
		syms, _ := symbols.ExtractSymbols(f.Path, f.Language, content)
		if err := db.SaveSymbols(ctx, database, syms); err != nil {
			t.Fatalf("failed to save symbols: %v", err)
		}
	}
	if err := db.SaveFTS(ctx, database, fts); err != nil {
		t.Fatalf("failed to save fts: %v", err)
	}
}

func TestEnsureFreshSymbols(t *testing.T) {
	tempDir := t.TempDir()
	srcPath := filepath.Join(tempDir, "a.go")
	if err := os.WriteFile(srcPath, []byte("package p\n\nfunc Alpha() {}\n"), 0644); err != nil {
		t.Fatalf("failed to write a.go: %v", err)
	}
	indexForTest(t, tempDir)

	// An ignored file that exists on disk but was never indexed.
	if err := os.MkdirAll(filepath.Join(tempDir, "node_modules"), 0755); err != nil {
		t.Fatalf("failed to create node_modules: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "node_modules", "x.js"), []byte("const SECRETVALUE = 1\n"), 0644); err != nil {
		t.Fatalf("failed to write x.js: %v", err)
	}

	database, err := db.Open(filepath.Join(tempDir, ".codive", "index.db"))
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer database.Close()
	server := NewServer(tempDir, database, "test")
	defer server.Close()
	ctx := context.Background()
	call := func(name string, args map[string]any) string {
		t.Helper()
		args["workspace_path"] = tempDir
		res, err := server.executeTool(ctx, name, args)
		if err != nil {
			t.Fatalf("%s failed: %v", name, err)
		}
		return res.Content[0].Text
	}

	t.Run("unindexed file is not pulled into the index", func(t *testing.T) {
		call("read_file_context", map[string]any{"path": "node_modules/x.js"})
		if _, ok, _ := db.GetFile(ctx, database, "node_modules/x.js"); ok {
			t.Error("read_file_context indexed an ignored file")
		}
		if hits, _ := db.SearchFTS(ctx, database, "SECRETVALUE", 5); len(hits) != 0 {
			t.Errorf("ignored file content leaked into FTS: %+v", hits)
		}
	})

	t.Run("unchanged file is not rewritten", func(t *testing.T) {
		before, _, _ := db.GetFile(ctx, database, "a.go")
		call("find_symbol", map[string]any{"query": "Alpha"})
		after, _, _ := db.GetFile(ctx, database, "a.go")
		if !after.LastIndexed.Equal(before.LastIndexed) {
			t.Errorf("unchanged file was re-indexed: LastIndexed %v -> %v", before.LastIndexed, after.LastIndexed)
		}
	})

	t.Run("modified file is refreshed before results are printed", func(t *testing.T) {
		newContent := []byte("package p\n\n\n\n\nfunc Alpha() {}\n")
		if err := os.WriteFile(srcPath, newContent, 0644); err != nil {
			t.Fatalf("failed to rewrite a.go: %v", err)
		}
		future := time.Now().Add(time.Minute)
		if err := os.Chtimes(srcPath, future, future); err != nil {
			t.Fatalf("failed to bump mtime: %v", err)
		}

		out := call("find_symbol", map[string]any{"query": "Alpha"})
		if !strings.Contains(out, "(L6)") || strings.Contains(out, "(L3)") {
			t.Errorf("expected only the refreshed location L6, got:\n%s", out)
		}
		rec, _, _ := db.GetFile(ctx, database, "a.go")
		if rec.ContentHash != scanner.HashBytes(newContent) {
			t.Errorf("refreshed record has stale/empty content hash %q", rec.ContentHash)
		}
	})
}

func TestNegotiateProtocolVersion(t *testing.T) {
	for requested, want := range map[string]string{
		"2024-11-05": "2024-11-05",
		"2025-03-26": "2025-03-26",
		"2025-06-18": "2025-06-18",
		"1999-01-01": supportedProtocolVersions[0],
		"":           supportedProtocolVersions[0],
	} {
		if got := negotiateProtocolVersion(requested); got != want {
			t.Errorf("negotiateProtocolVersion(%q) = %q, want %q", requested, got, want)
		}
	}
}

func TestSelectLines(t *testing.T) {
	content := "l1\nl2\nl3\nl4\nl5"
	tests := []struct {
		start, end, limit int
		body              string
		partial, wantErr  bool
	}{
		{0, 0, 10, "l1\nl2\nl3\nl4\nl5", false, false},
		{0, 0, 2, "l1\nl2", true, false},
		{2, 3, 10, "l2\nl3", true, false},
		{4, 99, 10, "l4\nl5", true, false},
		{3, 0, 2, "l3\nl4", true, false},
		{6, 0, 10, "", false, true},
		{3, 2, 10, "", false, true},
	}
	for _, tt := range tests {
		body, note, err := selectLines(content, tt.start, tt.end, tt.limit)
		if (err != nil) != tt.wantErr {
			t.Errorf("selectLines(%d,%d,%d) err = %v, wantErr %v", tt.start, tt.end, tt.limit, err, tt.wantErr)
			continue
		}
		if tt.wantErr {
			continue
		}
		if body != tt.body || (note != "") != tt.partial {
			t.Errorf("selectLines(%d,%d,%d) = %q (note %q), want %q partial=%v", tt.start, tt.end, tt.limit, body, note, tt.body, tt.partial)
		}
	}
}

// An agent-supplied workspace_path that has no index must only be
// auto-indexed when it is a git repository root.
func TestAutoIndexRequiresGitRepo(t *testing.T) {
	plain := t.TempDir()
	if err := os.WriteFile(filepath.Join(plain, "x.go"), []byte("package x\n"), 0644); err != nil {
		t.Fatalf("failed to write x.go: %v", err)
	}
	server := NewServer(t.TempDir(), nil, "test")
	defer server.Close()

	if _, err := server.executeTool(context.Background(), "find_symbol", map[string]any{"query": "x", "workspace_path": plain}); err == nil {
		t.Error("expected a non-git directory to be refused for auto-indexing")
	}
	if _, err := os.Stat(filepath.Join(plain, ".codive")); !os.IsNotExist(err) {
		t.Error("refused directory still got a .codive index created in it")
	}

	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatalf("failed to create .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "y.go"), []byte("package y\n\nfunc Yes() {}\n"), 0644); err != nil {
		t.Fatalf("failed to write y.go: %v", err)
	}
	res, err := server.executeTool(context.Background(), "find_symbol", map[string]any{"query": "Yes", "workspace_path": repo})
	if err != nil {
		t.Fatalf("expected git repo to be auto-indexed: %v", err)
	}
	if !strings.Contains(res.Content[0].Text, "y.go") {
		t.Errorf("auto-indexed repo did not return its symbol: %s", res.Content[0].Text)
	}
}

// MCP 2025-03-26 requires accepting JSON-RPC batches: requests in an array get
// an array of responses, and notifications inside a batch get none.
func TestServeBatch(t *testing.T) {
	server := NewServer(t.TempDir(), nil, "test")
	defer server.Close()

	in := `[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","id":2,"method":"ping"}]` + "\n" +
		`[{"jsonrpc":"2.0","method":"notifications/initialized"}]` + "\n" +
		`[]` + "\n"
	var out bytes.Buffer
	if err := server.Serve(strings.NewReader(in), &out); err != nil {
		t.Fatalf("serve failed: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 output lines (batch reply + empty-batch error), got %d:\n%s", len(lines), out.String())
	}
	var batch []JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[0]), &batch); err != nil {
		t.Fatalf("batch reply is not an array: %v\n%s", err, lines[0])
	}
	if len(batch) != 2 || batch[0].ID != float64(1) || batch[1].ID != float64(2) {
		t.Errorf("expected responses for ids 1 and 2 only, got %+v", batch)
	}
	var empty JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[1]), &empty); err != nil || empty.Error == nil || empty.Error.Code != -32600 {
		t.Errorf("expected -32600 for an empty batch, got %s", lines[1])
	}
}
