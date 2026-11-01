package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/recscse/codive/internal/db"
)

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return newHandler(dir, database)
}

func TestHostCheck(t *testing.T) {
	handler := newTestHandler(t)
	cases := map[string]int{
		"127.0.0.1:7890":              http.StatusOK,
		"localhost:7890":              http.StatusOK,
		"[::1]:7890":                  http.StatusOK,
		"evil.example":                http.StatusForbidden,
		"evil.example:7890":           http.StatusForbidden,
		"127.0.0.1.evil.example:7890": http.StatusForbidden,
	}
	for host, want := range cases {
		req := httptest.NewRequest("GET", "/api/explorer/graph", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Host %q: expected %d, got %d", host, want, rec.Code)
		}
	}
}

func TestExplorerPageServedSelfContained(t *testing.T) {
	handler := newTestHandler(t)
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "127.0.0.1:7890"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("codive explorer")) {
		t.Fatalf("explorer page not served: %d", rec.Code)
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Errorf("missing Content-Security-Policy header")
	}
	external := regexp.MustCompile(`(?i)<(script|link|img)[^>]+(src|href)=["']?(https?:)?//`)
	if m := external.Find(rec.Body.Bytes()); m != nil {
		t.Errorf("page must work offline, found external resource: %s", m)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("innerHTML")) {
		t.Errorf("page must insert repo content as text, found innerHTML")
	}

	req = httptest.NewRequest("GET", "/nope", nil)
	req.Host = "127.0.0.1:7890"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown path: expected 404, got %d", rec.Code)
	}
}

func TestChangesEndpoint(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git unavailable: %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	req := httptest.NewRequest("GET", "/api/explorer/changes", nil)
	req.Host = "127.0.0.1:7890"
	rec := httptest.NewRecorder()
	newHandler(dir, database).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("new.go")) {
		t.Fatalf("expected new.go in changes, got %d: %s", rec.Code, rec.Body.String())
	}
}
