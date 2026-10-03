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
			listRow(keymap.Open, "focus the session: keys reach the agent, the list stays on screen"),
			listRow(keymap.Attach, "attach: leave the list, fill the terminal with this pane"),
			listRow(keymap.QuickInput, "hotkeys: send a snippet to the session without entering it"),
			listRow(keymap.Priority, "priority: p steps urgent → high → medium → low → none"),
			listRow(keymap.LastPane, "back to the previous pane; again walks further back"),
			listRow(keymap.Rescind, "undo the latest submission while its turn is active"),
			listRow(keymap.NewSession, "new session here: n asks the agent, ctrl+n the full form"),
			listRow(keymap.Dismiss, "skip"),
			listRow(keymap.Archive, "kill it: close the pane and file the row"),
			listRow(keymap.Search, "fuzzy session search; esc closes, deleting text clears"),
			listRow(keymap.Triage, "triage: this group as one queue, head first"),
			listRow(keymap.Settings, "settings"),
			listRow(keymap.QuickActions, "quick actions: type what you want, ↵ runs it, and it shows the key"),
			listRow(keymap.Help, "this key map, where a binding is changed"),
			listRow(keymap.Quit, "quit (sessions keep running)"),
		}},
		{title: "common: enter the next session", rows: []helpRow{
			listRow(keymap.JumpAttention, "waiting on you"),
			note("one key per state lives under advanced, unbound"),
		}},
		{title: "common: inside a session", rows: []helpRow{
			note("typing goes straight to the agent, every letter of it"),
			bound(focus, keymap.Leave, "back to the list; in triage, on to the next"),
			bound(focus, keymap.LeaveHard, "back to the list, always stopping there"),
			bound(focus, keymap.QuickInput, "hotkey menu: send a snippet without leaving"),
			bound(focus, keymap.Dismiss, "skip"),
			bound(focus, keymap.Rescind, "undo the latest submission while its turn is active"),
			bound(focus, keymap.Archive, "kill it, asking first"),
			bound(focus, keymap.Help, "this key map, in place: only ctrl+h reaches it here"),
		}},
		{title: "advanced: the list", rows: []helpRow{
			note("rows marked — ship unbound: run them from quick actions (:)"),
			note("or rebind them on this screen"),
			listRow(keymap.CursorTop, "jump to the top of the list"),
			listRow(keymap.CursorBottom, "jump to the bottom"),
			listRow(keymap.JumpWaiting, "enter the next waiting session"),
			listRow(keymap.JumpFinished, "enter the next finished session"),
			listRow(keymap.JumpErrored, "enter the next errored or dead session"),
			listRow(keymap.JumpIdle, "enter the next idle session"),
			listRow(keymap.JumpWorking, "enter the next working session"),
			note("tab above walks the whole queue already"),
			listRow(keymap.JumpPane, "find a listed pane by name and focus it"),
			listRow(keymap.ReorderUp, "reorder the row up: swap with the visible sibling above"),
			note("reordering is manual order: triage and sorted views refuse it"),
			listRow(keymap.ReorderDown, "reorder the row down: swap with the visible sibling below"),
			listRow(keymap.PreviewUp, "scroll the preview up"),
			listRow(keymap.PreviewDown, "scroll the preview down"),
			listRow(keymap.PreviewPageUp, "scroll the preview a page up"),
			listRow(keymap.PreviewPageDown, "scroll the preview a page down"),
			listRow(keymap.PreviewTop, "the preview's oldest history"),
			listRow(keymap.PreviewBottom, "back to the preview's live bottom"),
			note("scrolling past either end moves to the adjacent row"),
			listRow(keymap.NewSessionForm, "the full form: name, CLI, directory, first task"),
			listRow(keymap.NewGroup, "new group"),
			listRow(keymap.NewTerminal, "new terminal tab: a shell under the selected agent, or in the group"),
			lit("1-9", "jump to the group with that number, 0 for root"),
			note("the next digit walks in: 2 then 1 is the subgroup numbered 2.1"),
			listRow(keymap.ClearSearch, "clear the search: delete the field's text (ctrl+u wipes it)"),
			listRow(keymap.LegendPeek, "peek at every key available for this row"),
			listRow(keymap.StatusFilter, "filter to what needs attention (waiting, finished, errored)"),
			note("in triage, answering hands a session over and opens the next;"),
			note("turning settings' triage auto proceed off keeps you in the session"),
			listRow(keymap.ShowAllWork, "show all of a session's pull requests and tickets"),
			listRow(keymap.ArchivedView, "archived view: killed rows wait 7 days; u restores, U undoes"),
			listRow(keymap.EmptyGroups, "hide / show empty groups"),
			listRow(keymap.FoldAll, "fold / unfold every group and every session's work"),
			listRow(keymap.Resize, "resize the split (←→ or hl, or drag; ↵ commits, esc cancels)"),
			listRow(keymap.NameSweep, "name sweep: ask idle, warm adopted panes to name themselves"),
			listRow(keymap.TakeOver, "take over the adopted panes now; busy ones once they go idle"),
			listRow(keymap.ToggleChrome, "hide / show the key hints along the foot"),
			listRow(keymap.ToggleRail, "hide / show the list beside the pane: the board, under a key"),
			note("w on this key map, or settings, reopens the welcome guide"),
			note("settings: \"on reopen\" sets what startup does with sessions that died"),
			note("settings: \"outside panes\" sets whether adopted panes are taken over"),
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
			listRow(keymap.RestartWith, "restart it with extra flags (same row, fresh context, plus flags)"),
			listRow(keymap.ArchiveAll, "kill every session listed (asks for a tick)"),
			listRow(keymap.Revive, "revive it, or restart a live one on the conversation it is on"),
			listRow(keymap.ReviveAll, "revive every dead session"),
			listRow(keymap.SwitchAccount, "switch account; migrates Claude context over 200k"),
			listRow(keymap.Restore, "restore it out of the archive (and revive it)"),
			note("a row left in the archive is deleted for good after 7 days"),
		}},
		{title: "advanced: group under the cursor", rows: []helpRow{
			listRow(keymap.Open, "fold / unfold"),
			listRow(keymap.RenameSelf, "edit it: name, parent, default path"),
			listRow(keymap.Move, "move it, with its whole subtree, under another group"),
			listRow(keymap.Archive, "kill the subtree"),
			listRow(keymap.Restore, "restore the subtree out of the archive"),
		}},
		{title: "advanced: hotkey menu", rows: []helpRow{
			note("Snippets only, no text box: type in a focused session."),
			lit("a-z § ±", "send the snippet on that key"),
			lit("↑↓", "on the list, switch the target session"),
			lit("space esc", "close"),
		}},
		{title: "advanced: inside a session", rows: []helpRow{
			bound(focus, keymap.HandOver, "the one-press alias for leaving, in triage and out"),
			bound(focus, keymap.BackAtPrompt, "Right at prompt's end: list; triage: Left list, finished Right next"),
			bound(focus, keymap.Dismiss, "skip"),
			bound(focus, keymap.ToggleConversation, "switch conversation / terminal (experimental compressed focus only)"),
			bound(focus, keymap.LastPane, "back to the previous pane; again walks further back"),
			bound(focus, keymap.JumpPane, "find a listed pane and focus it"),
			bound(focus, keymap.ToggleChrome, "hide / show the key hints along the foot; the pane takes the rows"),
			bound(focus, keymap.ToggleRail, "hide / show the list beside the pane; the pane takes the columns"),
			bound(focus, keymap.Archive, "focused: kill it, asking first; in triage, on into the queue"),
			bound(focus, keymap.NewSession, "new session in this group (only alt+n: letters reach the agent)"),
			bound(focus, keymap.PreviewPageUp, "scroll a page up"),
			bound(focus, keymap.PreviewPageDown, "scroll a page down"),
			bound(focus, keymap.CopySessionID, "copy the agent's session id"),
			bound(focus, keymap.PreviewUp, "focused: scroll a step without a wheel"),
			bound(focus, keymap.PreviewDown, "focused: scroll a step down"),
			bound(focus, keymap.PreviewTop, "focused: oldest history"),
			bound(focus, keymap.PreviewBottom, "focused: back to the live bottom"),
			note("scrolling past either end leaves to the adjacent row"),
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
			lit("↵", "run the field's action (snippets, CLIs, guide, keys)"),
			lit("esc", "save and close"),
			note("settings also reopens the welcome guide and the key map"),
		}},
		{title: "advanced: dialogs", rows: []helpRow{
			lit("tab", "next field"),
			lit("ctrl+v", "in a prompt field, paste an image as a chip"),
			lit("↵", "confirm"),
			lit("esc", "cancel"),
		}},
	}
}

// legendSections is the glyph map the key map used to carry: the marks on a
// session row and the marks under it. It lives on its own screen now, one key
// off the key map, so readers hunting a binding stop reading past glyphs and
// readers hunting a glyph stop reading past bindings. Every row is literal:
// none of these is a binding anybody can move.
func legendSections() []helpSection {
	return []helpSection{
		{title: "legend: the mark on a session row", rows: []helpRow{
			lit("◐ working", "the agent is busy on a turn"),
			lit("◆ waiting", "blocked on you: a dialog, a permission ask, a question"),
			lit("● finished", "the turn ended; entering the session clears it to idle"),
			lit("○ idle", "nothing running"),
			lit("✕ errored", "the tool reported an error, or the session is dead"),
			lit("◌ starting", "the pane is still launching"),
		}},
		{title: "legend: a pull request or ticket under a session", rows: []helpRow{
			lit("■ ◧ ◰", "a pull request: merged, open, checks running"),
			lit("▢ □", "a pull request: draft, closed"),
			lit("▣", "a pull request blocked on you"),
			lit("▲ ◭ △", "a ticket: done, started, not started"),
		}},
	}
}
