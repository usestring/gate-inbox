package convo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testdata/pi-1.5.1/named.jsonl is a session pi 1.5.1 wrote on a board launch
// with --session-id: one bash turn, then /name typed while it ran, so the
// session_info entry sits between the prompt and the reply.
const (
	piFixtureID  = "bbbbbbbb-cccc-4444-8888-dddddddddddd"
	piFixtureCwd = "/srv/project"
)

// installPiFixture copies the fixture into a sessions root under the folder
// pi uses for cwd and returns the root and the file.
func installPiFixture(t *testing.T, cwd string) (root, path string) {
	t.Helper()
	raw, err := os.ReadFile("testdata/pi-1.5.1/named.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.ReplaceAll(string(raw), piFixtureCwd, cwd))
	root = t.TempDir()
	dir := filepath.Join(root, PiProjectDir(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(dir, "2026-11-10T11-00-37-733Z_"+piFixtureID+".jsonl")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return root, path
}

func TestPiProjectDirMatchesPi(t *testing.T) {
	for cwd, want := range map[string]string{
		"/srv/project": "--srv-project--",
		`C:\work\demo`: "--C--work-demo--",
	} {
		if got := PiProjectDir(cwd); got != want {
			t.Errorf("PiProjectDir(%q) = %q, want %q", cwd, got, want)
		}
	}
}

func TestPiConversationReadsNamePromptsAndReply(t *testing.T) {
	cwd := t.TempDir()
	root, path := installPiFixture(t, cwd)
	ix := New("", "").WithPi(root)
	if err := ix.Refresh([]string{cwd}); err != nil {
		t.Fatal(err)
	}
	convos := ix.Conversations()
	if len(convos) != 1 {
		t.Fatalf("found %d conversations, want 1: %+v", len(convos), convos)
	}
	got := convos[0]
	if got.Tool != PiTool || got.ID != piFixtureID || got.Cwd != cwd || got.TranscriptPath != path {
		t.Errorf("identity = %q %q %q %q", got.Tool, got.ID, got.Cwd, got.TranscriptPath)
	}
	if got.Title != "quokka hunt" {
		t.Errorf("Title = %q, want the /name the session was given", got.Title)
	}
	if len(got.FirstPrompts) != 1 || !strings.HasPrefix(got.FirstPrompts[0], "Use the bash tool to run: echo zebra-quokka-42") {
		t.Errorf("FirstPrompts = %q", got.FirstPrompts)
	}
	if len(got.Prompts) != 1 {
		t.Errorf("Prompts = %q", got.Prompts)
	}
	// Prose only: the thinking and the tool call are not on screen.
	if len(got.Excerpts) != 1 || got.Excerpts[0] != "the marker is printed" {
		t.Errorf("Excerpts = %q", got.Excerpts)
	}
}

// The file only grows, so a second pass reads the appended entries alone and
// a later /name wins.
func TestPiConversationFollowsAppends(t *testing.T) {
	cwd := t.TempDir()
	root, path := installPiFixture(t, cwd)
	ix := New("", "").WithPi(root)
	if err := ix.Refresh([]string{cwd}); err != nil {
		t.Fatal(err)
	}
	appendLines(t, path,
		`{"type":"message","id":"c1","parentId":"5dbe7db9","timestamp":"2026-11-10T11:02:00.000Z","message":{"role":"user","content":[{"type":"text","text":"now list the files"}],"timestamp":1}}`,
		`{"type":"session_info","id":"c2","parentId":"c1","timestamp":"2026-11-10T11:02:01.000Z","name":"file listing"}`,
		`{"type":"message","id":"c3","parentId":"c2","timestamp":"2026-11-10T11:02:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"There are no files here."}],"timestamp":2}}`,
	)
	if err := ix.Refresh([]string{cwd}); err != nil {
		t.Fatal(err)
	}
	got := ix.Conversations()[0]
	if got.Title != "file listing" {
		t.Errorf("Title = %q, want the later name", got.Title)
	}
	if len(got.Prompts) != 2 || got.Prompts[1] != "now list the files" || len(got.FirstPrompts) != 2 {
		t.Errorf("Prompts = %q FirstPrompts = %q", got.Prompts, got.FirstPrompts)
	}
	if last := got.Excerpts[len(got.Excerpts)-1]; last != "there are no files here." {
		t.Errorf("last excerpt = %q", last)
	}
	full, _ := os.Stat(path)
	if read := ix.Cost().Bytes; read == 0 || read >= full.Size() {
		t.Errorf("second pass read %d bytes of a %d byte file; want only what was appended", read, full.Size())
	}
}

// A message the board typed on its own account is not what the session is
// about.
func TestPiConversationSkipsBoardMessages(t *testing.T) {
	var convo Conversation
	foldPi(&convo, []byte(`{"type":"session","version":3,"id":"x","cwd":"/w"}
{"type":"message","id":"a","parentId":null,"message":{"role":"user","content":"[gate-inbox] keep-warm ping"}}
{"type":"message","id":"b","parentId":"a","message":{"role":"user","content":"fix the build"}}
`))
	if len(convo.Prompts) != 1 || convo.Prompts[0] != "fix the build" {
		t.Errorf("Prompts = %q", convo.Prompts)
	}
}

// Linking: a pane launched with the id gets the conversation outright; an
// adopted pane with no id gets it when it is alone in its directory.
func TestPiConversationLinksToItsPane(t *testing.T) {
	cwd := t.TempDir()
	root, _ := installPiFixture(t, cwd)
	ix := New("", "").WithPi(root)
	if err := ix.Refresh([]string{cwd}); err != nil {
		t.Fatal(err)
	}
	launched := Link([]Pane{{Key: "row1", Tool: "pi", Cwd: cwd, AgentID: piFixtureID}}, ix.Conversations())
	if m, ok := launched.For("row1"); !ok || m.Conversation.Title != "quokka hunt" || !m.Has(SignalAgentID) {
		t.Errorf("launched pane: %+v %v", m, ok)
	}
	adopted := Link([]Pane{{Key: "row2", Tool: "pi", Cwd: cwd}}, ix.Conversations())
	if m, ok := adopted.For("row2"); !ok || m.Conversation.ID != piFixtureID {
		t.Errorf("adopted pane: %+v %v", m, ok)
	}
	claude := Link([]Pane{{Key: "row3", Tool: "claude", Cwd: cwd}}, ix.Conversations())
	if _, ok := claude.For("row3"); ok {
		t.Error("a claude pane took a pi conversation")
	}
}

func TestPiTranscriptForFindsTheFileByID(t *testing.T) {
	cwd := t.TempDir()
	root, path := installPiFixture(t, cwd)
	if got := PiTranscriptFor(root, piFixtureID, cwd); got != path {
		t.Errorf("direct = %q, want %q", got, path)
	}
	// A session resumed from another directory is found across folders.
	if got := PiTranscriptFor(root, piFixtureID, "/elsewhere"); got != path {
		t.Errorf("fallback = %q, want %q", got, path)
	}
	if got := PiTranscriptFor(root, "00000000-0000-4000-8000-000000000000", cwd); got != "" {
		t.Errorf("unknown id = %q", got)
	}
	if got := PiTranscriptFor(root, "*", cwd); got != "" {
		t.Errorf("a glob id matched %q", got)
	}
}

// pi 1.5 overwrites its argv with its process title, so the pane's
// conversation is the newest session file for its directory.
func TestPiConversationInPaneTakesTheNewestFile(t *testing.T) {
	cwd := t.TempDir()
	root, path := installPiFixture(t, cwd)
	older := filepath.Join(filepath.Dir(path), "2026-11-09T09-00-00-000Z_11111111-2222-4333-8444-555555555555.jsonl")
	if err := os.WriteFile(older, []byte(`{"type":"session","version":3,"id":"11111111-2222-4333-8444-555555555555","timestamp":"2026-11-09T09:00:00.000Z","cwd":"`+cwd+`"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	_ = os.Chtimes(older, past, past)
	id, gotCwd, ok := PiConversationInPane(root, cwd)
	if !ok || id != piFixtureID || gotCwd != cwd {
		t.Errorf("= %q %q %v, want %q %q", id, gotCwd, ok, piFixtureID, cwd)
	}
	if _, _, ok := PiConversationInPane(root, t.TempDir()); ok {
		t.Error("a directory with no session file resolved")
	}
}

func appendLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, line := range lines {
		if _, err := f.WriteString(line + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	// A later mtime, so the cache sees the append on a coarse clock.
	later := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(path, later, later)
}

// read_session's delta over a pi session: prompts, the prose of each turn,
// and tool results by call id, from a cursor forward.
func TestSincePiReadsTheConversationForward(t *testing.T) {
	_, path := installPiFixture(t, t.TempDir())
	first, err := SincePi(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Prompts) != 1 || !strings.HasPrefix(first.Prompts[0], "Use the bash tool to run: echo zebra-quokka-42") {
		t.Errorf("Prompts = %q", first.Prompts)
	}
	if first.LastTurn() != "the marker is printed" {
		t.Errorf("LastTurn = %q", first.LastTurn())
	}
	if len(first.Results) != 1 || first.Results[0].ToolUseID == "" {
		t.Errorf("Results = %+v", first.Results)
	}
	appendLines(t, path, `{"type":"message","id":"c3","parentId":"5dbe7db9","timestamp":"2026-11-10T11:02:02.000Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hm"},{"type":"text","text":"All done."}],"timestamp":2}}`)
	next, err := SincePi(path, first.Next)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Prompts) != 0 || len(next.Turns) != 1 || next.Turns[0] != "All done." || next.Rewound {
		t.Errorf("next = %+v", next)
	}
}

// A filtered copy written beside a session file is not a conversation.
func TestPiCopiesBesideASessionAreNotConversations(t *testing.T) {
	cwd := t.TempDir()
	root, path := installPiFixture(t, cwd)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".handover.jsonl", raw, 0o644); err != nil {
		t.Fatal(err)
	}
	ix := New("", "").WithPi(root)
	if err := ix.Refresh([]string{cwd}); err != nil {
		t.Fatal(err)
	}
	if got := ix.Conversations(); len(got) != 1 || got[0].TranscriptPath != path {
		t.Errorf("conversations = %+v", got)
	}
	if newest, _, ok := PiNewestFor(root, cwd); !ok || newest != path {
		t.Errorf("newest = %q %v", newest, ok)
	}
}
