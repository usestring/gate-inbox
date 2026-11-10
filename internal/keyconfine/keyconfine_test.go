package keyconfine

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestBubblewrapHidesTheKeysAndMapsBackOnlyWhatIsKept(t *testing.T) {
	got, err := wrap(Bubblewrap, "/home/u/.config/gate-inbox/channel-keys",
		[]string{"/home/u/.config/gate-inbox/channel-keys/caller.key", "opencode-sessions", "/etc/passwd"},
		[]string{"/bin/sh", "-c", "pi --session-id x"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"bwrap", "--dev-bind", "/", "/", "--die-with-parent",
		"--tmpfs", "/home/u/.config/gate-inbox/channel-keys",
		"--ro-bind-try", "/home/u/.config/gate-inbox/channel-keys/caller.key", "/home/u/.config/gate-inbox/channel-keys/caller.key",
		"--ro-bind-try", "/home/u/.config/gate-inbox/channel-keys/opencode-sessions", "/home/u/.config/gate-inbox/channel-keys/opencode-sessions",
		"--remount-ro", "/home/u/.config/gate-inbox/channel-keys",
		"--", "/bin/sh", "-c", "pi --session-id x"}
	if !slices.Equal(got, want) {
		t.Fatalf("argv:\n got %q\nwant %q", got, want)
	}
}

func TestSandboxExecDeniesTheKeysAndAllowsReadsOfWhatIsKept(t *testing.T) {
	got, err := wrap(SandboxExec, `/Users/u/a"b/channel-keys`, []string{"caller.key"}, []string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"sandbox-exec", "-p",
		`(version 1)(allow default)(deny file-read* file-write* (subpath "/Users/u/a\"b/channel-keys"))` +
			`(allow file-read* (subpath "/Users/u/a\"b/channel-keys/caller.key"))`,
		"codex"}
	if !slices.Equal(got, want) {
		t.Fatalf("argv:\n got %q\nwant %q", got, want)
	}
}

func TestArgvRefusesWhenNothingCanConfine(t *testing.T) {
	t.Cleanup(UseForTests("", errors.New("bwrap (bubblewrap) is not installed")))
	if _, err := Argv("/k", nil, []string{"pi"}); err == nil || !strings.Contains(err.Error(), "bubblewrap") {
		t.Fatalf("want a refusal naming the missing tool, got %v", err)
	}
	if err := Available(); err == nil {
		t.Fatal("Available must report the same refusal")
	}
}

func TestProbeRefusesAnUnsupportedSystem(t *testing.T) {
	if _, err := probe("plan9"); err == nil {
		t.Fatal("want a refusal on a system with no mechanism")
	}
}

// The real thing: a child of the confined process cannot read a key, can read
// what was kept, and cannot write into the directory either.
func TestConfinedProcessTreeCannotReadTheKeys(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bubblewrap is Linux only")
	}
	if _, err := probe("linux"); err != nil {
		t.Skipf("bwrap unavailable here: %v", err)
	}
	keyDir := filepath.Join(t.TempDir(), "channel-keys")
	if err := os.MkdirAll(filepath.Join(keyDir, "opencode-sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"sess1": "seal", "typesafe.key": "ts", "caller.key": "caller", "opencode-sessions/b": "bind"} {
		if err := os.WriteFile(filepath.Join(keyDir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	script := `cd "$1" && for f in sess1 typesafe.key caller.key opencode-sessions/b; do
	  if cat "$f" >/dev/null 2>&1; then echo "read $f"; else echo "denied $f"; fi
	done
	if touch planted 2>/dev/null; then echo "wrote"; else echo "no write"; fi`
	argv, err := wrap(Bubblewrap, keyDir, []string{"caller.key", "opencode-sessions"},
		[]string{"/bin/sh", "-c", "/bin/sh -c '" + script + "' sh " + keyDir})
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	want := "denied sess1\ndenied typesafe.key\nread caller.key\nread opencode-sessions/b\nno write\n"
	if string(out) != want {
		t.Fatalf("confined child saw:\n%s\nwant:\n%s", out, want)
	}
	if _, err := os.Stat(filepath.Join(keyDir, "planted")); err == nil {
		t.Fatal("the confined process planted a file in the real key directory")
	}
}
