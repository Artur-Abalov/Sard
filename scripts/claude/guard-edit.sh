#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# PreToolUse hook for Edit|Write: protects gate definitions and forbids
# silencing tests or linters. A tripwire for agents, not a security boundary.
set -euo pipefail

input="$(cat)"
path="$(jq -r '.tool_input.file_path // empty' <<<"$input")"
text="$(jq -r '[.tool_input.content, .tool_input.new_string] | map(select(. != null)) | join("\n")' <<<"$input")"

block() {
  echo "guard-edit: blocked — $1" >&2
  echo "Fix the root cause instead. If the rule itself must change, ask the user." >&2
  exit 2
}

protected='(^|/)(CLAUDE\.md|\.claude/settings\.json|scripts/claude/[^/]+|scripts/gate\.sh|scripts/crap\.sh|\.golangci\.yml|config/detekt\.yml)$'
if [ -n "$path" ] && [ -e "$path" ] && grep -Eq -- "$protected" <<<"$path"; then
  block "$path defines a quality gate and is protected"
fi

case "$path" in
  *.go | *.kt | *.kts | *.ts | *.tsx | *.js | *.mjs) ;;
  *) exit 0 ;;
esac

grep -Eq -- 't\.Skip(Now|f)?\(' <<<"$text" && block "skipping a Go test"
grep -Eq -- '@(Disabled|Ignore)\b' <<<"$text" && block "disabling a JUnit test"
grep -Eq -- '\b(it|test|describe)\.(skip|only|todo)\(|\bx(it|describe)\(' <<<"$text" && block "skipping or focusing a JS test"
grep -Eq -- '//[[:space:]]*nolint|eslint-disable|@Suppress\("(detekt|Complex|LongMethod|TooMany)' <<<"$text" && block "silencing a linter"

exit 0
