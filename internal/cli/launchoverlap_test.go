package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/classifier"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/store"
)

type overlapTransport func(*http.Request) (*http.Response, error)

func (f overlapTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestLaunchOverlapChecksOnlyTheOpeningPromptAfterOptIn(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	const thread = "124d40b2-a298-44f0-a064-66d9d1932c85"
	const earlier = "1309a722"
	const fresh = "567398cb"
	const codex = "0adec456"
	const lateCodex = "7812dcba"
	const independent = "other-target"
	t.Setenv(classifier.JEV.KeyEnv(), "test-token")
	add := func(id, tool string, when time.Time) {
		t.Helper()
		if err := st.CreateSession(store.Session{ID: id, Name: id, Tool: tool, Cwd: dir, CreatedAt: when}); err != nil {
			t.Fatal(err)
		}
	}
	var reads int
	source := func(context.Context) ([]extension.ActiveWork, error) {
		reads++
		return []extension.ActiveWork{
			{Key: "shared-fix", Description: "fix the session launch"},
			{Key: independent, Description: "fix the session launch on a different host"},
			{Key: "own-fix", Session: thread},
			{Key: "board-owned-fix", Session: fresh},
		}, nil
	}
	original := classifier.HTTPClient
	t.Cleanup(func() { classifier.HTTPClient = original })
	requests := make(chan string, 8)
	classifier.HTTPClient = &http.Client{Transport: overlapTransport(func(req *http.Request) (*http.Response, error) {
		var payload struct {
			State     json.RawMessage
			Questions map[string]json.RawMessage
		}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			return nil, err
		}
		requests <- string(payload.State)
		answers := map[string]map[string]any{}
		for name := range payload.Questions {
			score := 0.95
			if name == "independent_work_0" || name == "independent_work_2" {
				score = 0.05
			}
			answers[name] = map[string]any{"type": "noul", "noul": score}
		}
		raw, err := json.Marshal(map[string]any{"model": classifier.JEVModel, "answers": answers})
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(raw))}, nil
	})}
	var transcript string
	run := func(id string, args []string) string {
		t.Helper()
		var out bytes.Buffer
		payload, err := json.Marshal(hooks.CodexPayload{Thread: thread, Prompt: "fix the session launch", TranscriptPath: transcript})
		if err != nil {
			t.Fatal(err)
		}
		if err := RunHookWithActiveWork(t.Context(), bytes.NewReader(payload), &out, args, id, dir, source); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	now := time.Now()
	add(earlier, "claude", now.Add(-time.Minute))
	if out := run(earlier, []string{"prompt-submit"}); out != "" || reads != 0 {
		t.Fatalf("default-off output %q, claim reads %d", out, reads)
	}
	if err := st.SetLaunchOverlap(true, now); err != nil {
		t.Fatal(err)
	}
	if out := run(earlier, []string{"prompt-submit"}); out != "" || reads != 0 {
		t.Fatalf("pre-existing session output %q, claim reads %d", out, reads)
	}
	add(fresh, "claude", now.Add(time.Second))
	if err := st.SetLaunchOverlap(true, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if out := run(fresh, []string{"prompt-submit"}); !strings.Contains(out, "shared-fix") || strings.Contains(out, independent) {
		t.Fatalf("Claude opening hook = %q", out)
	}
	if out := run(fresh, []string{"prompt-submit"}); out != "" || reads != 1 {
		t.Fatalf("Claude follow-up = %q, claim reads %d", out, reads)
	}
	sent := <-requests
	if strings.Contains(sent, "own-fix") || strings.Contains(sent, "board-owned-fix") ||
		strings.Contains(sent, thread) || !strings.Contains(sent, "work_0") {
		t.Fatalf("unexpected classifier state %s", sent)
	}
	add(codex, "codex", now.Add(2*time.Second))
	manager := hooks.NewManager(dir)
	if err := manager.SyncCodex([]hooks.CodexRow{{ID: codex, Thread: thread}}); err != nil {
		t.Fatal(err)
	}
	args := []string{"codex", "UserPromptSubmit"}
	if out := run("daemon-row", args); !strings.Contains(out, "shared-fix") {
		t.Fatalf("Codex opening hook = %q", out)
	}
	if out := run("daemon-row", args); out != "" || reads != 2 {
		t.Fatalf("Codex follow-up = %q, claim reads %d", out, reads)
	}
	add(lateCodex, "codex", now.Add(3*time.Second))
	if err := manager.SyncCodex([]hooks.CodexRow{{ID: lateCodex, Thread: thread}}); err != nil {
		t.Fatal(err)
	}
	transcript = filepath.Join(dir, thread+".jsonl")
	const openingTurn = `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"investigate the launch queue"}]}}`
	if err := os.WriteFile(transcript, []byte(openingTurn+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := run("daemon-row", args); out != "" || reads != 2 || manager.LaunchScope(lateCodex) != "investigate the launch queue" {
		t.Fatalf("Late Codex hook = %q, claim reads %d, scope %q", out, reads, manager.LaunchScope(lateCodex))
	}
}
