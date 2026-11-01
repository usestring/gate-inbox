package ui

import (
	"slices"
	"testing"

	"github.com/usestring/gate-inbox/internal/store"
)

// The title a row wears after its name is something the reader can see, so
// the search finds the session by it, and the row does not then claim the
// hit came from its pane.
func TestSearchFindsASessionByItsTitle(t *testing.T) {
	m := &Model{width: 120, height: 30, collapsed: map[string]bool{}}
	m.sessions = []store.Session{
		{ID: "api", Name: "api-work"},
		{ID: "other", Name: "other"},
	}
	m.titles = map[string]string{"api": "Rate limit the public API"}
	if got := filterFor(m, "public api"); !slices.Equal(got, []string{"api-work"}) {
		t.Fatalf("title search matched %v, want [api-work]", got)
	}
	if m.matchedInPane(m.sessions[0]) {
		t.Fatal("a title hit was reported as a pane hit")
	}
	if got := filterFor(m, "rate*api"); !slices.Equal(got, []string{"api-work"}) {
		t.Fatalf("wildcard title search matched %v, want [api-work]", got)
	}
}

// Until the CLI writes a title the row wears the opening prompt, and the
// search follows what the row shows.
func TestSearchFindsASessionByItsOpeningPrompt(t *testing.T) {
	m := &Model{width: 120, height: 30, collapsed: map[string]bool{}}
	m.sessions = []store.Session{
		{ID: "api", Name: "api-work", MigrationOpening: []string{"add a token bucket limiter"}},
		{ID: "other", Name: "other"},
	}
	if got := filterFor(m, "token bucket"); !slices.Equal(got, []string{"api-work"}) {
		t.Fatalf("prompt search matched %v, want [api-work]", got)
	}
}

// The title can be a long sentence, so it is matched literally: an
// abbreviation that would fuzzily match its letters in order does not.
func TestSearchDoesNotFuzzTheTitle(t *testing.T) {
	m := &Model{width: 120, height: 30, collapsed: map[string]bool{}}
	m.sessions = []store.Session{{ID: "api", Name: "api-work"}}
	m.titles = map[string]string{"api": "Rate limit the public API"}
	if got := filterFor(m, "rlpb"); len(got) != 0 {
		t.Fatalf("an abbreviation matched the title: %v", got)
	}
}

func TestPanePickerFindsASessionByItsTitle(t *testing.T) {
	m := searchModel()
	m.titles = map[string]string{m.sessions[0].ID: "Repair the OAuth callback"}
	m.openPanePicker()
	m.panePicker.input.SetValue("oauth")
	matches := m.panePickMatches()
	if len(matches) != 1 || matches[0].sess.ID != m.sessions[0].ID {
		t.Fatalf("matches = %+v, want only the titled session", matches)
	}
}
