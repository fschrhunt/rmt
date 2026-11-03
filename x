#!/bin/sh
# Run the same development checks locally and in CI.
set -eu
cd "$(dirname "$0")"

# Describe the supported development commands.
usage() {
    cat <<'USAGE'
usage: ./x [command] [args...]
  check           Default; check formatting, vet, tests, and shell scripts
  fmt [--check]   Format Go files, or check formatting without changes
  lint            Run go vet
  test [args...]  Run go test (default: ./...)
  shell           Run ShellCheck on repository shell scripts
  build           Build dist/rmt without replacing the installed binary
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
    check|lint|shell|build|hooks|help|--help|-h) [ "$#" -eq 0 ] || invalid ;;
    fmt) [ "$#" -eq 0 ] || { [ "$#" -eq 1 ] && [ "$1" = --check ]; } || invalid ;;
esac
case "$command" in
    # Shell scripts run by tests are not Go source; force tests to notice script changes.
    check) ./x fmt --check; ./x lint; ./x test -count=1 ./...; ./x shell ;;
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
    shell)
        command -v shellcheck >/dev/null 2>&1 || { printf 'shellcheck is not on PATH\n' >&2; exit 2; }
        shellcheck x install.sh .githooks/pre-commit scripts/*.sh
        ;;
    build) mkdir -p dist; go build -o dist/rmt . ;;
    dev) exec go run . "$@" ;;
    hooks) git config core.hooksPath .githooks; printf 'Git hook installed.\n' ;;
    help|--help|-h) usage ;;
    *) invalid ;;
esac
