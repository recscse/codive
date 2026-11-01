package graph

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/recscse/codive/internal/db"
)

var fixtureFuncRe = regexp.MustCompile(`^func (?:\([^)]*\) )?(\w+)`)

var fixtureFiles = map[string]string{
	"go.mod": "module example.com/app\n\ngo 1.22\n",
	"main.go": `package main

import (
	"os"

	"example.com/app/store"
)

func main() {
	switch os.Args[1] {
	case "--json":
		return
	case "sync", "pull":
		runSync()
	case "nested":
		switch os.Args[2] {
		case "inner":
		}
	}
}

func runSync() { store.Save() }
`,
	"store/store.go": `package store

import (
	"database/sql"
	"net/http"
	"sync"
)

// Example from a comment: mux.HandleFunc("/commented", Handler)

type Conn struct{}

func (c *Conn) Close() {}

type Writer interface{ Flush() }

type Buf struct{}

func (b *Buf) Flush() {}

func Save() {
	const q = "INSERT INTO orders VALUES (1)"
	_ = q
	c := &Conn{}
	c.Close()
}

func Load(rows *sql.Rows, w Writer) {
	rows.Close()
	w.Flush()
}

func Handler(w http.ResponseWriter, r *http.Request) { Save() }

func Factory() http.HandlerFunc { return Handler }

func Routes(mux *http.ServeMux, client *http.Client) {
	mux.HandleFunc("/items", Handler)
	mux.HandleFunc("GET /made", Factory())
	client.Get("https://example.com/not-a-route")
}

func NoBody()

var conn = sync.OnceValue(func() *Conn { return &Conn{} })

func Lazy() { conn().Close() }
`,
	"store/store_test.go": `package store

import "testing"

func TestSave(t *testing.T) { Save() }

func TestRoutesFixture(t *testing.T) {
	const fixture = "POST /fake"
	_ = fixture
}
`,
	"web/web.go": `package web

type M struct{}

func (m *M) Group(p string, fns ...func()) *M { return m }
func (m *M) Get(p string, h func())           {}

func h() {}

func Reg(m *M) {
	m.Group("/a", func() {
		m.Get("/b", h)
		m.Group("/c", func() {
			m.Get("", h)
		})
	})
	g := m.Group("/v1")
	g.Get("/x", h)
	n := g.Group("/n")
	n.Get("/z", h)
	m.Group("/v2").Get("/y", h)
}

func Queries(run func(string)) {
	run("SELECT * FROM sqlite_master")
	run("SELECT id FROM accounts")
	run("cannot read FROM parent")
}
`,
	"cmd/tool/main.go": `package main

func main() {
	switch "x" {
	case "serve":
		run()
	}
}

func run() {}
`,
	"_examples/demo/main.go": `package main

import "net/http"

func main() {
	switch "x" {
	case "demo":
	}
	http.HandleFunc("/demo", nil)
}
`,
	"internal/deep/deep.go": "package deep\n\nfunc Deep() {}\n",
	"README.md":             "Call `app.post('/from-docs', x)` and SELECT * FROM prose.\n",
}

func buildFixture(t *testing.T) (*sql.DB, string, *GraphSnapshot) {
	t.Helper()
	root := t.TempDir()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	var changes db.IndexChanges
	for rel, content := range fixtureFiles {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		lang := "Markdown"
		if strings.HasSuffix(rel, ".go") {
			lang = "Go"
		}
		changes.Files = append(changes.Files, db.FileRecord{Path: rel, Language: lang})
		if lang != "Go" {
			continue
		}
		for i, line := range strings.Split(content, "\n") {
			if m := fixtureFuncRe.FindStringSubmatch(line); m != nil {
				kind := "function"
				if strings.HasPrefix(line, "func (") {
					kind = "method"
				}
				changes.Symbols = append(changes.Symbols, db.SymbolRecord{FilePath: rel, Name: m[1], Kind: kind, LineNumber: i + 1})
			}
		}
	}
	ctx := context.Background()
	if err := db.ApplyIndexChanges(ctx, database, changes); err != nil {
		t.Fatal(err)
	}
	snapshot, err := BuildGraph(ctx, database, root)
	if err != nil {
		t.Fatal(err)
	}
	return database, root, snapshot
}

func findEdge(s *GraphSnapshot, kind EdgeKind, source, target string) *Edge {
	for i, e := range s.Edges {
		if e.Kind == kind && strings.Contains(e.SourceID, source) && strings.Contains(e.TargetID, target) {
			return &s.Edges[i]
		}
	}
	return nil
}

func TestCallsResolveByTypeNotName(t *testing.T) {
	_, _, s := buildFixture(t)

	e := findEdge(s, EdgeCalls, ":Save:", ":Close:")
	if e == nil || e.Confidence != confidenceResolved || e.Evidence != EvidenceDirect {
		t.Errorf("c.Close() on a repo type should be a resolved edge, got %+v", e)
	}
	if e := findEdge(s, EdgeCalls, ":Load:", ":Close:"); e != nil {
		t.Errorf("rows.Close() on *sql.Rows must not link to the repo's Close: %+v", e)
	}
	e = findEdge(s, EdgeCalls, ":Load:", ":Flush:")
	if e == nil || e.Evidence != EvidenceInferred || e.Confidence != confidenceImplementer {
		t.Errorf("interface call should be an inferred edge, got %+v", e)
	}
	if findEdge(s, EdgeCalls, ":runSync:", "store/store.go:Save:") == nil {
		t.Errorf("cross-package call runSync -> store.Save is missing")
	}
	if findEdge(s, EdgeCalls, "TestSave", ":Save:") == nil {
		t.Errorf("call from test file is missing")
	}
}

func TestCLIBinaryName(t *testing.T) {
	cases := []struct{ file, module, want string }{
		{"main.go", "github.com/go-chi/chi/v5", "chi"},
		{"main.go", "github.com/recscse/codive", "codive"},
		{"cmd/gitea/main.go", "code.gitea.io/gitea", "gitea"},
		{"tools/gen/main.go", "example.com/app", ""},
	}
	for _, c := range cases {
		if got := cliBinaryName(c.file, c.module, "/repo"); got != c.want {
			t.Errorf("cliBinaryName(%q, %q) = %q, want %q", c.file, c.module, got, c.want)
		}
	}
}

func TestPackageIDsUseForwardSlashes(t *testing.T) {
	_, _, s := buildFixture(t)
	for _, n := range s.Nodes {
		if n.ID == "pkg:internal/deep" {
			return
		}
	}
	t.Errorf("nested package should have ID pkg:internal/deep on every OS")
}

func TestStdlibTypesAndFunctionValues(t *testing.T) {
	_, _, s := buildFixture(t)

	if findEdge(s, EdgeCalls, ":Lazy:", ":Close:") == nil {
		t.Errorf("a repo type returned through sync.OnceValue should still resolve")
	}
	if findEdge(s, EdgeReferences, ":Routes:", ":Handler:") == nil {
		t.Errorf("a handler passed as a value should be a reference")
	}
	if e := findEdge(s, EdgeReferences, ":Save:", ":Close:"); e != nil {
		t.Errorf("a direct call must not also be a reference: %+v", e)
	}
	if e := findEdge(s, EdgeCalls, ":Load:", "Lazy"); e != nil {
		t.Errorf("unexpected edge: %+v", e)
	}
}

func TestAnalyzeBlast(t *testing.T) {
	database, root, _ := buildFixture(t)
	ctx := context.Background()

	result, err := AnalyzeBlast(ctx, database, root, "Save")
	if err != nil || result == nil {
		t.Fatalf("AnalyzeBlast(Save) = %v, %v", result, err)
	}
	if result.TotalDefinitions != 1 || !containsNode(result.Definitions[0].TestsToRun, "sym:store/store_test.go:TestSave:5") {
		t.Errorf("unexpected result: %+v", result.Definitions[0])
	}
	text := result.Format()
	for _, want := range []string{"type-checked", "runSync", "TestSave", "known callers"} {
		if !strings.Contains(text, want) {
			t.Errorf("Format() missing %q:\n%s", want, text)
		}
	}

	for _, symbol := range []string{"store/store.go:Save", "store.go:Save", "store.Save"} {
		if r, err := AnalyzeBlast(ctx, database, root, symbol); err != nil || r == nil || r.TotalDefinitions != 1 {
			t.Errorf("AnalyzeBlast(%q) = %+v, %v", symbol, r, err)
		}
	}
	if r, err := AnalyzeBlast(ctx, database, root, "other.go:Save"); err != nil || r != nil {
		t.Errorf("wrong file should not match: %+v, %v", r, err)
	}
	if r, err := AnalyzeBlast(ctx, database, root, "NoSuchSymbol"); err != nil || r != nil {
		t.Errorf("unknown symbol should return nil: %+v, %v", r, err)
	}
}

func TestEndpointDetection(t *testing.T) {
	_, _, s := buildFixture(t)

	got := make(map[string]bool)
	for _, n := range s.Nodes {
		if n.Kind == KindEndpoint {
			got[n.Label] = true
		}
	}
	want := []string{"ANY /items", "GET /made", "CLI: app sync", "CLI: app pull", "CLI: app nested", "CLI: tool serve",
		"GET /a/b", "GET /a/c", "GET /v1/x", "GET /v1/n/z", "GET /v2/y"}
	for _, label := range want {
		if !got[label] {
			t.Errorf("missing endpoint %q; got %v", label, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("expected exactly %v, got %v", want, got)
	}

	if findEdge(s, EdgeRoutesTo, "endpoint:ANY:/items", ":Handler:") == nil {
		t.Errorf("route should link to its handler")
	}
	if findEdge(s, EdgeRoutesTo, "endpoint:GET:/made", ":Factory:") == nil {
		t.Errorf("route should link to its handler factory")
	}
	if findEdge(s, EdgeRoutesTo, "CLI:app pull", ":runSync:") == nil {
		t.Errorf("CLI command should link to the function its case calls")
	}
}

func TestTablesDetectedOutsideRouteLines(t *testing.T) {
	_, _, s := buildFixture(t)

	if findEdge(s, EdgeWritesTo, ":Save:", "db:table:orders") == nil {
		t.Errorf("Save should write to table orders")
	}
	if findEdge(s, EdgeReadsFrom, ":Queries:", "db:table:accounts") == nil {
		t.Errorf("Queries should read table accounts")
	}
	for _, n := range s.Nodes {
		switch n.ID {
		case "db:table:prose", "db:table:parent", "db:table:sqlite_master":
			t.Errorf("%s is not a user table", n.ID)
		}
	}
}

func TestFlowAndImpactUseResolvedEdges(t *testing.T) {
	database, _, s := buildFixture(t)
	ctx := context.Background()

	flow, err := TraverseExecutionFlow(ctx, database, "endpoint:CLI:app sync", 6)
	if err != nil {
		t.Fatal(err)
	}
	reached := make(map[string]bool)
	for _, step := range flow.Steps {
		reached[step.Node.Label] = true
	}
	for _, label := range []string{"runSync", "Save", "Close", "Table: orders"} {
		if !reached[label] {
			t.Errorf("flow should reach %q; got %v", label, reached)
		}
	}

	var saveID string
	for _, n := range s.Nodes {
		if n.Label == "Save" {
			saveID = n.ID
		}
	}
	impact, err := AnalyzeImpact(ctx, database, saveID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range append(impact.DirectCallers, impact.Transitive...) {
		if isTestPath(c.FilePath) {
			t.Errorf("test %s should be in tests_to_run, not callers", c.Label)
		}
	}
	if !containsNode(impact.DirectCallers, "sym:store/store.go:Handler:33") {
		t.Errorf("Handler should be a direct caller of Save; got %+v", impact.DirectCallers)
	}
	foundTest := false
	for _, n := range impact.TestsToRun {
		foundTest = foundTest || n.Label == "TestSave"
	}
	if !foundTest {
		t.Errorf("TestSave should be in tests_to_run; got %+v", impact.TestsToRun)
	}
}

func TestEnsureFreshRebuildsOnlyAfterIndexChange(t *testing.T) {
	database, root, _ := buildFixture(t)
	ctx := context.Background()

	if _, err := database.ExecContext(ctx, "DELETE FROM graph_nodes;"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureFresh(ctx, database, root); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.QueryRowContext(ctx, "SELECT COUNT(*) FROM graph_nodes;").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("unchanged index should not rebuild, found %d nodes", count)
	}

	err := db.ApplyIndexChanges(ctx, database, db.IndexChanges{Files: []db.FileRecord{{Path: "new.go", Language: "Go"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureFresh(ctx, database, root); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, "SELECT COUNT(*) FROM graph_nodes;").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatalf("changed index should rebuild the graph")
	}
}
