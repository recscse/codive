// Package graph provides graph data models, AST relationship extraction,
// and execution-flow path traversal for the Codive Codebase Explorer.
package graph

// NodeKind identifies the semantic category of a graph node.
type NodeKind string

const (
	KindRepo      NodeKind = "repository"
	KindModule    NodeKind = "module"
	KindPackage   NodeKind = "package"
	KindFile      NodeKind = "file"
	KindClass     NodeKind = "class"
	KindInterface NodeKind = "interface"
	KindStruct    NodeKind = "struct"
	KindFunction  NodeKind = "function"
	KindMethod    NodeKind = "method"
	KindEndpoint  NodeKind = "endpoint"
	KindModel     NodeKind = "model"
	KindDatabase  NodeKind = "database"
	KindTest      NodeKind = "test"
	KindBroker    NodeKind = "broker"    // Kafka, RabbitMQ, Redis PubSub
	KindScheduler NodeKind = "scheduler" // Cron, Tickers, Workers
	KindExternal  NodeKind = "external"  // 3rd-party APIs, Stripe, AWS
)

// EdgeKind identifies the semantic relationship connecting two nodes.
type EdgeKind string

const (
	EdgeContains     EdgeKind = "CONTAINS"
	EdgeImports      EdgeKind = "IMPORTS"
	EdgeCalls        EdgeKind = "CALLS"
	EdgeReferences   EdgeKind = "REFERENCES"
	EdgeImplements   EdgeKind = "IMPLEMENTS"
	EdgeExtends      EdgeKind = "EXTENDS"
	EdgeDependsOn    EdgeKind = "DEPENDS_ON"
	EdgeRoutesTo     EdgeKind = "ROUTES_TO"
	EdgeAccepts      EdgeKind = "ACCEPTS_TYPE"
	EdgeReturns      EdgeKind = "RETURNS_TYPE"
	EdgeTransforms   EdgeKind = "TRANSFORMS"
	EdgeReadsFrom    EdgeKind = "READS_FROM"
	EdgeWritesTo     EdgeKind = "WRITES_TO"
	EdgeTests        EdgeKind = "TESTS"
	EdgeProducesTo   EdgeKind = "PRODUCES_TO"   // Message queue publish
	EdgeConsumesFrom EdgeKind = "CONSUMES_FROM" // Message queue consumer
	EdgeSchedules    EdgeKind = "SCHEDULES"     // Cron/timer trigger
)

// EvidenceType represents the degree of certainty of an edge.
type EvidenceType string

const (
	EvidenceDirect   EvidenceType = "directly_extracted"
	EvidenceInferred EvidenceType = "inferred"
	EvidenceRuntime  EvidenceType = "runtime_observed"
)

// Node represents a vertex in the codebase graph.
type Node struct {
	ID            string            `json:"id"`
	Kind          NodeKind          `json:"kind"`
	Label         string            `json:"label"`
	FilePath      string            `json:"file_path"`
	LineNumber    int               `json:"line_number,omitempty"`
	EndLineNumber int               `json:"end_line_number,omitempty"`
	Language      string            `json:"language,omitempty"`
	Signature     string            `json:"signature,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// Edge represents a directed relationship between two nodes.
type Edge struct {
	ID           int64             `json:"id,omitempty"`
	SourceID     string            `json:"source_id"`
	TargetID     string            `json:"target_id"`
	Kind         EdgeKind          `json:"kind"`
	Evidence     EvidenceType      `json:"evidence"`
	LocationFile string            `json:"location_file,omitempty"`
	LocationLine int               `json:"location_line,omitempty"`
	Confidence   float64           `json:"confidence"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// GraphSnapshot is a complete or sub-graph representation returned to callers.
type GraphSnapshot struct {
	Nodes      []Node `json:"nodes"`
	Edges      []Edge `json:"edges"`
	TotalNodes int    `json:"total_nodes"`
	TotalEdges int    `json:"total_edges"`
	Truncated  bool   `json:"truncated,omitempty"`
	// Warnings describes known accuracy limits of this graph, such as a
	// missing Go toolchain.
	Warnings []string `json:"warnings,omitempty"`
}

// FlowStep represents a single hop in an execution flow.
type FlowStep struct {
	StepIndex int      `json:"step_index"`
	Node      Node     `json:"node"`
	CallEdge  *Edge    `json:"call_edge,omitempty"`
	Depth     int      `json:"depth"`
	Branches  []string `json:"branches,omitempty"` // IDs of alternative branch calls
}

// ExecutionFlow represents an end-to-end request-to-response pathway.
type ExecutionFlow struct {
	EntryPoint   Node       `json:"entry_point"`
	Steps        []FlowStep `json:"steps"`
	TotalSteps   int        `json:"total_steps"`
	MaxDepth     int        `json:"max_depth"`
	BranchPoints int        `json:"branch_points"`
	RelatedTests []Node     `json:"related_tests,omitempty"`
}

// ImpactReport represents the blast-radius risk graph for a symbol change.
type ImpactReport struct {
	TargetNode    Node     `json:"target_node"`
	RiskLevel     string   `json:"risk_level"` // "HIGH", "MEDIUM", "LOW"
	DirectCallers []Node   `json:"direct_callers"`
	Transitive    []Node   `json:"transitive_callers"`
	ImpactedFiles []string `json:"impacted_files"`
	TestsToRun    []Node   `json:"tests_to_run"`
}
