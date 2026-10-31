package ui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/jev"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/store"
)

// TestJevFinishEval scores the finish check against the Claude transcripts on
// this machine, because the board's own log only shows what JEV said, never
// whether the user then closed the session. It builds the cases at run time
// and writes them, with every score, outside the repository: transcripts are
// private and stay on the machine that holds them.
//
// It calls the live JEV API, so it runs only when asked:
//
//	JEV_FINISH_EVAL=<output dir> go test ./internal/ui -run TestJevFinishEval -v
//
// JEV_EVAL_CONFIG names the gate-inbox config directory (for the key and for
// which sessions were archived); JEV_EVAL_TURNS, JEV_EVAL_CHARS and
// JEV_EVAL_QUESTIONS (comma-separated: current, previous) narrow the grid.
//
// A case is the transcript as it stood the moment the user replied, labelled
// by what the user did next:
//
//   - done, the agent had finished: the user's last word on a session they
//     archived, or the nuke prompt on a session that then ended.
//   - open, the agent had not: a correction, a bare "continue", or the nuke
//     prompt on a session that kept working.
//   - followup: any other substantial reply. Noisy, since a user often hands a
//     finished agent new work, so it is reported apart from open.
//
// Thresholds cost no calls, so every variant is read at several.

// jevEvalPreviousQuestion is the question the check asked before this eval
// replaced it, kept as the baseline any new wording has to beat.
const jevEvalPreviousQuestion = "Has the agent delivered what the user asked for in this session, with no work left open, " +
	"nothing pending on the user, and no blocker stopping it?"

var jevEvalQuestions = map[string]string{
	"current":  jevFinishedQuestion,
	"previous": jevEvalPreviousQuestion,
}

var (
	jevEvalContinue   = regexp.MustCompile(`^(continue|keep going|proceed|go on|go ahead|carry on|resume|keep at it)\b`)
	jevEvalCorrection = regexp.MustCompile(`^(no[,. !]|nope|wrong|that's not|thats not|you didn't|you did not|you haven't|still |it still|this still|doesn't work|does not work|not working|didn't work)`)
	jevEvalThresholds = []float64{0.5, 0.6, 0.7, 0.8}
	jevEvalCaps       = map[string]int{"archived": 40, "nuke-ended": 60, "nuke-continued": 60, "correction": 30, "continue": 30, "followup": 60}
	jevEvalLabel      = map[string]string{"archived": "done", "nuke-ended": "done", "nuke-continued": "open", "correction": "open", "continue": "open", "followup": "followup"}
)

type jevEvalCase struct {
	Class  string           `json:"class"`
	Source string           `json:"source"`
	Next   string           `json:"next,omitempty"`
	msgs   []search.Message `json:"-"`
}

type jevEvalVariant struct {
	Question string `json:"question"`
	Turns    int    `json:"turns"`
	Chars    int    `json:"chars"`
}

func (v jevEvalVariant) String() string {
	return fmt.Sprintf("%s/t%d/c%d", v.Question, v.Turns, v.Chars)
}

type jevEvalScore struct {
	jevEvalVariant
	Class         string  `json:"class"`
	Source        string  `json:"source"`
	Finished      float64 `json:"finished"`
	MissingChrome float64 `json:"missing_chrome"`
	OK            bool    `json:"ok"`
	Millis        int64   `json:"ms"`
}

func jevEvalClassify(text string) string {
	t := strings.ToLower(strings.TrimSpace(text))
	switch {
	case t == "" || strings.HasPrefix(t, "<") || strings.HasPrefix(t, "[request interrupted") || strings.HasPrefix(t, "----") ||
		strings.HasPrefix(t, "base directory") || strings.HasPrefix(t, "this session is being continued"):
		return ""
	case strings.Contains(t, "nuke") && len(t) <= 120:
		return "nuke"
	case len(t) <= 40 && jevEvalContinue.MatchString(t):
		return "continue"
	case len(t) <= 200 && jevEvalCorrection.MatchString(t):
		return "correction"
	case len(t) >= 40:
		return "followup"
	}
	return ""
}

func jevEvalTurns(messages []search.Message, turns, chars int) []suggestMessage {
	var out []suggestMessage
	for i := len(messages) - 1; i >= 0 && len(out) < turns; i-- {
		m := messages[i]
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		if text := boundedText(m.Text, chars); text != "" {
			out = append([]suggestMessage{{Role: m.Role, Text: text}}, out...)
		}
	}
	return out
}

// jevEvalCases walks every transcript once. Cases whose latest turns match
// another's are dropped, since resumed and forked sessions repeat them.
func jevEvalCases(files []string, archived map[string]bool) map[string][]jevEvalCase {
	byClass := map[string][]jevEvalCase{}
	seen := map[string]bool{}
	add := func(class, source, next string, msgs []search.Message) {
		sum := sha256.New()
		for _, m := range jevEvalTurns(msgs, jevFinishMessages, jevFinishMessageChars) {
			sum.Write([]byte(m.Role + "\x00" + m.Text + "\x00"))
		}
		digest := hex.EncodeToString(sum.Sum(nil))
		if seen[digest] {
			return
		}
		seen[digest] = true
		byClass[class] = append(byClass[class], jevEvalCase{Class: class, Source: source, Next: boundedText(next, 120), msgs: msgs})
	}
	for _, path := range files {
		msgs, err := search.ReadMessages(search.Target{Tool: search.ToolClaude, Path: path}, "")
		if err != nil || len(msgs) < 2 {
			continue
		}
		source := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		prev := ""
		for i, m := range msgs {
			if m.Role == "user" && prev == "assistant" {
				class := jevEvalClassify(m.Text)
				if class == "nuke" {
					class = "nuke-ended"
					for _, later := range msgs[i+1:] {
						if later.Role == "user" && jevEvalClassify(later.Text) != "" && jevEvalClassify(later.Text) != "nuke" {
							class = "nuke-continued"
							break
						}
					}
				}
				if class != "" {
					add(class, source, m.Text, msgs[:i])
				}
			}
			if m.Role == "user" || m.Role == "assistant" {
				prev = m.Role
			}
		}
		if archived[source] && prev == "assistant" {
			add("archived", source, "", msgs)
		}
	}
	return byClass
}

// jevEvalArchived names the conversations of top-level sessions the user
// archived. Children are left out: the board archives those on its own.
func jevEvalArchived(t *testing.T, configDir string) map[string]bool {
	out := map[string]bool{}
	st, err := store.OpenReadOnly(filepath.Join(configDir, "state.db"))
	if err != nil {
		t.Logf("no archived sessions: %v", err)
		return out
	}
	defer st.Close()
	sessions, err := st.ListSessions(true)
	if err != nil {
		t.Logf("no archived sessions: %v", err)
		return out
	}
	for _, sess := range sessions {
		if sess.Archived && sess.ParentID == "" && sess.AgentSessionID != "" {
			out[sess.AgentSessionID] = true
		}
	}
	return out
}

func jevEvalInts(name string, fallback []int) []int {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	var out []int
	for _, field := range strings.Split(raw, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(field)); err == nil && n > 0 {
			out = append(out, n)
		}
	}
	return out
}

func TestJevFinishEval(t *testing.T) {
	outDir := os.Getenv("JEV_FINISH_EVAL")
	if outDir == "" {
		t.Skip("set JEV_FINISH_EVAL to an output directory to run against live JEV")
	}
	configDir := os.Getenv("JEV_EVAL_CONFIG")
	key := jev.Key(configDir)
	if key == "" {
		t.Skip("no TypeSafe key")
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	files, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", "*.jsonl"))
	byClass := jevEvalCases(files, jevEvalArchived(t, configDir))

	rng := rand.New(rand.NewSource(1))
	var cases []jevEvalCase
	classes := make([]string, 0, len(byClass))
	for class := range byClass {
		classes = append(classes, class)
	}
	sort.Strings(classes)
	for _, class := range classes {
		list := byClass[class]
		t.Logf("pool %-15s %d", class, len(list))
		rng.Shuffle(len(list), func(i, j int) { list[i], list[j] = list[j], list[i] })
		cases = append(cases, list[:min(len(list), jevEvalCaps[class])]...)
	}

	questions := []string{"current", "previous"}
	if raw := os.Getenv("JEV_EVAL_QUESTIONS"); raw != "" {
		questions = strings.Split(raw, ",")
	}
	var variants []jevEvalVariant
	for _, q := range questions {
		for _, turns := range jevEvalInts("JEV_EVAL_TURNS", []int{2, 4, 6, 8}) {
			for _, chars := range jevEvalInts("JEV_EVAL_CHARS", []int{800, 1500, 3000}) {
				variants = append(variants, jevEvalVariant{Question: q, Turns: turns, Chars: chars})
			}
		}
	}

	type job struct {
		c jevEvalCase
		v jevEvalVariant
	}
	jobs := make(chan job)
	var mu sync.Mutex
	var scores []jevEvalScore
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				state := jevEvalTurns(j.c.msgs, j.v.Turns, j.v.Chars)
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				start := time.Now()
				answers, ok := askJevNoul(ctx, jevHTTPClient, jevEndpoint, key, map[string]any{"messages": state}, map[string]string{
					"finished":       jevEvalQuestions[j.v.Question],
					"missing_chrome": jevMissingChromeQuestion,
				})
				cancel()
				mu.Lock()
				scores = append(scores, jevEvalScore{jevEvalVariant: j.v, Class: j.c.Class, Source: j.c.Source,
					Finished: answers["finished"], MissingChrome: answers["missing_chrome"], OK: ok, Millis: time.Since(start).Milliseconds()})
				mu.Unlock()
			}
		}()
	}
	for _, c := range cases {
		for _, v := range variants {
			jobs <- job{c, v}
		}
	}
	close(jobs)
	wg.Wait()

	jevEvalWriteJSONL(t, filepath.Join(outDir, "cases.jsonl"), cases)
	jevEvalWriteJSONL(t, filepath.Join(outDir, "scores.jsonl"), scores)
	report := jevEvalReport(variants, scores)
	if err := os.WriteFile(filepath.Join(outDir, "summary.tsv"), []byte(report), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + report)
}

func jevEvalWriteJSONL[T any](t *testing.T, path string, rows []T) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, row := range rows {
		if err := enc.Encode(row); err != nil {
			t.Fatal(err)
		}
	}
}

// jevEvalReport gives, per variant and threshold, the share of done cases the
// check would flag (recall) and the share of open and followup cases it would
// wrongly flag. "Flag" is what the board acts on: finished at or above the
// threshold with missing Chrome at or below the low band, as decide reads it.
func jevEvalReport(variants []jevEvalVariant, scores []jevEvalScore) string {
	var b strings.Builder
	fmt.Fprintf(&b, "variant\tthreshold\tdone_recall\topen_false\tfollowup_flagged\tn_done\tn_open\tn_followup\tfailed\tp90ms\n")
	for _, v := range variants {
		var ms []int64
		failed := 0
		for _, s := range scores {
			if s.jevEvalVariant == v {
				ms = append(ms, s.Millis)
				if !s.OK {
					failed++
				}
			}
		}
		sort.Slice(ms, func(i, j int) bool { return ms[i] < ms[j] })
		p90 := int64(0)
		if len(ms) > 0 {
			p90 = ms[len(ms)*9/10]
		}
		for _, threshold := range jevEvalThresholds {
			n := map[string]int{}
			flagged := map[string]int{}
			for _, s := range scores {
				if s.jevEvalVariant != v || !s.OK {
					continue
				}
				label := jevEvalLabel[s.Class]
				n[label]++
				if s.Finished >= threshold && s.MissingChrome <= jevFinishBandLow {
					flagged[label]++
				}
			}
			share := func(label string) string {
				if n[label] == 0 {
					return "-"
				}
				return fmt.Sprintf("%d%%", 100*flagged[label]/n[label])
			}
			fmt.Fprintf(&b, "%s\t%.1f\t%s\t%s\t%s\t%d\t%d\t%d\t%d\t%d\n", v, threshold,
				share("done"), share("open"), share("followup"), n["done"], n["open"], n["followup"], failed, p90)
		}
	}
	return b.String()
}
