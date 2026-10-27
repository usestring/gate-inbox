// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"errors"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/clipboard"
)

func TestBrowserCommandsHonorBrowserCandidates(t *testing.T) {
	target := "https://example.com/search?q=a b;still-an-argument"
	commands := browserCommands("linux", `missing:'/opt/Remote Browser/bin/open' --new-tab=%s:remote`, target)
	want := [][]string{
		{"missing", target},
		{"/opt/Remote Browser/bin/open", "--new-tab=" + target},
		{"remote", target},
		{"xdg-open", target},
	}
	if len(commands) != len(want) {
		t.Fatalf("commands = %d, want %d", len(commands), len(want))
	}
	for i := range commands {
		if !slices.Equal(commands[i].Args, want[i]) {
			t.Errorf("command %d = %q, want %q", i, commands[i].Args, want[i])
		}
	}
}

func TestOpenBrowserTriesCandidatesBeforeXDGOpen(t *testing.T) {
	var attempted []string
	err := openBrowserWith("linux", "missing:remote", "https://example.com", func(cmd *exec.Cmd) error {
		attempted = append(attempted, cmd.Args[0])
		if cmd.Args[0] == "remote" {
			return nil
		}
		return errors.New("not found")
	})
	if err != nil {
		t.Fatalf("openBrowserWith() error = %v", err)
	}
	if want := []string{"missing", "remote"}; !slices.Equal(attempted, want) {
		t.Fatalf("attempted %q, want %q", attempted, want)
	}
}

func TestOpenBrowserFallsBackToXDGOpen(t *testing.T) {
	var attempted []string
	err := openBrowserWith("linux", "missing", "https://example.com", func(cmd *exec.Cmd) error {
		attempted = append(attempted, cmd.Args[0])
		if cmd.Args[0] == "xdg-open" {
			return nil
		}
		return errors.New("not found")
	})
	if err != nil {
		t.Fatalf("openBrowserWith() error = %v", err)
	}
	if want := []string{"missing", "xdg-open"}; !slices.Equal(attempted, want) {
		t.Fatalf("attempted %q, want %q", attempted, want)
	}
}

func TestOpenBrowserReportsAllFailures(t *testing.T) {
	missingErr := errors.New("missing")
	xdgErr := errors.New("xdg failed")
	var attempted []string
	err := openBrowserWith("linux", "missing", "https://example.com", func(cmd *exec.Cmd) error {
		attempted = append(attempted, cmd.Args[0])
		if cmd.Args[0] == "xdg-open" {
			return xdgErr
		}
		return missingErr
	})
	if !errors.Is(err, missingErr) || !errors.Is(err, xdgErr) {
		t.Fatalf("openBrowserWith() error = %v, want both failures", err)
	}
	if want := []string{"missing", "xdg-open"}; !slices.Equal(attempted, want) {
		t.Fatalf("attempted %q, want %q", attempted, want)
	}
}

func TestOpenBrowserContinuesAfterRuntimeFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses Unix true and false commands")
	}
	var attempted []string
	err := openBrowserWith("linux", "false:true", "https://example.com", func(cmd *exec.Cmd) error {
		attempted = append(attempted, cmd.Args[0])
		return cmd.Run()
	})
	if err != nil {
		t.Fatalf("openBrowserWith() error = %v", err)
	}
	if want := []string{"false", "true"}; !slices.Equal(attempted, want) {
		t.Fatalf("attempted %q, want %q", attempted, want)
	}
}

func TestOpenBrowserFailureCopiesURL(t *testing.T) {
	m := footModel(t)
	m.width, m.height = 100, 34
	openBrowser = func(string) error { return errors.New("no browser available") }
	var copied string
	copyBrowserURL = func(target string) error {
		copied = target
		return nil
	}
	t.Cleanup(func() {
		openBrowser = defaultOpenBrowser
		copyBrowserURL = clipboard.WriteText
	})

	target := "https://example.com/manual"
	m.applyCmd(t, openLink(target))
	if copied != target {
		t.Fatalf("copied %q, want %q", copied, target)
	}
	if !strings.Contains(m.errBar.text, "URL copied") || !strings.Contains(m.errBar.text, "no browser available") {
		t.Fatalf("failure = %q, want copy outcome and cause", m.errBar.text)
	}
}

func TestOpenBrowserAndCopyFailureNamesURL(t *testing.T) {
	m := footModel(t)
	m.width, m.height = 100, 34
	openBrowser = func(string) error { return errors.New("no browser available") }
	copyBrowserURL = func(string) error { return errors.New("no clipboard available") }
	t.Cleanup(func() {
		openBrowser = defaultOpenBrowser
		copyBrowserURL = clipboard.WriteText
	})

	target := "https://example.com/manual"
	m.applyCmd(t, openLink(target))
	if !strings.Contains(m.errBar.text, target) || !strings.Contains(m.errBar.text, "no clipboard available") {
		t.Fatalf("failure = %q, want URL and copy cause", m.errBar.text)
	}
}

func localTerminal() bool { return false }

// overSSH puts the test on a terminal at the far end of an SSH session.
func overSSH(t *testing.T) {
	t.Helper()
	remoteTerminal = func() bool { return true }
	t.Cleanup(func() { remoteTerminal = localTerminal })
}

func stubLinkCopy(t *testing.T, copyErr error) *string {
	t.Helper()
	copied := new(string)
	copyBrowserURL = func(target string) error {
		*copied = target
		return copyErr
	}
	t.Cleanup(func() { copyBrowserURL = clipboard.WriteText })
	return copied
}

// Over SSH the link is copied and shown on the bar; the screen is never
// handed over and no browser starts on this host.
func TestRemoteLinkIsCopiedToTheBar(t *testing.T) {
	m := footModel(t)
	m.width, m.height = 140, 34
	overSSH(t)
	copied := stubLinkCopy(t, nil)
	openBrowser = func(string) error { t.Fatal("opened a browser on the remote host"); return nil }
	t.Cleanup(func() { openBrowser = defaultOpenBrowser })

	target := "https://example.com/docs?section=links&page=2"
	m.applyCmd(t, openLink(target))
	if *copied != target {
		t.Fatalf("copied %q, want %q", *copied, target)
	}
	if !m.errBar.worked() || !strings.Contains(m.errBar.text, target) {
		t.Fatalf("bar = %q (worked %v), want a done notice with the URL", m.errBar.text, m.errBar.worked())
	}
}

func TestRemoteLinkCopyFailureNamesURL(t *testing.T) {
	m := footModel(t)
	m.width, m.height = 140, 34
	overSSH(t)
	stubLinkCopy(t, errors.New("terminal clipboard unavailable"))

	target := "https://example.com/manual"
	m.applyCmd(t, openLink(target))
	if m.errBar.worked() || !strings.Contains(m.errBar.text, target) || !strings.Contains(m.errBar.text, "terminal clipboard unavailable") {
		t.Fatalf("bar = %q, want a failure with the URL and cause", m.errBar.text)
	}
}
