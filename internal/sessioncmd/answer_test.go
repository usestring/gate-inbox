package sessioncmd

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// The pane a child holding an AskUserQuestion shows.
const answerPane = `Which storefront should the census cover?

❯ 1. Germany only
  2. Every storefront

Enter to select · ↑/↓ to navigate · Esc to cancel
`

// childOn paints answerPane into a pane of its own and files it as the
// caller's child, which is the state the answer path exists for.
func childOn(t *testing.T, h *sessionHarness, parentID string) store.Session {
	t.Helper()
	return childShowing(t, h, parentID, "child001", "sampleapp-reach-census", answerPane)
}

// childShowing is childOn over an arbitrary pane.
//
// The pane is sized to its own content, because a capture is only of the
// visible screen: a fixture wider or taller than the window is wrapped and
// scrolled, and the dialog the test is about goes off the top.
//
// It waits for the pane to hold the dialog's closing legend before handing the
// row back. cat paints asynchronously, so a capture taken the moment after the
// pane is created can catch it still blank -- which reads as a child holding
// no question and fails whichever assertion follows.
func childShowing(t *testing.T, h *sessionHarness, parentID, id, name, pane string) store.Session {
	t.Helper()
	return childRunning(t, h, parentID, id, name, pane, "cat %s; sleep 60")
}

// answeringScript paints a one-question dialog, follows the arrows from the
// first row, and on Enter prints the record Claude Code prints of the answer
// it took.
const answeringScript = `cat "$1"
opts=(%s)
cur=1
while IFS= read -rsn1 key; do
  if [[ $key == $'\e' ]]; then
    read -rsn2 rest
    case $rest in
      '[A') ((cur > 1)) && cur=$((cur - 1)) ;;
      '[B') ((cur < ${#opts[@]})) && cur=$((cur + 1)) ;;
    esac
  elif [[ -z $key ]]; then
    printf '\n● User answered Claude'"'"'s questions:\n  ⎿  · %%s → %%s\n\n' %q "${opts[cur-1]}"
    break
  fi
done
sleep 60
`

// childAnswering is childShowing over a pane that takes its answer the way
// AskUserQuestion does, so the answer can be read back off it.
func childAnswering(t *testing.T, h *sessionHarness, parentID, id, name, pane, question string, options ...string) store.Session {
	t.Helper()
	quoted := make([]string, len(options))
	for i, option := range options {
		quoted[i] = fmt.Sprintf("%q", option)
	}
	script := filepath.Join(t.TempDir(), "answering.sh")
	body := fmt.Sprintf(answeringScript, strings.Join(quoted, " "), question)
	if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return childRunning(t, h, parentID, id, name, pane, "bash "+script+" %s")
}

// childRunning files a child whose pane runs command, formatted with the path
// of a file holding pane.
func childRunning(t *testing.T, h *sessionHarness, parentID, id, name, pane, command string) store.Session {
	t.Helper()
	dir := t.TempDir()
	fixture := filepath.Join(dir, "pane.txt")
	if err := os.WriteFile(fixture, []byte(pane), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	child := store.Session{
		ID:       id,
		Name:     name,
		Tool:     "echoer",
		Cwd:      dir,
		Group:    "backend",
		Status:   status.Waiting,
		ParentID: parentID,
	}
	width, height := paneSize(pane)
	if err := h.driver.Create(child.ID, child.Cwd, fmt.Sprintf(command, fixture), nil, width, height); err != nil {
		t.Fatalf("create child pane: %v", err)
	}
	t.Cleanup(func() { _ = h.driver.Kill(child.ID) })
	// The fixture's own closing line rather than one dialog's legend: these
	// panes are four harness dialogs now and they close with four different
	// lines, so waiting on any one of them waits forever on the other three.
	settled := lastLine(pane)
	deadline := time.Now().Add(10 * time.Second)
	for {
		painted, err := h.driver.CapturePane(child.ID)
		if err == nil && strings.Contains(ansi.Strip(painted), settled) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the child pane never painted its dialog:\n%s", painted)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := h.store.CreateSession(child); err != nil {
		t.Fatalf("create child row: %v", err)
	}
	return child
}

// lastLine is the fixture's closing line, which is the last thing painting it
// puts on the screen and so the signal that the paint is done.
func lastLine(pane string) string {
	lines := strings.Split(ansi.Strip(pane), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if trimmed := strings.TrimSpace(lines[i]); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// paneSize is the window a pane's content needs, never smaller than the
// ordinary terminal these tests otherwise assume.
func paneSize(pane string) (width, height int) {
	lines := strings.Split(ansi.Strip(pane), "\n")
	width, height = 80, max(24, len(lines)+2)
	for _, line := range lines {
		width = max(width, utf8.RuneCountInString(line)+2)
	}
	return width, height
}

func TestAnswerPicksTheOptionTheParentNamed(t *testing.T) {
	h := newSessionHarness(t)
	child := childAnswering(t, h, h.caller.ID, "child001", "sampleapp-reach-census", answerPane,
		"Which storefront should the census cover?", "Germany only", "Every storefront")
	answered, err := h.sessions.Answer(h.caller.ID, child.ID, "Every storefront")
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if answered.Selected != "Every storefront" || !answered.Verified {
		t.Errorf("selected %q verified=%v, want the second option read back", answered.Selected, answered.Verified)
	}
	if !strings.Contains(answered.Question, "Which storefront") {
		t.Errorf("question = %q, want the prompt from the pane", answered.Question)
	}
}

// Words are typed only into the dialog's free-text row. A dialog drawing none
// drops pasted words and takes the Enter after them as a pick of whatever the
// cursor is on, so the answer is refused before a keystroke rather than
// registered as an option nobody chose.
func TestAnswerRefusesWordsWithNowhereToTypeThem(t *testing.T) {
	h := newSessionHarness(t)
	child := childOn(t, h, h.caller.ID)
	_, err := h.sessions.Answer(h.caller.ID, child.ID, "cover de and kr, skip the rest")
	if err == nil || !strings.Contains(err.Error(), "no free-text row") {
		t.Fatalf("Answer err = %v, want a refusal naming the missing free-text row", err)
	}
}

// A child whose screen never shows the answer it took is an error, not a
// success: nothing says the answer landed.
func TestAnswerFailsWhenTheAnswerCannotBeReadBack(t *testing.T) {
	h := newSessionHarness(t)
	child := childOn(t, h, h.caller.ID)
	_, err := h.sessions.Answer(h.caller.ID, child.ID, "Every storefront")
	if err == nil || !strings.Contains(err.Error(), "cannot be confirmed") || !strings.Contains(err.Error(), `"Every storefront"`) {
		t.Fatalf("Answer err = %v, want one saying the answer cannot be confirmed", err)
	}
}

// The whole confinement: keys into somebody else's pane is exactly what
// send_session refuses to do, so the one relationship that opens it has to be
// checked, and the refusal has to say who does own the row.
func TestAnswerRefusesASessionThatIsNotTheCallersChild(t *testing.T) {
	h := newSessionHarness(t)
	stranger := childOn(t, h, "")
	_, err := h.sessions.Answer(h.caller.ID, stranger.ID, "Germany only")
	if err == nil || !strings.Contains(err.Error(), "nobody's child") {
		t.Fatalf("Answer err = %v, want a refusal naming who owns the row", err)
	}
}

func TestAnswerRefusesAnEmptyAnswer(t *testing.T) {
	h := newSessionHarness(t)
	child := childOn(t, h, h.caller.ID)
	if _, err := h.sessions.Answer(h.caller.ID, child.ID, "   "); err == nil {
		t.Fatal("an empty answer was accepted")
	}
}

func TestAnswerSaysSoWhenTheQuestionIsGone(t *testing.T) {
	h := newSessionHarness(t)
	child := store.Session{
		ID: "child002", Name: "quiet-child", Tool: "echoer", Cwd: t.TempDir(),
		Group: "backend", Status: status.Working, ParentID: h.caller.ID,
	}
	if err := h.driver.Create(child.ID, child.Cwd, "sleep 60", nil, 80, 24); err != nil {
		t.Fatalf("create child pane: %v", err)
	}
	t.Cleanup(func() { _ = h.driver.Kill(child.ID) })
	if err := h.store.CreateSession(child); err != nil {
		t.Fatalf("create child row: %v", err)
	}
	_, err := h.sessions.Answer(h.caller.ID, child.ID, "Germany only")
	if err == nil || !strings.Contains(err.Error(), "may already have been answered") {
		t.Fatalf("Answer err = %v, want it to say the question is gone", err)
	}
	// And that it is a pane with no dialog on it, not a dialog this refuses:
	// the two want opposite things done and used to read the same.
	if strings.Contains(err.Error(), "is a person's to answer") {
		t.Fatalf("Answer err = %v, want an empty screen told from a guarded dialog", err)
	}
}

// codexAskPane is Codex's request_user_input, the shape a Codex child stalls
// on that had no reading here at all: numbered options, a light marker glyph
// and none of the legend dialog.Parse anchored on.
const codexAskPane = `  Choose an option.

  › 1. Germany only  The storefront the census was scoped to.
    2. Every storefront  Everything the account can see.

  tab to add notes | enter to submit answer | esc to interrupt
`

func TestAnswerClearsACodexQuestionDialog(t *testing.T) {
	h := newSessionHarness(t)
	child := childShowing(t, h, h.caller.ID, "child004", "codex-census", codexAskPane)
	answered, err := h.sessions.Answer(h.caller.ID, child.ID, "Every storefront")
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if !strings.HasPrefix(answered.Selected, "Every storefront") {
		t.Errorf("selected %q, want the option the answer named", answered.Selected)
	}
	if !strings.Contains(answered.Question, "Choose an option.") {
		t.Errorf("question = %q, want the prompt from the pane", answered.Question)
	}
}

// The refusals. Each one is a different thing for the caller to do, and until
// now every one of them was the same sentence -- which is why 42 calls across
// four days told eleven managers nothing, and three of them archived a child
// with its answer already written.
func TestAnswerNamesTheShapeItWillNotAnswer(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   string
		pane string
		says string
	}{
		{"a Codex command approval", "child005",
			codexApprovalFixture, "permission prompt"},
		{"Codex's first-run trust prompt", "child006",
			codexTrustFixture, "directory-trust"},
		{"a multi-select", "child007",
			multiSelectFixture, "multi-select"},
		// Two rows wear the marker: Claude Code leaves a dim one where the
		// selection came from, and only colour tells them apart -- which the
		// capture no longer carries by the time the options are read.
		{"a dialog whose selection cannot be located", "child008",
			twoMarkerFixture, "selection this cannot locate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newSessionHarness(t)
			child := childShowing(t, h, h.caller.ID, tc.id, "stalled-child", tc.pane)
			_, err := h.sessions.Answer(h.caller.ID, child.ID, "Yes, proceed")
			if err == nil {
				t.Fatal("the dialog was answered; a keystroke cannot answer this one")
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("refusal = %q, want it to name the shape (%q)", err, tc.says)
			}
			// The old refusal sent every one of these to send_session, which
			// queues until the recipient is at rest -- and a session holding a
			// dialog never is, so the message was never delivered.
			if strings.Contains(err.Error(), "send it words instead") {
				t.Fatalf("refusal = %q, want it not to name the path that cannot work", err)
			}
		})
	}
}

const codexApprovalFixture = `  $ rm -rf build/

› 1. Yes, proceed (y)
  2. No, and tell Codex what to do differently (esc)

  Press enter to confirm or esc to cancel
`

const codexTrustFixture = `Do you trust the contents of this directory?

› 1. Yes, continue
  2. No, quit

  Press enter to continue
`

const multiSelectFixture = `  Which checks should run?

❯ 1. [ ] Build
  2. [ ] Lint
  3. [ ] Tests

  Enter to select · ↑/↓ to navigate · Esc to cancel
`

const twoMarkerFixture = `  Which approach first?

❯ 1. Reproduce the crypto natively
  2. Stop here
❯ 3. Go, but smaller

  Enter to select · ↑/↓ to navigate · Esc to cancel
`

// colouredAskPane is Claude Code's own capture of an AskUserQuestion, read raw
// rather than through a stripping helper. A coloured
// dialog is every real dialog, and colour is the whole difficulty: the legend
// dialog.Parse anchors at line start is drawn dim, so the escape sits between
// the line start and "Enter".
func colouredAskPane(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "ui", "testdata", "claude-stepper-first-step.txt"))
	if err != nil {
		t.Fatalf("read the captured dialog: %v", err)
	}
	// The fixture only proves anything while it still carries its escapes: one
	// with the colour taken out parses unstripped, so it would pass against the
	// very bug this is here to catch.
	if _, ok := dialog.Parse(string(raw)); ok {
		t.Fatal("the fixture parses unstripped, so it has lost the escapes that are the whole bug")
	}
	return string(raw)
}

func TestAnswerReadsADialogDrawnInColour(t *testing.T) {
	h := newSessionHarness(t)
	child := childShowing(t, h, h.caller.ID, "child003", "shape-question", colouredAskPane(t))
	// The pane is a still capture, so the tab never ticks; the answer reaching
	// that check at all is the dialog read through its colour, the question
	// on the screen resolved, and the option keyed.
	_, err := h.sessions.Answer(h.caller.ID, child.ID, "Square")
	if err == nil || !strings.Contains(err.Error(), "question 1 was keyed but its tab never ticked") {
		t.Fatalf("Answer err = %v, want the keyed question reported unconfirmed", err)
	}
	still := &stillPane{raw: colouredAskPane(t)}
	held := mustInspect(t, still.raw)
	answered, _ := answerHeld(still, still.raw, held, "Square")
	// Past the legend is not enough: askOption, the cursor marker and the
	// stepper all read the same text, and a fix that parsed the dialog and
	// then picked the wrong option would be worse than the clean refusal it
	// replaced.
	if want := dialog.SelectKeys(held.Cursor, held.Choose("Square")); held.Options[held.Choose("Square")-1] != "Square" ||
		!reflect.DeepEqual(still.keys, want) {
		t.Errorf("keyed %v, want %v for the option the answer named", still.keys, want)
	}
	if !strings.Contains(answered.Question, "Which shape?") {
		t.Errorf("question = %q, want the prompt from the pane", answered.Question)
	}
	if !strings.Contains(answered.Question, "1. Round") || strings.Contains(answered.Question, "\x1b") {
		t.Errorf("question = %q, want the options in plain text", answered.Question)
	}
	// Three questions in the one call, and the still pane took none of them.
	if answered.Standing != 3 || answered.Verified {
		t.Errorf("standing = %d verified = %v, want all three standing and nothing confirmed", answered.Standing, answered.Verified)
	}
}

// stillPane is a pane that never redraws, and notes the keys sent to it.
type stillPane struct {
	raw  string
	keys []string
}

func (p *stillPane) Capture() (string, error) { return p.raw, nil }
func (p *stillPane) Keys(keys ...string) error {
	p.keys = append(p.keys, keys...)
	return nil
}
func (p *stillPane) Type(text string) error {
	p.keys = append(p.keys, "paste:"+text)
	return nil
}

func mustInspect(t *testing.T, raw string) dialog.Dialog {
	t.Helper()
	held, ok := dialog.Inspect(ansi.Strip(raw))
	if !ok {
		t.Fatalf("no dialog read off:\n%s", raw)
	}
	return held
}
