#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Characterization tests for the helper scripts the agent pipeline uses:
# scripts/claude/{arch-edges,docs-index,test-quiet,gate-changed}.sh.
# Needs go, git, jq and make. Not part of `make gate`; run it by hand.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
failed=0

ok() { echo "ok   $1"; }
bad() {
  echo "FAIL $1"
  failed=1
}
check() { # check <name> <command...>
  local name="$1"
  shift
  if "$@" >/dev/null 2>&1; then ok "$name"; else bad "$name"; fi
}

# arch-edges.sh -------------------------------------------------------------
out="$(CLAUDE_PROJECT_DIR="$ROOT" "$ROOT/scripts/claude/arch-edges.sh")" || bad "arch-edges exits 0 on the real tree"
check "arch-edges: edge lines are 'pkg -> pkg'" \
  bash -c 'grep -q " -> " <<<"$1" && ! grep -v -E "^(PASS |domain: clean$|[a-z0-9/._-]+ -> [a-z0-9/._-]+$)" <<<"$1"' _ "$out"
for rule in "sdk imports no AGPL package" "proto imports no AGPL package" "agent opens no listener"; do
  check "arch-edges: PASS $rule" grep -qxF "PASS $rule" <<<"$out"
done
check "arch-edges: domain clean" grep -qxF "domain: clean" <<<"$out"

# A tree with one deliberate violation per rule.
t="$tmp/tree"
mkdir -p "$t/proto" "$t/server/src/main/kotlin/dev/sard/server/workflow"
cp "$ROOT/go.work" "$t/"
cp -r "$ROOT/agent" "$ROOT/cli" "$t/"
cp -r "$ROOT/proto/gen" "$t/proto/gen"
cat >"$t/agent/plugins/sdk/planted.go" <<'EOF'
// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk

import _ "github.com/Artur-Abalov/sard/agent/internal/role"
EOF
cat >"$t/agent/internal/role/planted_listener.go" <<'EOF'
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package role

import "net"

var _, _ = net.Listen("tcp", ":0")
EOF
printf 'package x\nimport org.springframework.stereotype.Service\n' \
  >"$t/server/src/main/kotlin/dev/sard/server/workflow/Bad.kt"
if bad_out="$(CLAUDE_PROJECT_DIR="$t" "$ROOT/scripts/claude/arch-edges.sh")"; then
  bad "arch-edges exits non-zero on a violating tree"
else ok "arch-edges exits non-zero on a violating tree"; fi
check "arch-edges: FAIL sdk rule on planted edge" grep -qxF "FAIL sdk imports no AGPL package" <<<"$bad_out"
check "arch-edges: FAIL listener rule on planted listener" grep -qxF "FAIL agent opens no listener" <<<"$bad_out"
check "arch-edges: planted server import reported" grep -q "Bad.kt:2:import org.springframework" <<<"$bad_out"
check "arch-edges: proto rule still PASS" grep -qxF "PASS proto imports no AGPL package" <<<"$bad_out"

# docs-index.sh -------------------------------------------------------------
d="$tmp/docs"
mkdir -p "$d/docs/specs/x" "$d/docs/sessions"
printf 'Feature: One thing\n  Scenario: a\n  Scenario: b\n' >"$d/docs/specs/x/one.feature"
printf '# Session one\n' >"$d/docs/sessions/2026-01-01-one.md"
CLAUDE_PROJECT_DIR="$d" "$ROOT/scripts/claude/docs-index.sh"
check "docs-index: feature line has title and scenario count" grep -qF "| x/one.feature | One thing | 2 |" "$d/docs/specs/INDEX.md"
check "docs-index: session line has first heading" grep -qF "| 2026-01-01-one.md | Session one |" "$d/docs/sessions/INDEX.md"
before="$(cat "$d/docs/specs/INDEX.md" "$d/docs/sessions/INDEX.md" | sha256sum)"
CLAUDE_PROJECT_DIR="$d" "$ROOT/scripts/claude/docs-index.sh"
check "docs-index: idempotent" test "$before" = "$(cat "$d/docs/specs/INDEX.md" "$d/docs/sessions/INDEX.md" | sha256sum)"

# test-quiet.sh -------------------------------------------------------------
q="$tmp/q"
mkdir -p "$q"
cp -r "$ROOT/tools" "$q/tools"
check "test-quiet: green tools run prints nothing" test -z "$(CLAUDE_PROJECT_DIR="$q" "$ROOT/scripts/claude/test-quiet.sh" tools)"
printf '\nfunc TestPlantedFailure(t *testing.T) { t.Fatal("planted") }\n' >>"$q/tools/internal/crap/crap_test.go"
if q_out="$(CLAUDE_PROJECT_DIR="$q" "$ROOT/scripts/claude/test-quiet.sh" tools)"; then
  bad "test-quiet: failing test gives non-zero exit"
else ok "test-quiet: failing test gives non-zero exit"; fi
check "test-quiet: failure is printed" grep -q "TestPlantedFailure" <<<"$q_out"
check "test-quiet: unknown module is exit 2" test "$(CLAUDE_PROJECT_DIR="$q" "$ROOT/scripts/claude/test-quiet.sh" nope >/dev/null 2>&1 || echo $?)" = 2

# gate-changed.sh: skip by agent_type, cache of a passing tree ------------------
g="$tmp/g"
mkdir -p "$g/tools" "$g/scripts/claude"
git -C "$g" init -q .
git -C "$g" config user.email t@t
git -C "$g" config user.name t
cp "$ROOT/scripts/claude/gate-changed.sh" "$g/scripts/claude/"
printf 'gate-fast:\n\t@echo run >> calls.log; test ! -e fail\n' >"$g/Makefile"
printf 'build/\ncalls.log\nfail\n' >"$g/.gitignore"
echo 'package x' >"$g/tools/x.go"
git -C "$g" add -A
git -C "$g" commit -qm base
git -C "$g" update-ref refs/remotes/origin/main HEAD
echo '// c' >>"$g/tools/x.go"
gate() { printf '%s' "$1" | CLAUDE_PROJECT_DIR="$g" bash "$g/scripts/claude/gate-changed.sh" >/dev/null 2>&1 || true; }
calls() { if [ -e "$g/calls.log" ]; then wc -l <"$g/calls.log" | tr -d ' '; else echo 0; fi; }
gate '{"agent_type":"architect"}'
check "gate-changed: architect is skipped" test "$(calls)" = 0
gate '{"agent_type":"specifier"}'
check "gate-changed: specifier is skipped" test "$(calls)" = 0
gate '{"agent_type":""}'
check "gate-changed: empty agent_type still gates" test "$(calls)" = 1
gate '{"agent_type":"cleaner"}'
check "gate-changed: same passing tree is not gated twice" test "$(calls)" = 1
echo '// d' >>"$g/tools/x.go"
gate '{"agent_type":"coder"}'
check "gate-changed: a changed tree is gated" test "$(calls)" = 2
touch "$g/fail"
echo '// e' >>"$g/tools/x.go"
gate '{"agent_type":"coder"}'
gate '{"agent_type":"coder"}'
check "gate-changed: a failing gate is never cached" test "$(calls)" = 4

if [ "$failed" -ne 0 ]; then
  echo "test-claude-helpers: FAILED"
  exit 1
fi
echo "test-claude-helpers: all passed"
