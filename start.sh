#!/bin/sh
# Portable entry point; Python owns initialization and Compose orchestration.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if ! command -v python3 >/dev/null 2>&1; then
  echo "Python 3 is required. Install it, then run ./start.sh again." >&2
  exit 1
fi
exec python3 "$ROOT/tools/local.py" "$@"
