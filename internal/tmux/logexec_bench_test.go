package tmux

import (
	"errors"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/logging"
)

// batchedCaptureArgv is the shape the poll pass actually hands logTmux: one
// chained capture carrying a command per pane, which on the operator's board
// runs to kilobytes of argv. The cost being measured is per byte of that
// argv, not per call, because redaction walks the formatted record.
func batchedCaptureArgv(panes int) []string {
	full := []string{"-L", "gate-inbox"}
	for i := 0; i < panes; i++ {
		id := strconv.Itoa(i)
		full = append(full, "capture-pane", "-p", "-J", "-t", "%"+id, "-S", "-200", "-E", "-1", ";")
	}
	return full
}

func benchLogAt(b *testing.B, level slog.Level) {
	b.Helper()
	logger, err := logging.Open(logging.Options{
		Path:  filepath.Join(b.TempDir(), "gate-inbox.log"),
		Level: level, MaxSizeMB: 8, MaxBackups: 1, MaxTotalMB: 16,
	})
	if err != nil {
		b.Fatalf("logging.Open: %v", err)
	}
	previous := logging.SetDefault(logger)
	b.Cleanup(func() {
		logging.SetDefault(previous)
		logger.Close()
	})
}

// BenchmarkRoutineTmuxRecord is the before and after of demoting the success
// branch, measured at the caller. "info" is what a running manager pays now
// (nothing: the record is not written), "debug" is what it paid before, which
// is the redaction pass over the whole argv plus the sink.
func BenchmarkRoutineTmuxRecord(b *testing.B) {
	full := batchedCaptureArgv(60)
	b.Logf("argv is %d bytes over %d arguments", len(strings.Join(full, " ")), len(full))
	for _, level := range []struct {
		name  string
		level slog.Level
	}{{"info", logging.LevelInfo}, {"debug", logging.LevelDebug}} {
		b.Run(level.name, func(b *testing.B) {
			benchLogAt(b, level.level)
			start := time.Now()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				logTmux(full, start, nil, nil)
			}
		})
	}
}

// A failure is written whatever the level, so its cost is unchanged. It is
// here to say so with a number rather than by assertion.
func BenchmarkFailedTmuxRecord(b *testing.B) {
	full := batchedCaptureArgv(60)
	benchLogAt(b, logging.LevelInfo)
	failure := errors.New("no server running on /tmp/tmux-1000/gate-inbox")
	start := time.Now()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logTmux(full, start, failure, []byte("no server running"))
	}
}
