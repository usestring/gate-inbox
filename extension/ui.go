package extension

import (
	"context"
	"errors"
	"fmt"
)

// UIProvider is an extension that adds to the interactive board's screens:
// keys on the session list, and badges on a session's row.
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

// KeyBinding is one action an extension adds to a screen.
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
	// Run answers the key. It is called off the board's event loop with the
	// row the cursor was on; ctx ends when the board exits. An error is put
	// on the board's status bar, prefixed with the extension's id.
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
}

// Badge is a short mark on a session's row. Rows are narrow, so a badge that
// does not fit is drawn as Short instead, and one that fits in neither is left
// off rather than cut; the badges drawn are always the first ones in order.
// Control characters are removed from both.
type Badge struct {
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
