package sessioncmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/git"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// A pane the board adopted is reached from its row alone. Each command here
// opens its own driver, as a CLI or MCP process does with no board running,
// so nothing one command registers is left for the next to lean on.
func TestAdoptedRowIsReadSentAndKilledWithNoBoard(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	socket := h.driver.SocketName()
	sessions := newSessions(h.sessions.configDir, MCPVocabulary(), func(string) (*tmux.Driver, error) {
		return tmux.NewWithSocket(socket)
	}, git.New)

	paneID, _ := newForeignPane(t, socket)
	if out, err := exec.Command("tmux", "-L", socket, "send-keys", "-t", paneID, "echo adopted-marker", "Enter").CombinedOutput(); err != nil {
		t.Fatalf("type into the foreign pane: %v: %s", err, out)
	}
	adopted := store.Session{ID: uuid.NewString()[:8], Name: "borrowed", Tool: "stoppable", Cwd: h.caller.Cwd, Status: status.Idle,
		TmuxSocket: socket, TmuxPaneID: paneID}
	if err := h.store.CreateSession(adopted); err != nil {
		t.Fatalf("create adopted row: %v", err)
	}

	screen := waitForSessionOutput(t, sessions, h.caller.ID, adopted.ID, "adopted-marker")
	if screen.Mode != "pane" {
		t.Fatalf("read mode = %q, want the live pane", screen.Mode)
	}

	sent, err := sessions.Send(h.caller.ID, adopted.ID, "hello", "", false)
	if err != nil {
		t.Fatalf("Send to the adopted row: %v", err)
	}
	if queued, _ := h.store.QueuedCount(adopted.ID); sent.MessageID == 0 || queued != 1 {
		t.Fatalf("send = %+v with %d queued, want one message queued", sent, queued)
	}

	killed, err := sessions.Kill(h.caller.ID, adopted.ID, extension.KillByCLI)
	if err != nil {
		t.Fatalf("Kill the adopted row: %v", err)
	}
	if killed.Status != status.Dead {
		t.Fatalf("killed status = %q", killed.Status)
	}
	assertAdoptedKilled(t, h.store, socket, adopted, paneID)
}

// assertAdoptedKilled checks a kill of an adopted row did what a kill of a
// started session does: the agent's pane is gone, its last screen kept, the
// row dead, and the end recorded as the operator's.
func assertAdoptedKilled(t *testing.T, st *store.Store, socket string, adopted store.Session, paneID string) {
	t.Helper()
	if foreignPaneExists(t, socket, paneID) {
		t.Fatalf("pane %s is still running after its row was killed", paneID)
	}
	row, err := st.Get(adopted.ID)
	if err != nil {
		t.Fatalf("get adopted row: %v", err)
	}
	if row.Status != status.Dead {
		t.Fatalf("adopted row status = %q, want dead", row.Status)
	}
	if snapshot, _ := st.Snapshot(adopted.ID); !strings.Contains(snapshot, "adopted-marker") {
		t.Fatalf("kill kept no last screen: %q", snapshot)
	}
	ends, err := st.SessionEnds()
	if err != nil {
		t.Fatalf("session ends: %v", err)
	}
	if end := ends[adopted.ID]; end.Reason != store.EndKilled || !end.Matches(row) {
		t.Fatalf("recorded end = %+v, want %q for the agent the row holds", end, store.EndKilled)
	}
}

// adoptedAsking stands up a pane outside the board, on the harness's server,
// holding answerPane's question the way AskUserQuestion does, and files it as
// an adopted top-level row. The pending call is saved the way the global
// PreToolUse hook saves it for an adopted Claude Code: through the launch's
// own ask-pending hook, under the row's id. Each command after this opens
// its own driver, so nothing but the row tells it where the pane is.
func adoptedAsking(t *testing.T, h *sessionHarness, name string) (*Sessions, store.Session) {
	t.Helper()
	socket := h.driver.SocketName()
	sessions := newSessions(h.sessions.configDir, MCPVocabulary(), func(string) (*tmux.Driver, error) {
		return tmux.NewWithSocket(socket)
	}, git.New)
	sessions.claudeHome = t.TempDir()

	dir := t.TempDir()
	fixture := filepath.Join(dir, "pane.txt")
	script := filepath.Join(dir, "answering.sh")
	body := fmt.Sprintf(answeringScript, `"Germany only" "Every storefront"`, storefront.Question)
	if err := os.WriteFile(fixture, []byte(answerPane), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	width, height := paneSize(answerPane)
	if out, err := exec.Command("tmux", "-L", socket, "new-session", "-d", "-s", name,
		"-x", fmt.Sprint(width), "-y", fmt.Sprint(height), "bash "+script+" "+fixture).CombinedOutput(); err != nil {
		t.Fatalf("foreign new-session: %v: %s", err, out)
	}
	out, err := exec.Command("tmux", "-L", socket, "list-panes", "-t", name, "-F", "#{pane_id}").Output()
	if err != nil {
		t.Fatalf("foreign list-panes: %v", err)
	}
	paneID := strings.TrimSpace(string(out))
	waitForForeign(t, socket, paneID, lastLine(answerPane))

	adopted := store.Session{ID: uuid.NewString()[:8], Name: name, Tool: "echoer", Cwd: dir, Status: status.Waiting,
		TmuxSocket: socket, TmuxPaneID: paneID}
	if err := h.store.CreateSession(adopted); err != nil {
		t.Fatalf("create adopted row: %v", err)
	}
	question := storefront
	question.Header = "Scope"
	payload, err := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "AskUserQuestion",
		"tool_use_id": childCallID, "tool_input": map[string]any{"questions": []convo.AskQuestion{question}}})
	if err != nil {
		t.Fatal(err)
	}
	AskPendingHook(h.sessions.configDir, adopted.ID, payload)
	return sessions, adopted
}

// waitForForeign waits for a pane outside the board to show marker.
func waitForForeign(t *testing.T, socket, paneID, marker string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, err := exec.Command("tmux", "-L", socket, "capture-pane", "-p", "-t", paneID).Output()
		if err == nil && strings.Contains(ansi.Strip(string(out)), marker) {
			return string(out)
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane %s never showed %q:\n%s", paneID, marker, out)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// A claude started outside the board has no parent, so its dialog is nobody's
// to answer until a session takes it as a child. Once one does, answer_session
// reaches the adopted pane by the ordinary parent rule, reads the question the
// global hook saved, and keys the answer into the pane; any other session is
// still refused.
func TestAdoptedRowIsAnsweredByTheSessionThatPlacedIt(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	sessions, adopted := adoptedAsking(t, h, "borrowed-asker")
	stranger := store.Session{ID: uuid.NewString()[:8], Name: "stranger", Tool: "echoer", Cwd: h.caller.Cwd,
		Group: "backend", Status: status.Idle}
	if err := h.store.CreateSession(stranger); err != nil {
		t.Fatalf("create stranger row: %v", err)
	}
	answer := []QuestionAnswer{{Answer: "Every storefront"}}

	if _, err := sessions.AnswerAll(h.caller.ID, adopted.ID, answer, true, false); err == nil ||
		!strings.Contains(err.Error(), "nobody's child") {
		t.Fatalf("answer before placing err = %v, want the nobody's-child refusal", err)
	}

	placed, err := sessions.AdoptSession(h.caller.ID, adopted.ID)
	if err != nil {
		t.Fatalf("place the adopted row: %v", err)
	}
	if placed.ParentID != h.caller.ID || placed.SpawnedBy != h.caller.ID || !placed.Running {
		t.Fatalf("placed = %+v, want a running child of %s", placed, h.caller.ID)
	}

	if _, err := sessions.AnswerAll(stranger.ID, adopted.ID, answer, true, false); err == nil ||
		!strings.Contains(err.Error(), "only the session that spawned a child answers it") {
		t.Fatalf("stranger's answer err = %v, want it refused", err)
	}
	if raw := waitForForeign(t, adopted.TmuxSocket, adopted.TmuxPaneID, lastLine(answerPane)); strings.Contains(raw, "User answered") {
		t.Fatalf("the refused answer reached the pane:\n%s", raw)
	}

	answered, err := sessions.AnswerAll(h.caller.ID, adopted.ID, answer, true, false)
	if err != nil {
		t.Fatalf("answer the placed adopted row: %v", err)
	}
	if answered.Selected != "Every storefront" || !answered.Verified {
		t.Fatalf("answered %+v, want the second option read back", answered)
	}
	waitForForeign(t, adopted.TmuxSocket, adopted.TmuxPaneID, "→ Every storefront")
	rows, err := h.store.AnswersFor(adopted.ID, childCallID)
	if err != nil || len(rows) != 1 || rows[0].Mode != store.AnswerByAgent || rows[0].BySession != h.caller.ID ||
		rows[0].State != store.AnswerKeyed {
		t.Fatalf("ledger = %+v, %v; want one keyed agent row against the saved call", rows, err)
	}
}

// The board answers an adopted pane's dialog as it answers a started one's.
func TestBoardAnswersAnAdoptedRowsDialog(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	sessions, adopted := adoptedAsking(t, h, "borrowed-board")
	answered, err := sessions.BoardAnswer(adopted.ID, "Germany only")
	if err != nil {
		t.Fatalf("BoardAnswer on the adopted row: %v", err)
	}
	if answered.Selected != "Germany only" || !answered.Verified {
		t.Fatalf("answered %+v, want the first option read back", answered)
	}
	waitForForeign(t, adopted.TmuxSocket, adopted.TmuxPaneID, "→ Germany only")
}
