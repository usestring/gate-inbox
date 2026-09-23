package namesweep

import (
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/promptcache"
	"github.com/usestring/gate-inbox/internal/status"
)

var now = time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)

// warm is a cache state comfortably inside the long TTL. Each carries its
// own transcript path, since two candidates sharing one is itself a refusal.
func warm(id string) promptcache.State {
	return promptcache.State{
		Known:         true,
		LastTurnAt:    now.Add(-4 * time.Minute),
		TTL:           promptcache.TTLLong,
		ContextTokens: 301_224,
		Transcript:    "/t/" + id + ".jsonl",
	}
}

func cold(id string) promptcache.State {
	return promptcache.State{
		Known:         true,
		LastTurnAt:    now.Add(-45 * time.Minute),
		TTL:           promptcache.TTLShort,
		ContextTokens: 180_000,
		Transcript:    "/t/" + id + ".jsonl",
	}
}

func adopted(name, state string) Candidate {
	return Candidate{
		ID:            "id-" + name,
		Name:          name,
		Tool:          "claude",
		Cwd:           "/repo/" + name,
		Status:        state,
		Adopted:       true,
		CacheReadable: true,
	}
}

func build(t *testing.T, candidates []Candidate, states map[string]promptcache.State) Plan {
	t.Helper()
	return Build(candidates,
		func(c Candidate) promptcache.State { return states[c.ID] },
		func(c Candidate) string { return "rename " + c.ID },
		now)
}

func targetNames(plan Plan) []string {
	names := make([]string, 0, len(plan.Targets))
	for _, target := range plan.Targets {
		names = append(names, target.Name)
	}
	return names
}

func reasonFor(plan Plan, name string) string {
	for _, verdict := range append(append([]Verdict{}, plan.Targets...), plan.Skipped...) {
		if verdict.Name == name {
			return verdict.Reason
		}
	}
	return ""
}

// Gate 1. A pane showing a permission dialog reads as waiting, and typing
// into it answers the dialog. Nothing but idle may be a target.
func TestGateOneRefusesEveryPaneThatIsNotAtAnIdlePrompt(t *testing.T) {
	states := map[string]promptcache.State{}
	var candidates []Candidate
	for _, state := range []string{status.Waiting, status.Working, status.Starting, status.Dead, status.Errored, status.Finished} {
		candidate := adopted(state+"-pane", state)
		candidates = append(candidates, candidate)
		states[candidate.ID] = warm(candidate.ID)
	}
	idle := adopted("idle-pane", status.Idle)
	candidates = append(candidates, idle)
	states[idle.ID] = warm(idle.ID)

	plan := build(t, candidates, states)
	if got := targetNames(plan); len(got) != 1 || got[0] != "idle-pane" {
		t.Fatalf("only the idle pane may be a target, got %v", got)
	}
	if reason := reasonFor(plan, status.Waiting+"-pane"); !strings.Contains(reason, "answer whatever dialog") {
		t.Fatalf("a waiting pane must be refused for the dialog it would answer, got %q", reason)
	}
}

// Gate 2. An expired prompt cache means a send re-creates the whole context
// at full price, so a cold session is left alone however idle it is.
func TestGateTwoRefusesAColdSession(t *testing.T) {
	hot, chilled := adopted("hot", status.Idle), adopted("chilled", status.Idle)
	plan := build(t, []Candidate{hot, chilled}, map[string]promptcache.State{
		hot.ID:     warm(hot.ID),
		chilled.ID: cold(chilled.ID),
	})
	if got := targetNames(plan); len(got) != 1 || got[0] != "hot" {
		t.Fatalf("only the warm session may be a target, got %v", got)
	}
	reason := reasonFor(plan, "chilled")
	if !strings.Contains(reason, "cold: 5m TTL expired 40m ago") || !strings.Contains(reason, "180k ctx") {
		t.Fatalf("a cold skip must say which TTL lapsed, when, and what a send would cost, got %q", reason)
	}
}

// The margin is the whole point of Gate 2 being a forecast rather than a
// reading: a session that expires while the sweep is still staggering must
// not be in the set the operator approves.
func TestGateTwoRefusesASessionInsideTheSafetyMargin(t *testing.T) {
	expiring := adopted("expiring", status.Idle)
	state := promptcache.State{
		Known:         true,
		LastTurnAt:    now.Add(-4*time.Minute - 45*time.Second),
		TTL:           promptcache.TTLShort,
		ContextTokens: 42_000,
		Transcript:    "/t/expiring.jsonl",
	}
	plan := build(t, []Candidate{expiring}, map[string]promptcache.State{expiring.ID: state})
	if len(plan.Targets) != 0 {
		t.Fatalf("a session 15s from expiry must not be a target, got %v", targetNames(plan))
	}
	if reason := reasonFor(plan, "expiring"); !strings.Contains(reason, "safety margin") {
		t.Fatalf("the skip must name the margin, got %q", reason)
	}
}

func TestAToolWithNoReadableCacheIsNeverATarget(t *testing.T) {
	candidate := adopted("codex-pane", status.Idle)
	candidate.Tool = "codex"
	candidate.CacheReadable = false
	plan := build(t, []Candidate{candidate}, map[string]promptcache.State{candidate.ID: warm(candidate.ID)})
	if len(plan.Targets) != 0 {
		t.Fatalf("an unpriceable tool must not be a target, got %v", targetNames(plan))
	}
	if reason := reasonFor(plan, "codex-pane"); !strings.Contains(reason, "unpriceable") {
		t.Fatalf("got %q", reason)
	}
}

func TestAMissingTranscriptIsTreatedAsCold(t *testing.T) {
	candidate := adopted("unknown", status.Idle)
	plan := build(t, []Candidate{candidate}, map[string]promptcache.State{candidate.ID: {}})
	if len(plan.Targets) != 0 {
		t.Fatalf("an unknown price must not be a target, got %v", targetNames(plan))
	}
}

// Two panes in one directory both resolve to the newest transcript there,
// so its warmth describes at most one of them. Neither is priced, so neither
// is sent to.
func TestTwoPanesResolvingToOneTranscriptAreBothRefused(t *testing.T) {
	first, second := adopted("first", status.Idle), adopted("second", status.Idle)
	first.Cwd, second.Cwd = "/repo/shared", "/repo/shared"
	shared := warm("shared")
	plan := build(t, []Candidate{first, second}, map[string]promptcache.State{
		first.ID:  shared,
		second.ID: shared,
	})
	if len(plan.Targets) != 0 {
		t.Fatalf("a shared transcript prices neither pane, got %v", targetNames(plan))
	}
	if reason := reasonFor(plan, "first"); !strings.Contains(reason, "ambiguous") {
		t.Fatalf("got %q", reason)
	}
}

func TestOnlyAdoptedLiveAgentPanesAreInScope(t *testing.T) {
	managed := adopted("managed", status.Idle)
	managed.Adopted = false
	archived := adopted("archived", status.Idle)
	archived.Archived = true
	shell := adopted("shell", status.Idle)
	shell.Shell = true
	kept := adopted("kept", status.Idle)

	states := map[string]promptcache.State{}
	for _, c := range []Candidate{managed, archived, shell, kept} {
		states[c.ID] = warm("shared")
	}
	// Every out-of-scope row prices the same transcript as the kept one
	// would if scope were not checked first, which would trip the ambiguity
	// rule and hide a scope bug behind it.
	plan := build(t, []Candidate{managed, archived, shell, kept}, states)
	if got := targetNames(plan); len(got) != 1 || got[0] != "kept" {
		t.Fatalf("only an adopted live agent pane is in scope, got %v", got)
	}
	if plan.OutOfScope != 3 {
		t.Fatalf("out-of-scope count = %d, want 3", plan.OutOfScope)
	}
	if len(plan.Skipped) != 0 {
		t.Fatalf("out-of-scope rows are not skips, got %d", len(plan.Skipped))
	}
}

func TestATargetCarriesTheExactTextItWouldReceive(t *testing.T) {
	candidate := adopted("named", status.Idle)
	plan := build(t, []Candidate{candidate}, map[string]promptcache.State{candidate.ID: warm(candidate.ID)})
	if len(plan.Targets) != 1 || plan.Targets[0].Directive != "rename id-named" {
		t.Fatalf("target must carry its own directive, got %+v", plan.Targets)
	}
	if reason := plan.Targets[0].Reason; !strings.Contains(reason, "warm: 1h TTL") || !strings.Contains(reason, "301k ctx") {
		t.Fatalf("a target must say why it is warm and what it holds, got %q", reason)
	}
}

func TestSkippedRowsCarryNoDirective(t *testing.T) {
	candidate := adopted("busy", status.Working)
	plan := build(t, []Candidate{candidate}, map[string]promptcache.State{candidate.ID: warm(candidate.ID)})
	if len(plan.Skipped) != 1 || plan.Skipped[0].Directive != "" {
		t.Fatalf("a skipped row must carry no text to send, got %+v", plan.Skipped)
	}
}

func TestTokensReadsAtAGlance(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want string
	}{
		{301_224, "301k"}, {1_400_000, "1.4M"}, {900, "900"},
	} {
		if got := Tokens(tc.in); got != tc.want {
			t.Errorf("Tokens(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
