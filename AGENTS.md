# Agent notes

Human setup, CI and the tmux isolation rules are in [`CONTRIBUTING.md`](CONTRIBUTING.md). This file
is how a coding agent should test a change locally. Agents usually share one box, so a full local run
costs every other session on it, and on a loaded box the full `internal/ui` and `internal/sessioncmd`
suites mostly report load flakes rather than regressions.

## Testing a change

- Run `go test -short` on the packages you changed, and only those:
  `env -u TMUX -u TMUX_PANE TMUX_TMPDIR="$(mktemp -d)" go test -short ./<changed package>`.
  `-short` skips the tests that assert wall-clock budgets; CI runs them in full.
- Always unset `TMUX` and `TMUX_PANE` and give the run its own `TMUX_TMPDIR`, as above.
- Leave the full `internal/ui` and `internal/sessioncmd` suites, and `scripts/ci-test.sh`, to CI.
  For the same reason, do not enable the `.githooks` pre-push hook: it runs the full suite on
  every push.
- Do not run `go build ./...` before testing: `go test` builds what it runs.
- Do not pass `-count=1` unless you are proving a flake. The test cache is what keeps a rerun of
  unchanged packages free.
- If you do need a full run, pass an explicit `-timeout` (for example `-timeout 30m`). Go's
  10-minute default fails a loaded run partway through, and the rerun repeats all of it.
- A test that fails under load and passes alone is a load flake, not a result. Do not rerun it on
  `origin/main` to prove that; say so in the PR and let CI decide.
