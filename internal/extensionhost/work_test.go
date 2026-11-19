package extensionhost

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

func workBoard(t *testing.T) (string, *sessioncmd.Sessions) {
	t.Helper()
	configDir := tmuxtest.ScratchDir(t)
	st, err := store.Open(filepath.Join(configDir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.CreateSession(store.Session{ID: "cafe0001", Name: "one", Tool: "claude", Cwd: configDir, Status: "idle"}); err != nil {
		t.Fatal(err)
	}
	return configDir, sessioncmd.NewSessions(configDir, sessioncmd.MCPVocabulary())
}

// An extension attaching a page lands it in the state database under the calling session and the
// extension's ID, where the board reads it from; attaching the same ID again replaces it.
func TestAttachFilesWorkUnderTheCallerAndTheExtension(t *testing.T) {
	configDir, cmds := workBoard(t)
	host := New(configDir, "cafe0001", cmds).ForExtension("pages")
	ctx := context.Background()
	if err := extension.AttachWork(ctx, host, extension.WorkItem{
		Kind: extension.WorkArtifact, ID: "p1", Title: "  Weekly\n report ", URL: "https://pages.example.test/p/1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := extension.AttachWork(ctx, host, extension.WorkItem{
		Kind: extension.WorkArtifact, ID: "p1", Title: "Weekly report v2", URL: "https://pages.example.test/p/1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := extension.AttachWork(ctx, host, extension.WorkItem{Kind: extension.WorkArtifact, URL: "https://pages.example.test/p/2"}); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(configDir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got, err := st.SessionWorkItems("cafe0001")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("stored %+v", got)
	}
	byID := map[string]store.SessionWork{}
	for _, item := range got {
		byID[item.ID] = item
	}
	if p1 := byID["p1"]; p1.Source != "pages" || p1.Kind != "artifact" || p1.Title != "Weekly report v2" {
		t.Errorf("p1 = %+v", p1)
	}
	// With no ID the URL is the ID.
	if _, ok := byID["https://pages.example.test/p/2"]; !ok {
		t.Errorf("an item with no ID was not keyed by its URL: %+v", got)
	}
}

// What the board cannot show, or cannot file under a session, is refused with a reason rather
// than stored.
func TestAttachRefusesWhatTheBoardCannotShow(t *testing.T) {
	configDir, cmds := workBoard(t)
	ctx := context.Background()
	lent := New(configDir, "cafe0001", cmds).ForExtension("pages")
	for name, tc := range map[string]struct {
		host extension.Host
		item extension.WorkItem
		want string
	}{
		"unknown kind": {lent, extension.WorkItem{Kind: "video", URL: "https://pages.example.test/v"}, "not one the board shows"},
		"relative url": {lent, extension.WorkItem{Kind: extension.WorkArtifact, URL: "/p/1"}, "not an absolute"},
		"file url":     {lent, extension.WorkItem{Kind: extension.WorkArtifact, URL: "file:///tmp/a.html"}, "not an absolute"},
		"operator":     {NewOperator(configDir, cmds).ForExtension("pages"), extension.WorkItem{Kind: extension.WorkArtifact, URL: "https://pages.example.test/p/1"}, "operator's shell"},
		"no extension": {New(configDir, "cafe0001", cmds), extension.WorkItem{Kind: extension.WorkArtifact, URL: "https://pages.example.test/p/1"}, "lent to no extension"},
		"gone session": {New(configDir, "cafe0009", cmds).ForExtension("pages"), extension.WorkItem{Kind: extension.WorkArtifact, URL: "https://pages.example.test/p/1"}, "no longer exists"},
	} {
		err := extension.AttachWork(ctx, tc.host, tc.item)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error saying %q", name, err, tc.want)
		}
	}
}

// A title is one row's label: it is kept to one line and bounded.
func TestSessionWorkTitleIsOneBoundedLine(t *testing.T) {
	stored, err := sessionWork("pages", extension.WorkItem{Kind: extension.WorkArtifact, URL: "https://pages.example.test/p", Title: strings.Repeat("é", maxWorkTitle)})
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Title) > maxWorkTitle || !strings.HasPrefix(stored.Title, "é") || strings.ContainsRune(stored.Title, '�') {
		t.Errorf("title = %d bytes %q", len(stored.Title), stored.Title)
	}
}
