package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

const previewQuestionsGolden = "testdata/preview_questions.txt"

var previewQuestionWidths = []int{40, 50, 60, 100}

type previewQuestionCase struct {
	name, pane, transcript string
}

var previewQuestionCases = []previewQuestionCase{
	{"second tab on screen", "claude-2.1.283-tabs-w50-second.ansi", "claude-ask-four-questions.jsonl"},
	{"first answered, second on screen", "claude-2.1.283-tabs-w80-after-first-answer.ansi", "claude-ask-four-questions.jsonl"},
	{"answered tab revisited", "claude-2.1.283-tabs-w80-revisit-answered.ansi", "claude-ask-four-questions.jsonl"},
	{"multi-select on screen", "claude-2.1.283-tabs-w44-multiselect.ansi", "claude-ask-three-multiselect.jsonl"},
	{"submit page, nothing answered", "claude-2.1.283-tabs-w80-submit-unanswered.ansi", "claude-ask-four-questions.jsonl"},
	{"submit page, every answer", "claude-2.1.283-tabs-w80-review-complete.ansi", "claude-ask-four-questions.jsonl"},
	{"submit page, one of two", "claude-2.1.284-w50-tabs-review-partial.ansi", "claude-ask-two-questions.jsonl"},
}

func pendingFixture(t testing.TB, name string) []convo.AskQuestion {
	t.Helper()
	questions, ok := convo.PendingAsk(filepath.Join("testdata", name))
	if !ok {
		t.Fatalf("%s holds no pending call", name)
	}
	return questions
}

// questionPreview is the selected session standing on pane, with asked as
// the call its transcript holds.
func questionPreview(t *testing.T, pane string, asked []convo.AskQuestion) *Model {
	t.Helper()
	m := drainFleet(t)
	m.rows[m.cursor].sess.Tool = "claude"
	sess, _ := m.selected()
	m.preview = readDialogFixture(t, pane)
	m.askQuestions = map[string][]convo.AskQuestion{}
	if asked != nil {
		m.askQuestions[sess.ID] = asked
	}
	return m
}

func strippedRows(rows []string) string {
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = strings.TrimRight(ansi.Strip(row), " ")
	}
	return strings.Join(out, "\n")
}

func previewQuestionFrames(t *testing.T) string {
	var b strings.Builder
	for _, c := range previewQuestionCases {
		m := questionPreview(t, c.pane, pendingFixture(t, c.transcript))
		for _, width := range previewQuestionWidths {
			rows := m.conversationRows(width, 200)
			for _, row := range rows {
				if w := textfmt.Width(row); w > width {
					t.Errorf("%s at %d: row is %d columns: %q", c.name, width, w, ansi.Strip(row))
				}
			}
			fmt.Fprintf(&b, "=== %s · %d columns\n%s\n", c.name, width, strippedRows(rows))
		}
	}
	// A preview shorter than the card: the questions off the screen give up
	// detail until it fits, and the one on the screen keeps all of it.
	m := questionPreview(t, previewQuestionCases[0].pane, pendingFixture(t, previewQuestionCases[0].transcript))
	for _, size := range [][2]int{{50, 30}, {50, 24}, {40, 20}, {60, 16}} {
		rows := m.conversationRows(size[0], size[1])
		if len(rows) > size[1] {
			t.Errorf("%dx%d: %d rows", size[0], size[1], len(rows))
		}
		fmt.Fprintf(&b, "=== %s · %dx%d\n%s\n", previewQuestionCases[0].name, size[0], size[1], strippedRows(rows))
	}
	return b.String()
}

// The preview of a session standing on a several-question dialog draws every
// question under the conversation: its header, whether it is answered and
// with what, the question and its options for the rest, which one the dialog
// is showing and the Submit tab. Set GATE_INBOX_GOLDEN=write to re-record.
func TestThePreviewDrawsEveryQuestionOfATabbedDialog(t *testing.T) {
	got := previewQuestionFrames(t)
	if os.Getenv("GATE_INBOX_GOLDEN") == "write" {
		if err := os.WriteFile(previewQuestionsGolden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(previewQuestionsGolden)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if got != string(want) {
		t.Errorf("preview questions differ from %s; re-record with GATE_INBOX_GOLDEN=write if intended\n%s",
			previewQuestionsGolden, firstDifference(string(want), got))
	}
}

func firstDifference(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < max(len(wl), len(gl)); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			return fmt.Sprintf("line %d:\nwant %q\ngot  %q", i+1, w, g)
		}
	}
	return ""
}

func TestThePreviewCardReadsTheDialogOffThePane(t *testing.T) {
	cases := []struct {
		pane string
		want []string
		not  []string
	}{
		{"claude-2.1.283-tabs-w50-second.ansi",
			[]string{"4 questions · 0 answered", "□ 2. Compliance  ◂ on screen", "Fail the release", "□ 4. Run tests", "Scheduled job", "▸ Submit"},
			[]string{"■", "unanswered"}},
		{"claude-2.1.283-tabs-w80-revisit-answered.ansi",
			[]string{"1 answered", "■ 1. Tooling  ◂ on screen", "→ pnpm", "□ 2. Compliance"}, nil},
		{"claude-2.1.283-tabs-w44-multiselect.ansi",
			[]string{"□ 2. Checks  ◂ on screen", "1. [ ] Build", "3. [ ] Lint", "1. Make"}, []string{"[ ] Make"}},
		{"claude-2.1.283-tabs-w80-review-complete.ansi",
			[]string{"4 answered", "→ Warn for a month, then block", "→ Nightly", "▸ Submit  ◂ on screen"}, []string{"Fail the release"}},
		{"claude-2.1.283-tabs-w80-submit-unanswered.ansi",
			[]string{"▸ Submit · 4 unanswered  ◂ on screen"}, nil},
	}
	for _, c := range cases {
		transcript := "claude-ask-four-questions.jsonl"
		if strings.Contains(c.pane, "multiselect") {
			transcript = "claude-ask-three-multiselect.jsonl"
		}
		m := questionPreview(t, c.pane, pendingFixture(t, transcript))
		text := strippedRows(m.conversationRows(60, 200))
		for _, want := range c.want {
			if !strings.Contains(text, want) {
				t.Errorf("%s: preview does not show %q:\n%s", c.pane, want, text)
			}
		}
		for _, not := range c.not {
			if strings.Contains(text, not) {
				t.Errorf("%s: preview shows %q:\n%s", c.pane, not, text)
			}
		}
	}
}

// A dialog asking one question, and one whose call the transcript does not
// hold, draw the preview they drew before: nothing is added to it.
func TestThePreviewIsUnchangedWithoutASeveralQuestionCall(t *testing.T) {
	baseline := strippedRows(questionPreview(t, "claude-2.1.283-tabs-w50-second.ansi", nil).conversationRows(50, 40))
	if strings.Contains(baseline, "questions ·") {
		t.Fatalf("a preview with no call read drew a card:\n%s", baseline)
	}
	single := questionPreview(t, "claude-2.1.284-w50-prose-single.ansi", pendingFixture(t, "claude-ask-two-questions.jsonl"))
	if got := strippedRows(single.conversationRows(50, 40)); got != baseline {
		t.Errorf("a single-question dialog changed the preview:\n%s\nwant\n%s", got, baseline)
	}
	short := questionPreview(t, "claude-2.1.283-tabs-w50-second.ansi", pendingFixture(t, "claude-ask-two-questions.jsonl"))
	if got := strippedRows(short.conversationRows(50, 40)); got != baseline {
		t.Errorf("a call that does not match the tab row drew a card:\n%s", got)
	}
}

// The card sits under the newest message, inside the frame's preview column,
// and moves with the pane: a later capture on the Submit page repaints it.
func TestTheFramePreviewShowsTheCardUnderTheConversation(t *testing.T) {
	m := questionPreview(t, "claude-2.1.283-tabs-w50-second.ansi", pendingFixture(t, "claude-ask-four-questions.jsonl"))
	m.width, m.height = 110, 70
	sess, _ := m.selected()
	m.applyConversation(conversationMsg{key: m.conversationIdentity(sess), messages: []search.Message{
		{Role: "user", Text: "Ask me the release questions."},
		{Role: "assistant", Text: "A few decisions before I start."},
	}})
	body := strings.Join(bodyRows(t, m), "\n")
	for _, want := range []string{"A few decisions before I start.", "4 questions · 0 answered", "◂ on screen", "Where should the integration tests run?"} {
		if !strings.Contains(body, want) {
			t.Fatalf("frame does not show %q:\n%s", want, body)
		}
	}
	if strings.Index(body, "A few decisions") > strings.Index(body, "4 questions") {
		t.Errorf("the card is above the newest message:\n%s", body)
	}
	m.preview = readDialogFixture(t, "claude-2.1.283-tabs-w80-review-complete.ansi")
	if body := strings.Join(bodyRows(t, m), "\n"); !strings.Contains(body, "4 questions · 4 answered") {
		t.Errorf("the card did not follow the pane to the Submit page:\n%s", body)
	}
}

func writeClaudeTranscript(t *testing.T, id string, body []byte) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	dir := filepath.Join(home, "projects", "some-project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The poll pass reads the call once and again only when the transcript
// changes, and only for a Claude pane holding a several-question tab row.
func TestThePollReadsThePendingCallOncePerTranscriptChange(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "claude-ask-four-questions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	path := writeClaudeTranscript(t, "conv-1", fixture)
	pane := ansi.Strip(readDialogFixture(t, "claude-2.1.283-tabs-w50-second.ansi"))
	sess := store.Session{ID: "s1", Tool: "claude", AgentSessionID: "conv-1", Cwd: t.TempDir(), Status: status.Waiting}
	p := &poller{}
	pass := func(sess store.Session, pane string) []convo.AskQuestion {
		next := map[string]*askRead{}
		asked := p.pendingAsk(sess, pane, next)
		p.askReads = next
		return asked
	}

	first := pass(sess, pane)
	if len(first) != 4 || first[1].Header != "Compliance" {
		t.Fatalf("first pass read %+v", first)
	}
	if again := pass(sess, pane); &again[0] != &first[0] {
		t.Error("an unchanged transcript was read again")
	}
	answered := append(fixture, []byte(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_preview1","content":"done"}]}}`+"\n")...)
	if err := os.WriteFile(path, answered, 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if got := pass(sess, pane); got != nil {
		t.Errorf("an answered call still read as pending: %+v", got)
	}

	codex := sess
	codex.Tool = "codex"
	single := ansi.Strip(readDialogFixture(t, "claude-2.1.284-w50-prose-single.ansi"))
	for name, c := range map[string]struct {
		sess store.Session
		pane string
	}{"codex": {codex, pane}, "single question": {sess, single}, "no dialog": {sess, "just output"}} {
		next := map[string]*askRead{}
		if got := p.pendingAsk(c.sess, c.pane, next); got != nil || len(next) != 0 {
			t.Errorf("%s: read %+v", name, got)
		}
	}
}

// The card is not drawn in the focus accent or the selected row's frame, on
// any theme, quiet or not, so it never reads as the focused view.
func TestTheQuestionCardHasItsOwnColour(t *testing.T) {
	t.Cleanup(func() { applyTheme(themes[themeIndex(defaultTheme)]) })
	for _, theme := range themes {
		for _, quiet := range []bool{false, true} {
			drawn := theme
			if quiet {
				drawn = quietened(theme)
			}
			card := cardColor(drawn)
			if strings.EqualFold(card, drawn.Accent) || strings.EqualFold(card, drawn.Dim) {
				t.Errorf("%s (quiet %v): card %s is the accent or the selected frame", theme.Name, quiet, card)
			}
			reachable := false
			for _, c := range []string{drawn.Working, drawn.Finished, drawn.Accent2, drawn.Waiting, drawn.Errored} {
				if min(rgbDistance(c, drawn.Accent), rgbDistance(c, drawn.Dim)) >= cardDistance {
					reachable = true
				}
			}
			if gap := min(rgbDistance(card, drawn.Accent), rgbDistance(card, drawn.Dim)); reachable && gap < cardDistance {
				t.Errorf("%s (quiet %v): card %s is only %.0f from the focus colours", theme.Name, quiet, card, gap)
			}
		}
		applyTheme(theme)
		lines := questionCardLines(nil, true, 40, cardFull)
		want, accent := fgSeq(cardColor(theme)), fgSeq(theme.Accent)
		for _, line := range lines {
			if !strings.Contains(line, want) {
				t.Errorf("%s: card row does not carry the card colour: %q", theme.Name, line)
			}
		}
		if last := lines[len(lines)-2]; strings.Contains(last, accent) && !strings.EqualFold(theme.Accent, cardColor(theme)) {
			t.Errorf("%s: the on-screen Submit row is drawn in the focus accent: %q", theme.Name, last)
		}
	}
}
