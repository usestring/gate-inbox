package app

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

func runHost(args []string, dir string) error {
	worker := sessioncmd.NewSessions(dir, sessioncmd.CLIVocabulary())
	switch {
	case len(args) == 1 && args[0] == "sessions":
		list, err := worker.HostSessions()
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(list)
	case len(args) == 2 && args[0] == "attach":
		return worker.AttachHost(args[1])
	default:
		return fmt.Errorf("usage: %s host sessions | host attach <session-id>", Name)
	}
}
