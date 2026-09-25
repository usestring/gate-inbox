#!/usr/bin/env bash
# Keep Apache-2.0 §4(b) modification notices on every upstream file this fork
# has changed.
#
# Gate Inbox is a fork of the upstream project named in NOTICE, at the commit
# NOTICE pins. The licence asks that a modified file say so, so each one
# carries this line at its top, in the file's own comment syntax:
#
#   Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.
#
# Which files: every path whose content differs from the pinned upstream tree
# (git diff -M against the pin; a file moved without a content change is not
# modified), plus the files listed in derived below -- files the fork added at
# a new path that nonetheless carry upstream code moved out of an upstream
# file, which a rename detector cannot see. Files the fork wrote from scratch
# are its own and carry no notice. A file deleted since drops out of the list.
#
# Run with no arguments to add missing notices, replace any earlier wording of
# the notice with the current one, and rewrite LICENSES/MODIFIED-FILES.txt.
# Running it again changes nothing. Run with --check to change nothing and
# exit non-zero, naming each file that is missing its notice or a stale list.
#
# NOTICE supplies the upstream repository URL and the full pinned commit. The
# pin is fetched from that URL when this clone does not have it; when it
# cannot be fetched, --check falls back to the committed list.
set -euo pipefail

notice="Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE."
# Every wording of the notice, current or earlier, starts with this.
notice_prefix="Modified by Durable Alpha, 2026: changes from "
list_file=LICENSES/MODIFIED-FILES.txt

# Added at a new path, but built from code moved out of the upstream file in
# the trailing comment. Found by matching lines of 30 or more characters that
# occur in exactly one upstream file; five or more from one file counts.
derived=(
	app/app.go                                # main.go
	app/app_test.go                           # main_test.go
	app/board.go                              # main.go
	app/version.go                            # main.go
	extension/artifacts/artifacts.go          # internal/mcpserver/mcpserver.go
	internal/cli/rename.go                    # internal/cli/review.go
	internal/sessioncmd/accounts.go           # internal/sessioncmd/session.go
	internal/sessioncmd/migrate.go            # internal/sessioncmd/session.go
	internal/sessioncmd/park.go               # internal/sessioncmd/session.go
	internal/sessioncmd/sendhold_test.go      # internal/sessioncmd/session_test.go
	internal/store/inboxsubject_test.go       # internal/store/inbox_test.go
	internal/tmux/asyncsubmit_test.go         # internal/tmux/tmux_test.go
	internal/tmux/pin_test.go                 # internal/tmux/tmux_test.go
	internal/tmux/shared_test.go              # internal/tmux/tmux_test.go
	internal/tmux/target_test.go              # internal/tmux/tmux_test.go
	internal/ui/asyncsend_test.go             # internal/ui/inbox_test.go
	internal/ui/fleet_bench_test.go           # internal/ui/view_test.go
	internal/ui/links.go                      # internal/ui/notices.go
	internal/ui/links_test.go                 # internal/ui/notices_test.go
	internal/ui/panestate.go                  # internal/ui/focuswatch.go
	internal/ui/welcome.go                    # internal/ui/help.go
)

# Modified, but with nowhere to put the line: go.sum has no comment syntax, a
# PNG is binary, and NOTICE is itself the distribution's statement of
# modification.
exempt=(go.sum NOTICE docs/brand/mark-512.png)

usage() {
	echo "usage: $0 [--check]" >&2
	exit 2
}

mode="write"
case "${1:-}" in
"") ;;
--check) mode=check ;;
*) usage ;;
esac
[ $# -le 1 ] || usage

cd "$(git rev-parse --show-toplevel)"

[ -f NOTICE ] || {
	echo "modification-notices: NOTICE is missing" >&2
	exit 2
}
# The pin is the one full 40-character commit NOTICE names, and the URL the
# one http(s) URL it names.
mapfile -t pins < <(grep -oE '\b[0-9a-f]{40}\b' NOTICE | sort -u)
if [ ${#pins[@]} -ne 1 ]; then
	echo "modification-notices: NOTICE must name exactly one full 40-character upstream commit; it names ${#pins[@]}" >&2
	exit 2
fi
upstream_pin=${pins[0]}
mapfile -t urls < <(grep -oE 'https?://[^[:space:]<>]+' NOTICE | sed 's/[.,;:)]*$//' | sort -u)
if [ ${#urls[@]} -gt 1 ]; then
	echo "modification-notices: NOTICE names ${#urls[@]} URLs; it must name only the upstream repository" >&2
	exit 2
fi
upstream_url=${urls[0]:-}

# The notice line for one path, or nothing when its type takes no comment.
notice_for() {
	case "$1" in
	*.go | go.mod | *.js | *.mjs | *.ts | *.tsx) printf '// %s\n' "$notice" ;;
	*.css) printf '/* %s */\n' "$notice" ;;
	*.md | *.html | *.svg) printf '<!-- %s -->\n' "$notice" ;;
	*.sh | *.bash | *.yml | *.yaml | *.toml | .gitignore | */.gitignore | Makefile | */Makefile)
		printf '# %s\n' "$notice"
		;;
	esac
}

has_notice() {
	head -n 5 "$1" | grep -qxF "$2"
}

# The number of the first of the top five lines that carries an earlier
# wording of the notice, or nothing.
old_notice_line() {
	head -n 5 "$1" | awk -v prefix="$notice_prefix" -v line="$2" \
		'index($0, prefix) && $0 != line { print NR; exit }'
}

replace_notice() {
	local path=$1 at=$2 line=$3 tmp
	tmp=$(mktemp "${TMPDIR:-/tmp}/modification-notice.XXXXXX")
	awk -v at="$at" -v line="$line" 'NR == at { print line; next } { print }' "$path" >"$tmp"
	cat "$tmp" >"$path"
	rm -f "$tmp"
}

add_notice() {
	local path=$1 line=$2 tmp
	tmp=$(mktemp "${TMPDIR:-/tmp}/modification-notice.XXXXXX")
	if head -n 1 "$path" | grep -q '^#!'; then
		{ head -n 1 "$path"; printf '%s\n' "$line"; tail -n +2 "$path"; } >"$tmp"
	else
		{ printf '%s\n\n' "$line"; cat "$path"; } >"$tmp"
	fi
	# cat rather than mv, so the file keeps its mode.
	cat "$tmp" >"$path"
	rm -f "$tmp"
}

have_pin() {
	git cat-file -e "$upstream_pin^{commit}" 2>/dev/null && return 0
	if [ -z "$upstream_url" ]; then
		echo "modification-notices: NOTICE names no upstream repository URL to fetch $upstream_pin from" >&2
		return 1
	fi
	git fetch --quiet --no-tags "$upstream_url" "$upstream_pin" 2>/dev/null
}

# Every path that must carry the notice, sorted.
compute_list() {
	{
		git diff --no-color --name-status -M "$upstream_pin" -- |
			awk -F '\t' '$1 ~ /^[MT]/ { print $2 } $1 ~ /^R/ && $1 != "R100" { print $3 }'
		local path
		for path in "${derived[@]}"; do
			[ ! -e "$path" ] || echo "$path"
		done
	} | grep -vxF -f <(printf '%s\n' "${exempt[@]}") | LC_ALL=C sort -u
}

status=0
for path in "${derived[@]}"; do
	if [ ! -e "$path" ]; then
		echo "derived file $path no longer exists; update derived in $0" >&2
		status=1
	fi
done

if have_pin; then
	expected=$(compute_list)
	if [ "$mode" = check ]; then
		stale=$(diff <(cat "$list_file" 2>/dev/null) <(printf '%s\n' "$expected") || true)
		if [ -n "$stale" ]; then
			echo "$list_file is stale; run $0 (< listed, > expected):" >&2
			echo "$stale" | grep '^[<>]' >&2
			status=1
		fi
	fi
else
	[ "$mode" = check ] || {
		echo "modification-notices: cannot fetch $upstream_pin from ${upstream_url:-upstream}" >&2
		exit 2
	}
	echo "modification-notices: upstream pin unavailable; checking the committed list only" >&2
	expected=$(cat "$list_file")
fi

missing=()
outdated=()
untyped=()
while IFS= read -r path; do
	[ -n "$path" ] || continue
	line=$(notice_for "$path")
	if [ -z "$line" ]; then
		untyped+=("$path")
		continue
	fi
	old=$(old_notice_line "$path" "$line")
	if [ -n "$old" ]; then
		if [ "$mode" = check ]; then
			outdated+=("$path")
		else
			replace_notice "$path" "$old" "$line"
			echo "replaced notice: $path"
		fi
		continue
	fi
	has_notice "$path" "$line" && continue
	if [ "$mode" = check ]; then
		missing+=("$path")
	else
		add_notice "$path" "$line"
		echo "added notice: $path"
	fi
done <<<"$expected"

if [ ${#untyped[@]} -gt 0 ]; then
	echo "no comment syntax known for these modified files; add one to notice_for or list them in exempt:" >&2
	printf '  %s\n' "${untyped[@]}" >&2
	status=1
fi
if [ ${#missing[@]} -gt 0 ]; then
	echo "missing the modification notice:" >&2
	printf '  %s\n' "${missing[@]}" >&2
	status=1
fi

if [ ${#outdated[@]} -gt 0 ]; then
	echo "carrying an earlier wording of the modification notice:" >&2
	printf '  %s\n' "${outdated[@]}" >&2
	status=1
fi

if [ "$mode" = write ]; then
	mkdir -p "$(dirname "$list_file")"
	printf '%s\n' "$expected" >"$list_file"
fi
exit "$status"
