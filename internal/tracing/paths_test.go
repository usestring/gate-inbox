package tracing

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// pathShape is what a test calls a raw path in exported output: a rooted or
// relative-prefixed token, a drive letter, a UNC share, a file URL, or a
// known root anywhere in a token.
var pathShape = regexp.MustCompile(`(?i)(^|[\s"'=(\[{,;:<>])(~[\w.-]*[/\\]|\.{1,2}[/\\]|[a-z]:[/\\]|\\\\[\w.-]+\\|/[\w.-])|file:/|[/\\](home|users|tmp|var|private|opt|root|mnt|volumes)[/\\]`)

// abs and win build fixture paths from their parts, so no fixture reads as
// a real one.
func abs(parts ...string) string { return "/" + strings.Join(parts, "/") }
func win(drive string, parts ...string) string {
	return drive + `:\` + strings.Join(parts, `\`)
}

// withPlaces points the filter at a home, a repository and a working
// directory of the test's own, so a test can see them caught as text.
func withPlaces(t *testing.T) (home, repo string) {
	t.Helper()
	home = filepath.Join(t.TempDir(), "someone-home")
	repo = filepath.Join(home, "code", "private-repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Chdir(repo)
	localPlaces = sync.OnceValue(findLocalPlaces)
	t.Cleanup(func() { localPlaces = sync.OnceValue(findLocalPlaces) })
	return home, repo
}

func TestAPathIsHashedSegmentBySegment(t *testing.T) {
	key := []byte("this install's 32-byte test key!")
	project := abs("home", "someone", "repos", "project")
	repo := hashPathWith(key, project)
	node := hashPathWith(key, project+"/node")
	other := hashPathWith(key, abs("home", "someone", "repos", "other"))
	if !regexp.MustCompile(`^pa:[0-9a-f]{8}(/[0-9a-f]{8}){3}$`).MatchString(repo) {
		t.Fatalf("hashed path %q: want pa: and four 8-hex segments", repo)
	}
	if !strings.HasPrefix(node, repo+"/") || strings.Count(node, "/") != 4 {
		t.Errorf("a path inside another should extend its hashes: %q under %q", node, repo)
	}
	if other[:len(other)-9] != repo[:len(repo)-9] || other == repo {
		t.Errorf("siblings should share all but their last hash: %q %q", other, repo)
	}
	for _, same := range []string{project + "/", abs("home", "someone", ".", "repos", "x", "..", "project"),
		strings.ReplaceAll(project, "/", `\`), "file://" + project} {
		if got := hashPathWith(key, same); got != repo {
			t.Errorf("%q normalises to %q, want %q", same, got, repo)
		}
	}
	// The same name at another depth, or in a relative path, is not the
	// same hash: each covers the path up to it.
	if hashPathWith(key, abs("project"))[3:] == repo[len(repo)-8:] || strings.HasPrefix(hashPathWith(key, project[1:]), "pa:") {
		t.Error("a hash names its segment alone, or a relative path reads as absolute")
	}
	if rel := hashPathWith(key, "../x/y"); !regexp.MustCompile(`^pr:[0-9a-f]{8}(/[0-9a-f]{8}){2}$`).MatchString(rel) {
		t.Errorf("relative path %q: want pr: and three segments, its climb included", rel)
	}
	upper, lower := hashPathWith(key, win("C", "Users", "someone", "repo")), hashPathWith(key, "c:"+abs("Users", "someone", "repo"))
	if upper != lower || strings.Count(upper, "/") != 3 || hashPathWith(key, win("D", "Users", "someone", "repo")) == upper {
		t.Errorf("a drive letter is the first segment, case folded: %q %q", upper, lower)
	}
	if hashPathWith([]byte("another install's 32-byte key!!!"), project) == repo {
		t.Error("two installs hash a path alike, so a guess on one reverses the other")
	}
}

func TestPathsAreHashedInPlaceInsideText(t *testing.T) {
	home, repo := withPlaces(t)
	cases := []struct{ in, want string }{
		{"open " + abs("home", "someone", "x.go") + ": denied", "open " + HashPath(abs("home", "someone", "x.go")) + ": denied"},
		{"read ~/notes.md failed", "read " + HashPath(home+"/notes.md") + " failed"},
		{"see ./cmd/main.go.", "see " + HashPath("cmd/main.go") + "."},
		{"from ../../secrets", "from " + HashPath("../../secrets")},
		{`C:\Program Files\Tool\tool.exe not found`, HashPath(`C:\Program Files\Tool\tool.exe`) + " not found"},
		{`\\fileserver\share\doc.txt is busy`, HashPath(`//fileserver/share/doc.txt`) + " is busy"},
		{"cwd=" + abs("Users", "someone", "project"), "cwd=" + HashPath(abs("Users", "someone", "project"))},
		{"stat " + abs("private", "var", "folders", "x") + ": no such file", "stat " + HashPath(abs("private", "var", "folders", "x")) + ": no such file"},
		{"loading file://" + abs("opt", "app", "config.toml"), "loading " + HashPath(abs("opt", "app", "config.toml"))},
		{"in workspace/home/someone/repo", "in " + HashPath("workspace/home/someone/repo")},
		{"repo " + repo + "/internal/a.go: bad", "repo " + HashPath(repo+"/internal/a.go") + ": bad"},
		{"under " + home + " somewhere", "under " + HashPath(home) + " somewhere"},
		// What must survive: ratios, either-ors, URLs for the URL scrubber,
		// plain words, and a path already hashed.
		{"3/4 sessions and/or tools", "3/4 sessions and/or tools"},
		{"https://example.invalid/api failed", "https://example.invalid/api failed"},
		{"status: exit 1", "status: exit 1"},
		{HashPath("/a/b"), HashPath("/a/b")},
		{"", ""},
	}
	for _, c := range cases {
		if got := HidePaths(c.in); got != c.want {
			t.Errorf("HidePaths(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A field that is a path goes as its hash, so two spans from one directory
// still join; a key that is a path is dropped, since nothing says what it
// meant.
func TestAPathAttributeGoesAsItsHash(t *testing.T) {
	_, repo := withPlaces(t)
	attrs := privatize([]Attr{
		{Key: "cwd", Value: repo}, {Key: "binary", Value: `C:\Program Files\tool.exe`},
		{Key: "args", Value: []string{"--dir", repo}}, {Key: abs("home", "someone", "key"), Value: 1},
		{Key: "count", Value: 3}, {Key: "took", Value: time.Second}, {Key: "ok", Value: true},
	})
	got := map[string]any{}
	for _, a := range attrs {
		got[a.Key] = a.Value
	}
	if got["cwd"] != HashPath(repo) || got["binary"] != HashPath(`C:\Program Files\tool.exe`) ||
		got["args"] != "[--dir "+HashPath(repo)+"]" {
		t.Errorf("paths should go as their hashes: %v", got)
	}
	if _, ok := got[abs("home", "someone", "key")]; ok {
		t.Error("a key holding a path was sent")
	}
	if got["count"] != 3 || got["took"] != time.Second || got["ok"] != true {
		t.Errorf("numbers and flags changed: %v", got)
	}
}

// Whatever a span is given, nothing path-shaped comes out the other end: not
// in a name, an attribute, an error or a link.
func FuzzNoPathLeavesInASpan(f *testing.F) {
	for _, seed := range []string{
		abs("home", "a", "b"), "~/x", "./y", "../z", win("C", "Users", "a"), "c:/a", `\\srv\share\f`, "file://" + abs("etc", "x"),
		"open " + abs("Users", "a", "b") + ": no", "x=" + abs("tmp", "y"), abs("private", "var", "folders", "q"),
		"a" + abs("home", "b"), "plain words", "3/4", "error: " + abs("opt", "thing", "bin") + " failed with ../rel",
		abs("mnt", "c", "Users", "x"), abs("Volumes", "Disk", "x"), win("C", "Program Files", "x y", "z"),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		now := time.Now()
		body, err := encode([]Span{{
			Name: s, Start: now, End: now, Err: errors.New(s),
			Attrs:   []Attr{{Key: "v", Value: s}, {Key: s, Value: 1}, {Key: "in", Value: "a " + s + " b"}},
			traceID: newTraceID(), spanID: newSpanID(), links: []Link{{TraceID: s, SpanID: s}},
		}})
		if err != nil {
			return
		}
		for _, v := range exportedStrings(t, body) {
			if pathShape.MatchString(v) {
				t.Fatalf("%q came out as %q", s, v)
			}
		}
	})
}

// exportedStrings is every string an OTLP body carries, keys and values.
func exportedStrings(t *testing.T, body []byte) []string {
	t.Helper()
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch typed := v.(type) {
		case string:
			out = append(out, typed)
		case []any:
			for _, x := range typed {
				walk(x)
			}
		case map[string]any:
			for k, x := range typed {
				out = append(out, k)
				walk(x)
			}
		}
	}
	walk(doc)
	return out
}

// The call sites core has today, each given the path it could plausibly
// pick up, and an extension's span besides: no path and no part of one --
// not "home", not the user, not the repository -- leaves in clear.
func TestNoCallSiteShipsAPath(t *testing.T) {
	home, repo := withPlaces(t)
	now := time.Now()
	span := func(name string, err error, attrs ...Attr) Span {
		return Span{Name: name, Start: now, End: now.Add(time.Millisecond), Err: err, Attrs: attrs,
			traceID: newTraceID(), spanID: newSpanID()}
	}
	spans := []Span{
		span("git.run", errors.New("fatal: not a git repository: "+repo+"/.git"),
			Attr{Key: "git.subcommand", Value: "status"}, Attr{Key: "dir", Value: repo}),
		span("worktracker.refresh", nil, Attr{Key: "session", Value: "abc"}, Attr{Key: "refs", Value: 2}),
		span("tmux.run", errors.New("error connecting to "+abs("tmp", "tmux-1000", "someone")+" (No such file or directory)"),
			Attr{Key: "socket", Value: abs("tmp", "tmux-1000", "someone")}, Attr{Key: "args", Value: 4}),
		span("hooks.read", errors.New("open "+home+"/.config/gate-inbox/hooks/abc.status: permission denied"),
			Attr{Key: "mailbox", Value: "status"}),
		span("sessioncmd.models", errors.New(`exec: "`+abs("usr", "local", "bin", "claude")+`": stat failed`), Attr{Key: "tool", Value: "claude"}),
		span("sessioncmd.migrate", errors.New("cannot move "+win("C", "Users", "someone", "repo")+" to "+win("D", "work"))),
		span("mcp.tool", nil, Attr{Key: "tool.id", Value: "create_session"}),
		span("telemetry.session", errors.New("transcript ~"+abs(".agent", "logs", "someone-home", "x.jsonl")+" unreadable"),
			Attr{Key: "extension", Value: "telemetry"}, Attr{Key: "cwd", Value: repo},
			Attr{Key: "binary", Value: abs("Applications", "Thing.app", "Contents", "MacOS", "thing")}),
	}
	spans[len(spans)-1].links = []Link{{TraceID: newTraceID(), SpanID: newSpanID()}}
	body, err := encode(spans)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range exportedStrings(t, body) {
		lower := strings.ToLower(v)
		for _, raw := range []string{"home", "users", "someone", "private-repo", ".git", "tmux-1000", ".agent", "jsonl", "thing.app", "program files"} {
			if strings.Contains(lower, raw) {
				t.Errorf("%q left the machine in %q", raw, v)
			}
		}
		if pathShape.MatchString(v) {
			t.Errorf("a path left the machine: %q", v)
		}
	}
	text := string(body)
	if !strings.Contains(text, "permission denied") || !strings.Contains(text, `"status"`) ||
		!strings.Contains(text, HashPath(repo)) {
		t.Errorf("the filter took what failed and why with it, or lost the cwd's hash: %s", body)
	}
}
