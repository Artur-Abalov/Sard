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

mkdir -p "$WORK/docs/adr" "$WORK/docs/qa" "$WORK/docs/specs" "$WORK/scripts"
echo "sudo -u sard-agent old decision" >"$WORK/docs/adr/x.md"
"$CHECK" "$WORK" >/dev/null || die "docs/adr was checked"
rm "$WORK/docs/adr/x.md"

expect_rc() { # want, description
  local want="$1" rc=0
  "$CHECK" "$WORK" >/dev/null || rc=$?
  [ "$rc" -eq "$want" ] || die "$2: exit $rc, want $want"
}

echo "sudo -u sard-agent restic snapshots" >"$WORK/docs/qa/x.md"
expect_rc 1 "unmarked docs/qa"
rm "$WORK/docs/qa/x.md"

echo "    When I run sudo -u sard-agent restic snapshots" >"$WORK/docs/specs/x.feature"
expect_rc 1 "unmarked docs/specs"
rm "$WORK/docs/specs/x.feature"

printf '# allow-sudo-u: reason\nmiddle\nsudo -u sard-agent restic snapshots\n' >"$WORK/docs/qa/x.md"
expect_rc 1 "marker two lines above"
rm "$WORK/docs/qa/x.md"

echo "sudo -u x" >"$WORK/scripts/check-sudo-u.sh"
echo "sudo -u x" >"$WORK/scripts/test-check-sudo-u.sh"
expect_rc 0 "the scripts themselves"
rm "$WORK/scripts/check-sudo-u.sh" "$WORK/scripts/test-check-sudo-u.sh"

for variant in 'sudo -iu sard-agent id' 'sudo -Hu x id' 'sudo --user=x id' 'sudo --user x id' 'sudo -E -u x id' $'sudo\t-u x id'; do
  printf '%s\n' "$variant" >"$WORK/docs/guide.md"
  expect_rc 1 "variant [$variant]"
done

for harmless in 'sudo -s' 'sudo sard-agent' 'sudo --preserve-env id' 'sudo sard-agent -u' 'sudo -E sard-agent run-u' 'xsudo -u y'; do
  printf '%s\n' "$harmless" >"$WORK/docs/guide.md"
  expect_rc 0 "harmless [$harmless]"
done

echo "test-check-sudo-u: OK"
