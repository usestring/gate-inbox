package status

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// withoutPrefilters is the same engine with every derived literal removed:
// the same regexps, run the way they were run before. It is the reference
// every equivalence assertion below compares against, because "the fixtures
// still pass" would also be true of a prefilter that never fired.
func withoutPrefilters(engine *Engine) *Engine {
	bare := &Engine{tools: make(map[string]toolRules, len(engine.tools))}
	for name, tr := range engine.tools {
		copied := tr
		copied.rules = make([]rule, len(tr.rules))
		for i, r := range tr.rules {
			copied.rules[i] = rule{state: r.state, re: ungated(r.re)}
		}
		for _, field := range []**matcher{
			&copied.activityCutoff, &copied.inputLine, &copied.turnEnd,
			&copied.chromeLine, &copied.blockedLine, &copied.trailingNote,
			&copied.arrowDialog, &copied.stepRow, &copied.stepEntry,
			&copied.busyLine, &copied.limitLine, &copied.scrolledLine,
		} {
			*field = ungated(*field)
		}
		bare.tools[name] = copied
	}
	return bare
}

func ungated(m *matcher) *matcher {
	if m == nil {
		return nil
	}
	return &matcher{re: m.re}
}

func fixtureFrames(t testing.TB) map[string]string {
	t.Helper()
	names, err := filepath.Glob(filepath.Join("testdata", "*.txt"))
	if err != nil || len(names) == 0 {
		t.Fatalf("no status fixtures found: %v", err)
	}
	frames := map[string]string{}
	for _, name := range names {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		frames[filepath.Base(name)] = string(raw)
	}
	return frames
}

// The equivalence the whole change rests on: every captured frame, read by
// every tool's rules, answers exactly what it answered before the prefilter
// existed. Every entry point is asked, not only Match, because a caller that
// reads a dialog's arrows or a limit banner off the same patterns would
// otherwise be free to drift.
func TestEveryFixtureClassifiesTheSameWithoutThePrefilter(t *testing.T) {
	gated := defaultEngine(t)
	bare := withoutPrefilters(gated)
	frames := fixtureFrames(t)

	tools := make([]string, 0, len(gated.tools))
	for name := range gated.tools {
		tools = append(tools, name)
	}
	for file, frame := range frames {
		for _, tool := range tools {
			gotState, gotOK := gated.Match(tool, frame)
			wantState, wantOK := bare.Match(tool, frame)
			if gotState != wantState || gotOK != wantOK {
				t.Errorf("%s as %s: Match = %q/%v with the prefilter, %q/%v without it",
					file, tool, gotState, gotOK, wantState, wantOK)
			}
			gotRule, gotRuleOK := gated.RuleMatch(tool, frame)
			wantRule, wantRuleOK := bare.RuleMatch(tool, frame)
			if gotRule != wantRule || gotRuleOK != wantRuleOK {
				t.Errorf("%s as %s: RuleMatch = %q/%v with the prefilter, %q/%v without it",
					file, tool, gotRule, gotRuleOK, wantRule, wantRuleOK)
			}
			if got, want := gated.TypingHold(tool, frame), bare.TypingHold(tool, frame); got != want {
				t.Errorf("%s as %s: TypingHold = %q with the prefilter, %q without it", file, tool, got, want)
			}
			if got, want := gated.LimitBanner(tool, frame), bare.LimitBanner(tool, frame); got != want {
				t.Errorf("%s as %s: LimitBanner = %q with the prefilter, %q without it", file, tool, got, want)
			}
			if got, want := gated.ViewportDisplaced(tool, frame), bare.ViewportDisplaced(tool, frame); got != want {
				t.Errorf("%s as %s: ViewportDisplaced = %v with the prefilter, %v without it", file, tool, got, want)
			}
			if got, want := gated.DialogOwnsArrows(tool, frame), bare.DialogOwnsArrows(tool, frame); got != want {
				t.Errorf("%s as %s: DialogOwnsArrows = %v with the prefilter, %v without it", file, tool, got, want)
			}
			gotRegion, gotReady := gated.ActivityRegion(tool, frame)
			wantRegion, wantReady := bare.ActivityRegion(tool, frame)
			if gotReady != wantReady || gotRegion != wantRegion {
				t.Errorf("%s as %s: ActivityRegion differs with the prefilter (%d bytes/%v) and without it (%d bytes/%v)",
					file, tool, len(gotRegion), gotReady, len(wantRegion), wantReady)
			}
			if got, want := gated.TurnEndedState(tool, wantRegion), bare.TurnEndedState(tool, wantRegion); got != want {
				t.Errorf("%s as %s: TurnEndedState = %q with the prefilter, %q without it", file, tool, got, want)
			}
			for _, row := range strings.Split(frame, "\n") {
				gotPrefix, gotHas := gated.InputPrefix(tool, row)
				wantPrefix, wantHas := bare.InputPrefix(tool, row)
				if gotHas != wantHas || gotPrefix != wantPrefix {
					t.Errorf("%s as %s: InputPrefix(%q) = %q/%v with the prefilter, %q/%v without it",
						file, tool, row, gotPrefix, gotHas, wantPrefix, wantHas)
				}
			}
		}
	}
}

// The prefilter is only sound while every literal it derived is one the
// pattern cannot match without. This checks that against the text the board
// actually reads: every shipped pattern, against every fixture and every line
// of every fixture.
func TestNoShippedPatternMatchesWithoutItsLiteral(t *testing.T) {
	engine := defaultEngine(t)
	var subjects []string
	for _, frame := range fixtureFrames(t) {
		subjects = append(subjects, frame)
		subjects = append(subjects, strings.Split(frame, "\n")...)
	}
	gatedCount, total := 0, 0
	for tool, tr := range engine.tools {
		all := append([]*matcher{}, tr.activityCutoff, tr.inputLine, tr.turnEnd,
			tr.chromeLine, tr.blockedLine, tr.trailingNote, tr.arrowDialog,
			tr.stepRow, tr.stepEntry, tr.busyLine, tr.limitLine, tr.scrolledLine)
		for _, r := range tr.rules {
			all = append(all, r.re)
		}
		for _, m := range all {
			if m == nil {
				continue
			}
			total++
			if m.lit == "" {
				continue
			}
			gatedCount++
			for _, subject := range subjects {
				if m.re.MatchString(subject) && !strings.Contains(subject, m.lit) {
					t.Errorf("%s: pattern %q matches text that does not contain its derived literal %q: the prefilter would silently drop this status\nsubject: %q",
						tool, m.re, m.lit, subject)
				}
			}
		}
	}
	if gatedCount == 0 {
		t.Fatal("no shipped pattern carries a literal: the prefilter is doing nothing")
	}
	t.Logf("%d of %d shipped patterns carry a prefilter literal", gatedCount, total)
}

// The gate has to be live. Given a literal the pattern does not imply -- which
// derivation never produces, and which is exactly the mistake this file is
// written to make impossible -- a matcher answers no without consulting the
// regexp at all. A matcher that answered yes here would be one that is not
// prefiltering anything.
func TestAMatcherConsultsItsLiteralBeforeItsPattern(t *testing.T) {
	gated := &matcher{re: regexp.MustCompile("esc to interrupt"), lit: "not in this pane"}
	if gated.MatchString("esc to interrupt") {
		t.Fatal("the matcher ran its pattern despite a literal that is absent: nothing is being prefiltered")
	}
	if gated.FindStringIndex("esc to interrupt") != nil {
		t.Fatal("FindStringIndex ran its pattern despite an absent literal")
	}
	if gated.FindAllStringIndex("esc to interrupt", -1) != nil {
		t.Fatal("FindAllStringIndex ran its pattern despite an absent literal")
	}
	plain := &matcher{re: regexp.MustCompile("esc to interrupt")}
	if !plain.MatchString("esc to interrupt") {
		t.Fatal("a matcher with no literal stopped matching")
	}
}

// What the derivation is allowed to extract, and -- more to the point -- what
// it must refuse to.
func TestRequiredLiteralOnlyExtractsWhatEveryMatchMustContain(t *testing.T) {
	cases := []struct {
		pattern string
		want    string
		why     string
	}{
		{"esc to interrupt", "esc to interrupt", "a bare literal is the whole pattern"},
		{`(?m)^\s*\d+/\d+:select\b`, ":select", "the literal run between the classes"},
		{`(?m)Press enter to (confirm|continue)\b`, "Press enter to ", "the fixed head of an alternation"},
		{`(?i)requires more credits`, "", "case folded: REQUIRES would match and contains none of it"},
		{`(?im)^\s*error\b`, "", "case folded again, this time behind an anchor"},
		{"(colour|color) scheme", " scheme", "the alternation itself yields nothing"},
		{"(never)?always", "always", "an optional group is not required"},
		{"a*bcd", "bcd", "a starred atom is not required"},
		{"(abc)+de", "abc", "a plussed group is required, and the longer run wins"},
		{"[0-9]+", "", "a character class implies no byte in particular"},
		{`\x{25B3} Permission required`, "△ Permission required", "an escaped rune is still a literal"},
		{"(?s).*", "", "anything is not a literal"},
		{"(", "", "an unparseable pattern gates nothing"},
	}
	for _, tc := range cases {
		if got := requiredLiteral(tc.pattern); got != tc.want {
			t.Errorf("requiredLiteral(%q) = %q, want %q (%s)", tc.pattern, got, tc.want, tc.why)
		}
	}
}

// The implication the prefilter depends on, checked against text nobody
// wrote down: if a shipped pattern matches, the subject contains the literal
// derived from it. The seeds are the fixtures a line at a time, and the
// fuzzer mutates from there -- which is the only way to exercise the frames
// the board has not captured yet.
func FuzzNoShippedPatternMatchesWithoutItsLiteral(f *testing.F) {
	engine := defaultEngine(f)
	var gated []*matcher
	for _, tr := range engine.tools {
		for _, m := range append([]*matcher{tr.activityCutoff, tr.inputLine, tr.turnEnd,
			tr.chromeLine, tr.blockedLine, tr.trailingNote, tr.arrowDialog,
			tr.stepRow, tr.stepEntry, tr.busyLine, tr.limitLine, tr.scrolledLine},
			rulePatterns(tr)...) {
			if m != nil && m.lit != "" {
				gated = append(gated, m)
			}
		}
	}
	for _, frame := range fixtureFrames(f) {
		f.Add(frame)
		for _, line := range strings.Split(frame, "\n") {
			f.Add(line)
		}
	}
	f.Fuzz(func(t *testing.T, subject string) {
		for _, m := range gated {
			if m.re.MatchString(subject) && !strings.Contains(subject, m.lit) {
				t.Fatalf("pattern %q matched a subject that does not contain its literal %q: the prefilter hides this match\nsubject: %q",
					m.re, m.lit, subject)
			}
		}
	})
}

func rulePatterns(tr toolRules) []*matcher {
	out := make([]*matcher, 0, len(tr.rules))
	for _, r := range tr.rules {
		out = append(out, r.re)
	}
	return out
}

// The before and after. "gated" is what the board runs now; "ungated" is the
// same patterns without the literal check, which is what it ran before.
func BenchmarkMatchAPane(b *testing.B) {
	gated := defaultEngine(b)
	bare := withoutPrefilters(gated)
	frames := fixtureFrames(b)
	for _, file := range []string{"opencode-v2-working.txt", "claude-askuserquestion-single.txt", "codex-0153-settled-turn.txt"} {
		frame, ok := frames[file]
		if !ok {
			b.Fatalf("fixture %s is gone", file)
		}
		tool := strings.SplitN(file, "-", 2)[0]
		b.Run(file+"/gated", func(b *testing.B) {
			b.SetBytes(int64(len(frame)))
			for i := 0; i < b.N; i++ {
				gated.Match(tool, frame)
			}
		})
		b.Run(file+"/ungated", func(b *testing.B) {
			b.SetBytes(int64(len(frame)))
			for i := 0; i < b.N; i++ {
				bare.Match(tool, frame)
			}
		})
	}
}

// A whole pass's worth of the board: every tool's rules over every fixture,
// which is the shape of the ~10 scans per session per pass the change is
// aimed at.
func BenchmarkMatchEveryToolOverEveryFixture(b *testing.B) {
	gated := defaultEngine(b)
	bare := withoutPrefilters(gated)
	frames := fixtureFrames(b)
	run := func(b *testing.B, engine *Engine) {
		for i := 0; i < b.N; i++ {
			for _, frame := range frames {
				for tool := range engine.tools {
					engine.Match(tool, frame)
				}
			}
		}
	}
	b.Run("gated", func(b *testing.B) { run(b, gated) })
	b.Run("ungated", func(b *testing.B) { run(b, bare) })
}
