#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# PreToolUse hook for Bash: blocks commands that bypass quality gates or
# destroy work. A tripwire for agents, not a security boundary.
set -euo pipefail

cmd="$(jq -r '.tool_input.command // empty')"
[ -z "$cmd" ] && exit 0

block() {
  echo "guard: blocked — $1" >&2
  echo "If this is really needed, ask the user to run it themselves." >&2
  exit 2
}

# Skipping or excluding tests.
grep -Eq -- '(^|[[:space:]])-x[[:space:]]+[^[:space:]]*test' <<<"$cmd" && block "excluding a test task (-x test)"
grep -Eq -- '--exclude-task[[:space:]=]+[^[:space:]]*test' <<<"$cmd" && block "excluding a test task (--exclude-task)"
grep -Eq -- '-DskipTests|-Dmaven\.test\.skip' <<<"$cmd" && block "skipping tests"
grep -Eq -- 'go[[:space:]]+test[^|;&]*[[:space:]]-skip[[:space:]=]' <<<"$cmd" && block "go test -skip"

# Bypassing hooks and rewriting history.
grep -Eq -- '--no-verify' <<<"$cmd" && block "--no-verify bypasses git hooks"
grep -Eq -- 'git[[:space:]]+push[^|;&]*[[:space:]](--force([[:space:]]|$)|-f([[:space:]]|$))' <<<"$cmd" && block "force push (use --force-with-lease only with user approval)"

# Discarding uncommitted work.
grep -Eq -- 'git[[:space:]]+(checkout[[:space:]]+(--[[:space:]]|\.)|restore[[:space:]]|reset[[:space:]]+--hard|clean[[:space:]]+-[a-z]*f)' <<<"$cmd" && block "discarding uncommitted changes"

# rm -rf is allowed only on build output.
if grep -Eq -- '(^|[[:space:];&|])rm[[:space:]]+-[a-zA-Z]*r[a-zA-Z]*f|(^|[[:space:];&|])rm[[:space:]]+-[a-zA-Z]*f[a-zA-Z]*r' <<<"$cmd"; then
  allowed='^(\./)?([A-Za-z0-9_.-]+/)*(bin|build|dist|node_modules|\.gradle|\.bin|coverage|out|tmp)/?$'
  segment="$(grep -Eo -- 'rm[[:space:]]+-[a-zA-Z]+[^;&|]*' <<<"$cmd" | head -1)"
  for target in $segment; do
    case "$target" in rm | -*) continue ;; esac
    grep -Eq -- "$allowed" <<<"$target" || block "rm -rf outside build output: $target"
  done
fi

exit 0
