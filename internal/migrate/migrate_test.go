package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/store"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixtureRoots(t *testing.T) Roots {
	t.Helper()
	return Roots{ClaudeHome: t.TempDir(), CodexRoot: t.TempDir()}
}

func TestLocateFindsAClaudeTranscriptUnderTheProjectDirectory(t *testing.T) {
	roots := fixtureRoots(t)
	cwd := "/home/dev/repos/app"
	want := filepath.Join(roots.ClaudeHome, "projects", "-home-dev-repos-app", "abc-123.jsonl")
	writeFile(t, want, `{"type":"user"}`+"\n")
	source := store.Session{ID: "s1", Name: "app", Cwd: cwd, AgentSessionID: "abc-123"}

	got, err := Locate(roots, "claude", config.Tool{Command: "claude"}, source)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != want || got.Command != "" || !strings.Contains(got.Format, "Claude Code") {
		t.Fatalf("transcript = %+v", got)
	}
}

// A wrapper named for what it runs ("cc" running claude) is read by its
// session_store, so a custom block still resolves when it names one.
func TestLocateReadsTheFormatOffSessionStore(t *testing.T) {
	roots := fixtureRoots(t)
	want := filepath.Join(roots.CodexRoot, "2026", "09", "06", "rollout-2026-09-06T01-02-03-0192abcd.jsonl")
	writeFile(t, want, `{"type":"session_meta"}`+"\n")
	source := store.Session{ID: "s1", Name: "app", Cwd: "/x", AgentSessionID: "0192abcd"}

	got, err := Locate(roots, "cx", config.Tool{Command: "codex", SessionStore: "codex"}, source)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != want || !strings.Contains(got.Format, "Codex") {
		t.Fatalf("transcript = %+v", got)
	}
}

func TestLocateHandsOpenCodeOverAsAnExportCommand(t *testing.T) {
	source := store.Session{ID: "s1", Name: "app", Cwd: "/x", AgentSessionID: "ses_01'x"}
	got, err := Locate(fixtureRoots(t), "opencode", config.Tool{Command: "opencode", SessionStore: "opencode"}, source)
	if err != nil {
		t.Fatal(err)
	}
	if want := `opencode session export 'ses_01'\''x'`; got.Path != "" || got.Command != want {
		t.Fatalf("transcript = %+v, want command %q", got, want)
	}
}

func TestLocateRefusesWhatItCannotRead(t *testing.T) {
	roots := fixtureRoots(t)
	cases := []struct {
		name   string
		tool   string
		cfg    config.Tool
		source store.Session
		want   string
	}{
		{"a shell", "terminal", config.Tool{Shell: true}, store.Session{Name: "sh", AgentSessionID: "x"}, "is a shell"},
		{"no conversation id", "claude", config.Tool{Command: "claude"}, store.Session{Name: "fresh"}, "no captured conversation id"},
		{"no transcript yet", "claude", config.Tool{Command: "claude"}, store.Session{Name: "new", Cwd: "/x", AgentSessionID: "missing"}, "no claude transcript on disk"},
		{"an unknown store", "grok", config.Tool{Command: "grok"}, store.Session{Name: "g", AgentSessionID: "x"}, "cannot locate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Locate(roots, tc.tool, tc.cfg, tc.source)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestPromptNamesTheSourceTheFileAndHowToReadIt(t *testing.T) {
	brief := Brief{
		Source:     store.Session{ID: "cafe0001", Name: "payments-fix", Cwd: "/repo"},
		SourceTool: "claude",
		Transcript: Transcript{Path: "/home/dev/.claude/projects/-repo/abc.jsonl", Format: formatNotes["claude"]},
	}
	prompt := Prompt(brief)
	for _, want := range []string{
		"ran on claude", `"payments-fix" (id cafe0001)`, "/repo",
		"Its full transcript is the file /home/dev/.claude/projects/-repo/abc.jsonl",
		`"type":"user"`, "start from its end", "continue the work exactly where it left off",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "still running") {
		t.Fatalf("a stopped source must not be offered as someone to ask:\n%s", prompt)
	}
	if strings.HasPrefix(prompt, "[gate-inbox]") {
		t.Fatalf("a launch prompt carries no manager band:\n%s", prompt)
	}
}

func TestPromptOffersALiveSourceThroughTheManagersOwnActions(t *testing.T) {
	brief := Brief{
		Source:        store.Session{ID: "cafe0001", Name: "payments-fix", Cwd: "/repo"},
		SourceTool:    "codex",
		Transcript:    Transcript{Command: "opencode export 'x'"},
		SourceRunning: true,
		ReadAction:    "read_session",
		SendAction:    "send_session",
	}
	prompt := Prompt(brief)
	for _, want := range []string{
		"prints from this command, run here: opencode export 'x'",
		"still running as Gate Inbox session cafe0001", "ask it with send_session", "read its answer with read_session",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, prompt)
		}
	}
}
