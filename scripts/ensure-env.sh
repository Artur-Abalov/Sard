#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# First `make up`: deploy/.env does not exist yet. Copies deploy/.env.example. There is no secret to generate
# (DB2): db-init makes the database passwords on the first start, and the administrator has no password here, the first
# start asks for it in the wizard with the code from the server log (F4a). Prints where the file is.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="$ROOT/deploy/.env"

[ -f "$ENV_FILE" ] && exit 0

cp "$ROOT/deploy/.env.example" "$ENV_FILE"
chmod 600 "$ENV_FILE"

echo "Created deploy/.env"
