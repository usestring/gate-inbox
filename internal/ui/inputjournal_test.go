package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestInputJournalRingKeepsTheNewestOldestFirst(t *testing.T) {
	var j inputJournal
	base := time.Unix(0, 0)
	for i := range inputJournalSize + 10 {
		j.add(inputEntry{at: base.Add(time.Duration(i) * time.Second), kind: "key", input: string(rune('a' + i%26))})
	}
	got := j.entries()
	if len(got) != inputJournalSize {
		t.Fatalf("entries = %d, want %d", len(got), inputJournalSize)
	}
	if !got[0].at.Equal(base.Add(10*time.Second)) || !got[len(got)-1].at.Equal(base.Add(time.Duration(inputJournalSize+9)*time.Second)) {
		t.Fatalf("ring order wrong: first %v last %v", got[0].at, got[len(got)-1].at)
	}
}

func TestInputSummaryCountsKeysAndMouseByBranchOncePerWindow(t *testing.T) {
	var j inputJournal
	start := time.Unix(1000, 0)
	j.add(inputEntry{at: start, kind: "key", input: "enter", branch: "list"})
	j.add(inputEntry{at: start, kind: "key", input: "enter", branch: "resize"})
	j.add(inputEntry{at: start, kind: "MouseWheelMsg", input: "wheeldown"})
	if _, due := j.summaryDue(start.Add(30 * time.Second)); due {
		t.Fatal("summary due before its minute")
	}
	fields, due := j.summaryDue(start.Add(inputSummaryEvery))
	if !due {
		t.Fatal("summary not due after a minute")
	}
	got := map[string]any{}
	for i := 0; i+1 < len(fields); i += 2 {
		got[fields[i].(string)] = fields[i+1]
	}
	if got["keys"] != 2 || got["mouse"] != 1 || got["byBranch"] != "list=1,resize=1" || got["recentKeys"] != "enter enter" {
		t.Fatalf("summary = %v", got)
	}
	if _, due := j.summaryDue(start.Add(3 * inputSummaryEvery)); due {
		t.Fatal("an empty window was summarised")
	}
}

func TestUpdateJournalsInputsAndAnswersADump(t *testing.T) {
	m := buildModel(t)
	if err := m.store.CreateGroup("work", ""); err != nil {
		t.Fatal(err)
	}
	m.applyCmd(t, m.refreshCmd())
	m.selectGroupRow(t, "work")

	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})

	entries := m.inputs.entries()
	if len(entries) != 2 {
		t.Fatalf("journal = %d entries, want 2", len(entries))
	}
	if e := entries[0]; e.kind != "key" || e.input != "enter" || e.branch != "list" || e.row != "group:work" {
		t.Fatalf("key entry = %+v", e)
	}
	if e := entries[1]; e.kind != "MouseWheelMsg" {
		t.Fatalf("mouse entry kind = %q", e.kind)
	}

	reply := make(chan string, 1)
	m.Update(DumpRequest{Reply: reply})
	dump := <-reply
	for _, want := range []string{"mode=list", "branch=list", "row=group:work", `"enter"`, "MouseWheelMsg", "inputsSeen=2"} {
		if !strings.Contains(dump, want) {
			t.Fatalf("dump lacks %q:\n%s", want, dump)
		}
	}
	if got := len(m.inputs.entries()); got != 2 {
		t.Fatalf("the dump request itself was journaled: %d entries", got)
	}
}

func TestDumpRequestNeverBlocksTheLoop(t *testing.T) {
	m := buildModel(t)
	full := make(chan string, 1)
	full <- "earlier"
	done := make(chan struct{})
	go func() {
		m.Update(DumpRequest{Reply: full})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Update blocked on a full reply channel")
	}
}

func TestStuckOpenNeedsThreeRefusalsInsideTheWindow(t *testing.T) {
	var j inputJournal
	start := time.Unix(1000, 0)
	if j.stuckOpen(start) || j.stuckOpen(start.Add(10*time.Second)) {
		t.Fatal("stuck after fewer than three refusals")
	}
	// The first has aged out by now, so this is still two.
	if j.stuckOpen(start.Add(stuckOpenWindow + 5*time.Second)) {
		t.Fatal("a refusal outside the window was counted")
	}
	if !j.stuckOpen(start.Add(stuckOpenWindow + 6*time.Second)) {
		t.Fatal("three refusals inside the window not called stuck")
	}
	if j.stuckOpen(start.Add(stuckOpenWindow + 7*time.Second)) {
		t.Fatal("refusals not forgotten after being reported")
	}
}

func TestRepeatedRefusedOpensAskForADump(t *testing.T) {
	m := buildModel(t)
	var reasons []string
	m.SetAutoDump(func(reason string) { reasons = append(reasons, reason) })
	meta := inputMeta{kind: "key", input: "enter", branch: "list", row: "session:abc(idle)"}
	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	for range stuckOpenPresses {
		m.journalInput(meta, enter, modeList, modeList, time.Now())
	}
	if len(reasons) != 1 || !strings.Contains(reasons[0], "left the board on the list") {
		t.Fatalf("auto dumps = %q", reasons)
	}

	// Enter on a group folds it; that is not a refusal.
	reasons = nil
	group := inputMeta{kind: "key", input: "enter", branch: "list", row: "group:work"}
	for range stuckOpenPresses {
		m.journalInput(group, enter, modeList, modeList, time.Now())
	}
	if len(reasons) != 0 {
		t.Fatalf("group enters asked for a dump: %q", reasons)
	}
}

func TestLoopPingIsAnsweredWithoutJournaling(t *testing.T) {
	m := buildModel(t)
	reply := make(chan struct{}, 1)
	m.Update(LoopPing{Reply: reply})
	select {
	case <-reply:
	default:
		t.Fatal("ping not answered")
	}
	if m.inputs.total != 0 {
		t.Fatal("a ping was journaled as input")
	}
}
