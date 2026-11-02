#!/bin/sh
# Run the same development checks locally and in CI.
set -eu
cd "$(dirname "$0")"

# Describe the supported development commands.
usage() {
    cat <<'USAGE'
usage: ./x [command] [args...]
  check           Default; check formatting, run go vet and tests
  fmt [--check]   Format Go files, or check formatting without changes
  lint            Run go vet
  test [args...]  Run go test (default: ./...)
  build           Build dist/remote without replacing the installed binary
  dev [args...]   Run this checkout with go run
  hooks           Install the repository's Git hook
  help            Show this help
USAGE
}

# Reject invalid arguments before running commands that change files.
invalid() { usage >&2; exit 2; }
command=${1-check}
if [ "$#" -gt 0 ]; then shift; fi
case "$command" in
    check|lint|build|hooks|help|--help|-h) [ "$#" -eq 0 ] || invalid ;;
    fmt) [ "$#" -eq 0 ] || { [ "$#" -eq 1 ] && [ "$1" = --check ]; } || invalid ;;
esac
case "$command" in
    check) ./x fmt --check; ./x lint; ./x test ;;
    fmt)
        if [ "${1:-}" = --check ]; then
            unformatted=$(gofmt -l .)
            if [ -n "$unformatted" ]; then
                printf 'format these Go files with gofmt:\n%s\n' "$unformatted" >&2
                exit 1
            fi
        else
            gofmt -w .
        fi
        ;;
    lint) go vet ./... ;;
    test) if [ "$#" -eq 0 ]; then set -- ./...; fi; go test "$@" ;;
    build) mkdir -p dist; go build -o dist/remote . ;;
    dev) exec go run . "$@" ;;
    hooks) git config core.hooksPath .githooks; printf 'Git hook installed.\n' ;;
    help|--help|-h) usage ;;
    *) invalid ;;
esac
