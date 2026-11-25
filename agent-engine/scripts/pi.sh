#!/bin/sh
# Add only supported CLI options; Pi remains unmodified.
if [ -n "${KIKO_SKILL_PATHS_FILE:-}" ]; then
  while IFS= read -r skill; do
    [ -z "$skill" ] || set -- "$@" --skill "$skill"
  done < "$KIKO_SKILL_PATHS_FILE"
fi
if [ -n "${KIKO_POLICY_EXTENSION:-}" ]; then
  set -- "$@" -e "$KIKO_POLICY_EXTENSION"
fi
if [ -n "${KIKO_HUMAN_TOOLS_EXTENSION:-}" ]; then
  set -- "$@" -e "$KIKO_HUMAN_TOOLS_EXTENSION"
fi
if [ "${KIKO_PLAN_ONLY:-}" = 1 ]; then
  exec "$KIKO_PI_BINARY" --no-builtin-tools --no-skills --no-prompt-templates --no-extensions "$@"
fi
exec "$KIKO_PI_BINARY" --no-builtin-tools --no-skills --no-prompt-templates --no-extensions -e builtin:mcp "$@"
