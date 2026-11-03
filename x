#!/bin/sh
# Run the development commands in scripts from any working directory.
set -eu
exec "$(dirname "$0")/scripts/dev.sh" "$@"
