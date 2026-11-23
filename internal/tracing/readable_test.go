package tracing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withReadable sets patterns for one test and clears them after.
func withReadable(t *testing.T, patterns ...string) {
	t.Helper()
	if err := SetReadableRepos(patterns); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = SetReadableRepos(nil) })
}

// fakeRepo makes a checkout at dir whose origin is url, without git.
func fakeRepo(t *testing.T, dir, url string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := "[core]\n\tbare = false\n[remote \"upstream\"]\n\turl = https://github.com/elsewhere/other.git\n[remote \"origin\"]\n\turl = " + url + "\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// secretParts are the names above every fixture repository that must never
// be sent.
const secretUser, secretSlug = "someone-secret", "slug-secret"

func checkout(t *testing.T) string {
	return filepath.Join(t.TempDir(), secretUser, "repos", secretSlug)
}

func TestAPathInAReadableRepoIsSentRelativeToIt(t *testing.T) {
	repo := checkout(t)
	fakeRepo(t, repo, "git@github.com:Acme/Widgets.git")
	withReadable(t, "acme/*")
	file := filepath.Join(repo, "src", "main.go")
	cases := map[string]string{
		file:                           "acme/widgets:src/main.go",
		repo:                           "acme/widgets:",
		repo + "/":                     "acme/widgets:",
		"file://" + file:               "acme/widgets:src/main.go",
		repo + "/src/../docs/x.md":     "acme/widgets:docs/x.md",
		"open " + file + ": no such":   "open acme/widgets:src/main.go: no such",
		"cd " + repo + " && make test": "cd acme/widgets: && make test",
	}
	for in, want := range cases {
		if got := HidePaths(in); got != want {
			t.Errorf("HidePaths(%q) = %q, want %q", in, got, want)
		}
		if got := scrubMessage(in); got != want {
			t.Errorf("scrubMessage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAPathOutsideAReadableRepoStaysHashed(t *testing.T) {
	base := t.TempDir()
	private := filepath.Join(base, secretUser, "private")
	fakeRepo(t, private, "git@github.com:someone/private.git")
	gitlab := filepath.Join(base, secretUser, "gitlab")
	fakeRepo(t, gitlab, "git@gitlab.com:acme/widgets.git")
	noRepo := filepath.Join(base, secretUser, "notes", "plan.md")
	withReadable(t, "acme/*")
	for _, p := range []string{filepath.Join(private, "x.go"), filepath.Join(gitlab, "x.go"), noRepo, "./src/main.go"} {
		if got, want := HidePaths(p), HashPath(p); got != want {
			t.Errorf("HidePaths(%q) = %q, want the hash %q", p, got, want)
		}
	}
}

func TestNothingAboveTheRepoRootIsSent(t *testing.T) {
	repo := checkout(t)
	fakeRepo(t, repo, "https://github.com/acme/widgets")
	withReadable(t, "acme/widgets")
	for _, s := range []string{
		HidePaths(filepath.Join(repo, "a", "b.go")),
		HidePaths("failed in " + repo + "/a/b.go and " + filepath.Dir(repo) + "/other"),
		scrubMessage(filepath.Join(repo, "a")),
	} {
		for _, secret := range []string{secretUser, secretSlug, "repos", os.TempDir()} {
			if strings.Contains(s, secret) {
				t.Errorf("%q leaks %q", s, secret)
			}
		}
	}
}

func TestASymlinkOrDotDotOutOfAReadableRepoIsHashed(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "widgets")
	fakeRepo(t, repo, "git@github.com:acme/widgets.git")
	outside := filepath.Join(base, secretUser, "diary")
	write(t, filepath.Join(outside, "today.md"), "x")
	if err := os.Symlink(outside, filepath.Join(repo, "link")); err != nil {
		t.Fatal(err)
	}
	withReadable(t, "acme/*")
	for _, p := range []string{
		filepath.Join(repo, "link", "today.md"),
		filepath.Join(repo, "link", "not-yet.md"),
		filepath.Join(repo, "link"),
		repo + "/../" + secretUser + "/diary/today.md",
	} {
		if got, want := HidePaths(p), HashPath(p); got != want {
			t.Errorf("HidePaths(%q) = %q, want the hash %q", p, got, want)
		}
	}
	// A symlink that stays inside is fine.
	write(t, filepath.Join(repo, "real", "f.go"), "x")
	if err := os.Symlink(filepath.Join(repo, "real"), filepath.Join(repo, "alias")); err != nil {
		t.Fatal(err)
	}
	if got := HidePaths(filepath.Join(repo, "alias", "f.go")); got != "acme/widgets:alias/f.go" {
		t.Errorf("inside symlink: %q", got)
	}
}

func TestLinkedWorktreeAndSubmoduleUseTheirOwnOrigin(t *testing.T) {
	base := t.TempDir()
	super := filepath.Join(base, "super")
	fakeRepo(t, super, "git@github.com:acme/super.git")
	// A submodule: .git is a file naming .git/modules/<name> in the
	// superproject, which holds the submodule's own config.
	modules := filepath.Join(super, ".git", "modules", "lib")
	write(t, filepath.Join(modules, "config"), "[remote \"origin\"]\n\turl = https://github.com/acme/lib.git\n")
	write(t, filepath.Join(super, "lib", ".git"), "gitdir: ../.git/modules/lib\n")
	private := filepath.Join(super, "vendor", "secret")
	write(t, filepath.Join(super, ".git", "modules", "secret", "config"), "[remote \"origin\"]\n\turl = git@github.com:someone/secret.git\n")
	write(t, filepath.Join(private, ".git"), "gitdir: ../../.git/modules/secret\n")
	// A linked worktree: its .git file names a per-worktree git directory,
	// whose commondir leads back to the main one and its config.
	wt := filepath.Join(base, "worktrees", secretSlug)
	wtGit := filepath.Join(super, ".git", "worktrees", secretSlug)
	write(t, filepath.Join(wtGit, "commondir"), "../..\n")
	write(t, filepath.Join(wt, ".git"), "gitdir: "+wtGit+"\n")
	withReadable(t, "acme/*")
	cases := map[string]string{
		filepath.Join(super, "lib", "x.go"):  "acme/lib:x.go",
		filepath.Join(super, "y.go"):         "acme/super:y.go",
		filepath.Join(wt, "cmd", "main.go"):  "acme/super:cmd/main.go",
		filepath.Join(private, "readme.txt"): HashPath(filepath.Join(private, "readme.txt")),
	}
	for in, want := range cases {
		if got := HidePaths(in); got != want {
			t.Errorf("HidePaths(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRemoteURLForms(t *testing.T) {
	cases := map[string]string{
		"git@github.com:o/r.git":           "o/r",
		"git@github.com:O/R":               "o/r",
		"https://github.com/o/r.git":       "o/r",
		"https://github.com/o/r/":          "o/r",
		"https://user:tok@github.com/o/r":  "o/r",
		"ssh://git@github.com:22/o/r.git":  "o/r",
		"ssh://git@ssh.github.com/o/r.git": "",
		"https://gitlab.com/o/r.git":       "",
		"git@github.com-work:o/r.git":      "",
		"https://github.com/o":             "",
		"https://github.com/o/r/tree/main": "",
		"https://github.com/o/..":          "",
		"/srv/git/o/r.git":                 "",
		"file:///srv/github.com/o/r.git":   "",
		"":                                 "",
	}
	for in, want := range cases {
		if got := githubRepo(in); got != want {
			t.Errorf("githubRepo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDirIsSentOnlyWhenReadable(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "widgets")
	fakeRepo(t, repo, "git@github.com:acme/widgets.git")
	private := filepath.Join(base, "private")
	fakeRepo(t, private, "git@github.com:someone/private.git")
	withReadable(t, "acme/*")
	got := privatize([]Attr{{Key: "dir", Value: filepath.Join(repo, "pkg")}})
	if len(got) != 1 || got[0].Value != "acme/widgets:pkg" {
		t.Errorf("readable dir: %+v", got)
	}
	if got := privatize([]Attr{{Key: "dir", Value: private}}); len(got) != 0 {
		t.Errorf("private dir should be dropped: %+v", got)
	}
}

func TestNoReadableReposIsTodaysOutput(t *testing.T) {
	repo := checkout(t)
	fakeRepo(t, repo, "git@github.com:acme/widgets.git")
	_ = SetReadableRepos(nil)
	file := filepath.Join(repo, "src", "main.go")
	cases := map[string]string{
		file:                      HashPath(file),
		repo:                      HashPath(repo),
		"open " + file + ": no":   "open " + HashPath(file) + ": no",
		"x acme/widgets:src/a.go": "x acme/widgets:src/a.go",
	}
	for in, want := range cases {
		if got := HidePaths(in); got != want {
			t.Errorf("HidePaths(%q) = %q, want %q", in, got, want)
		}
	}
	if got := privatize([]Attr{{Key: "dir", Value: repo}}); len(got) != 0 {
		t.Errorf("dir should be dropped with no readable repos: %+v", got)
	}
	if err := SetReadableRepos([]string{"acme"}); err == nil {
		t.Error("a pattern without a name should be refused")
	}
	if err := SetReadableRepos([]string{"acme/[x"}); err == nil {
		t.Error("a bad glob should be refused")
	}
}

func TestReadableFormIsNotHashedAgain(t *testing.T) {
	repo := checkout(t)
	fakeRepo(t, repo, "git@github.com:acme/widgets.git")
	withReadable(t, "acme/*")
	// A path inside the repo that holds a known root ("tmp") must survive
	// every pass of the scanner.
	file := filepath.Join(repo, "node", "tmp", "x.js")
	if got := HidePaths("at " + file); got != "at acme/widgets:node/tmp/x.js" {
		t.Errorf("got %q", got)
	}
	// Text that only looks like the form but climbs out is hashed.
	if got := HidePaths("x acme/widgets:../home/me/secret"); strings.Contains(got, "home/me") {
		t.Errorf("climbing form leaked: %q", got)
	}
}
