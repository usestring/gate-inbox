package launchpace

import (
	"github.com/shirou/gopsutil/v4/load"

	"github.com/usestring/gate-inbox/internal/sysstat"
)

// hostLoad is the one-minute load average per logical CPU.
func hostLoad() (float64, bool) {
	avg, err := load.Avg()
	if err != nil || avg == nil {
		return 0, false
	}
	return avg.Load1 / float64(max(sysstat.LogicalCPUs(), 1)), true
}
