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
mkdir -p "$WORK/docs" "$WORK/web/locales"
echo "run: sudo sard-agent enroll" >"$WORK/docs/guide.md"

out="$("$CHECK" "$WORK")" || die "clean tree failed: $out"
grep -q 'OK' <<<"$out" || die "clean tree: no OK in: $out"

printf 'line one\nrun: sudo -u sard-agent sard-agent enroll\n' >"$WORK/web/locales/en.json"
rc=0
out="$("$CHECK" "$WORK")" || rc=$?
[ "$rc" -eq 1 ] || die "offender: exit $rc, want 1"
grep -q 'check-sudo-u: web/locales/en.json:2:' <<<"$out" || die "offender not named: $out"
grep -q 'FAILED' <<<"$out" || die "no FAILED: $out"

expect_rc() { # want, description
  local want="$1" rc=0
  "$CHECK" "$WORK" >/dev/null || rc=$?
  [ "$rc" -eq "$want" ] || die "$2: exit $rc, want $want"
}

# expect_file_rc <want> <description> <path relative to WORK> <content>
# Writes the content, runs the check, removes the file again.
expect_file_rc() {
  mkdir -p "$(dirname "$WORK/$3")"
  printf '%s\n' "$4" >"$WORK/$3"
  expect_rc "$1" "$2"
  rm "$WORK/$3"
}

readonly OFFENDER='sudo -u sard-agent restic snapshots'

expect_file_rc 0 "reason on the line above" web/locales/en.json \
  $'x # allow-sudo-u: restic reads the repository\n'"$OFFENDER"
expect_file_rc 0 "reason on the same line" web/locales/en.json \
  "$OFFENDER # allow-sudo-u: restic reads the repository"
expect_file_rc 1 "empty reason" web/locales/en.json "$OFFENDER # allow-sudo-u:"
expect_file_rc 0 "docs/sessions (history)" docs/sessions/2026-01-01.md "$OFFENDER"
expect_file_rc 0 "docs/adr (history)" docs/adr/x.md "$OFFENDER"
expect_file_rc 1 "unmarked docs/qa" docs/qa/x.md "$OFFENDER"
expect_file_rc 1 "unmarked docs/specs" docs/specs/x.feature "    When I run $OFFENDER"
expect_file_rc 1 "marker two lines above" docs/qa/x.md \
  $'# allow-sudo-u: reason\nmiddle\n'"$OFFENDER"
expect_file_rc 0 "the check script itself" scripts/check-sudo-u.sh "sudo -u x"
expect_file_rc 0 "the test script itself" scripts/test-check-sudo-u.sh "sudo -u x"

for variant in 'sudo -iu sard-agent id' 'sudo -Hu x id' 'sudo --user=x id' 'sudo --user x id' 'sudo -E -u x id' $'sudo\t-u x id'; do
  expect_file_rc 1 "variant [$variant]" docs/guide.md "$variant"
done

for harmless in 'sudo -s' 'sudo sard-agent' 'sudo --preserve-env id' 'sudo sard-agent -u' 'sudo -E sard-agent run-u' 'xsudo -u y'; do
  expect_file_rc 0 "harmless [$harmless]" docs/guide.md "$harmless"
done

echo "test-check-sudo-u: OK"
