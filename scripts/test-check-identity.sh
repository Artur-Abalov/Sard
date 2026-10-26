#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Test of scripts/check-identity.sh on a copy of the files it reads.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="$HERE/check-identity.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

die() { echo "test-check-identity: FAIL: $*" >&2; exit 1; }

fresh() {
  local dir
  dir="$(mktemp -d -p "$WORK")"
  mkdir -p "$dir/deploy/release"
  cp "$HERE/../deploy/release/identity.env" "$dir/deploy/release/"
  cp "$HERE/../deploy/docker-compose.yml" "$HERE/../deploy/.env.example" "$dir/deploy/"
  touch "$dir/deploy/release/sard-release.pub" "$dir/deploy/release/cosign.pub"
  echo "$dir"
}

expect() { # want exit, description, root [flag]
  local rc=0 out
  out="$("$CHECK" ${4:+"$4"} "$3" 2>&1)" || rc=$?
  [ "$rc" -eq "$1" ] || die "$2: exit $rc, want $1: $out"
}

expect 0 "the copies as committed" "$(fresh)"
expect 0 "the copies and the keys" "$(fresh)" --keys

root="$(fresh)"
sed -i 's|^SARD_REGISTRY_SERVER=.*|SARD_REGISTRY_SERVER=registry.example/sard-server|' "$root/deploy/release/identity.env"
expect 1 "a server renamed in identity.env only" "$root"

root="$(fresh)"
sed -i 's|ghcr.io/[a-z-]*/sard-agent|ghcr.io/other/sard-agent|' "$root/deploy/docker-compose.yml"
expect 1 "another agent in the compose file" "$root"

root="$(fresh)"
sed -i 's|^# SARD_IMAGE=.*|# SARD_IMAGE=ghcr.io/other/sard-server|' "$root/deploy/.env.example"
expect 1 "another server in .env.example" "$root"

# The names must be in the assignments themselves, not anywhere in the file.
root="$(fresh)"
server="$(sed -n 's/^SARD_REGISTRY_SERVER=//p' "$root/deploy/release/identity.env")"
sed -i "s|\${SARD_IMAGE:-$server}|\${SARD_IMAGE:-ghcr.io/other/sard-server}|" "$root/deploy/docker-compose.yml"
echo "# image: \${SARD_IMAGE:-$server}:\${SARD_VERSION}" >>"$root/deploy/docker-compose.yml"
expect 1 "the right name in a comment, another in the image line" "$root"

root="$(fresh)"
sed -i 's|^# SARD_IMAGE=.*|# SARD_IMAGE=ghcr.io/other/sard-server|' "$root/deploy/.env.example"
echo "# see # SARD_IMAGE=$server for the default" >>"$root/deploy/.env.example"
expect 1 "the right name in prose, another in the template line" "$root"

root="$(fresh)"
rm "$root/deploy/release/cosign.pub"
expect 1 "no cosign public key" "$root" --keys
expect 0 "no cosign public key, names only" "$root"

echo "test-check-identity: OK"
