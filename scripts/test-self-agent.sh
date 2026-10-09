#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# The agent next to the server (sard-self, docs/operations/self-agent.md) on a real
# docker compose stack of deploy/docker-compose.yml: F5's checks 1, 2 and 4, and with
# UPGRADE_FROM also check 5 (docs/specs/server/self-agent.feature, @e2e/@upgrade).
#
#   1  a clean `up --wait` gives an online built-in agent sard-self, no user action
#   2  a re-created sidecar is the same agent, and no new token is issued
#   4  the used built-in token is refused (TOKEN_USED) from another container; the
#      channel is mounted by the server and the sidecar only, read-only in the
#      sidecar; neither the token nor the role password is in any log
#   7  (F6) what the self-backup reads stays read-only in the sidecar: the CA directory and
#      the installation directory are mounted read-only and refuse a write, .env is readable
#      through group 10001; the role sard_self still cannot write to the database
#   5  (UPGRADE_FROM=<server image>) a stack of that version with an agent of its own
#      is replaced by this compose file: the agent and the CA are still there, the
#      sidecar appears and enrolls by itself
#
# Usage: SARD_IMAGE=sard-server SARD_AGENT_IMAGE=sard-agent SARD_VERSION=<tag> \
#          [UPGRADE_FROM=sard-server:rc1] scripts/test-self-agent.sh <workdir>
# The stack uses the fixed project name "sard": it removes that project's volumes. A pass
# takes the stack down; a failure leaves it up for a look (compose.log in <workdir>).
set -euo pipefail

[ $# -eq 1 ] || { echo "usage: $0 <workdir>" >&2; exit 2; }
root="$(cd "$(dirname "$0")/.." && pwd)"
work="$1"
mkdir -p "$work" && cd "$work"
cp "$root/deploy/docker-compose.yml" docker-compose.yml
admin="$(openssl rand -hex 16)"
cat >.env <<EOF
SARD_VERSION=${SARD_VERSION:?}
SARD_IMAGE=${SARD_IMAGE:?}
SARD_AGENT_IMAGE=${SARD_AGENT_IMAGE:?}
SARD_DB_PASSWORD=$(openssl rand -hex 24)
SARD_ADMIN_PASSWORD=$admin
EOF
# Group 10001 is the sidecar's: it reads .env for the self-backup (docs/operator/02-install.md).
chgrp 10001 .env 2>/dev/null || sudo chgrp 10001 .env
chmod 640 .env

fail() { echo "FAIL: $*" >&2; docker compose logs --no-color >"$work/compose.log" 2>&1 || true; exit 1; }
pass() { echo "ok: $*"; }
api="http://127.0.0.1:8080/api/v1"
cookies="$work/cookies"
login() { curl -fsS -c "$cookies" -H 'Content-Type: application/json' -d "{\"password\":\"$admin\"}" "$api/session" >/dev/null; }
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

docker compose down -v --remove-orphans >/dev/null 2>&1 || true

if [ -n "${UPGRADE_FROM:-}" ]; then
  # 5: the old version with this compose file's postgres/server part only.
  old="$work/old" && mkdir -p "$old"
  python3 - "$root/deploy/docker-compose.yml" "$old/docker-compose.yml" <<'PY'
import re, sys
s = open(sys.argv[1]).read()
s = s[: s.index("  # sard-self:")] + "volumes:\n  postgres-data:\n  sard-pki:\n"
s = s.replace("      SARD_SELF_DIR: /var/lib/sard/self\n", "").replace("      - sard-self-channel:/var/lib/sard/self\n", "")
s = s.replace(",server\n", "\n")
open(sys.argv[2], "w").write(s)
PY
  sed "s|^SARD_IMAGE=.*|SARD_IMAGE=${UPGRADE_FROM%%:*}|; s|^SARD_VERSION=.*|SARD_VERSION=${UPGRADE_FROM##*:}|" .env >"$old/.env"
  (cd "$old" && docker compose up -d --wait >/dev/null) || fail "5: the old version does not start"
  login
  old_token="$(curl -fsS -b "$cookies" -H 'Content-Type: application/json' -d '{"label":"upgrade"}' "$api/enrollment-tokens")"
  ca_before="$(docker compose exec -T server sha256sum /var/lib/sard/pki/ca/ca.crt | cut -d' ' -f1)"
  tokens_before="$(get /enrollment-tokens | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["items"]))')"
  [ -n "$old_token" ] || fail "5: no token from the old version"
  # An ordinary agent of the old version, enrolled through the server's own network namespace
  # (localhost is in the old certificate).
  raw="$(printf '%s' "$old_token" | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])')"
  docker run --rm --network "container:$(cd "$old" && docker compose ps -q server)" --user 0 --entrypoint sh \
    "$SARD_AGENT_IMAGE:$SARD_VERSION" -c "mkdir -p /tmp/o/tls && printf 'server:\n  address: localhost:9090\ntls:\n  ca_file: /tmp/o/tls/ca.pem\n  cert_file: /tmp/o/tls/agent.pem\n  key_file: /tmp/o/tls/agent.key\n' >/tmp/o/agent.yaml && /usr/libexec/sard/sard-agent enroll --config /tmp/o/agent.yaml --token '$raw'" \
    >"$work/old-enroll.log" 2>&1 || fail "5: enrolling an agent on the old version: $(cat "$work/old-enroll.log")"
  old_agents="$(get /agents | python3 -c 'import json,sys; print(" ".join(sorted(a["id"] for a in json.load(sys.stdin)["items"])))')"
  [ -n "$old_agents" ] || fail "5: no agent on the old version"
  pass "5: old version $UPGRADE_FROM up, agent $old_agents enrolled, CA $ca_before"
fi

start=$(date +%s)
# The token is captured before the sidecar starts, for check 4; the stack itself needs nothing.
docker compose up -d --wait server >/dev/null || fail "1: up --wait server"
for _ in $(seq 1 30); do channel 'test -s /var/lib/sard/self/enroll-token' && break; sleep 1; done
captured="$(channel 'cat /var/lib/sard/self/enroll-token')" || fail "1: no token in the channel"
login
listed="$(get /enrollment-tokens | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["items"]))')"
[ "$listed" = "${tokens_before:-0}" ] || fail "4: the REST token list shows $listed tokens, the built-in one among them"
pass "4: before enrollment the built-in token is in the channel only, not in the REST list"
docker compose up -d --wait >/dev/null || fail "1: up --wait"
id="$(wait_online 180)" || fail "1: no online built-in agent within 180 s"
pass "1: sard-self $id online $(( $(date +%s) - start )) s after up, no user action"
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
pw="$(channel 'cat /var/lib/sard/self/db-password')"
docker compose exec -T -e PGPASSWORD="$pw" postgres psql -h 127.0.0.1 -U sard_self -d sard -qAtc 'select count(*) from agents' >/dev/null \
  || fail "1: sard_self does not log in with the channel's password"
pass "1: sard_self logs in with the password from the channel"

if [ -n "${UPGRADE_FROM:-}" ]; then
  [ "$(docker compose exec -T server sha256sum /var/lib/sard/pki/ca/ca.crt | cut -d' ' -f1)" = "$ca_before" ] || fail "5: the CA changed"
  tokens_after="$(get /enrollment-tokens | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["items"]))')"
  [ "$tokens_after" = "$tokens_before" ] || fail "5: tokens $tokens_before before, $tokens_after after"
  for a in $old_agents; do get "/agents/$a" >/dev/null || fail "5: agent $a of the old version is gone"; done
  pass "5: after the upgrade: same CA, the old agent $old_agents and tokens ($tokens_after) kept, the sidecar enrolled"
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
  docker compose exec -T -e PGPASSWORD="$pw" postgres psql -h 127.0.0.1 -U sard_self -d sard -v ON_ERROR_STOP=1 -qAtc \
    "set default_transaction_read_only = off; $1" >/dev/null 2>&1
}
role_write 'select count(*) from agents' || fail "7: sard_self cannot read the database"
for sql in "update tenants set name = name" "create table x (i int)" "create temp table x (i int)" "select lo_create(0)"; do
  role_write "$sql" && fail "7: sard_self ran: $sql"
done
pass "7: sard_self reads the database and writes nothing (update, create, temp, large object)"
# The captured token from another container on the compose network: refused, TOKEN_USED (exit 3).
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
for name in $(docker compose ps --services); do
  docker compose logs --no-color "$name" 2>&1 | grep -F -q -e "$captured" -e "$pw" && fail "4: a token or the password in the logs of $name"
done
pass "4: neither a token nor the role password in any service's log"
# Decision 7: revoked only with the confirmation, then the sidecar enrolls again by itself.
post() { curl -s -o /dev/null -w '%{http_code}' -b "$cookies" -X POST "$api$1"; }
[ "$(post "/agents/$id/revoke")" = 409 ] || fail "revoke without confirm is not 409"
[ "$(builtin_agents | awk '{print $1}')" = "$id" ] || fail "revoke without confirm changed the agent"
[ "$(post "/agents/$id/revoke?confirm=sard-self")" = 200 ] || fail "revoke with confirm is not 200"
[ "$(post "/agents/$id/revoke")" = 200 ] || fail "a repeated revoke without confirm is not 200"
pass "revoke: 409 without confirm, 200 with it, 200 again for the revoked agent"
next="$(wait_online 180)" || fail "no new built-in agent within 180 s after the revoke"
[ "$next" != "$id" ] || fail "the revoked agent is online again"
pass "revoke: the sidecar enrolled again by itself as $next"
docker compose down -v --remove-orphans >/dev/null 2>&1 # it holds ports 8080 and 9090 of the host
echo "PASSED"
