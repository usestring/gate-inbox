package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hogs"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// writeHogProc lays down a one-process tree per root: the files the hog
// sampler reads, with utime in clock ticks and PSS in KiB.
func writeHogProc(t *testing.T, root string, uptime float64, procs map[int][2]uint64) {
	t.Helper()
	write := func(rel, body string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("uptime", fmt.Sprintf("%.2f 0\n", uptime))
	write("meminfo", "MemTotal: 67108864 kB\nMemAvailable: 33554432 kB\n")
	for pid, v := range procs {
		p := fmt.Sprint(pid)
		// RSS in pages, a little over the PSS: a tree is screened on RSS
		// before its PSS is read, and PSS is never more than RSS.
		write(p+"/stat", fmt.Sprintf("%d (stress) R 1 0 0 0 -1 0 0 0 0 0 %d 0 0 0 20 0 1 0 100 0 %d 0\n", pid, v[0], v[1]/4+16))
		write(p+"/cmdline", "stress\x00--cpu\x008\x00")
		write(p+"/smaps_rollup", fmt.Sprintf("Pss: %d kB\n", v[1]))
		write(p+"/task/"+p+"/children", "")
	}
}

func TestHogWatchQueuesOneSystemNoticePerSessionAndBadgesTheRow(t *testing.T) {
	root := t.TempDir()
	cfg := config.Hogs{
		SampleEvery: config.Duration{Duration: time.Second},
		ResetAfter:  config.Duration{Duration: time.Minute},
		Cooldown:    config.Duration{Duration: time.Hour},
		CPU:         config.HogTiers{Stop: []config.HogRule{{Percent: 400}}},
		Memory:      config.HogTiers{Notice: []config.HogRule{{GiB: 1}}},
	}
	var queued []store.InboxMessage
	w := newHogWatch(cfg, root, nil)
	if w != nil {
		t.Fatal("a /proc with no uptime file was taken as supported")
	}
	writeHogProc(t, root, 1000, map[int][2]uint64{100: {0, 2 << 20}, 200: {0, 2 << 20}, 300: {0, 1024}})
	w = newHogWatch(cfg, root, func(msg store.InboxMessage) error {
		queued = append(queued, msg)
		return nil
	})
	if w == nil {
		t.Fatal("watcher off for a readable /proc")
	}
	targets := []hogTarget{
		{id: "agent", root: 100, interrupt: true},
		{id: "terminal", root: 200, quiet: true},
		{id: "calm", root: 300},
	}
	t0 := time.Unix(1_000_000, 0)
	w.run(t0, targets)
	// First sample: no CPU yet, but memory is over its notice for both
	// heavy trees. The terminal is badged but told nothing.
	if len(queued) != 1 || queued[0].SessionID != "agent" {
		t.Fatalf("queued = %+v, want one memory notice to the agent", queued)
	}
	first := queued[0]
	if first.SenderID != store.SystemSenderID || first.Subject != hogs.Subject || first.Interrupt {
		t.Fatalf("notice = %+v", first)
	}
	if !strings.Contains(first.Body, "Memory notice") || !strings.Contains(first.Body, "pid 100") {
		t.Fatalf("body:\n%s", first.Body)
	}

	// Ten seconds of eight cores on the agent: a stop, which interrupts.
	writeHogProc(t, root, 1010, map[int][2]uint64{100: {8000, 2 << 20}, 200: {0, 2 << 20}, 300: {0, 1024}})
	queued = nil
	w.run(t0.Add(10*time.Second), targets)
	if len(queued) != 1 || !queued[0].Interrupt || !strings.Contains(queued[0].Body, "CPU stop") {
		t.Fatalf("queued = %+v, want one interrupting CPU stop", queued)
	}
	badges := w.badgeRows()
	if badges["agent"].CPU != hogs.TierStop || badges["agent"].Memory != hogs.TierNotice {
		t.Fatalf("agent badge = %+v", badges["agent"])
	}
	if badges["terminal"].Memory != hogs.TierNotice {
		t.Fatalf("terminal badge = %+v, want it drawn though it is never messaged", badges["terminal"])
	}
	if _, ok := badges["calm"]; ok {
		t.Fatal("a calm session wears a badge")
	}
}

func TestHogWatchOfferIsPacedAndNeverOverlaps(t *testing.T) {
	root := t.TempDir()
	writeHogProc(t, root, 1000, nil)
	w := newHogWatch(config.Hogs{SampleEvery: config.Duration{Duration: 10 * time.Second}}, root, func(store.InboxMessage) error { return nil })
	t0 := time.Unix(1_000_000, 0)
	w.busy.Store(true)
	w.offer(t0, nil)
	if !w.lastAt.IsZero() {
		t.Fatal("an offer while a sample is running started another")
	}
	w.busy.Store(false)
	w.offer(t0, nil)
	w.offer(t0.Add(5*time.Second), nil)
	if !w.lastAt.Equal(t0) {
		t.Fatalf("lastAt = %v, want the first offer's time and the second skipped", w.lastAt)
	}
	var nilWatch *hogWatch
	nilWatch.offer(t0, nil)
	if nilWatch.badgeRows() != nil {
		t.Fatal("a switched-off watcher has badges")
	}
}

func TestHogWatchIsOffWhenSwitchedOff(t *testing.T) {
	root := t.TempDir()
	writeHogProc(t, root, 1000, nil)
	off := false
	if w := newHogWatch(config.Hogs{Enabled: &off}, root, nil); w != nil {
		t.Fatal("enabled = false still built a watcher")
	}
}

func TestHogPolicyDropsRulesWithNoThreshold(t *testing.T) {
	policy := hogPolicy(config.Hogs{
		CPU:    config.HogTiers{Notice: []config.HogRule{{For: config.Duration{Duration: time.Minute}}, {Percent: 150}}},
		Memory: config.HogTiers{Warn: []config.HogRule{{AvailableBelow: 10}, {GrowthGiBPerMin: 0.5}}},
	})
	if len(policy.CPU) != 1 || policy.CPU[0].CPUPercent != 150 {
		t.Fatalf("cpu = %+v", policy.CPU)
	}
	if len(policy.Memory) != 1 || policy.Memory[0].GrowthBytesPerMin != 0.5*(1<<30) || policy.Memory[0].Tier != hogs.TierWarn {
		t.Fatalf("memory = %+v", policy.Memory)
	}
}

// The store accepts a notice from the board itself, and the envelope it is
// typed in under says it is Gate Inbox speaking, fenced, with nobody to reply
// to.
func TestSystemNoticeIsQueuedAndDeliveredUnderItsOwnBand(t *testing.T) {
	st, err := store.Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	msg := store.InboxMessage{
		SessionID: "abc123", SenderID: store.SystemSenderID, SenderName: "Gate Inbox",
		Body: "CPU notice: ...", Fingerprint: "f1", Subject: hogs.Subject, SentAt: time.Now(),
	}
	if _, _, err := st.Enqueue(msg, store.DefaultInboxLimits); err != nil {
		t.Fatal(err)
	}
	msg.Body, msg.Fingerprint = "CPU warn: ...", "f2"
	if _, superseded, err := st.Enqueue(msg, store.DefaultInboxLimits); err != nil || superseded != 1 {
		t.Fatalf("second notice: superseded %d, err %v; want it to replace the unread first", superseded, err)
	}
	heads, err := st.HeadMessages()
	if err != nil {
		t.Fatal(err)
	}
	head := heads["abc123"]
	if head.Body != "CPU warn: ..." {
		t.Fatalf("head = %+v", head)
	}
	text := inboxEnvelope(head, "claude", true, messageContext{})
	for _, want := range []string{"Notice from Gate Inbox", "not from the user or from another agent", "do not reply to it", "CPU warn: ..."} {
		if !strings.Contains(text, want) {
			t.Errorf("envelope lacks %q:\n%s", want, text)
		}
	}
	if strings.Count(text, "----GATE-INBOX-NOTICE-") != 3 {
		t.Errorf("envelope is not fenced on both sides:\n%s", text)
	}
	if strings.Contains(text, "send_session") || strings.Contains(text, "CROSS-SESSION") {
		t.Errorf("a board notice names a session to reply to:\n%s", text)
	}
}

func TestHogBadgeWordsAndTints(t *testing.T) {
	cases := []struct {
		badge hogs.Badge
		word  string
	}{
		{hogs.Badge{}, ""},
		{hogs.Badge{CPU: hogs.TierNotice}, "cpu hog"},
		{hogs.Badge{Memory: hogs.TierStop}, "mem hog"},
		{hogs.Badge{CPU: hogs.TierWarn, Memory: hogs.TierNotice}, "cpu+mem hog"},
	}
	for _, tc := range cases {
		got := hogBadge(tc.badge)
		if tc.word == "" {
			if got != "" {
				t.Errorf("%+v drew %q", tc.badge, got)
			}
			continue
		}
		if !strings.Contains(got, tc.word) {
			t.Errorf("%+v drew %q, want %q", tc.badge, got, tc.word)
		}
	}
}
