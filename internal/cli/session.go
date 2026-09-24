// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/usestring/gate-inbox/extension/cmdline"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

const (
	usageSessions      = "sessions [--parent <id|me>] [--status <state>] [--include-archived] [--limit <n>] [--json]"
	usageSpawn         = "spawn [--name <name>] [--prompt <text>] [--tool <cli>] [--model <model>] [--group <path>] [--directory <path>] [--nest] [--json]"
	usageSend          = `send <session-id> "<message>" [--subject <label>] [--interrupt] [--as-human] [--json]`
	usageRead          = "read <session-id> [--since <cursor>] [--json]"
	usageSendChildren  = "send-children \"<message>\" [--json]"
	usagePlace         = "place <session-id> [--release] [--json]"
	usageAnswer        = "answer <session-id> \"<answer>\" [--json]"
	usageWait          = "wait [<session-id>...] [--children] [--until <state>] [--timeout <duration>] [--json]"
	usageMessageStatus = "message-status <message-id> [--json]"
	usageKill          = "kill <session-id> [--json]"
	usageRevive        = "revive <session-id> [--json]"
	usageMigrate       = "migrate <session-id> --tool <cli> [--name <name>] [--json]"
	usageArchive       = "archive <session-id> [--restore] [--json]"
	usagePark          = "park [--dry-run] [--json]"
	usageUnpark        = "unpark [--json]"
	usageGroups        = "groups [--json]"
	usageCreateGroup   = "create-group <path> [--directory <path>] [--json]"
	usageDeleteGroup   = "delete-group <path> [--json]"
)

type sessionCommands interface {
	List(sessionID string, opts sessioncmd.ListOptions) (sessioncmd.SessionList, error)
	Create(sessionID string, opts sessioncmd.CreateSessionOptions) (sessioncmd.Session, error)
	Send(sessionID, targetID, message, subject string, interrupt bool) (sessioncmd.SendResult, error)
	SendChildren(sessionID, message string) (sessioncmd.ChildSend, error)
	SendAsHuman(sessionID, targetID, message, subject string, interrupt bool) (sessioncmd.SendResult, error)
	Read(sessionID, targetID, since string) (sessioncmd.SessionScreen, error)
	AdoptSession(sessionID, targetID string) (sessioncmd.Session, error)
	ReleaseSession(sessionID, targetID string) (sessioncmd.Session, error)
	Answer(sessionID, targetID, reply string) (sessioncmd.AnsweredQuestion, error)
	Wait(ctx context.Context, sessionID string, opts sessioncmd.WaitOptions) (sessioncmd.WaitResult, error)
	MessageStatus(sessionID string, messageID int64) (sessioncmd.MessageState, error)
	Kill(sessionID, targetID string) (sessioncmd.Session, error)
	Revive(sessionID, targetID string) (sessioncmd.Session, error)
	Migrate(sessionID, targetID string, opts sessioncmd.MigrateOptions) (sessioncmd.Session, error)
	Archive(sessionID, targetID string, archived bool) (sessioncmd.Session, error)
	Park(sessionID string, dryRun bool) (sessioncmd.ParkResult, error)
	Unpark(sessionID string) (sessioncmd.UnparkResult, error)
	Groups(sessionID string) ([]sessioncmd.Group, error)
	CreateGroup(sessionID, path, directory string) (sessioncmd.Group, error)
	DeleteGroup(sessionID, path string) (sessioncmd.GroupRemoval, error)
}

func newSessions(configDir string) sessionCommands {
	return sessioncmd.NewSessions(configDir, sessioncmd.CLIVocabulary())
}

func sessionSection() section {
	return section{
		title: "Agent sessions",
		commands: []command{
			{name: "sessions", usage: usageSessions, about: "list every agent session with its id, CLI, group, directory and status; call it before delegating anything", run: bind(newSessions, runSessions)},
			{name: "spawn", usage: usageSpawn, about: "start another agent CLI on a task of its own, so independent work runs beside you instead of queued behind you", run: bind(newSessions, runSpawn)},
			{name: "send", usage: usageSend, about: "queue a message for another agent; it is typed in once that agent is at rest, so it never lands on an approval prompt", run: bind(newSessions, runSend)},
			{name: "read", usage: usageRead, about: "read what another agent is doing: its status, any question it is holding, its last words, and either its whole screen or only what it has added --since a previous read's cursor", run: bind(newSessions, runRead)},
			{name: "send-children", usage: usageSendChildren, about: "queue one message for every session you spawned, for a correction about the work rather than about one worker", run: bind(newSessions, runSendChildren)},
			{name: "place", usage: usagePlace, about: "file a session under this one when a spawn of yours landed with no parent, or --release one of your own children back to the top level", run: bind(newSessions, runPlace)},
			{name: "answer", usage: usageAnswer, about: "answer a question one of the sessions you spawned has stopped on, a question at a time; send queues a message and a session on a dialog never reads it", run: bind(newSessions, runAnswer)},
			{name: "wait", usage: usageWait, about: "park until a session stops working, instead of reading its screen in a loop; name several or pass --children to park on a whole fan-out and return on the first one to arrive; exits non-zero when none of them did", run: bind(newSessions, runWait)},
			{name: "message-status", usage: usageMessageStatus, about: "check whether a message you sent is queued, held, delivered, dropped or answered", run: bind(newSessions, runMessageStatus)},
			{name: "kill", usage: usageKill, about: "stop another agent's process, ending whatever it is doing; its row keeps the last screen", run: bind(newSessions, runKill)},
			{name: "revive", usage: usageRevive, about: "bring a dead session back on its old row, resuming the conversation it held", run: bind(newSessions, runRevive)},
			{name: "migrate", usage: usageMigrate, about: "move a session's conversation to another agent CLI: a new session there reads the source's transcript and carries on; the source stays until you archive it", run: bind(newSessions, runMigrate)},
			{name: "archive", usage: usageArchive, about: "file a finished session out of the active list, ending it if it is still running, or restore it with --restore; a row left archived is deleted for good after 7 days", run: bind(newSessions, runArchive)},
			{name: "park", usage: usagePark, about: "stop every live agent session the manager started and record the set, so the machine can reboot; --dry-run only prints the plan; runs from any shell", run: bind(newSessions, runPark)},
			{name: "unpark", usage: usageUnpark, about: "bring back every session park stopped, resuming the conversation each held; runs from any shell", run: bind(newSessions, runUnpark)},
			{name: "groups", usage: usageGroups, about: "list the groups sessions and terminals are filed under", run: bind(newSessions, runGroups)},
			{name: "create-group", usage: usageCreateGroup, about: "create a group so a fleet you spawn stays together in the user's list", run: bind(newSessions, runCreateGroup)},
			{name: "delete-group", usage: usageDeleteGroup, about: "remove a group whose work is done; sessions still in it move to the root rather than stopping", run: bind(newSessions, runDeleteGroup)},
		},
	}
}

func runSessions(out io.Writer, sessions sessionCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usageSessions)
	var states stringList
	parent := set.String("parent", "", `keep only the sessions one spawned; "`+sessioncmd.SelfParent+`" is this session's own children`)
	set.Var(&states, "status", "state to keep, repeatable or comma separated: starting, working, waiting, finished, idle, errored or dead")
	includeArchived := set.Bool("include-archived", false, "also list rows archived out of the active list, which on an old board are most of them")
	limit := set.Int("limit", sessioncmd.DefaultSessionLimit, fmt.Sprintf("how many rows to print, at most %d; the last line says what was left out", sessioncmd.MaxSessionLimit))
	asJSON := cmdline.JSONFlag(set)
	if _, err := parseCommand(out, set, args, 0, 0); err != nil {
		return err
	}
	list, err := sessions.List(sessionID, sessioncmd.ListOptions{
		Parent:          *parent,
		Status:          states,
		IncludeArchived: *includeArchived,
		Limit:           *limit,
	})
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, list, sessioncmd.FormatSessionList(list))
}

func runSpawn(out io.Writer, sessions sessionCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usageSpawn)
	name := set.String("name", "", "kebab-case name naming the work it will do; the new agent names itself when this is empty")
	prompt := set.String("prompt", "", "first task to hand it, written as a full instruction, since it cannot see your conversation")
	tool := set.String("tool", "", "agent CLI to run; defaults to the CLI this session runs")
	model := set.String("model", "", "model that CLI should run on, in its own names; omit for the CLI's default")
	account := set.String("account", "", "named subscription it runs on, read from Secret Manager at launch; omit for the board's default account")
	group := set.String("group", "", "existing group path for a detached (--nest=false) session; a nested one is always in yours")
	directory := set.String("directory", "", "existing directory it works in; defaults to yours, or to the group's inherited path")
	nest := set.Bool("nest", true, "file it under this session, where its questions and rests reach you; --nest=false detaches it, for work that is not yours")
	asJSON := cmdline.JSONFlag(set)
	if _, err := parseCommand(out, set, args, 0, 0); err != nil {
		return err
	}
	opts := sessioncmd.CreateSessionOptions{
		Tool:      *tool,
		Name:      *name,
		Directory: *directory,
		Prompt:    *prompt,
		Model:     *model,
		Account:   *account,
	}
	// An omitted group inherits this session's and an omitted nest the
	// engine's own, so only a flag the caller actually typed is passed on.
	set.Visit(func(given *flag.Flag) {
		switch given.Name {
		case "group":
			opts.Group = group
		case "nest":
			opts.Nest = nest
		}
	})
	created, err := sessions.Create(sessionID, opts)
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, created, "created "+sessioncmd.FormatSession(created))
}

// agentEnv is set by Claude Code inside the shell it runs tool calls in, and
// not by a person's own shell. It is the only signal available that tells an
// agent's call apart from a person's, and a soft one on purpose: it stops an
// agent claiming the operator's voice in the ordinary course of its work, not
// one set on defeating it.
const agentEnv = "CLAUDECODE"

func runSend(out io.Writer, sessions sessionCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usageSend)
	subject := set.String("subject", "",
		"label for what this message is about; your next message to the same session under the same label replaces this one while it is still unread")
	interrupt := set.Bool("interrupt", false,
		"stop the session's running turn first so this is its next turn; refused for a CLI with no interrupt_keys, and never sent over a dialog")
	asHuman := set.Bool("as-human", false,
		"deliver as your own words rather than fenced as a message from another agent; for a person at a terminal, and refused from an agent's shell")
	asJSON := cmdline.JSONFlag(set)
	operands, err := parseCommand(out, set, args, 2, 2)
	if err != nil {
		return err
	}
	send := sessions.Send
	if *asHuman {
		if os.Getenv(agentEnv) != "" {
			return fmt.Errorf("--as-human delivers a message as the operator's own words, which is not an agent's to claim: "+
				"send it without the flag, or run it from your own shell (%s is set here)", agentEnv)
		}
		send = sessions.SendAsHuman
	}
	result, err := send(sessionID, operands[0], operands[1], *subject, *interrupt)
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, result, sessioncmd.FormatSendResult(result, operands[0]))
}

func runSendChildren(out io.Writer, sessions sessionCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usageSendChildren)
	asJSON := cmdline.JSONFlag(set)
	operands, err := parseCommand(out, set, args, 1, 1)
	if err != nil {
		return err
	}
	sent, err := sessions.SendChildren(sessionID, operands[0])
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, sent, sessioncmd.FormatChildSend(sent))
}

func runPlace(out io.Writer, sessions sessionCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usagePlace)
	release := set.Bool("release", false, "take one of this session's own children back out to the top level")
	asJSON := cmdline.JSONFlag(set)
	operands, err := parseCommand(out, set, args, 1, 1)
	if err != nil {
		return err
	}
	place := sessions.AdoptSession
	if *release {
		place = sessions.ReleaseSession
	}
	placed, err := place(sessionID, operands[0])
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, placed, sessioncmd.FormatPlacement(placed))
}

func runAnswer(out io.Writer, sessions sessionCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usageAnswer)
	asJSON := cmdline.JSONFlag(set)
	operands, err := parseCommand(out, set, args, 2, 2)
	if err != nil {
		return err
	}
	answered, err := sessions.Answer(sessionID, operands[0], operands[1])
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, answered, sessioncmd.FormatAnswer(answered))
}

func runRead(out io.Writer, sessions sessionCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usageRead)
	since := set.String("since", "", "cursor from a previous read of this session; returns only what it has added since, instead of the whole pane")
	asJSON := cmdline.JSONFlag(set)
	operands, err := parseCommand(out, set, args, 1, 1)
	if err != nil {
		return err
	}
	screen, err := sessions.Read(sessionID, operands[0], *since)
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, screen, sessioncmd.FormatSessionScreen(screen))
}

// runWait exits non-zero when no waited-on session reached an awaited state,
// so `gate-inbox wait <id> && next-step` reads the outcome the way a shell
// caller expects. The result still goes out first, JSON included.
func runWait(out io.Writer, sessions sessionCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usageWait)
	var until stringList
	set.Var(&until, "until", "state that ends the wait, repeatable or comma separated; defaults to every state meaning the session stopped working")
	children := set.Bool("children", false, "park on every session you spawned instead of naming ids; the first to arrive ends the wait and the result carries the whole set")
	timeout := set.Duration("timeout", 0, "how long to wait before giving up, default "+sessioncmd.DefaultWaitTimeout.String()+", maximum "+sessioncmd.MaxWaitTimeout.String())
	asJSON := cmdline.JSONFlag(set)
	operands, err := parseCommand(out, set, args, 0, anyNumber)
	if err != nil {
		return err
	}
	// The layer refuses this too, but a shell caller that named nothing
	// wants the usage line, which is where --children is written down.
	if len(operands) == 0 && !*children {
		return usageError(set.Name())
	}
	result, err := sessions.Wait(context.Background(), sessionID, sessioncmd.WaitOptions{
		SessionIDs: operands,
		Children:   *children,
		Until:      until,
		Timeout:    *timeout,
	})
	if err != nil {
		return err
	}
	human := sessioncmd.FormatWaitResult(result)
	if !result.Reached {
		if *asJSON {
			if err := cmdline.WriteJSON(out, result); err != nil {
				return err
			}
		}
		return errors.New(human)
	}
	return cmdline.Emit(out, *asJSON, result, human)
}

func runMessageStatus(out io.Writer, sessions sessionCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usageMessageStatus)
	asJSON := cmdline.JSONFlag(set)
	operands, err := parseCommand(out, set, args, 1, 1)
	if err != nil {
		return err
	}
	messageID, err := strconv.ParseInt(operands[0], 10, 64)
	if err != nil {
		return fmt.Errorf("message id %q is not a number; gate-inbox send prints the id it queued", operands[0])
	}
	state, err := sessions.MessageStatus(sessionID, messageID)
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, state, sessioncmd.FormatMessageState(state))
}

func runKill(out io.Writer, sessions sessionCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usageKill)
	asJSON := cmdline.JSONFlag(set)
	operands, err := parseCommand(out, set, args, 1, 1)
	if err != nil {
		return err
	}
	killed, err := sessions.Kill(sessionID, operands[0])
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, killed, "killed "+sessioncmd.FormatSession(killed))
}

func runRevive(out io.Writer, sessions sessionCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usageRevive)
	asJSON := cmdline.JSONFlag(set)
	operands, err := parseCommand(out, set, args, 1, 1)
	if err != nil {
		return err
	}
	revived, err := sessions.Revive(sessionID, operands[0])
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, revived, "revived "+sessioncmd.FormatSession(revived))
}

func runMigrate(out io.Writer, sessions sessionCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usageMigrate)
	tool := set.String("tool", "", "agent CLI the conversation moves to")
	name := set.String("name", "", "name for the new session; defaults to the source's name with the tool appended")
	account := set.String("account", "", "named subscription the new session runs on; defaults to the source's own, then the board's default")
	asJSON := cmdline.JSONFlag(set)
	operands, err := parseCommand(out, set, args, 1, 1)
	if err != nil {
		return err
	}
	migrated, err := sessions.Migrate(sessionID, operands[0], sessioncmd.MigrateOptions{Tool: *tool, Name: *name, Account: *account})
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, migrated, "migrated "+operands[0]+" to "+sessioncmd.FormatSession(migrated))
}

func runArchive(out io.Writer, sessions sessionCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usageArchive)
	restore := set.Bool("restore", false, "put an archived session back on the active list")
	asJSON := cmdline.JSONFlag(set)
	operands, err := parseCommand(out, set, args, 1, 1)
	if err != nil {
		return err
	}
	updated, err := sessions.Archive(sessionID, operands[0], !*restore)
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, updated, sessioncmd.FormatArchiveState(updated))
}

func runPark(out io.Writer, sessions sessionCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usagePark)
	dryRun := set.Bool("dry-run", false, "print what park would stop and leave, without stopping anything or recording the set")
	asJSON := cmdline.JSONFlag(set)
	if _, err := parseCommand(out, set, args, 0, 0); err != nil {
		return err
	}
	result, err := sessions.Park(sessionID, *dryRun)
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, result, sessioncmd.FormatParkResult(result))
}

func runUnpark(out io.Writer, sessions sessionCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usageUnpark)
	asJSON := cmdline.JSONFlag(set)
	if _, err := parseCommand(out, set, args, 0, 0); err != nil {
		return err
	}
	result, err := sessions.Unpark(sessionID)
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, result, sessioncmd.FormatUnparkResult(result))
}

func runGroups(out io.Writer, sessions sessionCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usageGroups)
	asJSON := cmdline.JSONFlag(set)
	if _, err := parseCommand(out, set, args, 0, 0); err != nil {
		return err
	}
	listed, err := sessions.Groups(sessionID)
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, listed, sessioncmd.FormatGroupList(listed))
}

func runCreateGroup(out io.Writer, sessions sessionCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usageCreateGroup)
	directory := set.String("directory", "", "default working directory sessions created in this group inherit")
	asJSON := cmdline.JSONFlag(set)
	operands, err := parseCommand(out, set, args, 1, 1)
	if err != nil {
		return err
	}
	created, err := sessions.CreateGroup(sessionID, operands[0], *directory)
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, created, "created group "+created.Path)
}

func runDeleteGroup(out io.Writer, sessions sessionCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usageDeleteGroup)
	asJSON := cmdline.JSONFlag(set)
	operands, err := parseCommand(out, set, args, 1, 1)
	if err != nil {
		return err
	}
	removal, err := sessions.DeleteGroup(sessionID, operands[0])
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, removal, sessioncmd.FormatGroupRemoval(removal))
}
