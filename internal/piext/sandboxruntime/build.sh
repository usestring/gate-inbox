#!/bin/sh
# Rebuilds the sandbox runtime the pi extension loads (internal/piext):
# @anthropic-ai/sandbox-runtime and its dependencies as one ES module, plus
# the licences of what it bundles (LICENSES/npm). Its Linux apply-seccomp
# helper is a binary and is not vendored; the runtime finds a system copy
# (a global npm install) when there is one. The outputs are committed, so a Go
# build needs neither bun nor the npm registry. Rerun after changing the
# pinned version in package.json, then commit bun.lock and the outputs.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
out=$(dirname "$here")
cd "$here"
bun install --frozen-lockfile
bun build entry.ts --target=node --format=esm --minify --outfile="$out/sandbox-runtime.mjs"
# Every bundled package's licence travels with the binary that embeds it.
licences=$(cd "$out/../.." && pwd)/LICENSES/npm
rm -rf "$licences"
for pkg in node_modules/[!@.]*/ node_modules/@*/*/; do
	[ -f "$pkg/package.json" ] || continue
	pkg=${pkg%/}
	name=${pkg#node_modules/}
	mkdir -p "$licences/$name"
	for f in "$pkg"/LICEN[CS]E* "$pkg"/NOTICE*; do
		[ -f "$f" ] && cp "$f" "$licences/$name/"
	done
done
rm -rf node_modules
