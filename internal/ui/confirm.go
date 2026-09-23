// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// A destructive answer is worth a dialog rather than a line of status text:
// the question sits in the middle of the frame, its target is spelled out,
// and the safe answer is the one already under the cursor.

// confirmTitle names the dialog after the act it is about to commit.
func (m *Model) confirmTitle() string {
	subject := "session"
	if m.confirm.isGroup {
		subject = "group"
	}
	// Whose pane it is belongs in the title: that is the line the operator
	// reads before the sentence under it, and "End session" says nothing
	// about a session the manager never started.
	if !m.confirm.isGroup && m.confirm.action == actionArchive && m.confirmAdopted() > 0 {
		return "▲ End someone else's pane"
	}
	switch m.confirm.action {
	case actionArchive:
		return "◇ End " + subject
	case actionRestore:
		return "◆ Restore " + subject
	case actionRestart:
		return "↻ Restart " + subject
	case actionRevive:
		return "◆ Revive " + subject
	case actionResume:
		return "↻ Restart " + subject
	case actionTakeover:
		return "↻ Take over adopted panes"
	default:
		// Unreachable: every builder sets an action, and the dispatch refuses
		// one that does not. Named rather than left blank so a dialog that
		// ever did slip through says what it is instead of nothing.
		return "▲ Confirm " + subject
	}
}

// confirmDestructive reports whether the pending answer takes something
// away, which decides whether the dialog reads as an alarm or as a move.
func (m *Model) confirmDestructive() bool {
	return m.confirm.action == actionRestart || m.confirm.action == actionArchive || m.confirm.action == actionTakeover
}

// confirmAdopted counts the panes in the pending answer that the manager
// never started. Nothing else the dialog asks can end somebody else's work,
// so this is the one case where the consequence line is the warning and has
// to be read, not skimmed past in grey.
func (m *Model) confirmAdopted() int {
	adopted := 0
	for _, sess := range m.confirm.sessions {
		if sess.TmuxPaneID != "" {
			adopted++
		}
	}
	return adopted
}

func (m *Model) viewConfirm() string {
	width := m.cardWidth()
	inner := cardInnerWidth(width)

	question, consequence := splitConfirmLabel(m.confirm.label)
	tone := annotationStyle
	if m.confirmDestructive() {
		tone = errStyle
	}

	var body strings.Builder
	for _, line := range strings.Split(ansi.Wordwrap(question, inner, "-"), "\n") {
		body.WriteString(tone.Render(line) + "\n")
	}
	if consequence != "" {
		aside := mutedStyle
		if m.confirmAdopted() > 0 {
			aside = errStyle
		}
		body.WriteString("\n")
		for _, line := range strings.Split(ansi.Wordwrap(consequence, inner, "-"), "\n") {
			body.WriteString(aside.Render(line) + "\n")
		}
	}
	// The tick sits under the consequence, where a reader arrives at it
	// having read what the answer covers. It is quiet until a y lands on an
	// unticked box, and then it is the loudest line in the dialog: that y
	// was the operator saying yes, and this is the only thing standing
	// between it and every session on screen.
	if m.confirm.ack != "" {
		box, tone := "[ ] ", mutedStyle
		if m.confirm.acked {
			box, tone = "[x] ", annotationStyle
		}
		if m.confirm.nudged {
			tone = errStyle
		}
		body.WriteString("\n")
		for _, line := range strings.Split(ansi.Wordwrap(box+m.confirm.ack, inner, "-"), "\n") {
			body.WriteString(tone.Render(line) + "\n")
		}
		if m.confirm.nudged {
			body.WriteString(errStyle.Render("tick it with space first") + "\n")
		}
	}

	answer := "confirm"
	switch m.confirm.action {
	case actionArchive:
		answer = "end"
	case actionRestore:
		answer = "restore"
	case actionRestart:
		answer = "restart"
	case actionRevive:
		answer = "revive"
	case actionResume:
		answer = "restart"
	case actionTakeover:
		answer = "take over"
	}
	hint := [][2]string{{"y/↵", answer}, {"n/esc", "cancel"}}
	if m.confirm.ack != "" {
		hint = [][2]string{{"space", "tick"}, {"y/↵", answer}, {"n/esc", "cancel"}}
	}
	// The choice is shown as its current state rather than as an offer, so
	// the operator reads what y is about to do instead of what k would.
	if len(m.confirm.keptChildren) > 0 {
		// "Kept" means left as they are, which is the opposite outcome on a
		// dialog that starts things from one that ends them: an archive
		// leaves a child running, a restart leaves it dead. Saying "leave
		// them running" under a restart would name the state the toggle
		// moves AWAY from, and the count says how much is behind the word.
		noun := "session"
		if len(m.confirm.keptChildren) != 1 {
			noun = "sessions"
		}
		subject := fmt.Sprintf("%d spawned %s", len(m.confirm.keptChildren), noun)
		state := "take them too"
		if m.confirm.keepChildren {
			state = "leave them running"
		}
		if m.confirm.action == actionResume {
			subject = fmt.Sprintf("%d dead %s under it", len(m.confirm.keptChildren), noun)
			state = "bring them back too"
			if m.confirm.keepChildren {
				state = "leave them as they are"
			}
			if len(m.confirm.keptChildren) == 1 {
				state = "bring it back too"
				if m.confirm.keepChildren {
					state = "leave it as it is"
				}
			}
		}
		body.WriteString("\n" + annotationStyle.Render(subject+": "+state) + "\n")
		hint = append([][2]string{{"k", "spawned"}}, hint...)
	}
	return m.cardSized(width, m.confirmTitle(), strings.TrimRight(body.String(), "\n"), hint)
}

// splitConfirmLabel cuts a confirm sentence into the question the dialog
// asks and the consequence it sets beneath, so the two read at different
// weights instead of as one run of prose.
func splitConfirmLabel(label string) (string, string) {
	if mark := strings.Index(label, "? "); mark >= 0 {
		return label[:mark+1], strings.TrimSpace(label[mark+2:])
	}
	return label, ""
}
