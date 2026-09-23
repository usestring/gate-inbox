package convo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTailReadsTheTitlePromptsAndProse(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	got := parseTail(raw, false)
	if got.Title != "Investigate and reduce disk usage" {
		t.Errorf("title = %q", got.Title)
	}
	if got.ID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("id = %q", got.ID)
	}
	if got.Cwd != "/repo/one" {
		t.Errorf("cwd = %q", got.Cwd)
	}
	want := []string{"open the thing", "now check the other one"}
	if len(got.Prompts) != len(want) {
		t.Fatalf("prompts = %q, want %q", got.Prompts, want)
	}
	for i, prompt := range want {
		if got.Prompts[i] != prompt {
			t.Errorf("prompt %d = %q, want %q", i, got.Prompts[i], prompt)
		}
	}
	if len(got.Excerpts) != 2 {
		t.Fatalf("excerpts = %q", got.Excerpts)
	}
	if got.Excerpts[0] != "reading the handler now to see what it does." {
		t.Errorf("excerpt not normalised: %q", got.Excerpts[0])
	}
	if got.Excerpts[1] != "a bare string body" {
		t.Errorf("string content body = %q", got.Excerpts[1])
	}
}

func TestParseTailDropsTheLineTheWindowCutInHalf(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// A window landing mid-file starts inside a record; keeping it would feed
	// half a JSON object to the parser, and the half that survives can be
	// valid JSON meaning something else.
	cut := raw[len(raw)/2:]
	got := parseTail(cut, true)
	first := cut[:strings.IndexByte(string(cut), '\n')]
	if json.Valid(first) {
		t.Skip("the fixture happened to split on a line boundary")
	}
	if got.Title == "" {
		t.Error("a partial window found no title at all")
	}
}

func TestReadTranscriptServesTheSecondReadFromCache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "one.jsonl")
	body, err := os.ReadFile(filepath.Join("testdata", "transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	ix := New(dir, "")
	var cost Cost
	first, ok := ix.readTranscript(path, &cost)
	if !ok || first.Title == "" {
		t.Fatalf("first read = %+v ok=%v", first, ok)
	}
	if cost.Files != 1 || cost.Cached != 0 {
		t.Fatalf("first read cost = %+v", cost)
	}
	second, ok := ix.readTranscript(path, &cost)
	if !ok || second.Title != first.Title {
		t.Fatalf("second read = %+v ok=%v", second, ok)
	}
	if cost.Files != 1 || cost.Cached != 1 {
		t.Errorf("second read re-read the file: %+v", cost)
	}
}

func TestReadTailTakesTheEndOfTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	body := strings.Repeat("a", 100) + "TAIL"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readTail(path, info.Size(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "aaaaaaTAIL" {
		t.Errorf("tail = %q", got)
	}
}

func TestProjectDirMatchesClaudeCodesMangling(t *testing.T) {
	cases := map[string]string{
		"/home/user/repos/sample-repo":   "-home-user-repos-sample-repo",
		"/home/dev/repos/maintainer.ltd": "-home-dev-repos-maintainer-ltd",
		"/home/dev/my_repo/a+b":          "-home-dev-my-repo-a-b",
		"":                               "",
	}
	for cwd, want := range cases {
		if got := projectDir(cwd); got != want {
			t.Errorf("projectDir(%q) = %q, want %q", cwd, got, want)
		}
	}
}

func TestLiveSidecarsIgnoresAProcessThatIsGone(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	// pid 1 is init, whose start time is nothing like the one recorded here,
	// which is what a recycled pid looks like.
	raw, err := json.Marshal(sidecar{PID: 1, SessionID: "abc", Cwd: "/repo", StartedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sessions", "1.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	ix := New(dir, "")
	if got := ix.liveSidecars(); len(got) != 0 {
		t.Errorf("liveSidecars = %+v, want none", got)
	}
}

func TestRefreshForgetsTranscriptsNothingPointsAtAnyMore(t *testing.T) {
	dir := t.TempDir()
	ix := New(dir, "")
	var cost Cost
	path := filepath.Join(dir, "gone.jsonl")
	body, err := os.ReadFile(filepath.Join("testdata", "transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := ix.readTranscript(path, &cost); !ok {
		t.Fatal("read failed")
	}
	if len(ix.tails) != 1 {
		t.Fatalf("cache holds %d", len(ix.tails))
	}
	if err := ix.Refresh(nil); err != nil {
		t.Fatal(err)
	}
	if len(ix.tails) != 0 {
		t.Errorf("cache still holds %d transcripts no conversation names", len(ix.tails))
	}
}

func TestOverlappingRefreshesDoNotRaceOnTheCache(t *testing.T) {
	dir := t.TempDir()
	ix := New(dir, "")
	done := make(chan struct{})
	for i := 0; i < 4; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 20; j++ {
				if err := ix.Refresh([]string{dir}); err != nil {
					t.Error(err)
					return
				}
				ix.Conversations()
				ix.Cost()
			}
		}()
	}
	for i := 0; i < 4; i++ {
		<-done
	}
}

func TestAnUnreadableOpencodeStillPublishesTheClaudeSide(t *testing.T) {
	dir := t.TempDir()
	projects := filepath.Join(dir, "projects", "-repo-one")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join("testdata", "transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projects, "11111111-2222-3333-4444-555555555555.jsonl"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	// A directory where the database should be is not something sql can open,
	// which is the shape of every reason opencode's store is unreadable.
	broken := filepath.Join(dir, "opencode.db")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	ix := New(dir, broken)
	if err := ix.Refresh([]string{"/repo/one"}); err == nil {
		t.Fatal("a broken database was not reported")
	}
	got := ix.Conversations()
	if len(got) != 1 || got[0].Title != "Investigate and reduce disk usage" {
		t.Errorf("conversations = %+v, want the claude side published anyway", got)
	}
}

// A conversation too young to have been titled must come back untitled rather
// than with something assembled from its first prompt: the row keeps the name
// it has until there is a real title to replace it with.
func TestATranscriptWithNoTitleYieldsNoTitle(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "titleless.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	got := parseTail(raw, false)
	if got.Title != "" {
		t.Errorf("title = %q, want none", got.Title)
	}
	if got.ID != "99999999-8888-7777-6666-555555555555" || got.Cwd != "/repo/two" {
		t.Errorf("id/cwd = %q/%q", got.ID, got.Cwd)
	}
}
