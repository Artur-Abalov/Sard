#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Tests scripts/lib/covering-tests.awk, the choice of the tests a Go mutant is
# run against first (docs/adr/0057-go-mutation-exec.md):
#
#   1. a changed line names the tests whose covered blocks contain it;
#   2. a line where two blocks meet names the tests of both;
#   3. a line outside every covered block names no test;
#   4. blocks of another file, or with a count of 0, never count;
#   5. an insertion names the tests of the lines on both sides of it, a
#      deletion and a change those of their whole range;
#   6. several hunks name the union, sorted, each test once.
#
#   scripts/test-covering-tests.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
AWK="$ROOT/scripts/lib/covering-tests.awk"
failures=0

fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}

expect() { # expect <name> <want> <got>
  [ "$2" = "$3" ] || fail "$1: want [$2], got [$3]"
}

work="$(mktemp -d)"
trap 'rm -r "$work"' EXIT
cat >"$work/index" <<'EOF'
TestA example.com/m/pkg/a.go:10.2,12.3 1 1
TestB example.com/m/pkg/a.go:12.3,20.2 2 1
TestC example.com/m/pkg/b.go:10.1,30.1 1 1
TestD example.com/m/pkg/a.go:30.1,31.1 1 0
TestE example.com/m/pkg/xa.go:1.1,99.1 1 1
TestF example.com/m/pkg/a.go:40.1,44.1 1 3
TestB example.com/m/pkg/a.go:40.1,44.1 1 1
EOF

# covering <diff of a.go>: the chosen tests, space separated.
covering() { printf '%s\n' "$1" | awk -f "$AWK" -v idx="$work/index" -v file=a.go | paste -sd' ' -; }

# 1
expect "line inside one block" "TestA" "$(covering '11c11')"
# 2
expect "line where two blocks meet" "TestA TestB" "$(covering '12c12')"
# 3
expect "line outside every block" "" "$(covering '25c25')"
# 4
expect "another file's block" "TestB" "$(covering '15c15')"
expect "block with count 0" "" "$(covering '30c30')"
# 5
expect "insertion after line 12" "TestA TestB" "$(covering '12a13,14')"
expect "insertion after line 20" "TestB" "$(covering '20a21')"
expect "deletion outside blocks" "" "$(covering '21,22d20')"
expect "deletion reaching a block" "TestB" "$(covering '19,21d18')"
expect "change of a range" "TestA TestB" "$(covering '9,13c9,14')"
# 6
expect "hunks, union sorted once" "TestA TestB TestF" "$(covering "$(printf '11c11\n< x\n---\n> y\n41c41\n< x\n---\n> y')")"

if [ "$failures" -gt 0 ]; then
  echo "test-covering-tests: $failures failed" >&2
  exit 1
fi
echo "test-covering-tests: all passed"
