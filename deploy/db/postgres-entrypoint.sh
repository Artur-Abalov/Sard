#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# The entrypoint of the postgres service of deploy/docker-compose.yml (DB2, docs/adr/0069-database-roles.md): the
# superuser sard gets a password made here from /dev/urandom for initdb, which writes only its hash into the volume.
# No file, no variable and no .env keeps it, and pg_hba.conf refuses the superuser on the network anyway. The way in
# for an administrator is the local socket: docker compose exec postgres psql -U sard.
set -eu

POSTGRES_PASSWORD="$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')"
export POSTGRES_PASSWORD
exec docker-entrypoint.sh "$@"
