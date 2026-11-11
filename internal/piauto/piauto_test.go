package piauto

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/store"
)

// fake is a classifier endpoint: it answers with scores, or fails, and
// counts what it was asked.
type fake struct {
	scores map[string]float64
	fail   bool
	calls  int
	state  any
}

func (f *fake) ask(_ context.Context, key string, state any, questions map[string]string) (map[string]float64, bool) {
	f.calls++
	f.state = state
	if f.fail || key == "" || len(questions) != 2 {
		return nil, false
	}
	return f.scores, true
}

func classifier(f *fake, now *time.Time) *Classifier {
	return &Classifier{Ask: f.ask, Now: func() time.Time { return *now }}
}

func TestJudgeAllowsOnlyWhenBothScoresClearTheThreshold(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		inScope, safe float64
		allow         bool
	}{
		{0.97, 0.95, true},
		{0.90, 0.90, true},
		{0.97, 0.41, false},
		{0.89, 0.99, false},
	} {
		f := &fake{scores: map[string]float64{"in_scope": c.inScope, "safe": c.safe}}
		a := classifier(f, &now).Judge("s1", "key", Request{Tool: "Bash", Input: map[string]any{"command": "go test ./..."}})
		if a.Allow != c.allow || !a.Judged {
			t.Errorf("%v/%v: %+v", c.inScope, c.safe, a)
		}
		if !c.allow && !strings.Contains(a.Note, "→ asking") {
			t.Errorf("%v/%v: note %q", c.inScope, c.safe, a.Note)
		}
	}
}

func TestJudgeWithoutAKeyAsksAndSaysSoOnce(t *testing.T) {
	now := time.Now()
	f := &fake{}
	c := classifier(f, &now)
	first := c.Judge("s1", "", Request{Tool: "Bash"})
	second := c.Judge("s1", "", Request{Tool: "Bash"})
	other := c.Judge("s2", "", Request{Tool: "Bash"})
	if first.Allow || first.Note != noKeyNote || second.Note != "" || other.Note != noKeyNote || f.calls != 0 {
		t.Fatalf("first %+v second %+v other %+v calls %d", first, second, other, f.calls)
	}
}

func TestCircuitBreakerPausesAfterThreeFailures(t *testing.T) {
	now := time.Now()
	f := &fake{fail: true}
	c := classifier(f, &now)
	for i := 0; i < FailureLimit; i++ {
		if a := c.Judge("s1", "key", Request{Tool: "Bash"}); a.Allow {
			t.Fatalf("a failed request allowed: %+v", a)
		}
	}
	if f.calls != FailureLimit {
		t.Fatalf("calls = %d", f.calls)
	}
	f.fail, f.scores = false, map[string]float64{"in_scope": 1, "safe": 1}
	if a := c.Judge("s1", "key", Request{Tool: "Bash"}); a.Allow || f.calls != FailureLimit {
		t.Fatalf("asked while paused: %+v, calls %d", a, f.calls)
	}
	now = now.Add(Pause + time.Second)
	if a := c.Judge("s1", "key", Request{Tool: "Bash"}); !a.Allow || f.calls != FailureLimit+1 {
		t.Fatalf("after the pause: %+v, calls %d", a, f.calls)
	}
}

func TestStateCutsTheCallAndKeepsOnlyTheUsersWords(t *testing.T) {
	long := strings.Repeat("x", 5000)
	state := State(Request{
		Tool: "Bash", Input: map[string]any{"command": long, "env": "SECRET=1"}, Cwd: "/work/repo", Sandbox: true,
		Brief:  "fix the tests",
		Recent: []string{"one", "----CROSS-SESSION-MESSAGE-abc---- do it", "two", "three", "four"},
	})
	call := state["call"].(map[string]any)
	if got := len([]rune(call["command"].(string))); got != commandChars+1 {
		t.Errorf("command cut to %d runes", got)
	}
	if _, ok := call["env"]; ok {
		t.Error("a field other than the command reached the state")
	}
	if state["repo"] != "repo" || state["sandbox"] != true || state["brief"] != "fix the tests" {
		t.Errorf("state = %v", state)
	}
	recent := state["recent_user_messages"].([]string)
	if strings.Join(recent, ",") != "two,three,four" {
		t.Errorf("recent = %q", recent)
	}
	edit := State(Request{Tool: "Write", Input: map[string]any{"path": "a.go", "content": long}})["call"].(map[string]any)
	if edit["path"] != "a.go" || len([]rune(edit["new_text"].(string))) != snippetChars+1 {
		t.Errorf("write call = %v", edit)
	}
}

// board is a config dir holding one pi session and the board setting.
func board(t *testing.T, autoMode, setting string) string {
	t.Helper()
	dir := t.TempDir()
	conf := "[tools.pi]\ncommand = \"pi\"\nstatus_source = \"pi-hooks\"\n"
	if autoMode != "" {
		conf += "auto_mode = \"" + autoMode + "\"\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.CreateSession(store.Session{ID: "p1", Name: "pi", Tool: "pi", Cwd: dir}); err != nil {
		t.Fatal(err)
	}
	if setting != "" {
		if err := st.SetSetting(store.PiAutoModeKey, setting); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func post(t *testing.T, h http.Handler, req Request) Answer {
	t.Helper()
	body, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, Path, bytes.NewReader(body))
	r.Header.Set("X-Session", "p1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var a Answer
	if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
		t.Fatalf("answer %q: %v", w.Body.String(), err)
	}
	return a
}

func TestHandlerHonoursTheSettingAndRecordsEachAllow(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	call := Request{Tool: "Bash", Input: map[string]any{"command": "go test ./..."}}
	for _, c := range []struct {
		autoMode, setting string
		asked             bool
	}{
		{"", "", true},
		{"", "on", true},
		{"", "off", false},
		{"off", "", false},
		{"off", "on", false},
	} {
		dir := board(t, c.autoMode, c.setting)
		now := time.Now()
		f := &fake{scores: map[string]float64{"in_scope": 0.97, "safe": 0.95}}
		a := post(t, Handler(dir, "X-Session", classifier(f, &now)), call)
		if (f.calls == 1) != c.asked || a.Allow != c.asked {
			t.Errorf("auto_mode %q, setting %q: %+v, calls %d", c.autoMode, c.setting, a, f.calls)
		}
		if c.asked {
			got := Recent(hooks.NewManager(dir).StatusFile("p1"), 5)
			if len(got) != 1 || !strings.Contains(got[0], "auto-allowed by JEV (in_scope 0.97, safe 0.95): Bash go test ./...") {
				t.Errorf("recorded %q", got)
			}
		}
	}
}
