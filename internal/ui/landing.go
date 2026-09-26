package ui

import (
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/codexq"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// A key that might answer a session only arms a pending landing. The session
// is handed over once the agent is seen to have taken the answer, never on
// the strength of the key alone.
//
// Reading "done with this one" out of the keystroke was a guess, and every
// wrong guess cost the operator a session: Enter on /model opened a picker on
// a pane the drain had already walked away from, Enter on a stepper question
// moved the stepper along, and a declined permission left the agent stopped
// with nobody watching it. What the agent does next is not a guess. Claude's
// hooks log UserPromptSubmit for a prompt it took and PostToolUse for a call
// it ran, its transcript and Codex's rollout record the message or the
// answer, and a pane that had a dialog up and now shows the agent working has
// had the dialog answered. So the key snapshots where each of those stood, and
// a short run of looks decides.
//
// No evidence inside the window means the operator stays where they are,
// which is the cheap mistake: they press § themselves. Evidence that the
// answer was turned down, or that a fresh dialog came up, keeps them there
// too.

// landingWindow is how long an answer has to show up. The hooks land within
// a few hundred milliseconds of the key; the rest is room for a transcript
// write or a pane repaint on a loaded machine.
const landingWindow = 2500 * time.Millisecond

// landingTick paces the looks. Each is a stat and a short read per source,
// plus one capture when the pane is a source.
var landingTick = 150 * time.Millisecond

type landingVerdict int

const (
	landingPending landingVerdict = iota
	// landingSeen is evidence the agent took the answer.
	landingSeen
	// landingRefused is evidence it did not: the call was declined, the turn
	// interrupted, or another dialog came up.
	landingRefused
)

// pendingLanding is the one answer the board is waiting to see land.
type pendingLanding struct {
	// gen tells this pending from the ones it replaced, so a look still in
	// flight for an older key cannot decide the newer one.
	gen      int
	active   bool
	sess     store.Session
	via      extension.OperatorVia
	text     string
	deadline time.Time
	// handOver is whether landing should also move the drain on. It is read
	// when the key is pressed and cleared by anything that says the operator
	// is not done: another key into the pane, or leaving.
	handOver bool
	probe    *landingProbe
}

// landingCheckMsg is one look's verdict.
type landingCheckMsg struct {
	gen     int
	verdict landingVerdict
}

// landingProbe holds where each source stood when the key was pressed. Only
// the chain of looks for one pending touches it, one look at a time, so it
// needs no lock.
type landingProbe struct {
	// dialog is whether the key answered a dialog rather than a composer
	// line. The two land differently: a prompt as UserPromptSubmit and a
	// typed message, a dialog as a tool result or the agent going back to
	// work.
	dialog bool

	hooks      *hooks.Manager
	hookID     string
	hookOffset int64

	transcript       string
	transcriptOffset int64

	rollout       string
	rolloutOffset int64

	// paneWorking reports the pane showing the agent at work. It is nil
	// where the pane cannot confirm: a composer on a tool with a better
	// source, or a pane that already read working before the key.
	paneWorking func() bool
}

// look reads each source once. Refusal wins over landing within a look, so a
// declined call is never read as answered because something else moved too.
func (p *landingProbe) look() landingVerdict {
	seen, refused := false, false
	if p.hooks != nil {
		if events, next, ok := p.hooks.Events(p.hookID, p.hookOffset); ok {
			p.hookOffset = next
			s, r := p.hookVerdict(events)
			seen, refused = seen || s, refused || r
		}
	}
	if p.transcript != "" {
		if delta, err := convo.Since(p.transcript, p.transcriptOffset); err == nil {
			p.transcriptOffset = delta.Next
			s, r := p.transcriptVerdict(delta)
			seen, refused = seen || s, refused || r
		}
	}
	if p.rollout != "" {
		if act, err := codexq.Since(p.rollout, p.rolloutOffset); err == nil {
			p.rolloutOffset = act.Next
			if p.dialog {
				seen = seen || len(act.Answered) > 0
			} else {
				seen = seen || act.UserMessage
			}
		}
	}
	switch {
	case refused:
		return landingRefused
	case seen:
		return landingSeen
	case p.paneWorking != nil && p.paneWorking():
		return landingSeen
	}
	return landingPending
}

// hookVerdict reads the events logged since the key. The first one that
// decides, decides: a new dialog after the answer landed is the next
// question, not a refusal of this one.
//
// A composer key lands only on UserPromptSubmit. A built-in command such as
// /model is not a prompt to the agent, so it never lands and the drain stays
// on the picker it opens. A dialog key lands on PostToolUse, the call it
// allowed having run; a waiting event first is another dialog, and a stop
// first is the turn ending without the call.
func (p *landingProbe) hookVerdict(events []hooks.Event) (seen, refused bool) {
	for _, ev := range events {
		if !p.dialog {
			if ev.Name == "UserPromptSubmit" {
				return true, false
			}
			continue
		}
		switch {
		case ev.State == status.Working && ev.Name == "PostToolUse":
			return true, false
		case ev.State == status.Waiting, ev.State == status.Finished, ev.State == status.Errored:
			return false, true
		}
	}
	return false, false
}

// interruptedMark opens the user record Claude Code writes when a turn or a
// tool call is interrupted from its pane.
const interruptedMark = "[Request interrupted by user"

func (p *landingProbe) transcriptVerdict(delta convo.Delta) (seen, refused bool) {
	for _, prompt := range delta.Prompts {
		if strings.HasPrefix(prompt, interruptedMark) {
			return false, true
		}
	}
	for _, result := range delta.Results {
		if result.Rejected {
			return false, true
		}
	}
	if p.dialog {
		return len(delta.Results) > 0, false
	}
	return len(delta.Prompts) > 0, false
}

// armLanding starts waiting on an answer about to be sent to sess. dialog is
// whether a dialog was up for it to answer, read before the key moved
// anything, and it must be called before the send so every offset it takes
// is from before anything the answer caused.
func (m *Model) armLanding(sess store.Session, via extension.OperatorVia, text string, dialog, handOver bool) tea.Cmd {
	gen := m.landing.gen + 1
	m.landing = pendingLanding{
		gen:      gen,
		active:   true,
		sess:     sess,
		via:      via,
		text:     text,
		deadline: time.Now().Add(landingWindow),
		handOver: handOver,
		probe:    m.landingProbeFor(sess, dialog),
	}
	return m.landingCheck(gen)
}

// dropLanding forgets the pending answer: the send it was armed for failed.
func (m *Model) dropLanding() {
	m.landing = pendingLanding{gen: m.landing.gen}
}

// landingProbeFor snapshots the sources sess's tool offers.
func (m *Model) landingProbeFor(sess store.Session, dialog bool) *landingProbe {
	p := &landingProbe{dialog: dialog}
	if m.hooks != nil && m.poller != nil && m.poller.statusSources[sess.Tool] == hooks.StatusSourceClaude {
		p.hooks, p.hookID = m.hooks, sess.ID
		p.hookOffset = fileSize(m.hooks.StatusFile(sess.ID))
	}
	format := historyToolFormats(m.cfg)[sess.Tool]
	if m.landingLocator != nil && (format == search.ToolClaude || format == search.ToolCodex) {
		if target, ok := m.landingLocator.Target(sess.ID, format, sess.Cwd, sess.AgentSessionID); ok {
			if format == search.ToolClaude {
				p.transcript, p.transcriptOffset = target.Path, fileSize(target.Path)
			} else {
				p.rollout, p.rolloutOffset = target.Path, fileSize(target.Path)
			}
		}
	}
	// The pane is the fallback source. A dialog that gives way to the agent
	// working has been answered however long the call it allowed then runs,
	// which the hooks and the transcript only report once the call ends. On
	// a composer it is the only source a tool without hooks or a transcript
	// has. Either way it counts only as a change: a pane that already read
	// working says nothing about this key.
	paneOnly := p.hooks == nil && p.transcript == "" && p.rollout == ""
	if (dialog || paneOnly) && m.tmux != nil && m.engine != nil && !paneWorks(m.engine, sess.Tool, m.preview) {
		driver, engine, id, tool := m.tmux, m.engine, sess.ID, sess.Tool
		p.paneWorking = func() bool {
			pane, err := driver.CapturePane(id)
			if err != nil {
				return false
			}
			return paneWorks(engine, tool, pane)
		}
	}
	return p
}

func paneWorks(engine *status.Engine, tool, pane string) bool {
	text := ansi.Strip(pane)
	if engine.ViewportDisplaced(tool, text) {
		return false
	}
	state, matched := engine.Match(tool, text)
	return matched && state == status.Working
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// landingCheck schedules the next look. The look runs in the command's
// goroutine, off the event loop.
func (m *Model) landingCheck(gen int) tea.Cmd {
	probe := m.landing.probe
	if probe == nil {
		return nil
	}
	return tea.Tick(landingTick, func(time.Time) tea.Msg {
		return landingCheckMsg{gen: gen, verdict: probe.look()}
	})
}

// keepLandingHere takes the handover off the pending answer: the operator
// has gone on typing into the session, or left it, so it is no longer the
// board's to move them off. The answer still counts as sent once it lands.
func (m *Model) keepLandingHere() {
	m.landing.handOver = false
}

// applyLandingCheck acts on one look.
func (m *Model) applyLandingCheck(msg landingCheckMsg) tea.Cmd {
	pending := m.landing
	if !pending.active || msg.gen != pending.gen {
		return nil
	}
	switch {
	case msg.verdict == landingSeen:
		m.dropLanding()
		return m.answerLanded(pending)
	case msg.verdict == landingRefused, !time.Now().Before(pending.deadline):
		m.dropLanding()
		return nil
	}
	return m.landingCheck(pending.gen)
}

// answerLanded records the answer as sent and, when the operator is still on
// the session and the drain proceeds by itself, hands it over.
func (m *Model) answerLanded(pending pendingLanding) tea.Cmd {
	m.noteSubmission(pending.sess)
	m.noteOperator(pending.sess, pending.via, pending.text, pending.probe != nil && pending.probe.dialog)
	if !pending.handOver || !m.autoProceeds() {
		return nil
	}
	sess, ok := m.selected()
	if !ok || sess.ID != pending.sess.ID {
		return nil
	}
	return m.handOverLanded(sess, time.Now())
}
