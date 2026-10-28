#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# go-mutesting --exec command: runs the tests of the mutated package against
# one mutant (docs/adr/0057-go-mutation-exec.md).
#
# - The working tree is never written: the mutant is handed to the compiler
#   through `go test -overlay`, so several go-mutesting processes can share
#   one checkout and an interrupted run leaves no mutated source behind.
# - The timeout is measured, not fixed: the package's unmutated tests are run
#   once per process (baseline), and a mutant gets max(3 x baseline, 10 s).
#   A mutant that runs past it is killed (an endless loop is a detected
#   mutant), and reported on a "mutate-exec: TIMEOUT" line.
# - A package whose unmutated tests fail or hang cannot judge mutants: that is
#   an error (exit 3), never a kill.
# - Test binaries run with -test.failfast: the first failing test kills the
#   mutant, so no later test of the package is started for it. It changes the
#   time of a kill, never whether a mutant is killed.
# - Covering tests first: once per package and process every test runs alone
#   with coverage, child processes included (GOCOVERDIR), into a map of the
#   blocks each test covers. A mutant runs first against the tests covering
#   its changed lines (scripts/lib/covering-tests.awk); if one fails, the
#   mutant is killed. If they all pass, or no test covers the lines (package
#   level variables are in no block), every test runs, as before: a survivor
#   is always one that every test lets through. A test that fails or hangs
#   alone leaves the package without a map, reported on a "mutate-exec: NOMAP"
#   line; its mutants run against every test. Only a package whose unmutated
#   tests take SELECT_MIN_BASELINE_MS or more gets a map: below that the choice
#   costs more than it saves.
# - Test binaries run with at most MEMORY_LIMIT_MB of address space: a
#   mutant that allocates without end is killed by "out of memory", not the
#   runner by the kernel.
#
# Exit codes are go-mutesting's: 0 killed, 1 survived, 2 did not compile,
# 3 error. Input: MUTATE_ORIGINAL, MUTATE_CHANGED, MUTATE_PACKAGE (set by
# go-mutesting), MUTATE_STATE_DIR (a directory private to this go-mutesting
# process, set by scripts/gate.sh).
set -uo pipefail

TIMEOUT_FACTOR=3
TIMEOUT_MIN_S=10
BASELINE_LIMIT_S=600
# Address space of a test binary, baseline included: a mutant that allocates
# without end (an endless loop that appends) dies with "out of memory" instead
# of taking the host down. Every agent, sdk, cli and tools package passes
# well within it unmutated.
MEMORY_LIMIT_MB=3072
# The longest -test.run pattern of covering tests, in bytes; a longer choice
# runs every test. A single exec argument is limited to 128 KiB.
SELECT_MAX_BYTES=65536
# Measured 2026-10-28: the map pays off for cmd/sard-agent (~10 s), postgresql
# and executor; packages of tens of milliseconds ran twice as long with it.
SELECT_MIN_BASELINE_MS=1000

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

: "${MUTATE_ORIGINAL:?}" "${MUTATE_CHANGED:?}" "${MUTATE_PACKAGE:?}" "${MUTATE_STATE_DIR:?}"

say() { echo "mutate-exec: $*" >&2; }

now_ms() { echo $(($(date +%s%N) / 1000000)); }

# run_tests <binary> <dir> <limit_s> [flag...]: the test binary in its package
# directory, killed after limit_s. Prints the elapsed milliseconds; returns the
# binary's status, or 124 on timeout.
run_tests() {
  local bin="$1" in="$2" limit="$3" start status
  shift 3
  start="$(now_ms)"
  (ulimit -v $((MEMORY_LIMIT_MB * 1024)) && cd "$in" && timeout --signal=KILL "$limit" "$bin" -test.count=1 -test.timeout=0 -test.failfast "$@" >/dev/null 2>&1)
  status=$?
  echo $(($(now_ms) - start))
  [ "$status" -eq 137 ] && status=124
  return "$status"
}

# covering_map: every test of the package alone, with coverage, into
# $work/covering.idx ("<test> <cover profile line>" per covered block). Leaves
# no map if a test fails or hangs alone.
covering_map() {
  local start
  start="$(now_ms)"
  go test -c -cover -covermode=set -o "$work/cover.test" "$MUTATE_PACKAGE" >/dev/null 2>&1 &&
    (cd "$dir" && "$work/cover.test" -test.list '.*' >"$work/tests" 2>/dev/null) ||
    { say "NOMAP $MUTATE_PACKAGE: the coverage build or the test list failed"; return; }
  mkdir -p "$work/cover"
  if ! BIN="$work/cover.test" DIR="$dir" OUT="$work/cover" LIMIT="$BASELINE_LIMIT_S" MEM_KB=$((MEMORY_LIMIT_MB * 1024)) \
    xargs -d '\n' -P "$(nproc)" -I{} bash -c 'd="$OUT/$1" && mkdir -p "$d" && ulimit -v "$MEM_KB" && cd "$DIR" &&
      GOCOVERDIR="$d" timeout --signal=KILL "$LIMIT" "$BIN" -test.run "^$1\$" -test.count=1 -test.timeout=0 \
        -test.gocoverdir="$d" -test.coverprofile="$d.out" >/dev/null 2>&1 || { echo "$1"; exit 1; }' _ {} \
    <"$work/tests" >"$work/cover-failed"; then
    say "NOMAP $MUTATE_PACKAGE: fails or hangs alone: $(paste -sd' ' "$work/cover-failed")"
    return
  fi
  while IFS= read -r t; do
    awk -v t="$t" 'NR > 1 && $NF > 0 { print t, $0 }' "$work/cover/$t.out"
  done <"$work/tests" >"$work/covering.tmp"
  mv "$work/covering.tmp" "$work/covering.idx"
  say "COVERMAP $MUTATE_PACKAGE $(wc -l <"$work/tests") tests $(($(now_ms) - start))ms"
}

# judge <status>: a run of the mutant that failed kills it.
judge() {
  case $1 in
    0) return ;;
    124) say "TIMEOUT $MUTATE_PACKAGE $original after ${limit_s}s (baseline ${baseline_ms}ms)"; exit 0 ;;
    *) exit 0 ;;
  esac
}

key="$(printf '%s' "$MUTATE_PACKAGE" | md5sum | cut -c1-16)"
work="$MUTATE_STATE_DIR/$key"
mkdir -p "$work"
dir="$(go list -f '{{.Dir}}' "$MUTATE_PACKAGE")" || { say "ERROR cannot resolve $MUTATE_PACKAGE"; exit 3; }

# Baseline once per package and process: the unmutated tests must pass.
if [ ! -f "$work/baseline-ms" ]; then
  if ! go test -c -o "$work/base.test" "$MUTATE_PACKAGE" >"$work/base.log" 2>&1; then
    say "ERROR baseline build of $MUTATE_PACKAGE failed"; cat "$work/base.log" >&2; exit 3
  fi
  if [ ! -x "$work/base.test" ]; then
    echo 0 >"$work/baseline-ms" # no test files: every mutant survives
  elif ms="$(run_tests "$work/base.test" "$dir" "$BASELINE_LIMIT_S")"; then
    echo "$ms" >"$work/baseline-ms"
    say "BASELINE $MUTATE_PACKAGE ${ms}ms"
    [ "$ms" -ge "$SELECT_MIN_BASELINE_MS" ] && covering_map
  else
    say "ERROR unmutated tests of $MUTATE_PACKAGE fail or hang (${ms}ms); mutants cannot be judged"; exit 3
  fi
fi
baseline_ms="$(cat "$work/baseline-ms")"
limit_s=$(((baseline_ms * TIMEOUT_FACTOR + 999) / 1000))
[ "$limit_s" -lt "$TIMEOUT_MIN_S" ] && limit_s=$TIMEOUT_MIN_S

original="$(cd "$(dirname "$MUTATE_ORIGINAL")" && pwd)/$(basename "$MUTATE_ORIGINAL")"
changed="$(cd "$(dirname "$MUTATE_CHANGED")" && pwd)/$(basename "$MUTATE_CHANGED")"
# Names the mutant before it runs: if a run takes the whole host down, the
# last START line in a streamed log names the mutant that did it; the START
# times give scripts/mutation-weights.sh the cost of each file.
say "START $(now_ms) $changed"
printf '{"Replace":{"%s":"%s"}}\n' "$original" "$changed" >"$work/overlay.json"
rm -f "$work/mutant.test"
if ! go test -c -overlay "$work/overlay.json" -o "$work/mutant.test" "$MUTATE_PACKAGE" >/dev/null 2>&1; then
  exit 2
fi
[ -x "$work/mutant.test" ] || exit 1

# The covering tests first, unless they are none, all, or too many to name.
if [ -s "$work/covering.idx" ]; then
  diff "$original" "$changed" |
    awk -f "$ROOT/scripts/lib/covering-tests.awk" -v idx="$work/covering.idx" -v file="$(basename "$original")" >"$work/selected"
  selected="$(paste -sd'|' "$work/selected")"
  if [ -n "$selected" ] && [ "$(wc -l <"$work/selected")" -lt "$(wc -l <"$work/tests")" ] &&
    [ "$(printf '%s' "$selected" | wc -c)" -le "$SELECT_MAX_BYTES" ]; then
    run_tests "$work/mutant.test" "$dir" "$limit_s" -test.run "^($selected)\$" >/dev/null
    judge $?
  fi
fi

run_tests "$work/mutant.test" "$dir" "$limit_s" >/dev/null
judge $?
diff -u --label original --label mutant "$original" "$changed" >&2
exit 1
