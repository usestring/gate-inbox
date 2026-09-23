// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

const usageRename = `rename "<name>"`

func renameSection() section {
	return section{
		title: "Your own session",
		commands: []command{
			{name: "rename", usage: usageRename, about: "name this session for the broad feature it is about, once, while it still carries a placeholder name", run: configCommand(runRename)},
		},
	}
}

func runRename(out io.Writer, args []string, sessionID, configDir string) error {
	set := newFlagSet(usageRename)
	operands, err := parseCommand(out, set, args, 1, 1)
	if err != nil {
		return err
	}
	name, err := nonBlank(usageRename, operands[0])
	if err != nil {
		return err
	}
	message, err := sessioncmd.Rename(configDir, sessionID, name)
	return printMessage(out, message, err)
}

// A blank operand is a mis-quoted argument rather than a value, so it reads
// as the usage error it is instead of clearing what it meant to set.
func nonBlank(usage, value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", usageError(usage)
	}
	return value, nil
}

func printMessage(out io.Writer, message string, err error) error {
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, message)
	return err
}
