package app

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/ui"
)

func TestWriteDumpIncludesTheBoardStateAndStacks(t *testing.T) {
	dir := t.TempDir()
	send := func(msg tea.Msg) {
		if req, ok := msg.(ui.DumpRequest); ok {
			req.Reply <- "== board state at now\nmode=list\n"
		}
	}
	path, err := writeDump(dir, send, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"mode=list", "== goroutines", "goroutine "} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("dump lacks %q:\n%s", want, data)
		}
	}
}

func TestWriteDumpSaysSoWhenTheLoopDoesNotAnswer(t *testing.T) {
	dir := t.TempDir()
	path, err := writeDump(dir, func(tea.Msg) {}, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "did not answer") || !strings.Contains(string(data), "== goroutines") {
		t.Fatalf("dump of a wedged loop:\n%s", data)
	}
}
