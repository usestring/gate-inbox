package tmux

import "testing"

// The verb is what a count is for: telling a capture apart from a send-keys,
// through the server flags the driver puts in front of every command.
func TestTheVerbIsReadPastTheServerFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"socket then verb", []string{"-L", "default", "capture-pane", "-p", "-e"}, "capture-pane"},
		{"bare switches", []string{"-L", "s", "-2", "list-panes", "-a"}, "list-panes"},
		{"config file carries a value", []string{"-f", "/dev/null", "-L", "s", "kill-server"}, "kill-server"},
		{"a command list is keyed by its first", []string{"-L", "s", "send-keys", "-t", "x", "hi", ";", "capture-pane"}, "send-keys"},
		{"flags only", []string{"-L", "s"}, "(none)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := execVerb(tc.args); got != tc.want {
				t.Fatalf("execVerb(%q) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

// A count nobody can reset is a count that only ever reports the whole
// process's history, which is the wrong denominator for a scenario.
func TestCountsAccumulateAndReset(t *testing.T) {
	ResetExecCounts()
	for i := 0; i < 3; i++ {
		countExec([]string{"-L", "s", "capture-pane", "-p"})
	}
	countExec([]string{"-L", "s", "send-keys", "-t", "x"})

	if got := ExecTotal(); got != 4 {
		t.Fatalf("ExecTotal = %d, want 4", got)
	}
	counts := ExecCounts()
	if counts["capture-pane"] != 3 || counts["send-keys"] != 1 {
		t.Fatalf("ExecCounts = %v, want capture-pane 3 and send-keys 1", counts)
	}
	if verbs := ExecVerbs(); len(verbs) == 0 || verbs[0] != "capture-pane" {
		t.Fatalf("ExecVerbs = %v, want the busiest verb first", verbs)
	}

	ResetExecCounts()
	if got := ExecTotal(); got != 0 {
		t.Fatalf("ExecTotal after reset = %d, want 0", got)
	}
	if got := ExecCounts()["capture-pane"]; got != 0 {
		t.Fatalf("capture-pane after reset = %d, want 0", got)
	}
}

// A call that addresses a server by socket path used to be counted under the
// path, which put one bucket per socket in a report meant to say which tmux
// subcommands this manager runs.
func TestAPathAddressedServerIsCountedByItsSubcommand(t *testing.T) {
	if verb := execVerb([]string{"-S", "/tmp/tmux-1000/default", "has-session", "-t", "x"}); verb != "has-session" {
		t.Fatalf("execVerb keyed a -S call as %q, want has-session", verb)
	}
	// The same flag after the subcommand is capture-pane's own start line,
	// and must not be mistaken for the server flag.
	if verb := execVerb([]string{"-L", "s", "capture-pane", "-p", "-S", "-100"}); verb != "capture-pane" {
		t.Fatalf("execVerb keyed a scrollback capture as %q, want capture-pane", verb)
	}
}
