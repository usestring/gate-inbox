package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/status"
)

// A list row says what its session is about beside the name: the title the
// conversation was given once there is one, the opening prompt before that.
func TestARailRowCarriesTheSessionsSummary(t *testing.T) {
	m := railModel(t,
		railSession("api-work", "", "claude", status.Working, "add a token bucket limiter", time.Minute),
		railSession("other", "", "claude", status.Idle, "", time.Hour),
	)
	m.selectSessionRow(t, "other")
	m.rebuildRows()
	if row := summaryRow(t, m, 100, "api-work"); !strings.Contains(row, "api-work  add a token bucket limiter") {
		t.Fatalf("the opening prompt is not beside the name:\n%q", row)
	}
	m.titles = map[string]string{"api-work": "Rate limit the public API"}
	row := summaryRow(t, m, 100, "api-work")
	if !strings.Contains(row, "api-work  Rate limit the public API") {
		t.Fatalf("the title does not replace the prompt:\n%q", row)
	}
	if !strings.HasSuffix(strings.TrimRight(row, " "), "1m ago") {
		t.Fatalf("the summary pushed the meta off the edge:\n%q", row)
	}
}

// A title that restates the name, or a rail too narrow to say anything
// useful, leaves the row as it was.
func TestARailRowDropsASummaryThatSaysNothing(t *testing.T) {
	m := railModel(t, railSession("fix-login-bug", "", "claude", status.Idle, "", time.Hour))
	m.titles = map[string]string{"fix-login-bug": "Fix login bug"}
	if row := summaryRow(t, m, 100, "fix-login-bug"); strings.Contains(row, "Fix login bug") {
		t.Fatalf("the row repeats its name as a summary:\n%q", row)
	}
	m.titles["fix-login-bug"] = "Repair the OAuth callback"
	if row := summaryRow(t, m, 44, "fix-login-bug"); strings.Contains(row, "Repair") {
		t.Fatalf("a narrow rail still squeezed the summary in:\n%q", row)
	}
}

func summaryRow(t *testing.T, m *Model, width int, name string) string {
	t.Helper()
	for _, line := range m.railLines(width, m.listBodyHeight()) {
		if text := ansi.Strip(line.text); strings.Contains(text, name) {
			return unboxed.Replace(text)
		}
	}
	t.Fatalf("no rail line names %s", name)
	return ""
}
