// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package app

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/logging"
)

func TestPrintHelpDoesNotRequireATerminal(t *testing.T) {
	var out bytes.Buffer
	if err := printHelp(&out); err != nil {
		t.Fatalf("printHelp: %v", err)
	}
	for _, want := range []string{
		"Usage: gate-inbox [command]",
		"Run the interactive manager when no command is given.",
		"-h, --help",
		"-v, --version",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("help text does not contain %q:\n%s", want, out.String())
		}
	}
}

type failingHelpWriter struct{}

func (failingHelpWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestPrintHelpReturnsWriteError(t *testing.T) {
	if err := printHelp(failingHelpWriter{}); err == nil {
		t.Fatal("printHelp succeeded after the writer failed")
	}
}

// Startup is the only place the alternate-scroll reset goes out, and it
// cannot be exercised headlessly: runBoard() takes over the terminal. Reading
// the call out of the syntax tree still fails if it is dropped, which is
// what would put wheel notches back on the session cursor (#110).
func TestStartupDisablesAlternateScroll(t *testing.T) {
	run := runFunc(t)
	found := false
	ast.Inspect(run, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "DisableAlternateScroll" {
			return true
		}
		if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "ui" {
			found = true
		}
		return true
	})
	if !found {
		t.Fatal("runBoard() never calls ui.DisableAlternateScroll")
	}
}

func TestResolveVersion(t *testing.T) {
	cases := []struct {
		label         string
		embedded      string
		moduleVersion string
		hasInfo       bool
		want          string
	}{
		{"ldflags win", "0.11.0", "v0.11.0", true, "0.11.0"},
		{"ldflags win over missing info", "0.11.0", "", false, "0.11.0"},
		{"go install at a tag", devVersion, "v0.11.0", true, "0.11.0"},
		{"pseudo-version", devVersion, "v0.10.6-0.20260730153639-3b5b8a9a5649", true, devVersion},
		{"go run", devVersion, "(devel)", true, devVersion},
		{"empty module version", devVersion, "", true, devVersion},
		{"no build info", devVersion, "", false, devVersion},
		{"two components", devVersion, "v0.11", true, devVersion},
		{"non-numeric", devVersion, "vX.Y.Z", true, devVersion},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			var info *debug.BuildInfo
			if tc.hasInfo {
				info = &debug.BuildInfo{Main: debug.Module{Version: tc.moduleVersion}}
			}
			if got := resolveVersion(tc.embedded, info, tc.hasInfo); got != tc.want {
				t.Fatalf("resolveVersion(%q, %q) = %q, want %q", tc.embedded, tc.moduleVersion, got, tc.want)
			}
		})
	}
}

// Somebody reporting a bug has to be able to find the log without being
// told where a config directory lives.
func TestPrintLogPathNamesTheFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.HomeEnv, home)
	t.Setenv(logging.LevelEnv, "")
	t.Setenv(logging.FileEnv, "")

	var out bytes.Buffer
	if err := printLogPath(&out); err != nil {
		t.Fatalf("printLogPath: %v", err)
	}
	want := filepath.Join(home, logging.DirName, logging.FileName)
	if !strings.Contains(out.String(), want) {
		t.Fatalf("printLogPath did not name %s:\n%s", want, out.String())
	}
	for _, field := range []string{"log level:", "rotation:"} {
		if !strings.Contains(out.String(), field) {
			t.Fatalf("printLogPath is missing %q:\n%s", field, out.String())
		}
	}
}

// The environment override is what a user turns on for one run, so it has
// to beat whatever the config says.
func TestPrintLogPathHonoursTheEnvironment(t *testing.T) {
	home := t.TempDir()
	elsewhere := filepath.Join(t.TempDir(), "chasing-a-bug.log")
	t.Setenv(config.HomeEnv, home)
	t.Setenv(logging.LevelEnv, "trace")
	t.Setenv(logging.FileEnv, elsewhere)

	var out bytes.Buffer
	if err := printLogPath(&out); err != nil {
		t.Fatalf("printLogPath: %v", err)
	}
	if !strings.Contains(out.String(), elsewhere) {
		t.Fatalf("printLogPath ignored %s:\n%s", logging.FileEnv, out.String())
	}
	if !strings.Contains(out.String(), "trace") {
		t.Fatalf("printLogPath ignored %s:\n%s", logging.LevelEnv, out.String())
	}
}

// runFunc is board.go's runBoard(), the one seam the manager's exit hangs off.
func runFunc(t *testing.T) *ast.FuncDecl {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "board.go", nil, 0)
	if err != nil {
		t.Fatalf("parse board.go: %v", err)
	}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "runBoard" && fn.Recv == nil {
			return fn
		}
	}
	t.Fatal("board.go has no runBoard function")
	return nil
}

// deferredCallNames is every function a body defers, by the name at the end
// of the call: "driver.RestorePinnedWindows()" and "restoreOnHangup(driver)()"
// both answer for the call that carries the name.
func deferredCallNames(fn *ast.FuncDecl) []string {
	var names []string
	ast.Inspect(fn, func(node ast.Node) bool {
		stmt, ok := node.(*ast.DeferStmt)
		if !ok {
			return true
		}
		ast.Inspect(stmt.Call, func(inner ast.Node) bool {
			switch expr := inner.(type) {
			case *ast.SelectorExpr:
				names = append(names, expr.Sel.Name)
			case *ast.Ident:
				names = append(names, expr.Name)
			}
			return true
		})
		return true
	})
	return names
}

// The preview pins a session's tmux window to the panel's size, and on a
// shared server that window is the operator's own. Nothing but the manager's
// exit puts it back: a window left manual stays frozen at the size of a panel
// that is gone, and resizing the terminal never moves it again. runBoard() takes
// over the terminal and cannot be exercised headlessly, so the wiring is read
// out of the syntax tree the way the alternate-scroll reset above is.
func TestExitRestoresPinnedWindows(t *testing.T) {
	deferred := deferredCallNames(runFunc(t))
	for _, want := range []string{"RestorePinnedWindows", "restoreOnHangup"} {
		if !slices.Contains(deferred, want) {
			t.Fatalf("runBoard() never defers %s; it defers %v", want, deferred)
		}
	}
}

func TestUnknownCommandNamesTheVerbAndTheHelp(t *testing.T) {
	err := unknownCommand("session")
	if err == nil {
		t.Fatal("an unknown verb must be refused")
	}
	for _, want := range []string{`"session"`, "gate-inbox help"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q should mention %s", err, want)
		}
	}
	// A mistyped verb exits 2, as it did before the app package existed.
	if code := ExitCode(err); code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
}
