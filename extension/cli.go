package extension

import "context"

// CLIProvider is implemented by an extension that adds commands to the
// executable's CLI face: `gate-inbox <name> ...`, run from a session's own
// shell the way the core's commands are.
//
// Commands is asked on every start of every face, before Configure and
// with no config read, so it returns a fixed list and does nothing else.
// A name the core or another extension already answers to stops the
// executable from starting at all, whichever face was asked: a build whose
// commands collide is broken, and saying so only when somebody happens to
// type the verb would hide it.
type CLIProvider interface {
	Commands() []Command
}

// Command is one top-level CLI command. A command with verbs of its own --
// `gate-inbox thing <add|list>` -- is one Command whose Run reads the verb
// from args.
type Command struct {
	// Group is the heading its help is listed under; commands sharing one
	// are listed together. Empty lists it under the extension's ID.
	Group string
	// Name is the word typed after the program name. It is lower case,
	// starts with a letter, and may hold digits and '-'.
	Name string
	// Usage is the synopsis help prints after the program name, such as
	// "thing <add|list> [--json]". Empty prints Name.
	Usage string
	// About is help's one line on what it does.
	About string
	// Run is the command, given the arguments after its name and a Host
	// acting as the session whose shell ran it. The extension has been
	// configured from the operator's config by then, so a section it
	// refuses stops the command before Run. Output goes to os.Stdout and
	// os.Stderr. Returning flag.ErrHelp says usage was asked for and shown,
	// which exits 0.
	Run func(ctx context.Context, args []string, host Host) error
}
