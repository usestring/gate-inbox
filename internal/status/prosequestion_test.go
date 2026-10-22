package status

import "testing"

const proseRule = "─────────────────────────────────────────────────────────────────────────────"

// Panes recorded off a live board where every row below read waiting.
// The first three ended their turn in prose that asks something, with no
// dialog on screen, and a reply typed at the prompt is all they want: the
// board must read them finished. The last three hold a dialog and must keep
// reading waiting. Wording is scrubbed; the shapes are exact.
func TestProseQuestionIsFinishedAndDialogsStayWaiting(t *testing.T) {
	engine := defaultEngine(t)
	cases := []struct {
		name, pane, want string
	}{
		{"turn ending on a question, with an unsent draft in the prompt",
			"● Here's what's still open, most urgent first.\n\n" +
				"  1. Phone the buyer and confirm the bank details.\n" +
				"  2. Submit the form once the signed agreement arrives.\n\n" +
				"  Which do you want me to start on? I could draft the two emails, reword the website and commit it, or search the mailbox.\n\n" +
				"✻ Sautéed for 16s · done 0:29\n\n" +
				proseRule + "\n❯ draft the two emails\n" + proseRule + "\n" +
				"  Sale documents | Opus 5.5 | ctx 160k/200k\n  ⏵⏵ auto mode on (shift+tab to cycle)",
			Finished},
		{"turn ending on a numbered decision list with a question mid-line",
			"  Two decisions for you:\n" +
				"  1. Python or Go for the service? I'd pick Python for the reasons above.\n" +
				"  2. Where does the GPU live? If it's your desktop, the cluster deployment is CPU-only, and I'd make the small checkpoint the default.\n\n" +
				"✻ Sautéed for 1m 22s · done Saturday 19 Sept, 9:42\n\n" +
				proseRule + "\n❯ \n" + proseRule + "\n  repo | Opus 5 (1M context) | ctx 72k/200k\n  ⏵⏵ auto mode on (shift+tab to cycle)",
			Finished},
		{"turn ending on a URL with a query string",
			"● The metadata URLs look like https://accounts.example.com/o/saml2?idpid=ID.\n\n" +
				"✻ Cooked for 7m 12s · done 0:33\n\n" +
				proseRule + "\n❯ \n" + proseRule + "\n  ⏵⏵ auto mode on (shift+tab to cycle)",
			Finished},
		{"AskUserQuestion approval dialog",
			"● Merging is blocked by branch policy, so I need approval.\n" +
				proseRule + "\n ☐ Approval \n\n" +
				"│ Approve the admin merge of the pin PR?\n\n" +
				"❯ 1. Admin merge\n     Squash-merge with --admin, then deploy.\n" +
				"  2. Stop before deploying\n     Leave it open.\n" +
				"  3. Type something.\n" + proseRule + "\n  4. Chat about this\n\n" +
				"Enter to select · ↑/↓ to navigate · Esc to cancel",
			Waiting},
		{"AskUserQuestion multi-question stepper",
			"● Found the root cause.\n" +
				proseRule + "\n←  ☐ Merge  ☐ Lock file  ✔ Submit  →\n\n" +
				"│ The fix is ready; its test jobs are still running. Merge it?\n\n" +
				"❯ 1. Merge on green (Recommended)\n     Squash-merge once CI passes.\n" +
				"  2. Review first\n     Leave it open.\n" +
				"  3. Type something.\n" + proseRule + "\n  4. Chat about this\n\n" +
				"Enter to select · Tab/Arrow keys to navigate · Esc to cancel",
			Waiting},
		{"Bash permission prompt",
			"● Bash(grep -n 'relay' dom.mjs | head -20)\n  ⎿  Waiting…\n\n" +
				proseRule + "\n Bash command\n Find relay call sites\n\n" +
				" Do you want to proceed?\n ❯ 1. Yes\n   2. No\n\n Esc to cancel · Tab to amend",
			Waiting},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := engine.Match("claude", tc.pane); got != tc.want {
				t.Fatalf("Match = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTurnEndedStateNeverWaitsOnProse(t *testing.T) {
	engine := defaultEngine(t)
	for _, region := range []string{
		"● Which do you want me to start on?\n",
		"  1. Python or Go for the service? I'd pick Python.\n",
		"● See https://example.com/a?b=c\n",
	} {
		for _, tool := range []string{"claude", "codex", "opencode"} {
			if got := engine.TurnEndedState(tool, region); got != Finished {
				t.Fatalf("TurnEndedState(%s, %q) = %q, want finished", tool, region, got)
			}
		}
	}
}
