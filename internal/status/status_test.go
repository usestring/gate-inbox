// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package status

import (
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
)

func testEngine(t *testing.T) *Engine {
	t.Helper()
	cfg := config.Config{
		Tools: map[string]config.Tool{
			"claude": {
				Command:       "claude",
				DefaultStatus: "idle",
				Rules: []config.Rule{
					{State: "working", Pattern: "esc to interrupt"},
					{State: "waiting", Pattern: `(?m)^ ❯ 1\.`},
					{State: "errored", Pattern: "(?i)^error:"},
				},
			},
		},
	}
	engine, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return engine
}

func TestMatch(t *testing.T) {
	engine := testEngine(t)
	cases := []struct {
		name string
		tool string
		pane string
		want string
	}{
		{"working spinner", "claude", "thinking... (esc to interrupt)", Working},
		{"persisted working-first rules still prefer waiting", "claude",
			"✶ Cooking… (2m 14s · esc to interrupt)\nDo you want to proceed?\n ❯ 1. Yes\n   2. No, and tell Claude what to do differently", Waiting},
		{"errored", "claude", "Error: something broke", Errored},
		{"idle fallback", "claude", "> ", Idle},
		{"first rule wins", "claude", "Error: x\nesc to interrupt", Working},
		{"unknown tool", "ghost", "anything", Idle},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := engine.Match(tc.tool, tc.pane); got != tc.want {
				t.Fatalf("Match(%q)=%q want %q", tc.pane, got, tc.want)
			}
		})
	}
}

func TestMatchWaitingOnlyOverridesWorkingFirstMatch(t *testing.T) {
	cfg := config.Config{Tools: map[string]config.Tool{
		"custom": {Rules: []config.Rule{
			{State: "errored", Pattern: "error signal"},
			{State: "working", Pattern: "working signal"},
			{State: "waiting", Pattern: "waiting signal"},
		}},
	}}
	engine, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	if got, _ := engine.Match("custom", "error signal\nworking signal\nwaiting signal"); got != Errored {
		t.Fatalf("Match() = %q want %q", got, Errored)
	}

	cfg.Tools["custom"] = config.Tool{Rules: []config.Rule{
		{State: "working", Pattern: "working signal"},
		{State: "errored", Pattern: "error signal"},
		{State: "waiting", Pattern: "waiting signal"},
	}}
	engine, err = NewEngine(cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if got, _ := engine.Match("custom", "working signal\nerror signal\nwaiting signal"); got != Waiting {
		t.Fatalf("Match() = %q want %q", got, Waiting)
	}
}

func TestMatchLegacyClaudeRuleOrderStillResolvesWaiting(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	claude := cfg.Tools["claude"]
	claude.Rules = []config.Rule{
		{State: Working, Pattern: `(?m)^[✻✳✶✽✢·✦✧+*] \S+… \(`},
		{State: Working, Pattern: "esc to interrupt"},
		{State: Waiting, Pattern: "Enter to confirm"},
		{State: Waiting, Pattern: `(?m)^[ \x{A0}]*❯[ \x{A0}]+\d+\.`},
		{State: Errored, Pattern: `(?im)^\s*error:`},
	}
	cfg.Tools["claude"] = claude
	engine, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	// Include a prior turn and the input cutoff so the real Claude scope
	// settings narrow matching to the current mixed-signal turn.
	pane := "⏺ Previous turn\n✻ Worked for 1s\n" +
		"✶ Cooking… (2m 14s · esc to interrupt)\nDo you want to proceed?\n" +
		" ❯ 1. Yes\n   2. No, and tell Claude what to do differently\n❯ "
	if got, matched := engine.Match("claude", pane); got != Waiting || !matched {
		t.Fatalf("Match() = (%q, %t) want (%q, true)", got, matched, Waiting)
	}
}

// Reconstructs the mixed signals reported in issue #112: an unanswered
// approval dialog while Claude's active-turn hint remains visible.
func TestClaudeMixedApprovalPane(t *testing.T) {
	engine := defaultEngine(t)
	pane := "✶ Cooking… (2m 14s · esc to interrupt)\nDo you want to proceed?\n" +
		" ❯ 1. Yes\n   2. Yes, and don't ask again\n" +
		"   3. No, and tell Claude what to do differently"
	if got, matched := engine.Match("claude", pane); got != Waiting || !matched {
		t.Fatalf("Match() = (%q, %t) want (%q, true)", got, matched, Waiting)
	}
}

func TestClaudeEnterToConfirmOverridesWorking(t *testing.T) {
	engine := defaultEngine(t)
	pane := "✶ Cooking… (2m 14s · esc to interrupt)\n" +
		"Review the selected choice\nEnter to confirm · Esc to cancel"
	if got, matched := engine.Match("claude", pane); got != Waiting || !matched {
		t.Fatalf("Match() = (%q, %t) want (%q, true)", got, matched, Waiting)
	}
}

func TestClaudeNumberedInputDoesNotLookLikeDialog(t *testing.T) {
	engine := defaultEngine(t)
	pane := "✳ Drizzling… (6s · esc to interrupt)\n❯ 1. refactor the parser"
	if got, matched := engine.Match("claude", pane); got != Working || !matched {
		t.Fatalf("Match() = (%q, %t) want (%q, true)", got, matched, Working)
	}
}

// Fixtures below are captured from real claude/opencode panes (2026-07-16).
func defaultEngine(t testing.TB) *Engine {
	t.Helper()
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("built-in config: %v", err)
	}
	engine, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("engine from built-in config: %v", err)
	}
	return engine
}

func TestDefaultRulesRealPanes(t *testing.T) {
	engine := defaultEngine(t)
	cases := []struct {
		name string
		tool string
		pane string
		want string
	}{
		{"claude active turn", "claude",
			"✳ Drizzling… (6s · thinking with medium effort)\n❯ ", Working},
		{"claude long turn", "claude",
			"✶ Cooking… (2m14s · esc to interrupt)\n❯ ", Working},
		{"claude done at prompt", "claude",
			"✻ Cogitated for 13s\n────\n❯ \n────\n  ⏵⏵ bypass permissions on", Finished},
		{"claude done, blank line before separator (real capture)", "claude",
			"✻ Cooked for 10s\n\n────\n❯ \n────\n  ▎ ○ Haiku 4.5", Finished},
		{"claude prompt with nbsp (real capture)", "claude",
			"✻ Cooked for 13s\n────\n❯ \n────\n  ⏵⏵ bypass permissions on", Finished},
		{"claude trust dialog", "claude",
			" ❯ 1. Yes, I trust this folder\n   2. No, exit\n Enter to confirm · Esc to cancel", Waiting},
		{"claude permission ask", "claude",
			"Do you want to proceed?\n ❯ 1. Yes\n   2. No, and tell Claude what to do differently", Waiting},
		{"claude done with ghost suggestion in prompt", "claude",
			"✻ Cogitated for 13s\n────\n❯ count from 1 to 300", Finished},
		{"claude plain-text question (real capture)", "claude",
			"⏺ What color now, what color want?\n✻ Crunched for 9s\n────\n❯ \n────\n  ▎ ✧ /plan  enter plan mode", Waiting},
		{"claude old question, newer statement turn", "claude",
			"⏺ What color now?\n✻ Crunched for 9s\n  DONE\n✻ Worked for 10s\n────\n❯ \n────", Finished},
		{"claude interrupted turn (real capture)", "claude",
			"  221\n⎿  Interrupted · What should Claude do instead?\n────\n❯ \n────\n  ⏵⏵ bypass permissions on", Waiting},
		// 2026-07-26 real capture: background agents outlive the turn that
		// spawned them, and the wait line has the same shape as a turn-end
		// summary ("glyph word for digit"), so it must not read as finished.
		{"claude waiting on background agents (real capture)", "claude",
			"⏺ Security agent done. 2 left (logic, backend/API).\n✻ Waiting for 2 background agents to finish\n────\n❯ \n────\n  ⏵⏵ bypass permissions on", Working},
		{"claude waiting on background agents with hidden-message note (real capture)", "claude",
			"✻ Waiting for 1 background agent to finish · 13 messages hidden (/focus to show)\n────\n❯ \n────\n  ⏵⏵ bypass permissions on", Working},
		{"claude background wait after a completed turn (real capture)", "claude",
			"⏺ done\n✻ Worked for 8m 12s\n  Ran 5 agents\n✻ Waiting for 5 background agents to finish\n────\n❯ \n────", Working},
		{"claude background wait under a recap block", "claude",
			"✻ Waiting for 2 background agents to finish\n※ recap: goal was X; next is Y.\n────\n❯ \n────", Working},
		{"claude background wait superseded by a newer turn", "claude",
			"✻ Waiting for 2 background agents to finish\n⏺ all agents reported\n✻ Worked for 5s\n────\n❯ \n────", Finished},
		// 2026-08-14 and 2026-09-07 real captures: a turn that leaves shells,
		// monitors, MCP tasks or background tasks running names them in a tail
		// on its own summary line. None of that is the agent working -- a
		// monitor can sit armed for hours while the prompt takes input -- so
		// the turn reads as ended. Background agents never reach this tail:
		// while any are pending Claude draws the wait line instead.
		{"claude turn end with one background shell (real capture)", "claude",
			"⏺ ok\n✻ Worked for 3s · 1 shell still running\n────\n❯ \n────\n  ⏵⏵ bypass permissions on · 1 shell", Finished},
		{"claude turn end with two background shells (real capture)", "claude",
			"  Ran 2 shell commands\n⏺ ok\n✻ Cooked for 4s · 2 shells still running\n────\n❯ \n────\n  ⏵⏵ bypass permissions on · 2 shells", Finished},
		{"claude turn end with an MCP task running (real capture)", "claude",
			"⏺ ok\n✻ Crunched for 1h 11m 30s · done 5:37 AM · 1 MCP task still running\n────\n❯ \n────", Finished},
		{"claude turn end with background tasks running (real capture)", "claude",
			"⏺ ok\n✻ Worked for 4m 13s · done 5:44 AM · 2 background tasks still running\n────\n❯ \n────", Finished},
		{"claude turn end with shells and monitors running (real capture)", "claude",
			"⏺ ok\n✻ Worked for 0s · done 4:28 AM · 8 shells, 2 monitors still running\n────\n❯ \n────", Finished},
		// 2026-09-08 board capture: the session sat at its prompt with a
		// drafted message while the board read working for two monitors.
		{"claude turn end with monitors running and a drafted prompt (real capture)", "claude",
			"⏺ done\n✻ Cogitated for 2m 44s · done 8:14 PM · 2 monitors still running\n────\n❯ run another 100 with the unreachable fallback\n────\n  ⏵⏵ auto mode on · 2 monitors", Finished},
		{"claude wait on agents and a dynamic workflow", "claude",
			"⏺ ok\n✻ Waiting for 1 background agent and 1 dynamic workflow to finish\n────\n❯ \n────", Working},
		// A bullet opens with "·", itself one of the summary glyphs, and any
		// session discussing this signal writes such a line. It leaves the
		// newest turn unsettled rather than busy, which is the default, not a
		// claim either way.
		{"claude prose bullet quoting a still-running tail", "claude",
			"⏺ ok\n✻ Worked for 3s\n· 2 MCP tasks still running\n────\n❯ \n────", Idle},
		// 2026-08-14 real capture: transient banners render under the busy
		// line and say nothing about whether the work drained.
		{"claude background wait under a plugin banner (real capture)", "claude",
			"⏺ ok\n✻ Waiting for 1 background agent to finish · 1 message hidden (/focus to show)\n  Plugins updated: 7 plugins · Run /reload-plugins to apply\n────\n❯ \n────", Working},
		// 2026-08-15 real capture: a weekly/session limit lands above the
		// turn-end summary, so matchScope (text after that summary) never
		// sees it and the quiet turn would otherwise read as finished.
		{"claude weekly usage limit (real capture)", "claude",
			"  ⎿  You've hit your weekly limit · resets 1am (Asia/Jerusalem)\n" +
				"     /usage-credits to finish what you’re working on.\n\n" +
				"✻ Churned for 2h 0m 54s\n────\n❯ \n────", Errored},
		{"claude session limit (real capture)", "claude",
			"You've hit your session limit · resets 9pm (Asia/Jerusalem)\n" +
				"✻ Crunched for 9s\n────\n❯ \n────", Errored},
		{"claude old limit, newer finished turn", "claude",
			"  ⎿  You've hit your weekly limit · resets 1am (Asia/Jerusalem)\n" +
				"✻ Churned for 2h 0m 54s\n  All done now.\n✻ Worked for 5s\n────\n❯ \n────", Finished},
		{"claude old limit, later turn-end with no other content", "claude",
			"  ⎿  You've hit your weekly limit · resets 1am (Asia/Jerusalem)\n" +
				"✻ Churned for 2h 0m 54s\n✻ Worked for 5s\n────\n❯ \n────", Finished},
		{"claude streaming without spinner (real capture)", "claude",
			"  183\n  184\n────\n❯ \n────\n  ▎ ● Fable 5 ✦ medium", Idle},
		{"claude fresh start, typed unsubmitted", "claude",
			"Try \"fix the build\"\n❯ count from 1 to 300", Idle},
		{"opencode fresh prompt, nothing ran yet", "opencode",
			"  ┃  Ask anything... \"What is the tech stack of this project?\"\n  tab agents  ctrl+p commands", Idle},
		{"opencode old question, newer statement turn", "opencode",
			"     What color?\n     Build · DeepSeek V4 Pro (New) · 9.7s · 66.7 tok/s\n     DONE\n     Build · DeepSeek V4 Pro (New) · 4.2s · 66.7 tok/s\n  ┃\n  ╹▀▀▀▀", Finished},
		{"opencode question with trailing pad", "opencode",
			"     Which fruit do you want to know more about?   \n     Build · DeepSeek V4 Pro (New) · 10.4s · 66.7 tok/s   \n     \n  ┃     \n  ┃  Build auto · DeepSeek V4 Pro (New) OpenCode Go   \n  ╹▀▀▀▀", Waiting},
		{"opencode out of credits", "opencode",
			"  ┃  This request requires more credits, or fewer max_tokens.", Errored},
		{"opencode usage limit reached", "opencode",
			"  ┃  Usage limit reached. It will reset in 4 hours.\n  ╹▀▀▀▀", Errored},
		// opencode 2.0.3 ends its finished-turn row with a throughput segment;
		// mid-turn it draws only the footer spinner. Its composer model row
		// must not read as a turn end.
		{"opencode v2 running", "opencode",
			"  ┃  write a haiku\n  ┃\n  ┃  Build auto · DeepSeek V4 Pro (New) OpenCode Go\n  ╹▀▀▀▀\n   ⬝⬝⬝⬝⬝⬝⬝⬝ esc interrupt        ctrl+p commands", Working},
		{"opencode v2 finished with duration and tok/s", "opencode",
			"     HELLO\n     Build · DeepSeek V4 Pro (New) · 4.8s · 66.7 tok/s\n  ┃\n  ┃  Build auto · DeepSeek V4 Pro (New) OpenCode Go\n  ╹▀▀▀▀", Finished},
		{"opencode v2 question", "opencode",
			"     Which file?\n     Build · DeepSeek V4 Pro (New) · 4.8s · 66.7 tok/s\n  ┃\n  ┃  Build auto · DeepSeek V4 Pro (New) OpenCode Go\n  ╹▀▀▀▀", Waiting},
		{"opencode v2 fresh prompt, composer row is not a turn end", "opencode",
			"  ┃  Ask anything...\n  ┃\n  ┃  Build auto · DeepSeek V4 Pro (New) OpenCode Go\n  ╹▀▀▀▀", Idle},
		{"opencode v2 wide pane, sidebar shares the finished row", "opencode",
			"     HELLO                                          13,293 tokens\n" +
				"     Build · DeepSeek V4 Pro (New) · 1.7s · 102.6 tok/s                    $0.01 spent\n" +
				"\n                                                    MCP\n" +
				"                                                    • example-web-access         Connected\n" +
				"  ┃\n  ┃  Build auto · DeepSeek V4 Pro (New) OpenCode Go\n  ╹▀▀▀▀", Finished},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := engine.Match(tc.tool, tc.pane); got != tc.want {
				t.Fatalf("Match(%s) = %q want %q", tc.name, got, tc.want)
			}
		})
	}
}

// Fixtures below mirror real Codex TUI frames, drawn from Codex's own render
// snapshot tests (openai/codex, codex-rs/tui) and a live-captured session
// (2026-07-18). Working/finished frames come from the snapshots; idle, the
// first-run trust dialog, and the usage-limit error were captured live.
func TestCodexRealPanes(t *testing.T) {
	engine := defaultEngine(t)
	cases := []struct {
		name string
		tool string
		pane string
		want string
	}{
		{"codex idle at prompt", "codex",
			"› Ask Codex to do anything\n  gpt-5.6-terra medium · /home/dev", Idle},
		{"codex active turn", "codex",
			"• Working (0s • esc to interrupt)\n\n› Ask Codex to do anything\n  gpt-5.6-terra medium · /home/dev", Working},
		{"codex active turn, other status verb", "codex",
			"• Analyzing (12s • esc to interrupt)\n\n› Ask Codex to do anything\n  gpt-5.6-terra medium · /home/dev", Working},
		{"codex active turn with animations disabled", "codex",
			"Working (12s • esc to interrupt)\n\n› Ask Codex to do anything\n  gpt-5.6-terra medium · /home/dev", Working},
		{"codex reconnecting turn with details", "codex",
			"• Reconnecting... 3/5 (1m 04s • esc to interrupt)\n" +
				"  └ Stream disconnected before completion\n\n" +
				"› Ask Codex to do anything\n  gpt-5.6-terra medium · /home/dev", Working},
		{"codex numbered draft is not a dialog", "codex",
			"• Working (0s • esc to interrupt)\n\n› 1. keep this as ordinary input\n  gpt-5.6-terra medium · /home/dev", Working},
		{"codex option-shaped draft without footer is not a dialog", "codex",
			"• Working (0s • esc to interrupt)\n\n› 1. Yes, proceed (y)", Working},
		{"codex finished work turn", "codex",
			"• Ran echo preparing\n  └ preparing\n\n────────────────────────────────\n\n• Final response.\n\n─ Worked for 2m 05s ─────────────\n\n› Ask Codex to do anything\n  gpt-5.6-terra medium · /home/dev", Finished},
		{"codex finished turn ending on a question", "codex",
			"• Which file should I edit, A or B?\n\n─ Worked for 3s ─────────────────\n\n› Ask Codex to do anything\n  gpt-5.6-terra medium · /home/dev", Waiting},
		{"codex command-approval modal", "codex",
			"  $ echo hello world\n\n› 1. Yes, proceed (y)\n  2. Yes, and don't ask again for commands that start with `echo hello world` (p)\n  3. No, and tell Codex what to do differently (esc)\n\n  Press enter to confirm or esc to cancel", Waiting},
		{"codex command-approval modal overrides stale working signal", "codex",
			"• Working (0s • esc to interrupt)\n\n  $ echo hello world\n\n› 1. Yes, proceed (y)\n  2. No, and tell Codex what to do differently (esc)\n\n  Press enter to confirm or esc to cancel", Waiting},
		{"codex command-approval modal after completed turn", "codex",
			"• Previous response.\n\n─ Worked for 1s ─────────────\n\n• Working (0s • esc to interrupt)\n\n  $ echo hello world\n\n› 1. Yes, proceed (y)\n  2. No, and tell Codex what to do differently (esc)\n\n  Press enter to confirm or esc to cancel", Waiting},
		{"codex first-run trust dialog", "codex",
			"Do you trust the contents of this directory? Working with untrusted contents comes with higher risk of prompt injection.\n\n› 1. Yes, continue\n  2. No, quit\n\n  Press enter to continue", Waiting},
		{"codex request-user-input selection", "codex",
			"  Choose an option.\n\n  › 1. Option 1  First choice.\n    2. Option 2  Second choice.\n\n  tab to add notes | enter to submit answer | esc to interrupt", Waiting},
		{"codex usage limit", "codex",
			"■ You've hit your usage limit. Upgrade to Plus to continue using Codex, or try again at Jul 22nd, 2026 10:42 AM.\n\n› Ask Codex to do anything", Errored},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := engine.Match(tc.tool, tc.pane); got != tc.want {
				t.Fatalf("Match(%s) = %q want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestTurnEndedState(t *testing.T) {
	engine := defaultEngine(t)
	cases := []struct {
		name   string
		region string
		want   string
	}{
		{"plain response", "• Final response.\n\n", Finished},
		{"question response", "• Which file should I edit, A or B?\n\n", Waiting},
		{"question above trailing separator", "• Which file?\n\n────────────\n", Waiting},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := engine.TurnEndedState("codex", tc.region); got != tc.want {
				t.Fatalf("TurnEndedState = %q want %q", got, tc.want)
			}
		})
	}
}

func TestNewEngineBadPattern(t *testing.T) {
	cfg := config.Config{
		Tools: map[string]config.Tool{
			"bad": {Rules: []config.Rule{{State: "working", Pattern: "("}}},
		},
	}
	if _, err := NewEngine(cfg); err == nil {
		t.Fatal("expected error for invalid regex")
	}

	cfg = config.Config{
		Tools: map[string]config.Tool{
			"bad": {LimitLine: "("},
		},
	}
	if _, err := NewEngine(cfg); err == nil {
		t.Fatal("expected error for invalid limit_line regex")
	}
}

func TestLongTurnAndMidLineQuestion(t *testing.T) {
	engine := defaultEngine(t)
	cases := []struct {
		name string
		tool string
		pane string
		want string
	}{
		{"claude long duration with hidden-messages suffix (real capture)", "claude",
			"  Done, runtime-proven.\n✻ Crunched for 8m 48s · 6 messages hidden (/focus to show)\n────\n❯ \n────\n  ⏵⏵ bypass permissions on", Finished},
		{"claude question mid final line (real capture)", "claude",
			"  Approve commit? Then I'll redeploy to staging so you can feel it there.\n✻ Crunched for 8m 48s · 6 messages hidden (/focus to show)\n────\n❯ \n────\n  ⏵⏵ bypass permissions on", Waiting},
		{"claude statement after older mid-line question", "claude",
			"  Approve commit? ok.\n✻ Crunched for 8m 48s\n  Deployed. All done.\n✻ Worked for 12s\n────\n❯ \n────", Finished},
		{"opencode long duration", "opencode",
			"     All finished here.\n     Build · DeepSeek V4 Pro (New) · 1m 22s · 66.7 tok/s\n  ┃\n  ╹▀▀▀▀", Finished},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := engine.Match(tc.tool, tc.pane); got != tc.want {
				t.Fatalf("Match(%s) = %q want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestRealPaneEdgeCases(t *testing.T) {
	engine := defaultEngine(t)
	cases := []struct {
		name string
		tool string
		pane string
		want string
	}{
		{"claude long spinner without esc hint (real capture)", "claude",
			"✽ Zigzagging… (3m 18s · ↓ 1.4k tokens · thought for 1s)\n────\n❯ ", Working},
		{"claude separator carrying hint text (real capture)", "claude",
			"  Approve commit? Then I'll redeploy to staging.\n✻ Crunched for 8m 48s · 6 messages hidden (/focus to show)\n\n──────────────────    /rc · focus\n❯ nice! works! BUT older prompt echo\n\n✻ Crunched for 2m 2s\n\n──────────────────\n❯ ", Finished},
		{"claude question with dec-graphics separator", "claude",
			"  Ship it now?\n✻ Crunched for 2m 2s\nqqqqqqqqqqqqqqqqqq\n❯ ", Waiting},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := engine.Match(tc.tool, tc.pane); got != tc.want {
				t.Fatalf("Match(%s) = %q want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestRecapBelowSummary(t *testing.T) {
	engine := defaultEngine(t)
	pane := "  All set on the twin box.\n" +
		"✻ Crunched for 1m 1s · 3 messages hidden (/focus to show)\n" +
		"※ recap: Setting up laptop-casting: twin box is done and proven, now deploying\n" +
		"  plus ports. (disable recaps in /config)\n" +
		"────\n❯ done, code is 431652\n────\n  ⏵⏵ bypass permissions on"
	if got, _ := engine.Match("claude", pane); got != Finished {
		t.Fatalf("recap below summary should still be finished, got %q", got)
	}
}

func TestQuotedSignalsDoNotTrigger(t *testing.T) {
	engine := defaultEngine(t)
	cases := []struct {
		name string
		tool string
		pane string
		want string
	}{
		{"claude quoting spinner and esc text in a finished turn", "claude",
			"  The rule matches \"esc to interrupt\" in the pane.\n" +
				"  Example spinner: ✳ Drizzling… (6s · thinking)\n" +
				"  Menu sample:\n ❯ 1. Yes, I trust this folder\n Enter to confirm\n" +
				"✻ Crunched for 2m 2s\n────\n❯ \n────\n  ⏵⏵ bypass permissions on", Finished},
		{"claude quoting menu text then real question", "claude",
			"  We match \" ❯ 1.\" for dialogs. Should I apply it?\n" +
				"✻ Crunched for 1m 5s\n────\n❯ \n────", Waiting},
		{"claude real spinner during turn still working", "claude",
			"  old output\n✻ Crunched for 2m 2s\n  streaming new answer\n✳ Drizzling… (6s · thinking)\n────\n❯ ", Working},
		{"codex marker-less turn quoting interrupt hint", "codex",
			"  Output:\n\n" +
				"  tool:       mytool\n" +
				"  result:     working\n" +
				"  pattern:    esc to interrupt\n" +
				"  default:    idle\n\n" +
				"› Summarize recent commits\n" +
				"  gpt-5.6-sol medium · /home/dev", Idle},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := engine.Match(tc.tool, tc.pane); got != tc.want {
				t.Fatalf("Match(%s) = %q want %q", tc.name, got, tc.want)
			}
		})
	}
}

// The inbox gate asks RuleMatch rather than Match so it can tell a dialog
// drawn over the input line from a session resting on a question. Both of
// the fallbacks Match layers on top would pin that gate shut: a question
// left on screen reads as waiting, and a background wait as working, so a
// resting session would never be handed the message queued for it.
func TestRuleMatchLeavesTheFallbacksToMatch(t *testing.T) {
	engine := defaultEngine(t)
	cases := []struct {
		name  string
		tool  string
		pane  string
		match string
		rule  string
	}{
		{"a question left at a resting prompt", "claude",
			"⏺ What color now, what color want?\n✻ Crunched for 9s\n────\n❯ \n────\n  ▎ ✧ /plan  enter plan mode",
			Waiting, ""},
		{"a background wait outliving its turn", "claude",
			"⏺ Security agent done. 2 left (logic, backend/API).\n✻ Waiting for 2 background agents to finish\n────\n❯ \n────\n  ⏵⏵ bypass permissions on",
			Working, ""},
		{"a tool nobody configured", "ghost", "anything", Idle, ""},
		{"an approval dialog, which is what a rule is for", "claude",
			"Do you want to proceed?\n ❯ 1. Yes\n   2. No, and tell Claude what to do differently",
			Waiting, Waiting},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := engine.Match(tc.tool, tc.pane); got != tc.match {
				t.Fatalf("Match = %q, want %q", got, tc.match)
			}
			got, matched := engine.RuleMatch(tc.tool, tc.pane)
			if got != tc.rule || matched != (tc.rule != "") {
				t.Fatalf("RuleMatch = (%q, %t), want (%q, %t)", got, matched, tc.rule, tc.rule != "")
			}
		})
	}
}

// TypingHold is what the poller reads before typing a queued message in,
// so each branch is pinned here where the rules live: no readable input
// region holds, a working or dialog rule holds, and a resting prompt takes
// the text.
func TestTypingHold(t *testing.T) {
	cfg := config.Config{
		Tools: map[string]config.Tool{
			"claude": {
				Command:        "claude",
				DefaultStatus:  "idle",
				ActivityCutoff: `(?m)^> `,
				Rules: []config.Rule{
					{State: "working", Pattern: "esc to interrupt"},
					{State: "waiting", Pattern: `(?m)^ ❯ 1\.`},
					{State: "errored", Pattern: "(?i)^error:"},
				},
			},
		},
	}
	engine, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	cases := []struct {
		name string
		pane string
		want string
	}{
		{"no input line drawn yet", "starting up...", Working},
		{"mid-turn spinner", "thinking... (esc to interrupt)\n> ", Working},
		{"dialog on screen", "Do you want to proceed?\n ❯ 1. Yes\n   2. No\n> ", Waiting},
		{"resting prompt takes the text", "all done here\n> ", ""},
		{"errored is not a hold", "Error: something broke\n> ", ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := engine.TypingHold("claude", testCase.pane); got != testCase.want {
				t.Fatalf("TypingHold(%q) = %q, want %q", testCase.pane, got, testCase.want)
			}
		})
	}
}

// The affordance is the tool's own statement that it has parked its
// viewport above the live bottom. A tool that does not draw one, and a
// tool nobody configured, both report false rather than guessing.
func TestViewportDisplaced(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("default config: %v", err)
	}
	engine, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("status engine: %v", err)
	}
	// Exactly as a capture of a scrolled-back claude carries it.
	scrolled := "    62                                         Jump to bottom (ctrl+End) ↓\n❯ \n"
	if !engine.ViewportDisplaced("claude", scrolled) {
		t.Fatal("claude's jump-back affordance must read as a displaced viewport")
	}
	if engine.ViewportDisplaced("claude", "    62\n❯ \n") {
		t.Fatal("a pane at its live bottom must not read as displaced")
	}
	if engine.ViewportDisplaced("codex", scrolled) {
		t.Fatal("a tool with no affordance configured must not read the claude one")
	}
	if engine.ViewportDisplaced("nosuchtool", scrolled) {
		t.Fatal("an unknown tool must not read as displaced")
	}
}
