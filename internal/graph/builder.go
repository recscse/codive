package graph

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/recscse/codive/internal/db"
)

// Line patterns for code the Go analyzer does not cover.
// Spring Boot: @GetMapping("/api/...") or @RequestMapping(value = "/api/...", method = RequestMethod.GET)
var springMappingRe = regexp.MustCompile(`@(Get|Post|Put|Delete|Patch|Request)Mapping\s*\(\s*(?:value\s*=\s*)?["']([^"']+)["']`)

// Express / Koa: app.post('/path', ...)
var jsExpressRouteRe = regexp.MustCompile(`\b(?:app|router|server)\.(get|post|put|delete|patch)\s*\(\s*["']([^"']+)["']`)
var pythonRouteRe = regexp.MustCompile(`@(?:app|router)\.(get|post|put|delete|patch)\s*\(\s*["']([^"']+)["']`)

// Message Broker / Kafka / PubSub patterns
var kafkaProduceRe = regexp.MustCompile(`(?i)(?:kafka|producer|publish)\w*\s*\(\s*["']([^"']+)["']`)
var kafkaTopicRe = regexp.MustCompile(`(?i)\btopic\s*:?=\s*["']([^"']+)["']`)

// Scheduler / Cron patterns
var cronRe = regexp.MustCompile(`(?i)(?:cron|schedule|ticker)\w*\s*\(\s*["']([^"']+)["']`)
var timerRe = regexp.MustCompile(`(?i)(?:time\.NewTicker|time\.Tick|cron\.New|schedule\.Add)`)

// Database access patterns. Uppercase-only: lowercase matching catches English prose.
var dbReadRe = regexp.MustCompile(`\b(?:FROM|JOIN)\s+([a-zA-Z_][a-zA-Z0-9_]*)`)
var dbWriteRe = regexp.MustCompile(`\b(?:INTO|UPDATE|TABLE(?:\s+IF\s+NOT\s+EXISTS)?)\s+([a-zA-Z_][a-zA-Z0-9_]*)`)

var tableAccesses = []struct {
	re   *regexp.Regexp
	kind EdgeKind
}{{dbReadRe, EdgeReadsFrom}, {dbWriteRe, EdgeWritesTo}}

// nonUserTables are SQL keywords the patterns can catch and database internals.
var nonUserTables = map[string]bool{
	"select": true, "set": true, "where": true, "values": true, "join": true, "if": true,
	"sys": true, "dual": true, "sqlite_master": true, "sqlite_schema": true, "sqlite_sequence": true,
	"information_schema": true, "pg_catalog": true, "pg_class": true, "pg_tables": true, "mysql": true,
}

func isUserTable(table string) bool {
	return !nonUserTables[table] && len(table) > 2
}

// addTable records a table node and links it to the function accessing it.
func addTable(nodes []Node, edges []Edge, nodeMap map[string]bool, table, cleanPath string, line int, callerID string, kind EdgeKind) ([]Node, []Edge) {
	dbID := "db:table:" + table
	if !nodeMap[dbID] {
		nodes = append(nodes, Node{
			ID:         dbID,
			Kind:       KindDatabase,
			Label:      "Table: " + table,
			FilePath:   cleanPath,
			LineNumber: line,
			Metadata: map[string]string{
				"table": table,
				"type":  "Database Table",
			},
		})
		nodeMap[dbID] = true
	}
	if nodeMap[callerID] {
		edges = append(edges, Edge{
			SourceID:     callerID,
			TargetID:     dbID,
			Kind:         kind,
			Evidence:     EvidenceInferred,
			LocationFile: cleanPath,
			LocationLine: line,
			Confidence:   0.7,
		})
	}
	return nodes, edges
}

// isExamplePath reports code that documents a project rather than being part of it.
func isExamplePath(cleanPath string) bool {
	for _, part := range strings.Split(path.Dir(cleanPath), "/") {
		switch part {
		case "_examples", "examples", "example", "testdata", "vendor", "third_party":
			return true
		}
	}
	return false
}

// patternLanguages are the indexed languages scanned for line patterns;
// docs and config mention routes and SQL without implementing them.
var patternLanguages = map[string]bool{
	"Go": true, "Python": true, "JavaScript": true, "TypeScript": true, "Java": true, "Kotlin": true,
	"Scala": true, "Ruby": true, "PHP": true, "C#": true, "Rust": true, "Swift": true,
}

func isTestPath(cleanPath string) bool {
	base := path.Base(cleanPath)
	if strings.HasSuffix(base, "_test.go") || strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") ||
		strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") || strings.HasSuffix(strings.TrimSuffix(base, path.Ext(base)), "Test") {
		return true
	}
	return strings.Contains("/"+cleanPath, "/testdata/") || strings.Contains("/"+cleanPath, "/tests/") || strings.Contains("/"+cleanPath, "/__tests__/")
}

func isCommentLine(trimmed string) bool {
	for _, prefix := range []string{"//", "#", "*", "/*", "--"} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

// BuildGraph scans the workspace index, extracts routes, constructs nodes,
// links calls and hierarchy, and persists the graph into graph_nodes and graph_edges.
func BuildGraph(ctx context.Context, database *sql.DB, rootDir string) (*GraphSnapshot, error) {
	allFiles, err := db.GetAllFiles(ctx, database)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve files: %w", err)
	}

	allSymbols, err := db.GetAllSymbols(ctx, database)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve symbols: %w", err)
	}

	var nodes []Node
	var edges []Edge
	nodeMap := make(map[string]bool)
	symByName := make(map[string][]string) // name -> list of node IDs
	funcsByFile := make(map[string][]fileFunc)
	// Route handlers are looked up among the route's own file's symbols only.
	symNamesByFile := make(map[string][]string)

	// 1. Files & Packages
	pkgDirs := make(map[string]bool)
	for filePath, rec := range allFiles {
		cleanPath := filepath.ToSlash(filePath)
		dir := path.Dir(cleanPath)
		if dir == "." {
			dir = "(root)"
		}

		if !pkgDirs[dir] {
			pkgID := "pkg:" + dir
			pkgDirs[dir] = true
			if !nodeMap[pkgID] {
				nodes = append(nodes, Node{
					ID:       pkgID,
					Kind:     KindPackage,
					Label:    dir,
					FilePath: dir,
				})
				nodeMap[pkgID] = true
			}
		}

		fileID := "file:" + cleanPath
		if !nodeMap[fileID] {
			nodes = append(nodes, Node{
				ID:       fileID,
				Kind:     KindFile,
				Label:    path.Base(cleanPath),
				FilePath: cleanPath,
				Language: rec.Language,
			})
			nodeMap[fileID] = true
			edges = append(edges, Edge{
				SourceID:   "pkg:" + dir,
				TargetID:   fileID,
				Kind:       EdgeContains,
				Evidence:   EvidenceDirect,
				Confidence: 1.0,
			})
		}
	}

	// 2. Symbols
	for _, sym := range allSymbols {
		cleanPath := filepath.ToSlash(sym.FilePath)
		symID := symbolID(cleanPath, sym.Name, sym.LineNumber)

		kind := KindFunction
		lowerKind := strings.ToLower(sym.Kind)
		switch {
		case lowerKind == "method":
			kind = KindMethod
		case lowerKind == "struct" || lowerKind == "type" || lowerKind == "record" || lowerKind == "enum":
			kind = KindStruct
		case lowerKind == "class":
			kind = KindClass
		case lowerKind == "interface":
			kind = KindInterface
		case isTestPath(cleanPath) && strings.HasPrefix(strings.ToLower(sym.Name), "test"):
			kind = KindTest
		}

		if !nodeMap[symID] {
			nodes = append(nodes, Node{
				ID:         symID,
				Kind:       kind,
				Label:      sym.Name,
				FilePath:   cleanPath,
				LineNumber: sym.LineNumber,
				Signature:  sym.Signature,
			})
			nodeMap[symID] = true
			symByName[sym.Name] = append(symByName[sym.Name], symID)
			symNamesByFile[cleanPath] = append(symNamesByFile[cleanPath], sym.Name)
			if kind == KindFunction || kind == KindMethod {
				funcsByFile[cleanPath] = append(funcsByFile[cleanPath], fileFunc{id: symID, line: sym.LineNumber})
			}

			// File contains symbol
			fileID := "file:" + cleanPath
			if nodeMap[fileID] {
				edges = append(edges, Edge{
					SourceID:   fileID,
					TargetID:   symID,
					Kind:       EdgeContains,
					Evidence:   EvidenceDirect,
					Confidence: 1.0,
				})
			}
		}
	}

	// 3. Go: type-checked call edges, routes and CLI commands
	goNodes, goEdges := analyzeGo(rootDir, allFiles, nodeMap)
	nodes = append(nodes, goNodes...)
	edges = append(edges, goEdges...)

	// 4. Line patterns: non-Go routes, plus brokers, schedulers and tables in any language
	for filePath, rec := range allFiles {
		cleanPath := filepath.ToSlash(filePath)
		if !patternLanguages[rec.Language] || isTestPath(cleanPath) || isExamplePath(cleanPath) {
			continue
		}
		content, readErr := os.ReadFile(filepath.Join(rootDir, filePath))
		if readErr != nil {
			continue
		}

		lines := strings.Split(string(content), "\n")
		for lineIdx, line := range lines {
			lineNum := lineIdx + 1
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || isCommentLine(trimmed) {
				continue
			}

			if rec.Language != "Go" {
				var httpMethod, routePath string
				if m := springMappingRe.FindStringSubmatch(trimmed); len(m) == 3 {
					httpMethod = strings.ToUpper(m[1])
					if httpMethod == "REQUEST" {
						httpMethod = "ANY"
					}
					routePath = m[2]
				} else if m := jsExpressRouteRe.FindStringSubmatch(trimmed); len(m) == 3 {
					httpMethod = strings.ToUpper(m[1])
					routePath = m[2]
				} else if m := pythonRouteRe.FindStringSubmatch(trimmed); len(m) == 3 {
					httpMethod = strings.ToUpper(m[1])
					routePath = m[2]
				}

				if httpMethod != "" && routePath != "" {
					endpointID := fmt.Sprintf("endpoint:%s:%s", httpMethod, routePath)
					if !nodeMap[endpointID] {
						nodes = append(nodes, Node{
							ID:         endpointID,
							Kind:       KindEndpoint,
							Label:      fmt.Sprintf("%s %s", httpMethod, routePath),
							FilePath:   cleanPath,
							LineNumber: lineNum,
							Language:   rec.Language,
							Metadata: map[string]string{
								"method": httpMethod,
								"path":   routePath,
							},
						})
						nodeMap[endpointID] = true
					}

					// Find handler: look in adjacent lines for a function defined in this file
					for lookAhead := 1; lookAhead <= 6 && (lineIdx+lookAhead) < len(lines); lookAhead++ {
						aheadLine := lines[lineIdx+lookAhead]
						for _, name := range symNamesByFile[cleanPath] {
								ids := symByName[name]
							if strings.Contains(aheadLine, name) {
								for _, id := range ids {
									if strings.HasPrefix(id, "sym:"+cleanPath+":") {
										edges = append(edges, Edge{
											SourceID:     endpointID,
											TargetID:     id,
											Kind:         EdgeRoutesTo,
											Evidence:     EvidenceInferred,
											LocationFile: cleanPath,
											LocationLine: lineNum,
											Confidence:   0.7,
										})
										break
									}
								}
							}
						}
					}
				}
			}

			// Detect Message Brokers / Kafka Topics
			var brokerTopic string
			if m := kafkaProduceRe.FindStringSubmatch(trimmed); len(m) == 2 {
				brokerTopic = m[1]
			} else if m := kafkaTopicRe.FindStringSubmatch(trimmed); len(m) == 2 {
				brokerTopic = m[1]
			}
			if brokerTopic != "" {
				brokerID := "broker:kafka:" + brokerTopic
				if !nodeMap[brokerID] {
					nodes = append(nodes, Node{
						ID:         brokerID,
						Kind:       KindBroker,
						Label:      "Topic: " + brokerTopic,
						FilePath:   cleanPath,
						LineNumber: lineNum,
						Metadata: map[string]string{
							"type":  "Kafka / Event Stream",
							"topic": brokerTopic,
						},
					})
					nodeMap[brokerID] = true
				}
			}

			// Detect Schedulers / Cron jobs
			if timerRe.MatchString(trimmed) || cronRe.MatchString(trimmed) {
				schedulerID := fmt.Sprintf("scheduler:%s:%d", cleanPath, lineNum)
				if !nodeMap[schedulerID] {
					nodes = append(nodes, Node{
						ID:         schedulerID,
						Kind:       KindScheduler,
						Label:      fmt.Sprintf("Cron Worker (%s:%d)", filepath.Base(cleanPath), lineNum),
						FilePath:   cleanPath,
						LineNumber: lineNum,
						Metadata: map[string]string{
							"type": "Scheduler / Background Worker",
						},
					})
					nodeMap[schedulerID] = true
				}
			}

			// Detect Database Tables; the Go analyzer reads Go SQL from its string literals
			if rec.Language != "Go" {
				for _, access := range tableAccesses {
					for _, dbMatch := range access.re.FindAllStringSubmatch(trimmed, -1) {
						table := strings.ToLower(dbMatch[1])
						if isUserTable(table) {
							callerID := enclosingFunc(funcsByFile[cleanPath], lineNum)
							nodes, edges = addTable(nodes, edges, nodeMap, table, cleanPath, lineNum, callerID, access.kind)
						}
					}
				}
			}
		}
	}

	// 5. Connect Tests to Tested Symbols
	for _, n := range nodes {
		if n.Kind == KindTest {
			// e.g. TestSchemaMigration -> tested symbol might be SchemaMigration
			targetName := strings.TrimPrefix(n.Label, "Test")
			if targetIDs, exists := symByName[targetName]; exists {
				for _, targetID := range targetIDs {
					edges = append(edges, Edge{
						SourceID:     n.ID,
						TargetID:     targetID,
						Kind:         EdgeTests,
						Evidence:     EvidenceInferred,
						LocationFile: n.FilePath,
						LocationLine: n.LineNumber,
						Confidence:   0.90,
					})
				}
			}
		}
	}

	edges = dedupeEdges(edges)

	// 6. Save in SQLite
	if err := saveGraphToDB(ctx, database, nodes, edges); err != nil {
		return nil, fmt.Errorf("failed to save graph to database: %w", err)
	}
	stdlib := ""
	if hasGoFiles(allFiles) {
		stdlib = "ok"
		if !StdlibAvailable() {
			stdlib = "missing"
		}
	}
	if err := db.SetMeta(ctx, database, graphStdlibKey, stdlib); err != nil {
		return nil, fmt.Errorf("failed to record toolchain status: %w", err)
	}
	if err := db.SetMeta(ctx, database, graphFingerprintKey, filesFingerprint(allFiles)); err != nil {
		return nil, fmt.Errorf("failed to record graph fingerprint: %w", err)
	}

	return &GraphSnapshot{
		Nodes:      nodes,
		Edges:      edges,
		TotalNodes: len(nodes),
		TotalEdges: len(edges),
	}, nil
}

type fileFunc struct {
	id   string
	line int
}

// enclosingFunc returns the last function declared at or above line. funcs is
// in line order because symbols are loaded sorted by file and line.
func enclosingFunc(funcs []fileFunc, line int) string {
	id := ""
	for _, fn := range funcs {
		if fn.line > line {
			break
		}
		id = fn.id
	}
	return id
}

// dedupeEdges keeps the most confident edge per (source, target, kind), the
// uniqueness the graph_edges table enforces.
func dedupeEdges(edges []Edge) []Edge {
	type edgeKey struct {
		source, target string
		kind           EdgeKind
	}
	index := make(map[edgeKey]int, len(edges))
	unique := edges[:0]
	for _, e := range edges {
		key := edgeKey{e.SourceID, e.TargetID, e.Kind}
		if i, seen := index[key]; seen {
			if e.Confidence > unique[i].Confidence {
				unique[i] = e
			}
			continue
		}
		index[key] = len(unique)
		unique = append(unique, e)
	}
	return unique
}

const (
	graphFingerprintKey = "graph_files_fingerprint"
	graphStdlibKey      = "graph_stdlib_types"
)

// MissingToolchainWarning explains the accuracy cost of indexing Go code
// without a Go toolchain, where standard library types can't be loaded.
const MissingToolchainWarning = "Go toolchain not found, so standard library types weren't loaded and some Go callers may be missing. Install Go (https://go.dev/dl/) and run codive reindex for full results."

// Warnings lists accuracy problems recorded when the graph was last built.
func Warnings(ctx context.Context, database *sql.DB) []string {
	if v, _ := db.GetMeta(ctx, database, graphStdlibKey); v == "missing" {
		return []string{MissingToolchainWarning}
	}
	return nil
}

// filesFingerprint changes whenever an indexed file is added, removed or modified.
func filesFingerprint(allFiles map[string]db.FileRecord) string {
	paths := make([]string, 0, len(allFiles))
	for p := range allFiles {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		fmt.Fprintf(h, "%s\x00%+v\n", p, allFiles[p])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// EnsureFresh rebuilds the graph if the file index changed since it was last built.
func EnsureFresh(ctx context.Context, database *sql.DB, rootDir string) error {
	stale, err := IsStale(ctx, database)
	if err != nil || !stale {
		return err
	}
	_, err = BuildGraph(ctx, database, rootDir)
	return err
}

func hasGoFiles(allFiles map[string]db.FileRecord) bool {
	for _, rec := range allFiles {
		if rec.Language == "Go" {
			return true
		}
	}
	return false
}

func saveGraphToDB(ctx context.Context, database *sql.DB, nodes []Node, edges []Edge) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Clear existing graph tables
	for _, stmt := range []string{"DELETE FROM graph_nodes;", "DELETE FROM graph_edges;"} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	nodeStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO graph_nodes (id, kind, label, file_path, line_number, end_line_number, language, signature, metadata_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			kind = excluded.kind,
			label = excluded.label,
			file_path = excluded.file_path,
			line_number = excluded.line_number,
			signature = excluded.signature,
			metadata_json = excluded.metadata_json;
	`)
	if err != nil {
		return err
	}
	defer nodeStmt.Close()

	for _, n := range nodes {
		metaBytes, _ := json.Marshal(n.Metadata)
		if _, err := nodeStmt.ExecContext(ctx,
			n.ID, string(n.Kind), n.Label, n.FilePath, n.LineNumber, n.EndLineNumber, n.Language, n.Signature, string(metaBytes),
		); err != nil {
			return err
		}
	}

	edgeStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO graph_edges (source_id, target_id, kind, evidence, location_file, location_line, confidence, metadata_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_id, target_id, kind) DO NOTHING;
	`)
	if err != nil {
		return err
	}
	defer edgeStmt.Close()

	for _, e := range edges {
		metaBytes, _ := json.Marshal(e.Metadata)
		if _, err := edgeStmt.ExecContext(ctx,
			e.SourceID, e.TargetID, string(e.Kind), string(e.Evidence), e.LocationFile, e.LocationLine, e.Confidence, string(metaBytes),
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// loadPriority is the order nodes are loaded in when the graph is bounded:
// what a newcomer needs first (entry points, tables), then structure, then code.
// Large groups are cut by LIMIT alone: ordering a few hundred thousand symbols
// just to keep some of them costs seconds.
var loadPriority = []struct {
	where   string
	ordered bool
}{
	{"kind IN ('endpoint','database','broker','scheduler')", true},
	{"kind = 'package'", true},
	{"kind = 'file'", true},
	{"kind NOT IN ('endpoint','database','broker','scheduler','package','file','test')", false},
	{"kind = 'test'", false},
}

// LoadGraph loads nodes and edges from SQLite, bounded by a node limit. Only
// edges between loaded nodes are returned, and they are selected in SQL so a
// huge edge table is never read in full.
func LoadGraph(ctx context.Context, database *sql.DB, limit int) (*GraphSnapshot, error) {
	if limit <= 0 {
		limit = 3500
	}

	var totalNodes, totalEdges int
	_ = database.QueryRowContext(ctx, "SELECT COUNT(*) FROM graph_nodes;").Scan(&totalNodes)
	_ = database.QueryRowContext(ctx, "SELECT COUNT(*) FROM graph_edges;").Scan(&totalEdges)

	nodes := []Node{}
	for _, group := range loadPriority {
		remaining := limit - len(nodes)
		if remaining <= 0 {
			break
		}
		query := `SELECT id, kind, label, file_path, line_number, end_line_number, language, signature, metadata_json
			FROM graph_nodes WHERE ` + group.where
		if group.ordered {
			query += " ORDER BY id"
		}
		rows, err := database.QueryContext(ctx, query+" LIMIT ?;", remaining)
		if err != nil {
			return nil, err
		}
		got, err := scanNodes(rows)
		rows.Close()
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, got...)
	}

	var edges []Edge
	var err error
	if len(nodes) >= totalNodes {
		edges, err = loadEdges(ctx, database, "SELECT "+edgeColumns+" FROM graph_edges e;")
	} else {
		edges, err = loadEdgesAmong(ctx, database, nodes)
	}
	if err != nil {
		return nil, err
	}

	return &GraphSnapshot{
		Nodes:      nodes,
		Edges:      edges,
		Warnings:   Warnings(ctx, database),
		TotalNodes: totalNodes,
		TotalEdges: totalEdges,
		Truncated:  totalNodes > len(nodes),
	}, nil
}

const edgeColumns = "e.id, e.source_id, e.target_id, e.kind, e.evidence, e.location_file, e.location_line, e.confidence, e.metadata_json"

type edgeQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func loadEdges(ctx context.Context, q edgeQuerier, query string, args ...any) ([]Edge, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var edges []Edge
	for rows.Next() {
		var e Edge
		var kind, evid, meta string
		if err := rows.Scan(&e.ID, &e.SourceID, &e.TargetID, &kind, &evid, &e.LocationFile, &e.LocationLine, &e.Confidence, &meta); err != nil {
			return nil, err
		}
		e.Kind, e.Evidence = EdgeKind(kind), EvidenceType(evid)
		if meta != "" && meta != "{}" && meta != "null" {
			_ = json.Unmarshal([]byte(meta), &e.Metadata)
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

// loadEdgesAmong returns the edges whose two ends are both in nodes, by joining
// against a temporary table of their ids on one connection.
func loadEdgesAmong(ctx context.Context, database *sql.DB, nodes []Node) ([]Edge, error) {
	conn, err := database.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "CREATE TEMP TABLE IF NOT EXISTS _loaded_ids (id TEXT PRIMARY KEY) WITHOUT ROWID;"); err != nil {
		return nil, err
	}
	defer conn.ExecContext(context.Background(), "DROP TABLE IF EXISTS _loaded_ids;")
	if _, err := conn.ExecContext(ctx, "DELETE FROM _loaded_ids;"); err != nil {
		return nil, err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	stmt, err := tx.PrepareContext(ctx, "INSERT OR IGNORE INTO _loaded_ids (id) VALUES (?);")
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	for _, n := range nodes {
		if _, err := stmt.ExecContext(ctx, n.ID); err != nil {
			stmt.Close()
			tx.Rollback()
			return nil, err
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return loadEdges(ctx, conn, `SELECT `+edgeColumns+`
		FROM graph_edges e
		JOIN _loaded_ids s ON s.id = e.source_id
		JOIN _loaded_ids t ON t.id = e.target_id;`)
}
