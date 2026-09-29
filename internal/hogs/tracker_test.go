package hogs

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const gib = 1 << 30

// testPolicy is the documented default table, written out here rather than
// read from the config package so a change to either is a visible diff.
func testPolicy() Policy {
	return Policy{
		ResetAfter: 2 * time.Minute,
		Cooldown:   30 * time.Minute,
		CPU: []Rule{
			{Tier: TierNotice, CPUPercent: 100, For: 10 * time.Minute},
			{Tier: TierWarn, CPUPercent: 200, For: 5 * time.Minute},
			{Tier: TierWarn, CPUPercent: 100, For: 30 * time.Minute},
			{Tier: TierStop, CPUPercent: 400, For: 5 * time.Minute},
			{Tier: TierStop, CPUPercent: 200, For: 20 * time.Minute},
		},
		Memory: []Rule{
			{Tier: TierNotice, MemBytes: 8 * gib, For: 10 * time.Minute},
			{Tier: TierNotice, MemBytes: 4 * gib, HostAvailableBelow: 25},
			{Tier: TierWarn, MemBytes: 16 * gib, For: 5 * time.Minute},
			{Tier: TierWarn, GrowthBytesPerMin: 1 * gib, For: 5 * time.Minute},
			{Tier: TierWarn, MemBytes: 4 * gib, HostAvailableBelow: 15},
			{Tier: TierStop, MemBytes: 32 * gib},
			{Tier: TierStop, MemBytes: 4 * gib, HostAvailableBelow: 10, For: 2 * time.Minute},
		},
	}
}

// step is one sample: the session's CPU and memory from At until the next
// step, and the host's available percentage.
type step struct {
	at    time.Duration
	cpu   float64
	mem   uint64
	avail float64
}

// sent is one alert the run should raise, at the sample time it is raised.
type sent struct {
	at   time.Duration
	kind Kind
	tier Tier
}

// run samples every 10s between steps, holding each step's reading until the
// next, and returns every alert raised.
func run(policy Policy, steps []step, until time.Duration) ([]sent, *Tracker) {
	tr := NewTracker(policy)
	base := time.Unix(1_000_000, 0)
	var out []sent
	cur := 0
	for at := time.Duration(0); at <= until; at += 10 * time.Second {
		for cur+1 < len(steps) && steps[cur+1].at <= at {
			cur++
		}
		s := steps[cur]
		avail := s.avail
		if avail == 0 {
			avail = 60
		}
		sample := Sample{
			At:   base.Add(at),
			Host: Host{OK: true, MemTotal: 100 * gib, MemAvailable: uint64(avail * gib)},
			Sessions: map[string]Usage{"s": {
				CPUPercent: s.cpu, CPUValid: true, MemBytes: s.mem,
			}},
		}
		for _, a := range tr.Observe(sample)["s"] {
			out = append(out, sent{at: at, kind: a.Kind, tier: a.Tier})
		}
	}
	return out, tr
}

func (s sent) String() string { return fmt.Sprintf("%s %s@%s", s.kind, s.tier, s.at) }

func sentString(ss []sent) string {
	var parts []string
	for _, s := range ss {
		parts = append(parts, s.String())
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func TestTrackerTiers(t *testing.T) {
	m := time.Minute
	cases := []struct {
		name  string
		steps []step
		until time.Duration
		want  []sent
	}{
		{
			name:  "under every threshold says nothing",
			steps: []step{{cpu: 90, mem: 3 * gib}},
			until: 60 * m,
		},
		{
			name:  "one core for ten minutes is a notice, and only one inside the cooldown",
			steps: []step{{cpu: 120}},
			until: 25 * m,
			want:  []sent{{10 * m, KindCPU, TierNotice}},
		},
		{
			name:  "one core for thirty minutes escalates to warn",
			steps: []step{{cpu: 120}},
			until: 31 * m,
			want:  []sent{{10 * m, KindCPU, TierNotice}, {30 * m, KindCPU, TierWarn}},
		},
		{
			name:  "two cores for five minutes goes straight to warn",
			steps: []step{{cpu: 250}},
			until: 9 * m,
			want:  []sent{{5 * m, KindCPU, TierWarn}},
		},
		{
			name:  "four cores for five minutes goes straight to stop",
			steps: []step{{cpu: 450}},
			until: 9 * m,
			want:  []sent{{5 * m, KindCPU, TierStop}},
		},
		{
			name:  "two cores for twenty minutes is stop after warn",
			steps: []step{{cpu: 250}},
			until: 21 * m,
			want:  []sent{{5 * m, KindCPU, TierWarn}, {20 * m, KindCPU, TierStop}},
		},
		{
			name:  "a dip shorter than reset_after keeps the window open",
			steps: []step{{cpu: 150}, {at: 6 * m, cpu: 10}, {at: 7 * m, cpu: 150}},
			until: 11 * m,
			want:  []sent{{10 * m, KindCPU, TierNotice}},
		},
		{
			name:  "a dip past reset_after starts the window again",
			steps: []step{{cpu: 150}, {at: 6 * m, cpu: 10}, {at: 9 * m, cpu: 150}},
			until: 19 * m,
			want:  []sent{{19 * m, KindCPU, TierNotice}},
		},
		{
			name:  "escalation is upward only: a load that eases after warn is not told notice",
			steps: []step{{cpu: 250}, {at: 6 * m, cpu: 120}},
			until: 29 * m,
			want:  []sent{{5 * m, KindCPU, TierWarn}},
		},
		{
			name:  "a load that returns right after its episode ended is not re-told inside the cooldown",
			steps: []step{{cpu: 150}, {at: 11 * m, cpu: 0}, {at: 14 * m, cpu: 150}},
			until: 30 * m,
			want:  []sent{{10 * m, KindCPU, TierNotice}},
		},
		{
			name:  "memory over 8 GiB for ten minutes is a notice",
			steps: []step{{mem: 9 * gib}},
			until: 12 * m,
			want:  []sent{{10 * m, KindMemory, TierNotice}},
		},
		{
			name:  "memory over 16 GiB for five minutes is a warn",
			steps: []step{{mem: 17 * gib}},
			until: 6 * m,
			want:  []sent{{5 * m, KindMemory, TierWarn}},
		},
		{
			name:  "memory over 32 GiB is a stop on the first sample",
			steps: []step{{mem: 33 * gib}},
			until: 1 * m,
			want:  []sent{{0, KindMemory, TierStop}},
		},
		{
			name:  "4 GiB is nothing on a roomy host",
			steps: []step{{mem: 5 * gib, avail: 60}},
			until: 20 * m,
		},
		{
			name:  "4 GiB while the host is under 25% available is a notice at once",
			steps: []step{{mem: 5 * gib, avail: 20}},
			until: 1 * m,
			want:  []sent{{0, KindMemory, TierNotice}},
		},
		{
			name:  "4 GiB while the host is under 15% available is a warn at once",
			steps: []step{{mem: 5 * gib, avail: 12}},
			until: 1 * m,
			want:  []sent{{0, KindMemory, TierWarn}},
		},
		{
			name:  "4 GiB while the host is under 10% for two minutes is a stop",
			steps: []step{{mem: 5 * gib, avail: 8}},
			until: 3 * m,
			want:  []sent{{0, KindMemory, TierWarn}, {2 * m, KindMemory, TierStop}},
		},
		{
			name:  "headroom recovering before two minutes holds off the stop",
			steps: []step{{mem: 5 * gib, avail: 8}, {at: 1 * m, avail: 40, mem: 5 * gib}},
			until: 10 * m,
			want:  []sent{{0, KindMemory, TierWarn}},
		},
		{
			name:  "a small session is not blamed for a full host",
			steps: []step{{mem: 1 * gib, avail: 5}},
			until: 10 * m,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := run(testPolicy(), tc.steps, tc.until)
			if sentString(got) != sentString(tc.want) {
				t.Fatalf("alerts = %s, want %s", sentString(got), sentString(tc.want))
			}
		})
	}
}

// The cooldown bounds how often a tier is repeated; once it has passed, a
// load still at that tier is told again.
func TestTrackerRepeatsATierAfterTheCooldown(t *testing.T) {
	policy := testPolicy()
	policy.CPU = []Rule{{Tier: TierNotice, CPUPercent: 100, For: 10 * time.Minute}}
	got, _ := run(policy, []step{{cpu: 120}}, 45*time.Minute)
	want := []sent{{10 * time.Minute, KindCPU, TierNotice}, {40 * time.Minute, KindCPU, TierNotice}}
	if sentString(got) != sentString(want) {
		t.Fatalf("alerts = %s, want %s", sentString(got), sentString(want))
	}
}

// A leak: steady growth of 1.5 GiB a minute from a small start trips the
// growth rule after five minutes of measured slope, before the tree is large
// enough for any size rule.
func TestTrackerCatchesALeakByItsSlope(t *testing.T) {
	tr := NewTracker(testPolicy())
	base := time.Unix(1_000_000, 0)
	var first time.Duration = -1
	for at := time.Duration(0); at <= 8*time.Minute; at += 10 * time.Second {
		mem := uint64(512<<20) + uint64(at.Minutes()*1.5*gib)
		sample := Sample{At: base.Add(at), Host: Host{OK: true, MemTotal: 100 * gib, MemAvailable: 80 * gib},
			Sessions: map[string]Usage{"s": {MemBytes: mem}}}
		for _, a := range tr.Observe(sample)["s"] {
			if a.Kind == KindMemory && a.Tier == TierWarn && first < 0 {
				first = at
				if a.Rule.GrowthBytesPerMin == 0 {
					t.Fatalf("warn raised by %+v, want the growth rule", a.Rule)
				}
			}
		}
	}
	// The slope is known from 30s in, and the rule then needs five minutes.
	if first < 5*time.Minute || first > 6*time.Minute {
		t.Fatalf("leak warned at %s, want between 5m and 6m", first)
	}
}

// A flat tree at 3 GiB is not a leak, however long it sits there.
func TestTrackerDoesNotCallAFlatTreeALeak(t *testing.T) {
	got, _ := run(testPolicy(), []step{{mem: 3 * gib}}, 30*time.Minute)
	if len(got) != 0 {
		t.Fatalf("alerts = %s, want none", sentString(got))
	}
}

// CPU and memory are separate episodes: memory reaching warn does not stop
// CPU from being told its own notice, and both firing in one sample come back
// together so the watcher can send one message.
func TestTrackerKeepsKindsApartAndReportsThemTogether(t *testing.T) {
	got, tr := run(testPolicy(), []step{{cpu: 120, mem: 17 * gib}}, 11*time.Minute)
	want := []sent{{5 * time.Minute, KindMemory, TierWarn}, {10 * time.Minute, KindCPU, TierNotice}}
	if sentString(got) != sentString(want) {
		t.Fatalf("alerts = %s, want %s", sentString(got), sentString(want))
	}
	badge := tr.Badges()["s"]
	if badge.CPU != TierNotice || badge.Memory != TierWarn {
		t.Fatalf("badge = %+v, want cpu notice and memory warn", badge)
	}

	both, _ := run(testPolicy(), []step{{cpu: 450, mem: 33 * gib}}, 5*time.Minute)
	var at5 []sent
	for _, s := range both {
		if s.at == 5*time.Minute {
			at5 = append(at5, s)
		}
	}
	if len(at5) != 1 || at5[0].kind != KindCPU {
		t.Fatalf("at 5m: %s, want the CPU stop alone (memory stopped at 0s)", sentString(at5))
	}
}

// The badge clears once the episode ends, and a session that leaves the
// sample takes its state with it.
func TestTrackerBadgeClearsWithTheEpisode(t *testing.T) {
	_, tr := run(testPolicy(), []step{{cpu: 150}, {at: 11 * time.Minute, cpu: 0}}, 14*time.Minute)
	if badges := tr.Badges(); len(badges) != 0 {
		t.Fatalf("badges = %+v after the load has been gone past reset_after", badges)
	}
	_, tr = run(testPolicy(), []step{{cpu: 150}}, 11*time.Minute)
	tr.Observe(Sample{At: time.Unix(2_000_000, 0), Sessions: map[string]Usage{}})
	if len(tr.sessions) != 0 {
		t.Fatal("a session that left the sample is still tracked")
	}
}

// A first sample carries no CPU, and must not read as zero load closing a
// window or as a reading at all.
func TestTrackerIgnoresCPUItCannotMeasure(t *testing.T) {
	tr := NewTracker(Policy{CPU: []Rule{{Tier: TierStop, CPUPercent: 1}}})
	got := tr.Observe(Sample{At: time.Unix(1, 0), Sessions: map[string]Usage{"s": {CPUPercent: 999}}})
	if len(got) != 0 {
		t.Fatalf("alerts = %+v from a sample with no valid CPU", got)
	}
}

func TestComposeMergesKindsAndAsksByTheFirmestTier(t *testing.T) {
	usage := Usage{
		CPUPercent: 450, CPUValid: true, MemBytes: 20 * gib,
		TopCPU: []ProcUsage{{PID: 12, Command: "go test ./...", CPUPercent: 400}},
		TopMem: []ProcUsage{{PID: 99, Command: "chrome --headless", MemBytes: 12 * gib}},
	}
	alerts := []Alert{
		{Kind: KindCPU, Tier: TierStop, Rule: Rule{CPUPercent: 400}, Held: 5 * time.Minute},
		{Kind: KindMemory, Tier: TierWarn, Rule: Rule{MemBytes: 16 * gib}, Held: 6 * time.Minute},
	}
	body := Compose(alerts, usage, Host{OK: true, MemTotal: 64 * gib, MemAvailable: 10 * gib})
	for _, want := range []string{
		"CPU stop", "450%", "pid 12, 400% CPU: go test ./...",
		"Memory warn", "20.0 GiB", "pid 99, 12.0 GiB: chrome --headless", "10.0 GiB of 64.0 GiB available",
		"Stop the named processes now", "CPUQuota=200%", "MemoryMax=",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("notice lacks %q:\n%s", want, body)
		}
	}
	if Highest(alerts) != TierStop {
		t.Fatalf("Highest = %s", Highest(alerts))
	}
	notice := Compose([]Alert{{Kind: KindCPU, Tier: TierNotice, Rule: Rule{CPUPercent: 100}, Held: 10 * time.Minute}}, usage, Host{})
	if !strings.Contains(notice, "Confirm this work is intended") || strings.Contains(notice, "MemoryMax") {
		t.Fatalf("notice-tier CPU message:\n%s", notice)
	}
}
