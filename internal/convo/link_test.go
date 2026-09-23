package convo

import (
	"testing"
	"time"
)

func prose(text string) []string { return []string{Normalize(text)} }

const longProse = "the poller now hashes the activity region before it compares it, so a resize cannot read as streaming"

func TestProcessMatchNeedsNothingElse(t *testing.T) {
	panes := []Pane{{Key: "p1", Tool: "claude", Cwd: "/elsewhere", PIDs: []int{10, 11}}}
	convos := []Conversation{{Tool: "claude", ID: "c1", Cwd: "/repo", PID: 11}}
	got := Link(panes, convos)
	if len(got.Matched) != 1 || got.Matched["p1"].Conversation.ID != "c1" {
		t.Fatalf("link = %+v", got)
	}
	if !got.Matched["p1"].Has(SignalProcess) {
		t.Errorf("signals = %v", got.Matched["p1"].Signals)
	}
	if len(got.Unresolved) != 0 {
		t.Errorf("unresolved = %v", got.Unresolved)
	}
}

func TestDirectoryAloneIsNeverEnough(t *testing.T) {
	panes := []Pane{{Key: "p1", Tool: "claude", Cwd: "/repo", Text: "nothing in common"}}
	convos := []Conversation{
		{Tool: "claude", ID: "c1", Cwd: "/repo", Excerpts: prose(longProse)},
		{Tool: "claude", ID: "c2", Cwd: "/repo", Excerpts: prose("something else entirely and quite long too")},
	}
	got := Link(panes, convos)
	if len(got.Matched) != 0 {
		t.Fatalf("link = %+v, want nothing", got)
	}
	if len(got.Unresolved) != 1 || got.Unresolved[0] != "p1" {
		t.Errorf("unresolved = %v, want the pane reported back", got.Unresolved)
	}
}

func TestDirectoryAndProseTogetherLink(t *testing.T) {
	panes := []Pane{{Key: "p1", Tool: "claude", Cwd: "/repo", Text: Normalize("blah blah " + longProse + " blah")}}
	convos := []Conversation{{Tool: "claude", ID: "c1", Cwd: "/repo", Excerpts: prose(longProse)}}
	got := Link(panes, convos)
	if len(got.Matched) != 1 || !got.Matched["p1"].Has(SignalText) || !got.Matched["p1"].Has(SignalCwd) {
		t.Fatalf("link = %+v", got)
	}
}

func TestOneConversationNeverReachesTwoPanes(t *testing.T) {
	text := Normalize(longProse)
	panes := []Pane{
		{Key: "p1", Tool: "claude", Cwd: "/repo", Text: text},
		{Key: "p2", Tool: "claude", Cwd: "/repo", Text: text},
	}
	convos := []Conversation{{Tool: "claude", ID: "c1", Cwd: "/repo", Excerpts: prose(longProse)}}
	got := Link(panes, convos)
	if len(got.Matched) != 1 {
		t.Fatalf("link = %+v, want exactly one pane named", got)
	}
	seen := map[string]int{}
	for _, match := range got.Matched {
		seen[match.Conversation.ID]++
	}
	if seen["c1"] != 1 {
		t.Errorf("conversation handed out %d times", seen["c1"])
	}
}

func TestAnExactMatchOutranksAResemblance(t *testing.T) {
	text := Normalize(longProse)
	panes := []Pane{
		{Key: "p1", Tool: "claude", Cwd: "/repo", Text: text, PIDs: []int{7}},
		{Key: "p2", Tool: "claude", Cwd: "/repo", Text: text},
	}
	// p1 owns c1 by process. Taken pane-first, p1 would take c2 -- which it
	// merely resembles -- and leave p1's own conversation to p2.
	convos := []Conversation{
		{Tool: "claude", ID: "c2", Cwd: "/repo", Excerpts: prose(longProse)},
		{Tool: "claude", ID: "c1", Cwd: "/repo", PID: 7, Excerpts: prose(longProse)},
	}
	got := Link(panes, convos)
	if got.Matched["p1"].Conversation.ID != "c1" {
		t.Errorf("p1 = %q, want c1", got.Matched["p1"].Conversation.ID)
	}
	if got.Matched["p2"].Conversation.ID != "c2" {
		t.Errorf("p2 = %q, want c2", got.Matched["p2"].Conversation.ID)
	}
}

func TestADifferentToolNeverMatches(t *testing.T) {
	panes := []Pane{{Key: "p1", Tool: "opencode", Cwd: "/repo", PIDs: []int{11}}}
	convos := []Conversation{{Tool: "claude", ID: "c1", Cwd: "/repo", PID: 11}}
	if got := Link(panes, convos); len(got.Matched) != 0 {
		t.Fatalf("link = %+v, want nothing", got)
	}
}

func TestAPaneWithNoToolIsNeverNamed(t *testing.T) {
	panes := []Pane{{Key: "p1", Cwd: "/repo", PIDs: []int{11}}}
	convos := []Conversation{{Tool: "claude", ID: "c1", Cwd: "/repo", PID: 11}}
	if got := Link(panes, convos); len(got.Matched) != 0 {
		t.Fatalf("link = %+v, want nothing", got)
	}
}

func TestASoleCandidateInASoleDirectoryLinks(t *testing.T) {
	panes := []Pane{
		{Key: "p1", Tool: "opencode", Cwd: "/only"},
		{Key: "p2", Tool: "opencode", Cwd: "/shared"},
		{Key: "p3", Tool: "opencode", Cwd: "/shared"},
	}
	convos := []Conversation{
		{Tool: "opencode", ID: "c1", Cwd: "/only", UpdatedAt: time.Now()},
		{Tool: "opencode", ID: "c2", Cwd: "/shared", UpdatedAt: time.Now()},
		{Tool: "opencode", ID: "c3", Cwd: "/shared", UpdatedAt: time.Now()},
	}
	got := Link(panes, convos)
	if len(got.Matched) != 1 || got.Matched["p1"].Conversation.ID != "c1" {
		t.Fatalf("link = %+v, want only the sole pane in the sole directory", got)
	}
	if !got.Matched["p1"].Has(SignalSolo) {
		t.Errorf("signals = %v", got.Matched["p1"].Signals)
	}
	if len(got.Unresolved) != 2 {
		t.Errorf("unresolved = %v, want both panes in the shared directory", got.Unresolved)
	}
}

func TestLinkIsStableAcrossRuns(t *testing.T) {
	text := Normalize(longProse)
	panes := []Pane{
		{Key: "p1", Tool: "claude", Cwd: "/repo", Text: text},
		{Key: "p2", Tool: "claude", Cwd: "/repo", Text: text},
	}
	convos := []Conversation{
		{Tool: "claude", ID: "c1", Cwd: "/repo", Excerpts: prose(longProse)},
		{Tool: "claude", ID: "c2", Cwd: "/repo", Excerpts: prose(longProse)},
	}
	first := Link(panes, convos)
	for i := 0; i < 20; i++ {
		again := Link(panes, convos)
		if len(again.Matched) != len(first.Matched) {
			t.Fatalf("run %d linked %d, first linked %d", i, len(again.Matched), len(first.Matched))
		}
		for key, match := range first.Matched {
			if again.Matched[key].Conversation.ID != match.Conversation.ID {
				t.Fatalf("run %d moved %s from %s to %s", i, key, match.Conversation.ID, again.Matched[key].Conversation.ID)
			}
		}
	}
}

// The transcript path travels with the match because attribution is the
// expensive half of the problem: a second caller that needs the file itself --
// to read a session's usage records rather than to name it -- must not have to
// solve the mapping again and risk answering it differently.
func TestAMatchCarriesTheTranscriptPath(t *testing.T) {
	panes := []Pane{{Key: "p1", Tool: "claude", Cwd: "/repo", PIDs: []int{11}}}
	convos := []Conversation{{Tool: "claude", ID: "c1", Cwd: "/repo", PID: 11, TranscriptPath: "/home/dev/.claude/projects/-repo/c1.jsonl"}}
	got, ok := Link(panes, convos).For("p1")
	if !ok {
		t.Fatal("not linked")
	}
	if got.Conversation.TranscriptPath != "/home/dev/.claude/projects/-repo/c1.jsonl" {
		t.Errorf("transcript = %q", got.Conversation.TranscriptPath)
	}
}

// The rule the naive mapper gets wrong, and the reason this one exists: on the
// board this was built against, 23 of 26 agent panes share one directory. A
// mapper that resolves on newest-transcript-in-the-cwd either names them all
// after one conversation or, if it refuses ties, resolves none of them.
func TestSharingADirectoryResolvesNothingOnItsOwn(t *testing.T) {
	var panes []Pane
	var convos []Conversation
	for i := 0; i < 23; i++ {
		key := string(rune('a' + i))
		panes = append(panes, Pane{Key: key, Tool: "claude", Cwd: "/repo"})
		convos = append(convos, Conversation{Tool: "claude", ID: "c" + key, Cwd: "/repo"})
	}
	got := Link(panes, convos)
	if len(got.Matched) != 0 {
		t.Errorf("matched %d panes on a shared directory alone", len(got.Matched))
	}
	if len(got.Unresolved) != len(panes) {
		t.Errorf("unresolved = %d, want all %d reported back", len(got.Unresolved), len(panes))
	}
}

// The same board, with each pane's own agent process found in its tree, is
// resolved completely.
func TestASharedDirectoryIsNoObstacleToTheProcessSignal(t *testing.T) {
	var panes []Pane
	var convos []Conversation
	for i := 0; i < 23; i++ {
		key := string(rune('a' + i))
		panes = append(panes, Pane{Key: key, Tool: "claude", Cwd: "/repo", PIDs: []int{1000 + i}})
		convos = append(convos, Conversation{Tool: "claude", ID: "c" + key, Cwd: "/repo", PID: 1000 + i})
	}
	got := Link(panes, convos)
	if len(got.Matched) != len(panes) || len(got.Unresolved) != 0 {
		t.Fatalf("matched %d of %d, unresolved %v", len(got.Matched), len(panes), got.Unresolved)
	}
	for _, pane := range panes {
		if got.Matched[pane.Key].Conversation.ID != "c"+pane.Key {
			t.Errorf("%s got %q", pane.Key, got.Matched[pane.Key].Conversation.ID)
		}
	}
}

// A pane whose launch the caller owns names its own conversation, so nothing
// has to be read out of the pane at all.
func TestALaunchedWithIDNeedsNothingElse(t *testing.T) {
	panes := []Pane{{Key: "p1", Tool: "claude", AgentID: "c1"}}
	convos := []Conversation{
		{Tool: "claude", ID: "c0", Cwd: "/repo", Excerpts: prose(longProse)},
		{Tool: "claude", ID: "c1", Cwd: "/repo"},
	}
	got := Link(panes, convos)
	if len(got.Matched) != 1 || got.Matched["p1"].Conversation.ID != "c1" {
		t.Fatalf("link = %+v, want the conversation the pane launched with", got)
	}
	if !got.Matched["p1"].Has(SignalAgentID) {
		t.Errorf("signals = %v", got.Matched["p1"].Signals)
	}
}

// The id is an identity, so it outranks a pane that merely shows the same
// words: the launched pane keeps its own conversation.
func TestALaunchedWithIDOutranksProse(t *testing.T) {
	panes := []Pane{
		{Key: "guess", Tool: "claude", Cwd: "/repo", Text: Normalize(longProse)},
		{Key: "launched", Tool: "claude", Cwd: "/repo", AgentID: "c1"},
	}
	convos := []Conversation{{Tool: "claude", ID: "c1", Cwd: "/repo", Excerpts: prose(longProse)}}
	got := Link(panes, convos)
	if len(got.Matched) != 1 || got.Matched["launched"].Conversation.ID != "c1" {
		t.Fatalf("link = %+v, want the launched pane to keep its own conversation", got)
	}
}

// An id no conversation carries is worth nothing on its own, the way every
// other weak signal is.
func TestAnUnknownLaunchedWithIDLinksNothing(t *testing.T) {
	panes := []Pane{{Key: "p1", Tool: "claude", Cwd: "/repo", AgentID: "gone", Text: "nothing in common"}}
	convos := []Conversation{
		{Tool: "claude", ID: "c1", Cwd: "/repo", Excerpts: prose(longProse)},
		{Tool: "claude", ID: "c2", Cwd: "/repo", Excerpts: prose("something else entirely and quite long too")},
	}
	got := Link(panes, convos)
	if len(got.Matched) != 0 {
		t.Fatalf("link = %+v, want nothing", got)
	}
}
