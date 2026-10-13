#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# go-mutesting --exec command: runs the tests of the mutated package against
# one mutant (docs/adr/00XX-draft-go-mutation-exec.md).
#
# - The working tree is never written: the mutant is handed to the compiler
#   through `go test -overlay`, so several go-mutesting processes can share
#   one checkout and an interrupted run leaves no mutated source behind.
# - The timeout is measured, not fixed: the package's unmutated tests are run
#   once per process (baseline), and a mutant gets max(3 x baseline, 30 s).
#   A mutant that runs past it is killed (an endless loop is a detected
#   mutant), and reported on a "mutate-exec: TIMEOUT" line.
# - A package whose unmutated tests fail or hang cannot judge mutants: that is
#   an error (exit 3), never a kill.
#
# Exit codes are go-mutesting's: 0 killed, 1 survived, 2 did not compile,
# 3 error. Input: MUTATE_ORIGINAL, MUTATE_CHANGED, MUTATE_PACKAGE (set by
# go-mutesting), MUTATE_STATE_DIR (a directory private to this go-mutesting
# process, set by scripts/gate.sh).
set -uo pipefail

TIMEOUT_FACTOR=3
TIMEOUT_MIN_S=30
BASELINE_LIMIT_S=600

: "${MUTATE_ORIGINAL:?}" "${MUTATE_CHANGED:?}" "${MUTATE_PACKAGE:?}" "${MUTATE_STATE_DIR:?}"

say() { echo "mutate-exec: $*" >&2; }

now_ms() { echo $(($(date +%s%N) / 1000000)); }

# run_tests <binary> <dir> <limit_s>: the test binary in its package directory,
# killed after limit_s. Prints the elapsed milliseconds; returns the binary's
# status, or 124 on timeout.
run_tests() {
  local start status
  start="$(now_ms)"
  (cd "$2" && timeout --signal=KILL "$3" "$1" -test.count=1 -test.timeout=0 >/dev/null 2>&1)
  status=$?
  echo $(($(now_ms) - start))
  [ "$status" -eq 137 ] && status=124
  return "$status"
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
# last START line in a streamed log names the mutant that did it.
say "START $changed"
printf '{"Replace":{"%s":"%s"}}\n' "$original" "$changed" >"$work/overlay.json"
rm -f "$work/mutant.test"
if ! go test -c -overlay "$work/overlay.json" -o "$work/mutant.test" "$MUTATE_PACKAGE" >/dev/null 2>&1; then
  exit 2
fi
[ -x "$work/mutant.test" ] || exit 1

ms="$(run_tests "$work/mutant.test" "$dir" "$limit_s")"
case $? in
  0) diff -u --label original --label mutant "$original" "$changed" >&2; exit 1 ;;
  124) say "TIMEOUT $MUTATE_PACKAGE $original after ${limit_s}s (baseline ${baseline_ms}ms)"; exit 0 ;;
  *) exit 0 ;;
esac
