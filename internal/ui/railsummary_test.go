package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/status"
)

// A list row says what its session is about beside the name once the
// conversation has a title; the opening prompt has a line of its own under it.
func TestARailRowCarriesTheSessionsSummary(t *testing.T) {
	m := railModel(t,
		railSession("api-work", "", "claude", status.Working, "add a token bucket limiter", time.Minute),
		railSession("other", "", "claude", status.Idle, "", time.Hour),
	)
	m.selectSessionRow(t, "other")
	m.rebuildRows()
	if row := summaryRow(t, m, 100, "api-work"); strings.Contains(row, "token bucket") {
		t.Fatalf("the opening prompt is still beside the name:\n%q", row)
	}
	m.titles = map[string]string{"api-work": "Rate limit the public API"}
	row := summaryRow(t, m, 100, "api-work")
	if !strings.Contains(row, "api-work  Rate limit the public API") {
		t.Fatalf("the title is not beside the name:\n%q", row)
	}
	if !strings.HasSuffix(strings.TrimRight(row, " "), "1m ago") {
		t.Fatalf("the summary pushed the meta off the edge:\n%q", row)
	}
}

// Every row with an opening prompt carries it on a dimmed line of its own,
// cut to sixty cells, whichever density the rail is drawn at.
func TestARailRowCarriesItsOpeningPromptBeneathIt(t *testing.T) {
	long := "rewrite the billing reconciliation job so it pages through invoices instead of loading them all"
	for _, stacked := range []bool{false, true} {
		m := railModel(t,
			railSession("api-work", "", "claude", status.Working, "add a token bucket limiter", time.Minute),
			railSession("billing", "", "claude", status.Idle, long, time.Hour),
			railSession("quiet", "", "claude", status.Idle, "", time.Hour),
		)
		m.comfortableRows = stacked
		m.selectSessionRow(t, "quiet")
		m.rebuildRows()
		lines := railTextAt(m, 100)
		if got := lineAfterRow(t, lines, "api-work", stacked); !strings.Contains(got, "add a token bucket limiter") {
			t.Fatalf("stacked=%v: no prompt line under the row:\n%s", stacked, strings.Join(lines, "\n"))
		}
		cut := textfmt.TruncateWidth(long, promptRowWidth, "…")
		if got := lineAfterRow(t, lines, "billing", stacked); !strings.Contains(got, cut) || strings.Contains(got, long) {
			t.Fatalf("stacked=%v: the prompt is not cut to %d cells:\n%q", stacked, promptRowWidth, got)
		}
		if got := lineAfterRow(t, lines, "quiet", stacked); strings.TrimSpace(unboxed.Replace(got)) != "" && !strings.Contains(got, "▔") {
			t.Fatalf("stacked=%v: a row with no prompt grew a line:\n%q", stacked, got)
		}
	}
}

// A row still waiting on its name wears the prompt as that name, so it does
// not say it a second time underneath.
func TestARowAwaitingItsNameSkipsThePromptLine(t *testing.T) {
	sess := railSession("claude-ab12", "", "claude", status.Starting, "add a token bucket limiter", time.Minute)
	sess.CreatedAt = time.Now()
	m := railModel(t, sess)
	m.awaitedRenames = map[string]awaitedRename{sess.ID: {generated: sess.Name, prompt: sess.LaunchPrompt}}
	if got := m.promptRow(sess); got != "" {
		t.Fatalf("an unnamed row still carries a prompt line: %q", got)
	}
	if h := m.entryHeight(treeRow{sess: sess}); h != 1 {
		t.Fatalf("an unnamed row is %d lines tall, want 1", h)
	}
}

// lineAfterRow is the line under a session's own lines: under the meta at the
// comfortable density, under the name otherwise.
func lineAfterRow(t *testing.T, lines []string, name string, stacked bool) string {
	t.Helper()
	skip := 1
	if stacked {
		skip = 2
	}
	for i, text := range lines {
		if strings.Contains(text, name) && i+skip < len(lines) {
			return lines[i+skip]
		}
	}
	t.Fatalf("no rail line names %s", name)
	return ""
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
