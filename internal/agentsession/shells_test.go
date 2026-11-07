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
