package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

func writeText(t *testing.T, name, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The file form is a substitute for the inline text, never an addition to
// it, and it must be unambiguous about which file: an agent that names a
// relative path is told so rather than handed whatever sat in the server's
// working directory.
func TestTextArgTakesExactlyOneSource(t *testing.T) {
	path := writeText(t, "brief.md", "do the thing\n\n")

	if got, err := textArg("inline", "", "prompt", "prompt_file"); err != nil || got != "inline" {
		t.Fatalf("inline only = %q, %v", got, err)
	}
	if got, err := textArg("", path, "prompt", "prompt_file"); err != nil || got != "do the thing" {
		t.Fatalf("file only = %q, %v; want the file with its trailing newlines dropped", got, err)
	}
	if got, err := textArg("", "", "prompt", "prompt_file"); err != nil || got != "" {
		t.Fatalf("neither = %q, %v; want empty and no error, the tool decides whether that is allowed", got, err)
	}
	if _, err := textArg("inline", path, "prompt", "prompt_file"); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("both: err = %v, want a refusal naming both", err)
	}
	if _, err := textArg("", "brief.md", "prompt", "prompt_file"); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative: err = %v, want a refusal asking for an absolute path", err)
	}
	if _, err := textArg("", filepath.Join(t.TempDir(), "missing.md"), "prompt", "prompt_file"); err == nil || !strings.Contains(err.Error(), "prompt_file") {
		t.Fatalf("missing: err = %v, want it named by the argument", err)
	}
	empty := writeText(t, "empty.md", "\n")
	if _, err := textArg("", empty, "prompt", "prompt_file"); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty file: err = %v, want a refusal", err)
	}
}

func TestTextArgExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, "brief.md"), []byte("from home"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := textArg("", "~/brief.md", "prompt", "prompt_file"); err != nil || got != "from home" {
		t.Fatalf("~/brief.md = %q, %v", got, err)
	}
}

// Every tool that takes a body of text takes it from a file too, and what
// reaches the command layer is the file's text, exactly as the inline form
// would have carried it.
func TestSessionToolsReadTheirTextFromAFile(t *testing.T) {
	brief := writeText(t, "brief.md", "# Brief\n\nBuild the retry path.\n")
	fake := &fakeSessionCommands{
		created: sessioncmd.Session{ID: "e5f6a7b8", Name: "worker", Tool: "claude", Running: true},
	}
	session := connectServer(t, serverWithFakes(t, fake))

	if text, isError := callText(t, session, "create_session",
		map[string]any{"name": "worker", "tool": "claude", "prompt_file": brief}); isError {
		t.Fatalf("create_session: %s", text)
	}
	if want := "# Brief\n\nBuild the retry path."; fake.createdOpts.Prompt != want {
		t.Errorf("Prompt = %q, want the file's text %q", fake.createdOpts.Prompt, want)
	}

	message := writeText(t, "message.md", "the branch moved to main\n")
	if text, isError := callText(t, session, "send_session",
		map[string]any{"session_id": "e5f6a7b8", "message_file": message}); isError {
		t.Fatalf("send_session: %s", text)
	}
	if fake.sentMessage != "the branch moved to main" {
		t.Errorf("send_session Message = %q, want the file's text", fake.sentMessage)
	}

	fake.sentMessage = ""
	if text, isError := callText(t, session, "send_children",
		map[string]any{"message_file": message}); isError {
		t.Fatalf("send_children: %s", text)
	}
	if fake.sentMessage != "the branch moved to main" {
		t.Errorf("send_children Message = %q, want the file's text", fake.sentMessage)
	}

	body := writeText(t, "task.md", "see internal/retry\n")
	if text, isError := callText(t, session, "task",
		map[string]any{"action": "create", "title": "fix retries", "body_file": body}); isError {
		t.Fatalf("task create: %s", text)
	}
	if fake.taskBody != "see internal/retry" {
		t.Errorf("task Body = %q, want the file's text", fake.taskBody)
	}

	// message used to be required by the schema; offering its file twin made
	// both optional, so the tool itself refuses a call carrying neither.
	fake.sentMessage = ""
	if text, isError := callText(t, session, "send_session",
		map[string]any{"session_id": "e5f6a7b8"}); !isError || !strings.Contains(text, "pass message or message_file") {
		t.Fatalf("send_session with neither: isError=%v text=%q", isError, text)
	}
	if fake.sentMessage != "" {
		t.Errorf("a refused send still forwarded %q", fake.sentMessage)
	}

	// Both at once is refused before anything is forwarded, and the refusal
	// is a tool error the agent reads, not a transport failure.
	fake.sentMessage = ""
	if text, isError := callText(t, session, "send_session",
		map[string]any{"session_id": "e5f6a7b8", "message": "inline", "message_file": message}); !isError || !strings.Contains(text, "not both") {
		t.Fatalf("send_session with both: isError=%v text=%q", isError, text)
	}
	if fake.sentMessage != "" {
		t.Errorf("a refused send still forwarded %q", fake.sentMessage)
	}
}
