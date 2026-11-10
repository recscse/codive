package graph

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/recscse/codive/internal/db"
)

// An interface call may reach any type implementing the interface, so those
// links are possibilities rather than resolved calls.
const (
	confidenceResolved    = 1.0
	confidenceImplementer = 0.6
)

type implementerKey struct {
	iface *types.Interface
	name  string
}

func concreteTypes(pkg *types.Package) []*types.TypeName {
	var found []*types.TypeName
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		if obj, ok := scope.Lookup(name).(*types.TypeName); ok && !types.IsInterface(obj.Type()) {
			found = append(found, obj)
		}
	}
	return found
}

var goModuleRe = regexp.MustCompile(`(?m)^module\s+(\S+)`)
var versionSuffixRe = regexp.MustCompile(`^v[0-9]+$`)
var sqlStatementRe = regexp.MustCompile(`^\s*(?:SELECT|INSERT|UPDATE|DELETE|CREATE|ALTER|DROP|REPLACE|WITH)\s`)

// routeVerbs maps router registration calls to the HTTP method they imply.
// An empty method means it comes from the pattern ("GET /path") or is ANY.
var routeVerbs = map[string]string{
	"HandleFunc": "", "Handle": "",
	"GET": "GET", "POST": "POST", "PUT": "PUT", "DELETE": "DELETE", "PATCH": "PATCH",
	"Get": "GET", "Post": "POST", "Put": "PUT", "Delete": "DELETE", "Patch": "PATCH",
}

type goFile struct {
	ast      *ast.File
	path     string
	isTest   bool
	language string
}

type goPackage struct {
	importPath string
	name       string
	files      []*goFile
}

type callTarget struct {
	id         string
	evidence   EvidenceType
	confidence float64
}

// goAnalyzer type-checks the repo's Go packages from source. The standard
// library is loaded for real when a Go toolchain is available, so repo types
// passing through it (sync.OnceValue) survive. Other packages outside the module
// are stubbed as empty, so checking needs no dependencies; values of their types
// stay invalid and produce no edges rather than wrong ones.
type goAnalyzer struct {
	fset        *token.FileSet
	module      string
	nodeMap     map[string]bool
	pkgs        []*goPackage
	importable  map[string]*goPackage
	checked     map[string]*types.Package
	checkedInfo map[string]*types.Info
	implCache   map[implementerKey][]callTarget
	concrete    []*types.TypeName
	relPaths    map[string]string
	nodes       []Node
	edges       []Edge
}

// analyzeGo returns the endpoint nodes and the call and route edges of the repo's Go code.
func analyzeGo(rootDir string, allFiles map[string]db.FileRecord, nodeMap map[string]bool) ([]Node, []Edge) {
	a := &goAnalyzer{
		fset:        token.NewFileSet(),
		nodeMap:     nodeMap,
		importable:  make(map[string]*goPackage),
		checked:     make(map[string]*types.Package),
		checkedInfo: make(map[string]*types.Info),
		implCache:   make(map[implementerKey][]callTarget),
		relPaths:    make(map[string]string),
	}
	if content, err := os.ReadFile(filepath.Join(rootDir, "go.mod")); err == nil {
		if m := goModuleRe.FindSubmatch(content); m != nil {
			a.module = string(m[1])
		}
	}
	a.parseFiles(rootDir, allFiles)
	for importPath := range a.importable {
		_, _ = a.Import(importPath)
	}

	for _, pkg := range a.pkgs {
		info := a.packageInfo(pkg)
		for _, f := range pkg.files {
			a.extractCalls(f, info)
			if !f.isTest && !isExamplePath(f.path) {
				a.extractRoutes(f, info)
				a.extractCLICommands(f, info, rootDir)
				a.extractTables(f, info)
			}
		}
	}
	return a.nodes, a.edges
}

// packageInfo returns type information covering all of pkg's files. A package
// without tests was already fully checked if imported, so it is not checked twice.
func (a *goAnalyzer) packageInfo(pkg *goPackage) *types.Info {
	files := make([]*ast.File, 0, len(pkg.files))
	hasTests := false
	for _, f := range pkg.files {
		files = append(files, f.ast)
		hasTests = hasTests || f.isTest
	}
	if !hasTests && a.importable[pkg.importPath] == pkg {
		if _, err := a.Import(pkg.importPath); err == nil {
			if info, ok := a.checkedInfo[pkg.importPath]; ok {
				return info
			}
		}
	}
	info := newTypesInfo()
	a.typeCheck(pkg.importPath, files, info)
	return info
}

func newTypesInfo() *types.Info {
	return &types.Info{Uses: make(map[*ast.Ident]types.Object), Defs: make(map[*ast.Ident]types.Object)}
}

func (a *goAnalyzer) parseFiles(rootDir string, allFiles map[string]db.FileRecord) {
	paths := make([]string, 0, len(allFiles))
	for p, rec := range allFiles {
		if rec.Language == "Go" {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)

	byKey := make(map[string]*goPackage)
	for _, p := range paths {
		absPath := filepath.Join(rootDir, p)
		parsed, err := parser.ParseFile(a.fset, absPath, nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		cleanPath := filepath.ToSlash(p)
		a.relPaths[absPath] = cleanPath

		dir := path.Dir(cleanPath)
		importPath := a.module
		if dir != "." {
			importPath = path.Join(a.module, dir)
		}
		key := dir + "\x00" + parsed.Name.Name
		pkg := byKey[key]
		if pkg == nil {
			pkg = &goPackage{importPath: importPath, name: parsed.Name.Name}
			byKey[key] = pkg
			a.pkgs = append(a.pkgs, pkg)
		}
		pkg.files = append(pkg.files, &goFile{ast: parsed, path: cleanPath, isTest: strings.HasSuffix(cleanPath, "_test.go")})

		if pkg.name != "main" && !strings.HasSuffix(pkg.name, "_test") {
			a.importable[importPath] = pkg
		}
	}
}

func symbolID(cleanPath, name string, line int) string {
	return fmt.Sprintf("sym:%s:%s:%d", cleanPath, name, line)
}

func (a *goAnalyzer) typeCheck(importPath string, files []*ast.File, info *types.Info) *types.Package {
	conf := types.Config{Importer: a, FakeImportC: true, Error: func(error) {}}
	pkg, _ := conf.Check(importPath, a.fset, files, info)
	return pkg
}

// Import implements types.Importer. Non-test files of in-module packages are
// checked from source; anything else, including import cycles, gets a stub.
func (a *goAnalyzer) Import(importPath string) (*types.Package, error) {
	if importPath == "unsafe" {
		return types.Unsafe, nil
	}
	if pkg, ok := a.checked[importPath]; ok {
		return pkg, nil
	}
	if isStdlibPath(importPath) {
		if pkg, err := importStdlib(importPath); err == nil {
			a.checked[importPath] = pkg
			return pkg, nil
		}
	}
	stub := types.NewPackage(importPath, path.Base(importPath))
	stub.MarkComplete()
	a.checked[importPath] = stub

	src, ok := a.importable[importPath]
	if !ok {
		return stub, nil
	}
	var files []*ast.File
	for _, f := range src.files {
		if !f.isTest {
			files = append(files, f.ast)
		}
	}
	info := newTypesInfo()
	if pkg := a.typeCheck(importPath, files, info); pkg != nil {
		a.checked[importPath] = pkg
		a.checkedInfo[importPath] = info
		return pkg, nil
	}
	return stub, nil
}

var (
	stdlibOnce     sync.Once
	stdlibMu       sync.Mutex
	stdlibExports  map[string]string
	stdlibImporter types.Importer
)

// importStdlib loads standard library types from the export data the Go
// toolchain keeps in its build cache, which is far faster than checking GOROOT
// from source. Without a usable toolchain it fails and callers fall back to a
// stub. It has its own FileSet, so its positions must not be looked up in an
// analyzer's; resolve stops at the standard library before doing so.
func importStdlib(importPath string) (pkg *types.Package, err error) {
	// The importer panics on export data newer than this binary understands.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("cannot read export data for %s: %v", importPath, r)
		}
	}()
	stdlibOnce.Do(func() {
		cmd := exec.Command("go", "list", "-export", "-f", "{{.ImportPath}}={{.Export}}", "std")
		// Run outside the repo with the local toolchain: a repo's go.mod must not
		// choose, or trigger a download of, the toolchain codive executes.
		cmd.Dir = os.TempDir()
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOTOOLCHAIN=local", "GOFLAGS=")
		out, err := cmd.Output()
		if err != nil {
			return
		}
		stdlibExports = make(map[string]string)
		for _, line := range strings.Split(string(out), "\n") {
			if pkg, file, ok := strings.Cut(strings.TrimSpace(line), "="); ok && file != "" {
				stdlibExports[pkg] = file
			}
		}
		stdlibImporter = importer.ForCompiler(token.NewFileSet(), "gc", func(p string) (io.ReadCloser, error) {
			file, ok := stdlibExports[p]
			if !ok {
				return nil, fmt.Errorf("no export data for %s", p)
			}
			return os.Open(file)
		})
	})
	if stdlibImporter == nil {
		return nil, fmt.Errorf("go toolchain unavailable")
	}
	stdlibMu.Lock()
	defer stdlibMu.Unlock()
	return stdlibImporter.Import(importPath)
}

// StdlibAvailable reports whether standard library types can be loaded, which
// needs a working Go toolchain.
func StdlibAvailable() bool {
	_, err := importStdlib("fmt")
	return err == nil
}

// isStdlibPath reports import paths whose first element has no dot, the rule
// the go command uses to tell the standard library from downloaded modules.
func isStdlibPath(importPath string) bool {
	first, _, _ := strings.Cut(importPath, "/")
	return !strings.Contains(first, ".") && first != "C"
}

// resolve maps a called or referenced expression to the repo symbols it may denote.
func (a *goAnalyzer) resolve(expr ast.Expr, info *types.Info) []callTarget {
	var ident *ast.Ident
	switch e := expr.(type) {
	case *ast.Ident:
		ident = e
	case *ast.SelectorExpr:
		ident = e.Sel
	case *ast.ParenExpr:
		return a.resolve(e.X, info)
	case *ast.IndexExpr:
		return a.resolve(e.X, info)
	case *ast.IndexListExpr:
		return a.resolve(e.X, info)
	default:
		return nil
	}

	fn, ok := info.Uses[ident].(*types.Func)
	if !ok || fn.Pkg() == nil {
		return nil
	}
	if isStdlibPath(fn.Pkg().Path()) {
		return nil
	}
	fn = fn.Origin()
	pos := a.fset.Position(fn.Pos())
	cleanPath, inRepo := a.relPaths[pos.Filename]
	if !inRepo {
		return nil
	}
	if id := symbolID(cleanPath, fn.Name(), pos.Line); a.nodeMap[id] {
		return []callTarget{{id: id, evidence: EvidenceDirect, confidence: confidenceResolved}}
	}

	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Recv() == nil || !types.IsInterface(sig.Recv().Type()) {
		return nil
	}
	iface, ok := sig.Recv().Type().Underlying().(*types.Interface)
	if !ok {
		return nil
	}
	return a.implementers(iface, fn.Name(), fn.Pkg())
}

// implementers lists the repo methods an interface call may reach: name on
// every repo type that satisfies the interface. They are possibilities, not
// proof, so they are marked inferred.
func (a *goAnalyzer) implementers(iface *types.Interface, name string, ifacePkg *types.Package) []callTarget {
	key := implementerKey{iface, name}
	if cached, ok := a.implCache[key]; ok {
		return cached
	}
	if a.concrete == nil {
		for importPath := range a.importable {
			if pkg, ok := a.checked[importPath]; ok {
				a.concrete = append(a.concrete, concreteTypes(pkg)...)
			}
		}
	}
	// A package checked with its tests has its own copies of its types, which
	// only match an interface from that same check.
	candidates := a.concrete
	if a.checked[ifacePkg.Path()] != ifacePkg {
		candidates = append(concreteTypes(ifacePkg), candidates...)
	}

	var targets []callTarget
	seen := make(map[string]bool)
	for _, obj := range candidates {
		ptr := types.NewPointer(obj.Type())
		if !types.Implements(obj.Type(), iface) && !types.Implements(ptr, iface) {
			continue
		}
		method, _, _ := types.LookupFieldOrMethod(ptr, true, obj.Pkg(), name)
		m, isFunc := method.(*types.Func)
		if !isFunc {
			continue
		}
		pos := a.fset.Position(m.Pos())
		cleanPath, inRepo := a.relPaths[pos.Filename]
		if !inRepo {
			continue
		}
		id := symbolID(cleanPath, m.Name(), pos.Line)
		if a.nodeMap[id] && !seen[id] {
			seen[id] = true
			targets = append(targets, callTarget{id: id, evidence: EvidenceInferred, confidence: confidenceImplementer})
		}
	}
	a.implCache[key] = targets
	return targets
}

func (a *goAnalyzer) extractCalls(f *goFile, info *types.Info) {
	for _, decl := range f.ast.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		callerID := symbolID(f.path, fn.Name.Name, a.fset.Position(fn.Name.Pos()).Line)
		if !a.nodeMap[callerID] {
			continue
		}
		called := make(map[*ast.Ident]bool)
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				if ident := calledIdent(node.Fun); ident != nil {
					called[ident] = true
				}
				a.linkTargets(callerID, EdgeCalls, f.path, node.Pos(), a.resolve(node.Fun, info))
			case *ast.Ident:
				// A function used without being called, such as a handler passed as a value.
				if !called[node] {
					var exact []callTarget
					for _, t := range a.resolve(node, info) {
						if t.evidence == EvidenceDirect {
							exact = append(exact, t)
						}
					}
					a.linkTargets(callerID, EdgeReferences, f.path, node.Pos(), exact)
				}
			}
			return true
		})
	}
}

func calledIdent(expr ast.Expr) *ast.Ident {
	switch e := expr.(type) {
	case *ast.Ident:
		return e
	case *ast.SelectorExpr:
		return e.Sel
	case *ast.ParenExpr:
		return calledIdent(e.X)
	case *ast.IndexExpr:
		return calledIdent(e.X)
	case *ast.IndexListExpr:
		return calledIdent(e.X)
	}
	return nil
}

func (a *goAnalyzer) linkTargets(sourceID string, kind EdgeKind, cleanPath string, pos token.Pos, targets []callTarget) {
	for _, t := range targets {
		if t.id == sourceID {
			continue
		}
		a.edges = append(a.edges, Edge{
			SourceID:     sourceID,
			TargetID:     t.id,
			Kind:         kind,
			Evidence:     t.evidence,
			LocationFile: cleanPath,
			LocationLine: a.fset.Position(pos).Line,
			Confidence:   t.confidence,
		})
	}
}

func (a *goAnalyzer) extractRoutes(f *goFile, info *types.Info) {
	a.walkRoutes(f.ast, "", f, info, make(map[types.Object]string))
}

// walkRoutes registers routes under their group prefixes. It handles closure
// groups (r.Route("/a", func...), m.Group("/a", func...)) and router variables
// (g := r.Group("/a")). Sub-routers mounted or built in other functions keep
// only the prefixes visible here.
func (a *goAnalyzer) walkRoutes(root ast.Node, prefix string, f *goFile, info *types.Info, vars map[types.Object]string) {
	ast.Inspect(root, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			if len(node.Lhs) != len(node.Rhs) {
				return true
			}
			for i, rhs := range node.Rhs {
				ident, isIdent := node.Lhs[i].(*ast.Ident)
				if !isIdent {
					continue
				}
				if groupPrefix, ok := receiverPrefix(rhs, info, vars); ok {
					obj := info.Defs[ident]
					if obj == nil {
						obj = info.Uses[ident]
					}
					if obj != nil {
						vars[obj] = joinRoute(prefix, groupPrefix)
					}
				}
			}
			return true
		case *ast.CallExpr:
			sel, ok := node.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if sel.Sel.Name == "Group" || sel.Sel.Name == "Route" {
				var closure *ast.FuncLit
				for _, arg := range node.Args {
					if lit, isLit := arg.(*ast.FuncLit); isLit {
						closure = lit
						break
					}
				}
				if closure != nil {
					child := a.routeContext(sel.X, prefix, info, vars)
					if len(node.Args) > 0 {
						if lit, isString := stringLiteral(node.Args[0]); isString {
							child = joinRoute(child, lit)
						}
					}
					a.walkRoutes(closure.Body, child, f, info, vars)
					return false
				}
				return true
			}
			a.registerRoute(node, sel, a.routeContext(sel.X, prefix, info, vars), f, info)
		}
		return true
	})
}

// routeContext is the prefix a receiver routes under: its own if it is a known
// group, which already includes where it was created, else the enclosing one.
func (a *goAnalyzer) routeContext(receiver ast.Expr, prefix string, info *types.Info, vars map[types.Object]string) string {
	if p, ok := receiverPrefix(receiver, info, vars); ok {
		if _, isCall := receiver.(*ast.CallExpr); isCall {
			return joinRoute(prefix, p)
		}
		return p
	}
	return prefix
}

func receiverPrefix(expr ast.Expr, info *types.Info, vars map[types.Object]string) (string, bool) {
	switch e := expr.(type) {
	case *ast.Ident:
		p, ok := vars[info.Uses[e]]
		return p, ok
	case *ast.CallExpr:
		sel, ok := e.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Group" || len(e.Args) == 0 {
			return "", false
		}
		lit, isString := stringLiteral(e.Args[0])
		if !isString {
			return "", false
		}
		parent, _ := receiverPrefix(sel.X, info, vars)
		return joinRoute(parent, lit), true
	}
	return "", false
}

func joinRoute(prefix, route string) string {
	if route == "" {
		return prefix
	}
	if !strings.HasPrefix(route, "/") {
		route = "/" + route
	}
	return strings.TrimSuffix(prefix, "/") + route
}

func (a *goAnalyzer) registerRoute(call *ast.CallExpr, sel *ast.SelectorExpr, prefix string, f *goFile, info *types.Info) {
	method, isVerb := routeVerbs[sel.Sel.Name]
	if !isVerb || len(call.Args) < 2 {
		return
	}
	routePath, ok := stringLiteral(call.Args[0])
	if !ok {
		return
	}
	// Go 1.22 mux patterns carry their method: "GET /path".
	if verb, rest, found := strings.Cut(routePath, " "); found && method == "" {
		method, routePath = verb, strings.TrimSpace(rest)
	}
	if !strings.HasPrefix(routePath, "/") && !(routePath == "" && prefix != "") {
		return
	}
	routePath = joinRoute(prefix, routePath)
	if method == "" {
		method = "ANY"
	}

	endpointID := fmt.Sprintf("endpoint:%s:%s", method, routePath)
	if !a.nodeMap[endpointID] {
		a.nodeMap[endpointID] = true
		a.nodes = append(a.nodes, Node{
			ID:         endpointID,
			Kind:       KindEndpoint,
			Label:      fmt.Sprintf("%s %s", method, routePath),
			FilePath:   f.path,
			LineNumber: a.fset.Position(call.Pos()).Line,
			Language:   "Go",
			Metadata:   map[string]string{"method": method, "path": routePath},
		})
	}
	a.linkTargets(endpointID, EdgeRoutesTo, f.path, call.Pos(), a.handlerTargets(call.Args[len(call.Args)-1], info))
}

// handlerTargets finds the code behind a route handler argument: a function
// reference, a handler factory call, a conversion such as http.HandlerFunc(h),
// or the calls made inside an inline closure.
func (a *goAnalyzer) handlerTargets(expr ast.Expr, info *types.Info) []callTarget {
	switch e := expr.(type) {
	case *ast.CallExpr:
		if targets := a.resolve(e.Fun, info); len(targets) > 0 {
			return targets
		}
		var targets []callTarget
		for _, arg := range e.Args {
			targets = append(targets, a.handlerTargets(arg, info)...)
		}
		return targets
	case *ast.FuncLit:
		return a.callsWithin(e.Body, info)
	}
	return a.resolve(expr, info)
}

func (a *goAnalyzer) callsWithin(body ast.Node, info *types.Info) []callTarget {
	var targets []callTarget
	ast.Inspect(body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			targets = append(targets, a.resolve(call.Fun, info)...)
		}
		return true
	})
	return targets
}

// extractCLICommands treats the outermost string switch cases of a main.go as
// subcommands, routed to the repo functions their case bodies call. Switches
// nested inside a case are that command's own arguments, so they are not walked.
func (a *goAnalyzer) extractCLICommands(f *goFile, info *types.Info, rootDir string) {
	binary := cliBinaryName(f.path, a.module, rootDir)
	if binary == "" || f.ast.Name.Name != "main" {
		return
	}
	ast.Inspect(f.ast, func(n ast.Node) bool {
		sw, ok := n.(*ast.SwitchStmt)
		if !ok {
			return true
		}
		for _, stmt := range sw.Body.List {
			clause, ok := stmt.(*ast.CaseClause)
			if !ok {
				continue
			}
			var targets []callTarget
			for _, bodyStmt := range clause.Body {
				targets = append(targets, a.callsWithin(bodyStmt, info)...)
			}
			for _, value := range clause.List {
				name, isString := stringLiteral(value)
				if !isString || name == "" || strings.HasPrefix(name, "-") || name == "help" || name == "version" {
					continue
				}
				id := fmt.Sprintf("endpoint:CLI:%s %s", binary, name)
				if !a.nodeMap[id] {
					a.nodeMap[id] = true
					a.nodes = append(a.nodes, Node{
						ID:         id,
						Kind:       KindEndpoint,
						Label:      fmt.Sprintf("CLI: %s %s", binary, name),
						FilePath:   f.path,
						LineNumber: a.fset.Position(value.Pos()).Line,
						Language:   "Go",
						Metadata:   map[string]string{"type": "CLI Entry Point", "command": name},
					})
				}
				a.linkTargets(id, EdgeRoutesTo, f.path, value.Pos(), targets)
			}
		}
		return false
	})
}

// cliBinaryName names the program a main.go builds, or "" if it isn't a
// conventional entry point (the repo root, or cmd/<name>/).
func cliBinaryName(filePath, module, rootDir string) string {
	switch {
	case filePath == "main.go":
		name := path.Base(module)
		if versionSuffixRe.MatchString(name) {
			name = path.Base(path.Dir(module))
		}
		if module == "" {
			name = filepath.Base(rootDir)
		}
		return name
	case strings.HasPrefix(filePath, "cmd/") && strings.HasSuffix(filePath, "/main.go"):
		if parts := strings.Split(filePath, "/"); len(parts) == 3 {
			return parts[1]
		}
	}
	return ""
}

// extractTables reads SQL from string literals that start with a SQL verb, and
// links each table to the enclosing function.
func (a *goAnalyzer) extractTables(f *goFile, info *types.Info) {
	for _, decl := range f.ast.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		callerID := symbolID(f.path, fn.Name.Name, a.fset.Position(fn.Name.Pos()).Line)
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sql, isString := stringLiteral(asExpr(n))
			if !isString || !sqlStatementRe.MatchString(sql) {
				return true
			}
			line := a.fset.Position(n.Pos()).Line
			for _, access := range tableAccesses {
				for _, m := range access.re.FindAllStringSubmatch(sql, -1) {
					table := strings.ToLower(m[1])
					if !isUserTable(table) {
						continue
					}
					a.nodes, a.edges = addTable(a.nodes, a.edges, a.nodeMap, table, f.path, line, callerID, access.kind)
				}
			}
			return true
		})
	}
}

func asExpr(n ast.Node) ast.Expr {
	expr, _ := n.(ast.Expr)
	return expr
}

func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	return value, err == nil
}
