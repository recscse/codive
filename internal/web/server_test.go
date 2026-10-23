package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleSnippet(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "sample.go")
	content := "package main\n\nfunc Hello() string {\n\treturn \"world\"\n}\n"
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	handler := handleSnippet(tempDir)

	t.Run("valid snippet request", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/snippet?file=sample.go&line=3&radius=2", nil)
		rec := httptest.NewRecorder()
		handler(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp SnippetResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse json: %v", err)
		}

		if resp.FilePath != "sample.go" {
			t.Errorf("expected FilePath 'sample.go', got '%s'", resp.FilePath)
		}
		if resp.TargetLine != 3 {
			t.Errorf("expected TargetLine 3, got %d", resp.TargetLine)
		}
		if resp.TotalLines < 4 {
			t.Errorf("expected total lines at least 4, got %d", resp.TotalLines)
		}
	})

	t.Run("handles ultra-long lines without crashing", func(t *testing.T) {
		// Create a file with a 100KB single line (which would break bufio.Scanner default 64KB limit)
		hugeLineFile := filepath.Join(tempDir, "bundle.min.js")
		longLine := "var bundle={" + strings.Repeat("a:1,", 20000) + "};\n"
		if err := os.WriteFile(hugeLineFile, []byte(longLine), 0644); err != nil {
			t.Fatalf("failed to write huge line file: %v", err)
		}

		req := httptest.NewRequest("GET", "/api/snippet?file=bundle.min.js&line=1", nil)
		rec := httptest.NewRecorder()
		handler(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200 on ultra-long line file, got %d", rec.Code)
		}

		var resp SnippetResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse json: %v", err)
		}

		if !strings.Contains(resp.Content, "[truncated]") {
			t.Errorf("expected long line to be truncated for preview, got length %d", len(resp.Content))
		}
	})

	t.Run("directory traversal prevented", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/snippet?file=../secret.txt", nil)
		rec := httptest.NewRecorder()
		handler(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected status 400 for path traversal, got %d", rec.Code)
		}
	})

	t.Run("missing file param", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/snippet", nil)
		rec := httptest.NewRecorder()
		handler(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected status 400 for missing file, got %d", rec.Code)
		}
	})
}
