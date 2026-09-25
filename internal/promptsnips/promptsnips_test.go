package promptsnips

import (
	"fmt"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

func subs(text string, n int, at time.Time) []Submission {
	var out []Submission
	for i := range n {
		out = append(out, Submission{ID: fmt.Sprintf("%s-%d", text, i), Text: text, At: at})
	}
	return out
}

func keys(snips []Snippet) []string {
	var out []string
	for _, s := range snips {
		out = append(out, s.Key)
	}
	return out
}

func TestBuildThresholdIsStrictlyMoreThanThree(t *testing.T) {
	three := Build(subs("run the tests and fix failures", 3, now), now)
	if len(three) != 0 {
		t.Fatalf("3 occurrences became eligible: %v", keys(three))
	}
	four := Build(subs("run the tests and fix failures", 4, now), now)
	if len(four) != 1 || four[0].Count != 4 {
		t.Fatalf("4 occurrences: got %+v, want one snippet counted 4", four)
	}
}

func TestBuildCountsTranscriptCopiesOnce(t *testing.T) {
	var in []Submission
	for _, id := range []string{"a", "b", "c"} {
		// A resumed session and a fork both copy the turn with its ID.
		for range 3 {
			in = append(in, Submission{ID: id, Text: "open a ready-for-review PR", At: now})
		}
	}
	if got := Build(in, now); len(got) != 0 {
		t.Fatalf("3 submissions copied 3 times each became eligible: %+v", got)
	}
	in = append(in, Submission{ID: "d", Text: "open a ready-for-review PR", At: now})
	if got := Build(in, now); len(got) != 1 || got[0].Count != 4 {
		t.Fatalf("4 distinct IDs: got %+v", got)
	}
}

func TestBuildCountsEmptyIDsAsUnique(t *testing.T) {
	var in []Submission
	for range 4 {
		in = append(in, Submission{Text: "summarize the open threads", At: now})
	}
	if got := Build(in, now); len(got) != 1 || got[0].Count != 4 {
		t.Fatalf("got %+v", got)
	}
}

func TestBuildNormalizesAndKeepsNewestSpelling(t *testing.T) {
	in := []Submission{
		{ID: "1", Text: "Run  the tests", At: now.Add(-3 * time.Hour)},
		{ID: "2", Text: "run the TESTS", At: now.Add(-2 * time.Hour)},
		{ID: "3", Text: "  run the tests\t", At: now.Add(-4 * time.Hour)},
		{ID: "4", Text: "Run the tests!", At: now},
		{ID: "5", Text: "RUN THE TESTS", At: now.Add(-time.Hour)},
	}
	got := Build(in, now)
	if len(got) != 1 {
		t.Fatalf("got %+v, want one snippet", got)
	}
	if got[0].Key != "run the tests" || got[0].Count != 4 || got[0].Text != "RUN THE TESTS" {
		t.Fatalf("got %+v", got[0])
	}
}

func TestBuildCountsRecurringLinesInsideSubmissions(t *testing.T) {
	var in []Submission
	for i := range 4 {
		in = append(in, Submission{
			ID:   fmt.Sprint(i),
			Text: fmt.Sprintf("fix bug %d in the parser\nThen open a PR via create-pr.\nThen open a PR via create-pr.", i),
			At:   now,
		})
	}
	got := Build(in, now)
	if len(got) != 1 || got[0].Key != "then open a pr via create-pr." || got[0].Count != 4 {
		t.Fatalf("got %+v", got)
	}
}

func TestBuildIgnoresSubmissionsOutsideTheWindow(t *testing.T) {
	in := subs("deploy the staging stack", 3, now.Add(-Window-time.Hour))
	in = append(in, subs("deploy the staging stack", 1, now)...)
	in[len(in)-1].ID = "fresh"
	if got := Build(in, now); len(got) != 0 {
		t.Fatalf("stale submissions counted: %+v", got)
	}
	undated := subs("deploy the staging stack", 3, time.Time{})
	if got := Build(append(in[3:], undated...), now); len(got) != 1 || got[0].Count != 4 {
		t.Fatalf("undated submissions were not counted: %+v", got)
	}
}

func TestBuildDropsTooShortAndTooLong(t *testing.T) {
	long := make([]byte, MaxLength+1)
	for i := range long {
		long[i] = 'x'
	}
	in := append(subs("yes please", 5, now), subs(string(long), 5, now)...)
	if got := Build(in, now); len(got) != 0 {
		t.Fatalf("got %v", keys(got))
	}
}

func TestMatch(t *testing.T) {
	for _, tc := range []struct {
		key, input string
		want       Relevance
	}{
		{"run the tests and fix failures", "run the", PrefixMatch},
		{"run the tests and fix failures", "fix fail", WordMatch},
		{"run the tests and fix failures", "ix fail", NoMatch},
		{"run the tests and fix failures", "fix tests", TokenMatch},
		{"run the tests and fix failures", "fix deploy", NoMatch},
		{"run the tests", "run the tests", NoMatch},
		{"pre-merge checks pass", "merge", WordMatch},
		{"anything", "", NoMatch},
	} {
		if got := Match(tc.key, tc.input); got != tc.want {
			t.Errorf("Match(%q, %q) = %d, want %d", tc.key, tc.input, got, tc.want)
		}
	}
}

func TestSuggestRanksRelevanceThenFrequencyThenRecency(t *testing.T) {
	snips := []Snippet{
		{Key: "fix the flaky test", Text: "fix the flaky test", Count: 20, Last: now},
		{Key: "the tests pass locally", Text: "the tests pass locally", Count: 4, Last: now},
		{Key: "the tests need a fixture", Text: "the tests need a fixture", Count: 9, Last: now.Add(-time.Hour)},
		{Key: "the tests are green", Text: "the tests are green", Count: 9, Last: now},
		{Key: "unrelated snippet text", Text: "unrelated snippet text", Count: 50, Last: now},
	}
	got := keys(Suggest(snips, "The  tes", now, 10))
	want := []string{"the tests are green", "the tests need a fixture", "the tests pass locally", "fix the flaky test"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if got := Suggest(snips, "The  tes", now, 2); len(got) != 2 {
		t.Fatalf("limit ignored: %d", len(got))
	}
}

func TestSuggestDiscountsStaleFrequency(t *testing.T) {
	snips := []Snippet{
		{Key: "open the pr now", Count: 8, Last: now.Add(-3 * HalfLife)},
		{Key: "open the dashboard", Count: 4, Last: now},
	}
	if got := keys(Suggest(snips, "open the", now, 5)); len(got) != 2 || got[0] != "open the dashboard" {
		t.Fatalf("got %q", got)
	}
}

func TestSuggestNeedsTwoRunes(t *testing.T) {
	snips := []Snippet{{Key: "run the tests", Count: 4, Last: now}}
	for _, in := range []string{"", " ", "r"} {
		if got := Suggest(snips, in, now, 5); got != nil {
			t.Errorf("Suggest(%q) = %v", in, got)
		}
	}
	if got := Suggest(snips, "ru", now, 5); len(got) != 1 {
		t.Errorf("Suggest(ru) = %v", got)
	}
}

func BenchmarkSuggest(b *testing.B) {
	var snips []Snippet
	for i := range 2000 {
		key := fmt.Sprintf("snippet number %d about the tests and the deploy", i)
		snips = append(snips, Snippet{Key: key, Text: key, Count: 4 + i%10, Last: now})
	}
	for b.Loop() {
		Suggest(snips, "deploy", now, 8)
	}
}
