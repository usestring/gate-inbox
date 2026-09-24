package compat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/app"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

var binary struct {
	once sync.Once
	dir  string
	path string
	err  error
}

func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Run(func() int {
		code := m.Run()
		if binary.dir != "" {
			os.RemoveAll(binary.dir)
		}
		return code
	}))
}

// buildBinary builds this module's executable once per run, stamped with
// a fixed version so no golden carries a VCS revision. It has to be called
// before newScratch empties the environment the go command needs.
func buildBinary(t *testing.T) string {
	t.Helper()
	binary.once.Do(func() {
		binary.dir, binary.err = os.MkdirTemp("", "compat-bin-")
		if binary.err != nil {
			return
		}
		binary.path = filepath.Join(binary.dir, app.Name)
		cmd := exec.Command("go", "build", "-ldflags", "-X main.version=compat", "-o", binary.path, "..")
		if out, err := cmd.CombinedOutput(); err != nil {
			binary.err = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if binary.err != nil {
		t.Fatal(binary.err)
	}
	return binary.path
}

type invocation struct {
	stdout, stderr string
	exit           int
	// left lists the files the command left under the board's home.
	left []string
}

// run executes the binary in a scratch world of its own, with nothing on
// stdin and an environment made only of the scratch's variables.
func run(t *testing.T, exe string, args ...string) invocation {
	t.Helper()
	s := newScratch(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = s.env()
	cmd.Dir = s.root
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	result := invocation{stdout: s.redact(stdout.String()), stderr: s.redact(stderr.String())}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		result.exit = exit.ExitCode()
	default:
		t.Fatalf("run %v: %v", args, err)
	}
	filepath.WalkDir(s.home, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && path != filepath.Join(s.home, "bin", app.Name) {
			rel, _ := filepath.Rel(s.home, path)
			result.left = append(result.left, rel)
		}
		return nil
	})
	sort.Strings(result.left)
	return result
}

// TestCLIHelp records the top-level help, which is the list of every verb
// a session's shell can reach.
func TestCLIHelp(t *testing.T) {
	exe := buildBinary(t)
	help := run(t, exe, "help")
	if help.exit != 0 || help.stderr != "" {
		t.Fatalf("help exited %d: %s", help.exit, help.stderr)
	}
	golden(t, "cli/help.golden", help.stdout)
}

var verbPattern = regexp.MustCompile(`^[a-z][a-z-]*$`)

// commandsInHelp reads every command and group verb back out of the help
// text, so a verb added to the help is covered here without editing this
// file.
func commandsInHelp(help string) [][]string {
	var commands [][]string
	for _, line := range strings.Split(help, "\n") {
		rest, ok := strings.CutPrefix(line, "  "+app.Name+" ")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) > 1 && verbPattern.MatchString(fields[1]) {
			commands = append(commands, fields[:2])
		} else {
			commands = append(commands, fields[:1])
		}
	}
	return commands
}

// TestCLIUsage records every command's and verb's -h: its usage line and
// every flag it takes, hidden ones included.
func TestCLIUsage(t *testing.T) {
	exe := buildBinary(t)
	help := run(t, exe, "help")
	commands := commandsInHelp(help.stdout)
	if len(commands) < 30 {
		t.Fatalf("read only %d commands out of the help text", len(commands))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d commands and verbs\n", len(commands))
	for _, command := range commands {
		args := append(append([]string{}, command...), "-h")
		b.WriteString(describe(args, run(t, exe, args...)))
	}
	golden(t, "cli/usage.golden", b.String())
}

// TestCLIExitCodes records the status and the words of a success, an
// unknown command and each kind of usage error, and what each leaves
// behind in the board's home.
func TestCLIExitCodes(t *testing.T) {
	exe := buildBinary(t)
	var b strings.Builder
	for _, args := range [][]string{
		{"help"},
		{"--help"},
		{"-h"},
		{"--version"},
		{"-v"},
		{"--log-path"},
		{"logs"},
		{"bogus"},
		{"Help"},
		{"task"},
		{"task", "bogus"},
		{"send"},
		{"send", "cafe0001"},
		{"sessions", "--no-such-flag"},
		{"priority", "extreme"},
		{"rename"},
		{"rename", "compat-name"},
		{"task", "list"},
		{"groups", "--json"},
		{"reservations", "--json"},
	} {
		result := run(t, exe, args...)
		if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
			// The text itself is help.golden's; this records that each
			// spelling reaches it.
			result.stdout = fmt.Sprintf("(%d bytes of help text)\n", len(result.stdout))
		}
		b.WriteString(describe(args, result))
		fmt.Fprintf(&b, "--- left in home: %s\n", strings.Join(result.left, ", "))
	}
	golden(t, "cli/exit-codes.golden", b.String())
}

func describe(args []string, result invocation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n$ %s %s\nexit %d\n", app.Name, strings.Join(args, " "), result.exit)
	if result.stdout != "" {
		b.WriteString("--- stdout\n" + result.stdout)
		if !strings.HasSuffix(result.stdout, "\n") {
			b.WriteString("\n")
		}
	}
	if result.stderr != "" {
		b.WriteString("--- stderr\n" + result.stderr)
		if !strings.HasSuffix(result.stderr, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}
