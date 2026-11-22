package launchpace

import (
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
)

func TestFromConfigAppliesTheMemoryGuardOnlyWhenTheExperimentIsOn(t *testing.T) {
	if p := FromConfig(t.TempDir(), config.Relaunch{}, nil, false); p.underMemoryPressure != nil {
		t.Fatal("memory guard applied with the experiment off")
	}
	if p := FromConfig(t.TempDir(), config.Relaunch{}, nil, true); p.underMemoryPressure == nil {
		t.Fatal("memory guard missing with the experiment on")
	}
}
