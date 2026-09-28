#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# First `make up`: deploy/.env does not exist yet. Copies deploy/.env.example
# and replaces its SARD_ADMIN_PASSWORD placeholder with a random value (>= 24
# characters, well over the 12-character startup minimum, ADR 0020). Prints
# where the password landed, never the password itself (decision Р3).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="$ROOT/deploy/.env"

[ -f "$ENV_FILE" ] && exit 0

cp "$ROOT/deploy/.env.example" "$ENV_FILE"

# 32 hex characters (16 random bytes): well over the 24-character floor,
# alphanumeric only so it needs no shell quoting inside the .env file.
password="$(openssl rand -hex 16)"

awk -v pw="$password" '
  /^SARD_ADMIN_PASSWORD=/ { print "SARD_ADMIN_PASSWORD=" pw; next }
  { print }
' "$ENV_FILE" >"$ENV_FILE.tmp"
mv "$ENV_FILE.tmp" "$ENV_FILE"

echo "Generated an administrator password in deploy/.env"
