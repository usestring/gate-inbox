package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/usestring/gate-inbox/internal/jev"
)

type copyEvalCase struct {
	ID      string   `json:"id"`
	Source  string   `json:"source"`
	Kind    string   `json:"kind"`
	Message string   `json:"message"`
	Want    []string `json:"want"`
	Notes   string   `json:"notes"`
}

func readCopyEvalCases(t *testing.T) []copyEvalCase {
	t.Helper()
	path := os.Getenv("JEV_COPY_CASES")
	if path == "" {
		path = "testdata/jevcopy.json"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cases []copyEvalCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("empty copy corpus")
	}
	return cases
}

func TestJevCopyCorpus(t *testing.T) {
	for _, tc := range readCopyEvalCases(t) {
		t.Run(tc.ID, func(t *testing.T) {
			candidates := copyCandidates(tc.Message)
			answers := make(map[string]float64)
			for i, candidate := range candidates {
				for _, want := range tc.Want {
					if want == candidate {
						answers[fmt.Sprintf("c%d", i+1)] = 1
					}
				}
			}
			for i, part := range copyDraftParts(tc.Message) {
				for _, want := range tc.Want {
					if strings.Contains(want, tc.Message[part.start:part.end]) {
						answers[fmt.Sprintf("p%d", i+1)] = 1
					}
				}
			}
			if got := pickJevCopy(tc.Message, candidates, answers); !slices.Equal(got, tc.Want) {
				t.Fatalf("cannot recover labelled output:\ngot: %q\nwant: %q", got, tc.Want)
			}
		})
	}
}

type copyEvalResult struct {
	ID        string             `json:"id"`
	Variant   string             `json:"variant"`
	Want      []string           `json:"want"`
	Got       []string           `json:"got"`
	Scores    map[string]float64 `json:"scores,omitempty"`
	Questions map[string]string  `json:"questions,omitempty"`
	Answered  bool               `json:"answered"`
	Exact     bool               `json:"exact"`
	ElapsedMS int64              `json:"elapsed_ms"`
}

type copyEvalMetrics struct {
	Cases   int `json:"cases"`
	Exact   int `json:"exact"`
	Missed  int `json:"missed_items"`
	Extra   int `json:"extra_items"`
	Correct int `json:"correct_items"`
	Failed  int `json:"failed_requests"`
}

func (m *copyEvalMetrics) add(r copyEvalResult) {
	m.Cases++
	if r.Exact {
		m.Exact++
	}
	if !r.Answered {
		m.Failed++
	}
	for _, want := range r.Want {
		if slices.Contains(r.Got, want) {
			m.Correct++
		} else {
			m.Missed++
		}
	}
	for _, got := range r.Got {
		if !slices.Contains(r.Want, got) {
			m.Extra++
		}
	}
}

// Live evaluation is explicit because routine tests must never spend API calls
// or send local conversation text to a remote service.
func TestJevCopyEval(t *testing.T) {
	output := os.Getenv("JEV_COPY_EVAL")
	if output == "" {
		return
	}
	key := jev.Key(os.Getenv("JEV_EVAL_CONFIG"))
	if key == "" {
		t.Fatal("JEV copy eval needs a TypeSafe key")
	}
	if err := os.MkdirAll(output, 0o700); err != nil {
		t.Fatal(err)
	}
	var results []copyEvalResult
	metrics := map[string]*copyEvalMetrics{"before": {}, "after": {}}
	for _, tc := range readCopyEvalCases(t) {
		if filter := os.Getenv("JEV_COPY_FILTER"); filter != "" && !strings.Contains(tc.ID, filter) {
			continue
		}
		for _, variant := range []string{"before", "after"} {
			candidates := copyCandidates(tc.Message)
			questions := jevCopyQuestions(tc.Message, candidates)
			message := boundedText(tc.Message, jevCopyMessageChars)
			if variant == "before" {
				candidates = slices.DeleteFunc(candidates, func(c string) bool { return utf8.RuneCountInString(c) > 1000 })
				questions = make(map[string]string)
				for i, c := range candidates {
					questions[fmt.Sprintf("c%d", i+1)] = "Does the latest assistant message ask the user to copy this exact text and use it themselves: " +
						"run it in their own terminal, open it in a browser, or paste it into a form, chat or another app? " +
						"Answer no for text that only explains or names something, and for commands the agent runs itself. Text: " + c
				}
				message = boundedText(tc.Message, 1500)
			}
			result := copyEvalResult{ID: tc.ID, Variant: variant, Want: tc.Want, Questions: questions, Answered: true}
			started := time.Now()
			if len(questions) > 0 {
				ctx, cancel := context.WithTimeout(t.Context(), jevCopyTimeout)
				result.Scores, result.Answered = askJevNoul(ctx, jevHTTPClient, jevEndpoint, key, map[string]any{"message": message}, questions)
				cancel()
			}
			if result.Answered {
				if variant == "after" {
					result.Got = pickJevCopy(tc.Message, candidates, result.Scores)
				} else {
					for i, c := range candidates {
						if result.Scores[fmt.Sprintf("c%d", i+1)] >= jevCopyThreshold {
							result.Got = append(result.Got, c)
						}
					}
				}
			}
			result.ElapsedMS = time.Since(started).Milliseconds()
			result.Exact = result.Answered && slices.Equal(result.Got, result.Want)
			metrics[variant].add(result)
			results = append(results, result)
			t.Logf("%s %-29s exact=%v answered=%v picks=%d", variant, tc.ID, result.Exact, result.Answered, len(result.Got))
		}
	}
	if len(results) == 0 {
		t.Fatal("no cases matched")
	}
	report := struct {
		Model        string                      `json:"model"`
		Threshold    float64                     `json:"threshold"`
		CandidateMax int                         `json:"candidate_max"`
		MessageMax   int                         `json:"message_max"`
		Metrics      map[string]*copyEvalMetrics `json:"metrics"`
		Results      []copyEvalResult            `json:"results"`
	}{jevModel, jevCopyThreshold, jevCopyCandidateMax, jevCopyMessageChars, metrics, results}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, "results.json"), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, variant := range []string{"before", "after"} {
		m := metrics[variant]
		t.Logf("%s: exact %d/%d; correct items=%d missed=%d extra=%d failed requests=%d", variant, m.Exact, m.Cases, m.Correct, m.Missed, m.Extra, m.Failed)
	}
	if m := metrics["after"]; m.Exact != m.Cases {
		t.Errorf("%d current cases failed; see %s", m.Cases-m.Exact, filepath.Join(output, "results.json"))
	}
}
