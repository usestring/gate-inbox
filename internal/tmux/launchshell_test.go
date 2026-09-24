package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// launchshellHelperEnv picks a role when TestLoginProbeLeavesTheCallersTerminalAlone
// re-runs this binary: "measure" owns a terminal and runs the probe, "steal"
// is what the fake shell runs in place of a startup file.
const (
	launchshellHelperEnv  = "GATE_INBOX_LAUNCHSHELL_HELPER"
	launchshellFakeEnv    = "GATE_INBOX_LAUNCHSHELL_FAKE"
	launchshellKeptMarker = "terminal kept"
)

// runLaunchshellHelper is called from TestMain and exits the process when it
// is running as a helper, so a helper never reaches the test run.
func runLaunchshellHelper() {
	switch os.Getenv(launchshellHelperEnv) {
	case "measure":
		os.Exit(measureProbeTerminal())
	case "steal":
		os.Exit(stealTerminal())
	}
}

// ttyForegroundGroup reads the foreground process group of the terminal
// open on fd.
func ttyForegroundGroup(fd int) (int, error) {
	var pgrp int32
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(unix.TIOCGPGRP), uintptr(unsafe.Pointer(&pgrp)))
	if errno != 0 {
		return 0, errno
	}
	return int(pgrp), nil
}

// measureProbeTerminal runs the probe against the fake shell from a process
// that owns its terminal, and reports whether it still owns it afterwards.
func measureProbeTerminal() int {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		fmt.Println("measure: no controlling terminal:", err)
		return 2
	}
	defer tty.Close()
	before, err := ttyForegroundGroup(int(tty.Fd()))
	if err != nil {
		fmt.Println("measure: TIOCGPGRP:", err)
		return 2
	}
	if before != syscall.Getpgrp() {
		fmt.Printf("measure: not the foreground group to begin with: tty %d, self %d\n", before, syscall.Getpgrp())
		return 2
	}
	login := runsLoginScript(os.Getenv(launchshellFakeEnv))
	after, err := ttyForegroundGroup(int(tty.Fd()))
	if err != nil {
		fmt.Println("measure: TIOCGPGRP after:", err)
		return 2
	}
	fmt.Printf("probe verdict login=%v; foreground group before=%d after=%d self=%d\n", login, before, after, syscall.Getpgrp())
	if !login {
		return 1
	}
	if after != before {
		fmt.Println("terminal taken")
		return 1
	}
	fmt.Println(launchshellKeptMarker)
	return 0
}

// stealTerminal does what an interactive zsh's acquire_pgrp does when it is
// started with a controlling terminal it is not the foreground of: moves into
// a process group of its own and makes that the terminal's foreground group.
// With no controlling terminal there is nothing to take, and it says so.
func stealTerminal() int {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		fmt.Println("steal: no controlling terminal")
		return 0
	}
	defer tty.Close()
	// tcsetpgrp from a background group raises SIGTTOU, which would stop
	// this process rather than let it take the terminal.
	signal.Ignore(syscall.SIGTTOU)
	if err := syscall.Setpgid(0, 0); err != nil {
		fmt.Println("steal: setpgid:", err)
		return 0
	}
	pgrp := int32(syscall.Getpgrp())
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, tty.Fd(), uintptr(unix.TIOCSPGRP), uintptr(unsafe.Pointer(&pgrp)))
	if errno != 0 {
		fmt.Println("steal: TIOCSPGRP:", errno)
		return 0
	}
	fmt.Println("steal: took the terminal for group", pgrp)
	return 0
}

// The probe is an interactive shell, and an interactive zsh started with a
// controlling terminal it is not the foreground of takes that terminal
// (acquire_pgrp) and, on exit, hands it to the group it started in rather
// than the one that had it. The probe also runs inside `gate-inbox spawn`,
// and the MCP server that calls spawn is a child of the agent whose pane it
// lives in: that pane's terminal ended up with the MCP server's group, and
// the agent was stopped by SIGTTIN at its next read. So the probe must not
// have a controlling terminal at all. The fake shell here takes the terminal
// the way zsh does whenever it is given one; the run is measured from a
// process that owns a terminal, under script(1), because the test process
// itself has none to lose.
func TestLoginProbeLeavesTheCallersTerminalAlone(t *testing.T) {
	scriptBin, err := exec.LookPath("script")
	if err != nil {
		t.Skip("script(1) not installed; nothing here can own a terminal")
	}
	fake := writeFakeShell(t, "fakesteal",
		"#!/bin/sh\n"+
			launchshellHelperEnv+"=steal "+ShellQuote(os.Args[0])+" -test.run=XXX_NONE\n"+
			"if [ \"$1\" = -l ] && [ \"$2\" = -i ]; then shift 2; fi\n"+
			"exec /bin/sh \"$@\"\n")
	helper := ShellQuote(os.Args[0]) + " -test.run=XXX_NONE"
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command(scriptBin, "-q", "/dev/null", "/bin/sh", "-c", helper)
	} else {
		cmd = exec.Command(scriptBin, "-qec", helper, "/dev/null")
	}
	cmd.Env = append(tmuxtest.Environ(), launchshellHelperEnv+"=measure", launchshellFakeEnv+"="+fake)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), launchshellKeptMarker) {
		t.Fatalf("probe run under a terminal: %v\n%s", err, out)
	}
}

// writeFakeShell installs an executable at a path nothing else in the run
// shares, so its probe answer is cached under a key of its own.
func writeFakeShell(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("write fake shell: %v", err)
	}
	return path
}

// loginShellBody is a shell that behaves like the operator's: it exports
// something only its startup files would know, but only when it was started
// as a login, interactive one, and records every way it was called.
func loginShellBody(log, marker string) string {
	return "#!/bin/sh\n" +
		"echo \"$@\" >> " + log + "\n" +
		"if [ \"$1\" = -l ] && [ \"$2\" = -i ]; then\n" +
		"  shift 2\n" +
		"  GATE_INBOX_FAKE_RC=" + marker + "\n" +
		"  export GATE_INBOX_FAKE_RC\n" +
		"fi\n" +
		"exec /bin/sh \"$@\"\n"
}

// An agent has to start in the environment the operator gets from opening a
// terminal, which is the one their shell builds out of its startup files.
func TestLaunchRunsTheCommandUnderTheOperatorLoginShell(t *testing.T) {
	driver := requireTmux(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	shell := writeFakeShell(t, "fakelogin", loginShellBody(log, "from-the-rc-files"))
	t.Setenv("SHELL", shell)

	id := "login" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	marker := filepath.Join(dir, "env")
	if err := driver.Create(id, "/tmp", "printenv GATE_INBOX_FAKE_RC > "+marker, nil, 0, 0); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { driver.Kill(id) })

	if got := strings.TrimSpace(waitForFile(t, driver, id, marker)); got != "from-the-rc-files" {
		t.Fatalf("GATE_INBOX_FAKE_RC in pane = %q, want %q", got, "from-the-rc-files")
	}
}

// The shell the pane drops to when the agent exits is the operator's too,
// and just as much a login, interactive one: it is where they keep working.
func TestLaunchDropsToALoginInteractiveShell(t *testing.T) {
	driver := requireTmux(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	shell := writeFakeShell(t, "fakedrop", loginShellBody(log, "dropped"))
	t.Setenv("SHELL", shell)

	id := "drop" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	marker := filepath.Join(dir, "ran")
	if err := driver.Create(id, "/tmp", "echo ran > "+marker, nil, 0, 0); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { driver.Kill(id) })
	waitForFile(t, driver, id, marker)

	var last string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(log)
		if err == nil {
			calls := strings.Split(strings.TrimSpace(string(data)), "\n")
			last = calls[len(calls)-1]
			if last == "-l -i" {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("last call to the shell = %q, want %q: a bare login, interactive shell", last, "-l -i")
}

// An agent that gets stopped -- the terminal taken from under it, or a stray
// SIGTSTP -- comes back to the launch shell as a stopped job, not a finished
// one. The pane has to put it back in the foreground rather than drop to the
// operator's prompt and leave it stopped behind a session that reads as dead.
func TestLaunchResumesAnAgentThatWasStopped(t *testing.T) {
	driver := requireTmux(t)
	dir := t.TempDir()
	// Sourcing the script under an interactive sh gives the launch the job
	// control the operator's real login shell has.
	shell := writeFakeShell(t, "fakejobs",
		"#!/bin/sh\n"+
			"if [ \"$1\" = -l ] && [ \"$2\" = -i ]; then shift 2; exec /bin/sh -i \"$@\"; fi\n"+
			"exec /bin/sh \"$@\"\n")
	t.Setenv("SHELL", shell)

	id := "stop" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	marker := filepath.Join(dir, "resumed")
	agent := "sh -c 'kill -STOP $$; echo resumed > " + marker + "'"
	if err := driver.Create(id, "/tmp", agent, nil, 0, 0); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { driver.Kill(id) })

	if got := strings.TrimSpace(waitForFile(t, driver, id, marker)); got != "resumed" {
		t.Fatalf("stopped agent wrote %q, want %q: the launch left it stopped", got, "resumed")
	}
}

// A shell that cannot take the login flags still has to open a pane, so the
// launch falls back to the sh every pane once ran under.
func TestLaunchFallsBackWhenTheShellRefusesTheLoginFlags(t *testing.T) {
	driver := requireTmux(t)
	shell := writeFakeShell(t, "fakerefuse",
		"#!/bin/sh\ncase \"$1\" in -*) exit 3;; esac\nexec /bin/sh \"$@\"\n")
	t.Setenv("SHELL", shell)

	id := "fall" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	marker := filepath.Join(t.TempDir(), "ran")
	if err := driver.Create(id, "/tmp", "echo still-launched > "+marker, nil, 0, 0); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { driver.Kill(id) })

	if got := strings.TrimSpace(waitForFile(t, driver, id, marker)); got != "still-launched" {
		t.Fatalf("pane wrote %q, want %q", got, "still-launched")
	}
}

// fish and csh take -l and -i and then choke on `export`, so a shell is only
// trusted with the launch script once it has handed a token back through one.
func TestResolveLaunchShell(t *testing.T) {
	quiet := writeFakeShell(t, "fakequiet", "#!/bin/sh\nexit 0\n")
	honest := writeFakeShell(t, "fakehonest", "#!/bin/sh\nshift 2\nexec /bin/sh \"$@\"\n")
	missing := filepath.Join(t.TempDir(), "nothing-here")
	for _, tt := range []struct {
		name  string
		shell string
		want  launchShell
	}{
		{"unset", "", launchShell{path: "/bin/sh"}},
		{"runs the script", honest, launchShell{path: honest, flags: loginInteractive}},
		{"takes the flags but not the body", quiet, launchShell{path: quiet}},
		{"missing", missing, launchShell{path: missing}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SHELL", tt.shell)
			got := resolveLaunchShell()
			if got.path != tt.want.path || strings.Join(got.flags, " ") != strings.Join(tt.want.flags, " ") {
				t.Fatalf("resolveLaunchShell() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// The probe costs a whole startup-file chain, so it is asked once per shell
// however many sessions are created.
func TestLoginProbeIsCachedPerShell(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "probes")
	shell := writeFakeShell(t, "fakecount", "#!/bin/sh\necho probed >> "+log+"\nshift 2\nexec /bin/sh \"$@\"\n")
	t.Setenv("SHELL", shell)

	for range 3 {
		if got := resolveLaunchShell(); len(got.flags) == 0 {
			t.Fatalf("resolveLaunchShell() = %+v, want the login flags", got)
		}
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("read probe log: %v", err)
	}
	if got := len(strings.Fields(string(data))); got != 1 {
		t.Fatalf("probed the shell %d times, want 1", got)
	}
}
