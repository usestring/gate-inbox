package ui

import "github.com/usestring/gate-inbox/internal/store"

// A full board tints per state, and then tints the rail, the guides, the
// group names, the badges and the legend on top of that. Everything is
// coloured, so nothing is: the one row actually waiting on a person is the
// same amount of loud as the twelve that are getting on with it.
//
// Quiet is the palette narrowed to what a reader is scanning for. The states
// that want somebody keep their colour; the ones that do not, and the
// structure around them, fall back to the type ramp. It is a palette
// transform rather than a second set of styles, so it reaches every surface
// at once and no renderer has to know about it.

const (
	paletteSetting = "palette"
	paletteFull    = "full"
	paletteQuiet   = "quiet"
)

// paletteModes is the setting's cycle order.
var paletteModes = []string{paletteFull, paletteQuiet}

// quietPalette is whether the transform is in force. Package-level beside
// the theme it transforms, and for the same reason: every paint reads it.
var quietPalette bool

func storedPalette(st *store.Store) string {
	chosen, err := st.Setting(paletteSetting)
	if err != nil {
		return paletteFull
	}
	return normalizePalette(chosen)
}

func normalizePalette(chosen string) string {
	for _, mode := range paletteModes {
		if chosen == mode {
			return mode
		}
	}
	return paletteFull
}

// setPalette puts a palette mode in force and repaints from the theme's own
// tokens. applyTheme keeps current raw, so this reverses as cleanly as it
// applies -- a quietened theme quietened again would have nothing left to
// hand back.
func setPalette(name string) {
	quietPalette = normalizePalette(name) == paletteQuiet
	applyTheme(current)
}

// quietened is the palette narrowed. What survives is the attention set --
// waiting, errored, finished -- because those are the three states that mean
// a person is owed something, and picking them out is the whole job a
// coloured board is doing.
//
// Working and idle go to the type ramp: an agent mid-turn and one resting
// both need nothing, and a colour that says "nothing is required here" is
// spent saying nothing. Accent2 goes with them, because a group name is
// structure rather than state. Accent itself stays: it marks where the
// cursor and the keys are, which is a fact about the operator rather than
// decoration, and a board that cannot show its own focus is not calmer.
func quietened(t Theme) Theme {
	t.Working = t.Dim
	t.Idle = t.Subtle
	t.Accent2 = t.Dim
	return t
}
