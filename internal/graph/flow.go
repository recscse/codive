package graph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/recscse/codive/internal/db"
)

// minTraversalConfidence keeps ambiguous guesses, such as an interface method
// with several implementers, out of traced flows and blast radius results.
const minTraversalConfidence = 0.5

func isFlowEdge(e Edge) bool {
	switch e.Kind {
	case EdgeCalls, EdgeRoutesTo, EdgeReadsFrom, EdgeWritesTo:
		return e.Confidence >= minTraversalConfidence
	}
	return false
}

func isCallerEdge(e Edge) bool {
	switch e.Kind {
	case EdgeCalls, EdgeReferences, EdgeRoutesTo:
		return e.Confidence >= minTraversalConfidence
	}
	return false
}

// TraverseExecutionFlow follows forward calls from a starting node (such as an endpoint,
// main, or handler) up to maxDepth hops, keeping track of branches, alternative paths,
// and associated unit tests.
func TraverseExecutionFlow(ctx context.Context, database *sql.DB, startID string, maxDepth int) (*ExecutionFlow, error) {
	if maxDepth <= 0 {
		maxDepth = 6
	}

	startNode, err := getNodeByID(ctx, database, startID)
	if err != nil {
		return nil, fmt.Errorf("starting node not found: %w", err)
	}

	visited := make(map[string]bool)
	var steps []FlowStep
	stepIndex := 0
	branchPoints := 0

	type queueItem struct {
		nodeID   string
		depth    int
		callEdge *Edge
	}

	queue := []queueItem{{nodeID: startID, depth: 0, callEdge: nil}}
	visited[startID] = true

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		node, err := getNodeByID(ctx, database, curr.nodeID)
		if err != nil {
			continue
		}

		// Find outgoing call/route edges
		outgoingEdges, err := getOutgoingEdges(ctx, database, curr.nodeID)
		if err != nil {
			outgoingEdges = nil
		}

		var branchIDs []string
		for _, edge := range outgoingEdges {
			if isFlowEdge(edge) {
				branchIDs = append(branchIDs, edge.TargetID)
			}
		}

		if len(branchIDs) > 1 {
			branchPoints++
		}

		steps = append(steps, FlowStep{
			StepIndex: stepIndex,
			Node:      *node,
			CallEdge:  curr.callEdge,
			Depth:     curr.depth,
			Branches:  branchIDs,
		})
		stepIndex++

		if curr.depth < maxDepth {
			for _, edge := range outgoingEdges {
				if isFlowEdge(edge) && !visited[edge.TargetID] {
					visited[edge.TargetID] = true
					eCopy := edge
					queue = append(queue, queueItem{
						nodeID:   edge.TargetID,
						depth:    curr.depth + 1,
						callEdge: &eCopy,
					})
				}
			}
		}
	}

	// Discover associated unit tests
	relatedTests, _ := findTestsForNode(ctx, database, *startNode)

	return &ExecutionFlow{
		EntryPoint:   *startNode,
		Steps:        steps,
		TotalSteps:   len(steps),
		MaxDepth:     maxDepth,
		BranchPoints: branchPoints,
		RelatedTests: relatedTests,
	}, nil
}

// AnalyzeImpact generates an interactive blast radius sub-graph for a target symbol.
func AnalyzeImpact(ctx context.Context, database *sql.DB, targetID string) (*ImpactReport, error) {
	targetNode, err := getNodeByID(ctx, database, targetID)
	if err != nil {
		return nil, fmt.Errorf("target node not found: %w", err)
	}

	// Direct callers
	directEdges, err := getIncomingEdges(ctx, database, targetID)
	if err != nil {
		return nil, err
	}

	var directCallers []Node
	directMap := make(map[string]bool)
	impactedFilesMap := make(map[string]bool)
	if targetNode.FilePath != "" {
		impactedFilesMap[targetNode.FilePath] = true
	}

	// Callers in test code are tests to run, not production code at risk.
	var callingTests []Node
	seenTests := make(map[string]bool)
	classify := func(sourceID string) (*Node, bool) {
		caller, err := getNodeByID(ctx, database, sourceID)
		if err != nil {
			return nil, false
		}
		if isTestPath(caller.FilePath) {
			if !seenTests[caller.ID] {
				seenTests[caller.ID] = true
				callingTests = append(callingTests, *caller)
			}
			return nil, false
		}
		directMap[caller.ID] = true
		if caller.FilePath != "" {
			impactedFilesMap[caller.FilePath] = true
		}
		return caller, true
	}

	for _, e := range directEdges {
		if isCallerEdge(e) && !directMap[e.SourceID] {
			if caller, ok := classify(e.SourceID); ok {
				directCallers = append(directCallers, *caller)
			}
		}
	}

	// Transitive callers (depth 2)
	var transitiveCallers []Node
	for _, dc := range directCallers {
		secondHopEdges, _ := getIncomingEdges(ctx, database, dc.ID)
		for _, e2 := range secondHopEdges {
			if isCallerEdge(e2) && !directMap[e2.SourceID] && e2.SourceID != targetID {
				if caller, ok := classify(e2.SourceID); ok {
					transitiveCallers = append(transitiveCallers, *caller)
				}
			}
		}
	}

	// Risk level heuristic
	riskLevel := "LOW"
	totalCallers := len(directCallers) + len(transitiveCallers)
	if totalCallers > 10 || len(impactedFilesMap) > 5 {
		riskLevel = "HIGH"
	} else if totalCallers > 3 || len(impactedFilesMap) > 2 {
		riskLevel = "MEDIUM"
	}

	// Convert file map to slice
	var impactedFiles []string
	for f := range impactedFilesMap {
		impactedFiles = append(impactedFiles, f)
	}

	// Tests
	tests, _ := findTestsForNode(ctx, database, *targetNode)
	for _, t := range callingTests {
		if !containsNode(tests, t.ID) {
			tests = append(tests, t)
		}
	}

	return &ImpactReport{
		TargetNode:    *targetNode,
		RiskLevel:     riskLevel,
		DirectCallers: directCallers,
		Transitive:    transitiveCallers,
		ImpactedFiles: impactedFiles,
		TestsToRun:    tests,
	}, nil
}

func containsNode(nodes []Node, id string) bool {
	for _, n := range nodes {
		if n.ID == id {
			return true
		}
	}
	return false
}

func getNodeByID(ctx context.Context, database *sql.DB, id string) (*Node, error) {
	row := database.QueryRowContext(ctx, `
		SELECT id, kind, label, file_path, line_number, end_line_number, language, signature, metadata_json
		FROM graph_nodes WHERE id = ?;
	`, id)

	var n Node
	var kindStr, metaStr string
	if err := row.Scan(&n.ID, &kindStr, &n.Label, &n.FilePath, &n.LineNumber, &n.EndLineNumber, &n.Language, &n.Signature, &metaStr); err != nil {
		return nil, err
	}
	n.Kind = NodeKind(kindStr)
	if metaStr != "" {
		_ = json.Unmarshal([]byte(metaStr), &n.Metadata)
	}
	return &n, nil
}

func getOutgoingEdges(ctx context.Context, database *sql.DB, sourceID string) ([]Edge, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, source_id, target_id, kind, evidence, location_file, location_line, confidence, metadata_json
		FROM graph_edges WHERE source_id = ?;
	`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var edges []Edge
	for rows.Next() {
		var e Edge
		var kindStr, evidStr, metaStr string
		if err := rows.Scan(&e.ID, &e.SourceID, &e.TargetID, &kindStr, &evidStr, &e.LocationFile, &e.LocationLine, &e.Confidence, &metaStr); err != nil {
			return nil, err
		}
		e.Kind = EdgeKind(kindStr)
		e.Evidence = EvidenceType(evidStr)
		if metaStr != "" {
			_ = json.Unmarshal([]byte(metaStr), &e.Metadata)
		}
		edges = append(edges, e)
	}
	return edges, nil
}

func getIncomingEdges(ctx context.Context, database *sql.DB, targetID string) ([]Edge, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, source_id, target_id, kind, evidence, location_file, location_line, confidence, metadata_json
		FROM graph_edges WHERE target_id = ?;
	`, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var edges []Edge
	for rows.Next() {
		var e Edge
		var kindStr, evidStr, metaStr string
		if err := rows.Scan(&e.ID, &e.SourceID, &e.TargetID, &kindStr, &evidStr, &e.LocationFile, &e.LocationLine, &e.Confidence, &metaStr); err != nil {
			return nil, err
		}
		e.Kind = EdgeKind(kindStr)
		e.Evidence = EvidenceType(evidStr)
		if metaStr != "" {
			_ = json.Unmarshal([]byte(metaStr), &e.Metadata)
		}
		edges = append(edges, e)
	}
	return edges, nil
}

func findTestsForNode(ctx context.Context, database *sql.DB, n Node) ([]Node, error) {
	// Look up explicit TESTS edges
	rows, err := database.QueryContext(ctx, `
		SELECT gn.id, gn.kind, gn.label, gn.file_path, gn.line_number, gn.end_line_number, gn.language, gn.signature, gn.metadata_json
		FROM graph_edges ge
		JOIN graph_nodes gn ON ge.source_id = gn.id
		WHERE ge.target_id = ? AND ge.kind = 'TESTS';
	`, n.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tests []Node
	for rows.Next() {
		var t Node
		var kindStr, metaStr string
		if err := rows.Scan(&t.ID, &kindStr, &t.Label, &t.FilePath, &t.LineNumber, &t.EndLineNumber, &t.Language, &t.Signature, &metaStr); err == nil {
			t.Kind = NodeKind(kindStr)
			if metaStr != "" {
				_ = json.Unmarshal([]byte(metaStr), &t.Metadata)
			}
			tests = append(tests, t)
		}
	}

	// Fallback: query db.FindTestsFor if no explicit edge
	if len(tests) == 0 && n.Label != "" {
		testLocations, err := db.FindTestsFor(ctx, database, n.Label)
		if err == nil {
			for _, tl := range testLocations {
				tests = append(tests, Node{
					ID:       "file:" + tl.TestFilePath,
					Kind:     KindTest,
					Label:    filepath.Base(tl.TestFilePath),
					FilePath: tl.TestFilePath,
				})
			}
		}
	}

	return tests, nil
}
