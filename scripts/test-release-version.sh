#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Tests scripts/release-version.sh, the one rule from a git tag to every
# version of a release (docs/adr/0047-release-versions.md):
#
#   1. which tags are releases (vX.Y.Z, vX.Y.Z-beta.N, vX.Y.Z-rc.N) and which
#      are refused (rc1 without a dot, alpha, leading zeros, git describe);
#   2. the versions each tag gives: image X.Y.Z-pre, deb/rpm X.Y.Z~pre, file
#      names without "~";
#   3. dpkg and rpm order the package versions of deploy/release/version-order.txt
#      as listed, every development build (0.0.0~dev.…, v0.0.1-rc1 among them)
#      below them all.
#
#   scripts/test-release-version.sh
#
# Needs dpkg and rpm (rpm.vercmp); a missing tool fails the test.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RV="$ROOT/scripts/release-version.sh"
ORDER="$ROOT/deploy/release/version-order.txt"
failures=0

fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}

command -v dpkg >/dev/null || { echo "test-release-version: dpkg required" >&2; exit 1; }
command -v rpm >/dev/null || { echo "test-release-version: rpm required" >&2; exit 1; }

# 1. Release tags.
for tag in v0.1.0 v0.1.0-beta.1 v0.1.0-rc.1 v0.1.0-rc.10 v10.20.30-beta.99 v1.0.0; do
  "$RV" check "$tag" >/dev/null 2>&1 || fail "check $tag: refused, want accepted"
done
for tag in v0.0.1-rc1 v0.1.0-rc v0.1.0-beta v0.1.0-alpha.1 v0.1.0-rc.0 v0.1.0-rc.01 \
  v01.0.0 v0.01.0 0.1.0 v0.1 v0.1.0-RC.1 v0.1.0-rc.1.1 v0.1.0+build v0.1.0-beta.1-5-gabc1234 \
  "v0.1.0 " "" upgrade-base dev; do
  if "$RV" check "$tag" >/dev/null 2>&1; then
    fail "check '$tag': accepted, want refused"
  fi
done
# The refusal names the tag and the expected form.
msg="$("$RV" check v0.0.1-rc1 2>&1 || true)"
grep -q 'v0.0.1-rc1' <<<"$msg" || fail "refusal does not name the tag: $msg"
grep -q 'vX.Y.Z-rc.N' <<<"$msg" || fail "refusal does not name the expected form: $msg"

# 2. Versions of a tag.
# expect WANT ARG...: release-version.sh ARG... prints WANT.
expect() {
  local want="$1" got
  shift
  got="$("$RV" "$@" 2>&1)" || { fail "$*: failed: $got"; return; }
  [ "$got" = "$want" ] || fail "$*: got '$got', want '$want'"
}
expect 0.1.0~beta.1 package v0.1.0-beta.1
expect 0.1.0~rc.10 package v0.1.0-rc.10
expect 0.1.0 package v0.1.0
expect 2.3.4~rc.1 package v2.3.4-rc.1
# Not a release tag: a development version below every release.
expect 0.0.0~dev.v0.0.1.rc1 package v0.0.1-rc1
expect 0.0.0~dev.upgrade.base package upgrade-base
expect 0.0.0~dev.v0.1.0.beta.1.5.gabc1234 package v0.1.0-beta.1-5-gabc1234
expect 0.0.0~dev.eec079d.dirty package eec079d-dirty
expect 0.1.0-beta.1 image v0.1.0-beta.1
expect 0.1.0 image v0.1.0
if "$RV" image v0.0.1-rc1 >/dev/null 2>&1; then fail "image v0.0.1-rc1: accepted, want refused"; fi
expect sard-agent_v0.1.0-beta.1_amd64.deb deb-file v0.1.0-beta.1 amd64
expect sard-agent-v0.1.0-beta.1.x86_64.rpm rpm-file v0.1.0-beta.1 amd64
expect sard-agent-v0.1.0.aarch64.rpm rpm-file v0.1.0 arm64
expect sard-agent_upgrade-base_arm64.deb deb-file upgrade-base arm64
for v in 'a~b' 'a b' 'a/b' ''; do
  if "$RV" deb-file "$v" amd64 >/dev/null 2>&1; then fail "deb-file '$v': accepted, want refused"; fi
done

# 3. Order: development builds, then the listed releases.
versions=(0.0.0~dev.upgrade.base 0.0.0~dev.v0.0.1.rc1 0.0.0~dev.v0.1.0.rc.1.5.gabc1234)
while read -r tag; do
  versions+=("$("$RV" package "$tag")")
done < <(grep -v '^#' "$ORDER" | grep .)
[ "${#versions[@]}" -gt 10 ] || fail "too few versions read from $ORDER"

rpm_cmp() { rpm --eval "%{lua:print(rpm.vercmp('$1','$2'))}"; }

# Every development build is below every release; releases in listed order.
for ((i = 0; i < ${#versions[@]}; i++)); do
  for ((j = i + 1; j < ${#versions[@]}; j++)); do
    a="${versions[i]}" b="${versions[j]}"
    [ "$i" -lt 3 ] && [ "$j" -lt 3 ] && continue # development builds: no order among them
    dpkg --compare-versions "$a" lt "$b" || fail "dpkg: $a is not older than $b"
    [ "$(rpm_cmp "$a" "$b")" = -1 ] || fail "rpm: $a is not older than $b"
  done
done

if [ "$failures" -ne 0 ]; then
  echo "test-release-version: $failures failed" >&2
  exit 1
fi
echo "test-release-version: all checks passed (${#versions[@]} versions)"
