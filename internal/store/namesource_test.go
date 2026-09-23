package store

import (
	"path/filepath"
	"testing"
)

func TestANewRowIsUserNamedUnlessItSaysOtherwise(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("a", "")); err != nil {
		t.Fatal(err)
	}
	sess, err := st.Get("a")
	if err != nil {
		t.Fatal(err)
	}
	if sess.NameSource != SourceUser {
		t.Errorf("name source = %q, want %q", sess.NameSource, SourceUser)
	}
}

func TestAdoptionRecordsThatItMadeTheNameUp(t *testing.T) {
	st := newTestStore(t)
	row := sample("a", "")
	row.NameSource = SourceDerived
	if err := st.CreateSession(row); err != nil {
		t.Fatal(err)
	}
	sess, err := st.Get("a")
	if err != nil {
		t.Fatal(err)
	}
	if sess.NameSource != SourceDerived {
		t.Errorf("name source = %q", sess.NameSource)
	}
}

func TestAutoRenameTakesADerivedName(t *testing.T) {
	st := newTestStore(t)
	row := sample("a", "")
	row.NameSource = SourceDerived
	if err := st.CreateSession(row); err != nil {
		t.Fatal(err)
	}
	took, err := st.AutoRenameSession("a", "disk-usage", SourceTitle)
	if err != nil {
		t.Fatal(err)
	}
	if !took {
		t.Fatal("refused a derived name")
	}
	sess, err := st.Get("a")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Name != "disk-usage" || sess.NameSource != SourceTitle {
		t.Errorf("row = %q/%q", sess.Name, sess.NameSource)
	}
}

func TestAutoRenameTakesAnEarlierTitleName(t *testing.T) {
	st := newTestStore(t)
	row := sample("a", "")
	row.NameSource = SourceTitle
	if err := st.CreateSession(row); err != nil {
		t.Fatal(err)
	}
	took, err := st.AutoRenameSession("a", "disk-usage", SourceTitle)
	if err != nil || !took {
		t.Fatalf("took = %v, err = %v", took, err)
	}
}

// The guard this whole feature hangs off: a name a person typed is never
// replaced by a ticker, and the same goes for one the session's own agent
// asked for.
func TestAutoRenameNeverOverwritesANameSomebodyChose(t *testing.T) {
	for _, source := range []string{SourceUser, SourceAgent} {
		st := newTestStore(t)
		row := sample("a", "")
		row.Name = "the-name-i-picked"
		row.NameSource = source
		if err := st.CreateSession(row); err != nil {
			t.Fatal(err)
		}
		took, err := st.AutoRenameSession("a", "disk-usage", SourceTitle)
		if err != nil {
			t.Fatal(err)
		}
		if took {
			t.Errorf("%s: automatic rename reported taking the row", source)
		}
		sess, err := st.Get("a")
		if err != nil {
			t.Fatal(err)
		}
		if sess.Name != "the-name-i-picked" || sess.NameSource != source {
			t.Errorf("%s: row became %q/%q", source, sess.Name, sess.NameSource)
		}
	}
}

func TestAutoRenameCannotClaimAProtectedSource(t *testing.T) {
	st := newTestStore(t)
	row := sample("a", "")
	row.NameSource = SourceDerived
	if err := st.CreateSession(row); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{SourceUser, SourceAgent, "", "made-up"} {
		if _, err := st.AutoRenameSession("a", "disk-usage", source); err == nil {
			t.Errorf("source %q was accepted", source)
		}
	}
}

func TestRenamingFromTheCardMarksTheNameAsAPersonsChoice(t *testing.T) {
	st := newTestStore(t)
	row := sample("a", "")
	row.NameSource = SourceDerived
	if err := st.CreateSession(row); err != nil {
		t.Fatal(err)
	}
	if err := st.RenameSession("a", "mine"); err != nil {
		t.Fatal(err)
	}
	sess, err := st.Get("a")
	if err != nil {
		t.Fatal(err)
	}
	if sess.NameSource != SourceUser {
		t.Errorf("name source = %q", sess.NameSource)
	}
	took, err := st.AutoRenameSession("a", "disk-usage", SourceTitle)
	if err != nil {
		t.Fatal(err)
	}
	if took {
		t.Error("a name typed on the card was overwritten")
	}
}

func TestBackfillClaimsOnlyTheNamesAdoptionWouldHaveWritten(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rows := []struct {
		id, name, cwd, want string
	}{
		{"a", "sample-repo", "/home/dev/repos/sample-repo", SourceDerived},
		{"b", "sample-repo-4", "/home/dev/repos/sample-repo", SourceDerived},
		{"c", "http-gateway-fix", "/home/dev/repos/sample-repo", SourceUser},
		{"d", "sample-repo-4", "/home/dev/repos/other", SourceUser},
		{"e", "sample-repo-1", "/home/dev/repos/sample-repo", SourceUser},
	}
	for _, row := range rows {
		if err := st.CreateSession(Session{ID: row.id, Name: row.name, Tool: "claude", Cwd: row.cwd, Status: "idle"}); err != nil {
			t.Fatal(err)
		}
	}
	// The pass already ran on Open, before these rows existed, so re-arm it.
	if err := st.SetSetting(nameSourceBackfill, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.BackfillNameSource(); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		sess, err := st.Get(row.id)
		if err != nil {
			t.Fatal(err)
		}
		if sess.NameSource != row.want {
			t.Errorf("%q in %q = %q, want %q", row.name, row.cwd, sess.NameSource, row.want)
		}
	}
}

func TestBackfillRunsOnce(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(Session{ID: "a", Name: "tmp", Tool: "claude", Cwd: "/x/tmp", Status: "idle"}); err != nil {
		t.Fatal(err)
	}
	// The row was written after Open marked the pass done, so a second call
	// must leave it alone -- a name somebody types that happens to look
	// derived stays theirs.
	if err := st.BackfillNameSource(); err != nil {
		t.Fatal(err)
	}
	sess, err := st.Get("a")
	if err != nil {
		t.Fatal(err)
	}
	if sess.NameSource != SourceUser {
		t.Errorf("name source = %q, want the pass to have been done already", sess.NameSource)
	}
}
