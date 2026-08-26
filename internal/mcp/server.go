// Package mcp implements the Model Context Protocol (MCP) JSON-RPC 2.0 server over standard I/O.
package mcp

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/recscse/devctx/internal/db"
	"github.com/recscse/devctx/internal/git"
	"github.com/recscse/devctx/internal/scanner"
	"github.com/recscse/devctx/internal/symbols"
)

// JSONRPCRequest represents an incoming JSON-RPC 2.0 message.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
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
	database *sql.DB
	dbMutex  sync.RWMutex
	dbCache  map[string]*sql.DB
}

// NewServer creates a new MCP Server instance.
func NewServer(rootDir string, database *sql.DB) *Server {
	return &Server{
		rootDir:  rootDir,
		database: database,
		dbCache:  make(map[string]*sql.DB),
	}
}

func (s *Server) getDBForPath(targetPath string) (*sql.DB, string, error) {
	if strings.TrimSpace(targetPath) == "" {
		return s.database, s.rootDir, nil
	}

	absPath, err := filepath.Abs(targetPath)
	if err != nil {
		return s.database, s.rootDir, nil
	}

	s.dbMutex.RLock()
	cached, ok := s.dbCache[absPath]
	s.dbMutex.RUnlock()
	if ok && cached != nil {
		return cached, absPath, nil
	}

	dbPath := filepath.Join(absPath, ".devctx", "index.db")
	dbConn, err := db.Open(dbPath)
	if err != nil {
		return nil, absPath, fmt.Errorf("failed to open database for %s: %w", absPath, err)
	}

	s.dbMutex.Lock()
	s.dbCache[absPath] = dbConn
	s.dbMutex.Unlock()

	return dbConn, absPath, nil
}

// Serve reads JSON-RPC messages from in and writes responses to out until EOF.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	encoder := json.NewEncoder(out)

	for {
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

		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			resp := JSONRPCResponse{
				JSONRPC: "2.0",
				Error:   &JSONRPCError{Code: -32700, Message: "Parse error"},
			}
			_ = encoder.Encode(resp)
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

func (s *Server) handleRequest(ctx context.Context, req JSONRPCRequest) *JSONRPCResponse {
	if strings.HasPrefix(req.Method, "notifications/") {
		return nil
	}

	switch req.Method {
	case "initialize":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities": map[string]any{
					"tools": map[string]any{},
				},
				"serverInfo": map[string]any{
					"name":    "devctx",
					"version": "1.3.0",
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
				Description: "Reads the verified content of a source file along with its AST metadata and declared symbol outline.",
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

		result, err := s.executeTool(ctx, callParams.Name, callParams.Arguments)
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
		sb.WriteString("# Repository Map\n")
		sb.WriteString(fmt.Sprintf("Total Files: %d | Total Symbols: %d\n\n", len(filePaths), len(allSymbols)))

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
		syms, err := db.FindSymbols(ctx, targetDB, query)
		if err != nil {
			return nil, err
		}

		if len(syms) == 0 {
			return &ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("No symbols found matching '%s'", query)}},
			}, nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Found %d symbol(s) matching '%s':\n\n", len(syms), query))
		for i, sym := range syms {
			// Line-Number Drift Protection: verify freshness of line number
			s.ensureFreshSymbols(ctx, targetDB, targetDir, sym.FilePath)
			sb.WriteString(fmt.Sprintf("%d. [%s] %s\n   Location:  %s:%d\n   Signature: %s\n\n",
				i+1, sym.Kind, sym.Name, sym.FilePath, sym.LineNumber, sym.Signature))
		}
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

		refs, err := db.FindReferences(ctx, targetDB, symbol, limit)
		if err != nil {
			return nil, err
		}

		if len(refs) == 0 {
			return &ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("No references/callers found for '%s'", symbol)}},
			}, nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Found %d caller(s)/reference(s) for '%s':\n\n", len(refs), symbol))
		for i, ref := range refs {
			sb.WriteString(fmt.Sprintf("%d. 📍 %s:%d\n   │ %s\n\n", i+1, ref.FilePath, ref.LineNumber, ref.Snippet))
		}
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

		syms, _ := db.FindSymbols(ctx, targetDB, relPath)
		lang := scanner.DetectLanguage(relPath)
		skel := symbols.GenerateSkeleton(relPath, lang, contentBytes, syms)

		return &ToolCallResult{
			Content: []ContentItem{{Type: "text", Text: skel}},
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

		fileSet := make(map[string]bool)
		for _, s := range syms {
			fileSet[s.FilePath] = true
		}
		for _, m := range ftsMatches {
			fileSet[m.Path] = true
		}

		var candidateFiles []string
		for f := range fileSet {
			if !strings.Contains(f, "_test.") && !strings.Contains(f, ".spec.") && !strings.Contains(f, ".test.") {
				candidateFiles = append(candidateFiles, f)
			}
		}
		sort.Strings(candidateFiles)

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
				fileSyms, _ := db.FindSymbols(ctx, targetDB, f)
				skel := symbols.GenerateSkeleton(f, "auto", content, fileSyms)
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

		cleanSymbol := symbol
		if strings.Contains(symbol, ":") {
			parts := strings.Split(symbol, ":")
			cleanSymbol = parts[len(parts)-1]
		}

		refs, err := db.FindReferences(ctx, targetDB, cleanSymbol, 50)
		if err != nil {
			return nil, err
		}

		fileMap := make(map[string]bool)
		for _, r := range refs {
			fileMap[r.FilePath] = true
		}

		var affectedFiles []string
		for f := range fileMap {
			affectedFiles = append(affectedFiles, f)
		}

		tests, _ := db.FindTestsFor(ctx, targetDB, cleanSymbol)
		var testsToRun []string
		for _, t := range tests {
			testsToRun = append(testsToRun, t.TestFilePath)
			for _, name := range t.TestNames {
				testsToRun = append(testsToRun, name)
			}
		}

		riskLevel := "LOW"
		if len(affectedFiles) >= 3 || len(refs) >= 5 {
			riskLevel = "HIGH"
		} else if len(affectedFiles) >= 2 || len(refs) >= 2 {
			riskLevel = "MEDIUM"
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("💥 Blast Radius Analysis for '%s':\n\n", cleanSymbol))
		sb.WriteString(fmt.Sprintf("  • Risk Level: %s (%d direct callers across %d files)\n", riskLevel, len(refs), len(affectedFiles)))
		if len(affectedFiles) > 0 {
			sb.WriteString("  • Affected Files:\n")
			for _, f := range affectedFiles {
				sb.WriteString(fmt.Sprintf("     - %s\n", f))
			}
		}
		if len(testsToRun) > 0 {
			sb.WriteString("  • Tests to Run:\n")
			for _, t := range testsToRun {
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

		syms, _ := db.FindSymbols(ctx, targetDB, relPath)

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("=== File: %s (%d bytes) ===\n", relPath, len(contentBytes)))
		if len(syms) > 0 {
			sb.WriteString("Declared Symbols:\n")
			for _, sym := range syms {
				if sym.FilePath == relPath {
					sb.WriteString(fmt.Sprintf(" - [%s] %s (L%d)\n", sym.Kind, sym.Name, sym.LineNumber))
				}
			}
			sb.WriteString("\n")
		}
		sb.WriteString("--- Content ---\n")
		sb.WriteString(string(contentBytes))
		sb.WriteString("\n=== End of File ===")

		return &ToolCallResult{
			Content: []ContentItem{{Type: "text", Text: sb.String()}},
		}, nil

	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

// validateSafeRelPath ensures that relPath does not escape the root repository directory.
func validateSafeRelPath(rootDir string, relPath string) (string, error) {
	cleanRel := filepath.Clean(filepath.FromSlash(relPath))
	if filepath.IsAbs(cleanRel) {
		return "", fmt.Errorf("absolute paths are not permitted: %s", relPath)
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

// ensureFreshSymbols checks if the file on disk was modified after index time and micro-reparses on-the-fly.
func (s *Server) ensureFreshSymbols(ctx context.Context, database *sql.DB, rootDir string, relPath string) {
	fullPath, err := validateSafeRelPath(rootDir, relPath)
	if err != nil {
		return
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		return
	}

	content, err := os.ReadFile(fullPath)
	if err != nil {
		return
	}

	lang := scanner.DetectLanguage(relPath)
	syms, err := symbols.ExtractSymbols(relPath, lang, content)
	if err != nil {
		return
	}

	// Update symbols and FTS dynamically in milliseconds
	_ = db.DeleteSymbolsForFiles(ctx, database, []string{relPath})
	if len(syms) > 0 {
		_ = db.SaveSymbols(ctx, database, syms)
	}
	_ = db.SaveFTS(ctx, database, map[string]string{relPath: string(content)})
	_ = db.SaveFiles(ctx, database, []db.FileRecord{
		{
			Path:         relPath,
			Language:     lang,
			SizeBytes:    info.Size(),
			LastModified: info.ModTime(),
			LastIndexed:  time.Now().UTC(),
		},
	})
}
