package ui

import (
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/logging"
)

// The render and poll loops must never wait on the log. The frame
// benchmarks cannot show that on their own -- View writes no records -- so
// the cost is measured where it is actually paid: one key press, with the
// log off and with it on, at fleet scale.
func BenchmarkFleetKeyDispatch(b *testing.B) {
	for _, level := range []struct {
		label string
		on    bool
	}{{"logOff", false}, {"logOn", true}} {
		b.Run(level.label, func(b *testing.B) {
			if level.on {
				path := filepath.Join(b.TempDir(), "gate-inbox.log")
				logger, err := logging.Open(logging.Options{
					Path: path, Level: logging.LevelInfo,
					MaxSizeMB: 8, MaxBackups: 2, MaxTotalMB: 24, Compress: true,
				})
				if err != nil {
					b.Fatalf("logging.Open: %v", err)
				}
				previous := logging.SetDefault(logger)
				b.Cleanup(func() {
					logging.SetDefault(previous)
					logger.Close()
				})
			} else {
				previous := logging.SetDefault(nil)
				b.Cleanup(func() { logging.SetDefault(previous) })
			}
			m := fleetModel(b, fleetSize, 200, 50)
			m.preview = ""
			// The fixture carries no poll loop; Update tells one what the
			// cursor is on, so it needs somewhere to say it.
			m.poller = &poller{}
			down := tea.KeyPressMsg{Code: tea.KeyDown}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.Update(down)
			}
		})
	}
}
