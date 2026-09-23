package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// A user focused inside triage reads the footer to get out. While ctrl+q
// advances, naming it alongside ctrl+\ as "back to manager" sends them to the
// key that keeps them in the queue.
func TestTheFocusedFooterSeparatesTheExitsInTriage(t *testing.T) {
	m := shotModel()
	m.width, m.height = 200, 50
	m.mode = modeFocus
	m.triage = true

	footer := ansi.Strip(m.viewFooter())

	if !strings.Contains(footer, "stop triage") {
		t.Errorf("the focused footer never offers a way out of triage:\n%s", footer)
	}
	if !strings.Contains(footer, "next needing input") {
		t.Errorf("the focused footer does not say ctrl+q advances:\n%s", footer)
	}
	if strings.Contains(footer, `ctrl+q / ctrl+\`) {
		t.Errorf("triage still names the two exits as one, so they read as synonyms:\n%s", footer)
	}
}

// Outside triage they really are synonyms, and splitting them would spend a
// footer row on a distinction that does not exist.
func TestTheFocusedFooterKeepsOneExitOutsideTriage(t *testing.T) {
	m := shotModel()
	m.width, m.height = 200, 50
	m.mode = modeFocus
	m.triage = false

	footer := ansi.Strip(m.viewFooter())

	if !strings.Contains(footer, `ctrl+q / ctrl+\`) {
		t.Errorf("the focused footer lost its combined exit:\n%s", footer)
	}
	if strings.Contains(footer, "stop triage") {
		t.Errorf("a non-triage footer offers to stop triage:\n%s", footer)
	}
}
