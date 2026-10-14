#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Tests scripts/e2e-shard.sh, the split of the e2e classes over CI runners:
#
#   1. list names every *Test.kt class of the sources, by its package;
#   2. the shards of n cover every class exactly once, for n = 1..4, and 1/1
#      is the whole list;
#   3. the heaviest class goes to the lightest shard, a class without a weight
#      weighs the mean;
#   4. an empty shard and a malformed shard are refused;
#   5. missing names the classes without a JUnit report and fails, and passes
#      when every class has one;
#   6. times prints seconds per class, slowest first, and weights (--tsv) in
#      the format shard reads.
#
#   scripts/test-e2e-shard.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SHARD="$ROOT/scripts/e2e-shard.sh"
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
src="$work/src/dev/sard/e2e"
mkdir -p "$src" "$work/src/dev/sard/e2e/other"
for c in ATest BTest CTest DTest; do printf 'package dev.sard.e2e\n\nclass %s\n' "$c" >"$src/$c.kt"; done
printf 'package dev.sard.e2e.other\n\nclass ETest\n' >"$work/src/dev/sard/e2e/other/ETest.kt"
printf 'package dev.sard.e2e\n\nobject Helper\n' >"$src/Helper.kt"
printf '# seconds\tclass\n10\tdev.sard.e2e.ATest\n6\tdev.sard.e2e.BTest\n5\tdev.sard.e2e.CTest\n4\tdev.sard.e2e.DTest\n' >"$work/weights.tsv"
export E2E_SRC="$work/src" E2E_WEIGHTS="$work/weights.tsv"

shard() { "$SHARD" "$@" | paste -sd' ' -; }

# 1
expect list "dev.sard.e2e.ATest dev.sard.e2e.BTest dev.sard.e2e.CTest dev.sard.e2e.DTest dev.sard.e2e.other.ETest" "$(shard list)"

# 2
all="$("$SHARD" list | sort)"
expect "1/1" "$(shard list)" "$("$SHARD" 1/1 | sort | paste -sd' ' -)"
for n in 1 2 3 4; do
  got="$(for i in $(seq 1 "$n"); do "$SHARD" "$i/$n"; done | sort)"
  expect "cover $n" "$all" "$got"
done

# 3: weights A 10, B 6, C 5, D 4, ETest the mean 6.25: A→1, E→2, B→2, C→1, D→2
expect "2 shards, 1 of 2" "dev.sard.e2e.ATest dev.sard.e2e.CTest" "$(shard 1/2)"
expect "2 shards, 2 of 2" "dev.sard.e2e.other.ETest dev.sard.e2e.BTest dev.sard.e2e.DTest" "$(shard 2/2)"

# 4
if "$SHARD" 6/6 >/dev/null 2>&1; then fail "empty shard 6/6 accepted"; fi
for bad in 0/3 4/3 3 x/2 1/0 ""; do
  "$SHARD" "$bad" >/dev/null 2>&1 && fail "shard [$bad] accepted"
  [ "$("$SHARD" "$bad" >/dev/null 2>&1; echo $?)" = 2 ] || fail "shard [$bad]: want exit 2"
done

# 5
xml="$work/xml"
mkdir -p "$xml"
report() { # report <class> <seconds> <tests>
  printf '<?xml version="1.0"?>\n<testsuite name="%s" tests="%s" time="%s"></testsuite>\n' "$1" "$3" "$2" >"$xml/TEST-$1.xml"
}
report dev.sard.e2e.ATest 10.5 3
report dev.sard.e2e.BTest 2.25 1
report dev.sard.e2e.CTest 30 2
if out="$("$SHARD" missing "$xml" 2>&1)"; then fail "missing passed with two classes unreported"; fi
expect "missing names" "dev.sard.e2e.DTest dev.sard.e2e.other.ETest" "$(printf '%s\n' "$out" | grep '^dev' | paste -sd' ' -)"
report dev.sard.e2e.DTest 1 1
report dev.sard.e2e.other.ETest 0 1
"$SHARD" missing "$xml" >/dev/null || fail "missing failed with every class reported"

# 6
expect "times" "30.0 s 2 tests dev.sard.e2e.CTest|10.5 s 3 tests dev.sard.e2e.ATest|2.2 s 1 tests dev.sard.e2e.BTest" \
  "$("$SHARD" times "$xml" | head -3 | tr -s ' ' | sed 's/^ //' | paste -sd'|' -)"
expect "times total" "43.8 s total in 5 classes" "$("$SHARD" times "$xml" | tail -1 | tr -s ' ' | sed 's/^ //')"
"$SHARD" times --tsv "$xml" >"$work/new.tsv"
expect "tsv weights" "30	dev.sard.e2e.CTest" "$(grep CTest "$work/new.tsv")"
E2E_WEIGHTS="$work/new.tsv" "$SHARD" 1/2 | grep -qx dev.sard.e2e.CTest || fail "tsv weights not read by shard"

if [ "$failures" -gt 0 ]; then
  echo "test-e2e-shard: $failures failure(s)" >&2
  exit 1
fi
echo "test-e2e-shard: ok"
