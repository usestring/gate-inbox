# Codex caller binding

Codex tool shells hosted by a shared app-server daemon are outside their terminal client's process tree. Shell snapshots can carry the daemon's pane address, and a saved conversation association does not establish which client owns a terminal. Gate Inbox therefore requires a binding established at the client launch boundary before it can stop such a caller.

On macOS and Linux, launch a terminal client through Gate Inbox's bridge:

```sh
gate-inbox codex-client -- codex
gate-inbox codex-client -- codex resume <exact-thread-id>
```

Run this inside the client's own tmux pane. The bridge connects to an **existing** local Codex app-server at `$CODEX_HOME/app-server-control/app-server-control.sock` (default Codex home: `~/.codex`). An explicit `--server /tmp/fixture-server.sock` selects another existing Unix socket. It never starts, restarts or signals the daemon. The Codex client must support `--remote unix://PATH` and per-thread `shell_environment_policy.set` configuration; the offline native regression verifies Codex 0.162.1.

Gate Inbox can adopt the pane after this launch. A managed launch can also use `gate-inbox codex-client -- codex` as its tool command. The bridge obtains the real pane through verified process ancestry, authenticates both Unix socket peers, pins their process start identities, and takes the thread ID from the daemon's successful thread/start, thread/resume or thread/fork response. It passes a fresh private capability to that thread's tool shells through runtime configuration. Ephemeral structured checks do not bind a pane. Two bridged clients cannot claim the same daemon thread. Switching the visible thread revokes its previous capability; closing the bridge revokes all its capabilities.

The bridge uses the standard `exec_command` runtime and disables shell snapshots and the experimental code-mode host for its thread. It receives command start/completion notifications even when the client opts out. This preserves a kernel-visible shell command for runtimes without tool-call launch metadata. Other threads keep their configuration.

`gate-inbox stop` resolves the live capability to exactly one active row on the verified pane. It checks daemon ancestry and kernel launch metadata against an active command in the daemon-issued thread. When `CODEX_TOOL_CALL_ID` is unavailable, the full command arguments must match one pinned ancestor process and be unique in the daemon tree. Identical parallel commands refuse. Saved conversation matches and inherited tmux variables do not participate. The outside lifecycle worker rechecks the launch, pane, process start identities and binding, interrupts only the verified thread's current turn, then ends the pane and records the intentional stop. History stays available and kill observers are notified. A missing, stale, spoofed, recycled, disconnected or ambiguous binding refuses without standalone fallback.

Keep the tool shell alive while stopping, so an `exec` optimization cannot replace its command arguments before verification. For Codex 0.162.1, issue the tool command with a status-preserving postlude:

```sh
gate-inbox stop --json; result=$?; exit "$result"
```

Gate Inbox waits briefly for the runtime's command-start notification; it never treats missing command evidence as success. Redacted commands that cannot match the kernel arguments also refuse.

The bridge is a local same-user capability boundary, like Gate Inbox's existing lifecycle worker; it does not defend against a hostile process that can read another same-user process's secrets or modify its executable. Use one visible terminal client per thread. Stock clients attached outside the bridge do not acquire pane authority. Gate Inbox cannot retroactively authenticate an already-running stock client from its saved row. A repair never restarts that client: an explicitly authorized launch or exact-thread resume must use the bridge. For modes incompatible with Codex's remote client (`--add-dir`, an explicit `--remote`, or `--no-daemon`), use a private `codex --no-daemon` launch, whose tool shells retain verifiable pane ancestry.

Shutdown may need the runtime's scoped sandbox permission to access the private socket and tmux. Changing login-shell mode does not establish the binding.

Regression fixtures use private homes, state stores, sockets, daemon processes and panes. Run `go test ./internal/codexbind ./internal/sessioncmd ./app` for capability and lifecycle coverage. The opt-in native fixture uses an offline model stub and a disposable daemon. It creates all state itself and accepts no live socket or home:

```sh
go build -o /tmp/gate-inbox-binding .
python3 scripts/test-native-codex-binding.py --gate-inbox-binary /tmp/gate-inbox-binding --codex-binary /absolute/path/to/codex
```
