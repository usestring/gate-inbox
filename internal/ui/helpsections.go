package ui

// The key map's catalog. Every row that is about a binding names the action
// rather than the key, so the screen prints what is bound now and the rebind
// cursor has something to land on; the rows that are about a glyph, a mouse
// gesture or a field in a form stay literal, because none of those is a
// binding anybody can move.
//
// A row names exactly one action. The combined rows this screen used to
// carry ("↑↓ / jk", "x / X") read well and rebind badly -- the cursor lands
// on one row and has two bindings to choose between -- so where the catalog
// has two actions, this has two rows.

import "github.com/usestring/gate-inbox/internal/keymap"

// helpRow is one line of the key map: a binding, or a note about the ones
// around it.
type helpRow struct {
	// ctx and action name the binding. A row that carries one prints the key
	// bound to it now and can be rebound from this screen.
	ctx    keymap.Context
	action keymap.Action
	// key is the literal for a row that is not a binding: a glyph, a mouse
	// gesture, a field key inside a form.
	key  string
	text string
}

// bound is a row about a binding.
func bound(ctx keymap.Context, action keymap.Action, text string) helpRow {
	return helpRow{ctx: ctx, action: action, text: text}
}

// listRow is bound for the list, which is most of them.
func listRow(action keymap.Action, text string) helpRow {
	return bound(keymap.ContextList, action, text)
}

// lit is a row whose key is not a binding.
func lit(key, text string) helpRow { return helpRow{key: key, text: text} }

// note is a row with no key at all: prose about the rows around it.
func note(text string) helpRow { return helpRow{text: text} }

// helpSection is one context's bindings, titled for the thing the keys act
// on.
type helpSection struct {
	title string
	rows  []helpRow
}

// selfEvident are the bindings the key map leaves out: plain arrows move
// the cursor, and a row saying so is a row the operator has to read past.
// They stay rebindable in keys.toml.
var selfEvident = map[keymap.Context]map[keymap.Action]bool{
	keymap.ContextList:      {keymap.CursorUp: true, keymap.CursorDown: true, keymap.StepIn: true, keymap.StepOut: true},
	keymap.ContextNameSweep: {keymap.CursorUp: true, keymap.CursorDown: true},
	keymap.ContextWelcome:   {keymap.CursorUp: true, keymap.CursorDown: true},
}

func helpSections() []helpSection {
	focus := keymap.ContextFocus
	return []helpSection{
		{title: "common: the list", rows: []helpRow{
			note("Tell your agent to manage sessions and terminals in Gate Inbox."),
			listRow(keymap.Open, "focus the session: keys reach the agent, the list stays on screen"),
			listRow(keymap.Attach, "attach it: the pane takes the whole terminal"),
			listRow(keymap.QuickInput, "quick prompt: answer the session without entering it"),
			listRow(keymap.LastPane, "back to the session you were on before; again swaps back"),
			listRow(keymap.Rescind, "undo the latest submission while its turn is active"),
			listRow(keymap.NewSession, "new session in the cursor's group"),
			listRow(keymap.NewGroup, "new group"),
			listRow(keymap.Dismiss, "dismiss: mark a finished session idle, or mute one triage skips"),
			listRow(keymap.Priority, "toggle priority (priority panes show first)"),
			listRow(keymap.Archive, "kill it: close the pane and file the row"),
			listRow(keymap.Search, "search the list by name"),
			listRow(keymap.Triage, "triage: this group as one queue, head first"),
			listRow(keymap.Gate, "gate: drain that queue one at a time, full width"),
			listRow(keymap.Settings, "settings"),
			listRow(keymap.LegendPeek, "peek at every key available for this row"),
			listRow(keymap.Help, "this key map, where a binding is changed"),
			listRow(keymap.Quit, "quit (sessions keep running)"),
			lit("ctrl+c", "quit, from any screen"),
		}},
		{title: "common: enter the next session", rows: []helpRow{
			listRow(keymap.JumpAttention, "waiting on you"),
			listRow(keymap.JumpWaiting, "waiting"),
			listRow(keymap.JumpFinished, "finished"),
			listRow(keymap.JumpErrored, "errored or dead"),
			listRow(keymap.JumpIdle, "idle"),
			listRow(keymap.JumpWorking, "working"),
		}},
		{title: "common: inside a session", rows: []helpRow{
			note("typing goes straight to the agent, every letter of it"),
			bound(focus, keymap.Leave, "back to the list; in triage, on to the next"),
			bound(focus, keymap.LeaveHard, "back to the list, always stopping there"),
			bound(focus, keymap.Rescind, "undo the latest submission while its turn is active"),
			bound(focus, keymap.LastPane, "back to the previous session"),
			bound(focus, keymap.PreviewPageUp, "scroll a page up"),
			bound(focus, keymap.PreviewPageDown, "scroll a page down"),
		}},
		{title: "advanced: the list", rows: []helpRow{
			listRow(keymap.CursorTop, "jump to the top of the list"),
			listRow(keymap.CursorBottom, "jump to the bottom"),
			listRow(keymap.ReorderUp, "reorder the row up"),
			listRow(keymap.ReorderDown, "reorder the row down"),
			listRow(keymap.PreviewUp, "scroll the preview up"),
			listRow(keymap.PreviewDown, "scroll the preview down"),
			listRow(keymap.PreviewPageUp, "scroll the preview a page up"),
			listRow(keymap.PreviewPageDown, "scroll the preview a page down"),
			listRow(keymap.PreviewTop, "the preview's oldest history"),
			listRow(keymap.PreviewBottom, "back to the preview's live bottom"),
			listRow(keymap.NewSessionForm, "new session, asking first: name, CLI, directory, first task"),
			listRow(keymap.NewTerminal, "new terminal tab: a shell under the selected agent, or in the group"),
			lit("1-9", "jump to the group with that number, 0 for root"),
			note("the next digit walks in: 2 then 1 is the subgroup numbered 2.1"),
			listRow(keymap.ClearSearch, "clear the search"),
			listRow(keymap.StatusFilter, "filter to what needs attention (waiting, finished, errored)"),
			note("in triage, answering hands a session over and opens the next;"),
			note("turning settings' triage auto proceed off keeps you in the session"),
			listRow(keymap.HandOver, "in triage: mute this one and enter the next that needs you"),
			listRow(keymap.ShowAllWork, "show all of a session's pull requests and tickets"),
			listRow(keymap.ArchivedView, "archived view"),
			listRow(keymap.EmptyGroups, "hide / show empty groups"),
			listRow(keymap.FoldAll, "fold / unfold every group and every session's work"),
			listRow(keymap.Resize, "resize the split (←→ or hl, or drag; ↵ commits, esc cancels)"),
			listRow(keymap.NameSweep, "name sweep: ask idle, warm adopted panes to name themselves"),
			listRow(keymap.TakeOver, "panes started outside the board: keep, relaunch or leave out"),
			listRow(keymap.ToggleChrome, "hide / show the key hints along the foot"),
			listRow(keymap.ToggleRail, "hide / show the list beside the pane: the board, under a key"),
			note("w on this key map, or settings, reopens the welcome guide"),
			note("settings: \"on reopen\" and \"outside panes\" set what startup asks"),
			note("to stop sessions: gate-inbox park, and unpark to bring them back"),
			note("to leave for good: the README's \"Stop using it\" section"),
		}},
		{title: "advanced: session under the cursor", rows: []helpRow{
			note("settings swap what focus and attach do"),
			note("an archived row cannot be focused: attach reaches it, restore revives"),
			listRow(keymap.ToggleConversation, "show full or shortened messages"),
			listRow(keymap.Fork, "fork it into a new session in the same group"),
			listRow(keymap.Migrate, "migrate it to another CLI: a new session there reads its transcript"),
			listRow(keymap.RenameSelf, "rename it after the conversation running in it"),
			listRow(keymap.Rename, "rename it yourself, and re-pick its tool"),
			listRow(keymap.Move, "move it to a group, or a terminal into a session"),
			listRow(keymap.Restart, "restart it on an empty context (same name, group, dir, tool)"),
			listRow(keymap.ArchiveAll, "kill every session listed (asks for a tick)"),
			listRow(keymap.Revive, "revive it, or restart a live one on the conversation it is on"),
			listRow(keymap.ReviveAll, "revive every dead session"),
			listRow(keymap.SwitchAccount, "switch account; migrates Claude context over 200k"),
			listRow(keymap.Restore, "restore it out of the archive (and revive it)"),
			note("a row left in the archive is deleted for good after 7 days"),
		}},
		{title: "advanced: the mark on a session row", rows: []helpRow{
			lit("◐ working", "the agent is busy on a turn"),
			lit("◆ waiting", "blocked on you: a dialog, a permission ask, a question"),
			lit("● finished", "the turn ended; entering the session clears it to idle"),
			lit("○ idle", "nothing running"),
			lit("✕ errored", "the tool reported an error, or the session is dead"),
			lit("◌ starting", "the pane is still launching"),
		}},
		{title: "advanced: a pull request or ticket under a session", rows: []helpRow{
			listRow(keymap.Open, "open it in the browser"),
			note("the session keys act on sessions, so they are refused here"),
			lit("■ ◧ ◰", "a pull request: merged, open, checks running"),
			lit("▢ □", "a pull request: draft, closed"),
			lit("▣", "a pull request blocked on you"),
			lit("▲ ◭ △", "a ticket: done, started, not started"),
		}},
		{title: "advanced: group under the cursor", rows: []helpRow{
			listRow(keymap.Open, "fold / unfold"),
			listRow(keymap.QuickInput, "quick prompt: spawn a new agent in the group"),
			listRow(keymap.RenameSelf, "edit it: name, parent, default path"),
			listRow(keymap.Move, "move it, with its whole subtree, under another group"),
			listRow(keymap.Archive, "kill the subtree"),
			listRow(keymap.Restore, "restore the subtree out of the archive"),
		}},
		{title: "advanced: quick prompt", rows: []helpRow{
			lit("↵", "send"),
			lit("↑↓", "switch the target session"),
			lit("tab", "switch the tool a spawn uses ("+keymap.Display("alt+m")+" too)"),
			lit("ctrl+v", "paste an image as a chip at the cursor"),
			lit("bksp", "next to a chip, delete the whole chip"),
			lit("esc", "close"),
		}},
		{title: "advanced: inside a session", rows: []helpRow{
			bound(focus, keymap.HandOver, "the one-press alias for leaving, in triage and out"),
			bound(focus, keymap.BackAtPrompt, "focused, at the prompt's start: back to the list"),
			bound(focus, keymap.Dismiss, "in the gate: dismiss this one and go on to the next"),
			lit("space", "in gate messages: compose a reply with the conversation above it"),
			bound(focus, keymap.ToggleConversation, "show full or shortened messages; keeps your draft"),
			bound(focus, keymap.ToggleGateInput, "gate: switch conversation / terminal; Home / End scroll messages"),
			bound(focus, keymap.ToggleChrome, "hide / show the key hints along the foot; the pane takes the rows"),
			bound(focus, keymap.ToggleRail, "hide / show the list beside the pane; the pane takes the columns"),
			bound(focus, keymap.Archive, "focused: kill it, asking first; in triage, on into the queue"),
			bound(focus, keymap.NewSession, "in the gate: start a new session and come back to the queue"),
			bound(focus, keymap.CopySessionID, "copy the agent's session id"),
			bound(focus, keymap.PreviewUp, "focused: scroll a step without a wheel"),
			bound(focus, keymap.PreviewDown, "focused: scroll a step down"),
			bound(focus, keymap.PreviewTop, "focused: oldest history"),
			bound(focus, keymap.PreviewBottom, "focused: back to the live bottom"),
			note("in triage, answering hands the session over and opens the next"),
			note("turning settings' triage auto proceed off keeps you in the session"),
			lit("wheel", "focused: scroll the pane's history, type to catch up"),
			lit("drag", "focused: select pane text and copy it"),
			lit("double click", "focused: copy the word"),
			lit("triple click", "focused: copy the line"),
			lit("click", "focused: goes to the agent UI when it tracks the mouse"),
			lit(keymap.Display("alt+drag"), "focused: pass a whole drag to that agent UI"),
			note("every key this screen does not claim is typed into the agent, so a"),
			note("binding rebound onto a plain letter is a letter you can no longer type"),
		}},
		{title: "advanced: the name sweep", rows: []helpRow{
			bound(keymap.ContextNameSweep, keymap.Confirm, "run the sweep"),
			bound(keymap.ContextNameSweep, keymap.Cancel, "cancel it, or stop one that is sending"),
			bound(keymap.ContextNameSweep, keymap.PageUp, "scroll the plan a page up"),
			bound(keymap.ContextNameSweep, keymap.PageDown, "scroll the plan a page down"),
		}},
		{title: "advanced: the reopen card at startup (and O)", rows: []helpRow{
			bound(keymap.ContextRestore, keymap.Confirm, "apply: resume the ticked sessions, answer the panes"),
			bound(keymap.ContextRestore, keymap.Cancel, "leave everything as it is; from the picker, back"),
			bound(keymap.ContextRestore, keymap.More, "choose per session and per pane"),
			bound(keymap.ContextRestore, keymap.CursorUp, "move up the list"),
			bound(keymap.ContextRestore, keymap.CursorDown, "move down it"),
			bound(keymap.ContextRestore, keymap.Toggle, "tick / untick the row"),
			bound(keymap.ContextRestore, keymap.TickAll, "tick every row, or start over"),
			bound(keymap.ContextRestore, keymap.NextChoice, "next answer for outside panes: adopt, relaunch, ignore"),
			bound(keymap.ContextRestore, keymap.PrevChoice, "previous answer for outside panes"),
			bound(keymap.ContextRestore, keymap.NeverAsk, "apply this answer and stop asking (settings undoes it)"),
		}},
		{title: "advanced: a confirmation", rows: []helpRow{
			bound(keymap.ContextConfirm, keymap.Confirm, "go ahead"),
			bound(keymap.ContextConfirm, keymap.Toggle, "answer the tick a destructive dialog asks for"),
			lit("k", "keep the children rather than ending them with the parent"),
			lit("esc", "any other key cancels"),
		}},
		{title: "advanced: the welcome guide", rows: []helpRow{
			bound(keymap.ContextWelcome, keymap.Close, "close it"),
			bound(keymap.ContextWelcome, keymap.Help, "this key map"),
			bound(keymap.ContextWelcome, keymap.PageUp, "a page up"),
			bound(keymap.ContextWelcome, keymap.PageDown, "a page down"),
			bound(keymap.ContextWelcome, keymap.Top, "the top"),
			bound(keymap.ContextWelcome, keymap.Bottom, "the bottom"),
		}},
		{title: "advanced: settings", rows: []helpRow{
			lit("↵", "run the field's action (CLIs, report, suggest)"),
			lit("esc", "save and close"),
			note("settings also reopens the welcome guide"),
		}},
		{title: "advanced: dialogs", rows: []helpRow{
			lit("tab", "next field"),
			lit("ctrl+v", "in a prompt field, paste an image as a chip"),
			lit("↵", "confirm"),
			lit("esc", "cancel"),
		}},
	}
}
