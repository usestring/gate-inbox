#!/usr/bin/env bash
# Tests for release-gate.sh, against a generated repository a few files long,
# so the run takes seconds rather than scanning this one.
#
# Two properties are worth pinning. A clean commit passes even when the working
# tree around it is dirty, because only the archive ships. And each check fires
# on the problem it exists for, which is what a reader cannot see from a gate
# that passes.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
gate="$script_dir/release-gate.sh"

test_root="$(mktemp -d "${TMPDIR:-/tmp}/release-gate-test.XXXXXX")"
trap 'chmod -R u+w "$test_root" 2>/dev/null; rm -rf "$test_root"' EXIT

command -v gitleaks >/dev/null || { echo "FAIL: the gate needs gitleaks on PATH" >&2; exit 1; }

failures=0
fail() {
	echo "FAIL: $*" >&2
	failures=$((failures + 1))
}

repo="$test_root/repo"
mkdir -p "$repo/LICENSES" "$repo/testdata"
g() { git -C "$repo" -c user.name=t -c user.email=t@example.com -c commit.gpgsign=false "$@"; }
g init -q -b main

notice='// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.'
printf 'module example.com/fixture\n\ngo 1.26\n' >"$repo/go.mod"
printf '// Copyright upstream\n%s\n\npackage main\n\nfunc main() {}\n' "$notice" >"$repo/main.go"
echo "Apache License" >"$repo/LICENSE"
echo "fixture" >"$repo/NOTICE"
echo "# Third-party licences" >"$repo/LICENSES/THIRD-PARTY.md"
echo "main.go" >"$repo/LICENSES/MODIFIED-FILES.txt"
g add -A
g commit -q -m clean

# Stand-ins for the private list. The real one never enters the tree, so
# neither do its terms: every value below is made up.
terms="$test_root/terms.txt"
# Home paths and the upstream name are assembled at run time: this file
# ships and is scanned too.
home_root=/home
up_name="agent-""manager" up_env="AGENT_""MANAGER" up_prefix="a""m_" up_env_prefix="A""M_"
cat >"$terms" <<'TERMS'
# a comment and a blank line are skipped

opsname
identifier: acme-kube
identifier: \bacmectl\b
identifier: acme\.example
module: acme-private
host: \bbuildbox-[0-9]+\b
TERMS
# The shipped allowlist, so the notice line it excuses is exercised too.
allow="$test_root/allow"
cp "$script_dir/release-gate.allow" "$allow"

run() {
	local out="$1"
	shift
	local status=0
	(cd "$repo" && "$gate" --allowlist "$allow" "$@") >"$out" 2>&1 || status=$?
	echo "$status"
}

expect() {
	local out="$1" want="$2"
	grep -qF -- "$want" "$out" || fail "expected '$want' in $(basename "$out")"
}

# Only the commit is checked: neither an untracked file nor an unstaged edit
# may change the verdict.
echo 'see github.com/acme-private/core' >"$repo/untracked.md"
echo "// $home_root/opsuser/work" >>"$repo/main.go"
status=$(run "$test_root/clean.out" --private-patterns "$terms")
[ "$status" -eq 0 ] || fail "clean commit exited $status: $(cat "$test_root/clean.out")"
expect "$test_root/clean.out" PASS
g checkout -q -- main.go
rm "$repo/untracked.md"

# The private list is required, because the tree cannot carry it.
status=$(run "$test_root/noterms.out" --skip-build)
[ "$status" -eq 1 ] || fail "run without a private-term list exited $status"
expect "$test_root/noterms.out" "no private-term list given"

# One bad line must not switch a whole check off, and every tag must be fed.
bad="$test_root/bad.txt"
printf 'opsname\nname: foo(bar\n' >"$bad"
status=$(run "$test_root/bad.out" --private-patterns "$bad" --skip-build)
[ "$status" -eq 1 ] || fail "run with a malformed private list exited $status"
expect "$test_root/bad.out" "private-term list line 2 is not a valid extended regex"
expect "$test_root/bad.out" "the private-term list has no identifier: line"

# A token that lives only in an earlier commit still ships with the history.
# It is generated here so that this file does not carry one.
token="ghp_$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 36 || true)"
echo "token = \"$token\"" >"$repo/old.txt"
g add -A
g commit -q -m leak
g rm -q old.txt
g commit -q -m unleak

echo 'import "github.com/acme-private/core"' >"$repo/deps.md"
echo 'deploys to acme-kube' >"$repo/ops.md"
echo "cwd: $home_root/opsuser/src" >"$repo/testdata/golden.txt"
cat >>"$repo/testdata/golden.txt" <<'GOLDEN'
owner opsname
ssh buildbox-7
home is /home/user/src
tests run in /home/dev/src and /home/pi and /home/x
printf("/home/dev\n")
logs land in <out>/work/home/logs/app.log
launch.env.BIN=<scratch>/home/bin/tool
GOLDEN
echo 'git clone https://github.com/usestring/gate-inbox.git' >"$repo/clone.md"
echo 'acmectl reads a token for metrics.acme.example' >"$repo/mixed.md"
# A term past the first few hundred characters is still one an entry covers.
printf '%s acmectl\n' "$(printf 'x%.0s' $(seq 1 400))" >"$repo/long.md"
# The upstream name, in text or a path, anywhere but LICENSE and NOTICE.
printf 'see %s for history\ntmux -L %ssock\nexport %s_HOME\nexport %sGOLDEN=write\n' "$up_name" "$up_prefix" "$up_env" "$up_env_prefix" >"$repo/upstream.md"
echo "Based on $up_name." >>"$repo/LICENSE"
echo "Includes $up_name." >>"$repo/NOTICE"
# README.md's Credits section may name it, and nothing else there may slip.
printf '# Fixture\nsee %s here\n## Credits\nDerived from %s.\nsessions were %sx\n## After\n%s again\n' \
	"$up_name" "$up_name" "$up_prefix" "$up_name" >"$repo/README.md"
mkdir -p "$repo/tools/$up_name"
echo tool >"$repo/tools/$up_name/run.txt"
# The gate's own file names buy no exemption anywhere in the tree.
mkdir -p "$repo/sub"
echo 'module github.com/acme-private/tool' >"$repo/sub/release-gate.allow"
printf 'package main\n\nfunc other() {}\n' >"$repo/other.go"
echo "other.go" >>"$repo/LICENSES/MODIFIED-FILES.txt"
echo "gone.go" >>"$repo/LICENSES/MODIFIED-FILES.txt"
printf 'package main\n\nimport "fmt"\n\nfunc bad() { fmt.Printf("%%d\\n", "x") }\n' >"$repo/vet.go"
g rm -q LICENSES/THIRD-PARTY.md
g add -A
g commit -q -m dirty

printf '%s\n' 'identifiers | * | \bacmectl\b | fixture: a public tool name' >>"$allow"
status=$(run "$test_root/dirty.out" --private-patterns "$terms")
[ "$status" -eq 1 ] || fail "dirty commit exited $status"
out="$test_root/dirty.out"
expect "$out" "old.txt:1: gitleaks github-pat (in history)"
expect "$out" "deps.md:1: private reference 'acme-private'"
expect "$out" "sub/release-gate.allow:1: private reference 'acme-private'"
expect "$out" "ops.md:1: organisation identifier 'acme-kube'"
expect "$out" "testdata/golden.txt:1: operator home '$home_root/opsuser'"
expect "$out" "testdata/golden.txt:2: operator name 'opsname'"
expect "$out" "testdata/golden.txt:3: internal host 'buildbox-7'"
expect "$out" "LICENSES/THIRD-PARTY.md:0: missing"
expect "$out" "other.go:1: no modification notice"
expect "$out" "lists 'gone.go', which is not in the tree"
expect "$out" "vet.go:5:"
expect "$out" "upstream.md:1: upstream name '$up_name'"
expect "$out" "upstream.md:2: upstream session prefix '$up_prefix'"
expect "$out" "upstream.md:3: upstream name '$up_env'"
expect "$out" "upstream.md:4: upstream environment prefix '$up_env_prefix'"
expect "$out" "tools/$up_name:0: upstream name in a file path"
expect "$out" "README.md:2: upstream name '$up_name'"
expect "$out" "README.md:5: upstream session prefix '$up_prefix'"
expect "$out" "README.md:7: upstream name '$up_name'"
if grep -qF "README.md:4:" "$out"; then
	fail "the name in README.md's Credits section was reported"
fi
if grep -qE "^  (LICENSE|NOTICE):[0-9]+: upstream" "$out"; then
	fail "the Apache attribution in LICENSE or NOTICE was reported"
fi
if grep -qE "operator home '$home_root/(user|dev|pi|x|logs|bin)" "$out"; then
	fail "a synthetic home directory was reported"
fi
if grep -qF "clone.md:" "$out"; then
	fail "this repository's own clone URL was reported"
fi
# An entry for a tool name excuses that term alone, never the private host
# beside it.
expect "$out" "mixed.md:1: organisation identifier 'acme.example'"
if grep -qF "long.md:" "$out"; then
	fail "an allowlisted term past character 300 was still reported"
fi
if grep -qF "mixed.md:1: organisation identifier 'acmectl'" "$out"; then
	fail "the acmectl entry did not excuse acmectl"
fi

# A reviewed entry suppresses exactly its hit; one without a reason is itself
# a finding.
cat >>"$allow" <<'ALLOW'
identifiers | ops.md | acme-kube | fixture: the deploy target is documented on purpose
private-modules | deps.md | acme-private
upstream-name | upstream.md | .* | fixture: no entry may excuse the upstream name
ALLOW
run "$test_root/allow.out" --private-patterns "$terms" --skip-build >/dev/null
out="$test_root/allow.out"
if grep -qF "ops.md:1:" "$out"; then
	fail "an allowlisted hit was still reported"
fi
expect "$out" "allowlist entry needs 'check | path | regex | reason'"
expect "$out" "deps.md:1: private reference 'acme-private'"
expect "$out" "upstream.md:1: upstream name '$up_name'"

if [ "$failures" -gt 0 ]; then
	echo "$failures failure(s)" >&2
	exit 1
fi
echo "ok"
