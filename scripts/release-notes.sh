#!/usr/bin/env bash
#
# Build the GitHub Release body for a tag out of CHANGELOG.md.
#
#   scripts/release-notes.sh v0.3.0 > release-body.md
#
# The changelog section for the tag's version (Keep a Changelog style, e.g.
# "## [0.3.0] - 2026-10-17") is emitted verbatim, followed by a compare link
# against the previous stable tag. A tag without its own section — typically a
# release candidate cut before the entries were renamed — falls back to
# "## [Unreleased]" with a warning. Exits non-zero only when there is nothing
# to publish at all.
set -euo pipefail

cd "$(dirname "$0")/.."

tag="${1:?usage: release-notes.sh <tag> (e.g. v0.3.0)}"
version="${tag#v}"

# section <version> — print that version's changelog body without the heading,
# leading or trailing blank lines.
section() {
	awk -v ver="$1" '
		# Start of the wanted version section.
		index($0, "## [" ver "]") == 1 { found = 1; next }
		# The next version heading ends it.
		found && index($0, "## [") == 1 { exit }
		# So do the link reference definitions at the bottom of the file.
		found && $0 ~ /^\[[^]]+\]: / { exit }
		found {
			if (NF == 0 && !body) next                  # drop leading blanks
			body = 1
			lines[++n] = $0
		}
		END {
			while (n > 0 && lines[n] ~ /^[[:space:]]*$/) n--  # drop trailing blanks
			for (i = 1; i <= n; i++) print lines[i]
		}
	' CHANGELOG.md
}

notes=$(section "$version")
if [ -z "$notes" ]; then
	echo "release-notes.sh: CHANGELOG.md has no \"## [$version]\" section; using \"## [Unreleased]\"" >&2
	notes=$(section "Unreleased")
fi
if [ -z "$notes" ]; then
	echo "release-notes.sh: CHANGELOG.md has neither \"## [$version]\" nor \"## [Unreleased]\" contents" >&2
	exit 1
fi

printf '%s\n' "$notes"

# Compare against the previous stable tag: take our tag's place in the
# descending version order and pick the next entry that is not a prerelease
# (the hyphen only ever appears in a prerelease suffix, e.g. v0.3.0-rc1).
# A prerelease also skips its own base release, so v0.3.0-rc1 compares against
# v0.2.0 rather than against the v0.3.0 it is a candidate for.
base="${tag%%-*}"
prev=$(git tag --sort=-v:refname | awk -v tag="$tag" -v base="$base" '
	$0 == tag { seen = 1; next }
	seen && $0 !~ /-/ && $0 != base { print; exit }
')
if [ -n "$prev" ]; then
	printf '\n**Full changelog:** %s/%s/compare/%s...%s\n' \
		"${GITHUB_SERVER_URL:-https://github.com}" \
		"${GITHUB_REPOSITORY:-AkisAya/agent-session-viewer}" \
		"$prev" "$tag"
fi
