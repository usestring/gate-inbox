package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/ui"
)

func answerDumps(msg tea.Msg) {
	if req, ok := msg.(ui.DumpRequest); ok {
		req.Reply <- "== board state at now\nmode=list\n"
	}
}

func TestWriteDumpIncludesTheReasonStateAndStacks(t *testing.T) {
	path, err := writeDump(t.TempDir(), "because", answerDumps, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"reason: because", "mode=list", "== goroutines", "goroutine "} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("dump lacks %q:\n%s", want, data)
		}
	}
}

func TestWriteDumpSaysSoWhenTheLoopDoesNotAnswer(t *testing.T) {
	path, err := writeDump(t.TempDir(), "x", func(tea.Msg) {}, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "did not answer") || !strings.Contains(string(data), "== goroutines") {
		t.Fatalf("dump of a wedged loop:\n%s", data)
	}
}

func TestAutoDumpsAreRateLimited(t *testing.T) {
	d := &dumper{auto: make(chan string, 1)}
	now := time.Now()
	if !d.allowAuto(now) {
		t.Fatal("first automatic dump refused")
	}
	if d.allowAuto(now.Add(autoDumpEvery / 2)) {
		t.Fatal("second automatic dump inside the limit allowed")
	}
	if !d.allowAuto(now.Add(autoDumpEvery + time.Second)) {
		t.Fatal("automatic dump after the limit refused")
	}
}

func TestPruneDumpsKeepsTheNewest(t *testing.T) {
	dir := t.TempDir()
	for i := range 5 {
		name := fmt.Sprintf("dump-20261104T1800%02d.000Z.txt", i)
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "gate-inbox.log"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	pruneDumps(dir, 2)
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := "dump-20261104T180003.000Z.txt dump-20261104T180004.000Z.txt gate-inbox.log"
	if got := strings.Join(names, " "); got != want {
		t.Fatalf("after prune: %s, want %s", got, want)
	}
}

func TestWatchdogTriggersWhenThePingGoesUnanswered(t *testing.T) {
	d := &dumper{auto: make(chan string, 1)}
	held := make(chan chan<- struct{}, 1)
	d.send = func(msg tea.Msg) {
		if ping, ok := msg.(ui.LoopPing); ok {
			held <- ping.Reply // a wedged loop: never answered
		}
	}
	done := make(chan struct{})
	defer close(done)
	go d.watchLoop(done, 10*time.Millisecond, 50*time.Millisecond)
	select {
	case reason := <-d.auto:
		if !strings.Contains(reason, "did not answer a ping") {
			t.Fatalf("reason = %q", reason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watchdog never triggered")
	}
}

func TestWatchdogStaysQuietWhileTheLoopAnswers(t *testing.T) {
	d := &dumper{auto: make(chan string, 1)}
	d.send = func(msg tea.Msg) {
		if ping, ok := msg.(ui.LoopPing); ok {
			ping.Reply <- struct{}{}
		}
	}
	done := make(chan struct{})
	go d.watchLoop(done, 5*time.Millisecond, 50*time.Millisecond)
	time.Sleep(300 * time.Millisecond)
	close(done)
	select {
	case reason := <-d.auto:
		t.Fatalf("watchdog triggered on a live loop: %s", reason)
	default:
	}
}
