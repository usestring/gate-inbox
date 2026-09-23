package sessioncmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/internal/migrate"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// claudeSource seeds a claude row with a transcript on disk under the
// harness's own claude home, so a migration has something to point at. The
// locator scans every project directory, so the directory name is free.
func claudeSource(t *testing.T, h *sessionHarness) (store.Session, string) {
	t.Helper()
	claudeHome := t.TempDir()
	h.sessions.roots = migrate.Roots{ClaudeHome: claudeHome, CodexRoot: t.TempDir()}
	source := store.Session{
		ID:             uuid.NewString()[:8],
		Name:           "payments-fix",
		Tool:           "claude",
		Cwd:            h.caller.Cwd,
		Group:          h.caller.Group,
		Status:         status.Idle,
		AgentSessionID: "conv-1234",
	}
	if err := h.store.CreateSession(source); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(claudeHome, "projects", "any-project", source.AgentSessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte(`{"type":"user"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return source, transcript
}

func waitForUnwrappedOutput(t *testing.T, sessions *Sessions, callerID, targetID, marker string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var screen SessionScreen
	var err error
	for time.Now().Before(deadline) {
		screen, err = sessions.Read(callerID, targetID, "")
		if err == nil && strings.Contains(strings.ReplaceAll(screen.Output, "\n", ""), marker) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("session never showed %q: output=%q err=%v", marker, screen.Output, err)
}

func TestMigrateStartsTheConversationOverOnAnotherToolWithRealTmux(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	source, transcript := claudeSource(t, h)

	moved, err := h.sessions.Migrate(h.caller.ID, source.ID, MigrateOptions{Tool: "echoer"})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if moved.Tool != "echoer" || moved.Name != "payments-fix-echoer" || !moved.Running {
		t.Fatalf("moved = %+v", moved)
	}
	if moved.Group != source.Group || !sameTerminalPath(moved.Directory, source.Cwd) {
		t.Fatalf("moved target = %+v, source = %+v", moved, source)
	}
	stored, err := h.store.Get(moved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != status.Starting {
		t.Fatalf("stored row = %+v", stored)
	}
	if stored.NameSource != store.SourceUser {
		t.Fatalf("a migrated row's name is the operator's, not a placeholder: %q", stored.NameSource)
	}
	// echo prints the launch prompt, so the pane proves the new agent was
	// pointed at the transcript rather than handed an empty brief. The pane
	// wraps the path at its own width, so the screen is read unwrapped.
	waitForUnwrappedOutput(t, h.sessions, h.caller.ID, moved.ID, transcript)
	// The source is left alone: still on the board, still the same row.
	kept, err := h.store.Get(source.ID)
	if err != nil || kept.Archived || kept.Tool != "claude" {
		t.Fatalf("source after migrate = %+v, err %v", kept, err)
	}
}

func TestMigrateTakesANameAndRefusesWhatCannotMove(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	source, _ := claudeSource(t, h)

	named, err := h.sessions.Migrate(h.caller.ID, source.ID, MigrateOptions{Tool: "flagged", Name: "payments/take-two"})
	if err != nil {
		t.Fatalf("Migrate with a name: %v", err)
	}
	if named.Name != "payments-take-two" {
		t.Fatalf("name = %q", named.Name)
	}

	cases := []struct {
		name string
		opts MigrateOptions
		want string
	}{
		{"no tool", MigrateOptions{}, "tool is empty"},
		{"unknown tool", MigrateOptions{Tool: "nope"}, "not configured"},
		{"a shell", MigrateOptions{Tool: "terminal"}, "opens a shell"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := h.sessions.Migrate(h.caller.ID, source.ID, tc.opts); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
	// The caller itself runs a tool with no transcript the manager can find.
	if _, err := h.sessions.Migrate(h.caller.ID, h.caller.ID, MigrateOptions{Tool: "claude"}); err == nil || !strings.Contains(err.Error(), "no captured conversation id") {
		t.Fatalf("a source without a conversation should be refused: %v", err)
	}
}

func TestSwitchAccountMigratesLargeContextInsteadOfResuming(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"ALICE1", ""} {
		t.Run("target-"+target, func(t *testing.T) {
			h := newSessionHarness(t)
			source, transcript := claudeSource(t, h)
			if err := h.store.SetAccount(source.ID, "OLD"); err != nil {
				t.Fatal(err)
			}
			if err := h.store.SetSetting("account_borrower:"+source.ID, "ORIGINAL"); err != nil {
				t.Fatal(err)
			}
			if err := h.store.SetSetting(store.DefaultAccountSetting, "OTHER"); err != nil {
				t.Fatal(err)
			}
			raw := fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"model":"claude-fixture","content":[{"type":"text","text":"continue this work"}],"usage":{"input_tokens":1001,"cache_read_input_tokens":198000,"cache_creation_input_tokens":1000}}}`, time.Now().Format(time.RFC3339Nano))
			if err := os.WriteFile(transcript, []byte(raw+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := h.driver.Create(source.ID, source.Cwd, "cat", nil, 80, 24); err != nil {
				t.Fatal(err)
			}
			moved, err := h.sessions.SwitchAccount(h.caller.ID, source.ID, target)
			if err != nil {
				t.Fatal(err)
			}
			row, err := h.store.Get(moved.ID)
			if err != nil {
				t.Fatal(err)
			}
			if row.ID == source.ID || row.MigrationID != source.ID || row.AgentSessionID == source.AgentSessionID || row.Account != target {
				t.Fatalf("not migrated to chosen account: %+v", row)
			}
			kept, err := h.store.Get(source.ID)
			if err != nil || kept.Account != "OLD" || !h.driver.Exists(source.ID) {
				t.Fatalf("source changed: %+v %v", kept, err)
			}
			borrower, err := h.store.Setting("account_borrower:" + moved.ID)
			if err != nil || borrower != "ORIGINAL" {
				t.Fatalf("borrower=%q err=%v", borrower, err)
			}
			waitForUnwrappedOutput(t, h.sessions, h.caller.ID, moved.ID, "continue the work")
		})
	}
}
