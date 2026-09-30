package sessioncmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

const childCallID = "toolu_child_ask"

var (
	relayStart = time.Date(2026, 9, 28, 19, 50, 0, 0, time.UTC)
	gitleaks   = convo.AskQuestion{Header: "Approval",
		Question: "May I append the allowlist entry for testdata/fixture.key to ci/.gitleaks.toml?",
		Options:  []convo.AskOption{{Label: "Yes, append it"}, {Label: "No"}}}
)

// transcript builds a Claude Code JSONL transcript line by line.
type transcript struct{ lines []string }

func (tr *transcript) ask(id string, at time.Time, questions ...convo.AskQuestion) *transcript {
	tr.add(map[string]any{
		"type":      "assistant",
		"timestamp": at.Format(time.RFC3339Nano),
		"message": map[string]any{"role": "assistant", "content": []any{map[string]any{
			"type": "tool_use", "id": id, "name": "AskUserQuestion",
			"input": map[string]any{"questions": questions},
		}}},
	})
	return tr
}

func (tr *transcript) answer(id string, at time.Time, answers map[string]string) *transcript {
	tr.add(map[string]any{
		"type":      "user",
		"timestamp": at.Format(time.RFC3339Nano),
		"message": map[string]any{"role": "user", "content": []any{map[string]any{
			"type": "tool_result", "tool_use_id": id, "content": "The user answered.",
		}}},
		"toolUseResult": map[string]any{"answers": answers},
	})
	return tr
}

func (tr *transcript) add(record map[string]any) {
	raw, err := json.Marshal(record)
	if err != nil {
		panic(err)
	}
	tr.lines = append(tr.lines, string(raw))
}

func (tr *transcript) write(t *testing.T, home, conversation string) {
	t.Helper()
	path := filepath.Join(home, "projects", "any-project", conversation+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(tr.lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// relayFixture is a parent and a child, each with a transcript, and the
// store the ledger lives in.
type relayFixture struct {
	sessions *Sessions
	store    *store.Store
	home     string
	parent   store.Session
	child    store.Session
}

func newRelayFixture(t *testing.T, childQuestions ...convo.AskQuestion) *relayFixture {
	t.Helper()
	st, err := store.Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	home := t.TempDir()
	f := &relayFixture{
		sessions: &Sessions{claudeHome: home},
		store:    st,
		home:     home,
		parent:   store.Session{ID: "parent01", AgentSessionID: "conv-parent"},
		child:    store.Session{ID: "child001", AgentSessionID: "conv-child", SpawnedBy: "parent01"},
	}
	(&transcript{}).ask(childCallID, relayStart, childQuestions...).write(t, home, "conv-child")
	return f
}

func (f *relayFixture) parentSays(t *testing.T, tr *transcript) {
	t.Helper()
	tr.write(t, f.home, "conv-parent")
}

func (f *relayFixture) guard(relay bool) *answerGuard {
	return f.sessions.guard(f.store, f.parent, f.child, relay)
}

func (f *relayFixture) rows(t *testing.T) []store.DialogAnswer {
	t.Helper()
	rows, err := f.store.AnswersFor(f.child.ID, childCallID)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestRelayAcceptsTheUsersExactAnswer(t *testing.T) {
	f := newRelayFixture(t, gitleaks)
	f.parentSays(t, (&transcript{}).
		ask("toolu_parent", relayStart.Add(time.Minute), gitleaks).
		answer("toolu_parent", relayStart.Add(2*time.Minute), map[string]string{gitleaks.Question: "Yes, append it"}))
	g := f.guard(true)
	if err := g.admit([]plannedAnswer{{0, "Yes, append it"}}, nil); err != nil {
		t.Fatalf("admit: %v", err)
	}
	g.finish(nil)
	rows := f.rows(t)
	if len(rows) != 1 || rows[0].Mode != store.AnswerRelayedUser || rows[0].EvidenceToolUseID != "toolu_parent" ||
		rows[0].State != store.AnswerKeyed || rows[0].BySession != "parent01" {
		t.Fatalf("ledger = %+v, want one keyed relayed_user row citing toolu_parent", rows)
	}
}

func TestRelayRefusals(t *testing.T) {
	paraphrased := gitleaks
	paraphrased.Question = "Can I add the fixture key to the gitleaks allowlist?"
	otherOptions := gitleaks
	otherOptions.Options = []convo.AskOption{{Label: "Yes"}, {Label: "No"}}
	cases := []struct {
		name   string
		parent *transcript
		answer string
		setup  func(t *testing.T, f *relayFixture)
		want   string
	}{
		{
			name: "paraphrased question",
			parent: (&transcript{}).ask("toolu_parent", relayStart.Add(time.Minute), paraphrased).
				answer("toolu_parent", relayStart.Add(2*time.Minute), map[string]string{paraphrased.Question: "Yes, append it"}),
			answer: "Yes, append it",
			want:   "word for word",
		},
		{
			name: "different options",
			parent: (&transcript{}).ask("toolu_parent", relayStart.Add(time.Minute), otherOptions).
				answer("toolu_parent", relayStart.Add(2*time.Minute), map[string]string{gitleaks.Question: "Yes"}),
			answer: "Yes",
			want:   "word for word",
		},
		{
			name: "different answer",
			parent: (&transcript{}).ask("toolu_parent", relayStart.Add(time.Minute), gitleaks).
				answer("toolu_parent", relayStart.Add(2*time.Minute), map[string]string{gitleaks.Question: "No"}),
			answer: "Yes, append it",
			want:   `your user answered "No"`,
		},
		{
			name: "answered before the child asked",
			parent: (&transcript{}).ask("toolu_parent", relayStart.Add(-2*time.Minute), gitleaks).
				answer("toolu_parent", relayStart.Add(-time.Minute), map[string]string{gitleaks.Question: "Yes, append it"}),
			answer: "Yes, append it",
			want:   "before the child asked",
		},
		{
			name: "evidence already used",
			parent: (&transcript{}).ask("toolu_parent", relayStart.Add(time.Minute), gitleaks).
				answer("toolu_parent", relayStart.Add(2*time.Minute), map[string]string{gitleaks.Question: "Yes, append it"}),
			answer: "Yes, append it",
			setup: func(t *testing.T, f *relayFixture) {
				if _, err := f.store.RecordAnswer(store.DialogAnswer{TargetSession: "child002", TargetToolUseID: "toolu_other",
					QuestionHash: questionHash(gitleaks), Answer: "Yes, append it", BySession: "parent01",
					Mode: store.AnswerRelayedUser, EvidenceToolUseID: "toolu_parent"}); err != nil {
					t.Fatal(err)
				}
			},
			want: "already been relayed once",
		},
		{
			name: "evidence Gate Inbox typed into the parent",
			parent: (&transcript{}).ask("toolu_parent", relayStart.Add(time.Minute), gitleaks).
				answer("toolu_parent", relayStart.Add(2*time.Minute), map[string]string{gitleaks.Question: "Yes, append it"}),
			answer: "Yes, append it",
			setup: func(t *testing.T, f *relayFixture) {
				if _, err := f.store.RecordAnswer(store.DialogAnswer{TargetSession: "parent01", TargetToolUseID: "toolu_parent",
					QuestionHash: questionHash(gitleaks), Answer: "Yes, append it", BySession: "grandparent",
					Mode: store.AnswerByAgent}); err != nil {
					t.Fatal(err)
				}
			},
			want: "not by your user",
		},
		{
			name:   "no dialog in the parent at all",
			parent: &transcript{},
			answer: "Yes, append it",
			want:   "word for word",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRelayFixture(t, gitleaks)
			f.parentSays(t, tc.parent)
			if tc.setup != nil {
				tc.setup(t, f)
			}
			err := f.guard(true).admit([]plannedAnswer{{0, tc.answer}}, nil)
			if !errors.Is(err, errRelayRefused) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("admit err = %v, want a relay refusal saying %q", err, tc.want)
			}
			if rows := f.rows(t); len(rows) != 0 {
				t.Fatalf("a refused relay wrote %+v", rows)
			}
		})
	}
}

// A relayed user answer in the parent -- its own spawner relaying its user --
// is still the user's, so a relay chains through a grandparent.
func TestRelayChainsThroughARelayedParentAnswer(t *testing.T) {
	f := newRelayFixture(t, gitleaks)
	f.parentSays(t, (&transcript{}).ask("toolu_parent", relayStart.Add(time.Minute), gitleaks).
		answer("toolu_parent", relayStart.Add(2*time.Minute), map[string]string{gitleaks.Question: "Yes, append it"}))
	if _, err := f.store.RecordAnswer(store.DialogAnswer{TargetSession: "parent01", TargetToolUseID: "toolu_parent",
		QuestionHash: questionHash(gitleaks), Answer: "Yes, append it", BySession: "grandparent",
		Mode: store.AnswerRelayedUser, EvidenceToolUseID: "toolu_grandparent"}); err != nil {
		t.Fatal(err)
	}
	if err := f.guard(true).admit([]plannedAnswer{{0, "Yes, append it"}}, nil); err != nil {
		t.Fatalf("admit: %v", err)
	}
}

func TestEvidenceIsSpentOnce(t *testing.T) {
	f := newRelayFixture(t, gitleaks)
	f.parentSays(t, (&transcript{}).ask("toolu_parent", relayStart.Add(time.Minute), gitleaks).
		answer("toolu_parent", relayStart.Add(2*time.Minute), map[string]string{gitleaks.Question: "Yes, append it"}))
	if err := f.guard(true).admit([]plannedAnswer{{0, "Yes, append it"}}, nil); err != nil {
		t.Fatalf("first admit: %v", err)
	}
	err := f.guard(true).admit([]plannedAnswer{{0, "Yes, append it"}}, nil)
	if !errors.Is(err, errRelayRefused) || !strings.Contains(err.Error(), "already been relayed once") {
		t.Fatalf("second admit err = %v, want the evidence refused as spent", err)
	}
}

func TestApprovalQuestionRefusesAnAgentAnswer(t *testing.T) {
	fastSettle(t)
	f := newRelayFixture(t, gitleaks)
	sim := newSingleDialog(gitleaks, 60)
	raw, _ := sim.Capture()
	held, ok := dialog.Inspect(ansi.Strip(raw))
	if !ok {
		t.Fatalf("no dialog on:\n%s", raw)
	}
	_, err := answerGuarded(sim, raw, held, "Yes, append it", f.guard(false))
	if !errors.Is(err, errApprovalNeedsRelay) {
		t.Fatalf("err = %v, want the Approval refusal", err)
	}
	if len(sim.keys) != 0 || len(f.rows(t)) != 0 {
		t.Fatalf("a refused answer keyed %v and wrote %+v", sim.keys, f.rows(t))
	}
}

// Header matching ignores case and padding: "approval" is still an approval.
func TestApprovalHeaderMatchesLoosely(t *testing.T) {
	q := gitleaks
	q.Header = " approval "
	f := newRelayFixture(t, q)
	if err := f.guard(false).admit([]plannedAnswer{{0, "No"}}, nil); !errors.Is(err, errApprovalNeedsRelay) {
		t.Fatalf("err = %v, want the Approval refusal", err)
	}
}

// ledgerCheckingPane fails the first keystroke unless the ledger already
// holds a pending row for the child's call: the child's hook fires on submit
// and has to find it there.
type ledgerCheckingPane struct {
	*tabbedDialog
	t       *testing.T
	f       *relayFixture
	checked bool
	pending bool
}

func (p *ledgerCheckingPane) Keys(keys ...string) error {
	p.check()
	return p.tabbedDialog.Keys(keys...)
}

func (p *ledgerCheckingPane) Type(text string) error {
	p.check()
	return p.tabbedDialog.Type(text)
}

func (p *ledgerCheckingPane) check() {
	if p.checked {
		return
	}
	p.checked = true
	rows := p.f.rows(p.t)
	p.pending = len(rows) == 1 && rows[0].State == store.AnswerPending && rows[0].Mode == store.AnswerByAgent
}

func TestLedgerRowIsWrittenBeforeTheFirstKeystroke(t *testing.T) {
	fastSettle(t)
	f := newRelayFixture(t, rollout)
	pane := &ledgerCheckingPane{tabbedDialog: newSingleDialog(rollout, 60, plan...), t: t, f: f}
	raw, _ := pane.Capture()
	held, ok := dialog.Inspect(ansi.Strip(raw))
	if !ok {
		t.Fatalf("no dialog on:\n%s", raw)
	}
	if _, err := answerGuarded(pane, raw, held, "Do both", f.guard(false)); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if !pane.checked || !pane.pending {
		t.Fatalf("first keystroke found no pending ledger row (checked=%v)", pane.checked)
	}
	rows := f.rows(t)
	if len(rows) != 1 || rows[0].State != store.AnswerKeyed || rows[0].Answer != "Do both" {
		t.Fatalf("ledger after = %+v, want the row marked keyed", rows)
	}
}

func TestAHookNotesAnAgentTypedAnswer(t *testing.T) {
	configDir := tmuxtest.ScratchDir(t)
	st, err := store.Open(filepath.Join(configDir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, row := range []store.DialogAnswer{
		{TargetSession: "child001", TargetToolUseID: "toolu_agent", QuestionHash: "h", Answer: "Yes", BySession: "parent01", Mode: store.AnswerByAgent},
		{TargetSession: "child001", TargetToolUseID: "toolu_relayed", QuestionHash: "h", Answer: "Yes", BySession: "parent01", Mode: store.AnswerRelayedUser, EvidenceToolUseID: "toolu_p"},
	} {
		id, err := st.RecordAnswer(row)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.MarkAnswer(id, store.AnswerKeyed); err != nil {
			t.Fatal(err)
		}
	}
	payload := func(id string) []byte {
		return []byte(`{"hook_event_name":"PostToolUse","tool_name":"AskUserQuestion","tool_use_id":"` + id + `"}`)
	}
	cases := []struct {
		name, session string
		payload       []byte
		want          string
	}{
		{"agent", "child001", payload("toolu_agent"), "It is not the user's approval."},
		{"relayed", "child001", payload("toolu_relayed"), "matched this answer word for word"},
		{"no row", "child001", payload("toolu_person"), ""},
		{"another session's row", "child002", payload("toolu_agent"), ""},
		{"not AskUserQuestion", "child001", []byte(`{"tool_name":"Bash","tool_use_id":"toolu_agent"}`), ""},
		{"garbage", "child001", []byte(`not json`), ""},
		{"no session", "", payload("toolu_agent"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AskAnsweredHook(configDir, tc.session, tc.payload)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("hook printed %q, want nothing", got)
				}
				return
			}
			var out struct {
				HookSpecificOutput map[string]string `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal([]byte(got), &out); err != nil {
				t.Fatalf("hook output %q is not JSON: %v", got, err)
			}
			if out.HookSpecificOutput["hookEventName"] != "PostToolUse" ||
				!strings.Contains(out.HookSpecificOutput["classifierContext"], tc.want) || len(got) > 2000 {
				t.Fatalf("hook output = %s, want classifierContext saying %q", got, tc.want)
			}
			if len(out.HookSpecificOutput) != 2 {
				t.Fatalf("hook output carries fields beyond the note: %s", got)
			}
		})
	}
	if got := AskAnsweredHook(t.TempDir(), "child001", payload("toolu_agent")); got != "" {
		t.Fatalf("with no store the hook printed %q, want nothing", got)
	}
}

func TestSessionStartHookTellsOnlyAChild(t *testing.T) {
	configDir := tmuxtest.ScratchDir(t)
	st, err := store.Open(filepath.Join(configDir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	root := store.Session{ID: "root0001", Name: "root", Tool: "claude", Cwd: "/", Group: "g", Status: "idle"}
	child := store.Session{ID: "child001", Name: "child", Tool: "claude", Cwd: "/", Group: "g", Status: "idle", ParentID: root.ID, SpawnedBy: root.ID}
	for _, sess := range []store.Session{root, child} {
		if err := st.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
	}
	got := SessionStartHook(configDir, child.ID)
	if !strings.Contains(got, `"additionalContext"`) || !strings.Contains(got, `header \"Approval\"`) {
		t.Fatalf("child's SessionStart hook printed %q, want the approval note", got)
	}
	if got := SessionStartHook(configDir, root.ID); got != "" {
		t.Fatalf("a root session was told %q, want nothing", got)
	}
}
