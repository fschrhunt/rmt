#!/bin/sh
# Build and verify the four release archives and their SHA-256 checksums.
set -eu

fail() { printf 'package: %s\n' "$*" >&2; exit 1; }
[ "$#" -eq 2 ] || fail 'usage: .github/release/package.sh vX.Y.Z OUTPUT_DIRECTORY'
tag=$1
out=$2
case $tag in *[!v0-9.]* | '') fail 'invalid version' ;; esac
printf '%s\n' "$tag" | LC_ALL=C grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' || fail 'invalid version'
[ -n "$out" ] || fail 'output directory is empty'
mkdir -p "$out"
work=$(mktemp -d "${TMPDIR:-/tmp}/rmt-package.XXXXXX")
trap 'rm -rf "$work"' EXIT
export CGO_ENABLED=0

for os in darwin linux; do
    for arch in amd64 arm64; do
        target="rmt_${tag}_${os}_${arch}"
        stage="$work/$target"
        unpack="$work/verify-$target"
        archive="$out/$target.tar.gz"
        mkdir -p "$stage" "$unpack"
        GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags='-s -w' -o "$stage/rmt" .
        cp LICENSE "$stage/"
        tar -C "$stage" -czf "$archive" rmt LICENSE
        printf '%s\n' rmt LICENSE > "$work/expected"
        tar -tzf "$archive" > "$work/members"
        diff -u "$work/expected" "$work/members"
        tar -C "$unpack" -xzf "$archive"
        [ -x "$unpack/rmt" ]
        cmp "$stage/rmt" "$unpack/rmt"
        cmp LICENSE "$unpack/LICENSE"
        go version -m "$unpack/rmt" > "$work/metadata"
        grep -q "GOOS=$os\$" "$work/metadata"
        grep -q "GOARCH=$arch\$" "$work/metadata"
        grep -q 'CGO_ENABLED=0$' "$work/metadata"
    done
done
(
    cd "$out"
    sha256sum -- "rmt_${tag}_darwin_amd64.tar.gz" "rmt_${tag}_darwin_arm64.tar.gz" \
        "rmt_${tag}_linux_amd64.tar.gz" "rmt_${tag}_linux_arm64.tar.gz" > checksums.txt
    sha256sum --check checksums.txt
)
