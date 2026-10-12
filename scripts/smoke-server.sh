#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Smoke check of a running sard-server (release.yml, after the quickstart or
# the offline installation): health is UP, the version is the expected one,
# the console is served and is of that version, the first start goes through by the
# setup code from the server log (F4a) and the administrator can sign in with the
# password the wizard set, and the agent port answers TLS with a certificate for the
# expected name, signed by the server's own CA.
#
# Usage: scripts/smoke-server.sh <env-file> <expected-version> [http-base] [grpc-host:port]
#   env-file: the deploy .env; docker-compose.yml lies next to it, and the code is read
#   from `docker compose logs server` of that directory
set -euo pipefail

# shellcheck source=lib/setup-wizard.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/setup-wizard.sh"

[ $# -ge 2 ] || { echo "usage: $0 <env-file> <version> [http-base] [grpc-host:port]" >&2; exit 2; }
env_file="$1" version="$2"
http="${3:-http://localhost:8080}"
grpc="${4:-localhost:9090}"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }

env_dir="$(cd "$(dirname "$env_file")" && pwd)"
server_log() { (cd "$env_dir" && docker compose logs --no-color server); }
# The administrator password is chosen here and exists only in this run.
password="$(openssl rand -hex 16)"

curl -fsS "$http/actuator/health" | grep -q '"status":"UP"' || fail "health is not UP"
echo "ok: health UP"

status="$(curl -fsS "$http/api/v1/status")"
echo "$status" | grep -q "\"version\":\"$version\"" || fail "version: want $version, got $status"
echo "ok: version $version"

# The console (S10): the page is HTML with the version of this build, and the script
# it loads is served.
curl -fsS -D "$work/page.headers" -o "$work/page.html" "$http/" || fail "the console page is not served"
grep -qi '^content-type: text/html' "$work/page.headers" || fail "the console page is not text/html"
grep -qF "<meta name=\"sard-version\" content=\"$version\"" "$work/page.html" \
  || fail "console version: want $version, got $(grep -o '<meta name="sard-version"[^>]*>' "$work/page.html" || echo none)"
script="$(grep -oE '/assets/[^"]+\.js' "$work/page.html" | head -1)"
[ -n "$script" ] || fail "the console page references no script under /assets/"
curl -fsS -o /dev/null "$http$script" || fail "console script $script is not served"
echo "ok: console served, version $version, script $script"

# Before the wizard there is no administrator: sign-in is refused with setup_required.
code="$(curl -sS -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' \
  --data-binary '{"password":"anything-of-12-chars"}' "$http/api/v1/session")"
[ "$code" = 409 ] || fail "sign-in before the wizard answered $code, want 409"
sard_complete_wizard "$http" "$password" server_log || fail "the first-start wizard did not complete"
echo "ok: first start by the setup code from the log"
code="$(curl -sS -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' \
  --data-binary @- "$http/api/v1/session" <<<"{\"password\":\"$password\"}")"
[ "$code" = 204 ] || fail "sign-in answered $code, want 204"
echo "ok: administrator sign-in with the password of the wizard"

# The server sends its whole chain, root included: the last certificate is
# the Sard CA, and the leaf must verify against it for the dialed name.
host="${grpc%:*}"
openssl s_client -connect "$grpc" -servername "$host" -alpn h2 -showcerts </dev/null \
  >"$work/handshake" 2>/dev/null || fail "no TLS handshake on $grpc"
awk '/-----BEGIN CERTIFICATE-----/{n++; f="'"$work"'/cert" n ".pem"} f{print > f} /-----END CERTIFICATE-----/{f=""}' "$work/handshake"
ca="$(ls "$work"/cert*.pem | sort -V | tail -1)"
openssl x509 -in "$ca" -noout -subject | grep -q 'Sard CA' || fail "last certificate on $grpc is not the Sard CA"
# An IP literal (127.0.0.1, ::1) is an IP SAN, checked with -verify_ip.
check=-verify_hostname
[[ "$host" =~ ^[0-9.]+$ || "$host" == *:* ]] && check=-verify_ip
openssl verify -CAfile "$ca" "$check" "${host//[\[\]]/}" "$work/cert1.pem" >/dev/null \
  || fail "agent port certificate does not verify for $host"
grep -q '^ALPN protocol: h2' "$work/handshake" || fail "agent port did not negotiate h2"
echo "ok: agent port TLS, h2, certificate for $host signed by the Sard CA"
