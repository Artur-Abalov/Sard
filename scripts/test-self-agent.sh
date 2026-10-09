#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# The agent next to the server (sard-self, docs/operations/self-agent.md) on a real
# docker compose stack of deploy/docker-compose.yml: F5's checks 1, 2 and 4, and with
# UPGRADE_FROM also check 5 (docs/specs/server/self-agent.feature, @e2e/@upgrade).
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
#
# Usage: SARD_IMAGE=sard-server SARD_AGENT_IMAGE=sard-agent SARD_VERSION=<tag> \
#          [UPGRADE_FROM=ghcr.io/artur-abalov/sard-server:0.1.0-beta.1] scripts/test-self-agent.sh <workdir>
# UPGRADE_FROM_AGENT is the agent image of that version (default: UPGRADE_FROM with
# sard-server replaced by sard-agent); UPGRADE_COMPOSE is its compose file (default:
# deploy/docker-compose.yml of the git tag v<its tag>, `git show`).
# The stack uses the fixed project name "sard": it removes that project's volumes. A pass
# takes the stack down; a failure leaves it up for a look (compose.log in <workdir>).
set -euo pipefail

[ $# -eq 1 ] || { echo "usage: $0 <workdir>" >&2; exit 2; }
root="$(cd "$(dirname "$0")/.." && pwd)"
work="$1"
mkdir -p "$work" && cd "$work"
cp "$root/deploy/docker-compose.yml" docker-compose.yml
# The administrator password is set in the wizard and exists only in this run.
admin="$(openssl rand -hex 16)"
db_password="$(openssl rand -hex 24)"
cat >.env <<EOF
SARD_VERSION=${SARD_VERSION:?}
SARD_IMAGE=${SARD_IMAGE:?}
SARD_AGENT_IMAGE=${SARD_AGENT_IMAGE:?}
SARD_DB_PASSWORD=$db_password
EOF
chmod 600 .env
# shellcheck source=lib/setup-wizard.sh
source "$root/scripts/lib/setup-wizard.sh"

fail() { echo "FAIL: $*" >&2; docker compose logs --no-color >"$work/compose.log" 2>&1 || true; exit 1; }
pass() { echo "ok: $*"; }
api="http://127.0.0.1:8080/api/v1"
cookies="$work/cookies"
export SARD_WIZARD_COOKIES="$cookies"
server_log() { docker compose logs --no-color server 2>&1; }
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
pw="$(channel 'cat /var/lib/sard/self/db-password')"
docker compose exec -T -e PGPASSWORD="$pw" postgres psql -h 127.0.0.1 -U sard_self -d sard -qAtc 'select count(*) from agents' >/dev/null \
  || fail "1: sard_self does not log in with the channel's password"
pass "1: sard_self logs in with the password from the channel"

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
for name in $(docker compose ps --services); do
  docker compose logs --no-color "$name" 2>&1 | grep -F -q -e "${captured:-no-token}" -e "$pw" && fail "4: a token or the password in the logs of $name"
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
[ "$(curl -s -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' -d "{\"password\":\"$old_admin\"}" "$api/session")" = 401 ] \
  || fail "6: the old password still signs in"
[ "$(get /agents | python3 -c 'import json,sys; print(" ".join(sorted(a["id"] for a in json.load(sys.stdin)["items"])))')" = "$agents_before_reset" ] \
  || fail "6: agents changed by the recovery"
pass "6: admin-reset + restart + wizard: the new password signs in, the old one is refused (401), agents kept"
docker compose down -v --remove-orphans >/dev/null 2>&1 # it holds ports 8080 and 9090 of the host
echo "PASSED"
