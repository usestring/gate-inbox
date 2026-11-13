package app

import (
	"context"
	"errors"
	"os"
	"runtime/debug"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/managerbuild"
	"github.com/usestring/gate-inbox/internal/ui"
)

// noAutoRestartEnv turns the restart on a new build off, for someone who
// rebuilds mid-session and wants to pick when the board takes it.
const noAutoRestartEnv = "GATE_INBOX_NO_AUTO_RESTART"

// binaryWatchEvery is how often the board looks at its own binary on disk.
const binaryWatchEvery = 10 * time.Second

// errRestartBoard is runBoard's way of saying the board quit to run a newer
// build of itself. Run re-execs once every defer in runBoard has put the
// terminal, the pins and the singleton lock back.
var errRestartBoard = errors.New("board restarting on a new build")

type binaryStamp struct {
	size int64
	mod  time.Time
}

func stampOf(path string) (binaryStamp, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return binaryStamp{}, false
	}
	return binaryStamp{size: info.Size(), mod: info.ModTime()}, true
}

// watchOwnBinary sends ui.BinaryUpgraded once path holds a build other than
// the running one. A change has to hold still for one more look before it
// counts, so a binary still being written is never what the board re-execs,
// and a touch that leaves the bytes alone restarts nothing.
func watchOwnBinary(ctx context.Context, path, running string, every time.Duration, send func(tea.Msg)) {
	last, ok := stampOf(path)
	if !ok || running == "" {
		return
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	changed := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		now, ok := stampOf(path)
		if !ok {
			// Mid-replace, or removed: look again next tick.
			changed = true
			continue
		}
		if now != last {
			last, changed = now, true
			continue
		}
		if !changed {
			continue
		}
		changed = false
		if managerbuild.DigestFile(path) == running {
			continue
		}
		logging.Info("a new build of the board is on disk", "path", path)
		send(ui.BinaryUpgraded{})
		return
	}
}

// startBinaryWatch watches the binary this process was started from.
func startBinaryWatch(send func(tea.Msg)) func() {
	if os.Getenv(noAutoRestartEnv) != "" {
		return func() {}
	}
	exe, err := os.Executable()
	if err != nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	go watchOwnBinary(ctx, exe, managerbuild.Fingerprint(), binaryWatchEvery, send)
	return cancel
}

// reexecBoard replaces this process with the binary it was started from, with
// the same arguments and environment, so the board comes back in its own pane.
func reexecBoard() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, os.Environ())
}

// buildRevision is the commit the binary was built from, with "+dirty" when
// the tree had uncommitted changes, or "" when the build did not record one.
// A local build reports its version as "dev", which says nothing about which
// code it is running.
func buildRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	var revision string
	dirty := false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			dirty = setting.Value == "true"
		}
	}
	if revision != "" && dirty {
		revision += "+dirty"
	}
	return revision
}
