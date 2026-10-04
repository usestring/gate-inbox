package keymap

// The catalog is the defaults: every action the manager answers, the keys it
// answers on out of the box, and what to call it. It is the only place a key
// is written as a letter, and the order here is the order the key map screen
// reads in, so it is grouped the way the keys are learned rather than
// alphabetically.
//
// Adding a binding means adding a line here and one case in the handler that
// answers the action. Nothing else has to be told: the footer, the key map
// and the rebind screen all read this.

// List actions.
const (
	CursorUp        Action = "cursor_up"
	CursorDown      Action = "cursor_down"
	CursorTop       Action = "cursor_top"
	CursorBottom    Action = "cursor_bottom"
	Open            Action = "open"
	StepIn          Action = "step_in"
	StepOut         Action = "step_out"
	LastPane        Action = "last_pane"
	JumpPane        Action = "jump_pane"
	JumpAttention   Action = "jump_attention"
	JumpWaiting     Action = "jump_waiting"
	JumpFinished    Action = "jump_finished"
	JumpErrored     Action = "jump_errored"
	JumpIdle        Action = "jump_idle"
	JumpWorking     Action = "jump_working"
	Attach          Action = "attach"
	ReorderUp       Action = "reorder_up"
	ReorderDown     Action = "reorder_down"
	PreviewUp       Action = "preview_scroll_up"
	PreviewDown     Action = "preview_scroll_down"
	PreviewPageUp   Action = "preview_page_up"
	PreviewPageDown Action = "preview_page_down"
	PreviewTop      Action = "preview_top"
	PreviewBottom   Action = "preview_bottom"

	NewSession     Action = "new_session"
	NewSessionForm Action = "new_session_form"
	NewGroup       Action = "new_group"
	NewTerminal    Action = "new_terminal"
	Fork           Action = "fork"
	Migrate        Action = "migrate"
	TakeOver       Action = "take_over"

	Revive        Action = "revive"
	ReviveAll     Action = "revive_all"
	SwitchAccount Action = "switch_account"
	Restart       Action = "restart"
	RestartWith   Action = "restart_with"
	Archive       Action = "archive"
	ArchiveAll    Action = "archive_all"
	Restore       Action = "restore"
	Dismiss       Action = "dismiss"
	Priority      Action = "priority"
	Mute          Action = "mute"
	CopySessionID Action = "copy_session_id"
	HandOver      Action = "hand_over"
	QuickInput    Action = "quick_prompt"
	RenameSelf    Action = "rename_from_agent"
	Rename        Action = "rename"
	NameSweep     Action = "name_sweep"
	Move          Action = "move"

	ShowAllWork  Action = "show_all_work"
	StatusFilter Action = "status_filter"
	ToolFilter   Action = "tool_filter"
	Triage       Action = "triage"
	EmptyGroups  Action = "empty_groups"
	FoldAll      Action = "fold_all"
	ArchivedView Action = "archived_view"
	Search       Action = "search"
	ClearSearch  Action = "clear_search"
	Resize       Action = "resize_split"
	Settings     Action = "settings"
	LegendPeek   Action = "legend_peek"
	QuickActions Action = "quick_actions"
	Help         Action = "help"
	Quit         Action = "quit"
)

// Focus actions.
const (
	Leave        Action = "leave"
	LeaveHard    Action = "leave_stop"
	BackAtPrompt Action = "leave_at_prompt"
)

// Actions shared by more than one screen keep one name: closing a screen is
// Close wherever the screen is, and a cursor step is CursorUp on all of
// them. The context is what separates them.
const (
	Close    Action = "close"
	Refresh  Action = "refresh"
	Rescind  Action = "rescind_submission"
	Fold     Action = "fold"
	Confirm  Action = "confirm"
	Cancel   Action = "cancel"
	Toggle   Action = "toggle"
	PageUp   Action = "page_up"
	PageDown Action = "page_down"
	Top      Action = "top"
	Bottom   Action = "bottom"
	More     Action = "more"
	TickAll  Action = "tick_all"
	// ToggleChrome hides the footer and brings it back. It is the chrome
	// setting's "never" under a key, for an operator who wants the rows
	// for a moment rather than for good.
	ToggleChrome       Action = "toggle_chrome"
	ToggleConversation Action = "toggle_conversation"
	// ToggleRail hides the list beside the pane and brings it back. It is
	// the layout setting's "board" under a key, the columns answer to
	// ToggleChrome's rows.
	ToggleRail Action = "toggle_rail"
)

var Catalog = []Binding{
	// ---- the list ----
	{ContextList, CursorUp, []string{"up", "k"}, "move the cursor up", true},
	{ContextList, CursorDown, []string{"down", "j"}, "move the cursor down", true},
	// Rarely-run actions ship unbound: they stay runnable from quick
	// actions (:) and rebindable from the key map (h), but they hold no
	// key by default. A key nobody presses often is a key hit by accident.
	{ContextList, CursorTop, []string{}, "jump to the top of the list", false},
	{ContextList, CursorBottom, []string{}, "jump to the bottom", false},
	{ContextList, Open, []string{"enter"}, "focus the session, or fold the group", true},
	{ContextList, StepIn, []string{"right"}, "step in: a session's work, then focus it", false},
	{ContextList, StepOut, []string{"left"}, "step out: fold the work, close the group", false},
	{ContextList, LastPane, []string{"l"}, "back to the previous pane; again walks further back", false},
	{ContextList, JumpPane, []string{"ctrl+g"}, "find a pane by name and focus it", false},
	{ContextList, Rescind, []string{"ctrl+z"}, "undo the latest submission while its turn is active", false},
	{ContextList, JumpAttention, []string{"tab"}, "enter the next session waiting on you", false},
	{ContextList, JumpWaiting, []string{}, "enter the next waiting session", false},
	{ContextList, JumpFinished, []string{}, "enter the next finished session", false},
	{ContextList, JumpErrored, []string{}, "enter the next errored or dead session", false},
	{ContextList, JumpIdle, []string{}, "enter the next idle session", false},
	{ContextList, JumpWorking, []string{}, "enter the next working session", false},
	{ContextList, Attach, []string{"A", "shift+a"}, "attach: the pane takes the terminal", false},
	{ContextList, ReorderUp, []string{"K", "shift+k", "shift+up"}, "reorder the row up", false},
	{ContextList, ReorderDown, []string{"J", "shift+j", "shift+down"}, "reorder the row down", false},
	{ContextList, PreviewUp, []string{"alt+up"}, "scroll the preview up", false},
	{ContextList, PreviewDown, []string{"alt+down"}, "scroll the preview down", false},
	{ContextList, PreviewPageUp, []string{}, "scroll the preview a page up", false},
	{ContextList, PreviewPageDown, []string{}, "scroll the preview a page down", false},
	{ContextList, PreviewTop, []string{}, "scroll the preview to its oldest", false},
	{ContextList, PreviewBottom, []string{}, "scroll the preview back to live", false},

	{ContextList, NewSession, []string{"n"}, "new session: asks which agent, unless settings names one", false},
	{ContextList, NewSessionForm, []string{"ctrl+n"}, "new session, asking name, CLI, directory, task", false},
	{ContextList, NewGroup, []string{"g"}, "new group", false},
	{ContextList, NewTerminal, []string{"T", "shift+t"}, "new terminal tab", false},
	{ContextList, Fork, []string{}, "fork the session into a new one", false},
	{ContextList, Migrate, []string{}, "migrate it to another CLI", false},

	{ContextList, Revive, []string{"v"}, "revive it, or restart a live one on its own conversation", false},
	{ContextList, ReviveAll, []string{"V", "shift+v"}, "revive every dead session", false},
	{ContextList, SwitchAccount, []string{}, "switch its account: restarts it on its own conversation", false},
	{ContextList, Restart, []string{}, "restart it on an empty context", false},
	{ContextList, RestartWith, []string{}, "restart it with extra flags…", false},
	{ContextList, Archive, []string{"x"}, "kill it and file the row", false},
	{ContextList, ArchiveAll, []string{}, "kill every session listed", false},
	{ContextList, Restore, []string{"u"}, "restore it out of the archive", false},
	{ContextList, Dismiss, []string{"."}, "skip", false},
	{ContextList, Priority, []string{"p"}, "cycle priority: urgent → high → medium → low → none", false},
	{ContextList, Mute, []string{"M", "shift+m"}, "mute / unmute: keep it out of triage until unmuted", false},
	{ContextList, QuickInput, []string{" ", "space", "ctrl+]"}, "hotkey menu", false},
	{ContextList, RenameSelf, []string{"r"}, "rename it after its conversation", false},
	{ContextList, Rename, []string{}, "rename it yourself, and re-pick its tool", false},
	{ContextList, NameSweep, []string{}, "name sweep over idle adopted panes", false},
	{ContextList, TakeOver, []string{}, "take over the adopted panes now; busy ones once they go idle", false},
	{ContextList, Move, []string{"m"}, "move it to a group", false},

	{ContextList, ShowAllWork, []string{}, "show every pull request and ticket, not the first few", false},
	{ContextList, StatusFilter, []string{"w"}, "filter to what needs attention", false},
	{ContextList, ToolFilter, []string{"Y"}, "filter to one CLI, or show all", false},
	{ContextList, Triage, []string{"i"}, "triage this group as one queue", false},
	{ContextList, ToggleConversation, []string{"f3"}, "show full / shortened conversation", false},
	{ContextList, EmptyGroups, []string{}, "hide / show empty groups", false},
	{ContextList, FoldAll, []string{}, "fold / unfold everything", false},
	// No key: the archive is where killed rows wait out their 7-day
	// retention, visited deliberately rather than toggled past. Quick
	// actions (:) opens it, and u restores from it.
	{ContextList, ArchivedView, []string{}, "archived view", false},
	{ContextList, Search, []string{"/"}, "fuzzy session search", false},
	// No key: esc closes the search field and keeps the filter; the text
	// itself is cleared by deleting it (ctrl+u wipes the field). A
	// filter with no visible field used to read as sessions gone missing.
	{ContextList, ClearSearch, []string{}, "clear the search", false},
	{ContextList, Resize, []string{}, "resize the split", false},
	{ContextList, Settings, []string{"s"}, "settings", false},
	{ContextList, ToggleChrome, []string{}, "hide / show the key hints along the foot", false},
	{ContextList, ToggleRail, []string{}, "hide / show the list beside the pane", false},
	{ContextList, LegendPeek, []string{"?"}, "peek at every available key", false},
	{ContextList, QuickActions, []string{":", "ctrl+p"}, "quick actions: find any action by name, run it, see its key", false},
	{ContextList, Help, []string{"h", "H", "shift+h", "ctrl+h"}, "this key map", true},
	{ContextList, Quit, []string{"q"}, "quit (sessions keep running)", true},

	// ---- a focused session ----
	// Every key this screen does not claim is typed into the agent, so a
	// binding added here is a key taken away from the pane. That is the
	// reason the defaults are all chords.
	{ContextFocus, Leave, []string{"ctrl+q"}, "back to the manager; in triage, on to the next", true},
	{ContextFocus, HandOver, []string{"§"}, "ctrl+q's one-press alias", false},
	{ContextFocus, LeaveHard, []string{`ctrl+\`}, "back to the manager, always stopping there", true},
	{ContextFocus, Rescind, []string{"ctrl+z"}, "undo the latest submission while its turn is active", false},
	// A chord for the same reason alt+, is one: a plain "." is a character
	// the agent was owed. It mirrors the list's own dismiss key.
	{ContextFocus, Dismiss, []string{"alt+."}, "skip", false},
	// The focused-view setting under a key, and the same list binding the
	// operator already has: a session kept on its conversation with the
	// setting toggles back to the terminal here. A plain letter is a
	// character the agent was owed, so it stays F3, the key the list uses.
	{ContextFocus, ToggleConversation, []string{"f3"}, "show the conversation or the terminal", false},
	// A chord, like every other key this screen claims: a plain comma is a
	// character the agent was owed.
	{ContextFocus, ToggleChrome, []string{"alt+,"}, "hide / show the key hints along the foot", false},
	{ContextFocus, ToggleRail, []string{`alt+\`}, "hide / show the list beside the pane", false},
	{ContextFocus, Archive, []string{"ctrl+x"}, "kill it, asking first", false},
	// A chord, like every other key this screen claims: ctrl+h would otherwise
	// reach the agent as readline's backward-delete, and backspace still sends
	// that edit under its own name.
	{ContextFocus, Help, []string{"ctrl+h"}, "this key map", false},
	// The hotkey menu from inside the pane: the same snippet menu the list
	// opens, answering the focused session. One more claimed ctrl key, and
	// ctrl+] is the cheapest one left: no agent CLI binds it, and readline
	// only spends it on character search. It is rebindable like every other
	// action for an operator who misses it.
	{ContextFocus, QuickInput, []string{"ctrl+]"}, "hotkey menu: send a snippet without leaving the session", false},
	// Session controls reached from inside the pane: a drain that had to leave
	// the queue to spawn, copy an id or step back was not one queue. See the
	// ui package's focusactions.go.
	{ContextFocus, NewSession, []string{"alt+n"}, "new session in this one's group", false},
	{ContextFocus, CopySessionID, []string{"alt+y"}, "copy the agent's session id", false},
	{ContextFocus, LastPane, []string{"alt+l"}, "back to the previous pane; again walks further back", false},
	{ContextFocus, JumpPane, []string{"ctrl+g"}, "find a pane by name and focus it", false},
	{ContextFocus, BackAtPrompt, []string{"right", "left"}, "at a prompt edge, leave; in triage Right advances a finished one", false},
	{ContextFocus, PreviewUp, []string{"alt+up"}, "scroll the pane up", false},
	{ContextFocus, PreviewDown, []string{"alt+down"}, "scroll the pane down", false},
	{ContextFocus, PreviewPageUp, []string{"alt+u", "alt+pgup"}, "scroll a page up", false},
	{ContextFocus, PreviewPageDown, []string{"alt+d", "alt+pgdown"}, "scroll a page down", false},
	{ContextFocus, PreviewTop, []string{"alt+home"}, "oldest history", false},
	{ContextFocus, PreviewBottom, []string{"alt+end"}, "back to the live bottom", false},

	// ---- the name sweep ----
	{ContextNameSweep, Cancel, []string{"esc", "n", "q"}, "cancel the sweep", true},
	{ContextNameSweep, CursorUp, []string{"up", "k"}, "move up", true},
	{ContextNameSweep, CursorDown, []string{"down", "j"}, "move down", true},
	{ContextNameSweep, PageUp, []string{"ctrl+u", "pgup"}, "a page up", false},
	{ContextNameSweep, PageDown, []string{"ctrl+d", "pgdown"}, "a page down", false},
	{ContextNameSweep, Confirm, []string{"y", "enter"}, "run the sweep", true},

	// ---- the welcome card ----
	{ContextWelcome, Close, []string{"enter", "esc", "q", " ", "space"}, "close the guide", true},
	{ContextWelcome, Help, []string{"h", "H", "?", "shift+h", "ctrl+h"}, "the key map", false},
	{ContextWelcome, CursorUp, []string{"up", "k"}, "move up", true},
	{ContextWelcome, CursorDown, []string{"down", "j"}, "move down", true},
	{ContextWelcome, PageUp, []string{"ctrl+u", "pgup"}, "a page up", false},
	{ContextWelcome, PageDown, []string{"ctrl+d", "pgdown"}, "a page down", false},
	{ContextWelcome, Top, []string{"g", "home"}, "the top", false},
	{ContextWelcome, Bottom, []string{"G", "end"}, "the bottom", false},

	// ---- a confirmation ----
	{ContextConfirm, Toggle, []string{" ", "space"}, "answer the tick", false},
	{ContextConfirm, Confirm, []string{"y", "enter"}, "go ahead", true},
}
