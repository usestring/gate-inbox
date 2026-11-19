package sessioncmd

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
)

// piPane is pi 1.5.1's permission dialog as the rig captured it at width.
func piPane(t *testing.T, width int) *screenPane {
	return newScreenPane(t, fmt.Sprintf("pi-1.5.1-w%d-permission-touch.txt", width), "")
}

func TestAPiChildsPermissionDialogTakesARelayedChoice(t *testing.T) {
	fastSettle(t)
	for _, width := range []int{60, 100, 140, 160} {
		for _, want := range []string{"Yes", "No, and tell pi what to do differently"} {
			t.Run(fmt.Sprintf("%d/%s", width, want), func(t *testing.T) {
				f := screenFixture(t)
				f.child.Tool = "pi"
				pane := piPane(t, width)
				screen := readPane(t, pane)
				userAnswered(f, t, screen, want, relayStart.Add(time.Minute))
				raw, _ := pane.Capture()
				answered, err := (&runtime{}).answerPi(f.child, pane, raw, want, "parent", f.parent.ID, f.guard(true))
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

func TestAPiChildsPermissionDialogRefusesAnAnswerThatIsNotTheUsers(t *testing.T) {
	fastSettle(t)
	f := screenFixture(t)
	f.child.Tool = "pi"
	pane := piPane(t, 100)
	raw, _ := pane.Capture()
	_, err := (&runtime{}).answerPi(f.child, pane, raw, "Yes", "parent", f.parent.ID, f.guard(false))
	if err == nil || !strings.Contains(err.Error(), "relay_ask") {
		t.Fatalf("err = %v, want the relay refusal", err)
	}
	if len(pane.keys) > 0 {
		t.Fatalf("keyed %v", pane.keys)
	}
}

func TestAPiChildWithNoDialogIsNotKeyed(t *testing.T) {
	f := screenFixture(t)
	f.child.Tool = "pi"
	pane := piPane(t, 100)
	pane.closed = true
	raw, _ := pane.Capture()
	if _, err := (&runtime{}).answerPi(f.child, pane, raw, "Yes", "parent", f.parent.ID, f.guard(true)); !errors.Is(err, errPiNoDialog) {
		t.Fatalf("err = %v", err)
	}
	if len(pane.keys) > 0 {
		t.Fatalf("keyed %v", pane.keys)
	}
}
