package launchpace

import "github.com/usestring/gate-inbox/internal/config"

// FromConfig is the pacer of the board in dir, read from its [relaunch]
// settings.
func FromConfig(dir string, r config.Relaunch, settled Settled) *Pacer {
	return New(dir, Limits{
		MaxInFlight: r.MaxInFlight,
		MinGap:      r.MinGap.Duration,
		MaxGap:      r.MaxGap.Duration,
		Settle:      r.Settle.Duration,
		LoadPerCPU:  r.LoadPerCPU,
	}, settled)
}
