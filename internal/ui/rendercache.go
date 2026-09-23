package ui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// lipgloss.Style.Render walks its border, margin, padding and alignment
// machinery on every call, and none of the styles cached here set any of it.
// That made Render half of a frame at this machine's size, over strings that
// are the same handful every frame, so each render is done once and kept.
//
// A hit returns lipgloss's own output verbatim, which is what makes the cache
// invisible: the frame it paints is the frame it would have painted without
// one.
//
// Every key below comes from a bounded set — the key map, the status
// vocabulary, the tree's guide runs, the theme's own tones. Nothing keyed on
// a session name, a captured pane line or any other unbounded value belongs
// here, because none of these maps evicts.

type capKey struct{ a, b string }

var (
	keyCapCache      = map[capKey]string{}
	keyCapQuietCache = map[capKey]string{}
	statusTintCache  = map[capKey]string{}
	subtleCache      = map[string]string{}
	mutedCache       = map[string]string{}
	edgeCellCache    = map[string]string{}
	bgSeqCache       = map[string]string{}
	refillCache      = map[string]string{}
	hruleCache       = map[int]string{}
	toneCache        = map[string]string{}
)

// renderGen counts theme rebuilds, so a memo held across frames can tell an
// answer built by the theme now in force from one built by the last.
var renderGen int

// resetRenderCaches drops every memo. applyTheme re-derives the styles and
// the tones these hold renders of, so their answers stop being the ones
// lipgloss would now give.
func resetRenderCaches() {
	renderGen++
	clear(keyCapCache)
	clear(keyCapQuietCache)
	clear(statusTintCache)
	clear(subtleCache)
	clear(mutedCache)
	clear(edgeCellCache)
	clear(bgSeqCache)
	clear(refillCache)
	clear(hruleCache)
	clear(toneCache)
	clear(tintedCache)
	legendSlots = [4]struct {
		key, value string
		set        bool
	}{}
}

func statusTint(st, text string) string {
	key := capKey{st, text}
	if out, ok := statusTintCache[key]; ok {
		return out
	}
	out := lipgloss.NewStyle().Foreground(statusColor(st)).Render(text)
	statusTintCache[key] = out
	return out
}

func subtleText(text string) string {
	if out, ok := subtleCache[text]; ok {
		return out
	}
	out := subtleStyle.Render(text)
	subtleCache[text] = out
	return out
}

func mutedText(text string) string {
	if out, ok := mutedCache[text]; ok {
		return out
	}
	out := mutedStyle.Render(text)
	mutedCache[text] = out
	return out
}

// tone memoises a theme-derived fill. mix formats a hex string through
// fmt.Sprintf, and the fills are read once per painted cell rather than once
// per frame.
func themeTone(name, a, b string, ratio float64) string {
	if out, ok := toneCache[name]; ok {
		return out
	}
	out := mix(a, b, ratio)
	toneCache[name] = out
	return out
}

// fastStyle is a lipgloss style with the SGR prefix and reset it emits
// resolved once, at the point the theme builds it. Style.Render walks its
// border, margin, padding and alignment machinery on every call whatever the
// style sets, and for a style that sets only colour and weight the whole walk
// ends where it started: the output is the style's own sequence, the string,
// and a reset. So the sequences are taken once from lipgloss and every later
// render is the concatenation lipgloss would have arrived at.
//
// Unlike the maps above this holds no per-string state, so it works over the
// unbounded strings a session name or a captured line brings.
type fastStyle struct {
	lipgloss.Style
	pfx, sfx string
	direct   bool
}

// fastProbes are the strings the prefix is taken from and then checked
// against. They are printable and of different lengths on purpose: a width, a
// max width, an alignment or a border all size themselves to the text, so they
// show up as the first probe's answer failing to predict the rest. They carry
// both cases and a digit, so a transform shows up too -- and no tab, newline or
// carriage return, which are the three inputs Render rewrites before it styles
// anything.
var fastProbes = []string{"aZ0", "aZ0 bcdefghijklmnopqrstuvwxyz", "aZ0~"}

func newFastStyle(style lipgloss.Style) fastStyle {
	out := fastStyle{Style: style}
	first := style.Render(fastProbes[0])
	idx := strings.Index(first, fastProbes[0])
	if idx < 0 {
		return out
	}
	pfx, sfx := first[:idx], first[idx+len(fastProbes[0]):]
	for _, probe := range fastProbes[1:] {
		if pfx+probe+sfx != style.Render(probe) {
			return out
		}
	}
	out.pfx, out.sfx, out.direct = pfx, sfx, true
	return out
}

// Render answers exactly what the embedded style answers. A tab, a newline or
// a carriage return sends the call back to lipgloss, which rewrites all three
// before it styles anything.
func (f fastStyle) Render(strs ...string) string {
	if !f.direct || len(strs) != 1 {
		return f.Style.Render(strs...)
	}
	if strings.ContainsAny(strs[0], "\t\n\r") {
		return f.Style.Render(strs[0])
	}
	return f.pfx + strs[0] + f.sfx
}

// tintedCache holds the plain foreground styles a row builds from a value it
// carries rather than from the theme's own named styles. Every key is one of
// the five state colours applyTheme sets, and lipgloss.Color resolves to a
// comparable value type, so the map is both bounded and safe to key this way.
var tintedCache = map[color.Color]fastStyle{}

func tinted(c color.Color) fastStyle {
	if out, ok := tintedCache[c]; ok {
		return out
	}
	out := newFastStyle(lipgloss.NewStyle().Foreground(c))
	tintedCache[c] = out
	return out
}

// refillSeq is what an inner reset becomes inside a painted row: the reset
// itself, then the row's fill put back. paint substitutes it on every row
// that carries one, and the value depends only on the fill.
func refillSeq(bg string) string {
	if out, ok := refillCache[bg]; ok {
		return out
	}
	out := ansiReset + bgSeq(bg)
	refillCache[bg] = out
	return out
}
