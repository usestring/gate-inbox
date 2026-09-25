// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/keymap"
)

func runeKey(s string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}

func namedKey(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code} }

// helpSectionsOf is the catalog as the screen resolves it, which is what
// every test below reads: a row's key is whatever the map says it is now,
// and that is the thing worth asserting about.
func helpSectionsOf() []resolvedSection { return helpModel().resolvedHelp() }

// Every action in the catalog is documented. This replaced a test that
// scraped case literals out of keys.go: the handler holds no keys any more,
// so the question is not which letters it answers but whether every action
// it can answer has a line in the map that names its key.
func TestKeyMapDocumentsEveryAction(t *testing.T) {
	documented := map[keymap.Context]map[keymap.Action]bool{}
	for _, section := range helpSections() {
		for _, row := range section.rows {
			if row.action == "" {
				continue
			}
			if documented[row.ctx] == nil {
				documented[row.ctx] = map[keymap.Action]bool{}
			}
			documented[row.ctx][row.action] = true
		}
	}
	for _, binding := range keymap.Catalog {
		if !documented[binding.Context][binding.Action] {
			t.Errorf("%s.%s is bindable but has no row in the ? key map",
				binding.Context, binding.Action)
		}
	}
}

// And the other way: a help row about a binding that no longer exists would
// print a blank key and offer a rebind that goes nowhere.
func TestKeyMapNamesOnlyRealActions(t *testing.T) {
	known := map[keymap.Context]map[keymap.Action]bool{}
	for _, binding := range keymap.Catalog {
		if known[binding.Context] == nil {
			known[binding.Context] = map[keymap.Action]bool{}
		}
		known[binding.Context][binding.Action] = true
	}
	for _, section := range helpSections() {
		for _, row := range section.rows {
			if row.action != "" && !known[row.ctx][row.action] {
				t.Errorf("section %q names %s.%s, which is not in the catalog",
					section.title, row.ctx, row.action)
			}
		}
	}
}

func TestHelpSectionListsAKeyOnce(t *testing.T) {
	for _, section := range helpSectionsOf() {
		seen := map[string]bool{}
		for _, row := range section.rows {
			if row.key == "" {
				continue
			}
			if seen[row.key] {
				t.Errorf("section %q lists %q twice", section.title, row.key)
			}
			seen[row.key] = true
		}
	}
}

func TestHelpEveryRowHasADescription(t *testing.T) {
	for _, section := range helpSectionsOf() {
		if len(section.rows) == 0 {
			t.Errorf("section %q has no rows", section.title)
		}
		for _, row := range section.rows {
			if strings.TrimSpace(row.text) == "" {
				t.Errorf("section %q: key %q has no description", section.title, row.key)
			}
		}
	}
}

func TestHelpSearchNarrowsToMatchingRows(t *testing.T) {
	all := helpSectionsOf()
	if got := matchHelp(all, ""); len(got) != len(all) {
		t.Fatalf("empty query dropped sections: %d of %d", len(got), len(all))
	}
	got := matchHelp(all, "priority")
	if len(got) == 0 {
		t.Fatal("priority matches nothing")
	}
	for _, section := range got {
		for _, row := range section.rows {
			combined := strings.ToLower(row.key + " " + row.text + " " + string(row.action))
			if !strings.Contains(combined, "priority") &&
				!strings.Contains(strings.ToLower(section.title), "priority") {
				t.Errorf("section %q kept %q, which does not match", section.title, row.text)
			}
		}
	}
	if hits := matchHelp(all, "zzzz"); len(hits) != 0 {
		t.Fatalf("a query nothing answers kept %d sections", len(hits))
	}
}

func TestHelpSearchIsCaseInsensitive(t *testing.T) {
	lower := helpRowCount(matchHelp(helpSectionsOf(), "revive"))
	upper := helpRowCount(matchHelp(helpSectionsOf(), "REVIVE"))
	if lower == 0 || lower != upper {
		t.Fatalf("case changed the hits: %d lower, %d upper", lower, upper)
	}
}

// A section title standing in for its screen answers on its own: "the name
// sweep" should hand back that screen, not the rows that happen to spell the
// words.
func TestHelpSearchOnASectionTitleKeepsItsRows(t *testing.T) {
	var card resolvedSection
	for _, section := range helpSectionsOf() {
		if strings.HasPrefix(section.title, "the name sweep") {
			card = section
		}
	}
	if len(card.rows) == 0 {
		t.Fatal("no name sweep section in the catalog")
	}
	for _, section := range matchHelp(helpSectionsOf(), "the name sweep") {
		if section.title != card.title {
			continue
		}
		if len(section.rows) != len(card.rows) {
			t.Fatalf("title match kept %d of %d rows", len(section.rows), len(card.rows))
		}
		return
	}
	t.Fatal("the name sweep section did not survive its own title")
}

// The built-in review screen is gone, so no key in the map may still offer
// it and nothing may still bind ctrl+r.
func TestHelpNeverAdvertisesReview(t *testing.T) {
	review := regexp.MustCompile(`(?i)\breview`)
	for _, section := range helpSectionsOf() {
		if review.MatchString(section.title) {
			t.Errorf("key map still carries a %q section", section.title)
		}
		for _, row := range section.rows {
			if strings.EqualFold(row.key, "ctrl+r") || review.MatchString(row.text) {
				t.Errorf("key map still advertises review: %q / %q", row.key, row.text)
			}
		}
	}
	if frame := ansi.Strip(helpModel().frame()); review.MatchString(frame) {
		t.Errorf("the key map screen still mentions review:\n%s", frame)
	}
}

func TestGlobalHelpShowsAgentManagementGuidance(t *testing.T) {
	frame := ansi.Strip(helpModel().frame())
	if !strings.Contains(frame, "Tell your agent to manage sessions and terminals in Gate Inbox") {
		t.Fatalf("global help is missing agent-management guidance:\n%s", frame)
	}
}

func helpModel() *Model {
	return &Model{width: 120, height: 30, mode: modeHelp}
}

func TestHelpScrollClampsToContent(t *testing.T) {
	m := helpModel()
	m.scrollHelp(-5)
	if m.help.scroll != 0 {
		t.Fatalf("scrolled above the top: %d", m.help.scroll)
	}
	limit := m.helpScrollLimit()
	if limit == 0 {
		t.Fatal("the catalog should overflow a 30-row terminal")
	}
	m.scrollHelp(1000)
	if m.help.scroll != limit {
		t.Fatalf("scroll %d past the limit %d", m.help.scroll, limit)
	}
}

func TestHelpSearchShrinksTheScrollLimit(t *testing.T) {
	m := helpModel()
	full := m.helpScrollLimit()
	m.help.query = "worktree"
	if narrowed := m.helpScrollLimit(); narrowed >= full {
		t.Fatalf("searched limit %d did not shrink below %d", narrowed, full)
	}
}

func TestHelpSearchTypesAndClears(t *testing.T) {
	m := helpModel()
	m.handleHelpKey(runeKey("/"))
	if !m.help.searching {
		t.Fatal("/ did not open the search")
	}
	for _, r := range "fork" {
		m.handleHelpKey(runeKey(string(r)))
	}
	if m.help.query != "fork" {
		t.Fatalf("typed query is %q", m.help.query)
	}
	m.handleHelpKey(namedKey(tea.KeyBackspace))
	if m.help.query != "for" {
		t.Fatalf("backspace left %q", m.help.query)
	}
	m.handleHelpKey(namedKey(tea.KeyEnter))
	if m.help.searching || m.help.query != "for" {
		t.Fatalf("enter should leave the field with the search on, got %v %q", m.help.searching, m.help.query)
	}
	// q types into the search rather than closing while the field is up.
	m.handleHelpKey(runeKey("/"))
	m.handleHelpKey(runeKey("q"))
	if m.mode != modeHelp || m.help.query != "forq" {
		t.Fatalf("q while searching: mode %v query %q", m.mode, m.help.query)
	}
}

func TestHelpEscClearsTheSearchBeforeClosing(t *testing.T) {
	m := helpModel()
	m.help.query = "fork"
	m.help.scroll = 3
	m.handleHelpKey(namedKey(tea.KeyEsc))
	if m.mode != modeHelp {
		t.Fatal("esc closed the map while a search was on")
	}
	if m.help.query != "" || m.help.scroll != 0 {
		t.Fatalf("esc left query %q scroll %d", m.help.query, m.help.scroll)
	}
	m.handleHelpKey(namedKey(tea.KeyEsc))
	if m.mode != modeList {
		t.Fatalf("esc on a clean map left mode %v", m.mode)
	}
}

func TestHelpOpensClean(t *testing.T) {
	m := helpModel()
	m.help = helpState{scroll: 4, query: "fork", searching: true}
	m.closeHelp()
	m.openHelp()
	if !reflect.DeepEqual(m.help, helpState{}) {
		t.Fatalf("reopened with stale state: %+v", m.help)
	}
}

func TestHelpFramePaintsInsideTheTerminal(t *testing.T) {
	for _, width := range []int{60, 80, 120, 200} {
		for _, height := range []int{14, 24, 40} {
			m := &Model{width: width, height: height, mode: modeHelp}
			for _, query := range []string{"", "revive"} {
				m.help.query = query
				lines := strings.Split(m.frame(), "\n")
				if len(lines) != height {
					t.Errorf("%dx%d query %q: %d rows painted", width, height, query, len(lines))
				}
				for i, line := range lines {
					if got := ansi.StringWidth(line); got > width {
						t.Errorf("%dx%d query %q: row %d is %d wide", width, height, query, i, got)
					}
				}
			}
		}
	}
}

func TestHelpBodyShowsMoreMarkersWhenItOverflows(t *testing.T) {
	m := helpModel()
	frame := ansi.Strip(m.frame())
	if !strings.Contains(frame, "more below") {
		t.Fatal("an overflowing map should say there is more below")
	}
	m.help.scroll = m.helpScrollLimit()
	frame = ansi.Strip(m.frame())
	if !strings.Contains(frame, "more above") {
		t.Fatal("a map scrolled to the end should say there is more above")
	}
}

func TestHelpReportsWhenNothingMatches(t *testing.T) {
	m := helpModel()
	m.help.query = "zzzz"
	if frame := ansi.Strip(m.frame()); !strings.Contains(frame, "no key matches that") {
		t.Fatal("a query nothing answers should say so")
	}
}

// The column is measured from the catalog, so a key can never render clipped
// against its own description. This is what catches a long binding added later.
func TestHelpKeyColumnFitsEveryKey(t *testing.T) {
	column := helpModel().helpKeyColumn()
	for _, section := range helpSectionsOf() {
		for _, row := range section.rows {
			if w := ansi.StringWidth(row.key); w >= column {
				t.Errorf("key %q is %d wide, the column is %d", row.key, w, column)
			}
		}
	}
}

// Descriptions have to survive the default card too: a row wider than the
// column leaves it renders with an ellipsis instead of its own words.
func TestHelpDescriptionsFitTheDefaultCard(t *testing.T) {
	room := cardInnerWidth(helpCardWidth(120)) - helpModel().helpKeyColumn()
	for _, section := range helpSectionsOf() {
		for _, row := range section.rows {
			if w := ansi.StringWidth(row.text); w > room {
				t.Errorf("section %q: %q is %d wide, only %d is left beside the key column",
					section.title, row.text, w, room)
			}
		}
	}
}

func TestHelpHighlightSurvivesAnAwkwardQuery(t *testing.T) {
	// Folding "İ" lengthens it, which is the case that would slice out of
	// range if the run were measured by the raw query.
	for _, query := range []string{"İ", "ẞ", "", "  ", "the", "THE"} {
		for _, section := range helpSectionsOf() {
			for _, row := range section.rows {
				highlightMatch(row.text, query, 60)
			}
		}
	}
}

// An extension the config switched off leads the key map, with its reason,
// and a board with none to report shows no such section.
func TestHelpReportsDisabledExtensions(t *testing.T) {
	m := helpModel()
	m.height = 60
	if strings.Contains(ansi.Strip(m.viewHelp()), "disabled extensions") {
		t.Fatal("the key map reports disabled extensions when there are none")
	}
	m.SetExtensionNotes([]string{"ext1 disabled: [extensions.ext1]: unknown key(s): bogus"})
	if first := m.resolvedHelp()[0].title; first != "disabled extensions" {
		t.Fatalf("the key map opens with %q", first)
	}
	screen := ansi.Strip(m.viewHelp())
	for _, want := range []string{"disabled extensions", "ext1 disabled: [extensions.ext1]: unknown key(s): bogus"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("the key map does not show %q:\n%s", want, screen)
		}
	}
	m.help.query = "ext1"
	if kept := matchHelp(m.resolvedHelp(), m.help.query); len(kept) == 0 || kept[0].title != "disabled extensions" {
		t.Fatalf("a search for the extension does not find its note: %+v", kept)
	}
}
