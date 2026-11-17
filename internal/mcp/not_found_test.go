package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/recscse/codive/internal/db"
)

// findSymbolText runs find_symbol through the real request path and returns the
// text of the reply.
func findSymbolText(t *testing.T, dir, query string) string {
	t.Helper()
	database, err := db.Open(filepath.Join(dir, ".codive", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	server := NewServer(dir, database, "test")
	defer server.Close()
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "find_symbol", "arguments": map[string]any{"query": query}}})
	var out bytes.Buffer
	if err := server.Serve(strings.NewReader(string(req)+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Result ToolCallResult `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil || len(resp.Result.Content) == 0 {
		t.Fatalf("bad reply %q: %v", out.String(), err)
	}
	return resp.Result.Content[0].Text
}

func TestFindSymbolMissExplainsWhatWasSearched(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("app.py", "def present_fn(a):\n    return a\n")
	write("legacy.sql", "-- migration\nCREATE PROCEDURE legacy_proc_one AS BEGIN SELECT 1; END;\n")
	indexForTest(t, dir)

	// A name that exists only in a language with no extractor.
	got := findSymbolText(t, dir, "legacy_proc_one")
	t.Logf("reply for a miss in a text-only language:\n%s", got)
	for _, want := range []string{
		"No symbols found matching 'legacy_proc_one'",
		"not proof that the name doesn't exist",
		"SQL (1)", "no symbol extractor",
		"Python (1)",
		"legacy.sql L2", "CREATE PROCEDURE legacy_proc_one",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("reply lacks %q:\n%s", want, got)
		}
	}

	// A name that appears nowhere.
	got = findSymbolText(t, dir, "nowhere_to_be_found")
	if !strings.Contains(got, "does not appear as text in the indexed files either") || strings.Contains(got, " L") && strings.Contains(got, "It does appear as text") {
		t.Errorf("an absent name was reported as present:\n%s", got)
	}

	// A name that exists is answered as before, without the explanation.
	got = findSymbolText(t, dir, "present_fn")
	if !strings.Contains(got, "present_fn") || strings.Contains(got, "not proof") {
		t.Errorf("a normal hit changed:\n%s", got)
	}
}
