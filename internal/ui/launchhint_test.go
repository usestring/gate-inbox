// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func TestReportLaunchErrorOpensInstallHintForMissingCLI(t *testing.T) {
	m := buildModel(t)

	m.reportLaunchError(config.MissingToolError{Binary: "claude"})

	if m.mode != modeLaunchHint {
		t.Fatalf("mode = %v, want modeLaunchHint", m.mode)
	}
	if !strings.Contains(m.launchHint, "claude.ai/install.sh") {
		t.Fatalf("hint %q should name the install command", m.launchHint)
	}
	if !strings.Contains(m.launchHint, "claude") {
		t.Fatalf("hint %q should name the missing CLI", m.launchHint)
	}
	frame := ansi.Strip(m.viewLaunchHint())
	if !strings.Contains(frame, "claude.ai/install.sh") {
		t.Fatalf("dialog should show the install command:\n%s", frame)
	}
}

func TestReportLaunchErrorOpensHintForUnknownMissingCLI(t *testing.T) {
	m := buildModel(t)

	m.reportLaunchError(config.MissingToolError{Binary: "acme"})

	if m.mode != modeLaunchHint {
		t.Fatalf("mode = %v, want modeLaunchHint", m.mode)
	}
	if !strings.Contains(m.launchHint, "acme") {
		t.Fatalf("hint %q should name the missing CLI", m.launchHint)
	}
	if !strings.Contains(m.launchHint, "install") {
		t.Fatalf("hint %q should name how to install", m.launchHint)
	}
}

func TestSpawnMissingCLIPromptsInstall(t *testing.T) {
	m := buildModel(t)
	m.cfg.Tools["claude"] = config.Tool{Command: "gi-missing-cli-xyz", DefaultStatus: status.Idle}

	m.openForm()
	m.form.name.SetValue("agent")
	m.form.dir.SetValue(t.TempDir())
	claudeIndex := -1
	for i, name := range m.form.toolNames {
		if name == "claude" {
			claudeIndex = i
		}
	}
	if claudeIndex < 0 {
		t.Fatalf("claude not offered by the form: %v", m.form.toolNames)
	}
	m.form.toolIndex = claudeIndex
	pickGroup(t, m, "")
	m.submitForm()

	if m.mode != modeLaunchHint {
		t.Fatalf("mode = %v, err = %q, want modeLaunchHint", m.mode, m.errBar.text)
	}
	if !strings.Contains(m.launchHint, "gi-missing-cli-xyz") {
		t.Fatalf("hint %q should name the missing binary", m.launchHint)
	}
	if len(m.sessionRows()) != 0 {
		t.Fatalf("no session may spawn without the CLI, got %v", sessionNames(m))
	}
}

func TestReviveMissingCLIPromptsInstall(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: newID(), Name: "agent", Tool: "claude", Cwd: t.TempDir()}
	if err := m.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	m.sessions = []store.Session{sess}
	m.cfg.Tools["claude"] = config.Tool{
		Command:       "cat",
		ReviveCommand: "gi-missing-cli-xyz",
		DefaultStatus: status.Idle,
	}

	err := m.reviveSession(sess)
	if err == nil {
		t.Fatal("revive of a missing CLI should fail")
	}
	m.reportLaunchError(err)
	if m.mode != modeLaunchHint {
		t.Fatalf("mode = %v, err = %q, want modeLaunchHint", m.mode, m.errBar.text)
	}
	if !strings.Contains(m.launchHint, "gi-missing-cli-xyz") {
		t.Fatalf("hint %q should name the revive binary", m.launchHint)
	}
}

func TestReportLaunchErrorKeepsPlainErrorsOnStatusLine(t *testing.T) {
	m := buildModel(t)

	m.reportLaunchError(fmt.Errorf("tmux create: boom"))

	if m.mode != modeList {
		t.Fatalf("mode = %v, want modeList", m.mode)
	}
	if m.errBar.text != "tmux create: boom" {
		t.Fatalf("errBar = %q", m.errBar.text)
	}
}

// A form spawn the hint dialog refused takes the form off screen with it,
// so the images its prompt was holding have nothing left naming them.
func TestFormSpawnRefusedByTheHintReleasesItsImages(t *testing.T) {
	m := buildModel(t)
	m.cfg.Tools["claude"] = config.Tool{Command: "gi-missing-cli-xyz", DefaultStatus: status.Idle}

	m.openForm()
	m.form.name.SetValue("agent")
	m.form.dir.SetValue(t.TempDir())
	m.form.toolNames = []string{"claude"}
	m.form.toolIndex = 0
	path := tempImage(t, "mock.png")
	m.form.prompt.attachments = []imageAttachment{{id: 1, path: path}}
	m.form.prompt.input.SetValue("match " + imageToken(1))

	m.submitForm()

	if m.mode != modeLaunchHint {
		t.Fatalf("mode = %v, err = %q, want modeLaunchHint", m.mode, m.errBar.text)
	}
	if len(m.form.prompt.attachments) != 0 {
		t.Fatalf("attachments = %+v, want the refused form's images released", m.form.prompt.attachments)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the image file should be gone, stat err = %v", err)
	}
}

// A spawn that fails into the status bar leaves the form up, so the prompt
// still names its images and they have to survive for the retry. The
// failure is a hooks directory the launch cannot write, which is the far
// side of spawnSession rather than a field the form could have validated.
func TestFormSpawnErrorInTheBarKeepsItsImages(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	m.openForm()
	m.form.name.SetValue("agent")
	m.form.dir.SetValue(dir)
	hooksDir := m.hooks.Dir()
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(hooksDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(hooksDir, 0o755) })
	path := tempImage(t, "mock.png")
	m.form.prompt.attachments = []imageAttachment{{id: 1, path: path}}
	m.form.prompt.input.SetValue("match " + imageToken(1))

	m.submitForm()

	if m.mode != modeForm || m.errBar.text == "" {
		t.Fatalf("mode = %v, err = %q, want the form still up with the error", m.mode, m.errBar.text)
	}
	// Named so the test cannot pass on an earlier refusal: this is the
	// spawn failing, not a field the form checked before it got there.
	if !strings.Contains(m.errBar.text, hooksDir) {
		t.Fatalf("err = %q, want the hooks directory that blocked the spawn", m.errBar.text)
	}
	if len(m.sessionRows()) != 0 {
		t.Fatalf("a failed spawn leaves no session, got %v", sessionNames(m))
	}
	if len(m.form.prompt.attachments) != 1 {
		t.Fatalf("attachments = %+v, want the chip kept for the retry", m.form.prompt.attachments)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the image the prompt still names must survive: %v", err)
	}
}
