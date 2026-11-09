package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/usestring/gate-inbox/extension/cmdline"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
	"github.com/usestring/gate-inbox/internal/store"
)

const usagePROwner = "pr-owner <canonical-url> [--json]"
const usagePRFeedback = "pr-feedback <canonical-url> --event-key <key> --expect-session-id <id> --message-file <path> [--json]"

type prCommands interface {
	PROwner(string) (sessioncmd.PROwnership, error)
	PRFeedback(string, string, string, string) (store.PRDelivery, error)
}

func prSection() section {
	factory := func(dir string) prCommands { return sessioncmd.NewSessions(dir, sessioncmd.CLIVocabulary()) }
	return section{title: "Pull request custody", commands: []command{
		{name: "pr-owner", usage: usagePROwner, about: "resolve recorded PR custody to one live session; never revive or infer ownership from mentions", run: bind(factory, runPROwner)},
		{name: "pr-feedback", usage: usagePRFeedback, about: "queue feedback once for a proven PR owner; acceptance is durable, delivery uses normal inbox guards", run: bind(factory, runPRFeedback)},
	}}
}

func runPROwner(out io.Writer, commands prCommands, args []string, _ string) error {
	set := cmdline.NewFlagSet(usagePROwner)
	asJSON := cmdline.JSONFlag(set)
	pos, err := parseCommand(out, set, args, 1, 1)
	if err != nil {
		return err
	}
	result, err := commands.PROwner(pos[0])
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, result, result.Status+" "+result.SessionID)
}

func runPRFeedback(out io.Writer, commands prCommands, args []string, _ string) error {
	set := cmdline.NewFlagSet(usagePRFeedback)
	key := set.String("event-key", "", "durable event identity, 1-128 letters, digits, underscores or hyphens")
	expected := set.String("expect-session-id", "", "resolved owner; a changed owner refuses a new delivery")
	path := set.String("message-file", "", "UTF-8 feedback file, at most 8000 bytes")
	asJSON := cmdline.JSONFlag(set)
	pos, err := parseCommand(out, set, args, 1, 1)
	if err != nil {
		return err
	}
	if *path == "" || *key == "" || *expected == "" {
		return fmt.Errorf("event-key, expect-session-id and message-file are required")
	}
	file, err := os.Open(*path)
	if err != nil {
		return err
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, 8001))
	if err != nil {
		return err
	}
	if len(body) > 8000 {
		return fmt.Errorf("PR feedback exceeds 8000 bytes")
	}
	result, err := commands.PRFeedback(pos[0], *key, *expected, string(body))
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, result, fmt.Sprintf("%s message %d for %s", result.Status, result.MessageID, result.SessionID))
}
