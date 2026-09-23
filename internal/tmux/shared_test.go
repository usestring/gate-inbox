// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package tmux

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recordSeparator ends every argument the stub logs, so an argument carrying
// a space still arrives as one token.
const recordSeparator = "\x1e"

// stubTmux replaces the tmux binary with a script that records every
// invocation and answers the handful of reads the driver makes. It exists so
// this file can drive the whole surface against a server that does not exist:
// the point is what the manager sends, and sending it at a real default
// server is the one thing these tests must never do.
func stubTmux(t *testing.T, boundRootKeys ...string) (*Driver, func() [][]string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	var keyLines []string
	for _, key := range boundRootKeys {
		keyLines = append(keyLines, "bind-key -T root "+key+" \tdetach-client")
	}
	script := `#!/bin/sh
{ for a in "$@"; do printf '%s\036' "$a"; done; printf '\n'; } >> "` + log + `"
case "$3" in
list-keys) cat <<'KEYS'
` + strings.Join(keyLines, "\n") + `
KEYS
;;
show-options) echo "C-b" ;;
display-message) echo "%3" ;;
list-sessions) printf 'gi_one\nuser-shell\n' ;;
has-session) exit 0 ;;
esac
exit 0
`
	bin := filepath.Join(dir, "tmux")
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatalf("write tmux stub: %v", err)
	}
	driver, err := NewWithSocket(DefaultSocket)
	if err != nil {
		t.Fatalf("NewWithSocket: %v", err)
	}
	driver.bin = bin
	return driver, func() [][]string {
		raw, err := os.ReadFile(log)
		if err != nil {
			return nil
		}
		var calls [][]string
		for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
			args := strings.Split(strings.TrimSuffix(line, recordSeparator), recordSeparator)
			if len(args) > 0 && args[0] != "" {
				calls = append(calls, args)
			}
		}
		return calls
	}
}

// driveEverything runs every driver method that talks to the server, so the
// assertions below see the whole surface rather than the parts a reader
// remembered to list.
func driveEverything(t *testing.T, driver *Driver) {
	t.Helper()
	driver.PublishPaneTheme(PaneTheme{Background: "#101010", ColorFgBg: "15;0"})
	if err := driver.PushPaneTheme(); err != nil {
		t.Fatalf("PushPaneTheme: %v", err)
	}
	if err := driver.EnsureBindings(); err != nil {
		t.Fatalf("EnsureBindings: %v", err)
	}
	if err := driver.Create("audit", t.TempDir(), "", nil, 80, 24); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := driver.RefreshChrome("audit"); err != nil {
		t.Fatalf("RefreshChrome: %v", err)
	}
	if err := driver.SetLabel("audit", "audit"); err != nil {
		t.Fatalf("SetLabel: %v", err)
	}
	if err := driver.Resize("audit", 100, 40); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if err := driver.PrepareAttach("audit"); err != nil {
		t.Fatalf("PrepareAttach: %v", err)
	}
	driver.RestorePinnedWindows()
	if _, err := driver.PendingRequest(); err != nil {
		t.Fatalf("PendingRequest: %v", err)
	}
	if err := driver.ClearRequest(); err != nil {
		t.Fatalf("ClearRequest: %v", err)
	}
	if err := driver.Kill("audit"); err != nil {
		t.Fatalf("Kill: %v", err)
	}
}

// splitCommands cuts one invocation into the tmux commands it carries, since
// the driver sends several at a time separated by ";".
func splitCommands(args []string) [][]string {
	var commands [][]string
	current := []string{}
	for _, arg := range args[2:] {
		if arg == ";" {
			commands = append(commands, current)
			current = []string{}
			continue
		}
		current = append(current, arg)
	}
	return append(commands, current)
}

func isGlobalFlag(arg string) bool {
	return strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(arg, "g")
}

func TestUnownedSocketNeverWritesServerWide(t *testing.T) {
	driver, calls := stubTmux(t)
	if driver.owned {
		t.Fatal("driver on tmux's default socket reports itself as owned")
	}
	driveEverything(t, driver)

	writes := map[string]bool{"set-option": true, "set-environment": true, "set-window-option": true}
	for _, call := range calls() {
		for _, command := range splitCommands(call) {
			if len(command) == 0 {
				continue
			}
			line := strings.Join(command, " ")
			switch command[0] {
			case "kill-server":
				t.Errorf("kill-server on a server we do not own: %q", line)
			case "unbind-key":
				t.Errorf("unbind-key drops a binding that is not ours: %q", line)
			}
			if !writes[command[0]] {
				continue
			}
			var global bool
			option := ""
			for _, arg := range command[1:] {
				if isGlobalFlag(arg) {
					global = true
					continue
				}
				if !strings.HasPrefix(arg, "-") && option == "" {
					option = arg
				}
			}
			// The one global write left is the manager's own user option,
			// which tmux itself never reads and no config can collide with.
			if global && option != requestOption {
				t.Errorf("server-global write on a server we do not own: %q", line)
			}
		}
	}
}

func TestUnownedSocketBindsOnlyFreeRootKeys(t *testing.T) {
	driver, calls := stubTmux(t, "C-q", "M-o")
	if err := driver.EnsureBindings(); err != nil {
		t.Fatalf("EnsureBindings: %v", err)
	}
	var bound []string
	for _, call := range calls() {
		for _, command := range splitCommands(call) {
			if len(command) > 2 && command[0] == "bind-key" {
				if command[1] != "-n" {
					t.Errorf("bind-key outside the root table on a shared server: %q", strings.Join(command, " "))
					continue
				}
				bound = append(bound, command[2])
			}
		}
	}
	for _, key := range bound {
		if key == "C-q" || key == "M-o" {
			t.Errorf("replaced the operator's existing %s binding", key)
		}
	}
	if len(bound) == 0 {
		t.Error("bound nothing at all; the free keys should still be taken")
	}
}

func TestOwnedSocketKeepsServerWideOptions(t *testing.T) {
	driver, calls := stubTmux(t)
	driver.socket = "amownedstub"
	driver.owned = true
	if !driver.owned {
		t.Fatal("driver on its own socket reports itself as unowned")
	}
	driveEverything(t, driver)

	var sawGlobalTheme, sawUnbind bool
	for _, call := range calls() {
		for _, command := range splitCommands(call) {
			line := strings.Join(command, " ")
			if strings.HasPrefix(line, "set-option -g window-style") {
				sawGlobalTheme = true
			}
			if strings.HasPrefix(line, "unbind-key") {
				sawUnbind = true
			}
		}
	}
	if !sawGlobalTheme {
		t.Error("owned socket lost its server-global pane theme")
	}
	if !sawUnbind {
		t.Error("owned socket lost the unbind that retires the old editor key")
	}
}

func TestRootKeyName(t *testing.T) {
	cases := map[string]string{
		"bind-key -T root C-q                    detach-client":    "C-q",
		`bind-key -T root C-\\                   detach-client`:    `C-\`,
		"bind-key -T root MouseDown1Pane         select-pane -t =": "MouseDown1Pane",
		"bind-key -r -T root M-o                 send-keys M-o":    "M-o",
		"bind-key -T prefix d                    detach-client":    "",
		"": "",
	}
	for line, want := range cases {
		if got := rootKeyName(line); got != want {
			t.Errorf("rootKeyName(%q) = %q, want %q", line, got, want)
		}
	}
}

// TestUnownedThemeLandsOnTheSessionAlone is the same guard against a real
// tmux, which is the only thing that can say the re-scoped options are still
// options tmux accepts and puts where they were meant to go.
func TestUnownedThemeLandsOnTheSessionAlone(t *testing.T) {
	driver := requireTmux(t)
	driver.owned = false
	const background = "bg=#4b2d17"
	driver.PublishPaneTheme(PaneTheme{Background: "#4b2d17", ColorFgBg: "15;0"})

	id := uniqueID("sharedtheme")
	if err := driver.Create(id, t.TempDir(), "", nil, 80, 24); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { driver.Kill(id) })
	if err := driver.PushPaneTheme(); err != nil {
		t.Fatalf("PushPaneTheme: %v", err)
	}

	name := sessionName(id)
	out, err := tmuxCmd("show-options", "-t", name, "-v", "window-style").CombinedOutput()
	if err != nil {
		t.Fatalf("show session window-style: %v: %s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != background {
		t.Errorf("session window-style = %q, want %q", got, background)
	}
	out, err = tmuxCmd("show-environment", "-t", name, "COLORFGBG").CombinedOutput()
	if err != nil {
		t.Fatalf("show session COLORFGBG: %v: %s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "COLORFGBG=15;0" {
		t.Errorf("session COLORFGBG = %q, want COLORFGBG=15;0", got)
	}

	out, _ = tmuxCmd("show-options", "-g", "-v", "window-style").CombinedOutput()
	if got := strings.TrimSpace(string(out)); got == background {
		t.Errorf("repainted every window on the server: global window-style = %q", got)
	}
	out, _ = tmuxCmd("show-environment", "-g", "COLORFGBG").CombinedOutput()
	if got := strings.TrimSpace(string(out)); got == "COLORFGBG=15;0" {
		t.Errorf("re-env'd every session on the server: global COLORFGBG = %q", got)
	}
}

// TestSessionPinnedToAnotherServerStaysThere covers the rows a manager that
// ran on its own socket left behind: the session is recorded with the server
// it lives on, and moving the manager must not move where those are looked
// for.
func TestSessionPinnedToAnotherServerStaysThere(t *testing.T) {
	driver, calls := stubTmux(t)
	if err := driver.Adopt("old", Target{Socket: "gate-inbox", Name: "%3"}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if target := driver.TargetFor("old"); target.Socket != "gate-inbox" || target.Name != "%3" {
		t.Fatalf("TargetFor(old) = %+v, want the pane on gate-inbox", target)
	}
	if !driver.Exists("old") {
		t.Fatal("Exists said no for a pane the stub server answered for")
	}
	var reached bool
	for _, call := range calls() {
		if len(call) > 1 && call[0] == "-L" && call[1] == "gate-inbox" {
			reached = true
		}
	}
	if !reached {
		t.Error("no command went to the server the session is pinned to")
	}
}

// hasTmuxEnv reports whether a command would hand $TMUX to what it runs.
func hasTmuxEnv(env []string) bool {
	for _, entry := range env {
		if strings.HasPrefix(entry, "TMUX=") {
			return true
		}
	}
	return false
}

// TestAttachFromInsideTheSameServer covers the attach the manager makes for a
// living. Sharing a server with the terminal the manager runs in is what tmux
// calls nesting and refuses, so the attach has to say it means it.
func TestAttachFromInsideTheSameServer(t *testing.T) {
	driver := requireTmux(t)
	id := uniqueID("attach")
	if err := driver.Create(id, t.TempDir(), "", nil, 80, 24); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { driver.Kill(id) })

	out, err := tmuxCmd("list-panes", "-t", sessionName(id), "-F", "#{pane_id}").CombinedOutput()
	if err != nil {
		t.Fatalf("list-panes: %v: %s", err, out)
	}
	pane := strings.TrimSpace(string(out))

	// The manager sits in some other pane on the same server: the attach
	// must go through, so tmux's nesting guard has to come off.
	t.Setenv("TMUX", "/tmp/tmux-1000/"+testSocket+",1,0")
	t.Setenv("TMUX_PANE", "%99999")
	if hasTmuxEnv(driver.AttachCommand(id).Env) {
		t.Error("kept $TMUX, so tmux refuses the attach as nesting")
	}

	// The manager sits in this very session: attaching it would put its
	// screen inside itself, and tmux's refusal is the right answer.
	t.Setenv("TMUX_PANE", pane)
	cmd := driver.AttachCommand(id)
	if cmd.Env != nil && !hasTmuxEnv(cmd.Env) {
		t.Error("dropped $TMUX for the one attach that loops back on itself")
	}

	// Nothing around the manager at all, the way a plain terminal runs it.
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	if hasTmuxEnv(driver.AttachCommand(id).Env) {
		t.Error("invented a $TMUX for a manager that is not inside tmux")
	}
}
