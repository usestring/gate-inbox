package extension

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// UIProvider is an extension that adds to the interactive board's screens:
// keys on the session list, badges on a session's row, and views of its own.
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
	Keys []KeyBinding
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
	// Notify puts one line on the board's status bar.
	Notify(text string)
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
}

// ViewHandle is an opened view. Both methods may be called from any
// goroutine, and do nothing once the view is no longer on screen.
type ViewHandle interface {
	// Refresh asks the board to draw the view again.
	Refresh()
	// Close closes the view.
	Close()
}

// Badge is a short mark on a session's row. Rows are narrow, so a badge
// offers renditions of itself, widest first, and the row draws the first one
// that fits; a badge none of whose renditions fits is left off rather than
// cut, and the badges drawn are always the first ones in order. Control
// characters are removed from every span.
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
}

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
		if !ok || !enabled(ext) {
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
