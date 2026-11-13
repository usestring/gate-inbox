package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/managerbuild"
	"github.com/usestring/gate-inbox/internal/ui"
)

func writeBinary(t *testing.T, path, body string, mod time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

// watch runs the watcher over path and reports whether it announced a new
// build before the deadline.
func watch(t *testing.T, path, running string, change func()) bool {
	t.Helper()
	got := make(chan tea.Msg, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		watchOwnBinary(ctx, path, running, 5*time.Millisecond, func(msg tea.Msg) { got <- msg })
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	change()
	select {
	case msg := <-got:
		if _, ok := msg.(ui.BinaryUpgraded); !ok {
			t.Fatalf("watcher sent %T", msg)
		}
		return true
	case <-time.After(300 * time.Millisecond):
		cancel()
		<-done
		return false
	}
}

func TestWatchOwnBinaryAnnouncesANewBuild(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gate-inbox")
	start := time.Now().Add(-time.Hour)
	writeBinary(t, path, "old build", start)
	if !watch(t, path, managerbuild.DigestFile(path), func() { writeBinary(t, path, "new build", start.Add(time.Minute)) }) {
		t.Fatal("a replaced binary was never announced")
	}
}

func TestWatchOwnBinaryIgnoresATouchThatKeepsTheBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gate-inbox")
	start := time.Now().Add(-time.Hour)
	writeBinary(t, path, "same build", start)
	if watch(t, path, managerbuild.DigestFile(path), func() { writeBinary(t, path, "same build", start.Add(time.Minute)) }) {
		t.Fatal("a touch with unchanged bytes restarted the board")
	}
}

func TestWatchOwnBinaryIsQuietWhenNothingChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gate-inbox")
	writeBinary(t, path, "build", time.Now().Add(-time.Hour))
	if watch(t, path, managerbuild.DigestFile(path), func() {}) {
		t.Fatal("an untouched binary was announced as new")
	}
}
