# Codex caller binding

Codex tool shells hosted by a shared app-server daemon are outside their terminal client's process tree. Shell snapshots can carry the daemon's pane address, and a saved conversation association does not establish which client owns a terminal. Gate Inbox therefore requires a binding established at the client launch boundary before it can stop such a caller.

Ordinary managed Codex launches, revives and forks use the shared-daemon bridge by default. Older config files with the former stock commands are migrated when loaded; custom commands remain explicit choices. The launcher resolves the running Gate Inbox executable rather than depending on another copy on PATH. Machine-local interactive launchers should use the same entrypoint so later adoption retains verified authority without restarting a client.

On macOS and Linux, launch through:

```sh
gate-inbox codex-client -- codex
gate-inbox codex-client -- codex resume <exact-thread-id>
```

Run this inside the client's own tmux pane. The bridge reuses the local Codex app-server at `$CODEX_HOME/app-server-control/app-server-control.sock` (default Codex home: `~/.codex`). An explicit `--server /tmp/fixture-server.sock` selects another existing Unix socket. When the default socket is absent, it calls native `codex app-server daemon start`, which is idempotent. An existing socket is inspected with native `daemon version` and is never restarted or updated by the bridge. The reported managed executable must own the authenticated Unix peer; separately installed daemon packages are supported. New threads carry the caller's working directory, so a daemon started in another directory cannot redirect new work. Resume and fork preserve the source thread's directory; an explicit `--cd` remains authoritative. An explicit socket never starts a server. Startup failure does not fall back to private mode. Older Codex installations whose native help advertises neither remote clients nor daemon mode retain their original private launch, without unsupported flags. The Codex client must support `--remote unix://PATH` and per-thread `shell_environment_policy.set` configuration; the offline native regression verifies Codex 0.162.1.

Gate Inbox can adopt the pane after this launch. A managed launch can also use `gate-inbox codex-client -- codex` as its tool command. The bridge obtains the real pane through verified process ancestry, authenticates both Unix socket peers, pins their process start identities, and takes the thread ID from the daemon's successful thread/start, thread/resume or thread/fork response. It passes a private capability to that thread's tool shells through runtime configuration. Ephemeral structured checks do not bind a pane. Two bridged clients cannot claim the same daemon thread. Switching the visible thread or closing the bridge removes its live authority. The capability endpoint remains stable for one daemon/thread identity because cached Codex threads can ignore new environment overrides on resume. Its private alias routes only to the current exclusive lock-owning bridge; a disconnected endpoint cannot authorize shutdown. A resumed client still needs daemon-issued identity, fresh command events and kernel caller proof.

The bridge uses the standard `exec_command` runtime and disables shell snapshots and the experimental code-mode host for its thread. It receives command start/completion notifications even when the client opts out. This preserves a kernel-visible shell command for runtimes without tool-call launch metadata. Other threads keep their configuration.

`gate-inbox stop` resolves the live capability to exactly one active row on the verified pane. It checks daemon ancestry and kernel launch metadata against an active command in the daemon-issued thread. When `CODEX_TOOL_CALL_ID` is unavailable, the full command arguments must match one pinned ancestor process and be unique in the daemon tree. Identical parallel commands refuse. Saved conversation matches and inherited tmux variables do not participate. The outside lifecycle worker rechecks the launch, pane, process start identities and binding, interrupts only the verified thread's current turn, then ends the pane and records the intentional stop. History stays available and kill observers are notified. A missing, stale, spoofed, recycled, disconnected or ambiguous binding refuses without standalone fallback.

Keep the tool shell alive while stopping, so an `exec` optimization cannot replace its command arguments before verification. For Codex 0.162.1, issue the tool command with a status-preserving postlude:

```sh
gate-inbox stop --json; result=$?; exit "$result"
```

Gate Inbox waits briefly for the runtime's command-start notification; it never treats missing command evidence as success. Redacted commands that cannot match the kernel arguments also refuse.

The bridge is a local same-user capability boundary, like Gate Inbox's existing lifecycle worker; it does not defend against a hostile process that can read another same-user process's secrets or modify its executable. Use one visible terminal client per thread. Stock clients attached outside the bridge do not acquire pane authority. Gate Inbox cannot retroactively authenticate an already-running stock client from its saved row. A repair never restarts that client: an explicitly authorized launch or exact-thread resume must use the bridge. The bridge refuses modes incompatible with Codex's remote client (`--add-dir`, `--worktree`, an explicit `--remote`, or `--no-daemon`) rather than silently changing execution mode. Native utility subcommands and help/version pass through without starting a bridge or daemon. A raw private launch remains an explicit operator choice.

Shutdown may need the runtime's scoped sandbox permission to access the private socket and tmux. Changing login-shell mode does not establish the binding.

Regression fixtures use private homes, state stores, sockets, daemon processes and panes. Run `go test ./internal/codexbind ./internal/sessioncmd ./app` for capability and lifecycle coverage. The opt-in native fixture uses an offline model stub and a disposable daemon. It creates all state itself and accepts no live socket or home:

```sh
go build -o /tmp/gate-inbox-binding .
python3 scripts/test-native-codex-binding.py --gate-inbox-binary /tmp/gate-inbox-binding --codex-binary /absolute/path/to/codex
```
