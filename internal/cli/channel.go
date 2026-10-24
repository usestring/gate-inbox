package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/usestring/gate-inbox/extension/cmdline"
	"github.com/usestring/gate-inbox/internal/gatechannel"
)

const usageChannel = `channel <lease|release|pending|answer> [flags]`

// RunChannel is the "channel" subcommand a gate channel consumer runs: a
// process outside the board, such as a voice front end, that answers held
// AskUserQuestion calls (gatechannel). Like hook it is machine-facing and left
// out of the help. Every verb prints one JSON value on success.
//
//	channel lease --consumer <name> [--token <t>] [--paused] [--hold 45s] [--ttl 10s]
//	channel release --token <t>
//	channel pending --token <t>
//	channel answer < answer.json
//
// It refuses to run as a managed session, so an agent cannot lease the
// channel and answer its own questions.
func RunChannel(in io.Reader, out io.Writer, args []string, sessionID, configDir string) error {
	if sessionID != "" {
		return errors.New("channel: a managed session cannot act as a gate channel consumer")
	}
	if len(args) == 0 {
		return fmt.Errorf("usage: %s %s", program, usageChannel)
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "lease":
		set := cmdline.NewFlagSet("channel lease --consumer <name> [--token <t>] [--paused] [--hold 45s] [--ttl 10s]")
		consumer := set.String("consumer", "", "the consumer's name")
		token := set.String("token", "", "the token the first lease printed; omit to take a new lease")
		paused := set.Bool("paused", false, "keep the lease but hold nothing: the consumer cannot deliver right now")
		hold := set.Duration("hold", 45*time.Second, "how long to hold one call")
		ttl := set.Duration("ttl", 10*time.Second, "how long this renewal lasts")
		if _, err := parseCommand(out, set, rest, 0, 0); err != nil {
			return err
		}
		lease, err := gatechannel.Renew(configDir, *consumer, *token, !*paused, *hold, *ttl, time.Now())
		if err != nil {
			return fmt.Errorf("channel lease: %w", err)
		}
		return printJSON(out, lease)
	case "release":
		set := cmdline.NewFlagSet("channel release --token <t>")
		token := set.String("token", "", "the lease's token")
		if _, err := parseCommand(out, set, rest, 0, 0); err != nil {
			return err
		}
		if err := gatechannel.Release(configDir, *token); err != nil {
			return fmt.Errorf("channel release: %w", err)
		}
		return printJSON(out, map[string]bool{"released": true})
	case "pending":
		set := cmdline.NewFlagSet("channel pending --token <t>")
		token := set.String("token", "", "the lease's token")
		if _, err := parseCommand(out, set, rest, 0, 0); err != nil {
			return err
		}
		holds, err := gatechannel.Pending(configDir, *token, time.Now())
		if err != nil {
			return fmt.Errorf("channel pending: %w", err)
		}
		return printJSON(out, holds)
	case "answer":
		set := cmdline.NewFlagSet("channel answer < answer.json")
		if _, err := parseCommand(out, set, rest, 0, 0); err != nil {
			return err
		}
		var answer gatechannel.Answer
		if err := json.NewDecoder(io.LimitReader(in, 1<<20)).Decode(&answer); err != nil {
			return fmt.Errorf("channel answer: the answer is not JSON: %w", err)
		}
		if err := gatechannel.Deliver(configDir, answer, gatechannel.RealClock); err != nil {
			return fmt.Errorf("channel answer: %w", err)
		}
		return printJSON(out, map[string]bool{"delivered": true})
	}
	return fmt.Errorf("channel: unknown verb %q; usage: %s %s", verb, program, usageChannel)
}

func printJSON(out io.Writer, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(raw))
	return err
}
