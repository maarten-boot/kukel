#!/usr/bin/env bash
# release-notes.sh TAG [REPO]: the notes of the GitHub release of TAG, on
# standard output: the logo, the section of CHANGELOG.md for TAG (from its
# "## TAG" heading to the next "## "), and a link to the commits since the
# previous tag. A tag with a "-" (v1.5.0-rc1) takes the section of its
# version if there is one, else "Unreleased". Links in the section to files
# of the repository point to them at TAG, so that they work on the release
# page. Without a section for TAG it fails, so that no release goes out
# without its changes. Run it in the repository, with its tags
# (release.yml checks out with fetch-depth: 0).
set -euo pipefail

tag=${1:?usage: release-notes.sh TAG [REPO]}
repo=${2:-maarten-boot/kukel}

# The text of the section headed "## $1" (a heading "## v1.4.0 (date)").
section() {
	awk -v want="$1" '
		/^## / { if (on) exit; on = ($2 == want) ; next }
		on' CHANGELOG.md
}

text=$(section "$tag")
if [[ -z $text && $tag == *-* ]]; then
	text=$(section "${tag%%-*}")
	[[ -n $text ]] || text=$(section Unreleased)
fi
if [[ -z ${text//[$'\n ']/} ]]; then
	echo "release-notes.sh: CHANGELOG.md has no section \"## $tag\"" >&2
	exit 1
fi

# [text](path) to a file of the repository, not a URL or an anchor.
text=$(sed -E "s#\]\(([^):\#][^):]*)\)#](https://github.com/$repo/blob/$tag/\1)#g" <<<"$text")

# Each paragraph and list item on one line: GitHub shows a newline in
# release notes as a line break. Code blocks stay as they are.
text=$(awk '
	function flush() { if (cur != "") print cur; cur = "" }
	/^```/ { flush(); code = !code; print; next }
	code { print; next }
	/^[ \t]*$/ { flush(); print ""; next }
	/^[ \t]*- / || /^#/ { flush(); cur = $0; next } # an item, also a nested one (its indent kept)
	{ sub(/^[ \t]+/, ""); cur = (cur == "" ? $0 : cur " " $0) }
	END { flush() }' <<<"$text")

echo "<p align=\"center\"><img src=\"https://raw.githubusercontent.com/$repo/$tag/docs/Kukel-text-small.png\" alt=\"The Kukel logo\" width=\"120\"></p>"
echo
printf '%s\n' "$text" | sed -e '/./,$!d' # without the empty lines at the start
if prev=$(git describe --tags --abbrev=0 "$tag^" 2>/dev/null); then
	echo
	echo "**Full Changelog**: https://github.com/$repo/compare/$prev...$tag"
fi
