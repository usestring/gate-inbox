package config

import (
	"testing"

	"github.com/usestring/gate-inbox/internal/priority"
)

func TestAccountSwitchCutoffFollowsUrgentAndHighWindows(t *testing.T) {
	tool := Tool{CompactWindow: map[string]int{"urgent": 750_000, "high": 600_000, "medium": 400_000}}
	for _, tc := range []struct {
		tool Tool
		tier priority.Tier
		want int
	}{
		{tool, priority.Urgent, 750_000},
		{tool, priority.High, 600_000},
		{tool, priority.Medium, 200_000},
		{tool, priority.Low, 200_000},
		{tool, priority.Unset, 200_000},
		{Tool{}, priority.Urgent, 200_000},
		{Tool{CompactWindow: map[string]int{"high": 150_000}}, priority.High, 200_000},
	} {
		if got := tc.tool.AccountSwitchCutoff(tc.tier); got != tc.want {
			t.Errorf("%v at %q: cutoff %d, want %d", tc.tool.CompactWindow, tc.tier, got, tc.want)
		}
	}
}
