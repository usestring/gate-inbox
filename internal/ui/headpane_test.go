package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// pageView is a header's page that records what it is told.
type pageView struct {
	title  string
	keys   []ViewKey
	closed []CloseReason
	// leaveOn is the key that answers true.
	leaveOn string
}

func (v *pageView) Title() string { return v.title }

func (v *pageView) Render(width, height int) [][]Span {
	return [][]Span{{{Text: "the question waiting on you", Tone: ToneWarn}}, {{Text: "1. carry on"}}}
}

func (v *pageView) Key(key ViewKey) bool {
	v.keys = append(v.keys, key)
	return key.Key == v.leaveOn
}

func (v *pageView) Closed(reason CloseReason) { v.closed = append(v.closed, reason) }

// paneModel is a board whose "other" headers have a page, with the pages it
// made and the presses each was made for.
func paneModel(t *testing.T) (*Model, *ExtensionBridge, *[]*pageView, *[]Press) {
	t.Helper()
	var pages []*pageView
	var made []Press
	m, bridge := rowMarksModel(t, ExtensionUI{
		Owner: "other",
		Keys:  []ExtensionKey{{Screen: "page", Action: string(ActionClose), Keys: []string{"ctrl+w"}, Label: "back"}},
		HeaderPane: &HeaderPane{Screen: "page", View: func(press Press, _ ViewHandle) ExtensionView {
			page := &pageView{title: "◈ ship it", leaveOn: "esc"}
			pages, made = append(pages, page), append(made, press)
			return page
		}},
	})
	bridge.Group("other", "s9", []Span{{Text: "◈ ship it"}})
	m.Update(extensionBadgesMsg{})
	return m, bridge, &pages, &made
}

func paneText(m *Model) string {
	var out []string
	for _, line := range m.contentLines(80, 20) {
		out = append(out, ansi.Strip(line.text))
	}
	return strings.Join(out, "\n")
}

// Arriving on a header with a page shows the page where the session's pane
// would be, without taking the keyboard, and made once for that header.
func TestArrivingOnAHeaderShowsItsPage(t *testing.T) {
	m, _, pages, made := paneModel(t)
	m.cursor = headIndex(t, m, "other", "s9")
	got := paneText(m)
	if !strings.Contains(got, "◈ ship it") || !strings.Contains(got, "the question waiting on you") {
		t.Fatalf("the content column does not show the page:\n%s", got)
	}
	if m.mode != modeList {
		t.Fatalf("arriving on the header took the keyboard: %s", m.mode)
	}
	paneText(m)
	if len(*pages) != 1 || (*made)[0].SessionID != "s9" {
		t.Fatalf("pages made = %d for %#v, want one for s9", len(*pages), *made)
	}
	if m.pane.box.ok {
		t.Fatal("an unfocused page drew the focus ring's box")
	}
}

// The open key focuses the page the way it focuses a session: keys go to the
// page, and the focused session's way out comes back to the list.
func TestOpenFocusesAHeadersPage(t *testing.T) {
	m, _, pages, _ := paneModel(t)
	m.cursor = headIndex(t, m, "other", "s9")
	paneText(m)
	m.handleKey(key("enter"))
	if m.mode != modeHeadPane {
		t.Fatalf("enter on the header left the board in %s, want the page focused", m.mode)
	}
	paneText(m)
	if !m.pane.box.ok {
		t.Fatal("a focused page drew no ring")
	}
	m.handleKey(key("2"))
	page := (*pages)[0]
	if len(page.keys) != 1 || page.keys[0].Key != "2" {
		t.Fatalf("the page was told %#v, want the 2", page.keys)
	}
	m.handleKey(key("ctrl+q"))
	if m.mode != modeList {
		t.Fatalf("ctrl+q left the board in %s, want the list", m.mode)
	}
	if len(page.keys) != 1 {
		t.Fatalf("the way out reached the page: %#v", page.keys)
	}

	// The page's own close, and the page answering true, leave it too; it
	// is the same page each time.
	m.handleKey(key("enter"))
	m.handleKey(key("ctrl+w"))
	if m.mode != modeList {
		t.Fatalf("the page's close left the board in %s", m.mode)
	}
	m.handleKey(key("enter"))
	m.handleKey(key("esc"))
	if m.mode != modeList || len(*pages) != 1 {
		t.Fatalf("mode %s with %d pages made, want the list and one page", m.mode, len(*pages))
	}
	if len(page.closed) != 0 {
		t.Fatalf("leaving the page closed it: %v", page.closed)
	}
}

// A card the focused page opens goes over it and hands the keyboard back to
// it when it closes.
func TestACardFromAPageReturnsToIt(t *testing.T) {
	m, _, _, _ := paneModel(t)
	m.cursor = headIndex(t, m, "other", "s9")
	paneText(m)
	m.handleKey(key("enter"))
	m.extScreens["page"] = true
	card := &pageView{title: "budget"}
	m.Update(extensionOpenMsg{id: 99, owner: "other", screen: "page", view: card})
	if m.mode != modeExtensionView {
		t.Fatalf("the card did not open over the page: %s", m.mode)
	}
	m.handleKey(key("ctrl+w"))
	if m.mode != modeHeadPane {
		t.Fatalf("closing the card left the board in %s, want the page focused again", m.mode)
	}
}

// A header that goes takes its page with it, told so, and a focused page
// gives the keyboard back.
func TestAPageGoesWithItsHeader(t *testing.T) {
	m, bridge, pages, _ := paneModel(t)
	m.cursor = headIndex(t, m, "other", "s9")
	paneText(m)
	m.handleKey(key("enter"))
	bridge.Group("other", "s9", nil)
	m.Update(extensionBadgesMsg{})
	if m.mode != modeList {
		t.Fatalf("the board stayed in %s after the header went", m.mode)
	}
	if page := (*pages)[0]; len(page.closed) != 1 || page.closed[0] != CloseHandle {
		t.Fatalf("the page was told %v", page.closed)
	}
}

// A triage walk that reaches a session with a page over it lands focused on
// the page.
func TestTriageLandsOnAHeadersPage(t *testing.T) {
	m, _, _, _ := paneModel(t)
	m.triage = true
	m.rebuildRows()
	m.enterTriageHead()
	if m.mode != modeHeadPane {
		t.Fatalf("triage landed in %s, want the page", m.mode)
	}
	if row, _ := m.cursorRow(); !row.isHead() || row.sess.ID != "s9" {
		t.Fatalf("triage cursor on %s, want the header over s9", rowKey(row))
	}
}

// A header with a page is a row, and only a row: drawn again as a caption
// over its session, every charter showed its title on two lines.
func TestAHeaderWithAPageIsDrawnOnce(t *testing.T) {
	m, _, _, _ := paneModel(t)
	var lines []string
	for _, line := range m.entryLines(m.rows, 0, 80, 30) {
		lines = append(lines, ansi.Strip(line.text))
	}
	if got := strings.Count(strings.Join(lines, "\n"), "◈ ship it"); got != 1 {
		t.Fatalf("the header is drawn %d times, want once:\n%s", got, strings.Join(lines, "\n"))
	}
}
