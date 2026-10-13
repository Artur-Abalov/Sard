#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Test of scripts/check-sudo-u.sh on a throwaway git repository.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="$HERE/check-sudo-u.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

die() { echo "test-check-sudo-u: FAIL: $*" >&2; exit 1; }

git -C "$WORK" init -q
mkdir -p "$WORK/docs/sessions" "$WORK/web/locales"
echo "run: sudo sard-agent enroll" >"$WORK/docs/guide.md"

out="$("$CHECK" "$WORK")" || die "clean tree failed: $out"
grep -q 'OK' <<<"$out" || die "clean tree: no OK in: $out"

printf 'line one\nrun: sudo -u sard-agent sard-agent enroll\n' >"$WORK/web/locales/en.json"
rc=0
out="$("$CHECK" "$WORK")" || rc=$?
[ "$rc" -eq 1 ] || die "offender: exit $rc, want 1"
grep -q 'check-sudo-u: web/locales/en.json:2:' <<<"$out" || die "offender not named: $out"
grep -q 'FAILED' <<<"$out" || die "no FAILED: $out"

printf 'x # allow-sudo-u: restic reads the repository\nsudo -u sard-agent restic snapshots\n' >"$WORK/web/locales/en.json"
"$CHECK" "$WORK" >/dev/null || die "reason on the line above was refused"

printf 'sudo -u sard-agent restic snapshots # allow-sudo-u: restic reads the repository\n' >"$WORK/web/locales/en.json"
"$CHECK" "$WORK" >/dev/null || die "reason on the same line was refused"

printf 'sudo -u sard-agent restic snapshots # allow-sudo-u:\n' >"$WORK/web/locales/en.json"
rc=0
"$CHECK" "$WORK" >/dev/null || rc=$?
[ "$rc" -eq 1 ] || die "empty reason accepted"

rm "$WORK/web/locales/en.json"
echo "sudo -u sard-agent old history" >"$WORK/docs/sessions/2026-01-01.md"
"$CHECK" "$WORK" >/dev/null || die "docs/sessions was checked"

echo "test-check-sudo-u: OK"
