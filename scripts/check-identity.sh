#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# The copies of deploy/release/identity.env (D24, SC1) agree with it: the
# image names in deploy/docker-compose.yml and deploy/.env.example, which a
# release ships on their own. With --keys also the public keys it names
# exist (release.yml checks that before it builds anything).
# Optional argument: the root directory to check (default: the repository).
set -euo pipefail

KEYS=0
if [ "${1:-}" = --keys ]; then KEYS=1; shift; fi
ROOT="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
# shellcheck source=../deploy/release/identity.env
. "$ROOT/deploy/release/identity.env"

failed=0
expect() { # file, fixed string
  grep -qF -- "$2" "$ROOT/$1" && return
  echo "check-identity: $1 has no \"$2\" (deploy/release/identity.env)"
  failed=1
}

expect deploy/docker-compose.yml "\${SARD_IMAGE:-$SARD_REGISTRY_SERVER}:"
expect deploy/docker-compose.yml "\${SARD_AGENT_IMAGE:-$SARD_REGISTRY_AGENT}:"
expect deploy/.env.example "# SARD_IMAGE=$SARD_REGISTRY_SERVER"
expect deploy/.env.example "# SARD_AGENT_IMAGE=$SARD_REGISTRY_AGENT"
if [ "$KEYS" -eq 1 ]; then
  for key in "$SARD_MINISIGN_PUBLIC_KEY" "$SARD_COSIGN_PUBLIC_KEY"; do
    [ -f "$ROOT/$key" ] || { echo "check-identity: public key $key missing (deploy/release/identity.env)"; failed=1; }
  done
fi

if [ "$failed" -ne 0 ]; then
  echo "check-identity: FAILED"
  exit 1
fi
echo "check-identity: OK"
