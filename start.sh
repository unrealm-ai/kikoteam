#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
exec /bin/sh "$root/tools/local-launcher.sh" "$@"
