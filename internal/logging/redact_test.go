package logging

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fixtures are assembled from a prefix and a body rather than written out
// whole, so this file holds no string that looks like a credential to a
// secret scanner while still exercising the real shapes.
func shape(prefix, rest string) string { return prefix + rest }

var alnum = "0123456789abcdefghijklmnopqrstuvwxyz"

var secretFixtures = []struct {
	label  string
	secret string
}{
	{"claude oauth", shape("sk-", "ant-oat01-AbCdEf0123456789_-ghIJKLmnopQRstuvWXyz")},
	{"anthropic api", shape("sk-", "ant-api03-"+alnum)},
	{"openai", shape("sk-", "proj-"+alnum)},
	{"openrouter", shape("sk-", "or-v1-"+alnum)},
	{"github pat classic", shape("ghp", "_"+alnum+"ABCDEF")},
	{"github oauth", shape("gho", "_"+alnum+"ABCDEF")},
	{"github fine grained", shape("github", "_pat_11ABCDEFG0123456789_abcdefghijklmnop")},
	{"aws access key", shape("AKIA", "IOSFODNN7EXAMPLE")},
	{"aws session key", shape("ASIA", "IOSFODNN7EXAMPLE")},
	{"slack bot", shape("xoxb", "-1234567890-0987654321-AbCdEfGhIjKlMnOp")},
	{"google api", shape("AIza", "SyA0123456789abcdefghijklmnopqrstuvw")},
	{"grafana service account", shape("glsa", "_0123456789abcdefghij_01234567")},
	{"jwt", shape("eyJ", "hbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1rXwW1gFWFOEjXk")},
}

func TestScrubRemovesEverySecretShape(t *testing.T) {
	for _, fixture := range secretFixtures {
		t.Run(fixture.label, func(t *testing.T) {
			line := "agent printed export TOKEN=" + fixture.secret + " and carried on"
			got := Scrub(line)
			if strings.Contains(got, fixture.secret) {
				t.Fatalf("Scrub kept the secret: %q", got)
			}
			if !strings.Contains(got, redacted) {
				t.Fatalf("Scrub removed nothing: %q", got)
			}
			if !strings.Contains(got, "and carried on") {
				t.Fatalf("Scrub ate the surrounding line: %q", got)
			}
		})
	}
}

func TestScrubRemovesBearerAndNamedSecrets(t *testing.T) {
	cases := []struct {
		label string
		in    string
		gone  string
	}{
		{"bearer", "Authorization: Bearer abcdef0123456789abcdef", "abcdef0123456789abcdef"},
		{"api_key", `api_key="hunter2-hunter2"`, "hunter2-hunter2"},
		{"password", "password: correct-horse-battery", "correct-horse-battery"},
		{"client secret", "client_secret=abcdef012345", "abcdef012345"},
		{"access token", "access-token = zzzzzzzzzzzz", "zzzzzzzzzzzz"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			got := Scrub(tc.in)
			if strings.Contains(got, tc.gone) {
				t.Fatalf("Scrub kept %q in %q", tc.gone, got)
			}
			if !strings.Contains(got, redacted) {
				t.Fatalf("Scrub removed nothing: %q", got)
			}
		})
	}
}

func TestScrubLeavesOrdinaryTextAlone(t *testing.T) {
	for _, line := range []string{
		"key",
		"tmux command socket=default cmd=list-panes -a took=4.1ms",
		"key=enter branch=list mode=list row=session name=sample-repo-71",
		"session gi_0f0f0f0f cwd=/home/user/repos/sample-repo",
	} {
		if got := Scrub(line); got != line {
			t.Fatalf("Scrub rewrote an ordinary line:\n in: %q\nout: %q", line, got)
		}
	}
}

// A secret can reach a record as an attribute rather than as message text,
// so the handler scrubs on the way out as well. The two together are what
// make redaction a property of the log rather than of every call site.
func TestHandlerScrubsAttributesAndErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gate-inbox.log")
	logger, err := Open(Options{Path: path, Level: LevelInfo, MaxSizeMB: 1, MaxBackups: 1, MaxTotalMB: 4})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	secret := secretFixtures[0].secret
	logger.Info("tmux command failed",
		"cmd", "send-keys -t gi_1 "+secret,
		"err", &scrubTestError{"denied for " + secret},
		"args", []string{"--token", secret},
		slog.Group("nested", slog.String("value", secret)))
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	body := readFile(t, path)
	if strings.Contains(body, secret) {
		t.Fatalf("the secret reached the log:\n%s", body)
	}
	if !strings.Contains(body, "tmux command failed") {
		t.Fatalf("the record never arrived:\n%s", body)
	}
}

type scrubTestError struct{ text string }

func (e *scrubTestError) Error() string { return e.text }

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

// An opaque credential in an environment-variable assignment carries no
// recognisable prefix; the variable's name is the only signal there is.
func TestScrubRemovesOpaqueValuesNamedAsCredentials(t *testing.T) {
	for _, tc := range []struct{ in, gone string }{
		{"export GITHUB_TOKEN=0123456789abcdef", "0123456789abcdef"},
		{"export CLAUDE_CODE_OAUTH_TOKEN=zzzzzzzzzzzz", "zzzzzzzzzzzz"},
		{"ANTHROPIC_API_KEY=aaaaaaaaaaaa", "aaaaaaaaaaaa"},
		{"WEBHOOK_SECRET: bbbbbbbbbbbb", "bbbbbbbbbbbb"},
		{"credential=cccccccccccc", "cccccccccccc"},
	} {
		got := Scrub(tc.in)
		if strings.Contains(got, tc.gone) {
			t.Fatalf("Scrub kept %q in %q", tc.gone, got)
		}
		if !strings.Contains(got, redacted) {
			t.Fatalf("Scrub removed nothing from %q", tc.in)
		}
	}
}

// Redaction runs over every record, so a false positive costs the log the
// field it was written for. These are the fields this program actually logs.
func TestScrubLeavesThisProgramsOwnFieldsAlone(t *testing.T) {
	for _, line := range []string{
		`key=enter branch=search mode=list row=session name=gcp-migration cursor=2/4 searching=true query=sample-repo`,
		`socket=default cmd="capture-pane -p -e -t %358" took=2.655ms`,
		`session=cbc3292b tool=claude status=finished archived=false`,
		`sockets=[default] candidates=31 taken=4 rejected="already on the board=27"`,
		`version=dev home=/home/user/.config/gate-inbox pollInterval=2s`,
	} {
		if got := Scrub(line); got != line {
			t.Fatalf("Scrub rewrote a log line this program writes:\n in: %q\nout: %q", line, got)
		}
	}
}

// tmux captures physical lines, so a token wider than the pane arrives split
// in two. Scrubbing the halves separately leaves the tail of a live key in
// the file, which is the one guarantee trace makes.
func TestScrubWrappedCatchesATokenSplitAcrossTwoLines(t *testing.T) {
	secret := secretFixtures[0].secret
	split := len(secret) / 2
	pane := "$ echo $TOKEN\n" + secret[:split] + "\n" + secret[split:] + "\n$ make test"

	if got := Scrub(pane); !strings.Contains(got, secret[split:]) {
		t.Fatalf("the fixture does not reproduce the leak Scrub alone has:\n%s", got)
	}
	got := ScrubWrapped(pane)
	if strings.Contains(got, secret[split:]) {
		t.Fatalf("the tail of the split token survived:\n%s", got)
	}
	if strings.Contains(got, secret[:split]) {
		t.Fatalf("the head of the split token survived:\n%s", got)
	}
	if !strings.Contains(got, "$ make test") {
		t.Fatalf("ScrubWrapped ate the rest of the pane:\n%s", got)
	}
}

// The continuation rule only fires under a line that ends in a redaction,
// so ordinary pane output has to come through whole.
func TestScrubWrappedLeavesOrdinaryPaneOutputAlone(t *testing.T) {
	pane := "$ go test ./...\nok  \tgithub.com/usestring/gate-inbox\t0.3s\n$ "
	if got := ScrubWrapped(pane); got != pane {
		t.Fatalf("ScrubWrapped rewrote a clean pane:\n in: %q\nout: %q", pane, got)
	}
}

func TestScrubRemovesCredentialsFromAURL(t *testing.T) {
	got := Scrub("cloning https://alice:ghs_notarealtokenvalue00@github.com/example-org/sample-repo.git")
	if strings.Contains(got, "notarealtokenvalue00") {
		t.Fatalf("a credential in a URL survived: %q", got)
	}
	if !strings.Contains(got, "github.com/example-org/sample-repo.git") {
		t.Fatalf("Scrub ate the URL: %q", got)
	}
}
