// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

// Package cli implements Gate Inbox's subcommands over internal/sessioncmd,
// the same layer the MCP server drives. An agent whose CLI carries no MCP
// client reaches the workspace from its shell instead, and both fronts speak
// the same words because the sentences come from sessioncmd.
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/usestring/gate-inbox/extension/cmdline"
)

type Command func(args []string, sessionID, configDir string) error

// ErrUsageShown reports that -h already printed the usage, so the caller
// exits without an error line. It is flag.ErrHelp, which cmdline returns and
// an extension's command returns too.
var ErrUsageShown = flag.ErrHelp

const anyNumber = cmdline.AnyNumber

// program opens every usage line.
const program = "gate-inbox"

type command struct {
	name  string
	usage string
	about string
	// Verbs are reached through run, never registered at the top level.
	verbs []command
	run   Command
}

type section struct {
	title    string
	commands []command
}

func sections() []section {
	return []section{
		sessionSection(),
		taskSection(),
		fileSection(),
		terminalSection(),
		renameSection(),
		prioritySection(),
	}
}

func Commands() map[string]Command {
	table := map[string]Command{}
	for _, section := range sections() {
		for _, command := range section.commands {
			table[command.name] = command.run
		}
	}
	return table
}

func Help() string {
	var help strings.Builder
	help.WriteString("Usage: gate-inbox [command]\n\n")
	help.WriteString("Run the interactive manager when no command is given.\n\n")
	help.WriteString("Gate Inbox runs your session beside the user's other agents and terminals.\n")
	help.WriteString("Every command acts as the session it runs in, so run them from your own shell.\n")
	for _, section := range sections() {
		help.WriteString("\n" + section.title + "\n")
		help.WriteString(usageLines(section.commands))
	}
	help.WriteString("\nOptions:\n")
	help.WriteString("  -h, --help     Show this help text\n")
	help.WriteString("  -v, --version  Print the installed version\n")
	help.WriteString("  --log-path     Print where the diagnostic log is written (alias: logs)\n")
	return help.String()
}

func usageLines(commands []command) string {
	var lines strings.Builder
	for _, command := range commands {
		lines.WriteString(cmdline.UsageLines(program, []cmdline.Verb{{Usage: command.usage, About: command.about}}))
		lines.WriteString(usageLines(command.verbs))
	}
	return lines.String()
}

func groupSection(title, name, about string, verbs []command) section {
	group := command{
		name:  name,
		usage: name + " <" + strings.Join(verbNames(verbs), "|") + ">",
		about: about,
		verbs: verbs,
		run: func(args []string, sessionID, configDir string) error {
			return dispatch(os.Stdout, name, verbs, args, sessionID, configDir)
		},
	}
	return section{title: title, commands: []command{group}}
}

func verbNames(verbs []command) []string {
	names := make([]string, 0, len(verbs))
	for _, verb := range verbs {
		names = append(names, verb.name)
	}
	return names
}

func dispatch(out io.Writer, group string, verbs []command, args []string, sessionID, configDir string) error {
	bound := make([]cmdline.Verb, 0, len(verbs))
	for _, verb := range verbs {
		bound = append(bound, cmdline.Verb{
			Name:  verb.name,
			Usage: verb.usage,
			About: verb.about,
			Run: func(args []string) error {
				return verb.run(args, sessionID, configDir)
			},
		})
	}
	return cmdline.Dispatch(out, program, group, bound, args)
}

// bind hands a subcommand the layer it drives, which tests replace with a
// fake so no handler reaches the manager's live tmux socket.
func bind[Layer any](open func(configDir string) Layer, run func(io.Writer, Layer, []string, string) error) Command {
	return func(args []string, sessionID, configDir string) error {
		return run(os.Stdout, open(configDir), args, sessionID)
	}
}

func configCommand(run func(out io.Writer, args []string, sessionID, configDir string) error) Command {
	return func(args []string, sessionID, configDir string) error {
		return run(os.Stdout, args, sessionID, configDir)
	}
}

func usageError(usage string) error {
	return fmt.Errorf("usage: %s %s", program, usage)
}

func parseCommand(out io.Writer, set *flag.FlagSet, args []string, min, max int) ([]string, error) {
	return cmdline.Parse(out, program, set, args, min, max)
}

type stringList []string

func (list *stringList) String() string {
	return strings.Join(*list, ",")
}

func (list *stringList) Set(value string) error {
	*list = append(*list, strings.Split(value, ",")...)
	return nil
}
