#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# What the package's post-install script tells the operator (U1b,
# docs/specs/agent/agent-install.feature), run as the package managers run it
# in a throwaway Debian container, no systemd needed:
#
#   scripts/test-postinstall.sh [<image>]      default debian:12
#
# - first install (deb "configure", rpm "1"): the next step is printed, the
#   enroll command through sudo (no "sudo -u") and where the token comes from;
# - an upgrade (deb "configure <old>", rpm "2"): no word about registration;
# - either way the unit is neither enabled nor started by the script.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${1:-debian:12}"
die() { echo "test-postinstall: FAIL: $*" >&2; exit 1; }

# run_script <script arguments>...: the post-install script's output, in a clean container.
run_script() {
  docker run --rm -v "$ROOT/deploy/agent/postinstall.sh:/postinstall.sh:ro" "$IMAGE" \
    sh -c 'mkdir -p /etc/sard && sh /postinstall.sh "$@" 2>&1' sh "$@"
}

expect_hint() {
  local out
  out="$(run_script "$@")" || die "$*: the script failed: $out"
  grep -q 'sudo sard-agent enroll --server' <<<"$out" || die "$*: no enroll command through sudo in: $out"
  ! grep -q 'sudo -u' <<<"$out" || die "$*: the hint still says sudo -u: $out"
  grep -qi 'console' <<<"$out" || die "$*: no mention of the console (the token) in: $out"
  ! grep -qi 'systemctl' <<<"$out" || die "$*: the script runs or advises systemctl before enrollment: $out"
  echo "ok: $* prints the next step"
}

expect_silent() {
  local out
  out="$(run_script "$@")" || die "$*: the script failed: $out"
  ! grep -qi 'enroll' <<<"$out" || die "$*: an upgrade talks about registration: $out"
  echo "ok: $* prints nothing about registration"
}

expect_hint configure
expect_silent configure 1.3.2
expect_hint 1
expect_silent 2
echo "test-postinstall: all checks passed"
