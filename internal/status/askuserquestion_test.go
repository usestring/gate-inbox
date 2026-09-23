package status

import (
	"strings"
	"testing"
)

const dialogSeparator = "─────────────────────────────────────────────────────────────────────────────"

// askUserQuestionPane is a recorded Claude Code pane sitting on an
// AskUserQuestion dialog, scrubbed of the conversation it came from and
// kept structurally exact: a turn that ended, an earlier prompt line whose
// marker is not the last one in the pane, options carrying the marker on
// the selected one only, and the keybinding legend under them. That last
// point is the whole case. The dialog had held the session for seven hours
// and the board read it as idle.
const askUserQuestionPane = `     85  ## URL state
     86
     87  The section below describes the state the page keeps in the address bar.

` + "●" + ` Built and scaffolded the project (7 files, ~870 lines).

  Deploy:
  install the dependencies, set the access token, then publish.

  What testing changed

  I checked the colour ramps against a real tile rather than assuming, and the first version was broken: fixed stops put nearly every
  polygon in one bucket. So classification now defaults to deciles computed from what is on screen.

` + "✻" + ` Worked for 10m 59s

` + "❯" + ` i
  ` + "⎺" + `  44 skills available

` + "●" + ` Looks like that got cut off — what did you want?

` + dialogSeparator + `
 ` + "☐" + ` Next step

Your last message came through as just "i" — what should I pick up?

` + "❯" + ` 1. Run it locally (Recommended)
     Install the dependencies and start the dev server, so the page actually renders in a browser.
  2. Set up the repo
     Initialise git, add an ignore file, commit the scaffold.
  3. Publish it
     Set the access token and deploy. Needs credentials and an install first.
  4. Stop here
     Leave the project as-is on disk; nothing further from me right now.
  5. Type something.
` + dialogSeparator + `
  6. Chat about this

Enter to select ` + "·" + ` ` + "↑/↓" + ` to navigate ` + "·" + ` Esc to cancel
`

// selectLower moves the marker off the first option, which is what the
// pane looks like after the user arrows down but before answering.
func selectLower(pane string) string {
	moved := strings.Replace(pane, "❯ 1. Run it locally", "  1. Run it locally", 1)
	return strings.Replace(moved, "  4. Stop here", "❯ 4. Stop here", 1)
}

func TestAskUserQuestionDialogWaits(t *testing.T) {
	engine := defaultEngine(t)
	cases := []struct {
		name string
		pane string
	}{
		{"first option selected", askUserQuestionPane},
		{"lower option selected", selectLower(askUserQuestionPane)},
		{"tab-navigated variant", strings.Replace(askUserQuestionPane,
			"↑/↓ to navigate", "Tab/Arrow keys to navigate", 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := engine.Match("claude", tc.pane); got != Waiting {
				t.Fatalf("Match=%q want %q: a dialog blocking on a person must not read as idle", got, Waiting)
			}
			if got, matched := engine.RuleMatch("claude", tc.pane); !matched || got != Waiting {
				t.Fatalf("RuleMatch=%q,%v want %q,true", got, matched, Waiting)
			}
		})
	}
}

// A numbered draft the user is still typing carries the marker and the
// numbers of a dialog, which is why the line under the input marker is
// excluded from the footer. The legend rule must not undo that: nothing
// below the marker here is Claude's own chrome, so the pane is not waiting
// on anyone.
func TestTypedNumberedDraftDoesNotWait(t *testing.T) {
	engine := defaultEngine(t)
	draft := `` + "●" + ` Here is the plan.

` + "✻" + ` Worked for 10m 59s

` + "❯" + ` 1. run the tests
  2. fix the failures
  3. open the PR
  and mention Enter to select somewhere in the draft
`
	if got, _ := engine.Match("claude", draft); got == Waiting {
		t.Fatalf("Match=%q: a numbered draft the user is typing must not read as waiting", got)
	}
	if got, matched := engine.RuleMatch("claude", draft); matched && got == Waiting {
		t.Fatalf("RuleMatch=%q,%v: a numbered draft must trip no waiting rule", got, matched)
	}
}
