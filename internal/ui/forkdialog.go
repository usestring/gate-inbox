package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/store"
)

// forkDialogWindow bounds how long after a fork the manager will answer the
// resume dialog for it. Loading a long conversation takes a while, so the
// window is generous; it exists only so the expectation cannot outlive the
// launch it belongs to and answer some later dialog that happens to carry
// the same option.
const forkDialogWindow = 5 * time.Minute

// forkDialog is one fork waiting on the dialog its own resume raises.
type forkDialog struct {
	option string
	keys   []string
	expiry time.Time
}

// expectForkDialog arms the answer for a session the fork key just created.
// Nothing is sent until the option is actually on screen, so an unarmed
// session -- or one whose tool names no dialog -- is never typed into.
func (p *poller) expectForkDialog(id, option string, keys []string) {
	if option == "" || len(keys) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.forkDialogs[id] = forkDialog{option: option, keys: keys, expiry: time.Now().Add(forkDialogWindow)}
}

// maybeAnswerForkDialog picks the fork's option out of a resume dialog the
// agent CLI is showing.
//
// A fork exists to carry the whole conversation, and Claude Code offers to
// resume a large one from a summary instead -- with the summary preselected,
// so the fastest answer is the one that drops every response the fork was
// made for. The manager takes that choice away from the keyboard rather than
// leaving it on a recommendation the operator has to read past mid-launch.
//
// The screen is the gate: keys go out only while the option is visible, so a
// dialog that never appears (a small conversation, a CLI that dropped the
// prompt) costs nothing and a pane at its composer is never typed into.
func (p *poller) maybeAnswerForkDialog(sess store.Session, pane string) error {
	p.mu.Lock()
	want, armed := p.forkDialogs[sess.ID]
	if armed && time.Now().After(want.expiry) {
		delete(p.forkDialogs, sess.ID)
		armed = false
	}
	p.mu.Unlock()
	if !armed || !strings.Contains(ansi.Strip(pane), want.option) {
		return nil
	}
	if err := p.tmux.SendKeys(sess.ID, want.keys...); err != nil {
		// The expectation stays armed: the dialog is still up, so the next
		// pass answers it rather than leaving the fork parked on a question.
		return fmt.Errorf("answer the resume dialog for %s: %w", sess.Name, err)
	}
	p.mu.Lock()
	delete(p.forkDialogs, sess.ID)
	p.mu.Unlock()
	return nil
}
