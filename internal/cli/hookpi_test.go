package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/logging"
)

// The pi extension asks for the board's standing instructions and gets the
// server's whole block, not just its first line, with the launch steering
// after it.
func TestHookPiSteeringAnswersTheWholeBlock(t *testing.T) {
	var out bytes.Buffer
	if err := RunHook(strings.NewReader(`{}`), &out, []string{"pi", "steering"}, "abcd1234", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	var answer struct {
		Steering string `json:"steering"`
	}
	if err := json.Unmarshal(out.Bytes(), &answer); err != nil {
		t.Fatalf("hook printed %q: %v", out.String(), err)
	}
	for _, want := range []string{"CROSS-SESSION-MESSAGE", "# Delegating work", "report_status"} {
		if !strings.Contains(answer.Steering, want) {
			t.Fatalf("steering lacks %q:\n%s", want, answer.Steering)
		}
	}
}

// Pi's own /name renames the board row: the name lands in the session's name
// file for the board's next poll, and the hook prints nothing.
func TestHookPiSessionInfoChangedRenamesTheRow(t *testing.T) {
	configDir := t.TempDir()
	var out bytes.Buffer
	if err := RunHook(strings.NewReader(`{"name":"pi-board-tools"}`), &out, []string{"pi", "session_info_changed"}, "abcd1234", configDir); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("hook printed %q", out.String())
	}
	got, err := os.ReadFile(hooks.NewManager(configDir).NameFile("abcd1234"))
	if err != nil || string(got) != "pi-board-tools" {
		t.Fatalf("name file = %q, %v", got, err)
	}
	for _, payload := range []string{`{}`, `{"name":""}`, `not json`} {
		other := t.TempDir()
		if err := RunHook(strings.NewReader(payload), &out, []string{"pi", "session_info_changed"}, "abcd1234", other); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(hooks.NewManager(other).NameFile("abcd1234")); !os.IsNotExist(err) {
			t.Fatalf("payload %q renamed the row", payload)
		}
	}
}

// A pi config's own gate-inbox server is logged to the board's log, which
// only the board otherwise opens.
func TestHookPiMCPOverrideLogsToTheBoard(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv(logging.FileEnv, "")
	var out bytes.Buffer
	if err := RunHook(strings.NewReader(`{"sources":["/home/u/.pi/agent/mcp.json"]}`), &out, []string{"pi", "mcp_override"}, "abcd1234", configDir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(configDir, logging.DirName, logging.FileName))
	if err != nil || !strings.Contains(string(data), "overrides the board's") || !strings.Contains(string(data), "abcd1234") {
		t.Fatalf("board log = %q, %v", data, err)
	}
}
