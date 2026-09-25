package extension

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// UIProvider is an extension that adds to the interactive board's screens:
// keys on the session list, badges on a session's row, views of its own, and
// a warning the operator confirms before a session opens.
//
// The contract is deliberately narrow. The board never hands an extension its
// model, its renderer or its event loop: a key is a named action the
// operator's key file can move, answered by a function run off the event
// loop; a badge is text and a tone the extension pushes whenever it likes,
// and the row reads the last one pushed. A slow or broken extension costs its
// own key and its own badge, never the frame.
type UIProvider interface {
	// UI is called once, when the interactive board starts: after Configure,
	// and before any BoardProvider starts. host stays valid for the life of
	// the board, so an extension may keep it and push badges from its own
	// goroutines, including a BoardProvider's.
	UI(host UIHost) (UI, error)
}

// UI is what an extension adds to the board's screens.
type UI struct {
	Keys    []KeyBinding
	Filters []Filter
	// Aliases carry the operator's stored choices over from the names they
	// were kept under before this extension took them over.
	Aliases Aliases
}

// Aliases are old names for this extension's filters and actions. Where an
// extension takes over something the board or an earlier release stored
// under another name -- a list toggle the board kept as a setting of its
// own, an action the operator's key file names -- an alias keeps what the
// operator chose rather than starting them over from the defaults.
type Aliases struct {
	Settings []SettingAlias
	Actions  []ActionAlias
}

// SettingAlias carries a stored setting over to one of this UI's filters.
//
// While the filter has no stored state of its own, the board reads From
// once, when it starts: "on", "1", "true" and "yes" turn the filter on,
// and any other value leaves it off. What was read is then stored as the
// filter's own state, so it is carried once, and later changes to From are
// not read again. From itself is left as it was, for any build that still
// reads it. Several aliases for one filter are tried in order, and the
// first one that holds a value is carried.
type SettingAlias struct {
	// From is the setting's name as the board stored it.
	From string
	// Filter is the Action of the Filter in this UI that now keeps it.
	Filter string
}

// ActionAlias is an old name for one of this UI's actions. An override
// in the operator's key file under From, on Screen, applies to To as if it
// were written under To, and the key map screen reports it as renamed
// rather than unknown. An override written under To itself wins over one
// under From. The next change the operator saves from the key map screen
// writes the keys under To and drops From.
//
// From follows KeyBinding's rules for an action name, and may not be an
// action Screen already has or an alias another action there claims.
type ActionAlias struct {
	// Screen is the list when empty.
	Screen string
	From   string
	// To is a KeyBinding's or a Filter's Action in this UI.
	To string
}

// Filter is a narrowing of the session list, toggled by a key on the list
// like the board's own filters. While it is on, the list shows only the
// sessions Keep keeps, and its header says so beside the key that lifts it.
// Several filters that are on narrow together. Whether a filter is on is
// kept between runs of the board.
//
// Action, Keys and Label are a list key's, and follow KeyBinding's rules.
type Filter struct {
	Action string
	Keys   []string
	Label  string
	// Badge is the short word the list's header shows while the filter is
	// on; Label stands in when it is empty.
	Badge string
	// Keep is asked of every session each time the list is built while the
	// filter is on, on the board's event loop, so it must answer from what
	// the extension already holds and never wait. A Keep that panics keeps
	// every session. A child is drawn under its parent, so a filter that
	// keeps a child should keep the parent too.
	Keep func(SessionInfo) bool
}

// ScreenList is the board's session list, the screen KeyBinding.Screen names
// when it is left empty.
const ScreenList = "list"

// ActionClose is the action that closes a view. The board answers it itself
// on every view screen, and a screen whose bindings name no close gets one on
// esc, so no view is a screen with no way off it.
const ActionClose = "close"

// KeyBinding is one action an extension adds to a screen.
//
// Screen is the list when empty. Any other name, other than one of the
// board's own screens, is a screen of the extension's own: its bindings are
// the keys a View opened on it is told about, under a table of that name in
// the operator's key file.
//
// Action is the name the operator's key file writes it under, beside the
// board's own actions for that screen: lower case letters, digits and '_',
// and not one the screen already has. Keys are the defaults. Where a default
// is a key the board already uses on that screen, the board keeps it and the
// problem is reported on the key map screen; the binding keeps its other
// keys, and the operator can move it onto a free one.
type KeyBinding struct {
	Screen string
	Action string
	Keys   []string
	// Label is what the key map screen says the key does.
	Label string
	// Run answers the key on the list. It is called off the board's event
	// loop with the row the cursor was on; ctx ends when the board exits. An
	// error is put on the board's status bar, prefixed with the extension's
	// id. On a view's screen the view answers the key, and Run is not
	// called.
	Run func(ctx context.Context, press Press) error
}

// Press is the row a key was pressed on.
type Press struct {
	// SessionID is the session under the cursor, or "" on a group row.
	SessionID string
	// Group is that session's group, or the group row's own path.
	Group string
}

// UIHost is the board as an extension's UI sees it.
type UIHost interface {
	// Decorate replaces this extension's badges on a session's row. Called
	// with no badges, it clears them. It may be called from any goroutine,
	// and never blocks on the board.
	Decorate(sessionID string, badges ...Badge)
	// Group replaces this extension's header over a session's row: one line
	// drawn above the row, at its depth in the tree, that says what the row
	// belongs to. It is part of the row's entry rather than a row of its
	// own, so a key pressed there is pressed on the session. Called with an
	// empty line, it clears the header. Headers from several extensions
	// stack in build order. Like Decorate, Group, Hide and Own may be called
	// from any goroutine and never block on the board.
	Group(sessionID string, header Line)
	// Hide sets whether the session is left out of the browsing tree, with
	// everything drawn under it. Search, triage and the status filters
	// still show it: they were opened to find a session.
	Hide(sessionID string, hidden bool)
	// Own sets whether this extension answers the session's questions. An
	// owned session is left out of the triage queue, the attention filter
	// and the jumps to what is waiting on the operator, and its children's
	// questions stay folded in triage as if it were working, since it will
	// answer them once the extension has answered it. A session any
	// extension says needs a person, through Attention, is on the queue
	// even while it is owned.
	Own(sessionID string, owned bool)
	// Attention replaces this extension's claim on where a session stands
	// in the operator's queue, for what its status cannot say: a session
	// blocked on the operator while its pane says it is working, or one
	// that should sort ahead of or behind its status in triage. The zero
	// Attention clears the claim. Where several extensions claim one
	// session, it needs a person if any says so, and the most urgent rank
	// wins. Like Decorate, it may be called from any goroutine and never
	// blocks on the board.
	Attention(sessionID string, attention Attention)
	// ConfirmOpen sets the warning the board shows before it opens this
	// session from the list. With a warning set, the first open key on the
	// row puts it on the status bar and opens nothing; the same key again,
	// on the same row, opens the session. Esc, any other key or a move to
	// another row drops the pending open. An empty warning lets the row open
	// at once again. Where several extensions warn about one session, the
	// board shows every warning, in the order the build lists them. It may
	// be called from any goroutine, and never blocks on the board.
	ConfirmOpen(sessionID, warning string)
	// Notify puts one line on the board's status bar.
	Notify(text string)
	// Alert raises a desktop notification, for what the operator should
	// hear about while looking elsewhere. It returns at once: delivery runs
	// in the background, one alert at a time, and a burst that outruns it
	// is dropped rather than queued without end.
	Alert(alert Alert)
	// Open shows view on screen, a screen this extension declared keys for.
	// It is shown only if the board is still on its list, or on another
	// view, when the request reaches it: a press whose load took a while
	// must not take the screen from an operator who has moved on.
	Open(screen string, view View) ViewHandle
}

// View is a screen of an extension's own, drawn as a card over the list. The
// board draws the frame, the title and the key hints from the screen's
// bindings; the view supplies the body.
//
// Every method is called on the board's event loop and must return
// promptly: a view loads off the loop and calls Refresh on its handle. A
// view that panics is closed and the panic reported.
type View interface {
	Title() string
	// Render is the body at width cells by height rows. Rows past height
	// are not drawn, each row is cut to width, and control characters are
	// removed.
	Render(width, height int) []Line
	// Key is a press the board did not answer itself. Returning true closes
	// the view.
	Key(key ViewKey) (close bool)
}

// Line is one row of a view: runs of text, each in a tone.
type Line []Span

// Width is the cells the board takes to draw l, its control characters
// removed: the measure a badge's rung is fitted to its row by.
func (l Line) Width() int {
	width := 0
	for _, span := range l {
		width += ansi.StringWidth(strings.Map(dropControl, span.Text))
	}
	return width
}

// dropControl removes the characters the board strips from an extension's
// text before drawing it.
func dropControl(r rune) rune {
	if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
		return -1
	}
	return r
}

// Span is text in one tone, which the board draws in its theme's colour for
// that meaning; ToneMuted is the ordinary text colour.
type Span struct {
	Text string
	Tone Tone
	Bold bool
}

// ViewKey is a press, as a view is told it.
type ViewKey struct {
	// Action is what the press stands for on the view's screen, or "" when
	// nothing there is bound to it.
	Action string
	// Key is the press's name, spelled the way the key file spells keys.
	Key string
	// Text is what the press types, for a view with a field of its own.
	Text string
	// Field is the field that had the keyboard, and Values every field's
	// value by ID, for a Form; both are empty for a view without fields.
	Field  string
	Values map[string]string
}

// ActionSubmit is the action a Form is told when the operator submits it:
// ViewKey.Values holds what every field says. A screen that binds no submit
// key submits on ctrl+s, and enter submits from a one-line field or a
// choice either way.
const ActionSubmit = "submit"

// Closer is a View told when the board closes it, so an extension can put
// back the view it was opened from: a child view that answers esc by
// reopening its parent.
//
// Closed is called once, on the board's event loop, after the board is back
// on its list, so a view opened from Closed is opened over the list. It is
// not called for a view that panicked, nor when the board exits with a view
// up. A Closed that panics is reported like a view that panics.
type Closer interface {
	Closed(reason CloseReason)
}

// CloseReason is why the board closed a view.
type CloseReason string

const (
	// CloseDismissed is the operator pressing the screen's close action.
	CloseDismissed CloseReason = "dismissed"
	// CloseSubmitted is the view answering true to ActionSubmit.
	CloseSubmitted CloseReason = "submitted"
	// CloseReturned is the view answering true to any other press.
	CloseReturned CloseReason = "returned"
	// CloseHandle is ViewHandle.Close.
	CloseHandle CloseReason = "handle"
	// CloseReplaced is another view opened while this one was up. The new
	// view is already on screen when Closed is called, so a view opened from
	// Closed would replace it in turn.
	CloseReplaced CloseReason = "replaced"
)

// Form is a View with fields the operator types into: the board draws them
// under the view's own lines with its own inputs, and owns the cursor,
// paste, line breaks in a multi-line field, and focus, which tab and
// shift+tab move, and up and down too: in a multi-line field they move the
// cursor between its rows, and move to the field before or after only from
// its first or last row. While a field that is typed into has the keyboard,
// it takes every key but those, submit, the screen's close, and keys the
// screen binds with ctrl or alt held, which go to Key as usual. On a choice,
// up and down move between fields too, and keys other than left and right go
// to Key.
type Form interface {
	View
	// Fields is called on the board's event loop each time the view is
	// drawn, so a form can add, drop or relabel fields as it loads.
	Fields() []Field
}

// Field is one field on a Form.
type Field struct {
	// ID names the field in ViewKey.Values. Fields are matched by ID from
	// one call of Fields to the next, which keeps what the operator typed.
	ID    string
	Label string
	// Value is what the field holds when the board first sees it, and what
	// it is set to again whenever Fields offers a different Value than it
	// last did: a form that loads a value off the loop puts it here and
	// calls Refresh. Otherwise the operator's typing stands.
	Value       string
	Placeholder string
	// Multiline makes the field a box that takes line breaks, Height rows
	// tall (three when zero); enter there is a new line.
	Multiline bool
	Height    int
	// Limit is the most characters the field takes; zero is the board's
	// default.
	Limit int
	// Choices makes the field a picker over these values, moved with left
	// and right, instead of one that is typed into. Value picks the one
	// shown first.
	Choices []string
}

// ViewHandle is an opened view. Both methods may be called from any
// goroutine, and do nothing once the view is no longer on screen.
type ViewHandle interface {
	// Refresh asks the board to draw the view again.
	Refresh()
	// Close closes the view.
	Close()
}

// Alert is a desktop notification. The board sends it by the best path the
// operator's terminal and system offer, falling back to the terminal bell.
// Control characters are removed from both fields, and each is squashed to
// one line.
type Alert struct {
	// Title names what the alert is about, such as a session's name. It is
	// shown as the notification's subtitle, under the board's own name.
	Title string
	Body  string
	// Tone picks the sound and urgency: ToneWarn for something waiting on
	// the operator, ToneGood for something finished, ToneBad for a failure.
	Tone Tone
}

// Attention is an extension's claim on a session's place in the operator's
// queue.
type Attention struct {
	// NeedsPerson puts the session on the operator's queue whatever its
	// status: triage hands it over, the attention filter keeps it, the jumps
	// to what is waiting stop on it, and its children's questions are the
	// operator's again. It holds even while the session is owned: a claim
	// that somebody is needed is an escalation, and an ownership never
	// hides one. Nor does a parent: the session is never folded into the
	// session that spawned it, and that parent sorts in triage at this
	// session's place when it is ahead of its own.
	NeedsPerson bool
	// Rank is where the session sorts in triage, in place of its status's
	// place. Sessions that need a person still sort ahead of those that do
	// not, whatever their rank.
	Rank AttentionRank
}

// AttentionRank is a place in triage's order, named by the status that holds
// it. The zero value leaves the session's status to decide.
type AttentionRank int

const (
	RankByStatus AttentionRank = iota
	RankWaiting
	// RankBlocked sorts after the sessions waiting on a question and before
	// the errored ones. No status holds it: it is for a session blocked on
	// a decision about its work rather than on one question in front of the
	// operator, which is handed over once the live questions are.
	RankBlocked
	RankErrored
	RankFinished
	RankIdle
)

// Badge is a short mark on a session's row. Rows are narrow, so a badge
// offers renditions of itself, widest first, and the row draws the first one
// that fits; a badge none of whose renditions fits is left off rather than
// cut, and the badges drawn are always the first ones in order. Control
// characters are removed from every span.
//
// A badge goes in the extension-badge slot, which comes last on the row,
// after the board's own badges, unless its Placement says otherwise.
type Badge struct {
	// Rungs are the renditions, widest first, each a line of toned spans:
	// "◈ 2c · 3/h · 12m", then "◈ 2c · 3/h", then "◈". A span's Bold is
	// drawn. When Rungs is set, Text, Short and Tone are ignored.
	Rungs []Line
	// Text, Short and Tone are the shorthand for a badge in one tone: with
	// no Rungs, the renditions are Text and then Short, both in Tone. A
	// badge with neither Rungs nor Text is not drawn.
	Text  string
	Short string
	Tone  Tone
	// Placement is where on the row the badge asks to be drawn. The zero
	// value, PlaceDefault, is the extension-badge slot.
	Placement BadgePlacement
}

// BadgePlacement is where on a session's row a badge asks to be drawn.
type BadgePlacement int

const (
	// PlaceDefault draws the badge in the extension-badge slot, last on
	// the row.
	PlaceDefault BadgePlacement = iota
	// PlaceAfterName draws the badge right after the session's name. It
	// takes only room the row has spare, trying each of its renditions
	// there in turn, so it never cuts the name or pushes out the board's
	// own badges. When none fits, it falls back to the extension-badge
	// slot and narrows there as a default badge would. Several badges
	// placed after the name keep build order: once one falls back, the
	// ones after it fall back too.
	PlaceAfterName
)

// Tone is what a badge means, which the board draws in its own theme's
// colour for that meaning.
type Tone string

const (
	ToneMuted  Tone = ""
	ToneAccent Tone = "accent"
	ToneGood   Tone = "good"
	ToneWarn   Tone = "warn"
	ToneBad    Tone = "bad"
)

// UIResult is what one extension's UI came to.
type UIResult struct {
	ID      string
	Version string
	UI      UI
	Err     error
}

// StartUI asks every enabled UIProvider, in order, for its UI. hostFor gives
// each one a host of its own. A provider that errors or panics contributes
// nothing and the rest carry on; the result reports each provider asked.
func (r *Registry) StartUI(hostFor func(id string) UIHost) ([]UIResult, error) {
	if !r.configured {
		return nil, errors.New("extensions must be configured before the board asks for their UI")
	}
	var results []UIResult
	for i, ext := range r.extensions {
		provider, ok := ext.(UIProvider)
		if !ok || !r.serving(i) {
			continue
		}
		ui, err := uiOne(provider, hostFor(r.ids[i]))
		if err != nil {
			ui = UI{}
		}
		results = append(results, UIResult{ID: r.ids[i], Version: ext.Descriptor().Version, UI: ui, Err: err})
	}
	return results, nil
}

func uiOne(provider UIProvider, host UIHost) (ui UI, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			ui, err = UI{}, fmt.Errorf("panicked while building its UI: %v", recovered)
		}
	}()
	return provider.UI(host)
}
