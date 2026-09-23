package ui

import (
	"fmt"
	"image/color"
	"regexp"
	"testing"

	"github.com/usestring/gate-inbox/internal/status"
)

// withPalette runs a check under one palette and puts the package-level
// choice back, so a test cannot leave the rest of the suite drawing quiet.
func withPalette(t *testing.T, name string, check func()) {
	t.Helper()
	theme := current
	wasQuiet := quietPalette
	t.Cleanup(func() {
		quietPalette = wasQuiet
		applyTheme(theme)
	})
	setPalette(name)
	check()
}

// distinctTints counts how many different colours a set of states paints in,
// which is the thing quiet is meant to reduce.
func distinctTints(states []string) map[string]bool {
	seen := map[string]bool{}
	for _, state := range states {
		seen[hexOf(statusColor(state))] = true
	}
	return seen
}

func hexOf(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("%02x%02x%02x", r>>8, g>>8, b>>8)
}

// frameColours is every distinct colour a real fleet frame paints, read off
// the escapes rather than off the tokens, so a surface that reaches past
// statusColor is counted too.
func frameColours(t *testing.T) map[string]bool {
	t.Helper()
	m := fleetModel(t, 20, 120, 40)
	seen := map[string]bool{}
	for _, match := range sgrColour.FindAllStringSubmatch(m.frame(), -1) {
		seen[match[1]] = true
	}
	return seen
}

// sgrColour matches the truecolor foreground and background sequences
// lipgloss emits, which is how every tint reaches the frame.
var sgrColour = regexp.MustCompile(`\x1b\[(?:38|48);2;(\d+;\d+;\d+)m`)

// Full is the default: nothing about an existing board changes.
func TestFullIsTheDefaultPalette(t *testing.T) {
	m := buildModel(t)
	if got := storedPalette(m.store); got != paletteFull {
		t.Errorf("a fresh store reads %q, want %q", got, paletteFull)
	}
	if quietPalette {
		t.Error("the package came up quiet")
	}
}

// The states that mean a person is owed something keep their colour. Quiet
// narrows what is coloured; it does not take the signal away.
func TestQuietKeepsTheAttentionStatesColoured(t *testing.T) {
	withPalette(t, paletteFull, func() {
		loud := distinctTints([]string{status.Waiting, status.Errored, status.Finished})
		withPalette(t, paletteQuiet, func() {
			quiet := distinctTints([]string{status.Waiting, status.Errored, status.Finished})
			if len(quiet) != len(loud) {
				t.Errorf("quiet paints the attention states in %d colours, full in %d", len(quiet), len(loud))
			}
			for tint := range loud {
				if !quiet[tint] {
					t.Error("quiet dropped one of the attention colours")
				}
			}
		})
	})
}

// Working and idle stop being colours of their own: an agent mid-turn and
// one resting both need nothing, and a tint that says so is spent saying
// nothing.
func TestQuietSendsTheRestingStatesToTheTypeRamp(t *testing.T) {
	withPalette(t, paletteQuiet, func() {
		if hexOf(statusColor(status.Working)) != hexOf(colorDim) {
			t.Error("working kept a colour of its own under quiet")
		}
		if hexOf(statusColor(status.Idle)) != hexOf(colorSubtle) {
			t.Error("idle kept a colour of its own under quiet")
		}
		if hexOf(statusColor(status.Starting)) != hexOf(statusColor(status.Working)) {
			t.Error("starting and working parted company under quiet")
		}
	})
}

// The board as a whole gets quieter, which is the complaint: measured over
// a real fleet frame rather than over the tokens, so a surface that reaches
// past statusColor is counted too.
func TestQuietPaintsAFleetFrameInFewerColours(t *testing.T) {
	var loud, quiet int
	withPalette(t, paletteFull, func() {
		loud = len(frameColours(t))
	})
	withPalette(t, paletteQuiet, func() {
		quiet = len(frameColours(t))
	})
	if quiet >= loud {
		t.Errorf("a quiet frame uses %d colours, a full one %d; it should use fewer", quiet, loud)
	}
}

// The cursor and the keys keep the accent. A board that cannot show its own
// focus is not calmer, it is broken.
func TestQuietKeepsTheFocusAccent(t *testing.T) {
	var full string
	withPalette(t, paletteFull, func() { full = hexOf(colorAccent) })
	withPalette(t, paletteQuiet, func() {
		if hexOf(colorAccent) != full {
			t.Error("quiet took the accent that marks the cursor and the keys")
		}
	})
}

// Turning it off gives the palette back. The transform is applied to a copy,
// so the theme's own tokens are still there to restore -- a palette
// flattened into itself would have nothing to hand back.
func TestQuietReversesCleanly(t *testing.T) {
	before := hexOf(statusColor(status.Working))
	withPalette(t, paletteQuiet, func() {
		if hexOf(statusColor(status.Working)) == before {
			t.Fatal("quiet changed nothing, so this test proves nothing")
		}
		setPalette(paletteFull)
		if got := hexOf(statusColor(status.Working)); got != before {
			t.Error("turning quiet off did not give the working colour back")
		}
		// And again, to catch a transform that eats its own output.
		setPalette(paletteQuiet)
		setPalette(paletteFull)
		if got := hexOf(statusColor(status.Working)); got != before {
			t.Error("a second round trip lost the working colour")
		}
	})
}

// Switching theme under quiet narrows the new theme rather than repainting
// in the old one's tokens.
func TestQuietFollowsAThemeChange(t *testing.T) {
	withPalette(t, paletteQuiet, func() {
		for _, theme := range themes {
			applyTheme(theme)
			if hexOf(statusColor(status.Working)) != hexOf(colorDim) {
				t.Errorf("theme %q kept a working colour under quiet", theme.Name)
			}
		}
	})
}

// The choice survives the panel and the store, applies as it is stepped, and
// an unknown stored value leaves the board as it was.
func TestPaletteRoundTripsAndFailsSafe(t *testing.T) {
	m := buildModel(t)
	theme := current
	t.Cleanup(func() { quietPalette = false; applyTheme(theme) })
	m.openSettings()
	m.settings.field = settingsFieldPalette
	m.applyCmd(t, m.cycleSetting(1))
	if m.settings.palette != paletteQuiet {
		t.Fatalf("one step landed on %q, want %q", m.settings.palette, paletteQuiet)
	}
	if !quietPalette {
		t.Error("stepping the picker did not put the palette it names on screen")
	}
	if _, cmd := m.saveAndCloseSettings(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if got := storedPalette(m.store); got != paletteQuiet {
		t.Fatalf("stored %q, want %q", got, paletteQuiet)
	}

	if err := m.store.SetSetting(paletteSetting, "muted"); err != nil {
		t.Fatal(err)
	}
	if got := storedPalette(m.store); got != paletteFull {
		t.Errorf("an unknown stored value read as %q, want %q", got, paletteFull)
	}
}
