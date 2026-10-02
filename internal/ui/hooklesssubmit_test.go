package ui

import (
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/status"
)

// A session whose tool is configured for claude-hooks but whose pane was
// adopted -- so nothing writes its hook file -- has to read a composer
// submission off the pane. If the hooks source is attached anyway it never
// produces evidence, the pane source is switched off, and the drain sits on
// the session forever.
func TestHooklessComposerSubmitLandsOffThePane(t *testing.T) {
	m := buildModel(t)
	m.autoProceed = true
	liveTriageFleet(t, m, map[string]string{
		"firstidle":  status.Idle,
		"secondidle": status.Idle,
	})
	for i := range m.sessions {
		m.sessions[i].Tool = "claude-hooked"
	}
	m.hookless = map[string]bool{}
	for _, sess := range m.sessions {
		m.hookless[sess.ID] = true
	}
	// A working signal the pane can actually paint, since the test config's
	// claude-hooked rules never match the plain echo these panes produce.
	engine, err := status.NewEngine(config.Config{Tools: map[string]config.Tool{
		"claude-hooked": {
			DefaultStatus: status.Idle,
			Rules:         []config.Rule{{State: status.Working, Pattern: "WORKING-NOW"}},
		},
	}})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	m.engine = engine
	m.rebuildRows()
	m.triage = true
	m.rebuildRows()

	m.enterFocusOn(t, "firstidle")
	firstID := focusedID(t, m)
	m = pressEnter(m)

	if err := m.tmux.SendText(firstID, "WORKING-NOW"); err != nil {
		t.Fatalf("paint the working signal: %v", err)
	}
	waitForPaneText(t, m, firstID, "WORKING-NOW")
	lookForLanding(t, m)

	if m.mode != modeFocus {
		t.Fatalf("a hookless idle submission dropped out of the queue: %s", m.errBar.text)
	}
	if got := focusedName(t, m); got != "secondidle" {
		t.Fatalf("after a hookless idle prompt landed, focused %q want secondidle", got)
	}
}

// A managed claude session keeps its hooks source: it is the first-hand
// signal, and the pane fallback must not displace it.
func TestManagedComposerSubmitKeepsTheHooksSource(t *testing.T) {
	m := buildModel(t)
	sess := liveHookedSession(t, m, "managed")
	p := m.landingProbeFor(sess, false)
	if p.hooks == nil {
		t.Fatal("a managed claude session lost its hooks source")
	}
	if p.paneWorking != nil {
		t.Fatal("a managed claude composer submit armed the pane fallback alongside hooks")
	}
}

// A hookless claude session falls all the way back to its pane.
func TestHooklessComposerSubmitArmsThePaneFallback(t *testing.T) {
	m := buildModel(t)
	sess := liveHookedSession(t, m, "adopted")
	m.hookless = map[string]bool{sess.ID: true}
	m.preview = "❯ \n"
	p := m.landingProbeFor(sess, false)
	if p.hooks != nil {
		t.Fatal("a hookless session still armed the dead hooks source")
	}
	if p.paneWorking == nil {
		t.Fatal("a hookless composer submit has no source that can see it land")
	}
}
