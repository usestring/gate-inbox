// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

func TestMainPrintsHelpWithoutStartingTUI(t *testing.T) {
	if os.Getenv("GATE_INBOX_HELP_TEST") == "1" {
		flag := os.Args[len(os.Args)-1]
		os.Args = []string{os.Args[0], flag}
		main()
		return
	}
	for _, flag := range []string{"--help", "-h"} {
		t.Run(flag, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=TestMainPrintsHelpWithoutStartingTUI", "--", flag)
			cmd.Env = append(tmuxtest.Environ(), "GATE_INBOX_HELP_TEST=1")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("gate-inbox %s: %v\n%s", flag, err, out)
			}
			if !strings.Contains(string(out), "Usage: gate-inbox [command]") {
				t.Fatalf("gate-inbox %s did not print help:\n%s", flag, out)
			}
		})
	}
}

// Without mouse reporting the terminal keeps the wheel and scrolls the
// manager out of view, which is what alternate scroll used to prevent.
// Bubble Tea v2 claims it as a view field rather than a program option, so
// this reads the view that declares it instead of the program that used to.
func TestStartupClaimsMouse(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join("internal", "ui", "view.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse view.go: %v", err)
	}
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return true
		}
		field, ok := assign.Lhs[0].(*ast.SelectorExpr)
		if !ok || field.Sel.Name != "MouseMode" {
			return true
		}
		if mode, ok := assign.Rhs[0].(*ast.SelectorExpr); ok && mode.Sel.Name == "MouseModeCellMotion" {
			found = true
		}
		return true
	})
	if !found {
		t.Fatal("the view never claims cell-motion mouse reporting")
	}
}

// The TUI owns the terminal, so a byte printed from anywhere inside it lands
// in a rendered frame. A debug print left behind is the likeliest way to
// break this program, and it is invisible in review, so the packages that
// run while the interface is up are read for one here.
//
// internal/termseq and internal/ui/altscroll.go are the deliberate
// exceptions: they write terminal control sequences rather than text, which
// is what putting a frame on the screen consists of.
func TestTUIPackagesNeverPrint(t *testing.T) {
	packages := []string{
		filepath.Join("internal", "ui"),
		filepath.Join("internal", "tmux"),
		filepath.Join("internal", "logging"),
		filepath.Join("internal", "adopt"),
		filepath.Join("internal", "status"),
		filepath.Join("internal", "termseq"),
	}
	// Both write terminal control sequences rather than text, which is what
	// putting a frame on the screen consists of. Listed rather than left out
	// of the scan, so a second writer added beside them still fails.
	allowed := map[string]bool{
		filepath.Join("internal", "ui", "altscroll.go"):    true,
		filepath.Join("internal", "termseq", "termseq.go"): true,
	}
	for _, pkg := range packages {
		entries, err := os.ReadDir(pkg)
		if err != nil {
			t.Fatalf("read %s: %v", pkg, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(pkg, name)
			if allowed[path] {
				continue
			}
			for _, found := range terminalWrites(t, path) {
				t.Errorf("%s writes to the terminal: %s", path, found)
			}
		}
	}
}

// terminalWrites names every expression in a file that puts bytes on the
// user's screen.
func terminalWrites(t *testing.T, path string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var found []string
	ast.Inspect(file, func(node ast.Node) bool {
		switch expr := node.(type) {
		case *ast.SelectorExpr:
			pkg, ok := expr.X.(*ast.Ident)
			if !ok {
				return true
			}
			if pkg.Name == "os" && (expr.Sel.Name == "Stdout" || expr.Sel.Name == "Stderr") {
				found = append(found, "os."+expr.Sel.Name)
			}
			if pkg.Name == "fmt" && strings.HasPrefix(expr.Sel.Name, "Print") {
				found = append(found, "fmt."+expr.Sel.Name)
			}
		case *ast.CallExpr:
			if fn, ok := expr.Fun.(*ast.Ident); ok && (fn.Name == "print" || fn.Name == "println") {
				found = append(found, fn.Name)
			}
		}
		return true
	})
	return found
}
