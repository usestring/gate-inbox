package communications

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/usestring/gate-inbox/extension"
)

type Extension struct {
	cfg     extension.Config
	enabled bool
}

func New() *Extension { return &Extension{} }
func (e *Extension) Descriptor() extension.Descriptor {
	return extension.Descriptor{ID: "communications", Version: "1"}
}
func (e *Extension) Configure(cfg extension.Config) error {
	var settings struct {
		Enabled bool `toml:"enabled"`
	}
	if err := cfg.Decode(&settings); err != nil {
		return err
	}
	e.cfg, e.enabled = cfg, settings.Enabled
	return nil
}

func (e *Extension) Commands() []extension.Command {
	return []extension.Command{{Name: "communications", Usage: "communications <import|list|read|mark-read|draft>", About: "Import conversations and prepare local replies for Slack, email, and SMS", Run: func(ctx context.Context, args []string, _ extension.Host) error {
		return e.run(ctx, args, os.Stdin, os.Stdout)
	}}}
}

func (e *Extension) run(ctx context.Context, args []string, input io.Reader, output io.Writer) error {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		_, err := fmt.Fprintln(output, "Usage: gate-inbox communications <import|list|read|mark-read|draft>\n\n  import                    Read a JSON message array from stdin\n  list                      List conversations as JSON\n  read <id>                 Read a conversation and its drafts\n  mark-read <id> <revision>  Mark the reviewed revision read\n  draft                     Read conversation_id, revision, body from stdin\n\nDrafts stay local. Enable [extensions.communications] with enabled = true.")
		if err != nil {
			return err
		}
		return flag.ErrHelp
	}
	if !e.enabled {
		return errors.New("enable [extensions.communications] with enabled = true first")
	}
	if len(args) == 0 {
		return errors.New("usage: communications <import|list|read|mark-read|draft>")
	}
	dir, err := e.cfg.DataDir()
	if err != nil {
		return err
	}
	store, err := Open(dir)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var result any
	switch args[0] {
	case "import":
		if len(args) != 1 {
			return errors.New("usage: communications import < messages.json")
		}
		var messages []Message
		if err := decode(input, &messages); err != nil {
			return err
		}
		count, err := store.Import(ctx, messages)
		if err != nil {
			return err
		}
		result = struct {
			Imported int `json:"imported"`
		}{count}
	case "list":
		if len(args) != 1 {
			return errors.New("usage: communications list")
		}
		result, err = store.List(ctx)
	case "read":
		if len(args) != 2 {
			return errors.New("usage: communications read <conversation-id>")
		}
		result, err = store.Read(ctx, args[1])
	case "mark-read":
		if len(args) != 3 {
			return errors.New("usage: communications mark-read <conversation-id> <revision>")
		}
		err = store.MarkRead(ctx, args[1], args[2])
		result = struct {
			Read bool `json:"read"`
		}{true}
	case "draft":
		if len(args) != 1 {
			return errors.New("usage: communications draft < reply.json")
		}
		var request struct {
			ConversationID string `json:"conversation_id"`
			Revision       string `json:"revision"`
			Body           string `json:"body"`
		}
		if err := decode(input, &request); err != nil {
			return err
		}
		result, err = store.Draft(ctx, request.ConversationID, request.Revision, request.Body)
	default:
		return fmt.Errorf("unknown communications command %q", args[0])
	}
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func decode(input io.Reader, value any) error {
	data, err := io.ReadAll(io.LimitReader(input, (8<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 8<<20 {
		return errors.New("communications input exceeds 8 MiB")
	}
	return json.Unmarshal(data, value)
}
