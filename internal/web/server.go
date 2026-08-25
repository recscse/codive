// Package web provides the embedded interactive browser-based architecture graph server.
package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/recscse/ctxd/internal/db"
	"github.com/recscse/ctxd/internal/ui"
)

// GraphData represents the network nodes and edges for the web map.
type GraphData struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// GraphNode is a file or symbol in the visualization.
type GraphNode struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Kind     string `json:"kind"` // "file", "function", "struct", "class"
	Language string `json:"language,omitempty"`
	File     string `json:"file"`
	Line     int    `json:"line,omitempty"`
}

// GraphEdge connects symbols to files or callers to callees.
type GraphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// StartWebServer starts a local web server at port 7890 and opens the browser.
func StartWebServer(targetDir string, database *sql.DB, port int) error {
	if port <= 0 {
		port = 7890
	}

	ctx := context.Background()
	allFiles, err := db.GetAllFiles(ctx, database)
	if err != nil {
		return err
	}
	allSymbols, err := db.GetAllSymbols(ctx, database)
	if err != nil {
		return err
	}

	var nodes []GraphNode
	var edges []GraphEdge
	nodeMap := make(map[string]bool)

	// Add file nodes
	for path, rec := range allFiles {
		fileID := "file:" + path
		nodes = append(nodes, GraphNode{
			ID:       fileID,
			Label:    filepath.Base(path),
			Kind:     "file",
			Language: rec.Language,
			File:     path,
		})
		nodeMap[fileID] = true
	}

	// Add symbol nodes and link to their file
	for _, sym := range allSymbols {
		symID := fmt.Sprintf("sym:%s:%s", sym.FilePath, sym.Name)
		if !nodeMap[symID] {
			nodes = append(nodes, GraphNode{
				ID:       symID,
				Label:    sym.Name,
				Kind:     sym.Kind,
				File:     sym.FilePath,
				Line:     sym.LineNumber,
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

	graphData := GraphData{Nodes: nodes, Edges: edges}
	graphJSON, _ := json.Marshal(graphData)

	htmlContent := strings.Replace(htmlTemplate, "/* __GRAPH_DATA__ */", string(graphJSON), 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(htmlContent))
	})

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	url := fmt.Sprintf("http://%s", addr)

	ui.Header("🌐 ctxd Web Architecture Map Launched")
	fmt.Println()
	fmt.Printf("  %s %s\n", ui.Dim.Sprint("URL:        "), ui.CyanBold.Sprint(url))
	fmt.Printf("  %s %d files, %d declared symbols\n\n", ui.Dim.Sprint("Graph Nodes:"), len(allFiles), len(allSymbols))
	ui.Success("Press Ctrl+C in terminal to stop web server.")

	// Open browser automatically
	openBrowser(url)

	return http.ListenAndServe(addr, mux)
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
  <title>ctxd - Interactive Repository Map</title>
  <style>
    :root {
      --bg: #0d1117;
      --card-bg: #161b22;
      --border: #30363d;
      --text: #c9d1d9;
      --accent: #58a6ff;
      --green: #3fb950;
      --purple: #bc8cff;
      --orange: #f0883e;
    }
    * { box-sizing: border-box; margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; }
    body { background: var(--bg); color: var(--text); overflow: hidden; height: 100vh; display: flex; flex-direction: column; }
    
    header {
      background: var(--card-bg);
      border-bottom: 1px solid var(--border);
      padding: 12px 24px;
      display: flex;
      justify-content: space-between;
      align-items: center;
      z-index: 10;
    }
    .brand { display: flex; align-items: center; gap: 10px; font-weight: 700; font-size: 18px; color: #fff; }
    .brand span { color: var(--accent); }
    
    .search-box {
      background: var(--bg);
      border: 1px solid var(--border);
      border-radius: 6px;
      padding: 6px 14px;
      color: #fff;
      font-size: 14px;
      width: 320px;
      outline: none;
    }
    .search-box:focus { border-color: var(--accent); }

    .btn {
      background: #238636;
      color: #fff;
      border: none;
      padding: 8px 16px;
      border-radius: 6px;
      cursor: pointer;
      font-size: 13px;
      font-weight: 600;
      transition: opacity 0.2s;
    }
    .btn:hover { opacity: 0.9; }

    #container { flex: 1; position: relative; }
    canvas { width: 100%; height: 100%; display: block; cursor: grab; }
    canvas:active { cursor: grabbing; }

    #inspector {
      position: absolute;
      top: 20px;
      right: 20px;
      width: 340px;
      background: var(--card-bg);
      border: 1px solid var(--border);
      border-radius: 8px;
      padding: 18px;
      box-shadow: 0 8px 24px rgba(0,0,0,0.5);
      display: none;
    }
    #inspector h3 { color: var(--accent); font-size: 16px; margin-bottom: 8px; }
    #inspector .meta { font-size: 13px; color: #8b949e; margin-bottom: 12px; }
    #inspector .badge { display: inline-block; padding: 2px 8px; border-radius: 12px; font-size: 11px; font-weight: 600; text-transform: uppercase; }
    .badge-file { background: rgba(88, 166, 255, 0.2); color: var(--accent); }
    .badge-function { background: rgba(63, 185, 80, 0.2); color: var(--green); }
    .badge-struct { background: rgba(188, 140, 255, 0.2); color: var(--purple); }
    .badge-class { background: rgba(240, 136, 62, 0.2); color: var(--orange); }
  </style>
</head>
<body>
  <header>
    <div class="brand">⚡ ctxd <span>Interactive Map</span></div>
    <input type="text" id="search" class="search-box" placeholder="Filter files or symbols...">
    <button class="btn" onclick="copyPrompt()">📋 Copy Context Prompt</button>
  </header>

  <div id="container">
    <canvas id="graphCanvas"></canvas>
    <div id="inspector">
      <span id="inspBadge" class="badge"></span>
      <h3 id="inspTitle"></h3>
      <div id="inspMeta" class="meta"></div>
      <div id="inspDetails"></div>
    </div>
  </div>

  <script>
    const data = /* __GRAPH_DATA__ */;
    const canvas = document.getElementById('graphCanvas');
    const ctx = canvas.getContext('2d');

    let width, height;
    function resize() {
      width = canvas.width = window.innerWidth;
      height = canvas.height = window.innerHeight - 60;
    }
    window.addEventListener('resize', resize);
    resize();

    // Position nodes in a radial cluster
    const nodes = data.nodes.map((n, i) => {
      const angle = (i / data.nodes.length) * Math.PI * 2;
      const radius = n.kind === 'file' ? 180 + (i % 3) * 60 : 320 + (i % 4) * 50;
      return {
        ...n,
        x: width / 2 + Math.cos(angle) * radius + (Math.random() - 0.5) * 40,
        y: height / 2 + Math.sin(angle) * radius + (Math.random() - 0.5) * 40,
        vx: 0,
        vy: 0,
        radius: n.kind === 'file' ? 10 : 6
      };
    });

    const nodeLookup = {};
    nodes.forEach(n => nodeLookup[n.ID || n.id] = n);

    let filter = '';
    document.getElementById('search').addEventListener('input', (e) => {
      filter = e.target.value.toLowerCase();
    });

    function draw() {
      ctx.clearRect(0, 0, width, height);

      // Draw Edges
      ctx.strokeStyle = '#21262d';
      ctx.lineWidth = 1;
      data.edges.forEach(e => {
        const src = nodeLookup[e.source];
        const tgt = nodeLookup[e.target];
        if (src && tgt) {
          ctx.beginPath();
          ctx.moveTo(src.x, src.y);
          ctx.lineTo(tgt.x, tgt.y);
          ctx.stroke();
        }
      });

      // Draw Nodes
      nodes.forEach(n => {
        const matches = !filter || n.label.toLowerCase().includes(filter) || (n.file && n.file.toLowerCase().includes(filter));
        ctx.globalAlpha = matches ? 1.0 : 0.15;

        ctx.beginPath();
        ctx.arc(n.x, n.y, n.radius, 0, Math.PI * 2);
        if (n.kind === 'file') ctx.fillStyle = '#58a6ff';
        else if (n.kind === 'function') ctx.fillStyle = '#3fb950';
        else if (n.kind === 'struct') ctx.fillStyle = '#bc8cff';
        else ctx.fillStyle = '#f0883e';
        ctx.fill();

        if (matches && (n.kind === 'file' || nodes.length < 150)) {
          ctx.fillStyle = '#c9d1d9';
          ctx.font = n.kind === 'file' ? '12px sans-serif' : '10px sans-serif';
          ctx.fillText(n.label, n.x + n.radius + 4, n.y + 4);
        }
      });
      ctx.globalAlpha = 1.0;

      requestAnimationFrame(draw);
    }
    draw();

    // Click handler for inspector
    canvas.addEventListener('click', (e) => {
      const rect = canvas.getBoundingClientRect();
      const clickX = e.clientX - rect.left;
      const clickY = e.clientY - rect.top;

      const clicked = nodes.find(n => Math.hypot(n.x - clickX, n.y - clickY) < n.radius + 5);
      const inspector = document.getElementById('inspector');

      if (clicked) {
        inspector.style.display = 'block';
        document.getElementById('inspTitle').textContent = clicked.label;
        document.getElementById('inspBadge').textContent = clicked.kind;
        document.getElementById('inspBadge').className = 'badge badge-' + clicked.kind;
        document.getElementById('inspMeta').textContent = clicked.file + (clicked.line ? ' (Line ' + clicked.line + ')' : '');
      } else {
        inspector.style.display = 'none';
      }
    });

    function copyPrompt() {
      const text = 'Here is the repository map context extracted by ctxd:\nTotal Files: ' + nodes.filter(n=>n.kind==='file').length + '\nTotal Symbols: ' + nodes.filter(n=>n.kind!=='file').length;
      navigator.clipboard.writeText(text).then(() => alert('Copied token-optimized context prompt to clipboard!'));
    }
  </script>
</body>
</html>`
