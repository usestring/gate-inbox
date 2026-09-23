package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/adopt"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// logToTemp installs a logger writing under the test's own directory. No
// test may write into the developer's real home: a log path resolved from
// ambient state is exactly the fixture that breaks on a second checkout.
func logToTemp(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gate-inbox.log")
	logger, err := logging.Open(logging.Options{
		Path: path, Level: logging.LevelInfo, MaxSizeMB: 1, MaxBackups: 1, MaxTotalMB: 4,
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

// readLog closes nothing: the sink writes on a goroutine, so a test reads
// through the same wait the shutdown path uses.
func readLog(t *testing.T, path string) string {
	t.Helper()
	logging.Default().Close()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

// A model with rows but no tmux driver: the point is the dispatch record,
// and the keys used here never reach the driver.
func dispatchModel() *Model {
	m := &Model{poller: &poller{}, mode: modeList, focusOnEnter: true, width: 120, height: 40}
	m.rows = []treeRow{
		{isGroup: true, group: "backend"},
		{sess: store.Session{ID: "gi_1", Name: "sample-repo-71", Tool: "claude", Status: status.Waiting}},
		{sess: store.Session{ID: "gi_2", Name: "old-run", Tool: "codex", Status: status.Idle, Archived: true}},
	}
	return m
}

func TestKeyPressIsLoggedWithTheBranchItTook(t *testing.T) {
	path := logToTemp(t)
	m := dispatchModel()
	m.cursor = 1
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	body := readLog(t, path)
	for _, want := range []string{
		`msg=key`, `key=down`, `branch=list`, `mode=list`,
		`row=session`, `name=sample-repo-71`, `session=gi_1`, `tool=claude`,
		`status=waiting`, `archived=false`, `enterFocuses=true`, `cursor=1/3`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("the key record is missing %s:\n%s", want, body)
		}
	}
}

// The bug that motivated this log is a dispatch question: which handler ran
// while the search field was open. The record has to say so.
func TestSearchKeysRecordTheSearchBranchAndTheQuery(t *testing.T) {
	path := logToTemp(t)
	m := dispatchModel()
	m.cursor = 1
	m.searching = true
	m.search = "gate"
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	body := readLog(t, path)
	for _, want := range []string{`branch=search`, `searching=true`, `query=gate`, `key=enter`} {
		if !strings.Contains(body, want) {
			t.Fatalf("the search record is missing %s:\n%s", want, body)
		}
	}
}

func TestArchivedRowIsNamedInTheRecord(t *testing.T) {
	path := logToTemp(t)
	m := dispatchModel()
	m.cursor = 2
	m.Update(tea.KeyPressMsg{Code: tea.KeyUp})

	body := readLog(t, path)
	if !strings.Contains(body, "archived=true") || !strings.Contains(body, "session=gi_2") {
		t.Fatalf("an archived row is not identifiable in the record:\n%s", body)
	}
}

func TestModeChangeIsLoggedWithItsReason(t *testing.T) {
	path := logToTemp(t)
	m := dispatchModel()
	m.cursor = 1
	m.Update(tea.KeyPressMsg{Code: 'H', Text: "H"})

	body := readLog(t, path)
	if !strings.Contains(body, "became=help") {
		t.Fatalf("the mode the key moved to is not in the record:\n%s", body)
	}
}

func TestModeNamesAreStable(t *testing.T) {
	for _, want := range []struct {
		mode mode
		name string
	}{
		{modeList, "list"}, {modeFocus, "focus"},
		{modeNameSweep, "name-sweep"}, {modeConfirmDelete, "confirm-delete"},
	} {
		if got := want.mode.String(); got != want.name {
			t.Fatalf("mode %d = %q, want %q", int(want.mode), got, want.name)
		}
	}
}

// Nothing is logged when no logger has been installed, which is what keeps
// the render benchmarks and every other test off the disk.
func TestDispatchIsSilentWithoutALogger(t *testing.T) {
	previous := logging.SetDefault(nil)
	defer logging.SetDefault(previous)
	m := dispatchModel()
	m.cursor = 1
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if logging.Enabled(logging.LevelError) {
		t.Fatal("a logger is installed")
	}
}

func TestRejectionSummaryIsStableAndReadable(t *testing.T) {
	if got := rejectionSummary(nil); got != "none" {
		t.Fatalf("rejectionSummary(nil) = %q", got)
	}
	got := rejectionSummary(map[string]int{"no tool matched": 2, "already on the board": 27})
	if got != "already on the board=27 no tool matched=2" {
		t.Fatalf("rejectionSummary = %q", got)
	}
}

// "not confident" says nothing on its own; which of the two signals was
// missing is the whole answer to "why was my pane not adopted".
func TestSignalNamesReportWhatIdentificationSaw(t *testing.T) {
	if got := signalNames([]adopt.Signal{adopt.SignalPrompt, adopt.SignalCommand}); got != "command,prompt" {
		t.Fatalf("signalNames = %q", got)
	}
	if got := signalNames(nil); got != "" {
		t.Fatalf("signalNames(nil) = %q", got)
	}
}
