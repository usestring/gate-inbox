package keymap

import (
	"runtime"
	"strings"
)

// Display renders a key the way a footer or a key map prints it. The names
// bubbletea reports are for comparing, not for reading: "enter" is ↵ on
// screen and always has been, and a legend that started printing the raw
// names would be a visible regression in every frame.
func Display(key string) string {
	return displayForOS(key, runtime.GOOS)
}

// WHY: The footer repeats alt chords often, so the standard Mac glyph keeps
// them readable within its three-row budget.
const optionGlyph = "⌥"

func displayForOS(key, goos string) string {
	if key == "" {
		return ""
	}
	if pretty, ok := prettyKeys[key]; ok {
		return pretty
	}
	// A chord prints its own parts, so alt+pgup follows pgup without a
	// second table.
	parts := strings.Split(key, "+")
	tail := parts[len(parts)-1]
	if pretty, ok := prettyKeys[tail]; ok {
		tail = pretty
	}
	var b strings.Builder
	for _, mod := range parts[:len(parts)-1] {
		if goos == "darwin" && mod == "alt" {
			b.WriteString(optionGlyph)
			continue
		}
		b.WriteString(mod)
		b.WriteByte('+')
	}
	b.WriteString(tail)
	return b.String()
}

// DisplayPair renders two displayed keys that share a modifier as one
// chord: "alt+↑↓" rather than "alt+↑/alt+↓". On a Mac the modifier is the
// ⌥ glyph with no plus after it, so the shared prefix is the glyph
// instead: "⌥↑↓" rather than "⌥↑/⌥↓". Keys that share nothing are joined
// plainly.
func DisplayPair(first, second string) string {
	i, j := strings.LastIndex(first, "+"), strings.LastIndex(second, "+")
	if i > 0 && i == j && first[:i] == second[:j] {
		return first + second[j+1:]
	}
	firstTail, firstOK := strings.CutPrefix(first, optionGlyph)
	secondTail, secondOK := strings.CutPrefix(second, optionGlyph)
	if firstOK && secondOK && firstTail != "" && secondTail != "" {
		return first + secondTail
	}
	return first + "/" + second
}

// WHY: Key files accept the names shown on screen, including the legacy
// "option+" spelling, but bubbletea reports both as alt.
func canonicalKey(key string) string {
	key = strings.ReplaceAll(key, optionGlyph, "alt+")
	parts := strings.Split(key, "+")
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] == "option" {
			parts[i] = "alt"
		}
	}
	return strings.Join(parts, "+")
}

// Compact is Display with the chords shortened the way a footer writes
// them: ^n rather than ctrl+n. The key map spells them out instead, so this
// is not the default rendering -- a legend held to one row is.
func Compact(key string) string {
	if strings.HasPrefix(key, "ctrl+") && !strings.HasPrefix(key, "ctrl+alt+") {
		return "^" + Display(strings.TrimPrefix(key, "ctrl+"))
	}
	return Display(key)
}

var prettyKeys = map[string]string{
	"enter":      "↵",
	"up":         "↑",
	"down":       "↓",
	"left":       "←",
	"right":      "→",
	"shift+up":   "shift+↑",
	"shift+down": "shift+↓",
	" ":          "space",
	"esc":        "esc",
	"pgup":       "pgup",
	"pgdown":     "pgdn",
	"backspace":  "bksp",
	"tab":        "tab",
}
