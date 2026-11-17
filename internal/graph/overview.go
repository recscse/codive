package graph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/recscse/codive/internal/db"
)

// LanguageStat is how much of the repository is written in one language.
type LanguageStat struct {
	Name  string `json:"name"`
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"`
}

// AreaStat is one part of the repository: a folder that holds a meaningful
// share of the code, with its size.
type AreaStat struct {
	Path    string `json:"path"`
	Files   int    `json:"files"`
	Symbols int    `json:"symbols"`
}

// Overview is a plain summary of a repository for people who don't know it yet.
// Everything in it is counted from the index; nothing is estimated.
type Overview struct {
	Files     int            `json:"files"`
	Symbols   int            `json:"symbols"`
	Counts    map[string]int `json:"counts"`
	Languages []LanguageStat `json:"languages"`
	Areas     []AreaStat     `json:"areas"`
	// EntryPoints and Tables are samples; Counts holds the full totals.
	EntryPoints []Node `json:"entry_points"`
	Tables      []Node `json:"tables"`
	// CallTracingLanguages are the languages for which the graph holds call
	// edges. Callers, flow and blast radius are only meaningful for these.
	CallTracingLanguages []string `json:"call_tracing_languages"`
	Warnings             []string `json:"warnings,omitempty"`
}

// symbolNodeKinds are the graph node kinds that are code symbols, as opposed
// to structure (packages, files).
var symbolNodeKinds = map[NodeKind]bool{
	KindFunction: true, KindMethod: true, KindStruct: true, KindClass: true,
	KindInterface: true, KindTest: true,
}

// IsStale reports whether the graph is missing or out of date relative to the
// file index, without rebuilding it.
func IsStale(ctx context.Context, database *sql.DB) (bool, error) {
	allFiles, err := db.GetAllFiles(ctx, database)
	if err != nil {
		return false, fmt.Errorf("failed to retrieve files: %w", err)
	}
	stored, err := db.GetMeta(ctx, database, graphFingerprintKey)
	if err != nil {
		return false, err
	}
	return stored != filesFingerprint(allFiles), nil
}

// BuildOverview summarises the indexed repository.
func BuildOverview(ctx context.Context, database *sql.DB) (*Overview, error) {
	ov := &Overview{Counts: map[string]int{}, Warnings: Warnings(ctx, database)}

	rows, err := database.QueryContext(ctx, `SELECT kind, COUNT(*) FROM graph_nodes GROUP BY kind;`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			rows.Close()
			return nil, err
		}
		ov.Counts[kind] = n
		if symbolNodeKinds[NodeKind(kind)] {
			ov.Symbols += n
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	allFiles, err := db.GetAllFiles(ctx, database)
	if err != nil {
		return nil, err
	}
	ov.Files = len(allFiles)

	langs := map[string]*LanguageStat{}
	for _, rec := range allFiles {
		l := langs[rec.Language]
		if l == nil {
			l = &LanguageStat{Name: rec.Language}
			langs[rec.Language] = l
		}
		l.Files++
		l.Bytes += rec.SizeBytes
	}
	for _, l := range langs {
		ov.Languages = append(ov.Languages, *l)
	}
	sort.Slice(ov.Languages, func(i, j int) bool {
		if ov.Languages[i].Files != ov.Languages[j].Files {
			return ov.Languages[i].Files > ov.Languages[j].Files
		}
		return ov.Languages[i].Name < ov.Languages[j].Name
	})

	symbolsByFile := map[string]int{}
	srows, err := database.QueryContext(ctx, `SELECT file_path, COUNT(*) FROM symbols GROUP BY file_path;`)
	if err != nil {
		return nil, err
	}
	for srows.Next() {
		var p string
		var n int
		if err := srows.Scan(&p, &n); err != nil {
			srows.Close()
			return nil, err
		}
		symbolsByFile[p] = n
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		return nil, err
	}
	ov.Areas = repoAreas(allFiles, symbolsByFile)

	if ov.EntryPoints, err = sampleNodes(ctx, database, KindEndpoint, 15); err != nil {
		return nil, err
	}
	if ov.Tables, err = sampleNodes(ctx, database, KindDatabase, 15); err != nil {
		return nil, err
	}

	lrows, err := database.QueryContext(ctx, `
		SELECT DISTINCT f.language
		FROM graph_edges e
		JOIN graph_nodes n ON n.id = e.source_id
		JOIN files f ON f.path = n.file_path
		WHERE e.kind = 'CALLS';`)
	if err != nil {
		return nil, err
	}
	for lrows.Next() {
		var l string
		if err := lrows.Scan(&l); err != nil {
			lrows.Close()
			return nil, err
		}
		ov.CallTracingLanguages = append(ov.CallTracingLanguages, l)
	}
	lrows.Close()
	sort.Strings(ov.CallTracingLanguages)
	return ov, lrows.Err()
}

// repoAreas splits the repository into its main folders. Many projects keep
// everything under one or two wrapper folders (src/main/java/com/acme/...), so
// while one folder holds nearly everything the view descends into it, until
// the code actually divides into parts a newcomer can tell apart.
func repoAreas(files map[string]db.FileRecord, symbolsByFile map[string]int) []AreaStat {
	const (
		maxDepth      = 8
		dominantShare = 0.8
		maxAreas      = 14
	)
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, path.Clean(strings.ReplaceAll(p, "\\", "/")))
	}
	areaOf := func(p string, depth int) string {
		dir := path.Dir(p)
		if dir == "." {
			return "(root)"
		}
		parts := strings.Split(dir, "/")
		if len(parts) > depth {
			parts = parts[:depth]
		}
		return strings.Join(parts, "/")
	}

	depth := 1
	var counts map[string]int
	for {
		counts = map[string]int{}
		for _, p := range paths {
			counts[areaOf(p, depth)]++
		}
		if depth >= maxDepth || len(paths) == 0 {
			break
		}
		top, topN := "", 0
		for a, n := range counts {
			if n > topN || (n == topN && a < top) {
				top, topN = a, n
			}
		}
		if top == "(root)" || float64(topN) < dominantShare*float64(len(paths)) {
			break
		}
		// Descend only if the dominant folder actually has deeper structure.
		deeper := false
		for _, p := range paths {
			if areaOf(p, depth) == top && areaOf(p, depth+1) != top {
				deeper = true
				break
			}
		}
		if !deeper {
			break
		}
		depth++
	}

	stats := map[string]*AreaStat{}
	for _, p := range paths {
		a := areaOf(p, depth)
		s := stats[a]
		if s == nil {
			s = &AreaStat{Path: a}
			stats[a] = s
		}
		s.Files++
	}
	for p, n := range symbolsByFile {
		if s := stats[areaOf(path.Clean(strings.ReplaceAll(p, "\\", "/")), depth)]; s != nil {
			s.Symbols += n
		}
	}
	out := make([]AreaStat, 0, len(stats))
	for _, s := range stats {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Files != out[j].Files {
			return out[i].Files > out[j].Files
		}
		return out[i].Path < out[j].Path
	})
	if len(out) > maxAreas {
		out = out[:maxAreas]
	}
	return out
}

func sampleNodes(ctx context.Context, database *sql.DB, kind NodeKind, limit int) ([]Node, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, kind, label, file_path, line_number, end_line_number, language, signature, metadata_json
		FROM graph_nodes WHERE kind = ? ORDER BY label LIMIT ?;`, string(kind), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNodes(rows)
}

func scanNodes(rows *sql.Rows) ([]Node, error) {
	nodes := []Node{}
	for rows.Next() {
		var n Node
		var kind, meta string
		if err := rows.Scan(&n.ID, &kind, &n.Label, &n.FilePath, &n.LineNumber, &n.EndLineNumber, &n.Language, &n.Signature, &meta); err != nil {
			return nil, err
		}
		n.Kind = NodeKind(kind)
		if meta != "" {
			_ = json.Unmarshal([]byte(meta), &n.Metadata)
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// likeEscape makes s safe to embed in a LIKE pattern that uses ESCAPE '\'.
func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// SearchNodes finds graph nodes by name or file path directly in the database,
// so search works on repositories too large to load into the browser.
func SearchNodes(ctx context.Context, database *sql.DB, query string, limit int) ([]Node, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return []Node{}, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 40
	}
	esc := likeEscape(q)
	rows, err := database.QueryContext(ctx, `
		SELECT id, kind, label, file_path, line_number, end_line_number, language, signature, metadata_json
		FROM graph_nodes
		WHERE kind != 'package'
		  AND (label LIKE ? ESCAPE '\' OR file_path LIKE ? ESCAPE '\')
		ORDER BY
			CASE WHEN lower(label) = lower(?) THEN 0
			     WHEN label LIKE ? ESCAPE '\' THEN 1
			     WHEN label LIKE ? ESCAPE '\' THEN 2
			     ELSE 3 END,
			CASE kind WHEN 'test' THEN 1 ELSE 0 END,
			length(label), label
		LIMIT ?;`,
		"%"+esc+"%", "%"+esc+"%", q, esc+"%", "%"+esc+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNodes(rows)
}

// maxNeighborhoodEdges bounds the edges returned for one node, so a method
// called from thousands of places can't produce an enormous response.
const maxNeighborhoodEdges = 500

// NodeNeighborhood returns one node, the edges touching it, and the nodes at
// the other end of those edges: enough to render its page without the whole graph.
func NodeNeighborhood(ctx context.Context, database *sql.DB, id string) (*GraphSnapshot, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, kind, label, file_path, line_number, end_line_number, language, signature, metadata_json
		FROM graph_nodes WHERE id = ?;`, id)
	if err != nil {
		return nil, err
	}
	nodes, err := scanNodes(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, sql.ErrNoRows
	}

	erows, err := database.QueryContext(ctx, `
		SELECT id, source_id, target_id, kind, evidence, location_file, location_line, confidence, metadata_json
		FROM (
			SELECT * FROM graph_edges WHERE source_id = ?
			UNION
			SELECT * FROM graph_edges WHERE target_id = ?
		)
		ORDER BY confidence DESC LIMIT ?;`, id, id, maxNeighborhoodEdges+1)
	if err != nil {
		return nil, err
	}
	defer erows.Close()

	var edges []Edge
	seen := map[string]bool{id: true}
	var others []string
	for erows.Next() {
		var e Edge
		var kind, evid, meta string
		if err := erows.Scan(&e.ID, &e.SourceID, &e.TargetID, &kind, &evid, &e.LocationFile, &e.LocationLine, &e.Confidence, &meta); err != nil {
			return nil, err
		}
		e.Kind, e.Evidence = EdgeKind(kind), EvidenceType(evid)
		if meta != "" {
			_ = json.Unmarshal([]byte(meta), &e.Metadata)
		}
		edges = append(edges, e)
		for _, other := range []string{e.SourceID, e.TargetID} {
			if !seen[other] {
				seen[other] = true
				others = append(others, other)
			}
		}
	}
	if err := erows.Err(); err != nil {
		return nil, err
	}
	truncated := len(edges) > maxNeighborhoodEdges
	if truncated {
		edges = edges[:maxNeighborhoodEdges]
	}

	for i := 0; i < len(others); i += 200 {
		end := i + 200
		if end > len(others) {
			end = len(others)
		}
		batch := others[i:end]
		args := make([]any, len(batch))
		for j, o := range batch {
			args[j] = o
		}
		nrows, err := database.QueryContext(ctx, `
			SELECT id, kind, label, file_path, line_number, end_line_number, language, signature, metadata_json
			FROM graph_nodes WHERE id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")+`);`, args...)
		if err != nil {
			return nil, err
		}
		more, err := scanNodes(nrows)
		nrows.Close()
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, more...)
	}
	return &GraphSnapshot{Nodes: nodes, Edges: edges, TotalNodes: len(nodes), TotalEdges: len(edges), Truncated: truncated}, nil
}
