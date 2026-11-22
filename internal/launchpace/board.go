package launchpace

import "github.com/usestring/gate-inbox/internal/config"

// FromConfig applies the board's [relaunch] settings, and the host memory
// guard when memoryGuard says the experiment is on.
func FromConfig(dir string, r config.Relaunch, settled Settled, memoryGuard bool) *Pacer {
	p := New(dir, Limits{
		MaxInFlight: r.MaxInFlight,
		MinGap:      r.MinGap.Duration,
		MaxGap:      r.MaxGap.Duration,
		Settle:      r.Settle.Duration,
		LoadPerCPU:  r.LoadPerCPU,
	}, settled)
	if memoryGuard {
		p.underMemoryPressure = hostMemoryPressure
	}
	return p
}
