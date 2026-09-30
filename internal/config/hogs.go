package config

import "time"

// The built-in hog tiers. docs/configuration.md carries the same table; a
// change here is a change there.
const (
	defaultHogSampleEvery = 10 * time.Second
	defaultHogResetAfter  = 2 * time.Minute
	defaultHogCooldown    = 30 * time.Minute
)

func hogFor(d time.Duration) Duration { return Duration{Duration: d} }

// DefaultHogTiers returns the built-in CPU and memory tiers, fresh each call
// so no caller can edit another's copy.
func DefaultHogTiers() (cpu, memory HogTiers) {
	cpu = HogTiers{
		Notice: []HogRule{{Percent: 100, For: hogFor(10 * time.Minute)}},
		Warn: []HogRule{
			{Percent: 200, For: hogFor(5 * time.Minute)},
			{Percent: 100, For: hogFor(30 * time.Minute)},
		},
		Stop: []HogRule{
			{Percent: 400, For: hogFor(5 * time.Minute)},
			{Percent: 200, For: hogFor(20 * time.Minute)},
		},
	}
	memory = HogTiers{
		Notice: []HogRule{
			{GiB: 8, For: hogFor(10 * time.Minute)},
			{GiB: 4, AvailableBelow: 25},
		},
		Warn: []HogRule{
			{GiB: 16, For: hogFor(5 * time.Minute)},
			{GrowthGiBPerMin: 1, For: hogFor(5 * time.Minute)},
			{GiB: 4, AvailableBelow: 15},
		},
		// The low-headroom stop's window is short on purpose: the kernel's
		// OOM killer and any low-memory watchdog act in seconds once the host
		// runs out.
		Stop: []HogRule{
			{GiB: 32},
			{GiB: 4, AvailableBelow: 10, For: hogFor(2 * time.Minute)},
		},
	}
	return cpu, memory
}

func (h *Hogs) applyDefaults() {
	if h.SampleEvery.Duration <= 0 {
		h.SampleEvery.Duration = defaultHogSampleEvery
	}
	if h.ResetAfter.Duration <= 0 {
		h.ResetAfter.Duration = defaultHogResetAfter
	}
	if h.Cooldown.Duration <= 0 {
		h.Cooldown.Duration = defaultHogCooldown
	}
	cpu, memory := DefaultHogTiers()
	h.CPU.fill(cpu)
	h.Memory.fill(memory)
}

// fill takes the built-in rules for each tier the file left out. A nil tier
// was never written; an empty one was written as [] and stays off.
func (t *HogTiers) fill(def HogTiers) {
	if t.Notice == nil {
		t.Notice = def.Notice
	}
	if t.Warn == nil {
		t.Warn = def.Warn
	}
	if t.Stop == nil {
		t.Stop = def.Stop
	}
}
