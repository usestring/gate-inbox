// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package deps

import (
	"errors"
	"strings"
	"testing"
)

func stubUID(t *testing.T, uid int) {
	t.Helper()
	original := getUID
	getUID = func() int { return uid }
	t.Cleanup(func() { getUID = original })
}

func stubPath(t *testing.T, present ...string) {
	t.Helper()
	found := make(map[string]bool, len(present))
	for _, name := range present {
		found[name] = true
	}
	original := lookPath
	lookPath = func(name string) (string, error) {
		if found[name] {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() { lookPath = original })
}

func TestInstallCommandPrefersNativeManagerOverBrew(t *testing.T) {
	stubUID(t, 1)
	stubPath(t, "brew", "apt-get", "sudo")
	if got, want := installCommand("linux", "tmux"), "sudo apt-get update && sudo apt-get install -y tmux"; got != want {
		t.Fatalf("installCommand = %q, want %q", got, want)
	}
}

func TestInstallCommandOnDarwinUsesBrew(t *testing.T) {
	stubPath(t, "brew", "apt-get")
	if got, want := installCommand("darwin", "git"), "brew install git"; got != want {
		t.Fatalf("installCommand = %q, want %q", got, want)
	}
}

func TestHintFallsBackWithoutAKnownManager(t *testing.T) {
	stubPath(t)
	if got, want := hint("linux", "tmux"), "install tmux with your package manager"; got != want {
		t.Fatalf("hint = %q, want %q", got, want)
	}
}

func TestHintNamesTheCommand(t *testing.T) {
	stubUID(t, 1)
	stubPath(t, "pacman", "sudo")
	if got, want := hint("linux", "tmux"), "install it with: sudo pacman -S --needed tmux"; got != want {
		t.Fatalf("hint = %q, want %q", got, want)
	}
}

func TestInstallCommandRootDropsSudo(t *testing.T) {
	stubUID(t, 0)
	stubPath(t, "apt-get")
	if got, want := installCommand("linux", "tmux"), "apt-get update && apt-get install -y tmux"; got != want {
		t.Fatalf("installCommand = %q, want %q", got, want)
	}
}

func TestInstallCommandSkipsAptWithoutSudo(t *testing.T) {
	stubUID(t, 1)
	stubPath(t, "apt-get", "brew")
	if got, want := installCommand("linux", "tmux"), "brew install tmux"; got != want {
		t.Fatalf("installCommand = %q, want %q", got, want)
	}
}

func TestInstallCommandApkAsRootHasNoSudo(t *testing.T) {
	stubUID(t, 0)
	stubPath(t, "apk")
	if got, want := installCommand("linux", "tmux"), "apk add tmux"; got != want {
		t.Fatalf("installCommand = %q, want %q", got, want)
	}
}

func TestInstallCommandApkAsNonRootTakesSudo(t *testing.T) {
	stubUID(t, 1)
	stubPath(t, "apk", "sudo")
	if got, want := installCommand("linux", "tmux"), "sudo apk add tmux"; got != want {
		t.Fatalf("installCommand = %q, want %q", got, want)
	}
}

func TestInstallCommandSkipsApkWithoutSudo(t *testing.T) {
	stubUID(t, 1)
	stubPath(t, "apk")
	if got := installCommand("linux", "tmux"); got != "" {
		t.Fatalf("installCommand = %q, want empty", got)
	}
}

func TestHintPrefersOfficialInstallerOverPackageManager(t *testing.T) {
	stubPath(t, "brew")
	got := hint("darwin", "claude")
	if !strings.Contains(got, "npm install -g @anthropic-ai/claude-code") {
		t.Fatalf("hint = %q, want the official package", got)
	}
	if strings.Contains(got, "brew install") {
		t.Fatalf("official installer must win over brew, got %q", got)
	}
}

func TestOfficialInstallIsPortable(t *testing.T) {
	if len(official) == 0 {
		t.Fatal("no official installers")
	}
	for name, got := range official {
		if strings.Contains(got, "brew") || strings.Contains(got, "apt") || strings.Contains(got, "winget") {
			t.Errorf("%s install is OS-specific: %s", name, got)
		}
	}
}

// The opencode-ai npm package is still the 1.x line, which the board does not
// support; a fresh install goes straight to the v2 package.
func TestHintInstallsOpencodeV2(t *testing.T) {
	stubPath(t, "brew")
	got := hint("linux", "opencode")
	if !strings.Contains(got, "npm install -g @opencode/cli") {
		t.Fatalf("hint = %q, want the v2 package", got)
	}
}

// pi is a built-in tool, and no package manager ships it under that name: a
// guessed `apt-get install pi` installs something else or nothing.
func TestHintInstallsPiFromItsOwnPackage(t *testing.T) {
	stubPath(t, "apt-get")
	got := hint("linux", "pi")
	if !strings.Contains(got, "@earendil-works/pi-coding-agent") || strings.Contains(got, "apt-get") {
		t.Fatalf("hint = %q, want the pi package", got)
	}
}

func TestCommandIsTheVendorInstaller(t *testing.T) {
	if got := Command("claude"); got != official["claude"] {
		t.Fatalf("Command = %q, want the official installer", got)
	}
}

// A package-manager line names the tool's own command, so running one for
// a tool we do not know would install whatever package shares that name.
// The hint still says it; only the runnable command is withheld.
func TestCommandWithholdsPackageManagerLines(t *testing.T) {
	stubUID(t, 1)
	stubPath(t, "pacman", "sudo")
	for _, tool := range []string{"tmux", "acme"} {
		if got := Command(tool); got != "" {
			t.Fatalf("Command(%q) = %q, want none", tool, got)
		}
	}
	if got := hint("linux", "tmux"); !strings.Contains(got, "pacman") {
		t.Fatalf("hint = %q, want the package manager still suggested", got)
	}
}

// No install hint or offered command may download a script and pipe it into
// a shell: a built-in CLI installs from its vendor's package or page.
func TestNoPipedInstallScripts(t *testing.T) {
	for name, got := range official {
		if strings.Contains(got, "|") || strings.Contains(got, "curl") || strings.Contains(got, "wget") {
			t.Errorf("%s install pipes a remote script: %s", name, got)
		}
		if !strings.HasPrefix(got, "npm install -g ") && !strings.HasPrefix(got, "bun install -g ") {
			t.Errorf("%s install is not a package install: %s", name, got)
		}
	}
}

// omp's package runs on Bun (it imports bun:sqlite), so it installs with bun
// rather than npm, and never through omp.sh's piped installer.
func TestOmpInstallsWithBun(t *testing.T) {
	if got := Hint("omp"); got != "install it with: bun install -g @oh-my-pi/pi-coding-agent" {
		t.Fatalf("Hint = %q", got)
	}
}

// muse publishes no package, so the board links the vendor's page and offers
// nothing to run.
func TestMuseLinksItsInstallPage(t *testing.T) {
	stubPath(t, "brew")
	if got := hint("darwin", "muse"); got != "install it from https://dev.meta.ai" {
		t.Fatalf("hint = %q, want the vendor page", got)
	}
	if got := Command("muse"); got != "" {
		t.Fatalf("Command = %q, want none to run", got)
	}
}
