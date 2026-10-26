#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# The db-init service of deploy/docker-compose.yml (DB2, docs/adr/0069-database-roles.md): runs once per
# `docker compose up`, as the superuser of the postgres service over its local socket (the only way in for it: the
# network refuses it), and prepares the database for Sard:
#   1. deploy/db/bootstrap.sql: the roles, the schema "sard", who owns and may use what. Repeatable.
#   2. a password for each role that logs in and is not the superuser, written to the volume of the one consumer that
#      reads it, as a file of 64 hex characters, owner uid 10001, mode 0400:
#        sard_migrator  /run/sard-db/migrate/password  (the migrate service)
#        sard_app       /run/sard-db/app/password      (the server)
#        sard_backup    /run/sard-db/backup/password   (the agent next to the server)
#      A password that exists stays; the file removed is the way to a new one (docs/operator/16-postgresql.md).
#   3. a placeholder for the backup password in the channel volume, because the agent next to the server mounts the
#      password over /run/sard-self/db-password inside a volume it mounts read-only.
# A password reaches the database as text of a statement on the local socket and is never logged: the session turns
# the logging of statements off before it sends one. Nothing here is printed but the names of the roles.
set -eu

export PGHOST="${PGHOST:-/var/run/postgresql}" PGUSER="${PGUSER:-sard}" PGDATABASE="${PGDATABASE:-sard}"
PASSWORDS="${SARD_DB_PASSWORDS:-/run/sard-db}"
BOOTSTRAP="${SARD_DB_BOOTSTRAP:-/etc/sard/bootstrap.sql}"
OWNER_UID=10001

die() {
  echo "db-init: $* (docs/operator/16-postgresql.md)" >&2
  exit 1
}

sql() { psql -X -q -At -v ON_ERROR_STOP=1 "$@"; }

sql -f "$BOOTSTRAP" || die "bootstrap.sql failed; the database is as it was"
sql -c "ALTER DATABASE \"$PGDATABASE\" SET search_path = sard" || die "cannot set the search_path of the database"

new_password() { head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'; }

# The role has a password in the database and the volume has its file.
ensure() {
  role="$1"
  file="$2/password"
  created=no
  if [ ! -s "$file" ]; then
    umask 077
    new_password >"$file.new" || die "cannot write the password of $role into $2"
    chown "$OWNER_UID:$OWNER_UID" "$file.new" && chmod 0400 "$file.new" && mv -f "$file.new" "$file" ||
      die "cannot write the password of $role into $2"
    created=yes
  fi
  no_password=$(sql -c "SELECT rolpassword IS NULL FROM pg_authid WHERE rolname = '$role'") ||
    die "cannot read the role $role"
  if [ "$created" = yes ] || [ "$no_password" = t ]; then
    password=$(cat "$file")
    sql <<SQL || die "cannot set the password of $role"
SET log_statement = 'none';
SET log_min_duration_statement = -1;
SET log_min_error_statement = 'panic';
ALTER ROLE $role PASSWORD '$password';
SQL
    echo "db-init: the password of $role is set ($2/password)"
  fi
}

ensure sard_migrator "$PASSWORDS/migrate"
ensure sard_app "$PASSWORDS/app"
ensure sard_backup "$PASSWORDS/backup"

if [ -d "$PASSWORDS/channel" ] && [ ! -e "$PASSWORDS/channel/db-password" ]; then
  : >"$PASSWORDS/channel/db-password"
fi
echo "db-init: done"
