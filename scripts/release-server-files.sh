#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# The installation files of a release (DB2, docs/adr/0069-database-roles.md), put into <dest> under the names they
# are attached to the release with:
#
#   docker-compose.yml, sard.env.example (SARD_VERSION set to <version>)
#   and every file of deploy/db/, by its own name: db-init.sh, bootstrap.sql, pg_hba.conf, postgres-entrypoint.sh
#
# A release has no directories, so the compose file's `./db/<name>` is the asset <name>: the quickstart
# (README.md) saves each one into db/. SHA256SUMS.server, signed by sign-server, covers all of them.
#
#   scripts/release-server-files.sh <dest> <version>
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
die() { echo "release-server-files: $*" >&2; exit 1; }

[ "$#" -eq 2 ] || die "usage: release-server-files.sh <dest> <version>"
DEST="$1"
VERSION="${2#v}"
[ -d "$DEST" ] || die "$DEST is not a directory"

cp "$ROOT/deploy/docker-compose.yml" "$DEST/docker-compose.yml"
sed "s/^SARD_VERSION=.*/SARD_VERSION=$VERSION/" "$ROOT/deploy/.env.example" >"$DEST/sard.env.example"
grep -qx "SARD_VERSION=$VERSION" "$DEST/sard.env.example" || die "SARD_VERSION is not set in deploy/.env.example"
for f in "$ROOT"/deploy/db/*; do
  [ -f "$f" ] || continue
  cp "$f" "$DEST/$(basename "$f")"
done
