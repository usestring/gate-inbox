package ui

import (
	"fmt"

	"github.com/charmbracelet/x/ansi"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/restartpresets"
)

func TestRestartLaunchAppendsExtraFlags(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "flagged", t.TempDir(), "")
	sess := m.sessionRows()[0]
	tool := m.cfg.Tools[sess.Tool]

	plain, _ := restartLaunch(tool, "")
	if strings.Contains(plain, "--chrome") {
		t.Fatalf("plain restart carries flags: %q", plain)
	}
	with, _ := restartLaunch(tool, "--chrome")
	if !strings.Contains(with, "--chrome") {
		t.Fatalf("restart with flags = %q, want --chrome", with)
	}
	if tool.SessionIDFlag != "" {
		chromeAt := strings.Index(with, "--chrome")
		idAt := strings.Index(with, tool.SessionIDFlag)
		if chromeAt < 0 || idAt < 0 || chromeAt > idAt {
			t.Fatalf("flags must ride before the conversation id: %q", with)
		}
	}
}

func TestRestartWithPickerConfirmsWithFlags(t *testing.T) {
	m := buildModel(t)
	m.restartFlags = restartpresets.Set{Presets: []restartpresets.Preset{
		{Key: "c", Label: "with Chrome (--chrome)", Args: "--chrome"},
	}}
	createSession(t, m, "browser", t.TempDir(), "")
	m.selectSessionRow(t, "browser")

	m.openRestartWith()
	if m.mode != modeRestartWith {
		t.Fatalf("mode = %v, want restart picker (err=%q)", m.mode, m.errBar.text)
	}
	updated, _ := m.handleRestartWithKey(tea.KeyPressMsg{Code: 'c', Text: "c"})
	m = updated.(*Model)
	if m.mode != modeConfirmDelete {
		t.Fatalf("preset key left mode = %v, want confirm (err=%q)", m.mode, m.errBar.text)
	}
	if m.confirm.restartArgs != "--chrome" {
		t.Fatalf("confirm carries args = %q, want --chrome", m.confirm.restartArgs)
	}
	if !strings.Contains(m.confirm.label, "--chrome") {
		t.Fatalf("confirm label = %q, want the flags named", m.confirm.label)
	}
	if strings.Contains(m.confirm.label, "with with") {
		t.Fatalf("confirm label = %q, stutters", m.confirm.label)
	}
}

func TestRestartWithRunsFlagsOnTheLaunch(t *testing.T) {
	m := buildModel(t)
	m.restartFlags = restartpresets.Set{Presets: []restartpresets.Preset{
		{Key: "c", Label: "with Chrome (--chrome)", Args: "--chrome"},
	}}
	createSession(t, m, "chromed", t.TempDir(), "")
	sess := m.sessionRows()[0]

	argsFile := t.TempDir() + "/launch-args"
	tool := m.cfg.Tools[sess.Tool]
	tool.Command = argCaptureCommand(argsFile)
	m.cfg.Tools[sess.Tool] = tool
	if err := m.tmux.Kill(sess.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	m.selectSessionRow(t, "chromed")

	m.openRestartWith()
	updated, _ := m.handleRestartWithKey(tea.KeyPressMsg{Code: 'c', Text: "c"})
	m = updated.(*Model)
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	if m.errBar.text != "" {
		t.Fatalf("restart with flags: %q", m.errBar.text)
	}
	args := readWhenWritten(t, argsFile)
	if !strings.Contains(args, "--chrome") {
		t.Fatalf("launch arguments = %q, want --chrome", args)
	}
}

func TestRestartWithRefusesWithoutPresets(t *testing.T) {
	m := buildModel(t)
	m.restartFlags = restartpresets.Set{}
	createSession(t, m, "plain", t.TempDir(), "")
	m.selectSessionRow(t, "plain")

	m.openRestartWith()
	if m.mode == modeRestartWith {
		t.Fatal("picker opened with no presets to pick from")
	}
	if m.errBar.text == "" {
		t.Fatal("refusal left no explanation on the error bar")
	}
}

func TestRestartWithRefusalShowsValidationProblems(t *testing.T) {
	m := buildModel(t)
	m.restartFlags = restartpresets.Set{Problems: []string{"preset c has empty args", "preset d has empty args"}}
	createSession(t, m, "invalid", t.TempDir(), "")
	m.selectSessionRow(t, "invalid")
	m.openRestartWith()
	if m.mode == modeRestartWith {
		t.Fatal("picker opened without valid presets")
	}
	for _, problem := range m.restartFlags.Problems {
		if !strings.Contains(m.errBar.text, problem) {
			t.Fatalf("refusal %q omits %q", m.errBar.text, problem)
		}
	}
}

func TestRestartWithSelectionStaysVisible(t *testing.T) {
	for _, width := range []int{40, 80} {
		for _, height := range []int{14, 24} {
			for _, problems := range [][]string{nil, {"preset skipped"}} {
				t.Run(fmt.Sprintf("%dx%d/problems=%d", width, height, len(problems)), func(t *testing.T) {
					m := &Model{width: width, height: height, mode: modeRestartWith}
					m.restartFlags.Problems = problems
					for i := range 26 {
						m.restartFlags.Presets = append(m.restartFlags.Presets, restartpresets.Preset{Key: string(rune('a' + i)), Label: fmt.Sprintf("choice-%02d", i), Args: "--flag"})
					}
					for _, key := range []tea.KeyPressMsg{{Code: tea.KeyDown}, {Code: tea.KeyUp}} {
						for range 27 {
							m.handleRestartWithKey(key)
							frame, _ := m.paint()
							frame = ansi.Strip(frame)
							selected := m.restartFlags.Presets[m.restartWith.index]
							if !strings.Contains(frame, "❯ "+selected.Key+"  "+selected.Title()) {
								t.Fatalf("selection %d hidden:\n%s", m.restartWith.index, frame)
							}
							if !strings.Contains(frame, "esc") || !strings.Contains(frame, "back") {
								t.Fatalf("footer hidden:\n%s", frame)
							}
							for _, problem := range problems {
								if !strings.Contains(frame, problem) {
									t.Fatalf("problem hidden:\n%s", frame)
								}
							}
						}
					}
				})
			}
		}
	}
}
