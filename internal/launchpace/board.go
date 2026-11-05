package launchpace

import "github.com/usestring/gate-inbox/internal/config"

// FromConfig applies the board's [relaunch] settings and host memory guard.
func FromConfig(dir string, r config.Relaunch, settled Settled) *Pacer {
	p := New(dir, Limits{
		MaxInFlight: r.MaxInFlight,
		MinGap:      r.MinGap.Duration,
		MaxGap:      r.MaxGap.Duration,
		Settle:      r.Settle.Duration,
		LoadPerCPU:  r.LoadPerCPU,
	}, settled)
	p.underMemoryPressure = hostMemoryPressure
	return p
}
