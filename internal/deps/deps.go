// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

// Package deps reports how to install the external tools Gate Inbox runs,
// so a missing binary names the command that fixes it instead of a bare
// "not found in PATH". Agent CLIs install from the vendor's npm package
// (or the vendor's install page when there is none); tmux and git use the
// package manager on this machine. Nothing here downloads a script and
// pipes it into a shell.
package deps

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

var lookPath = exec.LookPath
var getUID = os.Getuid

type manager struct {
	bin     string
	install string
}

// Native package managers come before brew on Linux, where a Homebrew install
// is the exception; on macOS brew is the only one of these that ships packages.
var linuxManagers = []manager{
	{"apt-get", "sudo apt-get update && sudo apt-get install -y "},
	{"dnf", "sudo dnf install -y "},
	{"pacman", "sudo pacman -S --needed "},
	{"zypper", "sudo zypper install -y "},
	{"apk", "sudo apk add "},
	{"brew", "brew install "},
}

var darwinManagers = []manager{
	{"brew", "brew install "},
}

// official is the vendor's npm package install for a built-in agent CLI. It
// is the same command on macOS, Linux, and WSL, and it wins over a package
// manager that would guess `brew install claude`. Each name is the exact
// package the vendor publishes (and that its own installer downloads), never
// a piped install script. opencode's is the v2 package: the older
// opencode-ai package is the 1.x line, which the board does not support
// (opencode.Report.UpgradeNotice).
var official = map[string]string{
	"claude":   "npm install -g @anthropic-ai/claude-code",
	"cmd":      "npm install -g command-code",
	"codex":    "npm install -g @openai/codex",
	"opencode": "npm install -g @opencode/cli",
	"pi":       "npm install -g @earendil-works/pi-coding-agent",
}

// installPage is where a built-in agent CLI with no package-manager release
// says to install it from. The board links it rather than running the
// vendor's piped install script, so the dialog has no command to offer.
var installPage = map[string]string{
	"muse": "https://dev.meta.ai",
}

func Hint(tool string) string {
	return hint(runtime.GOOS, tool)
}

// Command is the install command the setup dialog offers to run, empty
// when there is none to offer. Only the agent CLIs with a vendor
// npm package have one: a package-manager line is built from the tool's
// own command name, so for a custom tool it would install whatever
// package happens to share that name. Those stay a suggestion to read.
func Command(tool string) string {
	return official[tool]
}

func hint(goos, tool string) string {
	if command := official[tool]; command != "" {
		return "install it with: " + command
	}
	if page := installPage[tool]; page != "" {
		return "install it from " + page
	}
	if command := installCommand(goos, tool); command != "" {
		return "install it with: " + command
	}
	return "install " + tool + " with your package manager"
}

func installCommand(goos, tool string) string {
	candidates := linuxManagers
	if goos == "darwin" {
		candidates = darwinManagers
	}
	for _, candidate := range candidates {
		if _, err := lookPath(candidate.bin); err != nil {
			continue
		}
		command := privilegedInstall(candidate.install)
		if command == "" {
			continue
		}
		return command + tool
	}
	return ""
}

func privilegedInstall(command string) string {
	if !strings.Contains(command, "sudo ") {
		return command
	}
	if getUID() == 0 {
		return strings.ReplaceAll(command, "sudo ", "")
	}
	if _, err := lookPath("sudo"); err == nil {
		return command
	}
	return ""
}
