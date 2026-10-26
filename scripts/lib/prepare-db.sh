# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Sourced by the test scripts that run sard-server next to a plain PostgreSQL container (DB2). The server is
# sard_app, which may not change the schema, so what db-init and the migrate service do in a compose stack is done
# here: deploy/db/bootstrap.sql as the superuser, a password for sard_migrator and sard_app, and the migrate command
# of the server image.
#
#   sard_prepare_db <db container> <network> <server image>
#     the database "sard" of the container (superuser sard, started with POSTGRES_DB=sard) is prepared; the password
#     of sard_app is left in SARD_APP_PASSWORD for the server container's SARD_DB_PASSWORD (SARD_DB_USER=sard_app).
#     The network needs the container's alias "db".

#   sard_wait_postgres <container> <user> <database>
#     waits for a container of a fresh official image: the entrypoint starts a temporary server for initdb, stops it
#     and starts the real one, so "ready to accept connections" is in the log twice before the answer of pg_isready
#     counts. Fails after two minutes.
sard_wait_postgres() {
  local db="$1" user="$2" database="$3" i
  for i in $(seq 1 120); do
    if [ "$(docker logs "$db" 2>&1 | grep -c 'ready to accept connections')" -ge 2 ] &&
      docker exec "$db" pg_isready -U "$user" -d "$database" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

sard_prepare_db() {
  local db="$1" network="$2" image="$3" root migrator
  root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
  sard_wait_postgres "$db" sard sard || { echo "sard_prepare_db: PostgreSQL of $db does not start" >&2; return 1; }
  docker exec -i "$db" psql -X -q -U sard -d sard -v ON_ERROR_STOP=1 <"$root/deploy/db/bootstrap.sql" >/dev/null
  migrator="$(od -An -tx1 -N16 /dev/urandom | tr -d ' \n')"
  SARD_APP_PASSWORD="$(od -An -tx1 -N16 /dev/urandom | tr -d ' \n')"
  docker exec -i "$db" psql -X -q -U sard -d sard -v ON_ERROR_STOP=1 \
    -c "alter role sard_migrator password '$migrator'" -c "alter role sard_app password '$SARD_APP_PASSWORD'"
  docker run --rm --network "$network" -e SARD_DB_URL=jdbc:postgresql://db:5432/sard \
    -e SARD_MIGRATE_PASSWORD="$migrator" "$image" migrate >/dev/null
}
