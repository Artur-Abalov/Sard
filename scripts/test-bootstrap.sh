#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# deploy/db/bootstrap.sql on a clean PostgreSQL 16, 17 and 18 (DB2, the @ci scenarios of
# docs/specs/server/database-roles.feature): what a DBA of an external cluster runs once.
#
#   1  a role with neither SUPERUSER nor CREATEROLE is refused with a message that names the right
#      missing, and nothing is created (no Sard role, no schema)
#   2  the superuser's run exits 0 and leaves the five roles with their attributes, the schema sard
#      owned by sard_owner, and no password anywhere (the script text has none either)
#   3  a second run exits 0 and changes nothing: the catalog is the same, row by row
#   5  in a cluster of its own, a role with CREATEROLE but not SUPERUSER, the owner of the database and
#      holding ADMIN OPTION on pg_read_all_data and pg_monitor, is refused while PUBLIC still holds
#      EXECUTE on the large-object functions of the database (the message names them and the fix, nothing is created);
#      after a superuser revoked it once, it runs the script twice: both runs exit 0 and the catalog
#      is the superuser's, with the role's name for the superuser's
#   6  sard_app a member of a role the CREATEROLE role has no ADMIN on: the run is refused naming the
#      role, and nothing is created
#   4  (SARD_IMAGE and SARD_VERSION set) the passwords of sard_migrator and sard_app are set, the
#      migrate command of the server image applies the migrations as sard_migrator, and the server
#      started as sard_app answers /api/v1/status without a warning about its role
#
# Usage: [BOOTSTRAP_VERSIONS="16 17 18"] [SARD_IMAGE=sard-server SARD_VERSION=e2e] scripts/test-bootstrap.sh
# The images are the ones test/images.env pins (SC1). Nothing is left behind: the containers and the
# network of a version are removed when the script ends, passed or not.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=lib/test-image.sh
source "$root/scripts/lib/test-image.sh"
# shellcheck source=lib/prepare-db.sh
source "$root/scripts/lib/prepare-db.sh"
script="$root/deploy/db/bootstrap.sql"
versions="${BOOTSTRAP_VERSIONS:-16 17 18}"
failures=0
started=()

# The containers and the network of one version (all named after it).
remove_stack() {
  docker rm -f "$1" "$1-migrate" "$1-server" "$1-dba" >/dev/null 2>&1 || true
  docker network rm "$1" >/dev/null 2>&1 || true
}
# Whatever a version started goes away when the script ends, however it ends.
cleanup() {
  local name
  for name in ${started[@]+"${started[@]}"}; do remove_stack "$name"; done
}
trap cleanup EXIT

fail() { echo "FAIL: $*" >&2; failures=$((failures + 1)); }
pass() { echo "ok: $*"; }

# The catalog of the Sard roles in the database, one text.
catalog_sql="
select 'role ' || rolname || ' ' || rolcanlogin || rolsuper || rolcreaterole || rolcreatedb || rolreplication || rolbypassrls || ' ' || rolconnlimit || ' ' || coalesce(rolpassword, '-') from pg_authid where rolname like 'sard\_%'
union all select 'setting ' || r.rolname || ' ' || s.setconfig::text from pg_db_role_setting s join pg_roles r on r.oid = s.setrole where r.rolname like 'sard\_%'
union all select 'member ' || u.rolname || ' of ' || g.rolname || ' ' || m.inherit_option || m.set_option from pg_auth_members m join pg_roles g on g.oid = m.roleid join pg_roles u on u.oid = m.member where u.rolname like 'sard\_%'
union all select 'schema ' || nspname || ' ' || nspowner::regrole || ' ' || coalesce(nspacl::text, '-') from pg_namespace where nspname in ('sard', 'public')
union all select 'default ' || defaclrole::regrole || ' ' || defaclobjtype::text || ' ' || defaclacl::text from pg_default_acl
union all select 'database ' || coalesce(datacl::text, '-') from pg_database where datname = 'sard'
order by 1"

# 5: $5 is the catalog the superuser's run left.
createrole_run() {
  local major="$1" image="$2" name="$3-dba" network="$4" expected="$5" as_dba catalog
  docker run -d --name "$name" --network "$network" -e POSTGRES_PASSWORD=superuser -e POSTGRES_DB=sard "$image" >/dev/null
  sard_wait_postgres "$name" postgres sard || { fail "$major: the second PostgreSQL does not start"; return; }
  local as_postgres=(docker exec -i "$name" psql -X -q -At -U postgres -d sard -v ON_ERROR_STOP=1)
  "${as_postgres[@]}" -c "create role dba login createrole password 'dba'" -c "grant pg_read_all_data to dba with admin option" \
    -c "grant pg_monitor to dba with admin option" -c "alter database sard owner to dba" >/dev/null
  as_dba=(docker exec -i -e PGPASSWORD=dba "$name" psql -X -q -At -U dba -h 127.0.0.1 -d sard -v ON_ERROR_STOP=1)
  local refused
  if refused="$("${as_dba[@]}" <"$script" 2>&1)"; then
    fail "$major: bootstrap.sql ran for a CREATEROLE role while PUBLIC can execute lo_import"; return
  elif ! grep -q 'lo_create(oid)' <<<"$refused" || ! grep -q 'REVOKE EXECUTE ON FUNCTION' <<<"$refused"; then
    fail "$major: the lo_* refusal names neither the functions nor the fix: $refused"; return
  elif [ "$("${as_postgres[@]}" -c "select (select count(*) from pg_roles where rolname like 'sard\\_%') + (select count(*) from pg_namespace where nspname = 'sard')")" != 0 ]; then
    fail "$major: the lo_* refusal left roles or the schema"; return
  fi
  pass "$major: PUBLIC with EXECUTE on lo_* refuses a CREATEROLE role, nothing is created"
  "${as_postgres[@]}" -c "revoke execute on function lo_create(oid), lo_creat(integer), lo_from_bytea(oid, bytea), lo_import(text), lo_import(text, oid) from public" >/dev/null
  "${as_dba[@]}" <"$script" >/dev/null 2>&1 || { fail "$major: bootstrap.sql failed for a CREATEROLE role"; return; }
  "${as_dba[@]}" <"$script" >/dev/null 2>&1 || { fail "$major: the second run for a CREATEROLE role failed"; return; }
  catalog="$("${as_postgres[@]}" -c "$catalog_sql")"
  [ "${catalog//dba/postgres}" = "$expected" ] \
    && pass "$major: a CREATEROLE role without SUPERUSER gets the superuser's catalog, twice" \
    || fail "$major: the catalog of the CREATEROLE role differs from the superuser's"
  member_without_admin "$major" "$name" "${as_postgres[@]}"
}

# 6: sard_app is a member of pg_write_all_data, which dba cannot revoke. Needs a fresh database and roles
# dba has ADMIN on, so it uses its own cluster-level names: the second cluster already holds the Sard roles,
# hence the membership is added there and the schema is dropped to start the script from scratch.
member_without_admin() {
  local major="$1" name="$2"
  shift 2
  local as_postgres=("$@") refused
  local as_dba=(docker exec -i -e PGPASSWORD=dba "$name" psql -X -q -At -U dba -h 127.0.0.1 -d sard6 -v ON_ERROR_STOP=1)
  "${as_postgres[@]}" -c "create database sard6 owner dba" -c "grant pg_write_all_data to sard_app" >/dev/null
  docker exec -i "$name" psql -X -q -At -U postgres -d sard6 -v ON_ERROR_STOP=1 -c "revoke execute on function lo_create(oid), lo_creat(integer), lo_from_bytea(oid, bytea), lo_import(text), lo_import(text, oid) from public" >/dev/null
  if refused="$("${as_dba[@]}" <"$script" 2>&1)"; then
    fail "$major: bootstrap.sql ran with a membership of sard_app the role cannot revoke"
  elif ! grep -q 'REVOKE pg_write_all_data FROM sard_app' <<<"$refused"; then
    fail "$major: the refusal does not name the role and the fix: $refused"
  elif [ "$(docker exec -i "$name" psql -X -q -At -U postgres -d sard6 -c "select count(*) from pg_namespace where nspname = 'sard'")" != 0 ]; then
    fail "$major: the refused script left the schema"
  else
    pass "$major: a membership of sard_app the CREATEROLE role cannot revoke is refused, naming the role"
  fi
}

one_version() {
  local major="$1" image name network
  image="$(test_image "postgres:$major")"
  name="sard-bootstrap-$major-$$"
  network="$name"
  started+=("$name")
  docker network create "$network" >/dev/null
  docker run -d --name "$name" --network "$network" --network-alias postgres -e POSTGRES_PASSWORD=superuser -e POSTGRES_DB=sard "$image" >/dev/null

  sard_wait_postgres "$name" postgres sard || { fail "$major: PostgreSQL does not start"; return; }
  local as_postgres=(docker exec -i "$name" psql -X -q -At -U postgres -d sard -v ON_ERROR_STOP=1)

  # 1: a role without the right to create roles.
  "${as_postgres[@]}" -c "create role weak login password 'weak'" >/dev/null
  local refused
  if refused="$(docker exec -i -e PGPASSWORD=weak "$name" psql -X -q -U weak -h 127.0.0.1 -d sard <"$script" 2>&1)"; then
    fail "$major: the script ran as a role without SUPERUSER and CREATEROLE"
  elif ! grep -q 'SUPERUSER or CREATEROLE' <<<"$refused"; then
    fail "$major: the refusal does not name the missing right: $refused"
  elif [ "$("${as_postgres[@]}" -c "select (select count(*) from pg_roles where rolname like 'sard\_%') + (select count(*) from pg_namespace where nspname = 'sard')")" != 0 ]; then
    fail "$major: the refused script left roles or the schema"
  else
    pass "$major: a role without SUPERUSER and CREATEROLE is refused, nothing is created"
  fi

  # 2: the superuser's run.
  if ! "${as_postgres[@]}" <"$script" >/dev/null; then fail "$major: bootstrap.sql failed for the superuser"; return; fi
  local roles
  roles="$("${as_postgres[@]}" -c "select string_agg(rolname || ':' || rolcanlogin::int || rolsuper::int || rolcreaterole::int || rolcreatedb::int || rolreplication::int || rolbypassrls::int || ':' || (rolpassword is null)::int, ' ' order by rolname) from pg_authid where rolname like 'sard\_%'")"
  [ "$roles" = "sard_app:100000:1 sard_backup:100000:1 sard_migrator:100000:1 sard_monitor:100000:1 sard_owner:000000:1" ] \
    || fail "$major: the roles are: $roles"
  [ "$("${as_postgres[@]}" -c "select nspowner::regrole from pg_namespace where nspname = 'sard'")" = sard_owner ] \
    || fail "$major: the schema sard is not owned by sard_owner"
  grep -Eiq "password +'" "$script" && fail "$major: the script sets a password"
  pass "$major: the roles and the schema sard, no password"

  # 3: a second run.
  local before after
  before="$("${as_postgres[@]}" -c "$catalog_sql")"
  "${as_postgres[@]}" <"$script" >/dev/null || fail "$major: the second run failed"
  after="$("${as_postgres[@]}" -c "$catalog_sql")"
  [ "$before" = "$after" ] && pass "$major: a second run changes nothing" || fail "$major: the second run changed the catalog"

  # 5: the same by a role that is not a superuser, in a cluster of its own (roles belong to the cluster).
  createrole_run "$major" "$image" "$name" "$network" "$after"

  # 4: migrate and the server, with the images of the build.
  if [ -n "${SARD_IMAGE:-}" ] && [ -n "${SARD_VERSION:-}" ]; then
    local migrator app i
    migrator="$(openssl rand -hex 16)"
    app="$(openssl rand -hex 16)"
    "${as_postgres[@]}" -c "alter role sard_migrator password '$migrator'" -c "alter role sard_app password '$app'" >/dev/null
    if docker run --name "$name-migrate" --network "$network" -e SARD_DB_URL=jdbc:postgresql://postgres:5432/sard \
      -e SARD_MIGRATE_PASSWORD="$migrator" "$SARD_IMAGE:$SARD_VERSION" migrate >"$name.migrate.log" 2>&1; then
      pass "$major: migrate as sard_migrator"
    else
      fail "$major: migrate failed: $(tail -5 "$name.migrate.log")"
      return
    fi
    docker run -d --name "$name-server" --network "$network" -e SARD_DB_URL=jdbc:postgresql://postgres:5432/sard \
      -e SARD_DB_USER=sard_app -e SARD_DB_PASSWORD="$app" -e SARD_AGENT_DOWNLOADS=false "$SARD_IMAGE:$SARD_VERSION" >/dev/null
    local up=no
    for i in $(seq 1 90); do
      if docker exec "$name-server" bash -c 'exec 3<>/dev/tcp/127.0.0.1/8080 && printf "GET /api/v1/status HTTP/1.0\r\n\r\n" >&3 && head -1 <&3 | grep -q "^HTTP/1\.[01] 200"' 2>/dev/null; then up=yes; break; fi
      sleep 1
    done
    if [ "$up" = yes ] && ! docker logs "$name-server" 2>&1 | grep -q 'more rights than the server needs'; then
      pass "$major: the server as sard_app answers /api/v1/status"
    else
      fail "$major: the server as sard_app: up=$up $(docker logs "$name-server" 2>&1 | grep -E 'WARN|ERROR|refused' | head -3)"
    fi
    rm -f "$name.migrate.log"
  fi
}

for v in $versions; do
  one_version "$v"
  remove_stack "sard-bootstrap-$v-$$"
done
[ "$failures" -eq 0 ] || { echo "FAILED: $failures check(s)" >&2; exit 1; }
echo PASSED
