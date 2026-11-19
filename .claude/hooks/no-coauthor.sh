#!/usr/bin/env bash
# Hard rule: commit messages must not contain Co-Authored-By trailers.
set -euo pipefail

INPUT=$(cat)
COMMAND=$(printf '%s' "$INPUT" | jq -r '.tool_input.command // empty')

if printf '%s' "$COMMAND" | grep -Eqi 'co-authored-by[[:space:]]*:'; then
  jq -n '{
    hookSpecificOutput: {
      hookEventName: "PreToolUse",
      permissionDecision: "deny",
      permissionDecisionReason: "Hard rule: no Co-Authored-By trailers in commit messages. Remove the trailer and commit again."
    }
  }'
fi

exit 0
