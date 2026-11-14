package ui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

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

// Title-only search keeps the rows whose name or generated title carries the
// query and drops the ones that only showed it on screen or said it in their
// transcript. Tab flips the scope back and the broad matches return.
func TestTitleSearchIgnoresPaneAndTranscript(t *testing.T) {
	m, _, _ := historyModel(t)
	m.titles = map[string]string{"4": "Recover db-primary-07 replica"}
	runHistoryQuery(t, m, "db-primary-07")
	if got := sessionNames(m); !slices.Equal(got, []string{"db-primary-07-migration", "bystander", "web-ui", "quiet"}) {
		t.Fatalf("full search = %v", got)
	}

	m.handleSearchKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if !m.searchTitleOnly {
		t.Fatal("tab did not switch the field to title-only")
	}
	if got := sessionNames(m); !slices.Equal(got, []string{"db-primary-07-migration", "bystander"}) {
		t.Fatalf("title search = %v, want the named and the titled rows only", got)
	}
	if m.historyHits != nil {
		t.Fatalf("title search kept transcript hits: %v", m.historyHits)
	}
	if line := m.searchFieldLine(80); !strings.Contains(line, "title:") {
		t.Fatalf("field does not say it is title-only: %q", line)
	}

	m.handleSearchKey(tea.KeyPressMsg{Code: tea.KeyTab})
	m.applyCmd(t, m.historySearchCmd())
	if got := sessionNames(m); len(got) != 4 {
		t.Fatalf("back to full search = %v, want all four matches", got)
	}
}

// The opening prompt a row falls back to as its summary is body text, not a
// title: a title-only query must not reach into it.
func TestTitleSearchSkipsOpeningPromptFallback(t *testing.T) {
	m := &Model{width: 120, height: 30, collapsed: map[string]bool{}}
	m.sessions = []store.Session{{ID: "a", Name: "alpha"}, {ID: "b", Name: "beta"}}
	m.titles = map[string]string{"b": "Rotate deploy keys"}
	m.searchTitleOnly = true
	if got := filterFor(m, "deploy"); !slices.Equal(got, []string{"beta"}) {
		t.Fatalf("title search = %v, want [beta]", got)
	}
	if got := filterFor(m, "alp"); !slices.Equal(got, []string{"alpha"}) {
		t.Fatalf("name search = %v, want [alpha]", got)
	}
}
