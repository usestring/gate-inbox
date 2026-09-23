// Package compat is the board's compatibility baseline: goldens of what an
// operator and the agents on the board can observe, recorded so that moving
// a feature out of this module can be proven not to have changed any of it.
//
// It holds no code the program runs. Its tests record, from synthetic inputs
// only:
//
//   - the CLI: the help text, every command's and verb's usage, and the exit
//     status of a success, an unknown command and a usage error;
//   - the MCP server: every tool's name, description, annotations and
//     schemas, and the server instructions, with extensions off and on;
//   - the config: config.toml, keys.toml and snippets.json as the board
//     resolves them from an absent, an empty, an old, a minimal and a full
//     file, with every setting marked as written, rewritten or backfilled;
//   - launch plans: the command, pending input, environment and resume
//     command each agent CLI is launched with, secrets redacted;
//   - account selection across the own, explicit and smart modes;
//   - the state store's schema;
//   - the default key bindings per context and the status-engine patterns
//     per tool.
//
// Every test runs against a scratch GATE_INBOX_HOME, a scratch TMUX_TMPDIR,
// an environment emptied of everything the host exported, and fake agent
// CLIs and a fake secret store on a PATH of their own, with a fake account
// pool in place of an extension's. Nothing reaches a tmux server, a real
// secret store, a monitoring endpoint or the network, and no operator path,
// account or transcript appears in a fixture.
//
// A golden that changes is a behaviour that changed. When the change is
// meant, re-record with
//
//	GATE_INBOX_GOLDEN=write go test ./compat/
//
// and review the diff of compat/testdata as the behaviour change it is.
package compat
