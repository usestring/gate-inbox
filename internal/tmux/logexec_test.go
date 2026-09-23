package tmux

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/tracetest"
)

func logToTemp(t *testing.T) string {
	t.Helper()
	return logToTempAt(t, logging.LevelInfo)
}

func logToTempAt(t *testing.T, level slog.Level) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gate-inbox.log")
	logger, err := logging.Open(logging.Options{
		Path: path, Level: level, MaxSizeMB: 1, MaxBackups: 1, MaxTotalMB: 4,
	})
	if err != nil {
		t.Fatalf("logging.Open: %v", err)
	}
	previous := logging.SetDefault(logger)
	t.Cleanup(func() {
		logging.SetDefault(previous)
		logger.Close()
	})
	return path
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	logging.Default().Close()
	body, err := os.ReadFile(path)
	// A sink that was handed nothing never creates the file, and "nothing was
	// written" is an answer these tests ask for rather than a broken run: the
	// assertions below say which of the two they wanted.
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

// The command, the server it went to and how long it took are what a
// "tmux did nothing" report needs and what the error alone never carries.
// The routine record lives at debug, so this is where it is asked for.
func TestEveryTmuxInvocationIsRecorded(t *testing.T) {
	path := logToTempAt(t, logging.LevelDebug)
	driver, _ := stubTmux(t)
	if !driver.Exists("one") {
		t.Fatal("the stub answered has-session with a refusal")
	}
	body := readLog(t, path)
	if !strings.Contains(body, `msg="tmux command"`) {
		t.Fatalf("no tmux command was recorded:\n%s", body)
	}
	for _, want := range []string{"socket=default", "has-session", "took="} {
		if !strings.Contains(body, want) {
			t.Fatalf("the tmux record is missing %s:\n%s", want, body)
		}
	}
}

func TestAFailedTmuxInvocationIsRecordedAsAWarning(t *testing.T) {
	path := logToTemp(t)
	driver, _ := stubTmux(t)
	driver.bin = filepath.Join(t.TempDir(), "no-such-tmux")

	driver.ScanPanes()
	body := readLog(t, path)
	if !strings.Contains(body, `msg="tmux command failed"`) {
		t.Fatalf("a failed invocation was not recorded:\n%s", body)
	}
	if !strings.Contains(body, "list-panes") {
		t.Fatalf("the failed record does not name the command:\n%s", body)
	}
}

func TestAttachIsRecordedBeforeTheTerminalIsHandedOver(t *testing.T) {
	path := logToTemp(t)
	driver, _ := stubTmux(t)
	driver.AttachCommand("one")
	body := readLog(t, path)
	for _, want := range []string{`msg="tmux attach"`, "session=one", `target="=gi_one"`, "nested="} {
		if !strings.Contains(body, want) {
			t.Fatalf("the attach record is missing %s:\n%s", want, body)
		}
	}
}

func TestSplitSocketNamesTheServer(t *testing.T) {
	socket, command := splitSocket([]string{"-L", "gate-inbox", "list-panes", "-a"})
	if socket != "gate-inbox" {
		t.Fatalf("socket = %q, want gate-inbox", socket)
	}
	if strings.Join(command, " ") != "list-panes -a" {
		t.Fatalf("command = %v", command)
	}
	if socket, command := splitSocket([]string{"list-panes"}); socket != "" || len(command) != 1 {
		t.Fatalf("a socketless invocation split wrong: %q %v", socket, command)
	}
}

func TestFirstLineKeepsTheMessageAndDropsTheRest(t *testing.T) {
	if got := firstLine([]byte("  can't find session: gi_9\nusage: tmux\n")); got != "can't find session: gi_9" {
		t.Fatalf("firstLine = %q", got)
	}
	if got := firstLine([]byte("  one line  ")); got != "one line" {
		t.Fatalf("firstLine = %q", got)
	}
}

// "warn" and "error" are what a user picks to see only failures, so a
// failure has to survive them. A guard written against info would leave
// both levels with an empty file.
func TestAFailureIsStillRecordedWithTheLogTurnedDownToWarn(t *testing.T) {
	path := logToTempAt(t, logging.LevelWarn)
	driver, _ := stubTmux(t)
	driver.bin = filepath.Join(t.TempDir(), "no-such-tmux")

	driver.ScanPanes()
	body := readLog(t, path)
	if !strings.Contains(body, `msg="tmux command failed"`) {
		t.Fatalf("a failure was swallowed at warn:\n%s", body)
	}
	if strings.Contains(body, `msg="tmux command"`) {
		t.Fatalf("warn wrote a routine command record:\n%s", body)
	}
}

// The routine record is the whole log. On the operator's board it was 98.1%
// of the records and 98.5% of the bytes, and every one of them costs the
// caller a redaction pass before the sink ever sees it -- so at the level a
// running manager is left on, it must not be written at all.
func TestARoutineTmuxInvocationIsSilentAtInfo(t *testing.T) {
	path := logToTempAt(t, logging.LevelInfo)
	driver, _ := stubTmux(t)
	if !driver.Exists("one") {
		t.Fatal("the stub answered has-session with a refusal")
	}
	body := readLog(t, path)
	if strings.Contains(body, `msg="tmux command"`) {
		t.Fatalf("info wrote a routine tmux record:\n%s", body)
	}
	if strings.Contains(body, "has-session") {
		t.Fatalf("info wrote the argv of a successful call:\n%s", body)
	}
}

// Demoting the success branch must not take the failures with it: a failed
// call is the one a reader came for, and it is still a warning.
func TestAFailureIsStillWarnedWhileRoutineCallsAreSilent(t *testing.T) {
	path := logToTempAt(t, logging.LevelInfo)
	driver, _ := stubTmux(t)
	if !driver.Exists("one") {
		t.Fatal("the stub answered has-session with a refusal")
	}
	driver.bin = filepath.Join(t.TempDir(), "no-such-tmux")
	driver.ScanPanes()

	body := readLog(t, path)
	if !strings.Contains(body, `msg="tmux command failed"`) {
		t.Fatalf("a failure was swallowed alongside the routine records:\n%s", body)
	}
	if strings.Contains(body, `msg="tmux command"`) {
		t.Fatalf("the successful call was recorded too:\n%s", body)
	}
}

// What a tmux call cost is read off its span now, not off the log line, so
// the span has to survive the demotion. Through tracetest, which pins the
// sample rate: the tail sampler keeps one ordinary trace in fifty and a test
// that emitted one span would otherwise pass on a coin toss.
func TestARoutineTmuxInvocationStillCarriesItsSpan(t *testing.T) {
	logToTempAt(t, logging.LevelInfo)
	spans := tracetest.Capture(t)
	driver, _ := stubTmux(t)
	if !driver.Exists("one") {
		t.Fatal("the stub answered has-session with a refusal")
	}
	for _, span := range spans() {
		if span.Name == "tmux.has-session" {
			return
		}
	}
	t.Fatal("a successful tmux call left no span behind: its cost is now unmeasurable")
}
