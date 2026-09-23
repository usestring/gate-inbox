#!/usr/bin/env bash
# Regenerate the Go dependency section of LICENSES/THIRD-PARTY.md and the
# licence files copied under LICENSES/third_party.
#
# Licences are classified offline from the module cache by
# github.com/google/licensecheck (scripts/thirdpartylicences, its own module so
# the scanner never enters this module's dependency graph). "Linked" means
# compiled into a binary built from ./... for linux, darwin or windows; the
# asset section of THIRD-PARTY.md is hand-written and left untouched.
#
#   scripts/third-party-licences.sh          rewrite in place
#   scripts/third-party-licences.sh --check  fail if the committed files are stale
set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd)
cd "$repo"

# Fetch every module in the graph into the cache from outside this module, so
# go.sum only ever lists what the build needs.
mods=$(go list -m -f '{{if not .Main}}{{.Path}}@{{.Version}}{{end}}' all)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
# shellcheck disable=SC2086
(cd "$tmp" && GOFLAGS= go mod download $mods)
(cd scripts/thirdpartylicences && go run . -root "$repo")

if [[ ${1:-} == --check ]]; then
  if [[ -n $(git status --porcelain -- LICENSES) ]]; then
    git status --short -- LICENSES >&2
    echo "LICENSES/ is stale: run scripts/third-party-licences.sh and commit the result" >&2
    exit 1
  fi
fi
