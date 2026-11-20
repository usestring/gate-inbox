package convo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testdata/omp-19.0.1/named.jsonl is a session omp 19.0.1 wrote: one prompt
// and its reply, then /rename, which rewrote the title line the file opens
// with and appended a title_change entry. omp prefixed the name it was given
// with a tag of its own.
const (
	ompFixtureID    = "eeeeeeee-ffff-7777-8888-aaaaaaaaaaaa"
	ompFixtureCwd   = "/srv/omp-project"
	ompFixtureTitle = "🧷 FXTR: fixture run"
)

// ompHome points HOME and TMPDIR at fresh directories, so the folder omp
// would file a directory under is the same here as on any machine.
func ompHome(t *testing.T) (home, tmp string) {
	t.Helper()
	home, tmp = t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TMPDIR", tmp)
	return home, tmp
}

// installOmpFixture copies the fixture into a sessions root under the folder
// omp uses for cwd and returns the root and the file.
func installOmpFixture(t *testing.T, cwd string) (root, path string) {
	t.Helper()
	raw, err := os.ReadFile("testdata/omp-19.0.1/named.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.ReplaceAll(string(raw), ompFixtureCwd, cwd))
	root = t.TempDir()
	dir := filepath.Join(root, OmpProjectDirs(cwd)[0])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(dir, "2026-11-20T02-09-40-365Z_"+ompFixtureID+".jsonl")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return root, path
}

// The names are omp's getDefaultSessionDirName, checked against omp 19.0.1,
// which filed a session started in /tmp/<dir>/proj under "-tmp-<dir>-proj".
// The first candidate is the one omp writes with this process's HOME and
// TMPDIR; pi's name is always the last.
func TestOmpProjectDirsMatchOmp(t *testing.T) {
	home, tmp := ompHome(t)
	for cwd, want := range map[string]string{
		filepath.Join(home, "src", "app"): "-src-app",
		home:                              "-",
		filepath.Join(tmp, "a", "b"):      "-tmp-a-b",
		"/tmp/omp-demo/proj":              "-tmp-omp-demo-proj",
		"/srv/project":                    "--srv-project--",
	} {
		got := OmpProjectDirs(cwd)
		if got[0] != want || got[len(got)-1] != PiProjectDir(cwd) {
			t.Errorf("OmpProjectDirs(%q) = %q, want %q first and pi's name last", cwd, got, want)
		}
	}
}

func TestOmpConversationReadsTitlePromptAndReply(t *testing.T) {
	home, _ := ompHome(t)
	cwd := filepath.Join(home, "work")
	root, path := installOmpFixture(t, cwd)
	ix := New("", "").WithPi(PiStore{Tool: OmpTool, Root: root})
	if err := ix.Refresh([]string{cwd}); err != nil {
		t.Fatal(err)
	}
	convos := ix.Conversations()
	if len(convos) != 1 {
		t.Fatalf("found %d conversations, want 1: %+v", len(convos), convos)
	}
	got := convos[0]
	if got.Tool != OmpTool || got.ID != ompFixtureID || got.Cwd != cwd || got.TranscriptPath != path {
		t.Errorf("identity = %q %q %q %q", got.Tool, got.ID, got.Cwd, got.TranscriptPath)
	}
	if got.Title != ompFixtureTitle {
		t.Errorf("Title = %q, want %q", got.Title, ompFixtureTitle)
	}
	if len(got.FirstPrompts) != 1 || got.FirstPrompts[0] != "Reply with just the word hi" {
		t.Errorf("FirstPrompts = %q", got.FirstPrompts)
	}
	if len(got.Excerpts) == 0 || got.Excerpts[0] != "hi" {
		t.Errorf("Excerpts = %q", got.Excerpts)
	}
}

// omp rewrites the title line in place, behind what an earlier pass read, so
// a later pass must see the new name even with no title_change after it.
func TestOmpTitleRewrittenInPlaceIsRead(t *testing.T) {
	home, _ := ompHome(t)
	cwd := filepath.Join(home, "work")
	slot := func(title string) string {
		line := `{"type":"title","v":1,"title":"` + title + `","updatedAt":"2026-11-20T00:00:00.000Z","pad":"`
		return line + strings.Repeat(" ", titleSlotBytes-len(line)-3) + "\"}\n"
	}
	root := t.TempDir()
	dir := filepath.Join(root, OmpProjectDirs(cwd)[0])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "2026-11-20T00-00-00-000Z_"+ompFixtureID+".jsonl")
	body := slot("") + `{"type":"session","version":3,"id":"` + ompFixtureID + `","cwd":"` + cwd + `"}` + "\n"
	if len(slot("")) != titleSlotBytes {
		t.Fatalf("slot is %d bytes", len(slot("")))
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ix := New("", "").WithPi(PiStore{Tool: OmpTool, Root: root})
	if err := ix.Refresh([]string{cwd}); err != nil {
		t.Fatal(err)
	}
	if got := ix.Conversations()[0].Title; got != "" {
		t.Fatalf("Title = %q before any name", got)
	}

	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte(slot("auto title")), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte(`{"type":"message","id":"a","message":{"role":"user","content":"next"}}`+"\n"), int64(len(body))); err != nil {
		t.Fatal(err)
	}
	file.Close()
	if err := ix.Refresh([]string{cwd}); err != nil {
		t.Fatal(err)
	}
	if got := ix.Conversations()[0].Title; got != "auto title" {
		t.Errorf("Title = %q, want the rewritten slot's", got)
	}
}

func TestOmpTranscriptAndPaneReadPastTheTitleLine(t *testing.T) {
	home, _ := ompHome(t)
	cwd := filepath.Join(home, "work")
	root, path := installOmpFixture(t, cwd)
	store := PiStore{Tool: OmpTool, Root: root}
	if got := store.TranscriptFor(ompFixtureID, cwd); got != path {
		t.Errorf("TranscriptFor = %q, want %q", got, path)
	}
	if got := store.TranscriptFor(ompFixtureID, "/elsewhere"); got != path {
		t.Errorf("TranscriptFor from another directory = %q, want %q", got, path)
	}
	id, gotCwd, ok := store.ConversationInPane(cwd)
	if !ok || id != ompFixtureID || gotCwd != cwd {
		t.Errorf("ConversationInPane = %q %q %v", id, gotCwd, ok)
	}
	// pi's store does not look in omp's folders.
	if _, _, ok := (PiStore{Tool: PiTool, Root: root}).ConversationInPane(cwd); ok {
		t.Error("pi's store read omp's folder")
	}
}

func TestPiStoreForPicksTheCLIsStore(t *testing.T) {
	for _, c := range []struct {
		name, sessionStore, want string
	}{
		{"pi", "", PiTool},
		{"pi-work", "pi", PiTool},
		{"omp", "", OmpTool},
		{"omp-fast", "omp", OmpTool},
		{"claude", "", ""},
		{"codex", "codex", ""},
	} {
		store, ok := PiStoreFor(c.name, c.sessionStore)
		if ok != (c.want != "") || store.Tool != c.want {
			t.Errorf("PiStoreFor(%q, %q) = %q %v, want %q", c.name, c.sessionStore, store.Tool, ok, c.want)
		}
	}
}
