package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The board is read from a phone and an iPad as often as from a desk, and a
// glyph the client's font has no cell for is not a small blemish: it lands as
// a `?` or as a double-width emoji that shunts the rest of the row sideways.
// Two rules keep it out. A rune is allowed only if it is either plain ASCII or
// named below, and every rune below is named with the reason it is safe --
// which is a font-coverage claim, so adding one is a decision, not a typo.
//
// The trap the block-elements repertoire is drawn from: a character can be in
// a block every font covers and still be resolved from the colour emoji font,
// because Unicode marks it emoji-eligible. `✉` U+2709 is such a rune, and on
// iOS it drew the inbox badge as an emoji beside a session name. Nothing in a
// range check catches that, so the dingbats are listed one at a time.
var widelyDrawnRunes = func() map[rune]bool {
	allowed := map[rune]bool{}
	add := func(runes string) {
		for _, r := range runes {
			allowed[r] = true
		}
	}
	addRange := func(lo, hi rune) {
		for r := lo; r <= hi; r++ {
			allowed[r] = true
		}
	}

	// Latin-1 Supplement and General Punctuation: the oldest and most
	// universally cut part of any font a terminal will pick.
	add("°·µ»±§é×–—…•“”‘’")

	// Arrows, individually. The block runs to U+21FF and its tail is as
	// sparsely cut as anything else; these nine are the ones the key legends,
	// the fork card and the worktree chip are written in.
	add("←↑→↓↕↵↳↻⇄")

	// Box Drawing, but only the full-length forms. U+2574-257F are the
	// half-length and mixed-weight pieces, and their coverage outside a
	// handful of desktop terminal fonts is as thin as the legacy blocks'.
	addRange(0x2500, 0x2573)

	// Block Elements (U+2580-259F) and Geometric Shapes (U+25A0-25FF): the
	// board's whole visual grammar -- the frame's fill and seam, and one
	// shape per session state. Eight of the shapes come straight back out:
	// Unicode marks them emoji-eligible, which is how `✉` broke, and a range
	// that let ▶ through would be repeating that mistake one block over.
	addRange(0x2580, 0x25FF)
	for _, r := range "▪▫▶◀◻◼◽◾" {
		delete(allowed, r)
	}

	// Braille Patterns: the startup spinner. Terminal fonts carry this block
	// because every CLI spinner in the world is drawn from it.
	addRange(0x2800, 0x28FF)

	// Mathematical Operators, individually: ⊘ marks a muted row, ⊗ a row
	// whose pane has stopped acting on input, ⊙ a row whose status is coming
	// off its pane rather than its hook file, and ≡ opens the search field.
	// All four are in every font a terminal will pick -- ⊗ and ⊙ are ⊘'s
	// immediate neighbours in the block, U+2297 and U+2299 either side of
	// U+2298, and all three carry Emoji=No the same way -- and ≡ is
	// here rather than an ASCII "/" because the rail is full of paths.
	add("⊘⊗⊙≡")

	// Miscellaneous Technical, one rune. ⌥ is emitted only on Darwin, and
	// Apple's macOS, iOS, and iPadOS text fonts include it. Emoji=No keeps it
	// in the text font. A non-Apple client attached to a Mac-hosted board may
	// fall back to `?`, but the action remains spelled out beside the key.
	add("⌥")

	// Dingbats, individually and never as a range. Each of these has
	// Emoji=No, so a client resolves it from the text font; their
	// neighbours ✉ ✳ ✴ ❗ do not, and must stay out.
	add("✓✕✗✎✦❯")

	return allowed
}()

// emojiGlyphRunes are the runes of the opt-in emoji mark set, and nothing
// else. They sit deliberately outside widelyDrawnRunes: they are
// double-width and font-dependent, which is precisely what that repertoire
// exists to keep off a phone. They are safe only because nothing draws them
// unless an operator turned them on, on a terminal they have watched render
// them -- which is the only way that font claim can be made at all.
// TestEmojiMarksShareOneWidthAndAreDistinct is what holds them to it.
var emojiGlyphRunes = func() map[rune]bool {
	allowed := map[rune]bool{}
	for _, mark := range emojiMarks(emojiGlyphs) {
		for _, r := range mark {
			allowed[r] = true
		}
	}
	return allowed
}()

// emojiMarks is every mark in a set, for the checks that have to cover all
// of them rather than a remembered list of them.
func emojiMarks(set glyphSet) []string {
	return []string{
		set.working, set.starting, set.waiting, set.finished, set.errored,
		set.idle, set.muted, set.deaf, set.hookless,
		set.checksPassing, set.checksFailing, set.checksPending,
	}
}

// assertWidelyDrawn fails on any rune of text that no ordinary font can be
// relied on to draw. ANSI styling is stripped first: the escapes are not
// glyphs, and a styled string is mostly escapes.
func assertWidelyDrawn(t *testing.T, where, text string) {
	t.Helper()
	assertWidelyDrawnExcept(t, where, text, nil)
}

// assertWidelyDrawnExcept is the same check over a surface that carries
// somebody else's characters as well as ours. A preview panel is a capture of
// an agent's own pane: `✳ Running tests…` is Claude drawing its spinner, and
// the board neither picked that glyph nor can pick another. Exempting exactly
// the runes the capture contains keeps the guard pointed at what the board
// draws, and TestDrawnLiteralsStayInWidelyDrawnGlyphs is what makes that
// division safe -- a glyph we write down is caught at its literal even when a
// capture happens to contain it too.
func assertWidelyDrawnExcept(t *testing.T, where, text string, foreign map[rune]bool) {
	t.Helper()
	for _, r := range ansi.Strip(text) {
		if r < 0x80 || widelyDrawnRunes[r] || foreign[r] {
			continue
		}
		t.Errorf("%s draws %q (U+%04X), which is outside the repertoire a phone's "+
			"font can be relied on for; pick a glyph from widelyDrawnRunes or add "+
			"this one there with the coverage claim that justifies it", where, string(r), r)
	}
}

// TestDrawnLiteralsStayInWidelyDrawnGlyphs reads this package's own source and
// holds every string literal in it to the repertoire. internal/ui is where the
// board's glyphs are chosen; internal/config's patterns are deliberately left
// out, because those match an agent's output rather than draw anything. The golden corpus alone
// cannot do this: it only covers the surfaces a fixture happens to reach, and
// the inbox badge -- the glyph that actually broke on the operator's iPad --
// renders only for a session with messages waiting, which no golden case had.
// A literal is checked wherever it sits, because a glyph that never reaches
// the screen costs nothing to keep safe and one that does is caught the moment
// it is typed.
func TestDrawnLiteralsStayInWidelyDrawnGlyphs(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				text, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				where := name + ":" + strconv.Itoa(fset.Position(lit.Pos()).Line)
				// glyphset.go is the one file allowed to hold the opt-in
				// emoji set, and only those exact runes. Exempting the file
				// rather than the whole package keeps the guard pointed at
				// every other literal: an emoji typed into a row, a badge or
				// a legend still fails here, wherever it is written.
				if strings.HasSuffix(name, "glyphset.go") {
					assertWidelyDrawnExcept(t, where, text, emojiGlyphRunes)
					return true
				}
				assertWidelyDrawn(t, where, text)
				return true
			})
		}
	}
}

// TestBadgesAndTitlesAreWidelyDrawn covers the surfaces that are built rather
// than written down: a badge composed with a count, one mark per state, and
// every dialog title the confirm dispatch can produce.
func TestBadgesAndTitlesAreWidelyDrawn(t *testing.T) {
	assertWidelyDrawn(t, "inboxBadge", inboxBadge(2))
	assertWidelyDrawn(t, "mutedGlyph", mutedGlyph())
	for _, state := range []string{"working", "starting", "waiting", "finished", "errored", "dead", "idle"} {
		assertWidelyDrawn(t, "statusGlyph("+state+")", statusGlyph(state))
	}
	assertWidelyDrawn(t, "gauge", gauge(75, 12))

	m := shotModel()
	for _, action := range []string{actionArchive, actionRestore, actionRestart, actionRevive} {
		m.confirm.action = action
		for _, group := range []bool{false, true} {
			m.confirm.isGroup = group
			assertWidelyDrawn(t, "confirmTitle", m.confirmTitle())
		}
	}
}
