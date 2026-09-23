package ui

import (
	"testing"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// Panes whose rules the status engine matches to one verdict each, so a
// precedence table can name a pane verdict rather than a screen.
var precedencePanes = map[string]string{
	status.Waiting:  "Enter to confirm\n❯ \n",
	status.Working:  "⏺ Security agent done. 2 left.\n✻ Waiting for 2 background agents to finish\n❯ \n",
	status.Finished: "here is the result\n\n✻ Baked for 5s\n\n❯ \n",
	status.Errored:  "error: something broke\n❯ \n",
}

// The pane rules cross-check every hook status that can go stale. Idle is
// the case sample-repo#983 left open: SessionStart writes it and no later event
// rewrites it, so a live waiting or working pane has to win. Finished and
// errored stay hook-authoritative against an idle hook, since re-deriving
// an alert the operator already dismissed would re-notify each poll. The
// waiting rows pin the deliberate gap: a hook-written waiting is a live
// Notification event, so no pane verdict displaces it.
func TestHookStatusPrecedenceAgainstPaneVerdicts(t *testing.T) {
	cases := []struct {
		name  string
		hook  string
		pane  string
		acked bool
		want  string
	}{
		{"idle hook, waiting pane", status.Idle, status.Waiting, false, status.Waiting},
		{"idle hook, working pane", status.Idle, status.Working, false, status.Working},
		{"idle hook, finished pane", status.Idle, status.Finished, false, status.Idle},
		{"idle hook, errored pane", status.Idle, status.Errored, false, status.Idle},
		{"idle hook, acked waiting pane", status.Idle, status.Waiting, true, status.Waiting},
		{"idle hook, acked finished pane", status.Idle, status.Finished, true, status.Idle},

		{"waiting hook, working pane", status.Waiting, status.Working, false, status.Waiting},
		{"waiting hook, finished pane", status.Waiting, status.Finished, false, status.Waiting},
		{"waiting hook, errored pane", status.Waiting, status.Errored, false, status.Waiting},
		{"waiting hook, waiting pane", status.Waiting, status.Waiting, false, status.Waiting},

		{"finished hook, waiting pane", status.Finished, status.Waiting, false, status.Waiting},
		{"finished hook, working pane", status.Finished, status.Working, false, status.Working},
		{"finished hook, errored pane", status.Finished, status.Errored, false, status.Errored},
		{"finished hook, finished pane", status.Finished, status.Finished, false, status.Finished},
		{"finished hook, acked finished pane", status.Finished, status.Finished, true, status.Idle},

		{"working hook, waiting pane", status.Working, status.Waiting, false, status.Waiting},
		{"working hook, finished pane", status.Working, status.Finished, false, status.Finished},
		{"working hook, errored pane", status.Working, status.Errored, false, status.Errored},
		{"working hook, acked finished pane", status.Working, status.Finished, true, status.Idle},

		{"errored hook, waiting pane", status.Errored, status.Waiting, false, status.Waiting},
		{"errored hook, working pane", status.Errored, status.Working, false, status.Working},
		{"errored hook, finished pane", status.Errored, status.Finished, false, status.Finished},
		{"errored hook, acked finished pane", status.Errored, status.Finished, true, status.Idle},
	}

	m := buildModel(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pane, ok := precedencePanes[tc.pane]
			if !ok {
				t.Fatalf("no pane fixture for %q", tc.pane)
			}
			sess := store.Session{ID: "prec-" + tc.name, Tool: "claude-hooked", Acked: tc.acked}
			writeHookStatus(t, m, sess.ID, tc.hook)
			if got := deriveStatus(t, m, sess, pane, true); got != tc.want {
				t.Fatalf("hook %q + pane %q (acked=%v) = %q, want %q",
					tc.hook, tc.pane, tc.acked, got, tc.want)
			}
		})
	}
}

// The pane fixtures must actually produce the verdicts the table names,
// or a precedence row could pass for the wrong reason.
func TestPrecedencePaneFixturesMatchTheirVerdicts(t *testing.T) {
	m := buildModel(t)
	for want, pane := range precedencePanes {
		got, matched := m.poller.engine.Match("claude-hooked", pane)
		if !matched || got != want {
			t.Fatalf("pane fixture for %q matched=%v status=%q", want, matched, got)
		}
	}
}
