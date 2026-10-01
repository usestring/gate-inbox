package sessioncmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// screenPane is a permission or trust dialog captured live on Claude Code
// 2.1.286, driven the way the harness drives it: Up and Down move the marker
// over the choices, Enter takes the dialog down. stuck keeps it standing.
type screenPane struct {
	rows   []string
	choice []int // row of each choice, in order
	cursor int
	closed bool
	stuck  bool
	keys   []string
}

var markerRow = regexp.MustCompile(`^(\s*)❯ `)

func newScreenPane(t *testing.T, fixture, prose string) *screenPane {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "dialog", "testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	plain := prose + ansi.Strip(string(raw))
	screen, ok := dialog.ReadScreen(plain)
	if !ok {
		t.Fatalf("%s reads as no dialog", fixture)
	}
	p := &screenPane{rows: strings.Split(plain, "\n"), cursor: screen.Cursor()}
	for i, row := range p.rows {
		if markerRow.MatchString(row) {
			p.rows[i] = markerRow.ReplaceAllString(row, "$1  ")
		}
	}
	for _, choice := range screen.Choices {
		for i := len(p.rows) - 1; i >= 0; i-- {
			text := strings.TrimSpace(p.rows[i])
			if choice.Number > 0 {
				text = strings.TrimPrefix(text, fmt.Sprintf("%d. ", choice.Number))
			}
			head := choice.Label[:min(len(choice.Label), len(text))]
			if text != "" && strings.HasPrefix(choice.Label, text) || text == head && len(text) > 3 {
				if contains(p.choice, i) {
					continue
				}
				p.choice = append(p.choice, i)
				break
			}
		}
	}
	return p
}

func contains(list []int, n int) bool {
	for _, v := range list {
		if v == n {
			return true
		}
	}
	return false
}

func (p *screenPane) Capture() (string, error) {
	if p.closed {
		return "● Bash(touch perm-probe.txt)\n\n❯ \n", nil
	}
	rows := append([]string(nil), p.rows...)
	row := rows[p.choice[p.cursor]]
	indent := len(row) - len(strings.TrimLeft(row, " "))
	rows[p.choice[p.cursor]] = row[:max(indent-2, 0)] + "❯ " + strings.TrimLeft(row, " ")
	return strings.Join(rows, "\n"), nil
}

func (p *screenPane) Keys(keys ...string) error {
	for _, key := range keys {
		p.keys = append(p.keys, key)
		switch key {
		case "Down":
			p.cursor = min(p.cursor+1, len(p.choice)-1)
		case "Up":
			p.cursor = max(p.cursor-1, 0)
		case "Enter":
			p.closed = !p.stuck
		}
	}
	return nil
}

func (p *screenPane) Type(string) error { return errors.New("a screen takes no words") }

// screenFixture is a parent and a child stopped on screen since relayStart.
func screenFixture(t *testing.T) *relayFixture {
	f := newRelayFixture(t, gitleaks)
	f.child.Status, f.child.LastStatusAt = status.Waiting, relayStart
	return f
}

// userAnswered puts the screen to the parent's user, as the relay told it
// to, and records their choice.
func userAnswered(f *relayFixture, t *testing.T, screen dialog.Screen, chose string, at time.Time) {
	options := make([]convo.AskOption, len(screen.Choices))
	for i, choice := range screen.Choices {
		options[i] = convo.AskOption{Label: choice.Label}
	}
	question := "probe is asking: " + screen.Prompt()
	f.parentSays(t, (&transcript{}).
		ask("toolu_parent_screen", at, convo.AskQuestion{Header: "Permission", Question: question, Options: options}).
		answer("toolu_parent_screen", at.Add(time.Minute), map[string]string{question: chose}))
}

func readPane(t *testing.T, pane dialogPane) dialog.Screen {
	t.Helper()
	raw, _ := pane.Capture()
	screen, ok := dialog.ReadScreen(raw)
	if !ok {
		t.Fatalf("no dialog on the pane:\n%s", raw)
	}
	return screen
}

func TestARelayedChoiceIsPickedByItsTextAndTheDialogClears(t *testing.T) {
	fastSettle(t)
	prose := "  1. Yes, delete everything\n  2. No, keep it\n\n"
	for _, fixture := range []string{"permission-bash", "permission-webfetch", "workspace-trust", "mcp-trust"} {
		for _, width := range widths {
			t.Run(fmt.Sprintf("%s/%d", fixture, width), func(t *testing.T) {
				f := screenFixture(t)
				pane := newScreenPane(t, fmt.Sprintf("claude-2.1.286-w%d-%s.ansi", width, fixture), prose)
				screen := readPane(t, pane)
				// The choice nearest the end that the marker is not on: never the
				// highlighted one, so a pick by position would be caught.
				want := screen.Choices[len(screen.Choices)-1].Label
				if screen.Cursor() == len(screen.Choices)-1 {
					want = screen.Choices[0].Label
				}
				userAnswered(f, t, screen, want, relayStart.Add(time.Minute))
				answered, err := (&runtime{}).answerScreen(f.child, pane, screen, want, "parent", f.parent.ID, f.guard(true))
				if err != nil {
					t.Fatalf("answer: %v (keys %v)", err, pane.keys)
				}
				if !pane.closed || answered.Selected != want || !answered.Verified {
					t.Fatalf("closed %v, answered %+v, keys %v", pane.closed, answered, pane.keys)
				}
				if got := pane.choice[pane.cursor]; got != pane.choice[screen.Choose(want)-1] {
					t.Fatalf("Enter landed on row %d, not %q (keys %v)", got, want, pane.keys)
				}
				rows, err := f.store.AnswersFor(f.child.ID, "")
				if err != nil || len(rows) != 1 || rows[0].Mode != store.AnswerRelayedUser || rows[0].State != store.AnswerKeyed {
					t.Fatalf("ledger %+v, err %v", rows, err)
				}
			})
		}
	}
}

func TestAScreenChoiceIsRefusedUnlessItIsTheUsersOwn(t *testing.T) {
	fastSettle(t)
	fixture := "claude-2.1.286-w50-permission-bash.ansi"
	for name, tc := range map[string]struct {
		relay  bool
		setup  func(f *relayFixture, screen dialog.Screen)
		answer string
		says   string
	}{
		"without relay": {false, nil, "Yes", "Ask your user with your own question tool, copying the question"},
		"a paraphrase": {true, func(f *relayFixture, screen dialog.Screen) {
			q := "May the child touch a file?"
			f.parentSays(t, (&transcript{}).ask("p1", relayStart.Add(time.Minute), convo.AskQuestion{Question: q,
				Options: []convo.AskOption{{Label: "Yes"}, {Label: "No"}}}).answer("p1", relayStart.Add(2*time.Minute), map[string]string{q: "Yes"}))
		}, "Yes", "word for word"},
		"another answer": {true, func(f *relayFixture, screen dialog.Screen) {
			userAnswered(f, t, screen, "No", relayStart.Add(time.Minute))
		}, "Yes", `answered "No", not "Yes"`},
		"answered before the dialog": {true, func(f *relayFixture, screen dialog.Screen) {
			userAnswered(f, t, screen, "Yes", relayStart.Add(-time.Hour))
		}, "Yes", "before the child asked"},
		"an answer that is no choice": {true, func(f *relayFixture, screen dialog.Screen) {
			userAnswered(f, t, screen, "Yes", relayStart.Add(time.Minute))
		}, "Yes, delete everything", "names none of its choices"},
		"no dialog of the parent's": {true, func(f *relayFixture, screen dialog.Screen) {
			f.parentSays(t, &transcript{})
		}, "Yes", "word for word"},
	} {
		t.Run(name, func(t *testing.T) {
			f := screenFixture(t)
			pane := newScreenPane(t, fixture, "")
			screen := readPane(t, pane)
			if tc.setup != nil {
				tc.setup(f, screen)
			}
			_, err := (&runtime{}).answerScreen(f.child, pane, screen, tc.answer, "parent", f.parent.ID, f.guard(tc.relay))
			if err == nil || !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("err = %v, want it to say %q", err, tc.says)
			}
			if len(pane.keys) > 0 {
				t.Fatalf("keyed %v", pane.keys)
			}
			for _, banned := range []string{"pane", "operator", "person"} {
				if strings.Contains(err.Error(), banned) {
					t.Errorf("the refusal hands the dialog off (%q): %v", banned, err)
				}
			}
		})
	}
}

func TestAScreenRelayIsSpentOnce(t *testing.T) {
	fastSettle(t)
	f := screenFixture(t)
	pane := newScreenPane(t, "claude-2.1.286-w60-permission-bash.ansi", "")
	screen := readPane(t, pane)
	userAnswered(f, t, screen, "Yes", relayStart.Add(time.Minute))
	if _, err := (&runtime{}).answerScreen(f.child, pane, screen, "Yes", "parent", f.parent.ID, f.guard(true)); err != nil {
		t.Fatal(err)
	}
	again := newScreenPane(t, "claude-2.1.286-w60-permission-bash.ansi", "")
	_, err := (&runtime{}).answerScreen(f.child, again, screen, "Yes", "parent", f.parent.ID, f.guard(true))
	if !errors.Is(err, errRelayRefused) || len(again.keys) > 0 {
		t.Fatalf("a second relay of one answer: err %v, keys %v", err, again.keys)
	}
}

func TestADialogThatDoesNotClearIsAnError(t *testing.T) {
	fastSettle(t)
	f := screenFixture(t)
	pane := newScreenPane(t, "claude-2.1.286-w40-workspace-trust.ansi", "")
	pane.stuck = true
	screen := readPane(t, pane)
	userAnswered(f, t, screen, "Yes, I trust this folder", relayStart.Add(time.Minute))
	_, err := (&runtime{}).answerScreen(f.child, pane, screen, "Yes, I trust this folder", "parent", f.parent.ID, f.guard(true))
	if err == nil || !strings.Contains(err.Error(), "still standing") {
		t.Fatalf("err = %v", err)
	}
}

func TestKeysAreSentOnlyAsTheUsersChoice(t *testing.T) {
	fastSettle(t)
	f := screenFixture(t)
	pane := newScreenPane(t, "claude-2.1.286-w50-mcp-trust.ansi", "")
	raw, _ := pane.Capture()
	shown := screenLines(ansi.Strip(raw))
	g := f.guard(true)
	if err := g.admitKeys(shown, "Up Enter"); !errors.Is(err, errRelayRefused) {
		t.Fatalf("keys with no dialog of the user's: %v", err)
	}
	question := "probe's screen reads:\n" + shown
	f.parentSays(t, (&transcript{}).
		ask("pk", relayStart.Add(time.Minute), convo.AskQuestion{Question: question,
			Options: []convo.AskOption{{Label: "Keys: Up Enter"}, {Label: "Keys: Enter"}}}).
		answer("pk", relayStart.Add(2*time.Minute), map[string]string{question: "Keys: Up Enter"}))
	if err := f.guard(true).admitKeys(shown, "Enter"); err == nil || !strings.Contains(err.Error(), `not "Enter"`) {
		t.Fatalf("other keys than the user's: %v", err)
	}
	g = f.guard(true)
	if err := g.admitKeys(shown, "Up Enter"); err != nil {
		t.Fatalf("the user's own keys: %v", err)
	}
	answered, err := sendKeys(pane, raw, []string{"Up", "Enter"})
	g.finish(err)
	if err != nil || !answered.Changed || !pane.closed {
		t.Fatalf("answered %+v, err %v", answered, err)
	}
	if keysAllowed.MatchString("rm -rf /") || keysAllowed.MatchString("C-c") || !keysAllowed.MatchString("2") {
		t.Fatal("the key allowlist lets through more than navigation and one character")
	}
}

type opencodePermissionPane struct {
	cursor int
	closed bool
	keys   []string
}

func (p *opencodePermissionPane) Capture() (string, error) {
	if p.closed {
		return "ready", nil
	}
	out := "┃  △ Permission required\n┃  $ echo hello\n┃  "
	for i, label := range []string{"Allow once", "Always allow", "Reject"} {
		background := "\x1b[48;5;0m"
		if i == p.cursor {
			background = "\x1b[48;5;1m"
		}
		out += background + label + "  "
	}
	return out + "\x1b[0m\n┃  ⇆ select enter confirm\n", nil
}
func (p *opencodePermissionPane) Keys(keys ...string) error {
	for _, key := range keys {
		p.keys = append(p.keys, key)
		switch key {
		case "Left":
			p.cursor--
		case "Right":
			p.cursor++
		case "Enter":
			p.closed = true
		}
	}
	return nil
}
func (p *opencodePermissionPane) Type(string) error { return errors.New("permission takes no text") }

func TestOpencodePermissionChoiceMovesSidewaysAndReadsBack(t *testing.T) {
	quickSettle(t)
	pane := &opencodePermissionPane{}
	raw, _ := pane.Capture()
	screen, ok := dialog.ReadScreenFor("opencode", raw)
	if !ok {
		t.Fatal("permission not read")
	}
	got, err := pickChoice(pane, screen, 3, "opencode")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Verified || got.Selected != "Reject" || strings.Join(pane.keys, ",") != "Right,Right,Enter" {
		t.Fatalf("result %+v, keys %q", got, pane.keys)
	}
}
