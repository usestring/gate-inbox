#!/usr/bin/env bash
# Check that a commit is safe to publish, before it is published.
#
# Everything is checked against `git archive REV` extracted to a scratch
# directory, never against the working tree: an untracked file or an unstaged
# edit does not ship, and a working-tree check would pass or fail on it. The
# secret scan also covers the history reachable from REV, because that goes
# public with the repository.
#
# The gate reports every finding rather than stopping at the first, grouped by
# check, as path:line: reason. It exits 1 when anything is found, 2 when it
# could not run, and 0 only on a clean tree. A check whose inputs are missing
# (no LICENSES/MODIFIED-FILES.txt, no gitleaks) reports that as a finding
# rather than skipping, so a green run means every check ran.
set -euo pipefail
shopt -s nullglob

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

rev=HEAD
allowlist="$script_dir/release-gate.allow"
# Every term that must not ship -- operator names, organisation identifiers,
# private module and repository names, internal hosts -- and that for the same
# reason cannot be written into the tree, the gate included. One extended
# regex per line, tagged with the check it feeds (see usage).
private_patterns="${RELEASE_GATE_PRIVATE_PATTERNS:-}"
skip_build=0
keep=0

# The modification-notice contract shared with scripts/modification-notices.sh.
notice_text='Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.'

usage() {
	cat >&2 <<'USAGE'
usage: scripts/release-gate.sh [options] [REV]

Checks the tree and history of REV (default HEAD) for release safety.

  --allowlist F         reviewed exceptions (default scripts/release-gate.allow)
  --private-patterns F  terms to forbid, one ERE per line, each tagged
                        name:, identifier:, module: or host: (an untagged
                        line is a name); every tag needs at least one line
                        (default $RELEASE_GATE_PRIVATE_PATTERNS; required)
  --skip-build          skip the clean-cache build (self-test only)
  --keep                keep the scratch directory and print its path
USAGE
	exit 2
}

while [ $# -gt 0 ]; do
	case "$1" in
	--allowlist) allowlist="${2:?}"; shift 2 ;;
	--private-patterns) private_patterns="${2:?}"; shift 2 ;;
	--skip-build) skip_build=1; shift ;;
	--keep) keep=1; shift ;;
	-h | --help) usage ;;
	-*) echo "unknown option: $1" >&2; usage ;;
	*) rev="$1"; shift ;;
	esac
done

repo="$(git rev-parse --show-toplevel)"
commit="$(git -C "$repo" rev-parse --verify --quiet "$rev^{commit}")" || {
	echo "release-gate: not a commit: $rev" >&2
	exit 2
}

work="$(mktemp -d "${TMPDIR:-/tmp}/release-gate.XXXXXX")"
cleanup() {
	if [ "$keep" -eq 1 ]; then
		echo "scratch kept at $work" >&2
		return
	fi
	# The module cache is written read-only.
	chmod -R u+w "$work" 2>/dev/null || true
	rm -rf "$work"
}
trap cleanup EXIT

tree="$work/tree"
mkdir -p "$tree"
git -C "$repo" archive --format=tar "$commit" | tar -x -C "$tree"

# One record per finding: check, path, line, reason, source text. The text is
# what allowlist entries match against.
raw="$work/raw.tsv"
: >"$raw"

add() {
	local reason="${4//$'\t'/ }" text="${5:-}"
	text="${text//$'\t'/ }"
	[ "${#reason}" -gt 200 ] && reason="${reason:0:197}..."
	# The whole line is kept: an allowlist entry has to see a term however far
	# along the line it sits. Only the reason is shortened, for display.
	printf '%s\t%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$reason" "$text" >>"$raw"
}

# Scanning helper: every line of the archive matching PATTERN, excluding
# go.sum, as path<TAB>line<TAB>text. The gate's own files are scanned too:
# they carry no private term, only the generic patterns below.
grep_tree() {
	local flags="$1" pattern="$2"
	local opts=(-rIn --exclude=go.sum)
	[ -n "$flags" ] && opts+=("$flags")
	(cd "$tree" && { grep "${opts[@]}" -E -e "$pattern" . 2>/dev/null || true; }) |
		sed -E 's#^\./##; s#^([^:]*):([0-9]+):#\1\t\2\t#'
}

# Reports each distinct term a line matches, so an allowlist entry for one
# term cannot hide another on the same line.
scan() {
	local check="$1" label="$2" flags="$3" pattern="$4"
	local path line text rest term seen
	[ "$flags" = -i ] && shopt -s nocasematch
	while IFS=$'\t' read -r path line text; do
		rest="$text" seen="|"
		while [[ $rest =~ $pattern ]]; do
			term="${BASH_REMATCH[0]}"
			if [[ $seen != *"|${term,,}|"* ]]; then
				add "$check" "$path" "$line" "$label '$term'" "$text"
				seen+="${term,,}|"
			fi
			rest="${rest#*"$term"}"
		done
	done < <(grep_tree "$flags" "$pattern")
	shopt -u nocasematch
}

# --- 0. the archive itself ---------------------------------------------------

# export-ignore drops a path from `git archive` but not from a push, so a path
# the archive leaves out would ship unchecked.
while IFS= read -r path; do
	[ -e "$tree/$path" ] || [ -L "$tree/$path" ] ||
		add gate "$path" 0 "tracked but left out of git archive (export-ignore?); ships unchecked"
done < <(git -C "$repo" ls-tree -r "$commit" | awk -F'\t' '{ split($1, m, " ") } m[2] == "blob" { print $2 }')

# --- 1. secrets --------------------------------------------------------------

if ! command -v gitleaks >/dev/null; then
	add secrets - 0 "gitleaks is not installed; the secret scan did not run"
elif ! command -v jq >/dev/null; then
	add secrets - 0 "jq is not installed; the secret scan could not be read"
else
	leaks_config=()
	[ -f "$tree/.gitleaks.toml" ] && leaks_config=(--config "$tree/.gitleaks.toml")

	status=0
	gitleaks dir --no-banner --redact --log-level error "${leaks_config[@]}" \
		--report-format json --report-path "$work/leaks-tree.json" "$tree" \
		>/dev/null 2>"$work/leaks-tree.err" || status=$?
	if [ "$status" -gt 1 ] || [ ! -s "$work/leaks-tree.json" ]; then
		add secrets - 0 "gitleaks dir failed: $(head -c 200 "$work/leaks-tree.err")"
	else
		while IFS=$'\t' read -r file line rule; do
			add secrets "${file#"$tree"/}" "$line" "gitleaks $rule" "$rule"
		done < <(jq -r '.[] | [.File, .StartLine, .RuleID] | @tsv' "$work/leaks-tree.json")
	fi

	status=0
	(cd "$repo" && gitleaks git --no-banner --redact --log-level error "${leaks_config[@]}" \
		--log-opts="$commit" --report-format json --report-path "$work/leaks-history.json" .) \
		>/dev/null 2>"$work/leaks-history.err" || status=$?
	if [ "$status" -gt 1 ] || [ ! -s "$work/leaks-history.json" ]; then
		add secrets - 0 "gitleaks git failed: $(head -c 200 "$work/leaks-history.err")"
	else
		while IFS=$'\t' read -r sha file line rule; do
			add secrets "${sha:0:12}:$file" "$line" "gitleaks $rule (in history)" "$rule"
		done < <(jq -r '.[] | [.Commit, .File, .StartLine, .RuleID] | @tsv' "$work/leaks-history.json")
	fi
fi

# --- private terms -----------------------------------------------------------

# Checks 2-4 each take one tag's lines. A malformed line would make the joined
# pattern fail to compile and silently match nothing, so each line is checked
# on its own, and a tag with no lines is a finding: a green run has to mean
# every check had something to look for.
declare -A private=([name]="" [identifier]="" [module]="" [host]="")
if [ -z "$private_patterns" ]; then
	add gate - 0 "no private-term list given (--private-patterns or RELEASE_GATE_PRIVATE_PATTERNS)"
elif [ ! -r "$private_patterns" ]; then
	add gate - 0 "private-term list unreadable: $private_patterns"
else
	n=0
	while IFS= read -r entry || [ -n "$entry" ]; do
		n=$((n + 1))
		[[ $entry =~ ^[[:space:]]*(#|$) ]] && continue
		kind=name
		if [[ $entry =~ ^(name|identifier|module|host):[[:space:]]*(.*)$ ]]; then
			kind="${BASH_REMATCH[1]}" entry="${BASH_REMATCH[2]}"
		fi
		# grep and bash's [[ =~ ]] both compile it; 2 from either means invalid.
		status=0 bstatus=0
		grep -qE -e "$entry" </dev/null 2>/dev/null || status=$?
		[[ x =~ $entry ]] || bstatus=$?
		if [ -z "$entry" ] || [ "$status" -gt 1 ] || [ "$bstatus" -gt 1 ]; then
			add gate - 0 "private-term list line $n is not a valid extended regex"
			continue
		fi
		private[$kind]+="${private[$kind]:+|}$entry"
	done <"$private_patterns"
	for kind in name identifier module host; do
		[ -n "${private[$kind]}" ] || add gate - 0 "the private-term list has no $kind: line"
	done
fi

# --- 2. private module and repository references -----------------------------

scan private-modules "private reference" "" '\bGOPRIVATE\b|\bGONOSUMDB\b|\bGONOPROXY\b'
if [ -n "${private[module]}" ]; then
	scan private-modules "private reference" -i "${private[module]}"
fi

# Any other module under the owner is private until shown otherwise.
while IFS=$'\t' read -r path line text; do
	rest="$text"
	while [[ $rest =~ github\.com/usestring/([A-Za-z0-9._-]+) ]]; do
		# A clone URL names the same repository with .git on the end.
		if [ "${BASH_REMATCH[1]%.git}" != gate-inbox ]; then
			add private-modules "$path" "$line" "non-public module '${BASH_REMATCH[0]}'" "$text"
			break
		fi
		rest="${rest#*"${BASH_REMATCH[0]}"}"
	done
done < <(grep_tree "" 'github\.com/usestring/')

# A replace to a local directory builds only on the machine that has it.
while IFS= read -r mod; do
	while IFS=: read -r line text; do
		add private-modules "${mod#./}" "$line" "go.mod replace to a local path" "$text"
	done < <(grep -nE '=>[[:space:]]*(\.{1,2}/|/)' "$tree/$mod" || true)
done < <(cd "$tree" && find . -name go.mod -not -path './.git/*')

# --- 3. organisation identifiers ---------------------------------------------

# Organisation names, their systems, secret and ticket names: the list's
# identifier: lines. Nothing here is generic enough to write down.
if [ -n "${private[identifier]}" ]; then
	scan identifiers "organisation identifier" -i "${private[identifier]}"
fi

# --- 4. operator paths, hosts and names --------------------------------------

# Home directories that are plainly made up.
# shellcheck disable=SC2016 # $USER is matched literally
synthetic_users='^(user|username|you|me|dev|pi|x|alice|bob|carol|dave|eve|example|someone|name|jdoe|jane|john|test|tester|runner|USER|\$USER|\$\{USER\}|<user>|<you>)$'
while IFS=$'\t' read -r path line text; do
	rest="$text"
	# A backslash ends the name too, as in "/home/dev\n" inside a Go string.
	# /home must start a path: <out>/work/home/logs and <scratch>/home/bin are
	# not home directories.
	while [[ $rest =~ (^|[^[:alnum:]_.>-])(/home|/Users)/([^/[:space:]\"\'\`:\)\\]+) ]]; do
		match="${BASH_REMATCH[0]}"
		home="${BASH_REMATCH[2]}/${BASH_REMATCH[3]}"
		if ! [[ ${BASH_REMATCH[3]} =~ $synthetic_users ]]; then
			add operator-paths "$path" "$line" "operator home '$home'" "$text"
			break
		fi
		rest="${rest#*"$match"}"
	done
done < <(grep_tree "" '(/home|/Users)/[^/[:space:]]')

# Hostnames that only resolve inside a private network, plus the list's
# host: lines for the ones that look public.
scan operator-paths "internal host" -i \
	'[a-z0-9-]+\.internal\b|\.svc\.cluster\.local|\.ts\.net\b'
if [ -n "${private[host]}" ]; then
	scan operator-paths "internal host" -i "${private[host]}"
fi
if [ -n "${private[name]}" ]; then
	scan operator-paths "operator name" -i "${private[name]}"
fi

# --- 5. licence files --------------------------------------------------------

for f in LICENSE NOTICE LICENSES/THIRD-PARTY.md; do
	if [ ! -f "$tree/$f" ]; then
		add licence-files "$f" 0 "missing"
	elif [ ! -s "$tree/$f" ]; then
		add licence-files "$f" 0 "empty"
	fi
done

# --- 6. per-file modification notices ----------------------------------------

modified=LICENSES/MODIFIED-FILES.txt
if [ ! -f "$tree/$modified" ]; then
	add modification-notices "$modified" 0 "missing, so no modified file can be checked for its notice"
else
	escaped="${notice_text//./\\.}"
	n=0
	while IFS= read -r path || [ -n "$path" ]; do
		n=$((n + 1))
		path="${path%$'\r'}"
		[[ $path =~ ^[[:space:]]*(#|$) ]] && continue
		if [ ! -f "$tree/$path" ]; then
			add modification-notices "$modified" "$n" "lists '$path', which is not in the tree"
			continue
		fi
		case "$path" in
		*.go) want="^// $escaped\$" ;;
		*) want="^[[:space:]]*(//|#|--|;|<!--|/\*|\*)?[[:space:]]*$escaped" ;;
		esac
		head -n 40 "$tree/$path" | grep -qE "$want" ||
			add modification-notices "$path" 1 "no modification notice in the first 40 lines"
	done <"$tree/$modified"
fi

# --- 7. clean build ----------------------------------------------------------

if [ "$skip_build" -eq 1 ]; then
	add clean-build - 0 "skipped (--skip-build)"
elif [ ! -f "$tree/go.mod" ]; then
	add clean-build go.mod 0 "missing"
else
	mkdir -p "$work/gomodcache" "$work/gocache"
	build_log="$work/build.log"
	status=0
	(
		cd "$tree"
		# GOENV=off drops the operator's go env file, which is where a
		# private-module setting would otherwise come from. An archive has no
		# VCS to stamp.
		env -u GOPRIVATE -u GONOPROXY -u GONOSUMDB -u GOPROXY -u GOSUMDB \
			GOENV=off GOWORK=off CGO_ENABLED=0 GOFLAGS=-buildvcs=false \
			GOMODCACHE="$work/gomodcache" GOCACHE="$work/gocache" \
			sh -c 'go build ./... && go vet ./...'
	) >"$build_log" 2>&1 || status=$?
	if [ "$status" -ne 0 ]; then
		found=0
		while IFS= read -r l; do
			if [[ $l =~ ^(\./)?([^[:space:]:]+\.go):([0-9]+)(:[0-9]+)?:[[:space:]]*(.*)$ ]]; then
				add clean-build "${BASH_REMATCH[2]}" "${BASH_REMATCH[3]}" "${BASH_REMATCH[5]}" "$l"
				found=1
			fi
		done <"$build_log"
		if [ "$found" -eq 0 ]; then
			add clean-build - 0 "go build/vet failed: $(grep -vE '^(#|go: downloading)' "$build_log" | head -n 3 | tr '\n' ' ')"
		fi
	fi
fi

# --- 8. upstream name -------------------------------------------------------

# The public tree names the upstream project only where Apache-2.0 requires
# the attribution, LICENSE and NOTICE, and in README.md's Credits section,
# which thanks it. Nowhere else may, file paths included, and no allowlist
# entry can excuse a hit. Its tmux session prefix and its
# environment-variable prefix are matched case-sensitively, as each is
# written in code.
upstream_name='agent[-_ ]?manager'
upstream_prefix='\bam_'
upstream_env_prefix='\bAM_'
scan upstream-name "upstream name" -i "$upstream_name"
scan upstream-name "upstream session prefix" "" "$upstream_prefix"
scan upstream-name "upstream environment prefix" "" "$upstream_env_prefix"
# The Credits section runs from its "## Credits" heading to the next "## "
# heading. Only the name is accepted there, never the prefixes.
credits_start=0 credits_end=0
if [ -f "$tree/README.md" ]; then
	read -r credits_start credits_end < <(awk '
		/^## Credits[[:space:]]*$/ { s = NR; next }
		s && !e && /^## / { e = NR - 1 }
		END { if (s && !e) e = NR; print s + 0, e + 0 }' "$tree/README.md")
fi
while IFS= read -r f; do
	add upstream-name "$f" 0 "upstream name in a file path"
done < <(cd "$tree" && find . -mindepth 1 | sed 's#^\./##' |
	{ grep -iE -e "$upstream_name" -e "(^|/)${upstream_prefix#'\b'}" || true; })

# --- allowlist ---------------------------------------------------------------

# Entries: check | path glob | line regex (ERE, case-insensitive) | reason.
# A glob's * also matches '/'. An entry with no reason is itself a finding.
allow_check=() allow_glob=() allow_re=() allow_used=() allow_line=()
if [ -f "$allowlist" ]; then
	n=0
	while IFS= read -r entry || [ -n "$entry" ]; do
		n=$((n + 1))
		[[ $entry =~ ^[[:space:]]*(#|$) ]] && continue
		c="${entry%% | *}"; rest="${entry#* | }"
		g="${rest%% | *}"; rest="${rest#* | }"
		r="${rest%% | *}"; reason="${rest#* | }"
		if [ "$rest" = "$r" ] || [ -z "${reason//[[:space:]]/}" ] || [ "$c" = "$entry" ]; then
			add gate "${allowlist#"$repo"/}" "$n" "allowlist entry needs 'check | path | regex | reason'" "$entry"
			continue
		fi
		allow_check+=("$c") allow_glob+=("$g") allow_re+=("$r") allow_used+=(0) allow_line+=("$n")
	done <"$allowlist"
fi

kept="$work/kept.tsv"
: >"$kept"
allowed=0
shopt -s nocasematch
while IFS=$'\t' read -r check path line reason text; do
	if [ "$check" = upstream-name ]; then
		case "$path" in
		LICENSE | NOTICE) ;;
		README.md)
			if ! [[ $reason == "upstream name '"* && $line -gt $credits_start && $line -le $credits_end ]]; then
				printf '%s\t%s\t%s\t%s\n' "$check" "$path" "$line" "$reason" >>"$kept"
			fi
			;;
		*) printf '%s\t%s\t%s\t%s\n' "$check" "$path" "$line" "$reason" >>"$kept" ;;
		esac
		continue
	fi
	hit=-1
	for i in "${!allow_check[@]}"; do
		# shellcheck disable=SC2053 # the glob is meant to match as a pattern
		if [ "${allow_check[$i]}" = "$check" ] && [[ $path == ${allow_glob[$i]} ]] && [[ $text =~ ${allow_re[$i]} ]]; then
			# An entry excuses only the term its own match covers.
			if [[ $reason =~ \'(.+)\'$ ]]; then
				term="${BASH_REMATCH[1],,}"
				[[ $text =~ ${allow_re[$i]} ]]
				[[ ${BASH_REMATCH[0],,} == *"$term"* ]] || continue
			fi
			hit=$i
			break
		fi
	done
	if [ "$hit" -ge 0 ]; then
		allow_used[hit]=1
		allowed=$((allowed + 1))
	else
		printf '%s\t%s\t%s\t%s\n' "$check" "$path" "$line" "$reason" >>"$kept"
	fi
done <"$raw"
shopt -u nocasematch

# --- report ------------------------------------------------------------------

checks=(gate secrets private-modules identifiers operator-paths licence-files modification-notices clean-build upstream-name)
titles=(
	"0 archive and gate config"
	"1 secrets (tree + history)"
	"2 private modules and repositories"
	"3 organisation identifiers"
	"4 operator paths, hosts and names"
	"5 licence files"
	"6 per-file modification notices"
	"7 clean build (empty module cache)"
	"8 upstream name (never allowlisted)"
)

files=$(find "$tree" -type f | wc -l | tr -d ' ')
echo "release gate: $(git -C "$repo" rev-parse --short "$commit") ($rev), git archive of $files files"
total=0
counts=()
for i in "${!checks[@]}"; do
	c="${checks[$i]}"
	count=$(awk -F'\t' -v c="$c" '$1 == c' "$kept" | wc -l | tr -d ' ')
	counts+=("$count")
	total=$((total + count))
	[ "$count" -eq 0 ] && continue
	echo
	echo "== ${titles[$i]}: $count"
	awk -F'\t' -v c="$c" '$1 == c { printf "  %s:%s: %s\n", $2, $3, $4 }' "$kept" | sort -t: -k1,1 -k2,2n
done

for i in "${!allow_check[@]}"; do
	if [ "${allow_used[$i]}" -eq 0 ]; then
		echo
		echo "note: allowlist entry at ${allowlist#"$repo"/}:${allow_line[$i]} matched nothing"
	fi
done

echo
echo "summary"
for i in "${!checks[@]}"; do
	printf '  %-40s %5d\n' "${titles[$i]}" "${counts[$i]}"
done
printf '  %-40s %5d\n' "total" "$total"
printf '  %-40s %5d\n' "allowlisted" "$allowed"

if [ "$total" -gt 0 ]; then
	echo "FAIL"
	exit 1
fi
echo "PASS"
