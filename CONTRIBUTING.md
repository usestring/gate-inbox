# Contributing

Issues and pull requests are welcome. For a large change, open an issue first so the design can be
agreed before the code is written.

## Building and testing

You need Go (the version in `go.mod`), tmux 3.1 or newer, and git.

```bash
go build ./...
go vet ./...
gofmt -l .                                   # must print nothing
env -u TMUX CGO_ENABLED=0 go test ./...
```

Many packages drive a real tmux server, so run the tests on a machine where tmux can open its
socket. `internal/ui` is the slow one: `-short` skips its wall-clock tests, and
`scripts/shard-test.sh` splits it across processes. [`README.md`](README.md#development) has the
details.

### Tests and your live tmux server

The suite is built so it cannot reach the tmux server you are working in, even when `go test` runs
inside a tmux pane (the `env -u TMUX` above is a courtesy, not the safeguard):

- Every test package that starts a process has a `TestMain` that calls `tmuxtest.Main`, `Run` or
  `Guard` from `internal/tmuxtest`. Importing that package unsets `TMUX` and `TMUX_PANE` and points
  `TMUX_TMPDIR` at a fresh directory for the run; the teardown kills every server in that directory
  by `-S` path and removes it.
- A child process gets `tmuxtest.Environ()` (or `tmuxtest.ScrubEnv(env)`), never `os.Environ()`.
- A raw `tmux` command in a test names its server with `-L` or `-S`, or goes through `tmuxOn`,
  `tmuxOnSocket`, `tmuxCmd` or `tmuxtest.KillServer`. Never write a bare `tmux kill-server` or
  `kill-session`.
- A test that fakes the `TMUX` a pane would carry builds it from `tmuxtest.SocketPath`, not a
  literal `/tmp/tmux-<uid>/...` path.

`internal/tmuxtest/static_test.go` enforces the last three rules. In a test binary,
`internal/tmuxguard` panics on any driver command whose socket resolves into tmux's default socket
directory or into one inherited through `TMUX` or `TMUX_TMPDIR`. `scripts/test-tmux-isolation.sh`
runs the whole suite with `TMUX` pointed at a sentinel server and fails if the server loses or gains
anything. CI runs the suite through it.

CI runs the same checks on every pull request, plus a gitleaks scan of the full history. It needs
no secrets, so it runs on a fork as-is. A test that skips needs an entry in `ci-allowed-skips.txt`,
or the job fails. That keeps a suite that silently skipped from counting as a pass.

## Pull requests

- One concern per pull request. Say what changed and why, and how you checked it.
- Add or update tests with the change.
- A change to what the board draws ships with a recording:
  [`tools/capture/README.md`](tools/capture/README.md) shows how to make one.
- Keep upstream's attribution intact. Files that came from the upstream project keep their
  existing notices, and `NOTICE` names it and records the fork. A bug that also exists upstream is worth reporting
  there too.

## Pull request titles and releases

Pull requests are squash-merged, so the title becomes the commit subject on `main`, and releases are
versioned from those subjects. A title must follow [Conventional Commits](https://www.conventionalcommits.org/):
`type(optional scope): subject`, for example `fix(ui): keep the triage drain open`. The `PR title`
check fails a pull request whose title does not parse.

| Type | Release |
| --- | --- |
| `feat` | minor (`0.1.8` to `0.2.0`) |
| `fix`, `perf` | patch (`0.1.8` to `0.1.9`) |
| `refactor`, `revert`, `chore`, `docs`, `ci`, `test`, `build`, `style` | none |
| any type with `!` (`feat!: ...`), or a `BREAKING CHANGE:` footer | major (`0.x` goes to `1.0.0`) |

On every push to `main`, the release workflow runs [semantic-release](https://semantic-release.gitbook.io/)
over the commits since the last `v*` tag. When they call for a release it pushes the next tag and
drafts a GitHub Release with notes grouped by type, and goreleaser attaches the binaries and
publishes it. Commits that only carry non-releasing types cut nothing; they appear in the next
release's compare link. Pushing a `v*` tag by hand still releases that tag through goreleaser alone.

## Licence

Gate Inbox is licensed under [Apache-2.0](LICENSE). Under section 5 of that licence, anything you
submit for inclusion is licensed under the same terms, unless you say otherwise.

## Security

Do not open a public issue for a vulnerability. [`SECURITY.md`](SECURITY.md) explains how to report
one privately.
