package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	for name, want := range map[string]int{
		"off": int(LevelOff), "error": int(LevelError), "warn": int(LevelWarn),
		"info": int(LevelInfo), "debug": int(LevelDebug), "trace": int(LevelTrace),
		"  TRACE  ": int(LevelTrace),
	} {
		got, err := ParseLevel(name)
		if err != nil {
			t.Fatalf("ParseLevel(%q): %v", name, err)
		}
		if int(got) != want {
			t.Fatalf("ParseLevel(%q) = %v, want %v", name, got, want)
		}
	}
	if _, err := ParseLevel("verbose"); err == nil {
		t.Fatal("ParseLevel accepted an unknown level")
	}
}

func TestResolveDefaultsToAQuietLogBesideTheStore(t *testing.T) {
	t.Setenv(LevelEnv, "")
	t.Setenv(FileEnv, "")
	home := t.TempDir()
	opts, err := Resolve(home, Settings{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := filepath.Join(home, "logs", "gate-inbox.log"); opts.Path != want {
		t.Fatalf("path = %q, want %q", opts.Path, want)
	}
	if opts.Level != LevelInfo {
		t.Fatalf("level = %v, want info", opts.Level)
	}
	if !opts.Compress {
		t.Fatal("rotated files are not compressed by default")
	}
	// Retention is derived from the cap rather than set beside it, so the
	// two can never be configured into contradicting each other.
	if worst := int64(opts.MaxSizeMB) * int64(opts.MaxBackups+1); worst > int64(opts.MaxTotalMB) {
		t.Fatalf("%d backups of %d MB exceed the %d MB cap", opts.MaxBackups, opts.MaxSizeMB, opts.MaxTotalMB)
	}
}

func TestResolvePrefersTheEnvironmentOverTheConfig(t *testing.T) {
	home := t.TempDir()
	elsewhere := filepath.Join(t.TempDir(), "somewhere.log")
	t.Setenv(LevelEnv, "trace")
	t.Setenv(FileEnv, elsewhere)
	opts, err := Resolve(home, Settings{Level: "error", File: filepath.Join(home, "configured.log")})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if opts.Level != LevelTrace {
		t.Fatalf("level = %v, want trace", opts.Level)
	}
	if opts.Path != elsewhere {
		t.Fatalf("path = %q, want %q", opts.Path, elsewhere)
	}
}

func TestResolveAcceptsADirectoryAsThePath(t *testing.T) {
	t.Setenv(LevelEnv, "")
	t.Setenv(FileEnv, "")
	home := t.TempDir()
	elsewhere := t.TempDir()
	opts, err := Resolve(home, Settings{File: elsewhere})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := filepath.Join(elsewhere, FileName); opts.Path != want {
		t.Fatalf("path = %q, want %q", opts.Path, want)
	}
}

func TestOffWritesNothingAtAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gate-inbox.log")
	logger, err := Open(Options{Path: path, Level: LevelOff})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	logger.Error("this should not exist")
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a log file appeared with logging off: %v", err)
	}
}

// Pane text is the one thing that must not be in a log somebody attaches to
// a bug report, because these panes hold live credentials.
func TestPaneTextNeverReachesTheLogByDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gate-inbox.log")
	logger, err := Open(Options{Path: path, Level: LevelInfo, MaxSizeMB: 1, MaxBackups: 1, MaxTotalMB: 4})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	restore := SetDefault(logger)
	defer SetDefault(restore)

	pane := "$ export CLAUDE_CODE_OAUTH_TOKEN=" + secretFixtures[0].secret + "\n$ make test"
	PaneText("pane capture", "gi_1", pane)
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	body := ""
	if raw, err := os.ReadFile(path); err == nil {
		body = string(raw)
	}
	if strings.Contains(body, "make test") {
		t.Fatalf("pane text reached the log at the default level:\n%s", body)
	}
	if strings.Contains(body, secretFixtures[0].secret) {
		t.Fatalf("a token reached the log:\n%s", body)
	}
}

func TestPaneTextAtTraceIsStillScrubbed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gate-inbox.log")
	logger, err := Open(Options{Path: path, Level: LevelTrace, MaxSizeMB: 1, MaxBackups: 1, MaxTotalMB: 4})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	restore := SetDefault(logger)
	defer SetDefault(restore)

	secret := secretFixtures[0].secret
	PaneText("pane capture", "gi_1", "$ export CLAUDE_CODE_OAUTH_TOKEN="+secret+"\n$ make test")
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	body := readFile(t, path)
	if strings.Contains(body, secret) {
		t.Fatalf("the token survived at trace:\n%s", body)
	}
	if !strings.Contains(body, "TRACE") || !strings.Contains(body, "pane capture") {
		t.Fatalf("the trace record never arrived:\n%s", body)
	}
	if !strings.Contains(body, "make test") {
		t.Fatalf("trace dropped the pane text it was asked for:\n%s", body)
	}
}

// A package that has not been given a logger must be silent rather than
// pick a file of its own: a unit test or a benchmark writing into somebody's
// home would be a surprise, and a slow one.
func TestTheDefaultLoggerIsDisabled(t *testing.T) {
	restore := SetDefault(nil)
	defer SetDefault(restore)
	if Enabled(LevelError) {
		t.Fatal("the default logger is enabled")
	}
	Error("this goes nowhere")
	if got := Default().Path(); got != "" {
		t.Fatalf("the default logger has a path: %q", got)
	}
}

// The TUI owns the terminal: a byte on stdout or stderr lands in the middle
// of a rendered frame. Nothing in the logging path may write to either, and
// that includes the paths where the log itself is failing.
func TestLoggingNeverTouchesStdoutOrStderr(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gate-inbox.log")
	logger, err := Open(Options{
		Path: path, Level: LevelTrace,
		MaxSizeMB: 1, MaxBackups: 1, MaxTotalMB: 2, Compress: true,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	realOut, realErr := os.Stdout, os.Stderr
	outRead, outWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	errRead, errWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout, os.Stderr = outWrite, errWrite
	defer func() { os.Stdout, os.Stderr = realOut, realErr }()

	filler := strings.Repeat("b", 512)
	for i := 0; i < 3000; i++ {
		logger.Info("filler", "i", i, "pad", filler)
	}
	// Take the log's own directory away underneath it, which is the class of
	// failure that tempts a library into printing a complaint.
	os.RemoveAll(dir)
	for i := 0; i < 3000; i++ {
		logger.Info("filler after the directory went away", "i", i, "pad", filler)
	}
	logger.Close()

	outWrite.Close()
	errWrite.Close()
	os.Stdout, os.Stderr = realOut, realErr

	for name, reader := range map[string]*os.File{"stdout": outRead, "stderr": errRead} {
		var buf [4096]byte
		n, _ := reader.Read(buf[:])
		if n > 0 {
			t.Fatalf("logging wrote %d bytes to %s: %q", n, name, string(buf[:n]))
		}
	}
}
