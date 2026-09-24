package tmuxtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The tests below read the module's test code rather than run it: they keep
// the isolation in isolate.go from being bypassed by the next test somebody
// writes. Each rule is one way a test reached the operator's live tmux server
// on 2026-09-23.

// socketBound are the helpers that put -L <test socket> in front of whatever
// they are given, so a kill-server passed to one of them is aimed at a test
// server. TestSocketBoundHelpersBindASocket keeps each of them honest.
var socketBound = map[string]bool{
	"tmuxOn":       true,
	"tmuxOnSocket": true,
	"tmuxCmd":      true,
	"KillServer":   true,
}

// environAllowed is where os.Environ() may appear unscrubbed in test code,
// keyed by file and enclosing function. Each is a save for restoring the
// process environment, never an environment handed to a child.
var environAllowed = map[string]bool{
	"compat/scratch_test.go:isolateEnv": true,
}

type testFile struct {
	rel  string
	file *ast.File
	fset *token.FileSet
}

// testCode parses every _test.go file in the module, plus this package's own
// helpers, which every test imports.
func testCode(t *testing.T) []testFile {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root not found at %s: %v", root, err)
	}
	var files []testFile
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		helper := filepath.Dir(rel) == filepath.Join("internal", "tmuxtest")
		if !strings.HasSuffix(path, ".go") || (!strings.HasSuffix(path, "_test.go") && !helper) {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		files = append(files, testFile{rel: filepath.ToSlash(rel), file: file, fset: fset})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 100 {
		t.Fatalf("found only %d test files: this guard would pass no matter what they did", len(files))
	}
	return files
}

func stringValue(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	return value, err == nil
}

func calleeName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}

func names(fn ast.Expr) (pkg, name string) {
	switch fn := fn.(type) {
	case *ast.Ident:
		return "", fn.Name
	case *ast.SelectorExpr:
		if x, ok := fn.X.(*ast.Ident); ok {
			return x.Name, fn.Sel.Name
		}
		return "", fn.Sel.Name
	}
	return "", ""
}

func namesServer(exprs []ast.Expr) bool {
	for _, e := range exprs {
		if v, ok := stringValue(e); ok && (v == "-L" || v == "-S") {
			return true
		}
	}
	return false
}

// TestNoKillWithoutAnExplicitSocket forbids a kill-server or kill-session
// that does not name its server. Without -L or -S tmux takes the server from
// $TMUX, which is how a test cleanup killed the operator's live server.
func TestNoKillWithoutAnExplicitSocket(t *testing.T) {
	checked := 0
	for _, f := range testCode(t) {
		seen, findings := unboundKills(f)
		checked += seen
		for _, finding := range findings {
			t.Error(finding)
		}
	}
	if checked == 0 {
		t.Fatal("no kill-server or kill-session found in test code: this guard would pass no matter what they did")
	}
}

// TestUnboundKillsAreFound plants the incident's own cleanup, and the shapes
// around it, to prove the rule above fires rather than passing on everything.
func TestUnboundKillsAreFound(t *testing.T) {
	// Spelled KILL so this literal is not itself a finding.
	const planted = `package x
func f() {
	exec.Command("tmux", "KILL-server").Run()
	exec.Command("tmux", "KILL-session", "-t", "s").Run()
	exec.Command("sh", "-c", "tmux KILL-server").Run()
	exec.Command("tmux", "-L", sock, "KILL-server").Run()
	exec.Command("tmux", "-S", path, "KILL-session", "-t", "s").Run()
	tmuxOn(sock, "KILL-server").Run()
}`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "planted_test.go", strings.ReplaceAll(planted, "KILL-", "kill-"), 0)
	if err != nil {
		t.Fatal(err)
	}
	seen, findings := unboundKills(testFile{rel: "planted_test.go", file: file, fset: fset})
	if seen != 6 || len(findings) != 3 {
		t.Fatalf("saw %d kills and flagged %d, want 6 and the first 3:\n%s", seen, len(findings), strings.Join(findings, "\n"))
	}
}

// unboundKills counts the kill-server and kill-session literals in one file
// and describes each that does not name its server.
func unboundKills(f testFile) (seen int, findings []string) {
	var stack []ast.Node
	ast.Inspect(f.file, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		value, ok := stringValue(asExpr(n))
		if !ok || (!strings.Contains(value, "kill-server") && !strings.Contains(value, "kill-session")) {
			return true
		}
		seen++
		if value != "kill-server" && value != "kill-session" {
			// A tmux command line inside one literal -- a shell script,
			// a command string -- names its server there or not at all.
			// Prose that merely mentions the command is not one.
			if strings.Contains(value, "tmux ") && !strings.Contains(value, "-L ") && !strings.Contains(value, "-S ") {
				findings = append(findings, fmt.Sprintf("%s: %q kills without -L or -S", f.fset.Position(n.Pos()), value))
			}
			return true
		}
		if !killNamesServer(stack) {
			findings = append(findings, fmt.Sprintf("%s: %s without -L/-S, or outside a helper that binds a test socket (%v)",
				f.fset.Position(n.Pos()), value, sortedKeys(socketBound)))
		}
		return true
	})
	return seen, findings
}

func asExpr(n ast.Node) ast.Expr {
	e, _ := n.(ast.Expr)
	return e
}

// killNamesServer reads the literal's nearest enclosing call or composite
// literal: -L or -S among its elements, or a call to a socket-bound helper.
// A literal that is only compared against -- a switch case, an ==, a
// strings.Contains -- runs nothing and passes.
func killNamesServer(stack []ast.Node) bool {
	switch parent := stack[len(stack)-2].(type) {
	case *ast.CallExpr:
		if pkg, _ := names(parent.Fun); pkg == "strings" || pkg == "slices" {
			return true
		}
		return namesServer(parent.Args) || socketBound[calleeName(parent)]
	case *ast.CompositeLit:
		if namesServer(parent.Elts) {
			return true
		}
		// A table row whose command sits beside the expected answer:
		// {name, []string{"-L", s, "kill-server"}, "kill-server"}.
		for _, elt := range parent.Elts {
			if row, ok := elt.(*ast.CompositeLit); ok && namesServer(row.Elts) {
				return true
			}
		}
		return false
	case *ast.CaseClause:
		return true
	case *ast.BinaryExpr:
		return parent.Op == token.EQL || parent.Op == token.NEQ
	}
	return false
}

// TestSocketBoundHelpersBindASocket keeps socketBound from being a list of
// names nobody checks: every helper on it that test code defines puts a -L or
// -S of its own in front, or hands off to another helper on the list.
func TestSocketBoundHelpersBindASocket(t *testing.T) {
	defined := 0
	for _, f := range testCode(t) {
		for _, decl := range f.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !socketBound[fn.Name.Name] || fn.Body == nil {
				continue
			}
			defined++
			binds := false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if v, ok := stringValue(asExpr(n)); ok && (v == "-L" || v == "-S") {
					binds = true
				}
				if call, ok := n.(*ast.CallExpr); ok && socketBound[calleeName(call)] && calleeName(call) != fn.Name.Name {
					binds = true
				}
				return !binds
			})
			if !binds {
				t.Errorf("%s: %s is trusted to bind a test socket but passes no -L or -S", f.fset.Position(fn.Pos()), fn.Name.Name)
			}
		}
	}
	if defined == 0 {
		t.Fatal("no socket-bound helper is defined in test code: the list above is stale")
	}
}

// TestNoUnscrubbedEnviron forbids handing a child process the test binary's
// own environment without ScrubEnv. fixtureHome and the board fixtures passed
// os.Environ() straight through, and with it whatever TMUX the run inherited.
func TestNoUnscrubbedEnviron(t *testing.T) {
	for _, f := range testCode(t) {
		for _, decl := range f.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			scrubbed := map[*ast.CallExpr]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if _, name := names(call.Fun); name == "ScrubEnv" {
					for _, arg := range call.Args {
						if inner, ok := arg.(*ast.CallExpr); ok {
							scrubbed[inner] = true
						}
					}
				}
				if pkg, name := names(call.Fun); pkg == "os" && name == "Environ" && !scrubbed[call] {
					if !environAllowed[f.rel+":"+fn.Name.Name] {
						t.Errorf("%s: os.Environ() in %s is not passed through tmuxtest.ScrubEnv; use tmuxtest.Environ()",
							f.fset.Position(call.Pos()), fn.Name.Name)
					}
				}
				return true
			})
		}
	}
}

// TestEveryChildSpawningPackageIsIsolated requires a TestMain that runs under
// tmuxtest in every package whose tests start a process or use this package.
// Importing tmuxtest isolates the binary at init; the TestMain is what tears
// down the private directory and whatever servers it holds.
func TestEveryChildSpawningPackageIsIsolated(t *testing.T) {
	spawns := map[string]bool{}
	isolated := map[string]bool{}
	for _, f := range testCode(t) {
		if !strings.HasSuffix(f.rel, "_test.go") {
			continue
		}
		dir := filepath.Dir(f.rel)
		for _, imp := range f.file.Imports {
			switch path, _ := strconv.Unquote(imp.Path.Value); path {
			case "os/exec", "github.com/usestring/gate-inbox/internal/tmuxtest":
				spawns[dir] = true
			}
		}
		for _, decl := range f.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "TestMain" || fn.Recv != nil || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					pkg, name := names(call.Fun)
					if (pkg == "tmuxtest" || (pkg == "" && dir == "internal/tmuxtest")) && (name == "Main" || name == "Run" || name == "Guard") {
						isolated[dir] = true
					}
				}
				return true
			})
		}
	}
	if len(spawns) < 10 {
		t.Fatalf("found only %d packages that spawn processes: this guard would pass no matter what they did", len(spawns))
	}
	for _, dir := range sortedKeys(spawns) {
		if !isolated[dir] {
			t.Errorf("%s: its tests start processes but its TestMain does not run under tmuxtest.Main, Run or Guard", dir)
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
