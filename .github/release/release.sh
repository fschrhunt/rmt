#!/bin/sh
# Write a release's notes, then ask Actions to release the current main commit.
# The notes start as a draft (the PR titles since the last release) and open in
# your editor; what you save becomes the GitHub release and its CHANGELOG.md
# entry. With --dry-run the draft is used as is and nothing is published.
#
# usage: .github/release/release.sh [--dry-run] vX.Y.Z
set -eu

fail() { printf 'release: %s\n' "$*" >&2; exit 1; }
dry_run=false
if [ "${1-}" = --dry-run ]; then dry_run=true; shift; fi
[ "$#" -eq 1 ] || fail 'usage: .github/release/release.sh [--dry-run] vX.Y.Z'
tag=$1
printf '%s\n' "$tag" | LC_ALL=C grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' ||
    fail 'expected a stable version such as v1.2.3, without leading zeros'
command -v gh >/dev/null 2>&1 || fail 'install the GitHub CLI (gh)'
cd "$(git rev-parse --show-toplevel)"
[ "$(git branch --show-current)" = main ] || fail 'run this from main'
[ -z "$(git status --porcelain)" ] || fail 'commit or set aside your changes first'
git fetch -q origin main --tags
[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] || fail 'update main from origin first'
git show-ref --verify --quiet "refs/tags/$tag" && fail "$tag already exists"

commit=$(git rev-parse HEAD)
result=$(gh run list --workflow ci.yml --branch main --event push --commit "$commit" \
    --json status,conclusion --jq '.[0] | "\(.status) \(.conclusion)"')
[ "$result" = 'completed success' ] || fail "main CI for $commit has not passed: ${result:-not found}"

# The draft: notes still under Unreleased from before releases wrote the
# changelog, otherwise the PR titles merged since the last release. Release
# tags sit on their own commit beside main, so count from where it branched.
notes=$(mktemp "${TMPDIR:-/tmp}/release-notes.XXXXXX")
trap 'rm -f "$notes"' EXIT
awk '/^## / { s = ($0 == "## Unreleased"); next } s' CHANGELOG.md | sed '/./,$!d' > "$notes"
if [ ! -s "$notes" ]; then
    last=$(git tag --list 'v*' --sort=-v:refname | head -n 1)
    range=HEAD
    [ -z "$last" ] || range="$(git merge-base "$last" HEAD)..HEAD"
    git log --no-merges --invert-grep --grep='^release: ' --format='- %s' "$range" > "$notes"
fi
if [ "$dry_run" = false ]; then
    ${VISUAL:-${EDITOR:-vi}} "$notes"
fi
grep -q '[^[:space:]]' "$notes" || fail 'the release notes are empty'

gh workflow run release.yml --ref main -f "tag=$tag" -f "commit=$commit" \
    -f "notes=$(cat "$notes")" -f "dry_run=$dry_run"
printf 'Requested %s for %s at %s. Watch it in GitHub Actions.\n' \
    "$(if [ "$dry_run" = true ]; then echo 'a release dry run'; else echo 'a release'; fi)" "$tag" "$commit"
