// Package web provides the embedded interactive browser-based architecture graph server.
package web

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/ui"
)

// GraphData represents the network nodes and edges for the web map.
type GraphData struct {
	TotalFiles       int         `json:"total_files"`
	TotalSymbols     int         `json:"total_symbols"`
	TruncatedSymbols bool        `json:"truncated_symbols,omitempty"`
	Directories      []string    `json:"directories"`
	Languages        []string    `json:"languages"`
	Nodes        []GraphNode `json:"nodes"`
	Edges        []GraphEdge `json:"edges"`
}

// GraphNode is a file or symbol in the visualization.
type GraphNode struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Kind      string `json:"kind"` // "file", "function", "method", "struct", "class", "interface", etc.
	Language  string `json:"language,omitempty"`
	File      string `json:"file"`
	Directory string `json:"directory"`
	Line      int    `json:"line,omitempty"`
	Signature string `json:"signature,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

// GraphEdge connects symbols to files or callers to callees.
type GraphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

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

// StartWebServer starts a local web server at port 7890 and opens the browser.
func StartWebServer(targetDir string, database *sql.DB, port int) error {
	if port <= 0 {
		port = 7890
	}

	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	ctx := context.Background()
	allFiles, err := db.GetAllFiles(ctx, database)
	if err != nil {
		return fmt.Errorf("failed to retrieve files: %w", err)
	}
	allSymbols, err := db.GetAllSymbols(ctx, database)
	if err != nil {
		return fmt.Errorf("failed to retrieve symbols: %w", err)
	}

	// Safety cap: on massive repos (10,000+ symbols), visualizer caps at 3,000 to prevent browser memory exhaustion
	const maxVisualSymbols = 3000
	truncated := false
	symbolsToRender := allSymbols
	if len(allSymbols) > maxVisualSymbols {
		symbolsToRender = allSymbols[:maxVisualSymbols]
		truncated = true
	}

	var nodes []GraphNode
	var edges []GraphEdge
	nodeMap := make(map[string]bool)
	dirSet := make(map[string]bool)
	langSet := make(map[string]bool)

	// Add file nodes
	for path, rec := range allFiles {
		fileID := "file:" + path
		dir := filepath.Dir(filepath.ToSlash(path))
		if dir == "." {
			dir = "(root)"
		}
		dirSet[dir] = true
		if rec.Language != "" {
			langSet[rec.Language] = true
		}

		nodes = append(nodes, GraphNode{
			ID:        fileID,
			Label:     filepath.Base(path),
			Kind:      "file",
			Language:  rec.Language,
			File:      path,
			Directory: dir,
			SizeBytes: rec.SizeBytes,
		})
		nodeMap[fileID] = true
	}

	// Add symbol nodes and link to their file
	for _, sym := range symbolsToRender {
		symID := fmt.Sprintf("sym:%s:%s:%d", sym.FilePath, sym.Name, sym.LineNumber)
		dir := filepath.Dir(filepath.ToSlash(sym.FilePath))
		if dir == "." {
			dir = "(root)"
		}
		if !nodeMap[symID] {
			nodes = append(nodes, GraphNode{
				ID:        symID,
				Label:     sym.Name,
				Kind:      sym.Kind,
				File:      sym.FilePath,
				Directory: dir,
				Line:      sym.LineNumber,
				Signature: sym.Signature,
			})
			nodeMap[symID] = true
		}
		fileID := "file:" + sym.FilePath
		if nodeMap[fileID] {
			edges = append(edges, GraphEdge{
				Source: fileID,
				Target: symID,
			})
		}
	}

	// Sort directories and languages for filters
	var dirList []string
	for d := range dirSet {
		dirList = append(dirList, d)
	}
	sort.Strings(dirList)

	var langList []string
	for l := range langSet {
		langList = append(langList, l)
	}
	sort.Strings(langList)

	graphData := GraphData{
		TotalFiles:       len(allFiles),
		TotalSymbols:     len(allSymbols),
		TruncatedSymbols: truncated,
		Directories:      dirList,
		Languages:        langList,
		Nodes:            nodes,
		Edges:            edges,
	}

	graphJSON, err := json.Marshal(graphData)
	if err != nil {
		return fmt.Errorf("failed to encode graph json: %w", err)
	}

	htmlContent := strings.Replace(htmlTemplate, "/* __GRAPH_DATA__ */", string(graphJSON), 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(htmlContent))
	})

	mux.HandleFunc("/api/graph", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(graphJSON)
	})

	mux.HandleFunc("/api/snippet", handleSnippet(absDir))

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	url := fmt.Sprintf("http://%s", addr)

	ui.Header("🌐 codive Web Architecture Map Launched")
	fmt.Println()
	fmt.Printf("  %s %s\n", ui.Dim.Sprint("URL:        "), ui.CyanBold.Sprint(url))
	if truncated {
		fmt.Printf("  %s %d files, %d symbols (rendering top %d for performance)\n\n",
			ui.Dim.Sprint("Graph Data: "), len(allFiles), len(allSymbols), maxVisualSymbols)
	} else {
		fmt.Printf("  %s %d files, %d declared symbols across %d directories\n\n",
			ui.Dim.Sprint("Graph Data: "), len(allFiles), len(allSymbols), len(dirList))
	}
	ui.Success("Press Ctrl+C in terminal to stop web server.")

	// Open browser automatically
	openBrowser(url)

	return http.ListenAndServe(addr, mux)
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

const htmlTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>codive — Architecture Network Map</title>
  <style>
    :root {
      --bg: #0d1117;
      --card-bg: #161b22;
      --border: #30363d;
      --text: #c9d1d9;
      --text-dim: #8b949e;
      --text-bright: #ffffff;
      --accent: #58a6ff;
      --accent-dim: rgba(88, 166, 255, 0.15);
      --green: #3fb950;
      --green-dim: rgba(63, 185, 80, 0.15);
      --purple: #bc8cff;
      --purple-dim: rgba(188, 140, 255, 0.15);
      --orange: #f0883e;
      --orange-dim: rgba(240, 136, 62, 0.15);
      --yellow: #d29922;
      --yellow-dim: rgba(210, 153, 34, 0.15);
      --red: #f85149;
    }

    * { box-sizing: border-box; margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; }
    body { background: var(--bg); color: var(--text); overflow: hidden; height: 100vh; display: flex; flex-direction: column; user-select: none; }

    header {
      background: var(--card-bg);
      border-bottom: 1px solid var(--border);
      padding: 10px 20px;
      display: flex;
      justify-content: space-between;
      align-items: center;
      gap: 16px;
      z-index: 20;
    }

    .brand-section {
      display: flex;
      align-items: center;
      gap: 10px;
      flex-shrink: 0;
    }
    .brand-title {
      font-weight: 700;
      font-size: 16px;
      color: var(--text-bright);
      display: flex;
      align-items: center;
      gap: 6px;
    }
    .brand-title .brand-accent { color: var(--accent); }
    .badge-pill {
      font-size: 11px;
      padding: 2px 8px;
      border-radius: 12px;
      background: #21262d;
      color: var(--text-dim);
      font-weight: 500;
    }

    .header-controls {
      display: flex;
      align-items: center;
      gap: 10px;
      flex: 1;
      justify-content: flex-end;
    }

    .search-wrapper {
      position: relative;
      width: 260px;
    }
    .search-input {
      width: 100%;
      background: var(--bg);
      border: 1px solid var(--border);
      border-radius: 6px;
      padding: 6px 12px 6px 30px;
      color: var(--text-bright);
      font-size: 13px;
      outline: none;
      transition: border-color 0.15s;
    }
    .search-input:focus {
      border-color: var(--accent);
      box-shadow: 0 0 0 3px rgba(88, 166, 255, 0.2);
    }
    .search-icon {
      position: absolute;
      left: 10px;
      top: 50%;
      transform: translateY(-50%);
      color: var(--text-dim);
      font-size: 13px;
      pointer-events: none;
    }
    .search-count {
      position: absolute;
      right: 8px;
      top: 50%;
      transform: translateY(-50%);
      font-size: 11px;
      color: var(--text-dim);
      background: #21262d;
      padding: 1px 6px;
      border-radius: 8px;
    }

    .select-dropdown {
      background: var(--bg);
      border: 1px solid var(--border);
      color: var(--text);
      padding: 6px 10px;
      border-radius: 6px;
      font-size: 12px;
      outline: none;
      cursor: pointer;
    }

    .btn-toggle {
      background: #21262d;
      border: 1px solid var(--border);
      color: var(--text);
      padding: 6px 12px;
      border-radius: 6px;
      font-size: 12px;
      font-weight: 500;
      cursor: pointer;
      display: flex;
      align-items: center;
      gap: 6px;
      transition: all 0.15s ease;
    }
    .btn-toggle:hover { background: #30363d; color: var(--text-bright); }
    .btn-toggle.active {
      background: var(--accent-dim);
      border-color: var(--accent);
      color: var(--accent);
    }

    .btn-action {
      background: #238636;
      color: #ffffff;
      border: 1px solid rgba(240, 246, 252, 0.1);
      padding: 6px 14px;
      border-radius: 6px;
      font-size: 12px;
      font-weight: 600;
      cursor: pointer;
      display: flex;
      align-items: center;
      gap: 6px;
      transition: background 0.15s ease;
    }
    .btn-action:hover { background: #2ea043; }

    #container {
      flex: 1;
      position: relative;
      overflow: hidden;
    }
    canvas {
      width: 100%;
      height: 100%;
      display: block;
      cursor: grab;
    }
    canvas:active { cursor: grabbing; }

    .canvas-toolbar {
      position: absolute;
      bottom: 20px;
      left: 20px;
      background: rgba(22, 27, 34, 0.88);
      backdrop-filter: blur(8px);
      border: 1px solid var(--border);
      border-radius: 8px;
      padding: 6px;
      display: flex;
      align-items: center;
      gap: 4px;
      box-shadow: 0 4px 16px rgba(0, 0, 0, 0.4);
      z-index: 10;
    }
    .tool-btn {
      background: transparent;
      border: none;
      color: var(--text);
      width: 32px;
      height: 32px;
      border-radius: 6px;
      display: flex;
      align-items: center;
      justify-content: center;
      cursor: pointer;
      font-size: 14px;
      transition: background 0.15s;
    }
    .tool-btn:hover { background: #30363d; color: var(--text-bright); }
    .toolbar-divider { width: 1px; height: 18px; background: var(--border); margin: 0 4px; }

    .legend-container {
      position: absolute;
      top: 16px;
      left: 20px;
      background: rgba(22, 27, 34, 0.85);
      backdrop-filter: blur(8px);
      border: 1px solid var(--border);
      border-radius: 8px;
      padding: 8px 14px;
      display: flex;
      gap: 12px;
      font-size: 11px;
      box-shadow: 0 4px 12px rgba(0, 0, 0, 0.3);
      z-index: 10;
      pointer-events: none;
    }
    .legend-item { display: flex; align-items: center; gap: 6px; }
    .legend-dot { width: 8px; height: 8px; border-radius: 50%; }

    #inspector {
      position: absolute;
      top: 16px;
      right: 16px;
      width: 440px;
      max-height: calc(100vh - 100px);
      background: rgba(22, 27, 34, 0.94);
      backdrop-filter: blur(12px);
      border: 1px solid var(--border);
      border-radius: 10px;
      box-shadow: 0 12px 32px rgba(0, 0, 0, 0.6);
      display: flex;
      flex-direction: column;
      z-index: 25;
      transform: translateX(110%);
      transition: transform 0.25s cubic-bezier(0.16, 1, 0.3, 1);
      overflow: hidden;
    }
    #inspector.open { transform: translateX(0); }

    .inspector-header {
      padding: 14px 18px;
      border-bottom: 1px solid var(--border);
      display: flex;
      justify-content: space-between;
      align-items: flex-start;
      gap: 12px;
    }
    .inspector-title-area { flex: 1; overflow: hidden; }
    .badge {
      display: inline-block;
      padding: 2px 8px;
      border-radius: 12px;
      font-size: 10px;
      font-weight: 700;
      text-transform: uppercase;
      letter-spacing: 0.5px;
      margin-bottom: 6px;
    }
    .badge-file { background: var(--accent-dim); color: var(--accent); border: 1px solid rgba(88, 166, 255, 0.3); }
    .badge-function { background: var(--green-dim); color: var(--green); border: 1px solid rgba(63, 185, 80, 0.3); }
    .badge-method { background: var(--yellow-dim); color: var(--yellow); border: 1px solid rgba(210, 153, 34, 0.3); }
    .badge-struct, .badge-class { background: var(--purple-dim); color: var(--purple); border: 1px solid rgba(188, 140, 255, 0.3); }
    .badge-interface, .badge-type { background: var(--orange-dim); color: var(--orange); border: 1px solid rgba(240, 136, 62, 0.3); }

    .node-title { font-size: 16px; font-weight: 700; color: var(--text-bright); word-break: break-all; }
    .close-btn { background: transparent; border: none; color: var(--text-dim); font-size: 16px; cursor: pointer; padding: 4px; border-radius: 4px; }
    .close-btn:hover { color: var(--text-bright); background: #30363d; }

    .inspector-body {
      padding: 14px 18px;
      overflow-y: auto;
      display: flex;
      flex-direction: column;
      gap: 14px;
    }
    .meta-row { font-size: 12px; color: var(--text-dim); display: flex; flex-direction: column; gap: 4px; }
    .meta-val {
      font-family: 'SFMono-Regular', Consolas, monospace;
      color: var(--text);
      word-break: break-all;
      background: #0d1117;
      padding: 4px 8px;
      border-radius: 4px;
      border: 1px solid #21262d;
    }
    .signature-block {
      background: #0d1117;
      border: 1px solid #21262d;
      border-radius: 6px;
      padding: 10px;
      font-family: 'SFMono-Regular', Consolas, monospace;
      font-size: 12px;
      color: #79c0ff;
      white-space: pre-wrap;
      word-break: break-all;
    }

    .code-preview-container { display: flex; flex-direction: column; gap: 6px; }
    .preview-header { font-size: 11px; font-weight: 600; text-transform: uppercase; color: var(--text-dim); letter-spacing: 0.5px; }
    .code-box {
      background: #0a0d12;
      border: 1px solid #21262d;
      border-radius: 6px;
      font-family: 'SFMono-Regular', Consolas, monospace;
      font-size: 11px;
      line-height: 1.5;
      overflow-x: auto;
      max-height: 280px;
      padding: 10px;
      color: #e6edf3;
    }
    .code-line { display: flex; gap: 12px; }
    .code-line-num { width: 32px; color: #484f58; text-align: right; user-select: none; flex-shrink: 0; }
    .code-line-content { white-space: pre; }
    .code-line.highlight { background: rgba(88, 166, 255, 0.15); border-radius: 2px; }
    .code-line.highlight .code-line-num { color: var(--accent); font-weight: 700; }

    .inspector-actions { display: flex; gap: 8px; margin-top: 4px; }
    .btn-secondary {
      flex: 1;
      background: #21262d;
      border: 1px solid var(--border);
      color: var(--text);
      padding: 6px 12px;
      border-radius: 6px;
      font-size: 12px;
      cursor: pointer;
      text-align: center;
      transition: background 0.15s;
    }
    .btn-secondary:hover { background: #30363d; color: var(--text-bright); }

    #toast {
      position: absolute;
      bottom: 24px;
      right: 24px;
      background: #238636;
      color: #fff;
      padding: 10px 18px;
      border-radius: 8px;
      font-size: 13px;
      font-weight: 600;
      box-shadow: 0 6px 20px rgba(0, 0, 0, 0.4);
      transform: translateY(100px);
      opacity: 0;
      transition: all 0.25s cubic-bezier(0.16, 1, 0.3, 1);
      z-index: 100;
      pointer-events: none;
    }
    #toast.show { transform: translateY(0); opacity: 1; }
  </style>
</head>
<body>
  <header>
    <div class="brand-section">
      <div class="brand-title">⚡ codive <span class="brand-accent">Architecture Map</span></div>
      <span class="badge-pill" id="fileCountBadge">0 files</span>
      <span class="badge-pill" id="symbolCountBadge">0 symbols</span>
    </div>

    <div class="header-controls">
      <div class="search-wrapper">
        <span class="search-icon">🔍</span>
        <input type="text" id="searchInput" class="search-input" placeholder="Search files, functions, types...">
        <span id="searchCount" class="search-count" style="display:none">0</span>
      </div>

      <select id="langFilter" class="select-dropdown">
        <option value="">All Languages</option>
      </select>

      <button id="toggleSymbolsBtn" class="btn-toggle" title="Toggle Symbol Nodes visibility">
        <span>●</span> Show Symbols
      </button>

      <button id="copyPromptBtn" class="btn-action">
        <span>📋</span> Copy Context Prompt
      </button>
    </div>
  </header>

  <div id="container">
    <div class="legend-container">
      <div class="legend-item"><span class="legend-dot" style="background:var(--accent)"></span> File</div>
      <div class="legend-item"><span class="legend-dot" style="background:var(--green)"></span> Function / Method</div>
      <div class="legend-item"><span class="legend-dot" style="background:var(--purple)"></span> Struct / Class</div>
      <div class="legend-item"><span class="legend-dot" style="background:var(--orange)"></span> Interface / Type</div>
    </div>

    <canvas id="graphCanvas"></canvas>

    <div class="canvas-toolbar">
      <button class="tool-btn" id="zoomInBtn" title="Zoom In (+)">➕</button>
      <button class="tool-btn" id="zoomOutBtn" title="Zoom Out (-)">➖</button>
      <button class="tool-btn" id="resetZoomBtn" title="Fit to Screen">⛶</button>
      <div class="toolbar-divider"></div>
      <button class="tool-btn" id="reheatBtn" title="Re-simulate / Organize">⟲</button>
    </div>

    <div id="inspector">
      <div class="inspector-header">
        <div class="inspector-title-area">
          <span id="inspBadge" class="badge badge-file">FILE</span>
          <h2 id="inspTitle" class="node-title">-</h2>
        </div>
        <button class="close-btn" id="closeInspBtn">✕</button>
      </div>

      <div class="inspector-body">
        <div class="meta-row">
          <span>Location</span>
          <div id="inspLocation" class="meta-val">-</div>
        </div>

        <div id="inspSignatureSection" class="meta-row" style="display:none">
          <span>Signature</span>
          <div id="inspSignature" class="signature-block"></div>
        </div>

        <div class="code-preview-container">
          <div class="preview-header">Source Preview</div>
          <div id="inspCodeBox" class="code-box">
            <div style="color:var(--text-dim); text-align:center; padding: 20px;">Click a node to inspect source code</div>
          </div>
        </div>

        <div class="inspector-actions">
          <button id="copyPathBtn" class="btn-secondary">Copy Path</button>
          <button id="copyNameBtn" class="btn-secondary">Copy Name</button>
        </div>
      </div>
    </div>
  </div>

  <div id="toast">Copied to clipboard!</div>

  <script>
    const data = /* __GRAPH_DATA__ */;

    document.getElementById('fileCountBadge').textContent = data.total_files + ' files';
    let symCountLabel = data.total_symbols + ' symbols';
    if (data.truncated_symbols) symCountLabel = 'top ' + data.nodes.filter(n=>n.kind!=='file').length + ' of ' + symCountLabel;
    document.getElementById('symbolCountBadge').textContent = symCountLabel;

    // Populate Language Filter dropdown
    const langSelect = document.getElementById('langFilter');
    data.languages.forEach(l => {
      const opt = document.createElement('option');
      opt.value = l;
      opt.textContent = l;
      langSelect.appendChild(opt);
    });

    const canvas = document.getElementById('graphCanvas');
    const ctx = canvas.getContext('2d');

    let width = 0, height = 0;
    let dpr = window.devicePixelRatio || 1;

    function resize() {
      width = window.innerWidth;
      height = window.innerHeight - 56;
      canvas.width = width * dpr;
      canvas.height = height * dpr;
      canvas.style.width = width + 'px';
      canvas.style.height = height + 'px';
      ctx.scale(dpr, dpr);
    }
    window.addEventListener('resize', () => { resize(); requestDraw(); });
    resize();

    // Directory clusters calculation
    const dirList = data.directories || [];
    const dirCenters = {};
    dirList.forEach((d, i) => {
      const angle = (i / Math.max(1, dirList.length)) * Math.PI * 2;
      const radius = Math.min(width, height) * 0.32;
      dirCenters[d] = {
        x: width / 2 + Math.cos(angle) * radius,
        y: height / 2 + Math.sin(angle) * radius
      };
    });

    // Grouping nodes for O(N) localized physics
    const fileNodes = [];
    const symbolNodes = [];
    const symbolsByFile = {};
    const nodeMap = {};

    const nodes = data.nodes.map((n) => {
      const isFile = n.kind === 'file';
      const dirCenter = dirCenters[n.directory] || { x: width / 2, y: height / 2 };
      const jitterAngle = Math.random() * Math.PI * 2;
      const jitterDist = isFile ? Math.random() * 40 : 30 + Math.random() * 45;

      const nodeObj = {
        ...n,
        x: dirCenter.x + Math.cos(jitterAngle) * jitterDist,
        y: dirCenter.y + Math.sin(jitterAngle) * jitterDist,
        vx: 0,
        vy: 0,
        radius: isFile ? 9 : 5,
        mass: isFile ? 3 : 1,
        pinned: false
      };

      nodeMap[n.id] = nodeObj;
      if (isFile) {
        fileNodes.push(nodeObj);
        symbolsByFile[n.file] = symbolsByFile[n.file] || [];
      } else {
        symbolNodes.push(nodeObj);
        symbolsByFile[n.file] = symbolsByFile[n.file] || [];
        symbolsByFile[n.file].push(nodeObj);
      }
      return nodeObj;
    });

    const edges = data.edges.map(e => ({
      source: nodeMap[e.source],
      target: nodeMap[e.target]
    })).filter(e => e.source && e.target);

    // Adaptive default: on large codebases (>300 nodes), default to "Files Only" for instant 60 FPS load
    const isLargeRepo = (data.total_files + data.total_symbols) > 300;
    let showSymbols = !isLargeRepo;

    const toggleSymbolsBtn = document.getElementById('toggleSymbolsBtn');
    toggleSymbolsBtn.classList.toggle('active', showSymbols);
    toggleSymbolsBtn.innerHTML = showSymbols ? '<span>●</span> Show Symbols' : '<span>○</span> Files Only';

    // Transform State (Pan & Zoom)
    const transform = { x: 0, y: 0, k: 1 };
    let selectedNode = null;
    let hoveredNode = null;
    let dragNode = null;
    let isPanning = false;
    let startMouse = { x: 0, y: 0 };
    let currentFilter = '';
    let currentLang = '';

    function getNodeColor(kind) {
      switch (kind) {
        case 'file': return '#58a6ff';
        case 'function': return '#3fb950';
        case 'method': return '#d29922';
        case 'struct':
        case 'class': return '#bc8cff';
        case 'interface':
        case 'type': return '#f0883e';
        default: return '#79c0ff';
      }
    }

    // High-performance O(N) localized simulation
    let simulationAlpha = 1.0;
    let isSimulating = true;

    function stepSimulation() {
      if (simulationAlpha < 0.005) {
        isSimulating = false;
        return;
      }

      const activeNodes = showSymbols ? nodes : fileNodes;

      // 1. Center Gravity
      const cx = width / 2;
      const cy = height / 2;
      for (let i = 0; i < activeNodes.length; i++) {
        const n = activeNodes[i];
        if (n.pinned) continue;
        n.vx += (cx - n.x) * 0.0005 * simulationAlpha;
        n.vy += (cy - n.y) * 0.0005 * simulationAlpha;
      }

      // 2. Directory Clustering (pulls files towards directory center)
      for (let i = 0; i < fileNodes.length; i++) {
        const n = fileNodes[i];
        if (n.pinned) continue;
        const dc = dirCenters[n.directory];
        if (dc) {
          n.vx += (dc.x - n.x) * 0.0025 * simulationAlpha;
          n.vy += (dc.y - n.y) * 0.0025 * simulationAlpha;
        }
      }

      // 3. O(N) Repulsion:
      // A) File nodes repel other file nodes (O(F^2), where F << N)
      for (let i = 0; i < fileNodes.length; i++) {
        const a = fileNodes[i];
        for (let j = i + 1; j < fileNodes.length; j++) {
          const b = fileNodes[j];
          const dx = b.x - a.x;
          const dy = b.y - a.y;
          const distSq = dx * dx + dy * dy + 1;
          const minDist = a.radius + b.radius + 30;
          if (distSq < minDist * minDist * 25) {
            const dist = Math.sqrt(distSq);
            const force = (1600 / distSq) * simulationAlpha;
            const fx = (dx / dist) * force;
            const fy = (dy / dist) * force;
            if (!a.pinned) { a.vx -= fx / a.mass; a.vy -= fy / a.mass; }
            if (!b.pinned) { b.vx += fx / b.mass; b.vy += fy / b.mass; }
          }
        }
      }

      // B) Symbols ONLY repel sibling symbols in the same file! (O(S_file^2) sum = O(N))
      if (showSymbols) {
        for (const fPath in symbolsByFile) {
          const syms = symbolsByFile[fPath];
          for (let i = 0; i < syms.length; i++) {
            const a = syms[i];
            for (let j = i + 1; j < syms.length; j++) {
              const b = syms[j];
              const dx = b.x - a.x;
              const dy = b.y - a.y;
              const distSq = dx * dx + dy * dy + 1;
              const minDist = a.radius + b.radius + 12;
              if (distSq < minDist * minDist * 16) {
                const dist = Math.sqrt(distSq);
                const force = (800 / distSq) * simulationAlpha;
                const fx = (dx / dist) * force;
                const fy = (dy / dist) * force;
                if (!a.pinned) { a.vx -= fx / a.mass; a.vy -= fy / a.mass; }
                if (!b.pinned) { b.vx += fx / b.mass; b.vy += fy / b.mass; }
              }
            }
          }
        }

        // C) Spring Tension: files hold declared symbols in orbit
        const springLength = 40;
        const springStrength = 0.05;
        for (let i = 0; i < edges.length; i++) {
          const e = edges[i];
          const dx = e.target.x - e.source.x;
          const dy = e.target.y - e.source.y;
          const dist = Math.sqrt(dx * dx + dy * dy) || 1;
          const delta = dist - springLength;
          const force = delta * springStrength * simulationAlpha;
          const fx = (dx / dist) * force;
          const fy = (dy / dist) * force;
          if (!e.source.pinned) { e.source.vx += fx * 0.4; e.source.vy += fy * 0.4; }
          if (!e.target.pinned) { e.target.vx -= fx; e.target.vy -= fy; }
        }
      }

      // 4. Dampen & Integrate
      const damping = 0.82;
      for (let i = 0; i < activeNodes.length; i++) {
        const n = activeNodes[i];
        if (n.pinned) continue;
        n.vx *= damping;
        n.vy *= damping;
        n.x += n.vx;
        n.y += n.vy;
      }

      simulationAlpha *= 0.985;
    }

    function reheatSimulation() {
      simulationAlpha = 1.0;
      isSimulating = true;
      requestDraw();
    }

    let animId = null;
    function requestDraw() {
      if (!animId) {
        animId = requestAnimationFrame(draw);
      }
    }

    function draw() {
      animId = null;
      stepSimulation();

      ctx.clearRect(0, 0, width, height);
      ctx.save();
      ctx.translate(transform.x, transform.y);
      ctx.scale(transform.k, transform.k);

      // Viewport culling boundaries in graph space (+margin)
      const margin = 40;
      const vLeft = -transform.x / transform.k - margin;
      const vRight = (width - transform.x) / transform.k + margin;
      const vTop = -transform.y / transform.k - margin;
      const vBottom = (height - transform.y) / transform.k + margin;

      // Draw Edges (with viewport culling)
      if (showSymbols) {
        ctx.lineWidth = 1;
        for (let i = 0; i < edges.length; i++) {
          const e = edges[i];
          // Culling: skip if both endpoints are outside same side
          if ((e.source.x < vLeft && e.target.x < vLeft) ||
              (e.source.x > vRight && e.target.x > vRight) ||
              (e.source.y < vTop && e.target.y < vTop) ||
              (e.source.y > vBottom && e.target.y > vBottom)) {
            continue;
          }

          const isConnected = selectedNode && (e.source === selectedNode || e.target === selectedNode);
          const isHovered = hoveredNode && (e.source === hoveredNode || e.target === hoveredNode);

          if (isConnected || isHovered) {
            ctx.strokeStyle = '#58a6ff';
            ctx.lineWidth = 1.8;
          } else {
            ctx.strokeStyle = 'rgba(48, 54, 61, 0.45)';
            ctx.lineWidth = 0.7;
          }

          ctx.beginPath();
          ctx.moveTo(e.source.x, e.source.y);
          ctx.lineTo(e.target.x, e.target.y);
          ctx.stroke();
        }
      }

      // Draw Nodes (with viewport culling & LOD label gating)
      const activeNodes = showSymbols ? nodes : fileNodes;

      for (let i = 0; i < activeNodes.length; i++) {
        const n = activeNodes[i];
        if (n.x < vLeft || n.x > vRight || n.y < vTop || n.y > vBottom) {
          continue; // Viewport Culled!
        }

        const isMatch = checkFilter(n);
        const isSelected = n === selectedNode;
        const isHover = n === hoveredNode;

        ctx.globalAlpha = isMatch ? 1.0 : 0.12;

        if (isSelected || isHover) {
          ctx.beginPath();
          ctx.arc(n.x, n.y, n.radius + (isSelected ? 5 : 3), 0, Math.PI * 2);
          ctx.strokeStyle = isSelected ? '#58a6ff' : 'rgba(255, 255, 255, 0.7)';
          ctx.lineWidth = 2;
          ctx.stroke();
        }

        ctx.beginPath();
        ctx.arc(n.x, n.y, n.radius, 0, Math.PI * 2);
        ctx.fillStyle = getNodeColor(n.kind);
        ctx.fill();

        // Level-of-Detail label gating: prevent rendering unreadable text
        const showLabel = isSelected || isHover || (n.kind === 'file' && transform.k > 0.45) || (transform.k > 1.2 && isMatch);
        if (showLabel && isMatch) {
          ctx.fillStyle = isSelected ? '#58a6ff' : (n.kind === 'file' ? '#e6edf3' : '#8b949e');
          ctx.font = (n.kind === 'file' ? 'bold 12px' : '11px') + " -apple-system, sans-serif";
          ctx.fillText(n.label, n.x + n.radius + 5, n.y + 4);
        }
      }
      ctx.globalAlpha = 1.0;
      ctx.restore();

      if (isSimulating) {
        requestDraw();
      }
    }

    function checkFilter(node) {
      if (currentLang && node.kind === 'file' && node.language !== currentLang) return false;
      if (!currentFilter) return true;
      const q = currentFilter;
      return node.label.toLowerCase().includes(q) || (node.file && node.file.toLowerCase().includes(q));
    }

    function toGraphCoords(screenX, screenY) {
      return {
        x: (screenX - transform.x) / transform.k,
        y: (screenY - transform.y) / transform.k
      };
    }

    function findNodeAt(screenX, screenY) {
      const p = toGraphCoords(screenX, screenY);
      const activeNodes = showSymbols ? nodes : fileNodes;
      for (let i = activeNodes.length - 1; i >= 0; i--) {
        const n = activeNodes[i];
        const dx = n.x - p.x;
        const dy = n.y - p.y;
        if (dx * dx + dy * dy <= (n.radius + 4) * (n.radius + 4)) {
          return n;
        }
      }
      return null;
    }

    // Canvas Events
    canvas.addEventListener('wheel', (e) => {
      e.preventDefault();
      const zoomFactor = e.deltaY < 0 ? 1.15 : 0.85;
      const mouse = { x: e.clientX, y: e.clientY - 56 };
      const newK = Math.max(0.08, Math.min(6.0, transform.k * zoomFactor));

      transform.x = mouse.x - (mouse.x - transform.x) * (newK / transform.k);
      transform.y = mouse.y - (mouse.y - transform.y) * (newK / transform.k);
      transform.k = newK;
      requestDraw();
    }, { passive: false });

    canvas.addEventListener('mousedown', (e) => {
      const mouse = { x: e.clientX, y: e.clientY - 56 };
      const hit = findNodeAt(mouse.x, mouse.y);
      if (hit) {
        dragNode = hit;
        dragNode.pinned = true;
        reheatSimulation();
      } else {
        isPanning = true;
        startMouse = mouse;
      }
    });

    window.addEventListener('mousemove', (e) => {
      const mouse = { x: e.clientX, y: e.clientY - 56 };
      if (dragNode) {
        const p = toGraphCoords(mouse.x, mouse.y);
        dragNode.x = p.x;
        dragNode.y = p.y;
        dragNode.vx = 0;
        dragNode.vy = 0;
        reheatSimulation();
      } else if (isPanning) {
        transform.x += mouse.x - startMouse.x;
        transform.y += mouse.y - startMouse.y;
        startMouse = mouse;
        requestDraw();
      } else {
        const hit = findNodeAt(mouse.x, mouse.y);
        if (hit !== hoveredNode) {
          hoveredNode = hit;
          canvas.style.cursor = hit ? 'pointer' : 'grab';
          requestDraw();
        }
      }
    });

    window.addEventListener('mouseup', () => {
      if (dragNode) {
        dragNode.pinned = false;
        dragNode = null;
      }
      isPanning = false;
    });

    canvas.addEventListener('click', (e) => {
      const mouse = { x: e.clientX, y: e.clientY - 56 };
      const hit = findNodeAt(mouse.x, mouse.y);
      if (hit) {
        selectNode(hit);
      }
    });

    function selectNode(node) {
      selectedNode = node;
      requestDraw();

      const insp = document.getElementById('inspector');
      insp.classList.add('open');

      document.getElementById('inspTitle').textContent = node.label;
      const badge = document.getElementById('inspBadge');
      badge.textContent = node.kind.toUpperCase();
      badge.className = 'badge badge-' + node.kind;

      const locText = node.file + (node.line ? ' (Line ' + node.line + ')' : '');
      document.getElementById('inspLocation').textContent = locText;

      const sigSec = document.getElementById('inspSignatureSection');
      if (node.signature) {
        sigSec.style.display = 'flex';
        document.getElementById('inspSignature').textContent = node.signature;
      } else {
        sigSec.style.display = 'none';
      }

      const codeBox = document.getElementById('inspCodeBox');
      codeBox.innerHTML = '<div style="color:var(--text-dim); padding: 12px; text-align:center;">Loading source snippet...</div>';

      const lineParam = node.line || 1;
      fetch('/api/snippet?file=' + encodeURIComponent(node.file) + '&line=' + lineParam)
        .then(res => res.json())
        .then(snippet => {
          renderCodeSnippet(codeBox, snippet);
        })
        .catch(err => {
          codeBox.innerHTML = '<div style="color:var(--red); padding: 12px;">Failed to load file preview: ' + err.message + '</div>';
        });
    }

    function renderCodeSnippet(container, snippet) {
      container.innerHTML = '';
      const lines = snippet.content.split('\n');
      for (let i = 0; i < lines.length; i++) {
        const lineNo = snippet.start_line + i;
        const lineDiv = document.createElement('div');
        lineDiv.className = 'code-line' + (lineNo === snippet.target_line ? ' highlight' : '');

        const numSpan = document.createElement('span');
        numSpan.className = 'code-line-num';
        numSpan.textContent = lineNo;

        const contentSpan = document.createElement('span');
        contentSpan.className = 'code-line-content';
        contentSpan.textContent = lines[i];

        lineDiv.appendChild(numSpan);
        lineDiv.appendChild(contentSpan);
        container.appendChild(lineDiv);
      }
    }

    document.getElementById('closeInspBtn').addEventListener('click', () => {
      document.getElementById('inspector').classList.remove('open');
      selectedNode = null;
      requestDraw();
    });

    const searchInput = document.getElementById('searchInput');
    const searchCount = document.getElementById('searchCount');

    searchInput.addEventListener('input', (e) => {
      currentFilter = e.target.value.trim().toLowerCase();
      if (currentFilter) {
        const matches = (showSymbols ? nodes : fileNodes).filter(checkFilter);
        searchCount.style.display = 'block';
        searchCount.textContent = matches.length;
      } else {
        searchCount.style.display = 'none';
      }
      requestDraw();
    });

    langSelect.addEventListener('change', (e) => {
      currentLang = e.target.value;
      requestDraw();
    });

    toggleSymbolsBtn.addEventListener('click', () => {
      showSymbols = !showSymbols;
      toggleSymbolsBtn.classList.toggle('active', showSymbols);
      toggleSymbolsBtn.innerHTML = showSymbols ? '<span>●</span> Show Symbols' : '<span>○</span> Files Only';
      reheatSimulation();
    });

    document.getElementById('zoomInBtn').addEventListener('click', () => {
      transform.k = Math.min(6.0, transform.k * 1.3);
      requestDraw();
    });
    document.getElementById('zoomOutBtn').addEventListener('click', () => {
      transform.k = Math.max(0.08, transform.k / 1.3);
      requestDraw();
    });
    document.getElementById('resetZoomBtn').addEventListener('click', () => {
      fitToScreen();
    });
    document.getElementById('reheatBtn').addEventListener('click', () => {
      reheatSimulation();
    });

    function fitToScreen() {
      const activeNodes = showSymbols ? nodes : fileNodes;
      if (activeNodes.length === 0) return;
      let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity;
      activeNodes.forEach(n => {
        if (n.x < minX) minX = n.x;
        if (n.x > maxX) maxX = n.x;
        if (n.y < minY) minY = n.y;
        if (n.y > maxY) maxY = n.y;
      });
      const graphW = Math.max(100, maxX - minX);
      const graphH = Math.max(100, maxY - minY);
      const scaleX = (width - 120) / graphW;
      const scaleY = (height - 120) / graphH;
      transform.k = Math.max(0.2, Math.min(1.5, Math.min(scaleX, scaleY)));
      transform.x = (width - graphW * transform.k) / 2 - minX * transform.k;
      transform.y = (height - graphH * transform.k) / 2 - minY * transform.k;
      requestDraw();
    }

    function showToast(msg) {
      const toast = document.getElementById('toast');
      toast.textContent = msg;
      toast.classList.add('show');
      setTimeout(() => toast.classList.remove('show'), 2500);
    }

    document.getElementById('copyPathBtn').addEventListener('click', () => {
      if (selectedNode) {
        navigator.clipboard.writeText(selectedNode.file).then(() => showToast('Copied path: ' + selectedNode.file));
      }
    });

    document.getElementById('copyNameBtn').addEventListener('click', () => {
      if (selectedNode) {
        navigator.clipboard.writeText(selectedNode.label).then(() => showToast('Copied name: ' + selectedNode.label));
      }
    });

    document.getElementById('copyPromptBtn').addEventListener('click', () => {
      const fileNodes = nodes.filter(n => n.kind === 'file');
      const symNodes = nodes.filter(n => n.kind !== 'file');

      const dirs = {};
      fileNodes.forEach(f => {
        dirs[f.directory] = dirs[f.directory] || [];
        dirs[f.directory].push(f);
      });

      let md = '# 🏛️ Repository Architecture Overview\n';
      md += 'Total Files: ' + fileNodes.length + ' | Total Symbols: ' + symNodes.length + '\n\n';

      const bt = String.fromCharCode(96);
      Object.keys(dirs).sort().forEach(d => {
        md += '## 📁 ' + d + '\n';
        dirs[d].forEach(f => {
          const symsInFile = symNodes.filter(s => s.file === f.file);
          md += '- ' + bt + f.label + bt + ' (' + (f.language || 'text') + ')\n';
          symsInFile.slice(0, 10).forEach(s => {
            md += '   • [' + s.kind + '] ' + bt + (s.signature || s.label) + bt + ' (L' + s.line + ')\n';
          });
          if (symsInFile.length > 10) {
            md += '   • ... (' + (symsInFile.length - 10) + ' more symbols)\n';
          }
        });
        md += '\n';
      });

      navigator.clipboard.writeText(md).then(() => {
        showToast('✓ Token-optimized architecture prompt copied!');
      });
    });

    fitToScreen();
    reheatSimulation();
  </script>
</body>
</html>`
