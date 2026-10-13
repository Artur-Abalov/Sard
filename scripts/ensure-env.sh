#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# First `make up`: deploy/.env does not exist yet. Copies deploy/.env.example
# and replaces its SARD_DB_PASSWORD placeholder with a random value. The
# administrator has no password here: the first start asks for it in the
# wizard, with the code from the server log (F4a). Prints where the file is,
# never a password.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="$ROOT/deploy/.env"

[ -f "$ENV_FILE" ] && exit 0

cp "$ROOT/deploy/.env.example" "$ENV_FILE"
chmod 600 "$ENV_FILE"

# 48 hex characters (24 random bytes), alphanumeric only so it needs no shell
# quoting inside the .env file. openssl is preferred; od reads the same
# /dev/urandom when it is absent.
random_hex() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 24
  else
    od -An -tx1 -N24 /dev/urandom | tr -d ' \n'
  fi
}

awk -v db="$(random_hex)" '
  /^SARD_DB_PASSWORD=/ { print "SARD_DB_PASSWORD=" db; next }
  { print }
' "$ENV_FILE" >"$ENV_FILE.tmp"
mv "$ENV_FILE.tmp" "$ENV_FILE"
chmod 600 "$ENV_FILE"

echo "Created deploy/.env with a generated database password"
