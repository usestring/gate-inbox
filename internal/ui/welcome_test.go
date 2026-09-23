package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// welcomeModel is a store-backed manager with a terminal big enough to draw
// the card whole, armed the way Init arms it.
func welcomeModel(t *testing.T) *Model {
	t.Helper()
	st, err := store.Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return &Model{store: st, mode: modeList, width: 120, height: 50, welcomeArmed: true}
}

func TestWelcomeOpensOnFirstRunAndNeverAgain(t *testing.T) {
	m := welcomeModel(t)
	m.maybeOpenWelcome()
	if m.mode != modeWelcome {
		t.Fatalf("expected the introduction to open, mode=%v", m.mode)
	}
	if seen, err := m.store.Setting(welcomeSeenSetting); err != nil || seen == "" {
		t.Fatalf("raising the card should spend the flag, got %q err %v", seen, err)
	}

	m.closeWelcome()
	if m.mode != modeList {
		t.Fatalf("dismissing should land on the list, mode=%v", m.mode)
	}
	// A later start reads the same store: the introduction is spent.
	m.welcomeArmed = true
	m.maybeOpenWelcome()
	if m.mode != modeList {
		t.Fatalf("the card came back on a second start, mode=%v", m.mode)
	}
}

func TestWelcomeStaysQuietOnAStoreAlreadyInUse(t *testing.T) {
	m := welcomeModel(t)
	if err := m.store.CreateSession(store.Session{ID: "a", Name: "alpha", Tool: "claude", Cwd: "/repo"}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	m.maybeOpenWelcome()
	if m.mode != modeList {
		t.Fatalf("an install already in use must not be interrupted, mode=%v", m.mode)
	}
	// Spent anyway: the card must not appear later just because the board
	// happens to be empty on some future start.
	if seen, err := m.store.Setting(welcomeSeenSetting); err != nil || seen == "" {
		t.Fatalf("the flag should be spent without showing, got %q err %v", seen, err)
	}
}

func TestWelcomeNeverRaisesOverAnotherScreen(t *testing.T) {
	m := welcomeModel(t)
	m.mode = modeSettings
	m.maybeOpenWelcome()
	if m.mode != modeSettings {
		t.Fatalf("the card took a screen that was already up, mode=%v", m.mode)
	}
	if seen, _ := m.store.Setting(welcomeSeenSetting); seen != "" {
		t.Fatal("declining to raise the card must not spend the flag")
	}
}

func TestWalkthroughRefusesInPlaceAndSaysSo(t *testing.T) {
	m := welcomeModel(t)
	m.maybeOpenWelcome()
	m.handleWelcomeKey(key("t"))
	if m.mode != modeWelcome {
		t.Fatalf("the refusal should keep the card up, mode=%v", m.mode)
	}
	if !strings.Contains(m.errBar.text, "not built yet") {
		t.Fatalf("the walkthrough key said nothing about being unavailable: %q", m.errBar.text)
	}
}

func TestKeyMapOpensFromTheWelcomeCardAndComesBack(t *testing.T) {
	m := welcomeModel(t)
	m.maybeOpenWelcome()
	m.handleWelcomeKey(key("?"))
	if m.mode != modeHelp {
		t.Fatalf("? should open the key map, mode=%v", m.mode)
	}
	m.handleHelpKey(key("esc"))
	if m.mode != modeWelcome {
		t.Fatalf("closing the key map should return to the card, mode=%v", m.mode)
	}
}

func TestWelcomeCardDrawsBothAnswers(t *testing.T) {
	m := welcomeModel(t)
	m.maybeOpenWelcome()
	body := ansi.Strip(m.viewWelcome())
	for _, want := range []string{"Welcome to Gate Inbox", "get started", "take the guided walkthrough", "not built yet"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the card never drew %q", want)
		}
	}
}

// The card is the only place several of these keys are named before an
// operator knows to press ?, so a section quietly losing its rows would
// leave the introduction introducing nothing.
func TestWelcomeCardNamesTheKeysTheWorkflowIsBuiltOn(t *testing.T) {
	m := welcomeModel(t)
	m.maybeOpenWelcome()
	body := ansi.Strip(m.viewWelcome())
	for _, want := range []string{"new session in the group under the cursor", "quick prompt", "triage", "work view"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the card never drew %q", want)
		}
	}
}

func TestShortTerminalScrollsTheCardRatherThanClippingIt(t *testing.T) {
	m := welcomeModel(t)
	m.height = 14
	m.maybeOpenWelcome()
	if m.welcomeScrollLimit() == 0 {
		t.Fatal("a 14-row terminal cannot hold the whole card, so it must scroll")
	}
	m.handleWelcomeKey(key("G"))
	if m.welcome.scroll != m.welcomeScrollLimit() {
		t.Fatalf("G should reach the bottom, scroll=%d limit=%d", m.welcome.scroll, m.welcomeScrollLimit())
	}
	if !strings.Contains(ansi.Strip(m.viewWelcome()), "get started") {
		t.Fatal("the choices are at the end of the card and must be reachable by scrolling to it")
	}
	m.handleWelcomeKey(key("g"))
	if m.welcome.scroll != 0 {
		t.Fatalf("g should return to the top, scroll=%d", m.welcome.scroll)
	}
}

// Dismissed once, the introduction is unreachable unless something reopens
// it -- and the walkthrough, when it exists, has to be retakeable.
func TestSettingsReopensTheWelcomeGuide(t *testing.T) {
	m := buildModel(t)
	m.openSettings()
	for i := 0; i < settingsFieldCount && m.settings.field != settingsFieldGuide; i++ {
		m.handleSettingsKey(key("down"))
	}
	if m.settings.field != settingsFieldGuide {
		t.Fatal("settings never reached the welcome guide row")
	}
	m.handleSettingsKey(key("enter"))
	if m.mode != modeWelcome {
		t.Fatalf("the guide row should raise the card, mode=%v", m.mode)
	}
	// Settings saved and left behind on the way in, so leaving the card
	// lands on the list rather than back inside settings.
	m.handleWelcomeKey(key("esc"))
	if m.mode != modeList {
		t.Fatalf("dismissing the reopened card should land on the list, mode=%v", m.mode)
	}
}

// On a narrow card the half that truncation would cut is the half that says
// what a key does, so every row wraps under itself instead.
func TestNarrowCardWrapsRatherThanTruncating(t *testing.T) {
	m := welcomeModel(t)
	m.width, m.height = 56, 60
	m.maybeOpenWelcome()
	body := ansi.Strip(m.viewWelcome())
	if strings.Contains(body, "…") {
		t.Fatalf("something was truncated on a 56-column card:\n%s", body)
	}
	// The tail of a wrapped description, which a truncating card would drop.
	if !strings.Contains(body, "cursor; it asks which agent") {
		t.Fatalf("a wrapped description lost its continuation:\n%s", body)
	}
	for _, line := range strings.Split(body, "\n") {
		if w := len([]rune(line)); w > 56 {
			t.Fatalf("a line ran %d columns past a 56-column terminal: %q", w-56, line)
		}
	}
}
