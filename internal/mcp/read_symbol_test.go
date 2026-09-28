package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readSymbolFixture(t *testing.T) (string, func(args map[string]any) string) {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"server.go": `package app

type Server struct{}

// Start boots the server.
func (s *Server) Start(port int) error {
	if port == 0 {
		return nil
	}
	return nil
}

func unrelated() {}
`,
		"client.go": `package app

type Client struct{}

// Start connects the client.
func (c *Client) Start() error { return nil }
`,
		"svc.py": `class Billing:
    def charge(self, amount):
        return amount * 2

class Refunds:
    def charge(self, amount):
        return -amount
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0644); err != nil {
			t.Fatal(err)
		}
	}
	indexForTest(t, dir)
	server := NewServer(dir, nil, "test")
	t.Cleanup(server.Close)
	return dir, func(args map[string]any) string {
		t.Helper()
		args["workspace_path"] = dir
		res, err := server.executeTool(context.Background(), "read_symbol", args)
		if err != nil {
			t.Fatalf("read_symbol(%v) failed: %v", args, err)
		}
		return res.Content[0].Text
	}
}

func TestReadSymbolReturnsOnlyTheDefinition(t *testing.T) {
	_, call := readSymbolFixture(t)
	out := call(map[string]any{"symbol": "Server.Start"})
	if !strings.Contains(out, "// Start boots the server.") || !strings.Contains(out, "return nil\n}") {
		t.Errorf("missing doc comment or body:\n%s", out)
	}
	if strings.Contains(out, "unrelated") || strings.Contains(out, "connects the client") {
		t.Errorf("returned code outside the definition:\n%s", out)
	}
	if !strings.Contains(out, "`server.go` L5-11") {
		t.Errorf("missing location header:\n%s", out)
	}
}

func TestReadSymbolDisambiguation(t *testing.T) {
	_, call := readSymbolFixture(t)

	both := call(map[string]any{"symbol": "Start"})
	if !strings.Contains(both, "boots the server") || !strings.Contains(both, "connects the client") {
		t.Errorf("bare name should return both definitions:\n%s", both)
	}
	one := call(map[string]any{"symbol": "Start", "limit": float64(1)})
	if !strings.Contains(one, "1 more definition(s)") || !strings.Contains(one, "pass `path`") {
		t.Errorf("capped result should list the rest:\n%s", one)
	}
	if out := call(map[string]any{"symbol": "client.go:Start"}); strings.Contains(out, "boots the server") || !strings.Contains(out, "connects the client") {
		t.Errorf("path:Name should pick client.go:\n%s", out)
	}
	py := call(map[string]any{"symbol": "Refunds.charge"})
	if !strings.Contains(py, "return -amount") || strings.Contains(py, "amount * 2") {
		t.Errorf("Class.method should pick the Refunds method:\n%s", py)
	}
}

func TestReadSymbolMissSuggestsNames(t *testing.T) {
	_, call := readSymbolFixture(t)
	out := call(map[string]any{"symbol": "Star"})
	if !strings.Contains(out, "No definition named `Star`") || !strings.Contains(out, "`Start`") {
		t.Errorf("miss should suggest similar names:\n%s", out)
	}
}

// Edits made after indexing must be reflected immediately.
func TestReadSymbolIsFresh(t *testing.T) {
	dir, call := readSymbolFixture(t)
	path := filepath.Join(dir, "server.go")
	src, _ := os.ReadFile(path)
	updated := strings.Replace(string(src), "if port == 0 {", "// freshly edited\n\tif port < 0 {", 1)
	if err := os.WriteFile(path, []byte("\n\n"+updated), 0644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Minute)
	_ = os.Chtimes(path, future, future)

	out := call(map[string]any{"symbol": "Server.Start"})
	if !strings.Contains(out, "freshly edited") || !strings.Contains(out, "L7-14") {
		t.Errorf("edit not reflected (expected new body at L7-14):\n%s", out)
	}
}

func TestReadSymbolCapsHugeDefinitions(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	sb.WriteString("package big\n\nfunc Huge() {\n")
	for i := 0; i < maxSymbolLines+100; i++ {
		sb.WriteString(fmt.Sprintf("\t_ = %d\n", i))
	}
	sb.WriteString("}\n")
	if err := os.WriteFile(filepath.Join(dir, "big.go"), []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}
	indexForTest(t, dir)
	server := NewServer(dir, nil, "test")
	defer server.Close()
	res, err := server.executeTool(context.Background(), "read_symbol", map[string]any{"symbol": "Huge", "workspace_path": dir})
	if err != nil {
		t.Fatal(err)
	}
	out := res.Content[0].Text
	if !strings.Contains(out, fmt.Sprintf("Showing the first %d", maxSymbolLines)) || !strings.Contains(out, fmt.Sprintf("start_line=%d", 3+maxSymbolLines)) {
		t.Errorf("huge definition not capped with a continuation hint:\n%.300s", out[len(out)-300:])
	}
}

func TestParseSymbolQuery(t *testing.T) {
	tests := []struct {
		in, path string
		want     symbolQuery
	}{
		{"Start", "", symbolQuery{name: "Start"}},
		{"Server.Start", "", symbolQuery{qualifier: "Server", name: "Start"}},
		{"pkg.Server.Start", "", symbolQuery{qualifier: "Server", name: "Start"}},
		{"Vec::push", "", symbolQuery{qualifier: "Vec", name: "push"}},
		{"internal/app/server.go:Start", "", symbolQuery{path: "internal/app/server.go", name: "Start"}},
		{"Start", "./a/b.go", symbolQuery{path: "a/b.go", name: "Start"}},
	}
	for _, tt := range tests {
		if got := parseSymbolQuery(tt.in, tt.path); got != tt.want {
			t.Errorf("parseSymbolQuery(%q, %q) = %+v, want %+v", tt.in, tt.path, got, tt.want)
		}
	}
}

func TestGoReceiverType(t *testing.T) {
	for sig, want := range map[string]string{
		"func (s *Server) Start(port int) error":             "Server",
		"func (Server) Name() string":                        "Server",
		"func (l *List[T]) Push(v T)":                        "List",
		"func (m moveToFrontDecoder) Decode(n int) (b byte)": "moveToFrontDecoder",
	} {
		if got, ok := goReceiverType(sig); !ok || got != want {
			t.Errorf("goReceiverType(%q) = %q, %v; want %q", sig, got, ok, want)
		}
	}
	for _, sig := range []string{"func Start()", "def charge(self, amount):", "public void run()"} {
		if _, ok := goReceiverType(sig); ok {
			t.Errorf("goReceiverType(%q) should not match", sig)
		}
	}
}

// A qualifier must match the receiver type exactly, not as a substring.
func TestReadSymbolQualifierIsExact(t *testing.T) {
	dir := t.TempDir()
	src := "package p\n\ntype Decoder struct{}\n\ntype moveToFrontDecoder []byte\n\nfunc (m moveToFrontDecoder) Decode(n int) byte { return 0 }\n\n// Decode reads the next value.\nfunc (d *Decoder) Decode(v any) error { return nil }\n"
	if err := os.WriteFile(filepath.Join(dir, "d.go"), []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	indexForTest(t, dir)
	server := NewServer(dir, nil, "test")
	defer server.Close()
	res, err := server.executeTool(context.Background(), "read_symbol", map[string]any{"symbol": "Decoder.Decode", "workspace_path": dir})
	if err != nil {
		t.Fatal(err)
	}
	out := res.Content[0].Text
	if !strings.Contains(out, "reads the next value") || strings.Contains(out, "moveToFrontDecoder) Decode") {
		t.Errorf("wrong method returned for Decoder.Decode:\n%s", out)
	}
}
