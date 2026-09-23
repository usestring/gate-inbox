package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/namesweep"
	"github.com/usestring/gate-inbox/internal/promptcache"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// seedSweepBoard puts a board of adopted and managed rows on the model
// without touching tmux: the plan is built from the model, not from panes.
func seedSweepBoard(m *Model, sessions ...store.Session) {
	m.sessions = sessions
}

func sweepRowFor(name, state, cwd string) store.Session {
	return store.Session{
		ID:         "id" + name,
		Name:       name,
		Tool:       "claude-hooked",
		Cwd:        cwd,
		Status:     state,
		TmuxPaneID: "%1",
		TmuxSocket: "default",
	}
}

func TestNameSweepCandidatesReadTheBoardTheGatesNeedToSee(t *testing.T) {
	m := buildModel(t)
	managed := sweepRowFor("managed", status.Idle, "/repo/a")
	managed.TmuxPaneID = ""
	shell := sweepRowFor("shell", status.Idle, "/repo/b")
	shell.Tool = "terminal"
	unpriceable := sweepRowFor("plain", status.Idle, "/repo/c")
	unpriceable.Tool = "claude"
	seedSweepBoard(m, sweepRowFor("adopted", status.Idle, "/repo/d"), managed, shell, unpriceable)

	by := map[string]namesweep.Candidate{}
	for _, candidate := range m.nameSweepCandidates() {
		by[candidate.Name] = candidate
	}
	if !by["adopted"].Adopted || !by["adopted"].CacheReadable {
		t.Fatalf("an adopted claude pane must be adopted and priceable: %+v", by["adopted"])
	}
	if by["managed"].Adopted {
		t.Fatal("a session the manager started is not adopted")
	}
	if !by["shell"].Shell {
		t.Fatal("a terminal tab must read as a shell")
	}
	if by["plain"].CacheReadable {
		t.Fatal("only a tool that writes cache numbers is priceable")
	}
}

// The directive an adopted pane receives has to work in a shell that
// inherited nothing from the manager.
func TestTheDirectiveCarriesTheSessionIDAndTheConfigDirectory(t *testing.T) {
	m := buildModel(t)
	directive := m.nameSweepDirective()(namesweep.Candidate{ID: "1b364bb3"})
	if !strings.Contains(directive, "GATE_INBOX_SESSION_ID='1b364bb3'") {
		t.Fatalf("got %q", directive)
	}
	if !strings.Contains(directive, "GATE_INBOX_HOME='"+filepath.Dir(m.hooks.Dir())+"'") {
		t.Fatalf("got %q", directive)
	}
	if !strings.Contains(directive, `rename "<name>"`) {
		t.Fatalf("got %q", directive)
	}
}

// writeCacheFixture plants a transcript so a session prices as warm or cold.
func writeCacheFixture(t *testing.T, root, cwd string, age time.Duration, ttl time.Duration, ctx int) {
	t.Helper()
	dir := promptcache.NewReader(root).ProjectDir(cwd)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	oneHour, fiveMinute := 0, 400
	if ttl == promptcache.TTLLong {
		oneHour, fiveMinute = 400, 0
	}
	line := assistantLineFor(time.Now().Add(-age), ctx, oneHour, fiveMinute)
	if err := os.WriteFile(filepath.Join(dir, "conv.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assistantLineFor(stamp time.Time, cacheRead, oneHour, fiveMinute int) string {
	return `{"type":"assistant","timestamp":"` + stamp.UTC().Format(time.RFC3339Nano) +
		`","message":{"usage":{"cache_read_input_tokens":` + itoa(cacheRead) +
		`,"cache_creation_input_tokens":` + itoa(oneHour+fiveMinute) +
		`,"cache_creation":{"ephemeral_1h_input_tokens":` + itoa(oneHour) +
		`,"ephemeral_5m_input_tokens":` + itoa(fiveMinute) + `}}}}`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// planFixture builds the dry run against a planted transcript root and
// returns the rendered card as plain text.
func planFixture(t *testing.T) (*Model, string) {
	t.Helper()
	root := t.TempDir()
	writeCacheFixture(t, root, "/repo/warm-one", 4*time.Minute, promptcache.TTLLong, 301_224)
	writeCacheFixture(t, root, "/repo/warm-two", 40*time.Second, promptcache.TTLShort, 88_000)
	writeCacheFixture(t, root, "/repo/stale", 45*time.Minute, promptcache.TTLShort, 180_000)
	writeCacheFixture(t, root, "/repo/dialog", time.Minute, promptcache.TTLLong, 42_000)
	writeCacheFixture(t, root, "/repo/busy", 30*time.Second, promptcache.TTLLong, 120_000)

	m := buildModel(t)
	managed := sweepRowFor("launched-by-manager", status.Idle, "/repo/warm-one")
	managed.TmuxPaneID = ""
	seedSweepBoard(m,
		sweepRowFor("sample-repo-http-gateway", status.Idle, "/repo/warm-one"),
		sweepRowFor("mogul-landing", status.Idle, "/repo/warm-two"),
		sweepRowFor("feed-repair", status.Waiting, "/repo/dialog"),
		sweepRowFor("protocol-fixture", status.Idle, "/repo/stale"),
		sweepRowFor("search-service-roll", status.Working, "/repo/busy"),
		managed,
	)

	reader := promptcache.NewReader(root)
	plan := namesweep.Build(m.nameSweepCandidates(),
		func(c namesweep.Candidate) promptcache.State { return reader.Lookup(c.Cwd, c.AgentSessionID) },
		m.nameSweepDirective(), time.Now())
	m.nameSweep = nameSweepState{plan: plan}
	m.mode = modeNameSweep
	return m, ansi.Strip(m.frame())
}

func TestTheDryRunShowsEveryPaneAndWhatWouldHappenToIt(t *testing.T) {
	m, rendered := planFixture(t)
	t.Log("\n" + rendered)

	for _, want := range []string{
		"Name sweep — dry run",
		"sample-repo-http-gateway",
		"warm: 1h TTL",
		"301k ctx",
		"mogul-landing",
		"feed-repair",
		"answer whatever dialog",
		"protocol-fixture",
		"cold: 5m TTL expired 40m ago",
		"180k ctx",
		"search-service-roll",
		"mid-turn",
		"1 managed, archived or shell row is not in scope",
		"y/↵ send",
		"n/esc cancel",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the dry run must say %q", want)
		}
	}
	if targets := len(m.nameSweep.plan.Targets); targets != 2 {
		t.Fatalf("two panes are idle and warm, got %d", targets)
	}
}

// The dry run sends nothing. Opening it and rendering it must not reach tmux
// with a single keystroke.
func TestOpeningTheSweepSendsNothing(t *testing.T) {
	m := buildModel(t)
	seedSweepBoard(m, sweepRowFor("a", status.Idle, "/repo/a"))
	cmd := m.openNameSweep()
	if m.mode != modeNameSweep || !m.nameSweep.building {
		t.Fatalf("N opens the dry run, got mode=%v building=%v", m.mode, m.nameSweep.building)
	}
	msg := cmd()
	plan, ok := msg.(nameSweepPlanMsg)
	if !ok {
		t.Fatalf("got %T, want a plan", msg)
	}
	if plan.plan.Empty() && len(plan.plan.Skipped) == 0 {
		t.Fatal("the plan must account for the board")
	}
	if m.nameSweep.result != nil {
		t.Fatal("nothing may have been sent")
	}
}

func TestEnterOnAnEmptyPlanSendsNothing(t *testing.T) {
	m := buildModel(t)
	m.mode = modeNameSweep
	m.nameSweep = nameSweepState{}
	if _, cmd := m.handleNameSweepKey(tea.KeyPressMsg{Code: 'y', Text: "y"}); cmd != nil {
		t.Fatal("an empty plan has nothing to send")
	}
}

func TestEscapeDuringASweepStopsItRatherThanClosingTheCard(t *testing.T) {
	m := buildModel(t)
	m.mode = modeNameSweep
	stop := make(chan struct{})
	m.nameSweep = nameSweepState{sending: true, stop: stop}
	m.handleNameSweepKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	select {
	case <-stop:
	default:
		t.Fatal("esc must end the sweep in flight")
	}
	if m.mode != modeNameSweep {
		t.Fatal("the card stays up until the sweep reports back")
	}
}

func TestTheResultCardReportsWhatWasAskedAndWhatWasHeld(t *testing.T) {
	m := buildModel(t)
	m.mode = modeNameSweep
	held := namesweep.Verdict{Candidate: namesweep.Candidate{Name: "feed-repair"}, Reason: "changed since the plan — waiting"}
	m.applyNameSweepResult(nameSweepDoneMsg{result: namesweep.Result{
		Sent: []namesweep.Verdict{{Candidate: namesweep.Candidate{Name: "sample-task"}}},
		Held: []namesweep.Verdict{held},
	}})
	rendered := ansi.Strip(m.frame())
	t.Log("\n" + rendered)
	for _, want := range []string{"Name sweep — done", "sent · 1", "sample-task", "held at the door · 1", "feed-repair"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the result must say %q", want)
		}
	}
}

func TestNPressOpensTheSweepFromTheList(t *testing.T) {
	m := buildModel(t)
	seedSweepBoard(m, sweepRowFor("a", status.Idle, "/repo/a"))
	m.handleKey(tea.KeyPressMsg{Code: 'N', Text: "N"})
	if m.mode != modeNameSweep {
		t.Fatalf("N must open the sweep, got mode %v", m.mode)
	}
}
