#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Installs and upgrades the sard-agent deb on clean Debian/Ubuntu hosts, and
# the rpm on clean Rocky Linux/AlmaLinux hosts, with systemd as PID 1,
# against a real sard-server (docs/adr/0043-agent-release.md, ADR 0048):
#
#   1. install N: user, directories and their owners and modes, the unit is
#      neither enabled nor running and, left alone for 60 s, never restarts
#      (NRestarts 0, empty journal); the output names the next step: the enroll
#      command through sudo (A8a, ADR 0050) and the console for the token
#      string (U1b);
#   2. as an administrator would: agent.yaml, "sudo -u sard-agent sard-agent
#      enroll", "sudo -u sard-agent sard-agent repo init", then the service;
#      the agent comes online with version N;
#   3. upgrade to N+1 (a newer package version, ADR 0048: 0.1.0~beta.1 →
#      0.1.0~beta.2 …): configuration, keys, the repository password and the
#      directories are unchanged, the service still enabled, the output says
#      nothing about enrollment, and the same agent comes back online with
#      version N+1.
#
#   OLD_DIST=dist-n NEW_DIST=dist-n1 scripts/test-agent-install.sh debian:12 rockylinux/rockylinux:9
#
# The image name picks the package: rockylinux and almalinux get
# the rpm (rpm -U, as on a host without repositories), every other the deb.
# OLD_DIST and NEW_DIST hold the amd64 packages of two versions (scripts/package-agent.sh);
# SERVER_IMAGE (default sard-server:e2e) is the server, run with PostgreSQL.
# Needs Docker with privileged containers (systemd). HOST_BUILD_FLAGS are
# extra `docker build` flags for the host image (a proxy, for instance).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVER_IMAGE="${SERVER_IMAGE:-sard-server:e2e}"
POSTGRES_IMAGE="${POSTGRES_IMAGE:-postgres:18-alpine}"
ADMIN_PASSWORD="install-test-admin"
RUN_ID="sard-pkgtest-$$"
NET="$RUN_ID"
HOST="$RUN_ID-host"

die() { echo "test-agent-install: FAIL: $*" >&2; exit 1; }
say() { echo "test-agent-install: $*"; }

[ "$#" -gt 0 ] || die "usage: OLD_DIST=… NEW_DIST=… test-agent-install.sh <distro image>..."
manifest_field() { sed -n "s/^  \"$2\": \"\(.*\)\",\$/\1/p" "$1/manifest.json"; }
OLD_VERSION="$(manifest_field "${OLD_DIST:?OLD_DIST not set}" version)"
NEW_VERSION="$(manifest_field "${NEW_DIST:?NEW_DIST not set}" version)"
NEW_PACKAGE_VERSION="$(manifest_field "$NEW_DIST" package_version)"
[ -n "$OLD_VERSION" ] && [ -n "$NEW_PACKAGE_VERSION" ] || die "no version in the manifests of $OLD_DIST, $NEW_DIST"

# use_family <distro image>: the package, the host image and the commands of its family.
use_family() {
  case "$1" in
    *rockylinux:* | *almalinux:*)
      FAMILY=rpm
      OLD_PKG="$(ls "$OLD_DIST"/sard-agent-*.x86_64.rpm)"
      NEW_PKG="$(ls "$NEW_DIST"/sard-agent-*.x86_64.rpm)"
      HOST_DOCKERFILE=host-rpm.Dockerfile
      INSTALL='rpm -Uvh'
      INSTALLED_VERSION="rpm -q --qf '%{VERSION}' sard-agent"
      ;;
    *)
      FAMILY=deb
      OLD_PKG="$(ls "$OLD_DIST"/sard-agent_*_amd64.deb)"
      NEW_PKG="$(ls "$NEW_DIST"/sard-agent_*_amd64.deb)"
      HOST_DOCKERFILE=host.Dockerfile
      INSTALL='DEBIAN_FRONTEND=noninteractive apt-get install -y -q'
      INSTALLED_VERSION="dpkg-query -W -f '\${Version}' sard-agent"
      ;;
  esac
}

cleanup() {
  docker rm -f "$HOST" "$RUN_ID-server" "$RUN_ID-db" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
}
# KEEP=1 leaves the containers of a failed run for a look around.
trap '[ -n "${KEEP:-}" ] || cleanup' EXIT

on_host() { docker exec "$HOST" sh -c "$1"; }

# expect <what> <command> <wanted output>
expect() {
  local got
  got="$(on_host "$2" 2>&1 || true)"
  [ "$got" = "$3" ] || die "$1: got \"$got\", want \"$3\""
  say "ok: $1 = $3"
}

start_server() {
  docker network create "$NET" >/dev/null
  docker run -d --name "$RUN_ID-db" --network "$NET" --network-alias db \
    -e POSTGRES_DB=sard -e POSTGRES_USER=sard -e POSTGRES_PASSWORD=sard "$POSTGRES_IMAGE" >/dev/null
  docker run -d --name "$RUN_ID-server" --network "$NET" --network-alias sard-server \
    -e SARD_DB_URL=jdbc:postgresql://db:5432/sard -e SARD_DB_USER=sard -e SARD_DB_PASSWORD=sard \
    -e SARD_PKI_SERVER_NAMES=sard-server -e SARD_AGENT_ENDPOINT=sard-server:9090 \
    -e SARD_ADMIN_PASSWORD="$ADMIN_PASSWORD" "$SERVER_IMAGE" >/dev/null
}

start_host() {
  local distro="$1"
  local tag="sard-pkgtest:${distro//[:\/]/-}"
  docker build -q ${HOST_BUILD_FLAGS:-} --build-arg DISTRO="$distro" -t "$tag" \
    -f "$ROOT/test/packages/$HOST_DOCKERFILE" "$ROOT/test/packages" >/dev/null
  docker run -d --name "$HOST" --hostname agent-host --network "$NET" --privileged \
    --cgroupns=host --tmpfs /run --tmpfs /run/lock -v /sys/fs/cgroup:/sys/fs/cgroup:rw \
    "$tag" >/dev/null
  wait_for "systemd on $distro" 'systemctl is-system-running | grep -Eq "running|degraded"'
  # Not /tmp: Debian 13 mounts a tmpfs there at boot, over what docker cp writes.
  docker cp "$OLD_PKG" "$HOST:/root/old.$FAMILY"
  docker cp "$NEW_PKG" "$HOST:/root/new.$FAMILY"
}

# wait_for <what> <host command>: up to 120 s.
wait_for() {
  for _ in $(seq 120); do
    on_host "$2" >/dev/null 2>&1 && return 0
    sleep 1
  done
  die "timed out waiting for $1"
}

# api <method> <path> [<json body>]: the server's REST API with the admin session.
api() {
  on_host "curl -sSf -b /root/jar -c /root/jar -X $1 -H 'Content-Type: application/json' \
    ${3:+-d '$3'} http://sard-server:8080$2"
}

check_fresh_install() {
  local output
  output="$(on_host "$INSTALL /root/old.$FAMILY 2>&1")" || die "install of the old $FAMILY: $output"
  grep -q 'sudo sard-agent enroll --server' <<<"$output" || die "the install does not print the enroll command: $output"
  grep -qi 'console' <<<"$output" || die "the install does not point to the console for the token: $output"
  say "ok: the first install prints the next step"
  expect "service user" "getent passwd sard-agent | cut -d: -f6,7" "/var/lib/sard-agent:/usr/sbin/nologin"
  expect "/etc/sard" "stat -c '%U:%G %a' /etc/sard" "root:sard-agent 750"
  expect "/etc/sard/tls" "stat -c '%U:%G %a' /etc/sard/tls" "sard-agent:sard-agent 700"
  expect "/etc/sard/secrets" "stat -c '%U:%G %a' /etc/sard/secrets" "sard-agent:sard-agent 700"
  expect "restic cache" "stat -c '%U:%G %a' /var/cache/sard/restic" "sard-agent:sard-agent 700"
  expect "unit enabled" "systemctl is-enabled sard-agent" "disabled"
  expect "unit active" "systemctl is-active sard-agent" "inactive"
  expect "no agent.yaml" "test -e /etc/sard/agent.yaml && echo present || echo absent" "absent"
  # Not enrolled, so not started: nothing may restart the unit in a loop meanwhile.
  sleep 60
  expect "no restarts without enrollment" "systemctl show -p NRestarts --value sard-agent" "0"
  expect "empty journal of the unit" "journalctl -u sard-agent --no-pager -q | wc -l" "0"
}

# configure_and_enroll: what docs/operations/agent-enroll.md and repo-init.md tell an administrator.
configure_and_enroll() {
  local token
  on_host "cat >/etc/sard/agent.yaml <<'EOF'
server:
  address: sard-server:9090
tls:
  ca_file: /etc/sard/tls/ca.pem
  cert_file: /etc/sard/tls/agent.pem
  key_file: /etc/sard/tls/agent.key
repositories:
  - name: main
    url: /srv/sard-repo/main
    password_file: /etc/sard/secrets/main.pass
EOF
chown root:sard-agent /etc/sard/agent.yaml && chmod 0640 /etc/sard/agent.yaml
install -d -o sard-agent -g sard-agent -m 0700 /srv/sard-repo
mkdir -p /etc/systemd/system/sard-agent.service.d
printf '[Service]\\nReadWritePaths=/srv/sard-repo\\n' >/etc/systemd/system/sard-agent.service.d/repo.conf"
  wait_for "sard-server" "curl -sf http://sard-server:8080/api/v1/status"
  wait_for "sard-server gRPC" "bash -c '</dev/tcp/sard-server/9090'"
  api POST /api/v1/session "{\"password\":\"$ADMIN_PASSWORD\"}" >/dev/null
  token="$(api POST /api/v1/enrollment-tokens '{}' | grep -o '"token":"[^"]*"' | cut -d'"' -f4)"
  [ -n "$token" ] || die "no enrollment token from the server"
  on_host "cd / && sudo -u sard-agent sard-agent enroll --token '$token'" || {
    # What sudo's PAM stack saw: the accounts, their shadow entries, the stack.
    on_host "rpm -q sudo pam shadow-utils setup 2>/dev/null || dpkg -l sudo libpam-modules 2>/dev/null | tail -2
      ls -l /etc/passwd /etc/shadow; grep -E '^(root|sard-agent):' /etc/passwd
      grep -E '^(root|sard-agent):' /etc/shadow | cut -d: -f1,3-; cat /etc/pam.d/sudo
      journalctl --no-pager -n 15 | grep -iE 'sudo|pam' | sed 's/--token [^ ]*/--token ***/'" || true
    die "enroll as sard-agent"
  }
  on_host "cd / && sudo -u sard-agent sard-agent repo init --generate-password main" || die "repo init as sard-agent"
  expect "agent key" "stat -c '%U %a' /etc/sard/tls/agent.key" "sard-agent 600"
  expect "repository password" "stat -c '%U %a' /etc/sard/secrets/main.pass" "sard-agent 600"
  on_host "systemctl enable --now sard-agent"
  wait_online "$OLD_VERSION"
}

# wait_online <version>: the one agent is online and reports that version.
wait_online() {
  local agents
  for _ in $(seq 90); do
    agents="$(api GET /api/v1/agents || true)"
    if grep -q '"status":"online"' <<<"$agents" && grep -q "\"agentVersion\":\"$1\"" <<<"$agents"; then
      AGENT_ID="$(grep -o '"id":"[^"]*"' <<<"$agents" | head -1)"
      [ "$(grep -o '"hostname"' <<<"$agents" | wc -l)" -eq 1 ] || die "more than one agent: $agents"
      say "ok: agent $AGENT_ID online with $1"
      return 0
    fi
    sleep 2
  done
  on_host "systemctl status sard-agent --no-pager; journalctl -u sard-agent --no-pager | tail -30" || true
  die "agent not online with $1: $agents"
}

# snapshot: owner, mode and content of everything the upgrade must keep.
# agent.example.yaml is the package's own file, not the administrator's: a
# new version may ship a new one (an unedited conffile is replaced).
snapshot() {
  on_host 'find /etc/sard /srv/sard-repo /var/lib/sard-agent /var/cache/sard -xdev \
      -printf "%p %u:%g %m\n" | grep -v "^/var/cache/sard/restic/" | sort
    find /etc/sard /srv/sard-repo -type f ! -name agent.example.yaml -exec sha256sum {} + | sort'
}

check_upgrade() {
  local before after id_before
  before="$(snapshot)"
  id_before="$AGENT_ID"
  local output
  output="$(on_host "$INSTALL /root/new.$FAMILY 2>&1")" || die "upgrade to the new $FAMILY: $output"
  ! grep -qi 'enroll' <<<"$output" || die "the upgrade talks about enrollment: $output"
  say "ok: the upgrade does not print the enroll hint"
  expect "installed version" "$INSTALLED_VERSION" "$NEW_PACKAGE_VERSION"
  expect "unit enabled after upgrade" "systemctl is-enabled sard-agent" "enabled"
  wait_online "$NEW_VERSION"
  [ "$AGENT_ID" = "$id_before" ] || die "upgrade changed the agent: $id_before → $AGENT_ID"
  after="$(snapshot)"
  # The running agent may add its own state; nothing that was there may change or go.
  comm -23 <(echo "$before") <(echo "$after") | grep . && die "upgrade changed or removed the lines above"
  say "ok: configuration, keys, password, state and directories unchanged"
}

# Every host gets its own server and database: exactly one agent to look at.
for distro in "$@"; do
  use_family "$distro"
  say "=== $distro ($FAMILY): install $OLD_VERSION, enroll, upgrade to $NEW_VERSION"
  start_server
  start_host "$distro"
  check_fresh_install
  configure_and_enroll
  check_upgrade
  cleanup
done
say "all hosts passed"
