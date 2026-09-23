package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/launch"
)

// railBlocks splits a rail into the runs its rules separate, stripped of
// styling: the list, then the opening block when there is one, then the
// dock.
func railBlocks(m *Model, width int) [][]string {
	var blocks [][]string
	current := []string{}
	for _, row := range m.railLines(width, m.listBodyHeight()) {
		if row.rule {
			blocks = append(blocks, current)
			current = []string{}
			continue
		}
		current = append(current, ansi.Strip(row.text))
	}
	return append(blocks, current)
}

// promptBlock is the prompt's own section within the opening block: the
// opening now leads with the selected row's own detail facts, so the prompt
// (when there is one) starts wherever its "prompt" label lands rather than
// at the top.
func promptBlock(t *testing.T, m *Model, width int) []string {
	t.Helper()
	blocks := railBlocks(m, width)
	if len(blocks) != 3 {
		t.Fatalf("rail has %d blocks, want list, opening and dock:\n%s", len(blocks), railLinesText(m.railLines(width, m.listBodyHeight())))
	}
	if !strings.Contains(blocks[2][0], "computer") {
		t.Fatalf("the dock is not the last block: %q", blocks[2])
	}
	opening := blocks[1]
	for i, line := range opening {
		if strings.TrimSpace(line) == "prompt" {
			return opening[i:]
		}
	}
	t.Fatalf("opening block has no prompt section: %q", opening)
	return nil
}

// The selected session's opening sits between the list and the machine
// dock, and the dock is pinned at the foot under it.
func TestTheRailShowsTheSelectedSessionsOpening(t *testing.T) {
	m := fleetModel(t, 30, 200, 60)
	sess, ok := m.selected()
	if !ok {
		t.Fatal("no session selected")
	}
	m.firstPrompts[sess.ID] = []string{"Add a token bucket limiter to the public API"}
	block := promptBlock(t, m, 59)
	if strings.TrimSpace(block[0]) != "prompt" {
		t.Fatalf("the block does not open with its label: %q", block)
	}
	if !strings.Contains(block[1], "❯ Add a token bucket limiter to the public API") {
		t.Fatalf("the opening is not on the rail: %q", block)
	}
	rows := m.railLines(59, m.listBodyHeight())
	if last := ansi.Strip(rows[len(rows)-2].text); !strings.Contains(last, "net") {
		t.Fatalf("the dock is not at the foot; last meter row reads %q", last)
	}
}

// Short prompts share the block, so a session opened in three lines shows
// all three; one long prompt takes the rows and is cut where they end.
func TestShortOpeningsShareTheBlockAndALongOneIsCut(t *testing.T) {
	m := fleetModel(t, 30, 200, 60)
	sess, _ := m.selected()
	m.firstPrompts[sess.ID] = []string{"fix the build", "then run it", "and open a PR"}
	block := promptBlock(t, m, 59)
	if len(block) != 4 || !strings.Contains(block[3], "❯ and open a PR") {
		t.Fatalf("three short prompts do not all show: %q", block)
	}

	long := strings.Repeat("investigate the flaky login test and ", 12)
	m.firstPrompts[sess.ID] = []string{long, "then run it"}
	block = promptBlock(t, m, 59)
	if len(block) != 1+promptBlockRows {
		t.Fatalf("a long prompt takes %d rows, want %d: %q", len(block)-1, promptBlockRows, block)
	}
	if !strings.HasSuffix(strings.TrimSpace(block[len(block)-1]), "…") {
		t.Fatalf("a cut prompt does not say so: %q", block[len(block)-1])
	}
	for _, line := range block {
		if strings.Contains(line, "then run it") {
			t.Fatalf("a prompt that does not fit was squeezed in: %q", block)
		}
		if got := ansi.StringWidth(line); got > 59 {
			t.Fatalf("block line is %d wide on a 59-column rail: %q", got, line)
		}
	}
}

// Until the sweep has read the transcript, the prompt the session launched
// with stands in, without the rename directive the launch put ahead of it.
func TestTheLaunchPromptStandsInUntilTheTranscriptIsRead(t *testing.T) {
	m := fleetModel(t, 30, 200, 60)
	sess, _ := m.selected()
	delete(m.firstPrompts, sess.ID)
	for i := range m.rows {
		if !m.rows[i].isGroup && m.rows[i].sess.ID == sess.ID {
			m.rows[i].sess.LaunchPrompt = launch.Prompt("", "ship the thing", true, false)
		}
	}
	block := promptBlock(t, m, 59)
	if len(block) != 2 || !strings.Contains(block[1], "❯ ship the thing") {
		t.Fatalf("the launch prompt does not stand in: %q", block)
	}
	if strings.Contains(strings.Join(block, "\n"), "rename") {
		t.Fatalf("the directive leaked onto the rail: %q", block)
	}
}

// A group row opens with its own facts in place of a session's, carrying no
// prompt section since a group is never launched; a rail too short for the
// full dock has no room for an opening block at all, group or session.
func TestTheOpeningBlockYieldsToGroupsAndShortRails(t *testing.T) {
	m := fleetModel(t, 30, 200, 60)
	for i, row := range m.rows {
		if row.isGroup {
			m.cursor = i
			break
		}
	}
	blocks := railBlocks(m, 59)
	if len(blocks) != 3 {
		t.Fatalf("a group row grew no opening block of its own: %d blocks", len(blocks))
	}
	if strings.TrimSpace(blocks[1][0]) != "group" {
		t.Fatalf("a group row's opening should lead with its own facts: %q", blocks[1])
	}
	if strings.Contains(strings.Join(blocks[1], "\n"), "prompt") {
		t.Fatalf("a group row's opening carries a prompt section: %q", blocks[1])
	}

	short := fleetModel(t, 30, 45, 20)
	if blocks := railBlocks(short, 44); len(blocks) != 2 {
		t.Fatalf("a short rail grew an opening block: %d blocks", len(blocks))
	}
}

// The sweep's answer lands on the model, and a shorter answer never
// replaces a longer one.
func TestApplyFirstPromptsKeepsTheLongerOpening(t *testing.T) {
	m := fleetModel(t, 3, 120, 40)
	m.applyFirstPrompts(map[string][]string{"sess-0": {"one", "two"}})
	m.applyFirstPrompts(map[string][]string{"sess-0": {"one"}, "sess-1": {"solo"}})
	if got := m.firstPrompts["sess-0"]; len(got) != 2 {
		t.Fatalf("sess-0 opening = %q, want the two-prompt answer kept", got)
	}
	if got := m.firstPrompts["sess-1"]; len(got) != 1 || got[0] != "solo" {
		t.Fatalf("sess-1 opening = %q", got)
	}
}

// The notes a launch puts ahead of a prompt come off the transcript's copy
// too, and a record that was nothing but a manager note is dropped.
func TestTypedPromptsStripTheLaunchNotes(t *testing.T) {
	got := typedPrompts([]string{
		launch.Prompt(launch.CoordinationNote, "do the task", true, false),
		launch.DeferredRenameDirective,
		"a plain one",
	})
	if len(got) != 2 || got[0] != "do the task" || got[1] != "a plain one" {
		t.Fatalf("typedPrompts = %q", got)
	}
}
