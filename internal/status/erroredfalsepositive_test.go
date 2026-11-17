package status

import (
	"testing"
)

const claudePrompt = "\n─────────────────────────────────────────\n❯ \n─────────────────────────────────────────\n  ~/project\n"

// Tool output Claude prints under a call is indented, so a line in it that
// starts "error:" is the command's output, not the agent failing. Reading it
// as errored put every turn that ran a failing git push or compiler on the
// errored tier.
func TestClaudeToolOutputErrorIsNotErrored(t *testing.T) {
	engine := defaultEngine(t)
	cases := []struct{ name, pane, want string }{
		{"git push rejected in tool output",
			"⏺ Bash(git push)\n  ⎿  To github.com:org/repo.git\n      ! [rejected]        main -> main (fetch first)\n     error: failed to push some refs\n\n⏺ The push was rejected; I'll rebase first.\n\n✻ Worked for 12s · done 10:20 AM\n" + claudePrompt,
			Finished},
		{"compiler error quoted in a reply",
			"⏺ The build failed:\n\n  error: cannot find module 'x'\n\n  I fixed the import.\n\n✻ Worked for 3s · done 10:21 AM\n" + claudePrompt,
			Finished},
		{"git push rejected in a turn with no summary line",
			"❯ push it\n\n⏺ Bash(git push)\n  ⎿  To github.com:org/repo.git\n      ! [rejected]        main -> main (fetch first)\n     error: failed to push some refs\n\n⏺ The push was rejected; I'll rebase first.\n" + claudePrompt,
			// No rule matches, so the pane falls back to its default and the
			// Stop hook decides; the point is that it is not errored.
			Idle},
		{"fatal CLI error at column zero",
			"Error: Invalid API key\n" + claudePrompt,
			Errored},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := engine.Match("claude", tc.pane); got != tc.want {
				t.Fatalf("Match() = %q, want %q", got, tc.want)
			}
		})
	}
}

// Esc in Codex draws "■ Conversation interrupted". That is the operator
// stopping a turn, which leaves the prompt waiting on them, not a failure.
func TestCodexInterruptIsNotErrored(t *testing.T) {
	engine := defaultEngine(t)
	pane := "› Fix the tests.\n\n• Working (3s • esc to interrupt)\n\n■ Conversation interrupted - tell the model what to do differently. Something went wrong? Hit `/feedback` to report the issue.\n\n› Ask Codex to do anything\n  gpt-5 · ~/project"
	if got, _ := engine.Match("codex", pane); got == Errored {
		t.Fatalf("Match() = %q, want a resting state", got)
	}
	if banner := engine.LimitBanner("codex", pane); banner != "" {
		t.Fatalf("LimitBanner() = %q, want none", banner)
	}
}
