package sessioncmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// The pane a session holding an AskUserQuestion shows to the board.
const boardAskPane = `Which region should the survey cover?

❯ 1. North only
  2. South only

Enter to select · ↑/↓ to navigate · Esc to cancel
`

// The board reads a session it has no relationship to: this child is
// nobody's, which is exactly the one a parent is refused.
func TestBoardReadsAnyAgentSessionsDialog(t *testing.T) {
	h := newSessionHarness(t)
	child := childShowing(t, h, "", "child101", "orphan", boardAskPane)
	read, err := h.sessions.BoardRead(child.ID)
	if err != nil {
		t.Fatalf("BoardRead: %v", err)
	}
	if !read.Live || !strings.Contains(read.Text, "Which region") {
		t.Fatalf("read = %+v, want the live pane", read)
	}
	if !read.HasDialog || read.Dialog.Kind != dialog.KindAsk || len(read.Dialog.Options) != 2 {
		t.Fatalf("dialog = %+v (%v), want the AskUserQuestion on the pane", read.Dialog, read.HasDialog)
	}
	if read.Session.Self {
		t.Fatal("the board is no session, so no row is its own")
	}
}

func TestBoardAnswersASessionNobodySpawned(t *testing.T) {
	h := newSessionHarness(t)
	child := childShowing(t, h, "", "child102", "orphan", boardAskPane)
	if _, err := h.sessions.Answer(h.caller.ID, child.ID, "South only"); err == nil {
		t.Fatal("a session answered a dialog it does not own; the board test below means nothing")
	}
	answered, err := h.sessions.BoardAnswer(child.ID, "South only")
	if err != nil {
		t.Fatalf("BoardAnswer: %v", err)
	}
	if answered.Selected != "South only" {
		t.Fatalf("selected %q, want the second option", answered.Selected)
	}
}

// The board lending its reach is not a person answering: a guarded dialog is
// refused to it as to a parent, and the sentinel says so.
func TestBoardAnswerRefusesTheGuardedDialogs(t *testing.T) {
	h := newSessionHarness(t)
	child := childShowing(t, h, "", "child103", "approval", codexApprovalFixture)
	_, err := h.sessions.BoardAnswer(child.ID, "Yes, proceed")
	if !errors.Is(err, dialog.ErrNotKeyAnswerable) || !strings.Contains(err.Error(), "permission prompt") {
		t.Fatalf("BoardAnswer err = %v, want a named refusal wrapping ErrNotKeyAnswerable", err)
	}
	read, err := h.sessions.BoardRead(child.ID)
	if err != nil {
		t.Fatalf("BoardRead: %v", err)
	}
	if !read.HasDialog || !read.Dialog.Guarded() {
		t.Fatalf("dialog = %+v (%v), want the guarded prompt read so a caller can name it", read.Dialog, read.HasDialog)
	}
}

func TestBoardAnswerSaysWhenThereIsNoDialog(t *testing.T) {
	h := newSessionHarness(t)
	child := childShowing(t, h, "", "child104", "resting", "all done, nothing to ask\n")
	_, err := h.sessions.BoardAnswer(child.ID, "South only")
	if !errors.Is(err, dialog.ErrNoDialog) || !strings.Contains(err.Error(), "not on a dialog") {
		t.Fatalf("BoardAnswer err = %v, want the no-dialog message wrapping ErrNoDialog", err)
	}
}

func TestBoardListHasNoSelf(t *testing.T) {
	h := newSessionHarness(t)
	child := childShowing(t, h, h.caller.ID, "child105", "child", boardAskPane)
	if _, err := h.sessions.BoardList(ListOptions{Parent: SelfParent}); err == nil {
		t.Fatal(`BoardList took "me" as a parent; the board is no session`)
	}
	list, err := h.sessions.BoardList(ListOptions{Parent: h.caller.ID})
	if err != nil {
		t.Fatalf("BoardList: %v", err)
	}
	if len(list.Sessions) != 1 || list.Sessions[0].ID != child.ID || list.Sessions[0].Self {
		t.Fatalf("list = %+v, want the one child and no row marked as the caller's", list.Sessions)
	}
	got, err := h.sessions.BoardGet(h.caller.ID)
	if err != nil || got.Self {
		t.Fatalf("BoardGet = %+v, %v; want the row, not marked as the board's own", got, err)
	}
}

// A board extension's message is queued under the extension, with no
// session to mark read, and held to the checks send_session is.
func TestBoardSendQueuesUnderTheExtension(t *testing.T) {
	h := newSessionHarness(t)
	child := childShowing(t, h, "", "child106", "orphan", "all done\n")
	sent, err := h.sessions.BoardSend("ext1", child.ID, "  carry on with step two  ", "step", false)
	if err != nil {
		t.Fatalf("BoardSend: %v", err)
	}
	if sent.MessageID == 0 || sent.QueuePosition != 1 {
		t.Fatalf("sent = %+v, want one queued message", sent)
	}
	heads, err := h.store.HeadMessages()
	if err != nil {
		t.Fatal(err)
	}
	head := heads[child.ID]
	if head.SenderID != store.ExtensionSenderID("ext1") || head.SenderName != "ext1" || head.Body != "carry on with step two" {
		t.Fatalf("queued = %+v, want the trimmed text under the extension's sender", head)
	}
	again, err := h.sessions.BoardSend("ext1", child.ID, "carry on with step three", "step", false)
	if err != nil || again.Superseded != 1 {
		t.Fatalf("second send on the subject = %+v, %v; want it to replace the first", again, err)
	}
}

func TestBoardSendKeepsSendSessionsChecks(t *testing.T) {
	h := newSessionHarness(t)
	child := childShowing(t, h, "", "child107", "orphan", "all done\n")
	cases := map[string]func() error{
		"message is empty": func() error {
			_, err := h.sessions.BoardSend("ext1", child.ID, " ", "", false)
			return err
		},
		"byte limit": func() error {
			_, err := h.sessions.BoardSend("ext1", child.ID, strings.Repeat("x", maxMessageBytes+1), "", false)
			return err
		},
		"needs the extension": func() error {
			_, err := h.sessions.BoardSend("", child.ID, "hello", "", false)
			return err
		},
		"does not exist": func() error {
			_, err := h.sessions.BoardSend("ext1", "feedf00d", "hello", "", false)
			return err
		},
	}
	for want, send := range cases {
		if err := send(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %q", err, want)
		}
	}
	if _, err := h.sessions.BoardKill(child.ID); err != nil {
		t.Fatalf("BoardKill: %v", err)
	}
	if _, err := h.sessions.BoardSend("ext1", child.ID, "hello", "", false); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("send to a killed session = %v, want it refused as not running", err)
	}
}

// The board kills a session nobody lets it own, the way the operator can;
// the row stays, dead, with its last screen.
func TestBoardKillEndsAnySessionAndKeepsItsRow(t *testing.T) {
	h := newSessionHarness(t)
	child := childShowing(t, h, "", "child108", "orphan", "last words\n")
	killed, err := h.sessions.BoardKill(child.ID)
	if err != nil {
		t.Fatalf("BoardKill: %v", err)
	}
	if killed.Status != status.Dead || killed.Running {
		t.Fatalf("killed = %+v, want a dead row", killed)
	}
	if h.driver.Exists(child.ID) {
		t.Fatal("the pane outlived the kill")
	}
	stored, err := h.store.Get(child.ID)
	if err != nil || stored.Status != status.Dead {
		t.Fatalf("stored = %+v, %v; want the row kept, dead", stored, err)
	}
}
