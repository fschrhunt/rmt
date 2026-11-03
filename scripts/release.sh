#!/bin/sh
# Prepare a reviewed changelog release, then ask Actions to tag a checked main commit.
set -eu

fail() { printf 'release: %s\n' "$*" >&2; exit 1; }
[ "$#" -eq 1 ] || fail 'usage: scripts/release.sh vX.Y.Z'
tag=$1
case $tag in *[!v0-9.]* | '') fail 'expected a stable version such as v1.2.3' ;; esac
printf '%s\n' "$tag" | LC_ALL=C grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' ||
    fail 'expected a stable version such as v1.2.3, without leading zeros'
command -v gh >/dev/null 2>&1 || fail 'install the GitHub CLI (gh)'
cd "$(git rev-parse --show-toplevel)"
[ "$(git branch --show-current)" = main ] || fail 'run this from main'
[ -z "$(git status --porcelain)" ] || fail 'commit or set aside your changes first'
git fetch -q origin main --tags
[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] || fail 'update main from origin first'
git show-ref --verify --quiet "refs/tags/$tag" && fail "$tag already exists"

# Remove only the worktree created by this invocation.
cleanup() {
    if [ "$created" = true ]; then git worktree remove --force "$work" >/dev/null 2>&1 || :; fi
    rmdir "$work" >/dev/null 2>&1 || :
}

# A fresh version gets a changelog-only PR; main and the current checkout stay intact.
count=$(awk -v tag="$tag" '/^## / && $2 == tag { n++ } END { print n+0 }' CHANGELOG.md)
[ "$count" -le 1 ] || fail "CHANGELOG.md has duplicate $tag sections"
if [ "$count" -eq 0 ]; then
    awk '
        /^## / { in_notes = ($0 == "## Unreleased"); if (in_notes) count++; next }
        in_notes && /[^[:space:]]/ { body = 1 }
        END { exit !(count == 1 && body) }
    ' CHANGELOG.md || fail 'CHANGELOG.md needs one nonempty Unreleased section'
    branch="release/$tag"
    git show-ref --verify --quiet "refs/heads/$branch" && fail "$branch already exists locally"
    remote=$(git ls-remote --heads origin "refs/heads/$branch")
    [ -z "$remote" ] || fail "$branch already exists on origin"
    work=$(mktemp -d "${TMPDIR:-/tmp}/rmt-release.XXXXXX")
    created=false
    trap cleanup EXIT
    git worktree add -q -b "$branch" "$work" origin/main
    created=true
    date=$(date -u +%Y-%m-%d)
    awk -v tag="$tag" -v date="$date" '
        $0 == "## Unreleased" { print; print ""; print "## " tag " - " date; next }
        { print }
    ' CHANGELOG.md > "$work/CHANGELOG.md"
    git -C "$work" add CHANGELOG.md
    git -C "$work" -c core.hooksPath=/dev/null commit -qm "release: $tag"
    git -C "$work" push -q -u origin "$branch"
    (cd "$work" && gh pr create --base main --head "$branch" --title "release: $tag" \
        --body "Prepare the changelog for $tag. After merging, run scripts/release.sh $tag on main.")
    printf 'Merge the release PR, update main, then run scripts/release.sh %s again.\n' "$tag"
    exit 0
fi

# Dispatch only after the exact current main commit passes the push CI workflow.
awk -v tag="$tag" '
    /^## / { in_notes = ($2 == tag); next }
    in_notes && /[^[:space:]]/ { body = 1 }
    END { exit !body }
' CHANGELOG.md || fail "CHANGELOG.md has empty notes for $tag"
commit=$(git rev-parse HEAD)
result=$(gh run list --workflow ci.yml --branch main --event push --commit "$commit" \
    --json status,conclusion --jq '.[0] | "\(.status) \(.conclusion)"')
[ "$result" = 'completed success' ] || fail "main CI for $commit has not passed: ${result:-not found}"
gh workflow run release.yml --ref main -f "tag=$tag" -f "commit=$commit"
printf 'Requested release %s for %s. Watch it in GitHub Actions.\n' "$tag" "$commit"
