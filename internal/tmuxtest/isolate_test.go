package tmuxtest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxguard"
)

func TestTheBinaryIsIsolatedBeforeAnyTestRuns(t *testing.T) {
	for _, key := range []string{"TMUX", "TMUX_PANE"} {
		if value, set := os.LookupEnv(key); set {
			t.Errorf("%s is set to %q inside a test run", key, value)
		}
	}
	dir := os.Getenv("TMUX_TMPDIR")
	if dir == "" || dir != PrivateDir() {
		t.Fatalf("TMUX_TMPDIR = %q, want this run's private directory %q", dir, PrivateDir())
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("private TMUX_TMPDIR %s is not a directory: %v", dir, err)
	}
	if strings.HasPrefix(SocketPath("x"), tmuxguard.DefaultSocketDir()+"/") {
		t.Fatalf("a -L socket resolves to %s, inside tmux's default directory", SocketPath("x"))
	}
	// Every socket this package names lands in the private directory.
	Check(t, NewSocket("isolation"))
}

func TestGuardRefusesTheLiveServer(t *testing.T) {
	live := filepath.Join(tmuxguard.DefaultSocketDir(), "default")
	cases := map[string]func(t *testing.T){
		"-S into the default directory": func(t *testing.T) {},
		"an inherited TMUX":             func(t *testing.T) { t.Setenv("TMUX", live+",1234,0") },
		"TMUX_TMPDIR back at /tmp":      func(t *testing.T) { t.Setenv("TMUX_TMPDIR", "/tmp") },
	}
	args := map[string][]string{
		"-S into the default directory": {"-S", live, "kill-server"},
		"an inherited TMUX":             {"list-sessions"},
		"TMUX_TMPDIR back at /tmp":      {"-L", "default", "kill-server"},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			setup(t)
			if err := tmuxguard.Check(args[name]); err == nil {
				t.Fatalf("tmux %v was allowed", args[name])
			}
			func() {
				defer func() {
					if recover() == nil {
						t.Fatalf("Enforce let tmux %v run", args[name])
					}
				}()
				tmuxguard.Enforce(args[name])
			}()
		})
	}
	if err := tmuxguard.Check([]string{"-L", "default", "kill-server"}); err != nil {
		t.Fatalf("the private default server was refused: %v", err)
	}
}

func TestResolveFollowsTmuxPrecedence(t *testing.T) {
	t.Setenv("TMUX", "/elsewhere/tmux-1/from-env,1,0")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"-S", "/x/s", "-L", "n", "ls"}, "/x/s"},
		{[]string{"-L", "n", "-S", "/x/s", "ls"}, "/x/s"},
		{[]string{"-f", "/dev/null", "-L", "n", "ls"}, SocketPath("n")},
		{[]string{"ls", "-L", "n"}, "/elsewhere/tmux-1/from-env"},
		{nil, "/elsewhere/tmux-1/from-env"},
	} {
		if got := tmuxguard.Resolve(c.args); got != c.want {
			t.Errorf("Resolve(%v) = %s, want %s", c.args, got, c.want)
		}
	}
}

func TestScrubEnvDropsTheInheritedServer(t *testing.T) {
	env := ScrubEnv([]string{"TMUX=/tmp/tmux-1000/default,1,0", "TMUX_PANE=%3", "TMUX_TMPDIR=/tmp", "KEEP=1"})
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		if key == "TMUX" || key == "TMUX_PANE" {
			t.Errorf("ScrubEnv kept %s", kv)
		}
	}
	for _, want := range []string{"KEEP=1", "TMUX_TMPDIR=" + PrivateDir()} {
		if !slices.Contains(env, want) {
			t.Errorf("ScrubEnv dropped %s: %v", want, env)
		}
	}
	if slices.Contains(env, "TMUX_TMPDIR=/tmp") {
		t.Errorf("ScrubEnv kept the inherited TMUX_TMPDIR: %v", env)
	}
}
