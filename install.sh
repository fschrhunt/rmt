#!/bin/sh
# Install a verified release without executing rmt or changing running services.
# Private releases cannot be downloaded anonymously. RMT_RELEASES can point to
# an accessible release mirror with the same /latest and /download layout.
set -eu
umask 077
LC_ALL=C
export LC_ALL
unset TAR_OPTIONS

releases=${RMT_RELEASES:-https://github.com/fschrhunt/rmt/releases}
releases=${releases%/}
dir=${HOME:?HOME must be set}/.local/bin
version=
work=
pending=

fail() { printf 'rmt install: %s\n' "$*" >&2; exit 1; }

# Remove only the private download directory and our unique destination file.
cleanup() {
    [ -z "$pending" ] || rm -f "$pending"
    [ -z "$work" ] || rm -rf "$work"
}
trap cleanup 0
trap 'exit 1' HUP INT TERM

# Work with piped input as well as direct invocation.
usage() {
    cat <<'USAGE'
usage: sh install.sh [--version vX.Y.Z] [--dir DIR]
  --version TAG  Install a stable tag without leading zeroes (default: latest)
  --dir DIR      Install rmt here (default: $HOME/.local/bin)
  -h, --help     Show this help

Requires curl, tar, mktemp, and sha256sum or shasum.
RMT_RELEASES overrides https://github.com/fschrhunt/rmt/releases for mirrors/tests.
For private releases, use an accessible mirror or download assets separately with gh.
The installer never runs rmt or stops/starts services.
USAGE
}

while [ "$#" -gt 0 ]; do
    case $1 in
        --version)
            [ "$#" -ge 2 ] || fail '--version needs a tag like v1.2.3'
            [ -n "$2" ] || fail '--version needs a tag like v1.2.3'
            version=$2; shift 2 ;;
        --version=*) version=${1#--version=}; [ -n "$version" ] || fail '--version needs a tag'; shift ;;
        --dir)
            [ "$#" -ge 2 ] || fail '--dir needs a folder'
            [ -n "$2" ] || fail '--dir needs a folder'
            dir=$2; shift 2 ;;
        --dir=*) dir=${1#--dir=}; [ -n "$dir" ] || fail '--dir needs a folder'; shift ;;
        -h|--help) usage; exit 0 ;;
        *) fail "unknown option $1; use --help" ;;
    esac
done

command -v curl >/dev/null 2>&1 || fail 'curl is required'
case $releases in http://*|https://*) ;; *) fail 'RMT_RELEASES must be an HTTP(S) release URL' ;; esac
if [ -z "$version" ]; then
    # Pin all subsequent downloads to the tag reached by the latest redirect.
    landing=$(curl -q -fsSLI --proto '=http,https' --proto-redir '=http,https' \
        -o /dev/null -w '%{url_effective}' -- "$releases/latest") ||
        fail 'could not resolve latest release; private releases need access or an RMT_RELEASES mirror'
    version=${landing##*/}
fi
# Check the whole value, including embedded newlines, against the release tag rule.
printf '%s\n' "$version" | awk '
    NR != 1 || $0 !~ /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/ { bad = 1 }
    END { exit (bad || NR != 1) }
' || fail 'expected a stable tag vX.Y.Z without leading zeroes'

case $(uname -s) in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    *) fail 'releases support macOS and Linux' ;;
esac
case $(uname -m) in
    arm64|aarch64) arch=arm64 ;;
    x86_64|amd64) arch=amd64 ;;
    *) fail 'releases support arm64 and amd64' ;;
esac

# Absolute paths also keep names beginning with a dash out of utility options.
case $dir in /*) ;; *) dir=$PWD/$dir ;; esac
target=$dir/rmt

# A package manager's symlink (including a dangling one) must never be replaced.
check_target() {
    [ ! -L "$target" ] || fail "$target is a symlink; choose another --dir or use its package manager"
    if [ -e "$target" ] && [ ! -f "$target" ]; then
        fail "$target is not a regular file"
    fi
}
check_target

tempdir=${TMPDIR:-/tmp}
case $tempdir in /*) ;; *) tempdir=$PWD/$tempdir ;; esac
work=$(mktemp -d "$tempdir/rmt-install.XXXXXX") || fail 'could not create download directory'
archive=rmt_${version}_${os}_${arch}.tar.gz

# Ignore curl configuration and accept only HTTP(S), including redirects.
fetch() {
    curl -q -fsSL --proto '=http,https' --proto-redir '=http,https' -o "$2" -- "$1"
}
printf 'Installing rmt %s for %s/%s\n' "$version" "$os" "$arch"
fetch "$releases/download/$version/$archive" "$work/$archive" ||
    fail "could not download $archive; private releases need access or an RMT_RELEASES mirror"
fetch "$releases/download/$version/checksums.txt" "$work/checksums.txt" || fail 'could not download checksums.txt'

# Select exactly one complete checksum record for this exact asset, never a suffix.
expected=$(awk -v asset="$archive" '
    $2 == asset || $2 == "*" asset {
        count++
        if (NF != 2 || length($1) != 64 || $1 ~ /[^0-9a-fA-F]/) bad = 1
        hash = tolower($1)
    }
    END { if (count != 1 || bad) exit 1; print hash }
' "$work/checksums.txt") || fail "missing, duplicate or malformed checksum for $archive"
if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum < "$work/$archive") || fail 'could not compute SHA-256'
elif command -v shasum >/dev/null 2>&1; then
    actual=$(shasum -a 256 < "$work/$archive") || fail 'could not compute SHA-256'
else
    fail 'sha256sum or shasum is required'
fi
[ "$expected" = "${actual%% *}" ] || fail "checksum mismatch for $archive; refusing to install"

# Releases contain exactly two regular files. Reject paths, links, extra members
# and duplicates before reading the binary. Never unpack archive paths to disk.
tar -tzf "$work/$archive" > "$work/members" || fail 'could not list release archive'
awk '
    $0 == "rmt" { binary++; next }
    $0 == "LICENSE" { license++; next }
    { bad = 1 }
    END { exit (bad || binary != 1 || license != 1) }
' "$work/members" || fail 'unsafe archive members; expected only rmt and LICENSE'
tar -tvzf "$work/$archive" > "$work/types" || fail 'could not inspect release archive'
awk '
    substr($0, 1, 1) != "-" { bad = 1 }
    END { exit (bad || NR != 2) }
' "$work/types" || fail 'unsafe archive; rmt and LICENSE must be regular files'
tar -xOzf "$work/$archive" rmt > "$work/rmt" || fail 'could not read rmt from archive'
[ -s "$work/rmt" ] || fail 'archive has an empty rmt binary'

# Stage on the destination filesystem so the final rename is atomic. Nothing
# touches an existing binary until the verified replacement has its final mode.
mkdir -p "$dir" || fail "could not create $dir"
pending=$(mktemp "$dir/.rmt-install.XXXXXX") || fail 'could not create destination temporary file'
cp "$work/rmt" "$pending" || fail 'could not stage rmt'
chmod 755 "$pending" || fail 'could not make rmt executable'
check_target
mv -f "$pending" "$target" || fail 'could not replace rmt'
pending=
printf 'Installed %s\n' "$target"
case :$PATH: in
    *":$dir:"*) ;;
    *) printf 'Add %s to your PATH to use rmt.\n' "$dir" ;;
esac
