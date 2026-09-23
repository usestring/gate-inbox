package sessname

import (
	"strings"
	"testing"
)

func TestCompressKeepsTheDistinctiveNouns(t *testing.T) {
	cases := []struct {
		title string
		want  string
	}{
		{"Build regional payment reconciliation system", "regional-payment-reconciliation"},
		{"Investigate and reduce disk usage", "disk-usage"},
		{"Port event parser to sample organization", "event-parser-sample"},
		{"Fix media login with identity proxy", "media-login-identity"},
		{"Go gate inbox v2", "go-gate-inbox"},
		{"Review Linear issue ABC-129619", "linear-abc-129619"},
		{"realm hygiene fixes bundle (@general subagent)", "realm-hygiene-fixes"},
		{"Improving protocol verifier implementation", "improving-protocol-verifier"},
	}
	for _, tc := range cases {
		got := Kebab{}.Compress(tc.title)
		if len(got) == 0 {
			t.Errorf("%q compressed to nothing", tc.title)
			continue
		}
		if got[0] != tc.want {
			t.Errorf("%q -> %q, want %q", tc.title, got[0], tc.want)
		}
	}
}

func TestCompressCandidatesGrowAndStayWithinTheRail(t *testing.T) {
	got := Kebab{}.Compress("Port event parser to sample organization")
	if len(got) < 2 {
		t.Fatalf("candidates = %q, want room to disambiguate", got)
	}
	for i := 1; i < len(got); i++ {
		if got[i] == got[i-1] {
			t.Errorf("candidate %d repeats %q", i, got[i])
		}
	}
	for _, name := range got {
		if len(name) > maxLen {
			t.Errorf("candidate %q is %d chars, over the rail's %d", name, len(name), maxLen)
		}
		if strings.ContainsAny(name, " _.") || strings.ToLower(name) != name {
			t.Errorf("candidate %q is not kebab-case", name)
		}
	}
}

func TestCompressPutsTheDroppedVerbBackToBreakATie(t *testing.T) {
	got := Kebab{}.Compress("Port event parser")
	last := got[len(got)-1]
	if !strings.HasSuffix(last, "-port") {
		t.Errorf("candidates = %q, want the verb spent last", got)
	}
}

func TestCompressKeepsAGenericTitleRatherThanReturningNothing(t *testing.T) {
	got := Kebab{}.Compress("Update the code")
	if len(got) == 0 {
		t.Fatal("a title of nothing but generics named nothing")
	}
}

func TestAssignSpendsMoreTitleRatherThanACounter(t *testing.T) {
	entries := []Entry{
		{ID: "a", Title: "Claude feedbuilder API design"},
		{ID: "b", Title: "Claude feedbuilder storage layout"},
	}
	got := Assign(Kebab{}, entries, nil)
	if got["a"] == got["b"] {
		t.Fatalf("both named %q", got["a"])
	}
	for id, name := range got {
		if strings.HasSuffix(name, "-2") || strings.HasSuffix(name, "-3") {
			t.Errorf("%s = %q, want more of the title instead of a counter", id, name)
		}
	}
}

func TestAssignFallsBackToACounterOnlyForIdenticalTitles(t *testing.T) {
	entries := []Entry{
		{ID: "a", Title: "Slack thread discussion"},
		{ID: "b", Title: "Slack thread discussion"},
	}
	got := Assign(Kebab{}, entries, nil)
	if got["a"] == got["b"] {
		t.Fatalf("both named %q", got["a"])
	}
	if got["a"] != "slack-thread-discussion" || got["b"] != "slack-thread-discussion-2" {
		t.Errorf("got %q and %q", got["a"], got["b"])
	}
}

func TestAssignKeepsANameARowAlreadyHas(t *testing.T) {
	entries := []Entry{{ID: "a", Title: "Investigate and reduce disk usage", Current: "disk-usage-cleanup"}}
	// The current name has to be one the title still yields, or it is not the
	// title's name any more and the row is due a new one.
	if got := Assign(Kebab{}, entries, nil)["a"]; got != "disk-usage" {
		t.Errorf("got %q, want the title's own name", got)
	}
	entries[0].Current = "disk-usage"
	if got := Assign(Kebab{}, entries, nil)["a"]; got != "disk-usage" {
		t.Errorf("got %q, want the name kept", got)
	}
}

func TestAssignNeverTakesANameAlreadyOnTheBoard(t *testing.T) {
	entries := []Entry{{ID: "a", Title: "Investigate and reduce disk usage"}}
	taken := map[string]bool{"disk-usage": true}
	if got := Assign(Kebab{}, entries, taken)["a"]; got == "disk-usage" {
		t.Errorf("got %q, which another row is using", got)
	}
}

func TestAssignIsStableWhateverOrderTheRowsArriveIn(t *testing.T) {
	forward := []Entry{
		{ID: "a", Title: "Claude feedbuilder API design"},
		{ID: "b", Title: "Claude feedbuilder API contract"},
	}
	backward := []Entry{forward[1], forward[0]}
	first, second := Assign(Kebab{}, forward, nil), Assign(Kebab{}, backward, nil)
	for id := range first {
		if first[id] != second[id] {
			t.Errorf("%s = %q one way and %q the other", id, first[id], second[id])
		}
	}
}

func TestAssignFallsBackToTheDirectoryWhenThereIsNoTitle(t *testing.T) {
	entries := []Entry{{ID: "a", Title: "", Fallback: "sample-repo"}}
	if got := Assign(Kebab{}, entries, nil)["a"]; got != "sample-repo" {
		t.Errorf("got %q", got)
	}
}

// The row this pass could not name is the one most at risk: it is not in
// entries, so nothing claims its name on its behalf, and it goes on wearing it
// either way.
func TestAssignLeavesTheNameOfARowItCannotNameAlone(t *testing.T) {
	entries := []Entry{{ID: "b", Title: "Investigate and reduce disk usage"}}
	taken := map[string]bool{"disk-usage": true}
	if got := Assign(Kebab{}, entries, taken)["b"]; got == "disk-usage" {
		t.Errorf("got %q, which a row that could not be named is still using", got)
	}
}

func TestAssignKeepsACurrentNameEvenWhenTheWholeBoardIsPassedAsTaken(t *testing.T) {
	entries := []Entry{{ID: "a", Title: "Investigate and reduce disk usage", Current: "disk-usage"}}
	taken := map[string]bool{"disk-usage": true, "something-else": true}
	if got := Assign(Kebab{}, entries, taken)["a"]; got != "disk-usage" {
		t.Errorf("got %q, want the row to keep the name it already has", got)
	}
}
