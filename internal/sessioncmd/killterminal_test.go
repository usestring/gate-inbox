package sessioncmd

import (
	"slices"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/status"
)

// An extension ending a session's work lists the terminals it opened and
// ends them with it; kill_session itself still sends a terminal to the
// terminal tools.
func TestKillOrEndTerminalEndsATerminalTheCallerCouldClose(t *testing.T) {
	h := newSessionHarness(t)
	watcher := &launchWatcher{}
	useWatcher(t, watcher)
	shell, err := h.terminals.Create(h.caller.ID, CreateTerminalOptions{})
	if err != nil {
		t.Fatalf("create terminal: %v", err)
	}
	plain, err := h.sessions.List(h.caller.ID, ListOptions{Parent: SelfParent})
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(plain.Sessions, func(s Session) bool { return s.ID == shell.ID }) {
		t.Fatalf("a plain list returned the terminal: %+v", plain.Sessions)
	}
	listed, err := h.sessions.List(h.caller.ID, ListOptions{Parent: SelfParent, IncludeTerminals: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Sessions) != 1 || listed.Sessions[0].ID != shell.ID || !listed.Sessions[0].Terminal || !listed.Sessions[0].Running {
		t.Fatalf("listed = %+v, want the running terminal, marked", listed.Sessions)
	}

	if _, err := h.sessions.Kill(h.caller.ID, shell.ID, extension.KillByMCP); err == nil || !strings.Contains(err.Error(), "is a terminal") {
		t.Fatalf("Kill = %v, want the terminal refused", err)
	}
	killed, err := h.sessions.KillOrEndTerminal(h.caller.ID, shell.ID, extension.KillByExtension)
	if err != nil {
		t.Fatalf("KillOrEndTerminal: %v", err)
	}
	if !killed.Terminal || killed.Status != status.Dead || killed.Running {
		t.Fatalf("killed = %+v, want a dead terminal", killed)
	}
	if got := watcher.kills(); len(got) != 0 {
		t.Fatalf("a terminal's end was told to the kill observers: %+v", got)
	}
	if h.driver.Exists(shell.ID) {
		t.Fatal("the terminal's pane outlived the kill")
	}
	if stored, err := h.store.Get(shell.ID); err != nil || stored.Status != status.Dead {
		t.Fatalf("stored = %+v, %v; want the row kept, dead", stored, err)
	}
}

// Terminals listed beside agents are paged like them: every row is read
// exactly once across the pages, a cursor taken on a terminal resumes after
// it, and the pages of a plain list still leave the terminals out.
func TestListPagesTerminalsLikeOtherRows(t *testing.T) {
	h := newSessionHarness(t)
	for _, name := range []string{"one", "two"} {
		if _, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: name}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := h.terminals.Create(h.caller.ID, CreateTerminalOptions{}); err != nil {
			t.Fatalf("create terminal: %v", err)
		}
	}
	read := func(opts ListOptions) (all []Session, pages int) {
		t.Helper()
		for {
			page, err := h.sessions.List(h.caller.ID, opts)
			if err != nil {
				t.Fatal(err)
			}
			all, pages = append(all, page.Sessions...), pages+1
			if page.Cursor == "" {
				return all, pages
			}
			opts.After = page.Cursor
		}
	}
	whole, err := h.sessions.List(h.caller.ID, ListOptions{Parent: SelfParent, IncludeTerminals: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(whole.Sessions) != 4 {
		t.Fatalf("whole list = %+v, want two agents and two terminals", whole.Sessions)
	}
	paged, pages := read(ListOptions{Parent: SelfParent, IncludeTerminals: true, Limit: 1})
	if pages != 4 || !slices.EqualFunc(paged, whole.Sessions, func(a, b Session) bool { return a.ID == b.ID }) {
		t.Fatalf("paged one at a time over %d pages: %+v, want the whole list in order: %+v", pages, paged, whole.Sessions)
	}
	plain, _ := read(ListOptions{Parent: SelfParent, Limit: 1})
	if len(plain) != 2 || slices.ContainsFunc(plain, func(s Session) bool { return s.Terminal }) {
		t.Fatalf("plain pages = %+v, want the two agents only", plain)
	}
}

// The reach is Kill's over the terminal's session: a caller outside the tree
// ends a terminal under a session it could kill, and leaves one nested under
// nobody, or under a session that is gone, running.
func TestKillOrEndTerminalReachesTerminalsOfSessionsItCouldKill(t *testing.T) {
	h := newSessionHarness(t)
	other := childShowing(t, h, "", "c0ffee01", "other", "busy\n")
	theirs, err := h.terminals.Create(other.ID, CreateTerminalOptions{})
	if err != nil {
		t.Fatalf("create their terminal: %v", err)
	}
	gone := childShowing(t, h, "", "c0ffee02", "gone", "busy\n")
	orphan, err := h.terminals.Create(gone.ID, CreateTerminalOptions{})
	if err != nil {
		t.Fatalf("create the orphan's terminal: %v", err)
	}
	if err := h.store.Delete(gone.ID); err != nil {
		t.Fatal(err)
	}
	nest := false
	loose, err := h.terminals.Create(h.caller.ID, CreateTerminalOptions{Nest: &nest})
	if err != nil {
		t.Fatalf("create un-nested terminal: %v", err)
	}
	for _, id := range []string{orphan.ID, loose.ID} {
		if _, err := h.sessions.KillOrEndTerminal(h.caller.ID, id, extension.KillByExtension); err == nil || !strings.Contains(err.Error(), "nested under no agent session") {
			t.Errorf("KillOrEndTerminal(%s) = %v, want it refused", id, err)
		}
		if !h.driver.Exists(id) {
			t.Errorf("terminal %s was ended", id)
		}
	}
	if _, err := h.sessions.KillOrEndTerminal(h.caller.ID, theirs.ID, extension.KillByExtension); err != nil {
		t.Fatalf("KillOrEndTerminal on another session's terminal: %v", err)
	}
	if h.driver.Exists(theirs.ID) {
		t.Fatal("the terminal under a killable session outlived the kill")
	}
	// Its session is then killed as Kill kills it, children first.
	if _, err := h.sessions.KillOrEndTerminal(h.caller.ID, other.ID, extension.KillByExtension); err != nil {
		t.Fatalf("KillOrEndTerminal on an agent: %v", err)
	}
	if _, err := h.sessions.KillOrEndTerminal(h.caller.ID, h.caller.ID, extension.KillByExtension); err == nil {
		t.Fatal("a session killed itself")
	}
}

// The board reaches every terminal some session could close, and not the
// operator's own.
func TestBoardKillEndsATerminalNestedUnderAnAgent(t *testing.T) {
	h := newSessionHarness(t)
	shell, err := h.terminals.Create(h.caller.ID, CreateTerminalOptions{})
	if err != nil {
		t.Fatalf("create terminal: %v", err)
	}
	nest := false
	loose, err := h.terminals.Create(h.caller.ID, CreateTerminalOptions{Nest: &nest})
	if err != nil {
		t.Fatalf("create un-nested terminal: %v", err)
	}
	listed, err := h.sessions.BoardList(ListOptions{Parent: h.caller.ID, IncludeTerminals: true})
	if err != nil || len(listed.Sessions) != 1 || listed.Sessions[0].ID != shell.ID {
		t.Fatalf("BoardList = %+v, %v; want the nested terminal", listed.Sessions, err)
	}
	killed, err := h.sessions.BoardKill(shell.ID)
	if err != nil {
		t.Fatalf("BoardKill: %v", err)
	}
	if !killed.Terminal || killed.Status != status.Dead || h.driver.Exists(shell.ID) {
		t.Fatalf("killed = %+v, want the terminal ended", killed)
	}
	if _, err := h.sessions.BoardKill(loose.ID); err == nil || !strings.Contains(err.Error(), "nested under no agent session") {
		t.Fatalf("BoardKill on the operator's terminal = %v, want it refused", err)
	}
	if !h.driver.Exists(loose.ID) {
		t.Fatal("the operator's terminal was ended")
	}
}
