package graph

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// maxBlastDefinitions and maxBlastListed keep blast radius output small enough
// for an agent's context while still saying how much was left out.
const (
	maxBlastDefinitions = 8
	maxBlastListed      = 25
)

// BlastResult is the graph-based blast radius of every Go definition of a name.
type BlastResult struct {
	Symbol           string          `json:"symbol"`
	Method           string          `json:"method"`
	TotalDefinitions int             `json:"total_definitions"`
	Warnings         []string        `json:"warnings,omitempty"`
	Definitions      []*ImpactReport `json:"definitions"`
}

// AnalyzeBlast finds the Go definitions of symbol and reports each one's impact.
// symbol may be "Name", "Recv.Name" or "path/file.go:Name". It returns nil if
// there is no Go definition, so callers can fall back to a text search.
func AnalyzeBlast(ctx context.Context, database *sql.DB, rootDir, symbol string) (*BlastResult, error) {
	if err := EnsureFresh(ctx, database, rootDir); err != nil {
		return nil, err
	}

	name, file := strings.TrimSpace(symbol), ""
	if i := strings.LastIndex(name, ":"); i >= 0 {
		file, name = strings.ReplaceAll(name[:i], "\\", "/"), name[i+1:]
	}
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}

	rows, err := database.QueryContext(ctx, `
		SELECT id, file_path FROM graph_nodes
		WHERE label = ? AND kind IN ('function', 'method') AND file_path LIKE '%.go'
		ORDER BY file_path, line_number;
	`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id, path string
		if err := rows.Scan(&id, &path); err != nil {
			return nil, err
		}
		if file == "" || path == file || strings.HasSuffix(path, "/"+file) {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}

	reports := make([]*ImpactReport, 0, len(ids))
	for _, id := range ids {
		report, err := AnalyzeImpact(ctx, database, id)
		if err != nil {
			return nil, err
		}
		reports = append(reports, report)
	}
	// The definitions most likely to matter come first.
	sort.SliceStable(reports, func(i, j int) bool {
		return len(reports[i].DirectCallers)+len(reports[i].Transitive) > len(reports[j].DirectCallers)+len(reports[j].Transitive)
	})
	result := &BlastResult{Symbol: name, Method: "type_checked_call_graph", TotalDefinitions: len(reports), Definitions: reports, Warnings: Warnings(ctx, database)}
	if len(reports) > maxBlastDefinitions {
		result.Definitions = reports[:maxBlastDefinitions]
	}
	return result, nil
}

// Format renders a blast result as compact text for the CLI and MCP tool.
func (r *BlastResult) Format() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Blast radius for '%s' (type-checked Go call graph): %d definition(s)\n", r.Symbol, r.TotalDefinitions)
	if r.TotalDefinitions > 1 {
		sb.WriteString("Several definitions share this name. Use file.go:Name to select one.\n")
	}
	for _, def := range r.Definitions {
		fmt.Fprintf(&sb, "\n%s:%d  %s\n", def.TargetNode.FilePath, def.TargetNode.LineNumber, firstNonEmpty(def.TargetNode.Signature, def.TargetNode.Label))
		fmt.Fprintf(&sb, "  Risk: %s | %d direct, %d indirect callers | %d file(s) | %d test(s)\n",
			def.RiskLevel, len(def.DirectCallers), len(def.Transitive), len(def.ImpactedFiles), len(def.TestsToRun))
		writeNodes(&sb, "Direct callers", def.DirectCallers)
		writeNodes(&sb, "Indirect callers (2 hops)", def.Transitive)
		writeNodes(&sb, "Tests to run", def.TestsToRun)
	}
	if hidden := r.TotalDefinitions - len(r.Definitions); hidden > 0 {
		fmt.Fprintf(&sb, "\n%d more definition(s) with fewer callers not shown.\n", hidden)
	}
	for _, w := range r.Warnings {
		fmt.Fprintf(&sb, "\nWarning: %s\n", w)
	}
	sb.WriteString("\nThese are known callers, not a guarantee: calls through standard library interfaces and reflection are not included.")
	return sb.String()
}

func writeNodes(sb *strings.Builder, title string, nodes []Node) {
	if len(nodes) == 0 {
		return
	}
	fmt.Fprintf(sb, "  %s:\n", title)
	for i, n := range nodes {
		if i == maxBlastListed {
			fmt.Fprintf(sb, "    ... and %d more\n", len(nodes)-maxBlastListed)
			return
		}
		if n.LineNumber > 0 {
			fmt.Fprintf(sb, "    %s (%s:%d)\n", n.Label, n.FilePath, n.LineNumber)
		} else {
			fmt.Fprintf(sb, "    %s\n", n.FilePath)
		}
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
