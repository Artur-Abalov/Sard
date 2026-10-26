#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# The agent next to the server (sard-self, docs/operations/self-agent.md) on a real
# docker compose stack of deploy/docker-compose.yml: F5's checks 1, 2 and 4, the roles and
# passwords of the database (DB2, checks 10 to 15), and with UPGRADE_FROM also check 5
# (docs/specs/server/self-agent.feature, @e2e/@upgrade).
#
# DEFERRED until the first release with DB2 (owner's decision 2026-10-26, ADR 0069 section 13): checks 5
# and 9 of the UPGRADE_FROM branch. There is no release to upgrade from (the migrations were rewritten,
# there is no upgrade path from builds before DB2), so CI does not set UPGRADE_FROM and the branch stays
# as it is, neither deleted nor skipped in code. WHEN THE FIRST RELEASE WITH DB2 SHIPS, SET UPGRADE_FROM
# TO IT IN CI (.github/workflows/ci.yml, job self-agent) AND REVIEW THE BRANCH: it was written for the
# compose file of a release with SARD_DB_PASSWORD in .env.
# The first start goes through the wizard by the code from the server log (F4a): the
# built-in token appears only after the step ca.
#
#   1  a clean `up --wait` and the wizard (code, CA, administrator) give an online
#      built-in agent sard-self; before the step ca there is no token in the channel
#   2  a re-created sidecar is the same agent, and no new token is issued
#   4  the used built-in token is refused (TOKEN_USED) from another container; the
#      channel is mounted by the server and the sidecar only, read-only in the
#      sidecar; neither the token nor the role password is in any log
#   5  (UPGRADE_FROM=<server image of a released 0.1.0-beta.N>) a stack of that
#      version, with its own compose file, a passed wizard, its sidecar and an agent
#      of its own, is replaced by this compose file: the same agents and tokens, the
#      same CA, sign-in with the password of the wizard, no setup code in the log of
#      the upgraded server. UPGRADE_FROM unset: no upgrade check.
#   6  admin-reset, a restart and the wizard give a new password; the old one is refused
#   8  (F4b) steps 3 and 4 of the wizard through REST with a local repository, as the owner does:
#      the commands of GET /self-backup/repository-command run in this directory, the repository
#      shows up with its restic id, PUT /self-backup/repository binds it, "back up now" runs both
#      system sources to success, the printed password command prints, the key copy is confirmed
#      (204), the restriction is none and an enrollment token is issued (201). The other stands
#      take the DB shortcut (sard_confirm_onboarding_in_database), this one does not.
#   7  (F6) what the self-backup reads stays read-only in the sidecar: the CA directory and
#      the installation directory are mounted read-only and refuse a write, .env is readable
#      through group 10001; the role sard_backup still cannot write to the database
#   9  (DB1) a clean install's database: the pinned PostgreSQL, page checksums, UTF8, the builtin
#      C.UTF-8 locale, UTC and the settings of the profile small, and no warning in the server's
#      log; after the upgrade (5) the old database keeps its locale, gets the profile, and the
#      server's log names the difference
#
# Usage: SARD_IMAGE=sard-server SARD_AGENT_IMAGE=sard-agent SARD_VERSION=<tag> \
#          [UPGRADE_FROM=ghcr.io/artur-abalov/sard-server:<first release with DB2>] scripts/test-self-agent.sh <workdir>
# UPGRADE_FROM_AGENT is the agent image of that version (default: UPGRADE_FROM with
# sard-server replaced by sard-agent); UPGRADE_COMPOSE is its compose file (default:
# deploy/docker-compose.yml of the git tag v<its tag>, `git show`).
# The compose file runs the agent's -pg18 variant: $SARD_AGENT_IMAGE:<tag>-pg18 must exist
# (make e2e-agent-images builds it as sard-agent:e2e-pg18).
# The stack uses the fixed project name "sard": it removes that project's volumes. A pass
# takes the stack down; a failure leaves it up for a look (compose.log in <workdir>).
set -euo pipefail

[ $# -eq 1 ] || { echo "usage: $0 <workdir>" >&2; exit 2; }
root="$(cd "$(dirname "$0")/.." && pwd)"
work="$1"
mkdir -p "$work" && cd "$work"
# What a release ships: the compose file and the files it refers to as ./db/… (scripts/release-server-files.sh).
cp "$root/deploy/docker-compose.yml" docker-compose.yml
mkdir -p db && cp "$root"/deploy/db/* db/
# The administrator password is set in the wizard and exists only in this run.
admin="$(openssl rand -hex 16)"
db_password="$(openssl rand -hex 24)" # only the deferred upgrade check (the old release's .env) uses it
cat >.env <<EOF
SARD_VERSION=${SARD_VERSION:?}
SARD_IMAGE=${SARD_IMAGE:?}
SARD_AGENT_IMAGE=${SARD_AGENT_IMAGE:?}
EOF
# Group 10001 is the sidecar's: it reads .env for the self-backup (docs/operator/02-install.md).
chgrp 10001 .env 2>/dev/null || sudo chgrp 10001 .env
chmod 640 .env
# shellcheck source=lib/setup-wizard.sh
source "$root/scripts/lib/setup-wizard.sh"

fail() { echo "FAIL: $*" >&2; docker compose logs --no-color >"$work/compose.log" 2>&1 || true; exit 1; }
pass() { echo "ok: $*"; }
api="http://127.0.0.1:8080/api/v1"
cookies="$work/cookies"
export SARD_WIZARD_COOKIES="$cookies"
server_log() { docker compose logs --no-color server 2>&1; }
login() { curl -fsS -c "$cookies" -H 'Content-Type: application/json' --data-binary @- "$api/session" <<<"{\"password\":\"$admin\"}" >/dev/null; }
get() { curl -fsS -b "$cookies" "$api$1"; }
# Live built-in agents as "id status" lines.
builtin_agents() { get /agents | python3 -c '
import json, sys
for a in json.load(sys.stdin)["items"]:
    if a["builtin"] and a["revokedAt"] is None:
        print(a["id"], a["status"])'; }
wait_online() { # $1: seconds; prints the online built-in agent id
  for _ in $(seq 1 "$1"); do
    line="$(builtin_agents | awk '$2 == "online"' | head -1)"
    [ -n "$line" ] && { echo "${line%% *}"; return 0; }
    sleep 1
  done
  return 1
}
channel() { docker compose exec -T server sh -c "$1"; }
# DB1: what initdb fixed and what the mounted settings say, one line.
db1_settings() {
  docker compose exec -T postgres psql -U sard -d sard -qAt -F' ' -c "select current_setting('server_version'), \
    current_setting('data_checksums'), pg_encoding_to_char(encoding), datlocprovider, coalesce(datlocale, '-'), \
    current_setting('log_timezone'), current_setting('shared_buffers'), current_setting('max_connections'), \
    current_setting('wal_level'), current_setting('track_io_timing') from pg_database where datname = 'sard'"
}
db1_warning='the database differs from what a new installation creates'
# shellcheck source=../deploy/postgres.env
pinned_version="$(. "$root/deploy/postgres.env" && echo "$SARD_POSTGRES_IMAGE" | sed -E 's/^postgres:([0-9.]+)-.*/\1/')"

docker compose down -v --remove-orphans >/dev/null 2>&1 || true

if [ -n "${UPGRADE_FROM:-}" ]; then
  # 5: a released 0.1.0-beta.N with its own compose file (sidecar included), wizard passed.
  old_tag="${UPGRADE_FROM##*:}"
  old_agent="${UPGRADE_FROM_AGENT:-${UPGRADE_FROM/sard-server/sard-agent}}"
  old="$work/old" && mkdir -p "$old"
  if [ -n "${UPGRADE_COMPOSE:-}" ]; then
    cp "$UPGRADE_COMPOSE" "$old/docker-compose.yml"
  else
    git -C "$root" show "v$old_tag:deploy/docker-compose.yml" >"$old/docker-compose.yml" \
      || fail "5: no deploy/docker-compose.yml in the tag v$old_tag"
  fi
  cat >"$old/.env" <<EOF
SARD_VERSION=$old_tag
SARD_IMAGE=${UPGRADE_FROM%:*}
SARD_AGENT_IMAGE=${old_agent%:*}
SARD_DB_PASSWORD=$db_password
EOF
  chmod 600 "$old/.env"
  (cd "$old" && docker compose up -d --wait >/dev/null) || fail "5: the old version does not start"
  # The code is in the log of the old stack; its compose project is the same ("sard").
  server_log() { (cd "$old" && docker compose logs --no-color server 2>&1); }
  sard_complete_wizard "http://127.0.0.1:8080" "$admin" server_log || fail "5: the wizard of the old version"
  old_token="$(curl -fsS -b "$cookies" -H 'Content-Type: application/json' -d '{"label":"upgrade"}' "$api/enrollment-tokens")"
  [ -n "$old_token" ] || fail "5: no token from the old version"
  # The old sidecar enrolls by itself after the step ca.
  sidecar="$(wait_online 180)" || fail "5: the old version has no online built-in agent within 180 s"
  ca_before="$(cd "$old" && docker compose exec -T server sha256sum /var/lib/sard/pki/ca/ca.crt | cut -d' ' -f1)"
  # An ordinary agent of the old version, enrolled through the server's own network namespace
  # (localhost is in the old certificate).
  raw="$(printf '%s' "$old_token" | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])')"
  docker run --rm --network "container:$(cd "$old" && docker compose ps -q server)" --user 0 --entrypoint sh \
    "${old_agent%:*}:$old_tag" -c "mkdir -p /tmp/o/tls && printf 'server:\n  address: localhost:9090\ntls:\n  ca_file: /tmp/o/tls/ca.pem\n  cert_file: /tmp/o/tls/agent.pem\n  key_file: /tmp/o/tls/agent.key\n' >/tmp/o/agent.yaml && /usr/libexec/sard/sard-agent enroll --config /tmp/o/agent.yaml --token '$raw'" \
    >"$work/old-enroll.log" 2>&1 || fail "5: enrolling an agent on the old version: $(cat "$work/old-enroll.log")"
  # Ids and whether they are live; the tokens by id and status.
  agents_of() { get /agents | python3 -c 'import json,sys; print("\n".join(sorted("%s %s" % (a["id"], a["revokedAt"] is None) for a in json.load(sys.stdin)["items"])))'; }
  tokens_of() { get /enrollment-tokens | python3 -c 'import json,sys; print("\n".join(sorted("%s %s" % (t["id"], t["status"]) for t in json.load(sys.stdin)["items"])))'; }
  agents_before="$(agents_of)"
  tokens_before="$(tokens_of)"
  [ "$(printf '%s\n' "$agents_before" | wc -l)" -ge 2 ] || fail "5: expected the built-in and the ordinary agent, got: $agents_before"
  pass "5: old version $UPGRADE_FROM up, wizard passed, agents: $(printf '%s' "$agents_before" | tr '\n' ';') CA $ca_before"
  # The old stack goes, its volumes stay: this compose file takes them over.
  (cd "$old" && docker compose down --remove-orphans >/dev/null) || fail "5: stopping the old version"
  server_log() { docker compose logs --no-color server 2>&1; }
  upgraded_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  docker compose up -d --wait >/dev/null || fail "5: up --wait with the current compose file"
  login || fail "5: sign-in with the password of the wizard after the upgrade"
  pass "5: sign-in with the password of the wizard after the upgrade"
  [ "$(agents_of)" = "$agents_before" ] || fail "5: agents changed: before '$agents_before', after '$(agents_of)'"
  [ "$(tokens_of)" = "$tokens_before" ] || fail "5: tokens changed: before '$tokens_before', after '$(tokens_of)'"
  [ "$(docker compose exec -T server sha256sum /var/lib/sard/pki/ca/ca.crt | cut -d' ' -f1)" = "$ca_before" ] || fail "5: the CA changed"
  steps="$(get /onboarding | python3 -c 'import json,sys; print(" ".join("%s=%s" % (s["id"], s["state"]) for s in json.load(sys.stdin)["steps"][:2]))')"
  [ "$steps" = "ca=done admin=done" ] || fail "5: steps after the upgrade: $steps"
  docker compose logs --no-color --since "$upgraded_at" server 2>&1 | grep -q 'SARD SETUP CODE' && fail "5: the upgraded server printed a setup code"
  failed="$(docker compose exec -T postgres psql -U sard -d sard -qAtc 'select count(*) from flyway_schema_history where not success')"
  applied="$(docker compose exec -T postgres psql -U sard -d sard -qAtc 'select count(*) from flyway_schema_history')"
  [ "$failed" = 0 ] && [ "$applied" -gt 0 ] || fail "5: Flyway: $applied applied, $failed failed"
  id="$(wait_online 180)" || fail "5: no online built-in agent within 180 s after the upgrade"
  [ "$id" = "$sidecar" ] || fail "5: the built-in agent is $id after the upgrade, was $sidecar"
  pass "5: after the upgrade: same CA, agents and tokens, steps ca and admin done, no setup code in the log, Flyway $applied applied; built-in agent $id online"
  settings="$(db1_settings)"
  case "$settings" in
    "$pinned_version "*" c - UTC 256MB 50 replica on") ;;
    *) fail "9: the upgraded database: $settings (want the pinned $pinned_version, libc locale kept, the profile small)" ;;
  esac
  server_log | grep "$db1_warning" | grep -q 'locale=libc' || fail "9: no warning about the old database's locale in the server's log"
  pass "9: the old database keeps its locale, has the profile small ($settings), the server warns about it"
  captured=""
else
  start=$(date +%s)
  # Before the step ca nothing is issued: the channel has no token (F4a).
  docker compose up -d --wait server >/dev/null || fail "1: up --wait server"
  sleep 60 # the spec waits one minute: several check intervals (15 s) without a token
  channel '! test -e /var/lib/sard/self/enroll-token' || fail "1: a token in the channel before the step ca"
  pass "1: no token in the channel before the step ca"
  # The wizard: code from the log, CA, administrator. The token is captured before the
  # sidecar starts, for check 4; the stack itself needs nothing.
  sard_complete_wizard "http://127.0.0.1:8080" "$admin" server_log || fail "1: the first-start wizard"
  for _ in $(seq 1 60); do channel 'test -s /var/lib/sard/self/enroll-token' && break; sleep 1; done
  captured="$(channel 'cat /var/lib/sard/self/enroll-token')" || fail "1: no token in the channel after the step ca"
  listed="$(get /enrollment-tokens | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["items"]))')"
  [ "$listed" = 0 ] || fail "4: the REST token list shows $listed tokens, the built-in one among them"
  pass "4: before enrollment the built-in token is in the channel only, not in the REST list"
  docker compose up -d --wait >/dev/null || fail "1: up --wait"
  id="$(wait_online 180)" || fail "1: no online built-in agent within 180 s"
  pass "1: sard-self $id online $(( $(date +%s) - start )) s after up and the wizard"
  [ "$(builtin_agents | wc -l)" -eq 1 ] || fail "1: more than one live built-in agent"
  get "/agents/$id" | python3 -c '
import json, sys
a = json.load(sys.stdin)
assert a["hostname"] == "sard-self", a["hostname"]
assert "sard-db" in a["secretNames"], a["secretNames"]' || fail "1: card: hostname or secret sard-db"
  pass "1: card: hostname sard-self, secret sard-db"
  for _ in $(seq 1 30); do channel '! test -e /var/lib/sard/self/enroll-token' && break; sleep 1; done
  channel '! test -e /var/lib/sard/self/enroll-token' || fail "1: the token file is still there"
  pass "1: the server removed the token file after enrollment"
fi
# The passwords of the roles (DB2): db-init made them, each is a file of the volume of the one service that reads it.
app_pw="$(docker compose exec -T server cat /run/sard-db/password)"
pw="$(docker compose exec -T self-agent cat /run/sard-self/db-password)" # sard_backup
migrator_pw="$(docker compose run --rm --no-deps -T --entrypoint cat migrate /run/sard-db/password)"
login_as() { # role password: logs in over the network of the stack, not the local socket
  docker compose exec -T -e PGPASSWORD="$2" postgres psql -h 127.0.0.1 -U "$1" -d sard -qAtc 'select current_user' 2>/dev/null
}
[ "$(login_as sard_backup "$pw")" = sard_backup ] || fail "1: sard_backup does not log in with its password"
pass "1: sard_backup logs in with the password mounted in the neighbour"

if [ -z "${UPGRADE_FROM:-}" ]; then
  settings="$(db1_settings)"
  [ "$settings" = "$pinned_version on UTF8 b C.UTF-8 UTC 256MB 50 replica on" ] \
    || fail "9: the new database: $settings (want $pinned_version on UTF8 b C.UTF-8 UTC 256MB 50 replica on)"
  server_log | grep -q "$db1_warning" && fail "9: the server warns about a database it created itself"
  pass "9: the new database: $settings, no warning in the server's log"
fi

# 10 to 15 (DB2, docs/specs/server/database-roles.feature, @deploy): the roles and passwords of the database.
if [ -z "${UPGRADE_FROM:-}" ]; then
  psql_su() { docker compose exec -T postgres psql -U sard -d sard -qAtc "$1"; }
  # 10: the five roles with their attributes, no sard_self, the schema sard owned by sard_owner, no table in public.
  roles="$(psql_su "select string_agg(rolname || ':' || rolcanlogin::int || rolsuper::int || rolcreaterole::int || rolcreatedb::int || rolreplication::int || rolbypassrls::int, ' ' order by rolname) from pg_roles where rolname like 'sard\_%'")"
  [ "$roles" = "sard_app:100000 sard_backup:100000 sard_migrator:100000 sard_monitor:100000 sard_owner:000000" ] \
    || fail "10: the roles of Sard are: $roles"
  [ "$(psql_su "select count(*) from pg_tables where schemaname = 'public'")" = 0 ] || fail "10: tables in public"
  [ "$(psql_su "select string_agg(distinct tableowner, ',') from pg_tables where schemaname = 'sard'")" = sard_owner ] \
    || fail "10: the tables of the schema sard are not all owned by sard_owner"
  pass "10: five roles without superuser rights, no sard_self, the schema sard owned by sard_owner, public empty"
  # 11: the server works as sard_app, the neighbour dumps as sard_backup.
  users="$(psql_su "select string_agg(distinct usename, ',' order by usename) from pg_stat_activity where datname = 'sard' and backend_type = 'client backend' and pid <> pg_backend_pid()")"
  case "$users" in sard_app | sard_app,sard_backup) ;; *) fail "11: sessions of the installation are of: $users" ;; esac
  pass "11: the connections of the running server are sard_app's ($users)"
  # 12: each password file is seen by its one consumer, read-only, 64 hex characters.
  [ "$(printf '%s' "$app_pw" | grep -Ec '^[0-9a-f]{64}$')" = 1 ] && [ "$(printf '%s' "$pw" | grep -Ec '^[0-9a-f]{64}$')" = 1 ] \
    && [ "$(printf '%s' "$migrator_pw" | grep -Ec '^[0-9a-f]{64}$')" = 1 ] || fail "12: a password is not 64 hex characters"
  docker compose exec -T server test ! -w /run/sard-db/password || fail "12: the server may write its password file"
  docker compose exec -T self-agent test ! -w /run/sard-self/db-password || fail "12: the neighbour may write its password file"
  docker compose exec -T server sh -c '! test -e /run/sard-db/migrate && ! test -e /run/sard-self/db-password' \
    || fail "12: the server sees a password of another consumer"
  docker compose exec -T self-agent sh -c '! test -e /run/sard-db' || fail "12: the neighbour sees /run/sard-db"
  for service in server self-agent postgres; do
    docker inspect "$(docker compose ps -q "$service")" --format '{{range .Mounts}}{{.Name}} {{end}}' | grep -q 'sard-db-migrate' \
      && fail "12: the volume of the password of sard_migrator is mounted in $service"
    docker compose exec -T "$service" env 2>/dev/null | grep -F -q -e "$migrator_pw" && fail "12: the password of sard_migrator is in the environment of $service"
  done
  pass "12: each password file is 64 hex characters, seen by its one consumer, read-only; sard_migrator's is in no long-lived container"
  # 13: no value of a password in .env, the compose file, docker inspect or the logs of the stack.
  everything="$(cat .env docker-compose.yml; docker inspect $(docker compose ps -aq) 2>/dev/null; docker compose logs --no-color 2>&1)"
  for secret in "$app_pw" "$pw" "$migrator_pw"; do
    grep -F -q -e "$secret" <<<"$everything" && fail "13: a password of a database role is in .env, the compose file, docker inspect or a log"
  done
  grep -q 'DB_PASSWORD' .env && fail "13: .env names a database password"
  pass "13: no password of a database role in .env, docker-compose.yml, docker inspect or the logs"
  # 14: the superuser does not log in over the network with any password of the installation; the socket stays open.
  for secret in "" "$app_pw" "$pw" "$migrator_pw"; do
    docker compose exec -T -e PGPASSWORD="$secret" postgres psql -h postgres -U sard -d sard -qAtc 'select 1' >/dev/null 2>&1 \
      && fail "14: the superuser logged in over the network"
  done
  [ "$(docker compose exec -T postgres psql -U sard -d sard -Atc 'select current_user')" = sard ] || fail "14: the local socket does not let the superuser in"
  pass "14: the superuser is refused over the network and let in over the local socket without a password"
  # 15: a restart changes neither the passwords nor the catalog of the schema sard, and says no WARN or ERROR in db-init and migrate.
  catalog() { psql_su "select string_agg(x, ';' order by x) from (select 'r ' || rolname || rolcanlogin || rolsuper as x from pg_roles where rolname like 'sard\_%' union all select 'm ' || u.rolname || g.rolname from pg_auth_members m join pg_roles u on u.oid = m.member join pg_roles g on g.oid = m.roleid where u.rolname like 'sard\_%' union all select 'c ' || relname || relowner::regrole || coalesce(relacl::text, '-') from pg_class where relnamespace = 'sard'::regnamespace and relkind in ('r','p') union all select 'd ' || defaclrole::regrole || defaclobjtype::text || defaclacl::text from pg_default_acl) t"; }
  catalog_before="$(catalog)"
  docker compose down >/dev/null 2>&1 && docker compose up -d --wait >/dev/null || fail "15: the restart"
  [ "$(docker compose exec -T server cat /run/sard-db/password)" = "$app_pw" ] || fail "15: the password of sard_app changed"
  [ "$(docker compose exec -T self-agent cat /run/sard-self/db-password)" = "$pw" ] || fail "15: the password of sard_backup changed"
  [ "$(catalog)" = "$catalog_before" ] || fail "15: the catalog of the roles and the schema sard changed"
  docker compose logs --no-color db-init migrate 2>&1 | grep -E 'WARN|ERROR' | grep -v 'level=' && fail "15: a WARN or an ERROR in the logs of db-init or migrate"
  pass "15: after down and up the passwords and the catalog are the same, db-init and migrate are quiet"
fi

# 2: re-created twice, the same agent and no new token.
tokens_issued() { docker compose exec -T postgres psql -U sard -d sard -qAtc 'select count(*) from enrollment_tokens where builtin'; }
before="$(tokens_issued)"
docker compose up -d --force-recreate --wait self-agent >/dev/null
again="$(wait_online 60)" || fail "2: not online after --force-recreate"
[ "$again" = "$id" ] || fail "2: --force-recreate made agent $again, not $id"
docker compose rm -sf self-agent >/dev/null && docker compose up -d --wait self-agent >/dev/null
again="$(wait_online 60)" || fail "2: not online after rm + up"
[ "$again" = "$id" ] || fail "2: rm + up made agent $again, not $id"
sleep 20 # more than one check interval
[ "$(tokens_issued)" = "$before" ] || fail "2: a built-in token was issued while the agent was live"
pass "2: re-created (force-recreate, rm + up): the same agent $id, no new built-in token"

# 4: the channel and the used token.
mounts() { docker inspect "$(docker compose ps -q "$1")" --format '{{range .Mounts}}{{.Name}}:{{.RW}} {{end}}'; }
users="$(for c in $(docker ps -q); do docker inspect "$c" --format '{{.Name}} {{range .Mounts}}{{.Name}} {{end}}'; done | grep -c 'sard_sard-self-channel' || true)"
[ "$users" -eq 2 ] || fail "4: $users containers mount the channel, not 2"
mounts self-agent | grep -q 'sard_sard-self-channel:false' || fail "4: the channel is not read-only in the sidecar"
docker compose run --rm --no-deps --entrypoint sh self-agent -c 'touch /run/sard-self/x' 2>/dev/null && fail "4: the sidecar wrote to the channel"
pass "4: the channel is mounted by server and self-agent only, read-only in self-agent"

# 7 (F6): the CA and the installation directory are read-only in the sidecar; .env is readable there.
targets() { docker inspect "$(docker compose ps -q "$1")" --format '{{range .Mounts}}{{.Destination}}:{{.RW}} {{end}}'; }
for dir in /var/lib/sard/pki /etc/sard/install; do
  targets self-agent | grep -q "$dir:false" || fail "7: $dir is not mounted read-only in the sidecar"
  docker compose exec -T self-agent sh -c "touch $dir/x" 2>/dev/null && fail "7: the sidecar wrote to $dir"
done
docker compose exec -T self-agent sh -c 'test -r /etc/sard/install/.env && test -r /var/lib/sard/pki/ca/ca.key' ||
  fail "7: the sidecar cannot read .env or the CA key"
pass "7: the CA and the installation directory are read-only in the sidecar, .env and the CA key readable"
# The role of the dump: even with read-only switched off for the session, nothing is written.
role_write() {
  docker compose exec -T -e PGPASSWORD="$pw" postgres psql -h 127.0.0.1 -U sard_backup -d sard -v ON_ERROR_STOP=1 -qAtc \
    "set default_transaction_read_only = off; $1" >/dev/null 2>&1
}
role_write 'select count(*) from agents' || fail "7: sard_backup cannot read the database"
for sql in "update tenants set name = name" "create table x (i int)" "create temp table x (i int)" "select lo_create(0)"; do
  role_write "$sql" && fail "7: sard_backup ran: $sql"
done
pass "7: sard_backup reads the database and writes nothing (update, create, temp, large object)"
# The captured token from another container on the compose network: refused, TOKEN_USED (exit 3).
# Only on the clean install: the token of an upgraded stack was used before the upgrade.
if [ -n "$captured" ]; then
set +e
docker compose run --rm --no-deps -T --entrypoint sh self-agent -c \
  "mkdir -p /tmp/o/tls && sed 's|/var/lib/sard-self/tls|/tmp/o/tls|' /etc/sard/self/agent.yaml | grep -v 'sard-db' | grep -v '^secrets:' >/tmp/o/agent.yaml && \
   /usr/libexec/sard/sard-agent enroll --config /tmp/o/agent.yaml --token '$captured'" >"$work/reuse.log" 2>&1
code=$?
set -e
[ "$code" -eq 3 ] || fail "4: reusing the built-in token from another container exited $code, not 3"
grep -q "TOKEN_USED\|already been used\|used" "$work/reuse.log" || fail "4: no TOKEN_USED in: $(cat "$work/reuse.log")"
grep -q "$captured" "$work/reuse.log" && fail "4: the token in the enroll output"
pass "4: the built-in token, used by sard-self, is refused from another container (exit 3)"
fi
for name in $(docker compose config --services); do
  docker compose logs --no-color "$name" 2>&1 | grep -F -q -e "${captured:-no-token}" -e "$pw" -e "$app_pw" -e "$migrator_pw" \
    && fail "4: a token or a password of a database role in the logs of $name"
done
pass "4: neither a token nor a password of a database role in any service's log"
# Decision 7: revoked only with the confirmation, then the sidecar enrolls again by itself.
post() { curl -s -o /dev/null -w '%{http_code}' -b "$cookies" -X POST "$api$1"; }
status="$(post "/agents/$id/revoke")"
[ "$status" = 409 ] || fail "revoke without confirm is $status, not 409"
[ "$(builtin_agents | awk '{print $1}')" = "$id" ] || fail "revoke without confirm changed the agent"
status="$(post "/agents/$id/revoke?confirm=sard-self")"
[ "$status" = 200 ] || fail "revoke with confirm is $status, not 200"
# Until the onboarding is complete (F4b Р22) only the revoke of the live built-in agent is allowed: a repeated
# one is refused as onboarding_incomplete and leaves the revocation as it was.
revoked_at() { get "/agents/$id" | python3 -c 'import json,sys; print(json.load(sys.stdin)["revokedAt"])'; }
before="$(revoked_at)"
again="$(curl -s -b "$cookies" -X POST "$api/agents/$id/revoke")"
case "$again" in *'"onboarding_incomplete"'*) ;; *) fail "a repeated revoke before the onboarding is complete: $again" ;; esac
[ "$(revoked_at)" = "$before" ] || fail "a refused repeated revoke changed revokedAt"
pass "revoke: 409 without confirm, 200 with it; a repeated one is onboarding_incomplete and changes nothing"
next="$(wait_online 180)" || fail "no new built-in agent within 180 s after the revoke"
[ "$next" != "$id" ] || fail "the revoked agent is online again"
pass "revoke: the sidecar enrolled again by itself as $next"
# 6: access recovery on compose (F4a): admin-reset, a restart, the new code, the admin step with a new
# password; the old password stops working and the installation's data stays.
agents_before_reset="$(get /agents | python3 -c 'import json,sys; print(" ".join(sorted(a["id"] for a in json.load(sys.stdin)["items"])))')"
old_admin="$admin"
docker compose run --rm -T server admin-reset >"$work/admin-reset.log" 2>&1 || fail "6: admin-reset: $(cat "$work/admin-reset.log")"
grep -q 'administrator password is removed' "$work/admin-reset.log" || fail "6: admin-reset said: $(cat "$work/admin-reset.log")"
docker compose restart server >/dev/null || fail "6: restart server"
docker compose up -d --wait server >/dev/null || fail "6: the server is not healthy after the restart"
admin="$(openssl rand -hex 16)"
sard_complete_wizard "http://127.0.0.1:8080" "$admin" server_log || fail "6: the wizard after admin-reset"
login || fail "6: sign-in with the recovered password"
[ "$(curl -s -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' --data-binary @- "$api/session" <<<"{\"password\":\"$old_admin\"}")" = 401 ] \
  || fail "6: the old password still signs in"
[ "$(get /agents | python3 -c 'import json,sys; print(" ".join(sorted(a["id"] for a in json.load(sys.stdin)["items"])))')" = "$agents_before_reset" ] \
  || fail "6: agents changed by the recovery"
pass "6: admin-reset + restart + wizard: the new password signs in, the old one is refused (401), agents kept"
# 8 (F4b): steps 3 and 4 of the wizard through REST, with a local repository (OQ-207).
json_field() { python3 -c 'import json,sys; d = json.load(sys.stdin); print(eval(sys.argv[1], {"d": d}))' "$1"; }
send() { # METHOD PATH [BODY]: prints "<status> <body>" on one line
  local out
  out="$(curl -sS -w '\n%{http_code}' -b "$cookies" -X "$1" -H 'Content-Type: application/json' --data-binary "${3:-}" "$api$2")" || return 1
  printf '%s %s\n' "${out##*$'\n'}" "$(printf '%s' "${out%$'\n'*}" | tr -d '\n')"
}
restriction() { get /onboarding | json_field 'd["restriction"]'; }
[ "$(restriction)" != none ] || fail "8: the restriction is none before the self-backup is bound"
cmd="$(get '/self-backup/repository-command?storage=local')" || fail "8: no repository-command for local storage"
repo="$(printf '%s' "$cmd" | json_field 'd["repositoryName"]')"
address="$(printf '%s' "$cmd" | json_field 'd["address"]')"
printf '%s' "$cmd" | json_field '"\n".join(d["commands"])' >"$work/repo-commands.sh"
[ -s "$work/repo-commands.sh" ] || fail "8: the returned command list is empty"
# In a terminal, as the owner runs them: docker compose run gives the container a TTY, and repo add shows the
# password of the recovery key there once (V3); without one it refuses (RECOVERY_KEY_OUTPUT_MISSING).
while IFS= read -r line; do
  [ -n "$line" ] || continue
  script -qec "$line" /dev/null </dev/null >>"$work/repo-add.log" 2>&1 \
    || fail "8: command failed: $line: $(tail -5 "$work/repo-add.log")"
done <"$work/repo-commands.sh"
pass "8: the commands of repository-command for $repo ran"
repo_id=""
for _ in $(seq 1 120); do
  repo_id="$(get /self-backup | json_field 'next((r["repositoryId"] for r in d["repositories"] if r["name"] == "'"$repo"'" and r["repositoryId"]), "")' 2>/dev/null || true)"
  [ -n "$repo_id" ] && break
  sleep 1
done
[ -n "$repo_id" ] || fail "8: the repository $repo has no restic id in GET /self-backup within 120 s"
pass "8: repository $repo registered with restic id $repo_id"
bound="$(send PUT /self-backup/repository "{\"repositoryName\":\"$repo\",\"confirmLocalStorage\":true,\"location\":\"$address\"}")"
[ "${bound%% *}" = 200 ] || fail "8: binding answered ${bound%% *}: ${bound#* }"
started="$(send POST /self-backup/runs)"
[ "${started%% *}" = 200 ] || fail "8: back up now answered ${started%% *}: ${started#* }"
run_ids="$(printf '%s' "${started#* }" | json_field '" ".join(r["runId"] for r in d["runs"])')"
[ "$(wc -w <<<"$run_ids")" -eq 2 ] || fail "8: expected two runs, got: $run_ids"
for run in $run_ids; do
  status=""
  for _ in $(seq 1 300); do
    status="$(get "/runs/$run" | json_field 'd["status"]')"
    case "$status" in succeeded | failed | cancelled) break ;; esac
    sleep 1
  done
  [ "$status" = succeeded ] || fail "8: run $run ended as '$status' within 300 s"
done
pass "8: both system sources backed up to $repo (runs $run_ids)"
reveal="$(get /self-backup | json_field 'd["passwordRevealCommand"]')"
[ -n "$reveal" ] && [ "$reveal" != None ] || fail "8: no passwordRevealCommand after the binding"
revealed="$(bash -c "$reveal" </dev/null 2>"$work/reveal.err")" || fail "8: the password command failed: $(cat "$work/reveal.err")"
[ -n "$revealed" ] || fail "8: the password command printed nothing"
pass "8: the password command prints a password"
[ "$(restriction)" != none ] || fail "8: the restriction is none before the key copy is confirmed"
confirmed="$(send POST /self-backup/keys-confirmation '{"copiedOutsideServer":true}')"
[ "${confirmed%% *}" = 204 ] || fail "8: the key copy confirmation answered ${confirmed%% *}: ${confirmed#* }"
[ "$(restriction)" = none ] || fail "8: the restriction is $(restriction) after the confirmation, not none"
issued="$(send POST /enrollment-tokens '{"label":"after-onboarding"}')"
[ "${issued%% *}" = 201 ] || fail "8: an enrollment token answered ${issued%% *}: ${issued#* }"
pass "8: the key copy confirmed (204), restriction none, an enrollment token issued (201)"
# 16 (DB2): the procedure of docs/operator/16-postgresql.md, "Смена пароля роли", for sard_app.
if [ -z "${UPGRADE_FROM:-}" ]; then
  docker compose run --rm --no-deps -T --entrypoint rm db-init /run/sard-db/app/password >/dev/null || fail "16: removing the password file"
  docker compose up -d --wait >/dev/null || fail "16: up after the removal"
  docker compose restart server >/dev/null && docker compose up -d --wait server >/dev/null || fail "16: restarting the server"
  new_pw="$(docker compose exec -T server cat /run/sard-db/password)"
  [ "$new_pw" != "$app_pw" ] && [ "$(printf '%s' "$new_pw" | grep -Ec '^[0-9a-f]{64}$')" = 1 ] || fail "16: the password file did not change"
  [ "$(login_as sard_app "$new_pw")" = sard_app ] || fail "16: sard_app does not log in with the new password"
  [ -z "$(login_as sard_app "$app_pw")" ] || fail "16: sard_app still logs in with the old password"
  curl -fsS "$api/status" >/dev/null || fail "16: the server does not answer after the procedure"
  pass "16: the password of sard_app changed by the documented procedure, the server answers"
fi
docker compose down -v --remove-orphans >/dev/null 2>&1 # it holds ports 8080 and 9090 of the host
echo "PASSED"
