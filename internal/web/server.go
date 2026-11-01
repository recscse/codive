// Package web provides the embedded interactive browser-based architecture graph server.
package web

import (
	"bufio"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/recscse/codive/internal/ui"
)

// SnippetResponse contains file excerpt details for the sidebar inspector.
type SnippetResponse struct {
	FilePath   string `json:"file_path"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	TotalLines int    `json:"total_lines"`
	TargetLine int    `json:"target_line"`
	Content    string `json:"content"`
	Language   string `json:"language"`
}

//go:embed explorer.html
var explorerHTML []byte

// StartWebServer starts the local explorer on port (default 7890) and opens the browser.
func StartWebServer(targetDir string, database *sql.DB, port int) error {
	if port <= 0 {
		port = 7890
	}

	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Bind before announcing, so a busy port is reported instead of opening a browser on another server.
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("cannot listen on %s (is codive web already running? try --port): %w", addr, err)
	}

	url := fmt.Sprintf("http://%s", addr)
	ui.Header("🌐 codive explorer")
	fmt.Println()
	fmt.Printf("  %s %s\n\n", ui.Dim.Sprint("URL:        "), ui.CyanBold.Sprint(url))
	ui.Success("Press Ctrl+C in terminal to stop web server.")

	openBrowser(url)

	return http.Serve(listener, newHandler(absDir, database))
}

// newHandler builds the explorer's routes behind its security headers and Host check.
func newHandler(absDir string, database *sql.DB) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(explorerHTML)
	})
	mux.HandleFunc("/api/snippet", handleSnippet(absDir))
	mux.HandleFunc("/api/explorer/flow", handleExplorerFlow(database))
	mux.HandleFunc("/api/explorer/impact", handleExplorerImpact(database))
	mux.HandleFunc("/api/explorer/graph", handleExplorerGraph(database, absDir))
	mux.HandleFunc("/api/explorer/changes", handleExplorerChanges(database, absDir))
	return secureLocal(mux)
}

// secureLocal rejects requests whose Host is not loopback, which stops a
// malicious website from reading source through DNS rebinding, and locks the
// page to its own origin.
func secureLocal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		switch strings.Trim(host, "[]") {
		case "127.0.0.1", "localhost", "::1":
		default:
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func handleSnippet(targetDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		relPath := r.URL.Query().Get("file")
		if relPath == "" {
			http.Error(w, "missing 'file' query parameter", http.StatusBadRequest)
			return
		}

		cleanRel := filepath.Clean(filepath.ToSlash(relPath))
		if strings.HasPrefix(cleanRel, "..") || filepath.IsAbs(cleanRel) {
			http.Error(w, "invalid path traversal", http.StatusBadRequest)
			return
		}

		absPath := filepath.Join(targetDir, filepath.FromSlash(cleanRel))
		f, err := os.Open(absPath)
		if err != nil {
			http.Error(w, fmt.Sprintf("file not found: %v", err), http.StatusNotFound)
			return
		}
		defer f.Close()

		targetLine := 1
		if lStr := r.URL.Query().Get("line"); lStr != "" {
			if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
				targetLine = l
			}
		}

		radius := 18
		if rStr := r.URL.Query().Get("radius"); rStr != "" {
			if rad, err := strconv.Atoi(rStr); err == nil && rad > 0 && rad <= 50 {
				radius = rad
			}
		}

		startLine := targetLine - radius
		if startLine < 1 {
			startLine = 1
		}
		endLine := targetLine + radius

		// Memory-safe streaming reader: works on files of arbitrary size and handles arbitrarily long lines
		reader := bufio.NewReader(f)
		var snippetBuilder strings.Builder
		currentLine := 0
		totalLines := 0

		for {
			line, err := reader.ReadString('\n')
			if len(line) > 0 {
				currentLine++
				totalLines = currentLine
				trimmed := strings.TrimRight(line, "\r\n")
				// Truncate ultra-long lines (e.g. minified JS/JSON) to 600 characters for DOM performance
				if len(trimmed) > 600 {
					trimmed = trimmed[:600] + " ... [truncated]"
				}

				if currentLine >= startLine && currentLine <= endLine {
					if snippetBuilder.Len() > 0 {
						snippetBuilder.WriteByte('\n')
					}
					snippetBuilder.WriteString(trimmed)
				}
			}

			if err != nil {
				break
			}

			// Don't keep reading if the file is massive (stop 200 lines after endLine)
			if currentLine > endLine+200 {
				totalLines = currentLine
				break
			}
		}

		if startLine > totalLines && totalLines > 0 {
			startLine = totalLines
		}
		if endLine > totalLines && totalLines > 0 {
			endLine = totalLines
		}

		ext := strings.TrimPrefix(filepath.Ext(absPath), ".")

		resp := SnippetResponse{
			FilePath:   cleanRel,
			StartLine:  startLine,
			EndLine:    endLine,
			TotalLines: totalLines,
			TargetLine: targetLine,
			Content:    snippetBuilder.String(),
			Language:   ext,
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
