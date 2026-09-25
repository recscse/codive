// Package mcp implements the Model Context Protocol (MCP) JSON-RPC 2.0 server over standard I/O.
package mcp

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/git"
	"github.com/recscse/codive/internal/indexer"
	"github.com/recscse/codive/internal/scanner"
	"github.com/recscse/codive/internal/symbols"
)

// JSONRPCRequest represents an incoming JSON-RPC 2.0 message.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	// Result and Error are set when the message is the client's response to
	// a request the server sent (such as roots/list); Method is then empty.
	Result json.RawMessage `json:"result,omitempty"`
	Error  *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCResponse represents an outgoing JSON-RPC 2.0 response.
type JSONRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      any           `json:"id,omitempty"`
	Result  any           `json:"result,omitempty"`
	Error   *JSONRPCError `json:"error,omitempty"`
}

// JSONRPCError represents a JSON-RPC 2.0 error object.
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Tool represents an MCP Tool definition.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// ContentItem represents a text content piece in MCP tool response.
type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ToolCallResult represents the output format of a tool execution.
type ToolCallResult struct {
	Content []ContentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// Server handles MCP JSON-RPC protocol messages over reader/writer streams.
type Server struct {
	rootDir  string
	version  string
	database *sql.DB
	dbMutex  sync.RWMutex
	dbCache  map[string]*sql.DB
	// freshen, when set, brings a workspace's index up to date before a
	// tool reads it (see SetFreshnessHook).
	freshen func(ctx context.Context, dir string, database *sql.DB)

	// clientSupportsRoots is set when the client declares the MCP roots
	// capability; the server then asks it which workspace is open instead of
	// relying on the path it was started with.
	clientSupportsRoots bool
	// rootFromClient is true once rootDir came from the client's roots.
	rootFromClient bool
	rootsRequests  int
	// outbound holds server-initiated messages (requests to the client),
	// written by Serve after the message currently being handled.
	outbound []any
}

// SetFreshnessHook registers fn to run before any index-reading tool call,
// with the target workspace and its database, so answers reflect recent
// edits even when no background sync has run yet. fn should be cheap when
// the index is already fresh.
func (s *Server) SetFreshnessHook(fn func(ctx context.Context, dir string, database *sql.DB)) {
	s.freshen = fn
}

// rootsRequestPrefix identifies codive's roots/list requests among responses.
const rootsRequestPrefix = "codive-roots-"

// requestRoots queues a roots/list request to the client.
func (s *Server) requestRoots() {
	s.rootsRequests++
	s.outbound = append(s.outbound, map[string]any{
		"jsonrpc": "2.0",
		"id":      fmt.Sprintf("%s%d", rootsRequestPrefix, s.rootsRequests),
		"method":  "roots/list",
	})
}

// handleResponse processes the client's reply to a server-initiated request.
func (s *Server) handleResponse(req JSONRPCRequest) {
	id, _ := req.ID.(string)
	if !strings.HasPrefix(id, rootsRequestPrefix) || req.Error != nil {
		return
	}
	var result struct {
		Roots []struct {
			URI string `json:"uri"`
		} `json:"roots"`
	}
	if err := json.Unmarshal(req.Result, &result); err != nil {
		return
	}
	// The first local root is the workspace; other roots stay reachable via
	// the tools' workspace_path argument.
	for _, r := range result.Roots {
		dir, ok := fileURIToPath(r.URI)
		if !ok {
			continue
		}
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			s.rootDir = dir
			s.rootFromClient = true
			return
		}
	}
}

// fileURIToPath converts a file:// URI (as sent in MCP roots) to a local path.
func fileURIToPath(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return "", false
	}
	p := u.Path
	// file:///C:/work -> /C:/work: drop the slash before a drive letter.
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	if u.Host != "" && u.Host != "localhost" {
		p = "//" + u.Host + p // UNC share
	}
	return filepath.Clean(filepath.FromSlash(p)), p != ""
}

// NewServer creates a new MCP Server instance. version is reported to MCP
// clients in the "initialize" response and should be the same build-time
// version string embedded in the CLI binary (main.Version), so the server's
// self-reported version never drifts from the binary that's actually running.
func NewServer(rootDir string, database *sql.DB, version string) *Server {
	if strings.TrimSpace(version) == "" {
		version = "dev"
	}
	return &Server{
		rootDir:  rootDir,
		version:  version,
		database: database,
		dbCache:  make(map[string]*sql.DB),
	}
}

// Close closes every database connection the server opened on demand for
// workspaces it resolved. The database passed to NewServer is owned by the
// caller and is left open.
func (s *Server) Close() {
	s.dbMutex.Lock()
	defer s.dbMutex.Unlock()
	for dir, conn := range s.dbCache {
		if conn != nil && conn != s.database {
			_ = conn.Close()
		}
		delete(s.dbCache, dir)
	}
}

func (s *Server) getDBForPath(targetPath string) (*sql.DB, string, error) {
	searchDir := s.rootDir
	if strings.TrimSpace(targetPath) != "" {
		if abs, err := filepath.Abs(targetPath); err == nil {
			searchDir = abs
		}
	} else if !s.rootFromClient {
		// Without client roots, prefer the current working directory if it
		// has an index: some clients start servers in the open workspace.
		if cwd, err := os.Getwd(); err == nil && cwd != "" {
			if _, err := os.Stat(filepath.Join(cwd, ".codive", "index.db")); err == nil {
				searchDir = cwd
			}
		}
	}

	// Traverse upwards looking for nearest .codive/index.db
	curr := searchDir
	var resolvedDir string
	for {
		dbPath := filepath.Join(curr, ".codive", "index.db")
		if _, err := os.Stat(dbPath); err == nil {
			resolvedDir = curr
			break
		}
		parent := filepath.Dir(curr)
		if parent == curr || parent == "" {
			break
		}
		curr = parent
	}

	// If no index exists anywhere, auto-index the target workspace on-the-fly.
	// Only the configured workspace or a git repository qualifies:
	// workspace_path comes from the agent, and auto-indexing something like a
	// drive root or home directory would walk (and copy into FTS) everything
	// under it.
	if resolvedDir == "" {
		if err := checkAutoIndexable(searchDir, s.rootDir); err != nil {
			return nil, searchDir, err
		}
		resolvedDir = searchDir
		dbPath := filepath.Join(resolvedDir, ".codive", "index.db")
		_ = os.MkdirAll(filepath.Dir(dbPath), 0755)

		dbConn, openErr := db.Open(dbPath)
		if openErr == nil {
			if _, err := indexer.Rebuild(context.Background(), dbConn, resolvedDir, nil); err != nil {
				dbConn.Close()
				return nil, resolvedDir, fmt.Errorf("failed to auto-index %s: %w", resolvedDir, err)
			}
			s.dbMutex.Lock()
			s.dbCache[resolvedDir] = dbConn
			s.dbMutex.Unlock()
			return dbConn, resolvedDir, nil
		}
	}

	absPath := resolvedDir
	s.dbMutex.RLock()
	cached, ok := s.dbCache[absPath]
	s.dbMutex.RUnlock()
	if ok && cached != nil {
		return cached, absPath, nil
	}

	dbPath := filepath.Join(absPath, ".codive", "index.db")
	dbConn, err := db.Open(dbPath)
	if err != nil {
		return nil, absPath, fmt.Errorf("failed to open database for %s: %w", absPath, err)
	}

	s.dbMutex.Lock()
	s.dbCache[absPath] = dbConn
	s.dbMutex.Unlock()

	return dbConn, absPath, nil
}

const (
	// maxOutputLineChars caps any single line in a tool response. Minified or
	// generated files can have one line of hundreds of kilobytes; without a
	// cap a single matching line becomes a six-figure-token response.
	maxOutputLineChars = 2000
	// maxOutputChars caps a whole tool response (~20k tokens).
	maxOutputChars = 80000
)

// guardOutput enforces the per-line and total size caps on a tool response,
// marking every cut so the agent knows the text is incomplete rather than
// mistaking it for the real content.
func guardOutput(text string) string {
	if len(text) <= maxOutputLineChars && len(text) <= maxOutputChars {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if len(l) > maxOutputLineChars {
			half := maxOutputLineChars / 2
			head := truncateUTF8(l, half)
			tail := l[len(l)-half:]
			for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
				tail = tail[1:]
			}
			lines[i] = fmt.Sprintf("%s … [%d chars clipped from this line] … %s", head, len(l)-len(head)-len(tail), tail)
		}
	}
	out := strings.Join(lines, "\n")
	if len(out) > maxOutputChars {
		out = truncateUTF8(out, maxOutputChars) + fmt.Sprintf("\n\n[Output truncated at %d characters. Narrow the request (a path, line range, directory_filter, or smaller limit) to see the rest.]", maxOutputChars)
	}
	return out
}

// truncateUTF8 cuts s to at most n bytes without splitting a UTF-8 sequence.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// toolCallTimeout bounds how long a single tools/call may run.
const toolCallTimeout = 60 * time.Second

// supportedProtocolVersions lists the MCP revisions this server can speak,
// newest first. It only uses tools with text content, which all of them
// share; 2025-03-26 additionally requires accepting JSON-RPC batches, which
// Serve handles.
var supportedProtocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// negotiateProtocolVersion follows the MCP lifecycle rule: echo the client's
// requested version when supported, otherwise offer the newest one we have
// and let the client decide whether to disconnect.
func negotiateProtocolVersion(requested string) string {
	for _, v := range supportedProtocolVersions {
		if v == requested {
			return v
		}
	}
	return supportedProtocolVersions[0]
}

// checkAutoIndexable reports whether dir may be indexed on demand: it must be
// an existing directory that is either the server's configured workspace or
// the root of a git repository (.git is a directory, or a file in worktrees).
func checkAutoIndexable(dir, rootDir string) error {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("workspace %s is not an accessible directory", dir)
	}
	if absRoot, err := filepath.Abs(rootDir); err == nil && filepath.Clean(dir) == filepath.Clean(absRoot) {
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return nil
	}
	return fmt.Errorf("no codive index found for %s, and it is not a git repository root, so it won't be auto-indexed; run `codive init %s` to index it explicitly", dir, dir)
}

// Serve reads JSON-RPC messages from in and writes responses to out until EOF.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	encoder := json.NewEncoder(out)

	for {
		// Send any requests queued while handling the previous message (e.g.
		// roots/list) before blocking on the next read.
		for _, msg := range s.outbound {
			if err := encoder.Encode(msg); err != nil {
				return err
			}
		}
		s.outbound = s.outbound[:0]

		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		line = []byte(strings.TrimSpace(string(line)))
		if len(line) == 0 {
			continue
		}

		// JSON-RPC batch (an array of requests): part of MCP 2025-03-26, which
		// this server advertises, so it must be accepted.
		if line[0] == '[' {
			var batch []json.RawMessage
			if err := json.Unmarshal(line, &batch); err != nil {
				_ = encoder.Encode(parseErrorResponse())
				continue
			}
			if len(batch) == 0 {
				_ = encoder.Encode(JSONRPCResponse{JSONRPC: "2.0", Error: &JSONRPCError{Code: -32600, Message: "Invalid Request: empty batch"}})
				continue
			}
			var responses []*JSONRPCResponse
			for _, raw := range batch {
				var req JSONRPCRequest
				if err := json.Unmarshal(raw, &req); err != nil {
					responses = append(responses, &JSONRPCResponse{JSONRPC: "2.0", Error: &JSONRPCError{Code: -32600, Message: "Invalid Request"}})
					continue
				}
				if resp := s.handleRequest(context.Background(), req); resp != nil {
					responses = append(responses, resp)
				}
			}
			// A batch of only notifications gets no response at all.
			if len(responses) > 0 {
				if err := encoder.Encode(responses); err != nil {
					return err
				}
			}
			continue
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			_ = encoder.Encode(parseErrorResponse())
			continue
		}

		resp := s.handleRequest(context.Background(), req)
		if resp != nil {
			if err := encoder.Encode(resp); err != nil {
				return err
			}
		}
	}
}

func parseErrorResponse() JSONRPCResponse {
	return JSONRPCResponse{
		JSONRPC: "2.0",
		Error:   &JSONRPCError{Code: -32700, Message: "Parse error"},
	}
}

func (s *Server) handleRequest(ctx context.Context, req JSONRPCRequest) *JSONRPCResponse {
	if req.Method == "" && req.ID != nil {
		// A response to a request the server sent, not a request itself.
		s.handleResponse(req)
		return nil
	}
	if strings.HasPrefix(req.Method, "notifications/") {
		if s.clientSupportsRoots && (req.Method == "notifications/initialized" || req.Method == "notifications/roots/list_changed") {
			s.requestRoots()
		}
		return nil
	}

	switch req.Method {
	case "initialize":
		var initParams struct {
			ProtocolVersion string `json:"protocolVersion"`
			Capabilities    struct {
				Roots *json.RawMessage `json:"roots"`
			} `json:"capabilities"`
		}
		_ = json.Unmarshal(req.Params, &initParams)
		s.clientSupportsRoots = initParams.Capabilities.Roots != nil
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": negotiateProtocolVersion(initParams.ProtocolVersion),
				"capabilities": map[string]any{
					"tools": map[string]any{},
				},
				"serverInfo": map[string]any{
					"name":    "codive",
					"version": s.version,
				},
			},
		}

	case "ping":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{},
		}

	case "tools/list":
		tools := []Tool{
			{
				Name:        "get_repo_map",
				Description: "PRIMARY REPO DISCOVERY TOOL. Call this FIRST before list_dir or find_by_name to inspect the whole project structure, file tree, and declared symbol signatures in a single token-efficient call.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"workspace_path": map[string]any{
							"type":        "string",
							"description": "Optional path to target repository root (defaults to configured workspace)",
						},
						"max_depth": map[string]any{
							"type":        "integer",
							"description": "Optional maximum folder depth to include (e.g. 2 for high-level overview)",
						},
						"directory_filter": map[string]any{
							"type":        "string",
							"description": "Optional subdirectory prefix to filter the map (e.g. 'backend/routers')",
						},
						"include_symbols": map[string]any{
							"type":        "boolean",
							"description": "Whether to include symbol signatures in the map (default: true)",
						},
						"token_budget": map[string]any{
							"type":        "integer",
							"description": "Maximum token budget limit (default: 3000 tokens) to prevent context inflation",
						},
					},
				},
			},
			{
				Name:        "find_symbol",
				Description: "PREFERRED OVER GREP for locating code definitions. Instantly finds exact function, class, struct, or interface definitions, line numbers, and type signatures across the codebase without scanning raw text.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "Symbol name or substring to search for (e.g. 'AuthService', 'ScanIncremental', 'InitSchema')",
						},
						"workspace_path": map[string]any{
							"type":        "string",
							"description": "Optional path to target repository root",
						},
					},
					"required": []string{"query"},
				},
			},
			{
				Name:        "find_references",
				Description: "FASTEST REFERENCE FINDER. Discovers all call sites, usages, and imports of any symbol, function, or class across the codebase in 1 step.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"symbol": map[string]any{
							"type":        "string",
							"description": "The symbol or function name to find callers/references for",
						},
						"limit": map[string]any{
							"type":        "integer",
							"description": "Maximum number of call sites to return (default: 30)",
						},
						"workspace_path": map[string]any{
							"type":        "string",
							"description": "Optional path to target repository root",
						},
					},
					"required": []string{"symbol"},
				},
			},
			{
				Name:        "get_git_changes",
				Description: "PRIMARY GIT DIFF TOOL. Returns modified files and uncommitted diffs with AST-aware context (enclosing function and class names) instead of raw unified diff text, saving 90% of diff tokens.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"workspace_path": map[string]any{
							"type":        "string",
							"description": "Optional path to target repository root",
						},
					},
				},
			},
			{
				Name:        "search_code",
				Description: "FASTEST CODE SEARCH TOOL. Performs sub-millisecond SQLite FTS5 full-text code search across indexed files. Use this INSTEAD OF grep/ripgrep for bounded, token-efficient snippet matches.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "Search keyword or phrase",
						},
						"limit": map[string]any{
							"type":        "integer",
							"description": "Maximum number of search results to return (default: 20)",
						},
						"workspace_path": map[string]any{
							"type":        "string",
							"description": "Optional path to target repository root",
						},
					},
					"required": []string{"query"},
				},
			},
			{
				Name:        "get_file_skeleton",
				Description: "TOKEN-SAVING SKELETONIZER. Strips function bodies and returns only imports, type definitions, structs, interfaces, and function signatures with line numbers, turning a 2,000-line file into a concise ~50-token skeleton.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "Relative path to the source file in the repository",
						},
						"workspace_path": map[string]any{
							"type":        "string",
							"description": "Optional path to target repository root",
						},
					},
					"required": []string{"path"},
				},
			},
			{
				Name:        "find_callers",
				Description: "CALL GRAPH ANALYSIS. Discovers all locations and functions where the specified function/symbol is called across the entire repository.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"symbol": map[string]any{
							"type":        "string",
							"description": "The function or method name to find callers for",
						},
						"limit": map[string]any{
							"type":        "integer",
							"description": "Maximum number of caller sites to return (default: 30)",
						},
						"workspace_path": map[string]any{
							"type":        "string",
							"description": "Optional path to target repository root",
						},
					},
					"required": []string{"symbol"},
				},
			},
			{
				Name:        "find_callees",
				Description: "CALL GRAPH ANALYSIS. Discovers what internal functions, methods, and types are called/invoked inside the specified function's implementation.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"symbol": map[string]any{
							"type":        "string",
							"description": "The function or method name to inspect callees for",
						},
						"workspace_path": map[string]any{
							"type":        "string",
							"description": "Optional path to target repository root",
						},
					},
					"required": []string{"symbol"},
				},
			},
			{
				Name:        "find_tests_for",
				Description: "TEST FILE LOCATOR. Automatically locates the corresponding unit/integration test file and specific test functions for a given source file or symbol.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"target": map[string]any{
							"type":        "string",
							"description": "Source file path (e.g. 'internal/scanner/scanner.go') or symbol name",
						},
						"workspace_path": map[string]any{
							"type":        "string",
							"description": "Optional path to target repository root",
						},
					},
					"required": []string{"target"},
				},
			},
			{
				Name:        "pack_feature_context",
				Description: "ONE-SHOT FEATURE DISCOVERY. Given a feature keyword (e.g. 'auth', 'uploads', 'scanner'), bundles related routes, data models, schemas, skeletons, and test suites into a single compressed context summary.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"topic": map[string]any{
							"type":        "string",
							"description": "Feature name or topic keyword (e.g. 'auth', 'database', 'scanner')",
						},
						"workspace_path": map[string]any{
							"type":        "string",
							"description": "Optional path to target repository root",
						},
					},
					"required": []string{"topic"},
				},
			},
			{
				Name:        "save_decision",
				Description: "PERSISTENT AGENT MEMORY. Records durable architectural decisions, tricky bug root causes, or design invariants so future agents never repeat mistakes.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"topic": map[string]any{
							"type":        "string",
							"description": "Topic or subsystem name (e.g. 'database', 'auth', 'caching')",
						},
						"summary": map[string]any{
							"type":        "string",
							"description": "Concise summary of the architectural rule, decision, or invariant",
						},
						"workspace_path": map[string]any{
							"type":        "string",
							"description": "Optional path to target repository root",
						},
					},
					"required": []string{"topic", "summary"},
				},
			},
			{
				Name:        "get_decisions",
				Description: "PERSISTENT AGENT MEMORY. Retrieves past architectural decisions, rules, and invariants stored by previous AI agents.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"topic": map[string]any{
							"type":        "string",
							"description": "Optional topic keyword to filter decisions",
						},
						"workspace_path": map[string]any{
							"type":        "string",
							"description": "Optional path to target repository root",
						},
					},
				},
			},
			{
				Name:        "blast_radius",
				Description: "PR BLAST RADIUS ANALYZER. Answers 'If I change this symbol, what will break?' by evaluating callers, affected modules, and exact test suites to execute before refactoring.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"symbol": map[string]any{
							"type":        "string",
							"description": "Symbol name or function to analyze (e.g. 'GenerateToken', 'ScanIncremental')",
						},
						"workspace_path": map[string]any{
							"type":        "string",
							"description": "Optional path to target repository root",
						},
					},
					"required": []string{"symbol"},
				},
			},
			{
				Name:        "read_file_context",
				Description: "Reads the verified content of a source file along with its AST metadata and declared symbol outline. Returns at most 1000 lines per call; use start_line/end_line to page through larger files.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "Relative path to the source file in the repository",
						},
						"start_line": map[string]any{
							"type":        "integer",
							"description": "Optional first line to return (1-based, inclusive). Defaults to 1.",
						},
						"end_line": map[string]any{
							"type":        "integer",
							"description": "Optional last line to return (1-based, inclusive). Defaults to start_line + 999.",
						},
						"workspace_path": map[string]any{
							"type":        "string",
							"description": "Optional path to target repository root",
						},
					},
					"required": []string{"path"},
				},
			},
		}
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"tools": tools,
			},
		}

	case "tools/call":
		var callParams struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &callParams); err != nil {
			return &JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &JSONRPCError{Code: -32602, Message: "Invalid tool call params"},
			}
		}

		// Requests are served one at a time, so a single runaway call (a huge
		// git diff, a slow query on a giant index) must not block every call
		// after it. Context-aware work (SQLite queries, git subprocesses) is
		// cancelled at the deadline and the call reports an error instead.
		callCtx, cancel := context.WithTimeout(ctx, toolCallTimeout)
		defer cancel()
		result, err := s.executeTool(callCtx, callParams.Name, callParams.Arguments)
		if err == nil && callCtx.Err() != nil {
			err = fmt.Errorf("%s did not finish within %v: %w", callParams.Name, toolCallTimeout, callCtx.Err())
		}
		if err != nil {
			return &JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: ToolCallResult{
					IsError: true,
					Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}},
				},
			}
		}

		for i := range result.Content {
			result.Content[i].Text = guardOutput(result.Content[i].Text)
		}
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  result,
		}

	default:
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &JSONRPCError{Code: -32601, Message: fmt.Sprintf("Method not found: %s", req.Method)},
		}
	}
}

func (s *Server) executeTool(ctx context.Context, name string, args map[string]any) (*ToolCallResult, error) {
	wsPath, _ := args["workspace_path"].(string)
	targetDB, targetDir, err := s.getDBForPath(wsPath)
	if err != nil {
		return nil, err
	}

	// Decisions don't come from source files, so they don't need a fresh index.
	if s.freshen != nil && name != "save_decision" && name != "get_decisions" {
		s.freshen(ctx, targetDir, targetDB)
	}

	switch name {
	case "get_repo_map":
		maxDepth := 0
		if d, ok := args["max_depth"].(float64); ok && d > 0 {
			maxDepth = int(d)
		}
		includeSymbols := true
		if inc, ok := args["include_symbols"].(bool); ok {
			includeSymbols = inc
		}
		tokenBudget := 3000
		if tb, ok := args["token_budget"].(float64); ok && tb > 0 {
			tokenBudget = int(tb)
		}
		dirFilter, _ := args["directory_filter"].(string)

		allFiles, err := db.GetAllFiles(ctx, targetDB)
		if err != nil {
			return nil, err
		}
		allSymbols, err := db.GetAllSymbols(ctx, targetDB)
		if err != nil {
			return nil, err
		}

		symbolsByFile := make(map[string][]db.SymbolRecord)
		if includeSymbols {
			for _, sym := range allSymbols {
				symbolsByFile[sym.FilePath] = append(symbolsByFile[sym.FilePath], sym)
			}
		}

		filterNorm := filepath.ToSlash(dirFilter)
		filterNorm = strings.Trim(filterNorm, "/")

		var filePaths []string
		for p := range allFiles {
			pNorm := filepath.ToSlash(p)
			if filterNorm != "" && !strings.HasPrefix(pNorm, filterNorm) {
				continue
			}
			if maxDepth > 0 {
				depth := len(strings.Split(pNorm, "/"))
				if depth > maxDepth {
					continue
				}
			}
			filePaths = append(filePaths, p)
		}

		// Sort by depth (shallower/root first)
		sort.Slice(filePaths, func(i, j int) bool {
			pi := filePaths[i]
			pj := filePaths[j]
			di := strings.Count(filepath.ToSlash(pi), "/")
			dj := strings.Count(filepath.ToSlash(pj), "/")
			if di != dj {
				return di < dj
			}
			return pi < pj
		})

		dirMap := make(map[string][]string)
		for _, p := range filePaths {
			dir := filepath.Dir(p)
			if dir == "." {
				dir = "(root)"
			}
			dirMap[dir] = append(dirMap[dir], p)
		}

		var dirs []string
		for d := range dirMap {
			dirs = append(dirs, d)
		}
		sort.Strings(dirs)

		maxChars := tokenBudget * 4
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("# Repository Map: `%s`\n", targetDir))
		sb.WriteString(fmt.Sprintf("> 📍 **Workspace Root**: `%s` | **Indexed Files**: %d | **Total Symbols**: %d\n\n", targetDir, len(filePaths), len(allSymbols)))

		truncatedFiles := 0

		for _, d := range dirs {
			if sb.Len() > maxChars {
				truncatedFiles += len(dirMap[d])
				continue
			}

			sb.WriteString(fmt.Sprintf("## %s\n", d))
			for _, f := range dirMap[d] {
				if sb.Len() > maxChars {
					truncatedFiles++
					continue
				}

				fileRec := allFiles[f]
				syms := symbolsByFile[f]
				if len(syms) == 0 || !includeSymbols {
					sb.WriteString(fmt.Sprintf("  📄 `%s` (%s, %d bytes)\n", filepath.Base(f), fileRec.Language, fileRec.SizeBytes))
				} else {
					sb.WriteString(fmt.Sprintf("  📄 `%s` (%s, %d bytes) — %d symbols:\n", filepath.Base(f), fileRec.Language, fileRec.SizeBytes, len(syms)))
					for _, sym := range syms {
						sig := strings.TrimSpace(sym.Signature)
						if sig == "" {
							sig = sym.Name
						}
						sb.WriteString(fmt.Sprintf("     • [%s] `%s` (L%d)\n", sym.Kind, sig, sym.LineNumber))
					}
				}
			}
			sb.WriteString("\n")
		}

		if truncatedFiles > 0 {
			sb.WriteString(fmt.Sprintf("\n> ⚠️ *Map truncated (+%d files omitted) to stay within %d tokens. Use 'directory_filter' or 'max_depth' to inspect deeper submodules.*\n", truncatedFiles, tokenBudget))
		}

		return &ToolCallResult{
			Content: []ContentItem{{Type: "text", Text: strings.TrimSpace(sb.String())}},
		}, nil

	case "find_symbol":
		query, _ := args["query"].(string)
		if strings.TrimSpace(query) == "" {
			return nil, fmt.Errorf("query argument is required")
		}
		limit := 30
		if l, ok := args["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}
		syms, err := db.FindSymbols(ctx, targetDB, query)
		if err != nil {
			return nil, err
		}

		// Line-number drift protection: refresh each file that will actually
		// be shown (once per file, not per match), then re-query if anything
		// was re-parsed so the locations printed below are the current ones.
		refreshed := false
		checked := make(map[string]bool)
		for _, sym := range syms[:min(limit, len(syms))] {
			if !checked[sym.FilePath] {
				checked[sym.FilePath] = true
				if s.ensureFreshSymbols(ctx, targetDB, targetDir, sym.FilePath) {
					refreshed = true
				}
			}
		}
		if refreshed {
			if fresh, err := db.FindSymbols(ctx, targetDB, query); err == nil {
				syms = fresh
			}
		}

		if len(syms) == 0 {
			return &ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("No symbols found matching '%s'", query)}},
			}, nil
		}

		total := len(syms)
		syms = syms[:min(limit, total)]

		var sb strings.Builder
		if total > len(syms) {
			// Exact name matches sort first, so the most likely targets are
			// always inside the shown slice.
			sb.WriteString(fmt.Sprintf("### Symbol Matches for `%s` (showing %d of %d — exact name matches first; use a more specific query or raise `limit` to see more)\n\n", query, len(syms), total))
		} else {
			sb.WriteString(fmt.Sprintf("### Symbol Matches for `%s` (Found: %d)\n\n", query, total))
		}

		// Auto-recall: inject matching architectural decisions before results
		decisions, _ := db.GetDecisions(ctx, targetDB, query)
		if len(decisions) > 0 {
			sb.WriteString("**📌 Architectural Constraints (auto-recalled)**\n")
			for _, d := range decisions {
				sb.WriteString(fmt.Sprintf("- [%s] %s *(recorded %s)*\n",
					d.Topic, d.Summary, d.CreatedAt.Format("2006-01-02")))
			}
			sb.WriteString("\n---\n\n")
		}

		rawTokenEstimate := 0
		for i, sym := range syms {
			// Semantic classification
			role := classifySymbolRole(sym.FilePath, sym.Kind)

			sb.WriteString(fmt.Sprintf("%d. **[%s]** `%s`\n", i+1, role, sym.Name))
			sb.WriteString(fmt.Sprintf("   - **File:** `%s` (L%d)\n", sym.FilePath, sym.LineNumber))
			if sym.Signature != "" && sym.Signature != sym.Name {
				sig := sym.Signature
				if len(sig) > 160 {
					sig = sig[:157] + "..."
				}
				sb.WriteString(fmt.Sprintf("   - **Signature:** `%s`\n", sig))
			}
			// Extract annotations from signature
			if anns := extractAnnotations(sym.Signature); len(anns) > 0 {
				sb.WriteString(fmt.Sprintf("   - **Annotations:** %s\n", strings.Join(anns, ", ")))
			}
			sb.WriteString("\n")
			rawTokenEstimate += 800 // rough: reading the file to find this symbol
		}

		used := estimateTokens(sb.String())
		sb.WriteString(tokenFooter(used, rawTokenEstimate))
		db.RecordTelemetry(ctx, targetDB, "find_symbol", used, rawTokenEstimate, 14)

		return &ToolCallResult{
			Content: []ContentItem{{Type: "text", Text: sb.String()}},
		}, nil

	case "find_references", "find_callers":
		symbol, _ := args["symbol"].(string)
		if strings.TrimSpace(symbol) == "" {
			return nil, fmt.Errorf("symbol argument is required")
		}
		limit := 30
		if l, ok := args["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}

		var page *db.ReferencePage
		var err error
		var heading, emptyMsg, telemetryName string
		if name == "find_callers" {
			// Genuinely narrower than find_references: excludes the symbol's own
			// declaration and anything that isn't a real call expression.
			page, err = db.FindCallersPage(ctx, targetDB, symbol, limit)
			heading, emptyMsg, telemetryName = "Callers", "No callers found for '%s'", "find_callers"
		} else {
			page, err = db.FindReferencesPage(ctx, targetDB, symbol, limit)
			heading, emptyMsg, telemetryName = "References", "No references found for '%s'", "find_references"
		}
		if err != nil {
			return nil, err
		}
		refs := page.Refs

		if len(refs) == 0 {
			return &ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: fmt.Sprintf(emptyMsg, symbol)}},
			}, nil
		}

		var sb strings.Builder
		if page.More {
			// Never let a capped list read as the complete set: an agent that
			// believes "30 references" when there are 3,000 will under-scope a
			// refactor.
			sb.WriteString(fmt.Sprintf("### %s for `%s` (showing first %d — MORE EXIST; raise `limit` for the full list)\n\n", heading, symbol, len(refs)))
		} else {
			sb.WriteString(fmt.Sprintf("### %s for `%s` (Found: %d, complete)\n\n", heading, symbol, len(refs)))
		}

		// Grouped by file (results already arrive file by file), so each path
		// is written once instead of once per hit.
		currentFile := ""
		for _, ref := range refs {
			if ref.FilePath != currentFile {
				currentFile = ref.FilePath
				sb.WriteString(fmt.Sprintf("**%s** [%s]\n", ref.FilePath, classifyRefRole(ref.FilePath)))
			}
			sb.WriteString(fmt.Sprintf("  L%d: %s\n", ref.LineNumber, strings.TrimSpace(ref.Snippet)))
		}

		used := estimateTokens(sb.String())
		rawEst := len(refs) * 1200
		sb.WriteString(tokenFooter(used, rawEst))
		db.RecordTelemetry(ctx, targetDB, telemetryName, used, rawEst, 5)

		return &ToolCallResult{
			Content: []ContentItem{{Type: "text", Text: sb.String()}},
		}, nil

	case "find_callees":
		symbol, _ := args["symbol"].(string)
		if strings.TrimSpace(symbol) == "" {
			return nil, fmt.Errorf("symbol argument is required")
		}

		callees, err := db.FindCallees(ctx, targetDB, symbol)
		if err != nil {
			return nil, err
		}

		if len(callees) == 0 {
			return &ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("No internal callees found inside '%s'", symbol)}},
			}, nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Found %d internal callee(s) called inside '%s':\n\n", len(callees), symbol))
		for i, c := range callees {
			sb.WriteString(fmt.Sprintf("%d. [%s] %s\n   Location: %s:%d\n\n", i+1, c.Kind, c.Name, c.FilePath, c.LineNumber))
		}
		return &ToolCallResult{
			Content: []ContentItem{{Type: "text", Text: sb.String()}},
		}, nil

	case "find_tests_for":
		target, _ := args["target"].(string)
		if strings.TrimSpace(target) == "" {
			return nil, fmt.Errorf("target argument is required")
		}

		tests, err := db.FindTestsFor(ctx, targetDB, target)
		if err != nil {
			return nil, err
		}

		if len(tests) == 0 {
			return &ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("No test files found for '%s'", target)}},
			}, nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Found %d test suite(s) for '%s':\n\n", len(tests), target))
		for i, t := range tests {
			sb.WriteString(fmt.Sprintf("%d. 🧪 %s\n", i+1, t.TestFilePath))
			for _, name := range t.TestNames {
				sb.WriteString(fmt.Sprintf("   • %s\n", name))
			}
			sb.WriteString("\n")
		}
		return &ToolCallResult{
			Content: []ContentItem{{Type: "text", Text: sb.String()}},
		}, nil

	case "get_file_skeleton":
		relPath, _ := args["path"].(string)
		if strings.TrimSpace(relPath) == "" {
			return nil, fmt.Errorf("path argument is required")
		}
		relPath = filepath.ToSlash(relPath)
		s.ensureFreshSymbols(ctx, targetDB, targetDir, relPath)

		fullPath, err := validateSafeRelPath(targetDir, relPath)
		if err != nil {
			return nil, err
		}
		contentBytes, err := os.ReadFile(fullPath)
		if err != nil {
			ftsContent, ftsErr := db.GetFileContent(ctx, targetDB, relPath)
			if ftsErr != nil {
				return nil, fmt.Errorf("cannot read file %s: %w", relPath, err)
			}
			contentBytes = []byte(ftsContent)
		}

		// Count real lines for header
		rawLineCount := strings.Count(string(contentBytes), "\n") + 1
		rawTokenEstimate := rawLineCount * 5 // ~5 tokens per line of code

		syms, _ := db.FindSymbolsInFile(ctx, targetDB, relPath)
		lang := scanner.DetectLanguage(relPath)
		skel := symbols.GenerateSkeleton(relPath, lang, contentBytes, syms)

		var sb strings.Builder

		// Auto-recall: inject architectural decisions relevant to this file
		basename := filepath.Base(relPath)
		nameWithoutExt := strings.TrimSuffix(basename, filepath.Ext(basename))
		decisions, _ := db.GetDecisions(ctx, targetDB, nameWithoutExt)
		if len(decisions) > 0 {
			sb.WriteString("**📌 Architectural Constraints (auto-recalled for this file)**\n")
			for _, d := range decisions {
				sb.WriteString(fmt.Sprintf("- **[%s]** %s *(recorded %s)*\n",
					d.Topic, d.Summary, d.CreatedAt.Format("2006-01-02")))
			}
			sb.WriteString("\n---\n\n")
		}

		sb.WriteString(skel)

		used := estimateTokens(sb.String())
		sb.WriteString(tokenFooter(used, rawTokenEstimate))
		db.RecordTelemetry(ctx, targetDB, "get_file_skeleton", used, rawTokenEstimate, 2)

		return &ToolCallResult{
			Content: []ContentItem{{Type: "text", Text: sb.String()}},
		}, nil

	case "pack_feature_context":
		topic, _ := args["topic"].(string)
		if strings.TrimSpace(topic) == "" {
			return nil, fmt.Errorf("topic argument is required")
		}

		syms, _ := db.FindSymbols(ctx, targetDB, topic)
		ftsMatches, _ := db.SearchFTS(ctx, targetDB, topic, 10)
		tests, _ := db.FindTestsFor(ctx, targetDB, topic)
		decisions, _ := db.GetDecisions(ctx, targetDB, topic)

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("# 📦 Feature Context Pack: `%s`\n\n", topic))

		if len(decisions) > 0 {
			sb.WriteString("## 🧠 Stored Architectural Decisions\n")
			for _, d := range decisions {
				sb.WriteString(fmt.Sprintf("- **[%s]** %s *(recorded %s)*\n", d.Topic, d.Summary, d.CreatedAt.Format("2006-01-02")))
			}
			sb.WriteString("\n")
		}

		if len(syms) > 0 {
			sb.WriteString("## 🧬 Core Types & Functions\n")
			for i, s := range syms {
				if i >= 10 {
					sb.WriteString(fmt.Sprintf("... +%d other symbol definitions\n", len(syms)-10))
					break
				}
				sb.WriteString(fmt.Sprintf("- `[%s]` **%s** (`%s:%d`)\n  `%s`\n", s.Kind, s.Name, s.FilePath, s.LineNumber, s.Signature))
			}
			sb.WriteString("\n")
		}

		// Call relationships for the top callable matches: who calls them and what
		// they call, in this same response — otherwise an agent needs separate
		// find_callers/find_callees round trips to build this picture itself.
		var relSyms []db.SymbolRecord
		for _, s := range syms {
			if s.Kind == "function" || s.Kind == "method" {
				relSyms = append(relSyms, s)
				if len(relSyms) >= 5 {
					break
				}
			}
		}
		if len(relSyms) > 0 {
			// Symbols are loaded only for files that actually contain a caller,
			// not for the whole repository (a full load per call is ~100k rows
			// on a large codebase).
			symbolsByFile := make(map[string][]db.SymbolRecord)
			fileSymbols := func(path string) []db.SymbolRecord {
				if syms, ok := symbolsByFile[path]; ok {
					return syms
				}
				syms, _ := db.FindSymbolsInFile(ctx, targetDB, path)
				symbolsByFile[path] = syms
				return syms
			}

			sb.WriteString("## 🔗 Call Relationships\n")
			for _, s := range relSyms {
				callers, _ := db.FindCallers(ctx, targetDB, s.Name, 6)
				callees, _ := db.FindCallees(ctx, targetDB, s.Name)

				sb.WriteString(fmt.Sprintf("- **%s** (`%s:%d`)\n", s.Name, s.FilePath, s.LineNumber))

				if len(callers) > 0 {
					seen := make(map[string]bool)
					var names []string
					for _, c := range callers {
						label := symbols.EnclosingFunctionName(fileSymbols(c.FilePath), c.LineNumber)
						if label == "" {
							label = fmt.Sprintf("%s:%d", c.FilePath, c.LineNumber)
						}
						if !seen[label] {
							seen[label] = true
							names = append(names, label)
						}
					}
					sb.WriteString(fmt.Sprintf("  - Called from: %s\n", strings.Join(names, ", ")))
				} else {
					sb.WriteString("  - Called from: *(no callers found in indexed code — may be an entry point or unused)*\n")
				}

				if len(callees) > 0 {
					names := make([]string, 0, len(callees))
					for i, c := range callees {
						if i >= 8 {
							names = append(names, fmt.Sprintf("+%d more", len(callees)-8))
							break
						}
						names = append(names, c.Name)
					}
					sb.WriteString(fmt.Sprintf("  - Calls: %s\n", strings.Join(names, ", ")))
				}
			}
			sb.WriteString("\n")
		}

		if len(tests) > 0 {
			sb.WriteString("## 🧪 Relevant Test Suites\n")
			for _, t := range tests {
				sb.WriteString(fmt.Sprintf("- **%s**\n", t.TestFilePath))
				for _, name := range t.TestNames {
					sb.WriteString(fmt.Sprintf("   • `%s`\n", name))
				}
			}
			sb.WriteString("\n")
		}

		// Rank candidate files by relevance rather than alphabetically: a file whose
		// declared symbols matched the topic is a far stronger signal than a file that
		// merely mentions the topic word in prose (e.g. a CHANGELOG entry), and among
		// FTS-only matches we keep SearchFTS's own bm25 rank order (best match first)
		// instead of discarding it. Otherwise a doc file that happens to sort before
		// the actually-relevant source file (alphabetically) could crowd it out of the
		// limited skeleton slots below.
		seenFile := make(map[string]bool)
		var candidateFiles []string
		addCandidate := func(f string) {
			if seenFile[f] {
				return
			}
			if strings.Contains(f, "_test.") || strings.Contains(f, ".spec.") || strings.Contains(f, ".test.") {
				return
			}
			seenFile[f] = true
			candidateFiles = append(candidateFiles, f)
		}
		for _, s := range syms {
			addCandidate(s.FilePath)
		}
		for _, m := range ftsMatches {
			addCandidate(m.Path)
		}

		// A skeleton is inherently useless for a file with no AST extractor (it can
		// only ever render the "no AST symbols" fallback), so prefer AST-capable
		// source files for the limited skeleton slots below over docs/markup that
		// merely mention the topic word more densely and out-rank the real source
		// file in FTS's bm25 score. Order is preserved within each group.
		var codeFiles, otherFiles []string
		for _, f := range candidateFiles {
			if symbols.IsASTCapableLanguage(scanner.DetectLanguage(f)) {
				codeFiles = append(codeFiles, f)
			} else {
				otherFiles = append(otherFiles, f)
			}
		}
		candidateFiles = append(codeFiles, otherFiles...)

		if len(candidateFiles) > 0 {
			sb.WriteString("## 📄 Structural File Skeletons\n\n")
			for i, f := range candidateFiles {
				if i >= 3 {
					break
				}
				fullPath := filepath.Join(targetDir, filepath.FromSlash(f))
				content, err := os.ReadFile(fullPath)
				if err != nil {
					continue
				}
				// Deliberately pass nil symbols with the real detected language (not the
				// literal string "auto", which matched no case in ExtractSymbols' switch
				// and silently produced an empty skeleton for every file, always). This
				// lets GenerateSkeleton's own fallback re-extract symbols correctly, the
				// same working pattern get_file_skeleton uses.
				lang := scanner.DetectLanguage(f)
				skel := symbols.GenerateSkeleton(f, lang, content, nil)
				sb.WriteString(fmt.Sprintf("```\n%s\n```\n\n", skel))
			}
		}

		return &ToolCallResult{
			Content: []ContentItem{{Type: "text", Text: strings.TrimSpace(sb.String())}},
		}, nil

	case "save_decision":
		topic, _ := args["topic"].(string)
		summary, _ := args["summary"].(string)
		if strings.TrimSpace(topic) == "" || strings.TrimSpace(summary) == "" {
			return nil, fmt.Errorf("both topic and summary arguments are required")
		}

		rec, err := db.SaveDecision(ctx, targetDB, topic, summary)
		if err != nil {
			return nil, err
		}

		return &ToolCallResult{
			Content: []ContentItem{{
				Type: "text",
				Text: fmt.Sprintf("✓ Recorded architectural decision for [%s]: %s (ID: %d)", rec.Topic, rec.Summary, rec.ID),
			}},
		}, nil

	case "get_decisions":
		topic, _ := args["topic"].(string)
		decisions, err := db.GetDecisions(ctx, targetDB, topic)
		if err != nil {
			return nil, err
		}

		if len(decisions) == 0 {
			return &ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("No architectural decisions found matching '%s'", topic)}},
			}, nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Found %d architectural decision(s):\n\n", len(decisions)))
		for i, d := range decisions {
			sb.WriteString(fmt.Sprintf("%d. [%s] %s\n   Recorded: %s (ID: %d)\n\n",
				i+1, d.Topic, d.Summary, d.CreatedAt.Format("2006-01-02 15:04:05 UTC"), d.ID))
		}

		return &ToolCallResult{
			Content: []ContentItem{{Type: "text", Text: sb.String()}},
		}, nil

	case "blast_radius":
		symbol, _ := args["symbol"].(string)
		if strings.TrimSpace(symbol) == "" {
			return nil, fmt.Errorf("symbol argument is required")
		}

		res, err := db.AnalyzeBlastRadius(ctx, targetDB, symbol)
		if err != nil {
			return nil, err
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("💥 Blast Radius Analysis for '%s':\n\n", res.Symbol))
		plus := ""
		if res.MoreReferences {
			plus = "+"
		}
		sb.WriteString(fmt.Sprintf("  • Risk Level: %s (%d%s references across %d%s files)\n", res.RiskLevel, len(res.References), plus, len(res.AffectedFiles), plus))
		if res.MoreReferences {
			sb.WriteString("  • Note: reference scan capped — counts and file list below are a lower bound, not the full set.\n")
		}
		if len(res.AffectedFiles) > 0 {
			sb.WriteString("  • Affected Files:\n")
			for _, f := range res.AffectedFiles {
				sb.WriteString(fmt.Sprintf("     - %s\n", f))
			}
		}
		if len(res.TestsToRun) > 0 {
			sb.WriteString("  • Tests to Run:\n")
			for _, t := range res.TestsToRun {
				sb.WriteString(fmt.Sprintf("     - %s\n", t))
			}
		} else {
			sb.WriteString("  • Tests to Run: None detected\n")
		}

		return &ToolCallResult{
			Content: []ContentItem{{Type: "text", Text: strings.TrimSpace(sb.String())}},
		}, nil

	case "get_git_changes":
		gitRes, err := git.GetGitChanges(ctx, targetDir, targetDB)
		if err != nil {
			return nil, err
		}
		return &ToolCallResult{
			Content: []ContentItem{{Type: "text", Text: git.FormatGitChanges(gitRes)}},
		}, nil

	case "search_code":
		query, _ := args["query"].(string)
		if strings.TrimSpace(query) == "" {
			return nil, fmt.Errorf("query argument is required")
		}
		limit := 20
		if limitVal, ok := args["limit"].(float64); ok && limitVal > 0 {
			limit = int(limitVal)
		}

		results, err := db.SearchFTS(ctx, targetDB, query, limit)
		if err != nil {
			return nil, err
		}

		if len(results) == 0 {
			return &ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("No matches found for '%s'", query)}},
			}, nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Found %d match(es) for '%s':\n\n", len(results), query))
		for i, res := range results {
			cleanSnippet := strings.ReplaceAll(res.Snippet, ">>>", "[")
			cleanSnippet = strings.ReplaceAll(cleanSnippet, "<<<", "]")
			sb.WriteString(fmt.Sprintf("%d. 📄 %s\n", i+1, res.Path))
			for _, line := range strings.Split(cleanSnippet, "\n") {
				sb.WriteString(fmt.Sprintf("   │ %s\n", strings.TrimRight(line, "\r")))
			}
			sb.WriteString("\n")
		}
		return &ToolCallResult{
			Content: []ContentItem{{Type: "text", Text: sb.String()}},
		}, nil

	case "read_file_context":
		relPath, _ := args["path"].(string)
		if strings.TrimSpace(relPath) == "" {
			return nil, fmt.Errorf("path argument is required")
		}
		relPath = filepath.ToSlash(relPath)

		// Line-Number Drift Protection
		s.ensureFreshSymbols(ctx, targetDB, targetDir, relPath)

		fullPath, err := validateSafeRelPath(targetDir, relPath)
		if err != nil {
			return nil, err
		}
		contentBytes, err := os.ReadFile(fullPath)
		if err != nil {
			// Fallback to FTS content if disk read fails
			ftsContent, ftsErr := db.GetFileContent(ctx, targetDB, relPath)
			if ftsErr != nil {
				return nil, fmt.Errorf("cannot read file %s: %w", relPath, err)
			}
			contentBytes = []byte(ftsContent)
		}

		syms, _ := db.FindSymbolsInFile(ctx, targetDB, relPath)

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("=== File: %s (%d bytes) ===\n", relPath, len(contentBytes)))
		if len(syms) > 0 {
			sb.WriteString("Declared Symbols:\n")
			for _, sym := range syms {
				sb.WriteString(fmt.Sprintf(" - [%s] %s (L%d)\n", sym.Kind, sym.Name, sym.LineNumber))
			}
			sb.WriteString("\n")
		}
		startLine, _ := args["start_line"].(float64)
		endLine, _ := args["end_line"].(float64)
		body, note, err := selectLines(string(contentBytes), int(startLine), int(endLine), maxReadLines)
		if err != nil {
			return nil, err
		}
		sb.WriteString("--- Content ---\n")
		sb.WriteString(body)
		if note != "" {
			sb.WriteString("\n" + note)
		}
		sb.WriteString("\n=== End of File ===")

		return &ToolCallResult{
			Content: []ContentItem{{Type: "text", Text: sb.String()}},
		}, nil

	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

// maxReadLines caps how much of a file read_file_context returns when no
// explicit range is requested, so one call on a generated or vendored file
// can't dump megabytes into the agent's context.
const maxReadLines = 1000

// selectLines returns lines [start, end] (1-based, inclusive) of content. A
// zero start means line 1 and a zero end means start+limit-1; the range is
// clamped to the file and to limit lines. note, when non-empty, tells the
// agent the output is partial and how to fetch the rest.
func selectLines(content string, start, end, limit int) (body, note string, err error) {
	lines := strings.Split(content, "\n")
	total := len(lines)
	if start < 0 || end < 0 {
		return "", "", fmt.Errorf("start_line and end_line must be positive")
	}
	if start == 0 {
		start = 1
	}
	if start > total {
		return "", "", fmt.Errorf("start_line %d is past the end of the file (%d lines)", start, total)
	}
	if end == 0 || end > start+limit-1 {
		end = start + limit - 1
	}
	if end > total {
		end = total
	}
	if end < start {
		return "", "", fmt.Errorf("end_line %d is before start_line %d", end, start)
	}
	body = strings.Join(lines[start-1:end], "\n")
	if start > 1 || end < total {
		note = fmt.Sprintf("[Showing lines %d-%d of %d. Pass start_line/end_line (max %d lines per call) to read more.]", start, end, total, limit)
	}
	return body, note, nil
}

// validateSafeRelPath ensures that relPath does not escape the root repository directory.
func validateSafeRelPath(rootDir string, relPath string) (string, error) {
	cleanRel := filepath.Clean(filepath.FromSlash(relPath))
	if filepath.IsAbs(cleanRel) {
		return "", fmt.Errorf("absolute paths are not permitted: %s", relPath)
	}
	// Anything returned here goes into the agent's context and on to its
	// model provider, so credential files are refused outright.
	if scanner.IsSecretPath(cleanRel) {
		return "", fmt.Errorf("refusing to read %s: the file name suggests it contains credentials", relPath)
	}
	if strings.HasPrefix(cleanRel, "..") || strings.Contains(cleanRel, filepath.FromSlash("/../")) {
		return "", fmt.Errorf("directory traversal outside repository boundary is prohibited: %s", relPath)
	}
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return "", err
	}
	absTarget := filepath.Join(absRoot, cleanRel)
	absTargetClean, err := filepath.Abs(absTarget)
	if err != nil {
		return "", err
	}
	relCheck, err := filepath.Rel(absRoot, absTargetClean)
	if err != nil || strings.HasPrefix(relCheck, "..") {
		return "", fmt.Errorf("path escapes repository boundary: %s", relPath)
	}
	return absTargetClean, nil
}

// ensureFreshSymbols re-parses an already-indexed file whose size or mtime on
// disk no longer matches the index, so line numbers don't drift between
// background syncs. It reports whether symbols were rewritten. Files that
// aren't in the index are left alone: they're either ignored (node_modules,
// .codiveignore, binaries) or not yet picked up by a scan, and indexing them
// here would bypass those filters.
func (s *Server) ensureFreshSymbols(ctx context.Context, database *sql.DB, rootDir string, relPath string) bool {
	rec, indexed, err := db.GetFile(ctx, database, relPath)
	if err != nil || !indexed {
		return false
	}
	fullPath, err := validateSafeRelPath(rootDir, relPath)
	if err != nil {
		return false
	}
	info, err := os.Stat(fullPath)
	if err != nil || info.IsDir() {
		return false
	}
	modTime := info.ModTime().UTC()
	if rec.SizeBytes == info.Size() && rec.LastModified.Equal(modTime) {
		return false
	}

	content, err := os.ReadFile(fullPath)
	if err != nil {
		return false
	}
	rec.SizeBytes = info.Size()
	rec.LastModified = modTime
	rec.LastIndexed = time.Now().UTC()

	hash := scanner.HashBytes(content)
	if hash == rec.ContentHash {
		// Only the mtime changed: refresh it so the next check short-circuits.
		_ = db.ApplyIndexChanges(ctx, database, db.IndexChanges{MetadataOnly: []db.FileRecord{rec}})
		return false
	}
	rec.ContentHash = hash

	syms, err := symbols.ExtractSymbols(relPath, rec.Language, content)
	if err != nil {
		return false
	}
	err = db.ApplyIndexChanges(ctx, database, db.IndexChanges{
		Files:   []db.FileRecord{rec},
		Symbols: syms,
		FTS:     map[string]string{relPath: string(content)},
	})
	return err == nil
}

// ── LLM-output helpers ─────────────────────────────────────────────────────

// classifySymbolRole returns a human-readable semantic role for display in
// find_symbol results. It distinguishes definitions, tests, and mocks.
func classifySymbolRole(filePath, kind string) string {
	lower := strings.ToLower(filepath.ToSlash(filePath))
	isTest := strings.Contains(lower, "_test.") ||
		strings.Contains(lower, "/test/") ||
		strings.Contains(lower, "spec.") ||
		strings.Contains(lower, "test.java") ||
		strings.HasPrefix(filepath.Base(lower), "test")

	switch {
	case isTest && (kind == "function" || kind == "method"):
		return "Test Case"
	case isTest:
		return "Test Helper"
	case kind == "class" || kind == "struct" || kind == "interface":
		return "Type Definition"
	case kind == "method":
		return "Method Definition"
	case kind == "function":
		return "Function Definition"
	case kind == "annotation":
		return "Annotation"
	case kind == "dependency":
		return "Dependency"
	default:
		return "Symbol Definition"
	}
}

// classifyRefRole returns the semantic role for a reference/caller result.
func classifyRefRole(filePath string) string {
	lower := strings.ToLower(filepath.ToSlash(filePath))
	switch {
	case strings.Contains(lower, "_test.") || strings.Contains(lower, "/test/") ||
		strings.HasPrefix(filepath.Base(lower), "test"):
		return "Test Mock / Assertion"
	case strings.Contains(lower, "mock") || strings.Contains(lower, "stub") || strings.Contains(lower, "fake"):
		return "Mock / Stub"
	case strings.Contains(lower, "config") || strings.Contains(lower, "configuration"):
		return "Configuration"
	default:
		return "Caller / Reference"
	}
}

// extractAnnotations extracts Java/Spring/Go annotation tags from a signature string.
func extractAnnotations(sig string) []string {
	var anns []string
	parts := strings.Fields(sig)
	for _, p := range parts {
		if strings.HasPrefix(p, "@") {
			// Clean trailing punctuation: @Slf4j, @Service, etc.
			clean := strings.TrimRight(p, "({,")
			if len(clean) > 1 && len(clean) < 40 {
				anns = append(anns, clean)
			}
		}
	}
	return anns
}

// estimateTokens returns a rough token count using the GPT-4 approximation
// of ~4 characters per token for English/code text.
func estimateTokens(text string) int {
	return (len(text) + 3) / 4
}

// tokenFooter produces the footer line appended to every MCP tool response.
// It shows the token cost of this response vs. what raw file reads would cost.
func tokenFooter(usedTokens, rawTokenEstimate int) string {
	if rawTokenEstimate <= 0 {
		rawTokenEstimate = usedTokens * 8
	}
	saved := rawTokenEstimate - usedTokens
	if saved < 0 {
		saved = 0
	}
	pct := 0
	if rawTokenEstimate > 0 {
		pct = (saved * 100) / rawTokenEstimate
	}
	return fmt.Sprintf("\n\n---\n*Context: ~%d tokens | Saved vs. raw read: ~%d tokens (%d%%)*\n",
		usedTokens, saved, pct)
}
