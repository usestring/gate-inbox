package ui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/store"
)

func TestFuzzySessionMetadata(t *testing.T) {
	sess := store.Session{Name: "GateInbox", Tool: "codex", Group: "backend/api", Status: "working"}
	for _, tc := range []struct {
		query string
		want  bool
	}{
		{"gtin", true},
		{"GTIN", true},
		{"gtin cdx", true},
		{"bapi wrk", true},
		{"gtin claude", false},
		{"nigt", false},
		{"gate*box", true},
		{"gti*n", false},
	} {
		t.Run(tc.query, func(t *testing.T) {
			if got := matchesMetadata(sess, strings.ToLower(tc.query)); got != tc.want {
				t.Fatalf("matchesMetadata(%q) = %v, want %v", tc.query, got, tc.want)
			}
		})
	}
	if !matchesMetadata(store.Session{Name: "東京の修正"}, "東修") {
		t.Fatal("Unicode abbreviation did not match")
	}
	if matchesMetadata(store.Session{Name: "gate", Tool: "inbox"}, "gtin") {
		t.Fatal("one term must not span separate metadata fields")
	}
}

func TestFuzzySearchRanksMetadataBeforePaneAndHistory(t *testing.T) {
	m := &Model{width: 120, height: 30, collapsed: map[string]bool{}}
	m.sessions = []store.Session{
		{ID: "history", Name: "archive"},
		{ID: "loose", Name: "g" + strings.Repeat("x", 30) + "t" + strings.Repeat("x", 30) + "in"},
		{ID: "pane", Name: "screen"},
		{ID: "fuzzy", Name: "gate-inbox"},
		{ID: "tie", Name: "gate-inbox"},
		{ID: "literal", Name: "gtin"},
	}
	m.searchText = map[string]string{"pane": "gtin"}
	m.historyQuery = "gtin"
	m.historyHits = map[string]search.Hit{"history": {Key: "history", Count: 1, Score: 1000}}
	filterFor(m, "gtin")
	want := []string{"s:literal", "s:fuzzy", "s:tie", "s:loose", "s:pane", "s:history"}
	if got := rowKeys(m); !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
}

func TestFuzzySearchDoesNotFuzzPaneOrTranscriptText(t *testing.T) {
	m, _, _ := historyModel(t)
	m.sessions[0].Name = "migration"
	runHistoryQuery(t, m, "dbpr07")
	if got := sessionNames(m); len(got) != 0 {
		t.Fatalf("an abbreviation must not match long pane or transcript text: %v", got)
	}
	runHistoryQuery(t, m, "db-*-07")
	if got := sessionNames(m); !slices.Equal(got, []string{"web-ui", "quiet"}) {
		t.Fatalf("wildcard search = %v, want [web-ui quiet]", got)
	}
}

func TestFuzzySearchKeepsSelectedSessionWhenRanksChange(t *testing.T) {
	m := flatModel(t)
	unfiltered := sessionNames(m)
	m.typeSearch("gte")
	m.selectSessionRow(t, "gate-api")
	m.handleSearchKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
	selected, ok := m.selected()
	if !ok || selected.ID != "s2" {
		t.Fatalf("refining a fuzzy query changed selected session: %+v", selected)
	}
	m.handleSearchKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.searching || m.search != "gtea" {
		t.Fatalf("enter did not keep the fuzzy query: searching=%v query=%q", m.searching, m.search)
	}
	m.clearSearch()
	if got := sessionNames(m); m.search != "" || !slices.Equal(got, unfiltered) {
		t.Fatalf("clearing fuzzy search restored %v, want %v", got, unfiltered)
	}
}

func TestFuzzySearchCarriesMatchingChild(t *testing.T) {
	m := flatModel(t)
	m.typeSearch("gtch")
	want := []string{"s:s3", "s:s4"}
	if got := rowKeys(m); !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want parent and fuzzy matching child %v", got, want)
	}
}

func TestFuzzySearchTypesMultipleTerms(t *testing.T) {
	m := flatModel(t)
	m.typeSearch("gte api")
	if m.search != "gte api" {
		t.Fatalf("typed query = %q, want both terms separated by a space", m.search)
	}
	if got := sessionNames(m); !slices.Equal(got, []string{"gate-api", "carrier", "gate-child"}) {
		t.Fatalf("multi-term query matched %v", got)
	}
}

func TestFuzzySearchEditsUnicodeText(t *testing.T) {
	m := flatModel(t)
	m.sessions[0].Name = "東京の修正"
	m.typeSearch("東修")
	if got := sessionNames(m); m.search != "東修" || !slices.Equal(got, []string{"東京の修正"}) {
		t.Fatalf("Unicode query %q matched %v", m.search, got)
	}
	m.handleSearchKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.search != "東" {
		t.Fatalf("backspace left %q, want a complete Unicode character", m.search)
	}
	m.handleSearchKey(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	if m.search != "東" {
		t.Fatal("control chord inserted text")
	}
	m.handleSearchKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.search != "" {
		t.Fatalf("backspace left %q, want empty query", m.search)
	}
}
