<!-- Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE. -->

# Install

Gate Inbox has no packaged releases yet, so the only route is building it from source. Release
archives, package-manager entries and an install script come with the first release.

## Dependencies

| Tool | What it powers |
| --- | --- |
| Go 1.26.5+ | building the binary |
| tmux 3.1+ | every agent session |
| git | repository roots, session-end pruning |
| wl-clipboard, xclip, or xsel | pasting images into a prompt on Linux |

Install tmux and git with your package manager. When the manager starts without tmux, it names the install command it detected, or tells you to use your package manager.

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

## Updating

There is no update check. Pull and rebuild:

```bash
git pull && go build -o gate-inbox .
```

Restarting the manager on the same state directory supersedes the running board, and every agent session keeps running.
