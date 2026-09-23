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
	CursorUp          Action = "cursor_up"
	CursorDown        Action = "cursor_down"
	CursorTop         Action = "cursor_top"
	CursorBottom      Action = "cursor_bottom"
	Open              Action = "open"
	StepIn            Action = "step_in"
	StepOut           Action = "step_out"
	LastPane          Action = "last_pane"
	JumpAttention     Action = "jump_attention"
	JumpAttentionBack Action = "jump_attention_back"
	JumpWaiting       Action = "jump_waiting"
	JumpFinished      Action = "jump_finished"
	JumpErrored       Action = "jump_errored"
	JumpIdle          Action = "jump_idle"
	JumpWorking       Action = "jump_working"
	Attach            Action = "attach"
	ReorderUp         Action = "reorder_up"
	ReorderDown       Action = "reorder_down"
	PreviewUp         Action = "preview_scroll_up"
	PreviewDown       Action = "preview_scroll_down"
	PreviewPageUp     Action = "preview_page_up"
	PreviewPageDown   Action = "preview_page_down"
	PreviewTop        Action = "preview_top"
	PreviewBottom     Action = "preview_bottom"

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
	Archive       Action = "archive"
	ArchiveAll    Action = "archive_all"
	Restore       Action = "restore"
	Dismiss       Action = "dismiss"
	Priority      Action = "priority"
	CopySessionID Action = "copy_session_id"
	HandOver      Action = "hand_over"
	QuickInput    Action = "quick_prompt"
	RenameSelf    Action = "rename_from_agent"
	Rename        Action = "rename"
	NameSweep     Action = "name_sweep"
	Move          Action = "move"
	Editor        Action = "editor"

	ShowAllWork  Action = "show_all_work"
	StatusFilter Action = "status_filter"
	Triage       Action = "triage"
	// Gate is triage, the hands-free handover and the full width armed as
	// one mode and put back as one: a queue drained a session at a time.
	// See the ui package's gate.go.
	Gate         Action = "gate"
	EmptyGroups  Action = "empty_groups"
	FoldAll      Action = "fold_all"
	ArchivedView Action = "archived_view"
	Search       Action = "search"
	ClearSearch  Action = "clear_search"
	Resize       Action = "resize_split"
	Settings     Action = "settings"
	LegendPeek   Action = "legend_peek"
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
	ToggleGateInput    Action = "toggle_gate_input"
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
	{ContextList, CursorTop, []string{"home"}, "jump to the top of the list", false},
	{ContextList, CursorBottom, []string{"end"}, "jump to the bottom", false},
	{ContextList, Open, []string{"enter"}, "focus the session, or fold the group", true},
	{ContextList, StepIn, []string{"right"}, "step in: a session's work, then focus it", false},
	{ContextList, StepOut, []string{"left"}, "step out: fold the work, close the group", false},
	{ContextList, LastPane, []string{"l"}, "focus the session you were on before this one; l again swaps back", false},
	{ContextList, Rescind, []string{"ctrl+z"}, "rescind the latest submission while its turn is active", false},
	{ContextList, JumpAttention, []string{"tab"}, "enter the next session waiting on you", false},
	{ContextList, JumpAttentionBack, []string{"shift+tab"}, "enter the one before it", false},
	{ContextList, JumpWaiting, []string{"alt+w"}, "enter the next waiting session", false},
	{ContextList, JumpFinished, []string{"alt+f"}, "enter the next finished session", false},
	{ContextList, JumpErrored, []string{"alt+e"}, "enter the next errored or dead session", false},
	{ContextList, JumpIdle, []string{"alt+i"}, "enter the next idle session", false},
	{ContextList, JumpWorking, []string{"alt+k"}, "enter the next working session", false},
	{ContextList, Attach, []string{"A", "shift+a"}, "attach: the pane takes the terminal", false},
	{ContextList, ReorderUp, []string{"K", "shift+k", "shift+up"}, "reorder the row up", false},
	{ContextList, ReorderDown, []string{"J", "shift+j", "shift+down"}, "reorder the row down", false},
	{ContextList, PreviewUp, []string{"alt+up"}, "scroll the preview up", false},
	{ContextList, PreviewDown, []string{"alt+down"}, "scroll the preview down", false},
	{ContextList, PreviewPageUp, []string{"alt+pgup"}, "scroll the preview a page up", false},
	{ContextList, PreviewPageDown, []string{"alt+pgdown"}, "scroll the preview a page down", false},
	{ContextList, PreviewTop, []string{"alt+home"}, "scroll the preview to its oldest", false},
	{ContextList, PreviewBottom, []string{"alt+end"}, "scroll the preview back to live", false},

	{ContextList, NewSession, []string{"n"}, "new session: asks which agent, unless settings names one", false},
	{ContextList, NewSessionForm, []string{"ctrl+n"}, "new session, asking name, CLI, directory, task", false},
	{ContextList, NewGroup, []string{"g"}, "new group", false},
	{ContextList, NewTerminal, []string{"T", "shift+t"}, "new terminal tab", false},
	{ContextList, Fork, []string{"f"}, "fork the session into a new one", false},
	{ContextList, Migrate, []string{"M", "shift+m"}, "migrate it to another CLI", false},

	{ContextList, Revive, []string{"v"}, "revive it, or restart a live one on its own conversation", false},
	{ContextList, ReviveAll, []string{"V", "shift+v"}, "revive every dead session", false},
	{ContextList, SwitchAccount, []string{"a"}, "switch its account: restarts it on its own conversation", false},
	{ContextList, Restart, []string{"R", "shift+r"}, "restart it on an empty context", false},
	{ContextList, Archive, []string{"x"}, "end it and file the row", false},
	{ContextList, ArchiveAll, []string{"X", "shift+x"}, "end every session listed", false},
	{ContextList, Restore, []string{"u"}, "restore it out of the archive", false},
	{ContextList, Dismiss, []string{"."}, "dismiss: mark it idle, or mute it", false},
	{ContextList, Priority, []string{"p"}, "priority: it goes first in triage", false},
	{ContextList, HandOver, []string{"§"}, "in triage: mute this and enter the next", false},
	{ContextList, QuickInput, []string{" ", "space"}, "quick prompt", false},
	{ContextList, RenameSelf, []string{"r"}, "rename it after its conversation", false},
	{ContextList, Rename, []string{"alt+r"}, "rename it yourself, and re-pick its tool", false},
	{ContextList, NameSweep, []string{"N", "shift+n"}, "name sweep over idle adopted panes", false},
	{ContextList, TakeOver, []string{"O", "shift+o"}, "take over adopted panes: restart each as a managed session once idle", false},
	{ContextList, Move, []string{"m"}, "move it to a group", false},
	{ContextList, Editor, []string{"o"}, "open its directory in your editor", false},

	{ContextList, ShowAllWork, []string{"W", "shift+w"}, "show every pull request and ticket, not the first few", false},
	{ContextList, StatusFilter, []string{"w"}, "filter to what needs attention", false},
	{ContextList, Triage, []string{"i"}, "triage this group as one queue", false},
	{ContextList, ToggleConversation, []string{"f3"}, "show full / shortened conversation", false},
	{ContextList, Gate, []string{"G", "shift+g"}, "gate: drain that queue one session at a time", false},
	{ContextList, EmptyGroups, []string{"e"}, "hide / show empty groups", false},
	{ContextList, FoldAll, []string{"F", "shift+f"}, "fold / unfold everything", false},
	{ContextList, ArchivedView, []string{"t"}, "archived view", false},
	{ContextList, Search, []string{"/"}, "search the list by name", false},
	{ContextList, ClearSearch, []string{"esc"}, "clear the search", false},
	{ContextList, Resize, []string{"|"}, "resize the split", false},
	{ContextList, Settings, []string{"s"}, "settings", false},
	{ContextList, ToggleChrome, []string{","}, "hide / show the key hints along the foot", false},
	{ContextList, ToggleRail, []string{`\`}, "hide / show the list beside the pane", false},
	{ContextList, LegendPeek, []string{"?"}, "peek at every available key", false},
	{ContextList, Help, []string{"H", "shift+h"}, "this key map", true},
	{ContextList, Quit, []string{"q"}, "quit (sessions keep running)", true},

	// ---- a focused session ----
	// Every key this screen does not claim is typed into the agent, so a
	// binding added here is a key taken away from the pane. That is the
	// reason the defaults are all chords.
	{ContextFocus, Leave, []string{"ctrl+q"}, "back to the manager; in triage, on to the next", true},
	{ContextFocus, ToggleConversation, []string{"f3"}, "show full / shortened conversation", false},
	{ContextFocus, ToggleGateInput, []string{"f2"}, "gate: switch conversation / terminal", false},
	{ContextFocus, HandOver, []string{"§"}, "ctrl+q's one-press alias", false},
	{ContextFocus, LeaveHard, []string{`ctrl+\`}, "back to the manager, always stopping there", true},
	{ContextFocus, Rescind, []string{"ctrl+z"}, "rescind the latest submission while its turn is active", false},
	{ContextFocus, Editor, []string{"alt+o"}, "open its directory in an editor", false},
	// A chord for the same reason alt+, is one: a plain "." is a character
	// the agent was owed. It mirrors the list's own dismiss key.
	{ContextFocus, Dismiss, []string{"alt+."}, "dismiss this one and go on to the next", false},
	// A chord, like every other key this screen claims: a plain comma is a
	// character the agent was owed.
	{ContextFocus, ToggleChrome, []string{"alt+,"}, "hide / show the key hints along the foot", false},
	{ContextFocus, ToggleRail, []string{`alt+\`}, "hide / show the list beside the pane", false},
	{ContextFocus, Archive, []string{"ctrl+x"}, "end it, asking first", false},
	// The gate's own session controls, reached from inside it: v1's gate view
	// offered the same rows on its menu, and a drain that had to leave the
	// queue to spawn, copy an id or step back was not one queue.
	{ContextFocus, NewSession, []string{"alt+n"}, "new session in this one's group, keeping your place", false},
	{ContextFocus, CopySessionID, []string{"alt+y"}, "copy the agent's session id", false},
	{ContextFocus, LastPane, []string{"alt+l"}, "back to the previous session; alt+l again swaps back", false},
	{ContextFocus, BackAtPrompt, []string{"left"}, "at the prompt's start, back to the manager", false},
	{ContextFocus, PreviewUp, []string{"alt+up"}, "scroll the pane up", false},
	{ContextFocus, PreviewDown, []string{"alt+down"}, "scroll the pane down", false},
	{ContextFocus, PreviewPageUp, []string{"alt+pgup"}, "scroll a page up", false},
	{ContextFocus, PreviewPageDown, []string{"alt+pgdown"}, "scroll a page down", false},
	{ContextFocus, PreviewTop, []string{"alt+home"}, "oldest history", false},
	{ContextFocus, PreviewBottom, []string{"alt+end"}, "back to the live bottom", false},

	// ---- the name sweep ----
	{ContextNameSweep, Cancel, []string{"esc", "n", "q"}, "cancel the sweep", true},
	{ContextNameSweep, CursorUp, []string{"up", "k"}, "move up", true},
	{ContextNameSweep, CursorDown, []string{"down", "j"}, "move down", true},
	{ContextNameSweep, PageUp, []string{"pgup", "ctrl+u"}, "a page up", false},
	{ContextNameSweep, PageDown, []string{"pgdown", "ctrl+d"}, "a page down", false},
	{ContextNameSweep, Confirm, []string{"y", "enter"}, "run the sweep", true},

	// ---- the restore prompt ----
	{ContextRestore, Cancel, []string{"esc", "n", "q"}, "leave the panes as they are", true},
	{ContextRestore, More, []string{"c"}, "choose which ones", false},
	{ContextRestore, CursorUp, []string{"up", "k"}, "move up", true},
	{ContextRestore, CursorDown, []string{"down", "j"}, "move down", true},
	{ContextRestore, Toggle, []string{" ", "space"}, "tick / untick the row", false},
	{ContextRestore, TickAll, []string{"a"}, "tick every row, or start over", false},
	{ContextRestore, Confirm, []string{"y", "enter"}, "restore the ticked panes", true},

	// ---- the welcome card ----
	{ContextWelcome, Close, []string{"enter", "esc", "q", " ", "space"}, "close the guide", true},
	{ContextWelcome, Help, []string{"H", "?", "shift+h"}, "the key map", false},
	{ContextWelcome, CursorUp, []string{"up", "k"}, "move up", true},
	{ContextWelcome, CursorDown, []string{"down", "j"}, "move down", true},
	{ContextWelcome, PageUp, []string{"pgup", "ctrl+u"}, "a page up", false},
	{ContextWelcome, PageDown, []string{"pgdown", "ctrl+d"}, "a page down", false},
	{ContextWelcome, Top, []string{"g", "home"}, "the top", false},
	{ContextWelcome, Bottom, []string{"G", "end"}, "the bottom", false},

	// ---- a confirmation ----
	{ContextConfirm, Toggle, []string{" ", "space"}, "answer the tick", false},
	{ContextConfirm, Confirm, []string{"y", "enter"}, "go ahead", true},
}
