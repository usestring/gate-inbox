package ui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/sysstat"
)

func TestManualLaunchMemoryChoice(t *testing.T) {
	for _, test := range []struct {
		name              string
		threshold, used   float64
		known, wantChoice bool
	}{
		{"disabled", 0, 99, true, false},
		{"enough memory", 15, 80, true, false},
		{"at threshold", 15, 85, true, false},
		{"low memory", 15, 95, true, true},
		{"unavailable reading", 15, 0, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := &Model{cfg: config.Config{NewSession: config.NewSession{MemoryAvailableBelow: test.threshold}},
				mode: modeForm, snap: sysstat.Snapshot{MemOK: test.known, MemPercent: test.used}}
			calls := 0
			request := manualLaunchRequest{Tool: "claude", Model: "chosen", Prompt: "keep this draft"}
			m.confirmManualLaunch(request, func() (tea.Model, tea.Cmd) { calls++; return m, nil })
			if (m.mode == modeLaunchMemory) != test.wantChoice || (calls == 0) != test.wantChoice {
				t.Fatalf("mode %v, launches %d; want choice %v", m.mode, calls, test.wantChoice)
			}
			if !test.wantChoice {
				return
			}
			m.handleLaunchMemoryKey(tea.KeyPressMsg{Code: tea.KeyEscape})
			if calls != 0 || m.mode != modeForm {
				t.Fatal("cancel launched or lost the form")
			}
			m.confirmManualLaunch(request, func() (tea.Model, tea.Cmd) { calls++; return m, nil })
			m.launchMemory.selected = 1
			m.handleLaunchMemoryKey(enterKey())
			if calls != 1 || m.mode != modeForm || m.launchMemory.local != nil {
				t.Fatal("local override did not run exactly once")
			}
		})
	}
}

func TestDevboxFailureKeepsTheManualRequest(t *testing.T) {
	m := &Model{cfg: config.Config{NewSession: config.NewSession{MemoryAvailableBelow: 15, DevboxCommand: "remote-launcher"}},
		mode: modeForm, snap: sysstat.Snapshot{MemOK: true, MemPercent: 99}}
	calls := 0
	request := manualLaunchRequest{Tool: "codex", Model: "selected-model", Prompt: "draft", Directory: "/local/project"}
	m.confirmManualLaunch(request, func() (tea.Model, tea.Cmd) { calls++; return m, nil })
	path := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(path, []byte("private request"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.finishDevboxLaunch(devboxLaunchDoneMsg{path: path, err: errors.New("unreachable")})
	if calls != 0 || m.mode != modeLaunchMemory || m.launchMemory.request.Prompt != request.Prompt || m.launchMemory.request.Model != request.Model {
		t.Fatal("failed remote launch ran locally or lost its request")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("private request was not removed")
	}
	m.launchMemory.selected = 1
	m.handleLaunchMemoryKey(enterKey())
	if calls != 1 {
		t.Fatal("local override after failure did not run")
	}
}

func TestNewSessionMemoryDoesNotGateSharedSpawning(t *testing.T) {
	m := buildModel(t)
	m.cfg.NewSession.MemoryAvailableBelow = 100
	m.snap = sysstat.Snapshot{MemOK: true, MemPercent: 99}
	if _, err := m.spawnSessionAs("claude", "", "background", t.TempDir(), "", "", false, "user"); err != nil {
		t.Fatal(err)
	}
	if m.mode == modeLaunchMemory {
		t.Fatal("shared spawning entered the manual chooser")
	}
}

func TestQuickAndFormLaunchesWaitForMemoryChoice(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		t.Run(map[bool]string{false: "quick", true: "form"}[advanced], func(t *testing.T) {
			m := buildModel(t)
			m.cfg.NewSession.MemoryAvailableBelow = 100
			m.snap = sysstat.Snapshot{MemOK: true, MemPercent: 99}
			if advanced {
				m.openForm()
				m.form.name.SetValue("manual-task")
				m.form.prompt.setValue("preserve my task", 0)
				m.form.model.SetValue("chosen-model")
				m.submitForm()
			} else {
				m.spawnInstantIn("claude", "")
			}
			if m.mode != modeLaunchMemory || len(m.sessions) != 0 {
				t.Fatalf("mode %v, sessions %d", m.mode, len(m.sessions))
			}
			m.handleLaunchMemoryKey(tea.KeyPressMsg{Code: tea.KeyEscape})
			if advanced && (m.form.name.Value() != "manual-task" || m.form.prompt.message() != "preserve my task" || m.form.model.Value() != "chosen-model") {
				t.Fatal("cancel discarded the form")
			}
		})
	}
}
