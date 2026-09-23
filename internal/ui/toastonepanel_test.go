package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The notice card renders on a one-panel terminal, where there is no rail
// for it to keep off.
func TestStatusToastShowsOnOnePanel(t *testing.T) {
	m := shotModel()
	m.width, m.height = 45, 30
	if _, right := m.splitWidths(); right != 0 {
		t.Fatalf("45 columns should be one panel, right width is %d", right)
	}
	m.errBar.text = "already up to date"
	if !strings.Contains(ansi.Strip(m.viewListFrame()), "already up to date") {
		t.Fatalf("the notice is not on the one-panel frame:\n%s", m.viewListFrame())
	}
}
