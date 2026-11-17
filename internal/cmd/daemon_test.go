package cmd

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/recscse/codive/internal/daemon"
)

// startTestDaemon runs a host behind a real listener, with its files in a
// temporary directory, and returns it.
func startTestDaemon(t *testing.T) *host {
	t.Helper()
	t.Setenv("CODIVE_DAEMON_DIR", t.TempDir())
	ln, err := daemon.Listen("vtest")
	if err != nil {
		t.Fatal(err)
	}
	h := newHost("vtest")
	h.stop = ln.Close
	ln.Session = h.session
	go ln.Serve()
	t.Cleanup(func() {
		ln.Close()
		h.closeAll()
	})
	return h
}

// rpc sends one JSON-RPC request on c and returns the text of the reply.
func rpc(t *testing.T, c net.Conn, r *bufio.Reader, id int, method string, params map[string]any) string {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	_ = c.SetDeadline(time.Now().Add(2 * time.Minute))
	if _, err := c.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	return line
}

func writeRepo(t *testing.T, name, symbol string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "package p\n\nfunc " + symbol + "(a int,\n\tb int) int {\n\treturn a + b\n}\n"
	if err := os.WriteFile(filepath.Join(root, "p.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func findSymbol(t *testing.T, root, symbol string, id int) string {
	t.Helper()
	c, err := daemon.Dial(root, "vtest")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	r := bufio.NewReader(c)
	rpc(t, c, r, 1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}})
	return rpc(t, c, r, id, "tools/call", map[string]any{"name": "find_symbol", "arguments": map[string]any{"query": symbol}})
}

// Several clients on one repository share one open index, the first one builds
// it, and each answer comes from the repository the client named, not from the
// daemon's own working directory.
func TestDaemonServesSeveralRepositoriesAndSharesEachIndex(t *testing.T) {
	h := startTestDaemon(t)
	alpha := writeRepo(t, "alpha", "AlphaOnlyFunc")
	beta := writeRepo(t, "beta", "BetaOnlyFunc")

	// This package's own directory sits inside a repository that has an index;
	// an unpinned server would answer from it.
	if got := findSymbol(t, alpha, "AlphaOnlyFunc", 2); !strings.Contains(got, "AlphaOnlyFunc") || !strings.Contains(got, "p.go") {
		t.Errorf("alpha: the symbol was not found in the repository the client named: %s", got)
	}
	if got := findSymbol(t, beta, "BetaOnlyFunc", 2); !strings.Contains(got, "BetaOnlyFunc") {
		t.Errorf("beta: %s", got)
	}
	if got := findSymbol(t, alpha, "BetaOnlyFunc", 3); strings.Contains(got, "p.go") {
		t.Errorf("alpha answered with a symbol from beta: %s", got)
	}

	if !strings.Contains(h.status(), "2 repositories open") {
		t.Errorf("status = %q, want 2 repositories open", h.status())
	}
	if _, err := os.Stat(filepath.Join(alpha, ".codive", ".gitignore")); err != nil {
		t.Errorf("the index directory does not ignore itself: %v", err)
	}
}

func TestTwoClientsOnOneRepositoryShareOneOpenIndex(t *testing.T) {
	h := startTestDaemon(t)
	root := writeRepo(t, "shared", "SharedFunc")

	open := func() (net.Conn, *bufio.Reader) {
		c, err := daemon.Dial(root, "vtest")
		if err != nil {
			t.Fatal(err)
		}
		r := bufio.NewReader(c)
		rpc(t, c, r, 1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}})
		return c, r
	}
	c1, r1 := open()
	c2, r2 := open()
	defer c1.Close()
	defer c2.Close()

	if !strings.Contains(h.status(), "1 repository open") || !strings.Contains(h.status(), "2 session(s)") {
		t.Errorf("status = %q, want one repository with 2 sessions", h.status())
	}
	for i, p := range []struct {
		c net.Conn
		r *bufio.Reader
	}{{c1, r1}, {c2, r2}} {
		got := rpc(t, p.c, p.r, 2, "tools/call", map[string]any{"name": "find_symbol", "arguments": map[string]any{"query": "SharedFunc"}})
		if !strings.Contains(got, "SharedFunc") {
			t.Errorf("client %d: %s", i+1, got)
		}
	}

	c1.Close()
	c2.Close()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(h.status(), "0 client session(s)") && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(h.status(), "0 client session(s)") {
		t.Errorf("sessions were not released: %s", h.status())
	}
}

func TestDaemonRefusesAWorkspaceItCannotOpen(t *testing.T) {
	startTestDaemon(t)
	for _, root := range []string{"relative/path", filepath.Join(t.TempDir(), "missing")} {
		if c, err := daemon.Dial(root, "vtest"); err == nil {
			c.Close()
			t.Errorf("Dial(%q) succeeded", root)
		}
	}
}
