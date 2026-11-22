package agentsession

import "testing"

// Only the outermost of a sandboxed chain counts, and nothing outside the
// pane's tree does.
func TestClaudeShellsUnderCountsEachShellOnce(t *testing.T) {
	table := func() ([]byte, error) {
		return []byte("" +
			"  10     1 claude\n" +
			"  11    10 gate-inbox mcp\n" +
			"  12    10 bwrap --new-session -- /usr/bin/zsh -c source /opt/agent/shell-snapshots/snapshot-zsh-1.sh && eval 'sleep 600'\n" +
			"  13    12 /usr/bin/zsh -c source /opt/agent/shell-snapshots/snapshot-zsh-1.sh && eval 'sleep 600'\n" +
			"  14    10 /usr/bin/zsh -c source /opt/agent/shell-snapshots/snapshot-zsh-1.sh && eval 'judge run'\n" +
			"  20     1 /usr/bin/zsh -c source /opt/agent/shell-snapshots/snapshot-zsh-1.sh && eval 'elsewhere'\n"), nil
	}
	if got := ClaudeShellsUnder(10, table); got != 2 {
		t.Fatalf("ClaudeShellsUnder = %d, want 2", got)
	}
	if got := ClaudeShellsUnder(11, table); got != 0 {
		t.Fatalf("ClaudeShellsUnder(mcp) = %d, want 0", got)
	}
}

// Only direct children count: a shell at its prompt has none, and one in a
// fan-out loop's sleep has one.
func TestChildrenOfCountsDirectChildren(t *testing.T) {
	table := func() ([]byte, error) {
		return []byte("" +
			"  40     1 /opt/homebrew/bin/bash -l\n" +
			"  41    40 sleep 1\n" +
			"  42    41 grandchild\n" +
			"  50     1 /opt/homebrew/bin/bash -l\n"), nil
	}
	if got := ChildrenOf(40, table); got != 1 {
		t.Fatalf("ChildrenOf(busy) = %d, want 1", got)
	}
	if got := ChildrenOf(50, table); got != 0 {
		t.Fatalf("ChildrenOf(idle) = %d, want 0", got)
	}
}
