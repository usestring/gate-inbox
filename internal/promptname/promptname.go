// Package promptname names a session from the first prompt it was given, by
// asking a small model from outside the session.
//
// It replaces asking the agent to name itself. That cost a turn of the agent's
// own context and attention on housekeeping, arrived where the user's typing
// goes, and a codex agent had to stop for an approval dialog to run it. The
// opening prompt is the whole input on purpose: it is what the session was
// started for, and it is in hand before the agent has done anything.
package promptname

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// MaxPromptChars is how much of the opening prompt the model is shown. The
// theme of a session is in its first lines; a pasted log after them only
// costs tokens.
const MaxPromptChars = 1000

// DefaultCommand is the cheapest call that needs no key of its own: Claude
// Code in print mode on Haiku, with no tools, MCP servers, settings, hooks,
// slash commands or saved session. Skipping persistence is load-bearing, not
// tidiness: a saved transcript would be a conversation the naming pass then
// tries to attribute to a pane.
var DefaultCommand = []string{
	"claude", "-p",
	"--model", "haiku",
	"--no-session-persistence",
	"--tools", "",
	"--strict-mcp-config",
	"--setting-sources", "",
	"--disable-slash-commands",
}

// timeout bounds one call. A measured call takes about six seconds.
const timeout = 45 * time.Second

const instruction = `Name this coding-agent session with a short 2-4 word kebab-case name for its broad feature or theme, judged only from its first prompt below. Reply with the name and nothing else.

FIRST PROMPT:
`

// runner runs command with stdin and returns its stdout. A test replaces it.
type runner func(ctx context.Context, command []string, stdin string) (string, error)

// Namer asks a model for a session name.
type Namer struct {
	command []string
	run     runner
}

// New returns a namer running command, or DefaultCommand when it is empty.
func New(command []string) *Namer {
	if len(command) == 0 {
		command = DefaultCommand
	}
	return &Namer{command: command, run: runCommand}
}

// Name returns the model's name for a session opened with prompt. The answer
// is the model's raw line; the caller kebab-cases it with the rest of the
// board's names so collisions are broken the same way.
func (n *Namer) Name(ctx context.Context, prompt string) (string, error) {
	prompt = Truncate(prompt)
	if prompt == "" {
		return "", errors.New("no prompt to name the session from")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := n.run(ctx, n.command, instruction+prompt)
	if err != nil {
		return "", err
	}
	name := lastLine(out)
	if name == "" {
		return "", errors.New("namer returned nothing")
	}
	return name, nil
}

// Truncate cuts prompt to MaxPromptChars runes.
func Truncate(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if runes := []rune(prompt); len(runes) > MaxPromptChars {
		prompt = string(runes[:MaxPromptChars])
	}
	return prompt
}

func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.Trim(strings.TrimSpace(lines[len(lines)-1]), "`\"'")
}

// runCommand runs from the temporary directory so the CLI discovers no
// project instructions to load, which is most of what a call would cost.
func runCommand(ctx context.Context, command []string, stdin string) (string, error) {
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = os.TempDir()
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w: %s", command[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
