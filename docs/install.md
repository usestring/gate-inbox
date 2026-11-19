<!-- Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE. -->

# Install

Gate Inbox has no packaged releases yet, so the only route is building it from source. Release
archives, package-manager entries and an install script come with the first release.

## Dependencies

| Tool | What it powers |
| --- | --- |
| Go 1.27.2+ | building the binary |
| tmux 3.1+ | every agent session |
| git | repository roots, session-end pruning |
| bubblewrap (`bwrap`), Linux only | launching any CLI other than Claude Code: it hides the board's key directory from the session ([Parent channel](parent-channel.md)); macOS uses the built-in `sandbox-exec` |
| wl-clipboard, xclip, or xsel | pasting images into a prompt on Linux |

Install tmux, git and, on Linux, bubblewrap with your package manager. Without a working `bwrap`, Codex, OpenCode, pi and other non-Claude sessions are refused at launch rather than started where they could read the keys; Ubuntu 24.04 and later also need unprivileged user namespaces allowed for it. When the manager starts without tmux, it names the install command it detected, or tells you to use your package manager.

## From source

```bash
git clone https://github.com/usestring/gate-inbox.git
cd gate-inbox
go build -o gate-inbox .
./gate-inbox
```

`go install github.com/usestring/gate-inbox@latest` does the same once the module is public, and installs to `$(go env GOPATH)/bin`.

## Windows

Run inside [WSL2](https://learn.microsoft.com/windows/wsl/install): the manager lives on tmux, which is a Linux/macOS tool. Build it inside a WSL shell as above.

The agent CLIs belong in the distro too. WSL appends the Windows `PATH` to the distro's, so a CLI installed on the Windows side is visible in a WSL shell, and running it there starts a Windows process or fails for want of a Linux runtime. A spawn finding only that copy stops on the setup dialog, which names where the Windows copy is and the command that installs the CLI in the distro.

Under WSL2 the board's desktop notifications arrive as native Windows toasts, posted through PowerShell interop, and the machine stats read the Windows host rather than the Linux VM (see [Stats](usage.md#stats)).

## Updating

There is no update check. Pull and rebuild:

```bash
git pull && go build -o gate-inbox .
```

Restarting the manager on the same state directory supersedes the running board, and every agent session keeps running.
