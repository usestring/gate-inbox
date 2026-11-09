package tracing

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Nothing a span carries off the machine names the machine, a path, a person
// or a raw session: the attributes call sites set today are keyed or dropped,
// error text is blanked where it names a place, and the resource leaves the
// host out.
func TestSpansAndTheResourceCarryNothingPrivate(t *testing.T) {
	host, _ := os.Hostname()
	const (
		session = "4b1a94f5-1111-2222-3333-444455556666"
		account = "SOMEONE"
		socket  = "someone-private-socket"
		tool    = "someone_private_tool"
	)
	// Built from parts, so the fixture reads as no real path or address.
	dir := "/" + strings.Join([]string{"home", "someone", "repos", "private-project"}, "/")
	email := "someone" + "@" + "example.invalid"
	now := time.Now()
	msg := "open " + dir + "/x: denied by https://metrics.example.invalid/api?u=someone, mail " + email
	if host != "" {
		msg += " on " + host
	}
	spans := []Span{{
		Name: "git.run", Start: now, End: now.Add(time.Millisecond), Err: errors.New(msg),
		Attrs: []Attr{
			{Key: "session", Value: session}, {Key: "created", Value: session}, {Key: "dir", Value: dir},
			{Key: "socket", Value: socket}, {Key: "account.name", Value: account}, {Key: "account.lender", Value: account},
			{Key: socketPrefix + socket, Value: 1}, {Key: "git.subcommand", Value: "status"}, {Key: "tool.id", Value: tool},
		},
		traceID: newTraceID(), spanID: newSpanID(),
	}}
	body, err := encode(spans)
	if err != nil {
		t.Fatal(err)
	}
	out := strings.NewReplacer(`\u003c`, "<", `\u003e`, ">").Replace(string(body))
	for _, private := range []string{session, account, "someone", dir, socket, tool, "metrics.example.invalid", "host.name"} {
		if strings.Contains(out, private) {
			t.Errorf("%q left the machine: %s", private, out)
		}
	}
	if host != "" && strings.Contains(out, host) {
		t.Errorf("the hostname left the machine: %s", out)
	}
	if regexp.MustCompile(`"/(home|Users|tmp)/`).MatchString(out) {
		t.Errorf("an absolute path left the machine: %s", out)
	}
	if !strings.Contains(out, KeyedID("session", session)) || !strings.Contains(out, `"status"`) {
		t.Errorf("the keyed session id or a harmless attribute went missing: %s", out)
	}
	if !strings.Contains(out, "denied by <url>") || !strings.Contains(out, "open <path>") {
		t.Errorf("the error lost what failed and why: %s", out)
	}
}

func TestKeyedIDsAreStableKeyedAndHex(t *testing.T) {
	id := KeyedID("session", "abc")
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(id) || id != KeyedID("session", "abc") {
		t.Fatalf("id %q", id)
	}
	if id == KeyedID("account", "abc") || KeyedID("session", "") != "" {
		t.Fatal("namespaces share ids, or empty was hashed")
	}
}

// The key is private on disk and the same on every run, so ids join across
// runs and nobody else on the machine can read it.
func TestTheInstallKeyIsPrivateAndStable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gate-inbox")
	first, err := loadKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, keyFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file %v mode %v", err, info.Mode().Perm())
	}
	second, err := loadKey(dir)
	if err != nil || string(first) != string(second) {
		t.Fatal("the key changed between runs")
	}
}
