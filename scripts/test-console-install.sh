#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# The commands the console shows (GET /api/v1/agent-install and
# /api/v1/agents/<id>/upgrade, U1b: docs/specs/agent/agent-install.feature,
# scenarios @e2e) bring a clean host to an agent online, run exactly as the
# server answers them, in order, on hosts that have access to the Sard server
# and nothing else (an internal Docker network, no internet):
#
#   - deb on Ubuntu 24.04, the tar.gz archive on Debian 12 and rpm on Rocky
#     and AlmaLinux (format=rpm, R1, ADR 0048), amd64: every
#     step but the signature one exits 0, the agent comes online with the
#     version of the packages; configure does not overwrite an edited
#     agent.yaml; after the archive's install the layout of the deb is there;
#   - the signature step, when the release is signed (a release image), on
#     the runner's copy of the downloaded files: exit 0 and the trusted
#     comment "sard-agent <version>", non-zero once SHA256SUMS is altered;
#   - UPGRADE=1 (needs OLD_DIST, the packages of the previous version): an agent
#     installed from the old package (deb or rpm, by the host) is marked outdated; the steps of its upgrade
#     bring it online with the new version, the same agentId, and
#     /etc/sard/agent.yaml, tls and secrets unchanged.
#
#   SERVER_IMAGE=sard-server:e2e scripts/test-console-install.sh [ubuntu:24.04 debian:12 rockylinux/rockylinux:9]
#   UPGRADE=1 OLD_DIST=dist-old SERVER_IMAGE=sard-server:e2e scripts/test-console-install.sh debian:12 rockylinux/rockylinux:9
#
# SERVER_IMAGE is a server with the agent packages of the version to install
# (scripts/test-agent-install.sh builds the same setup). Needs Docker with
# privileged containers (systemd) and jq on the machine that runs it; minisign
# there only for the signature step. HOST_BUILD_FLAGS are extra `docker build`
# flags for the host image (a proxy, for instance).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVER_IMAGE="${SERVER_IMAGE:-sard-server:e2e}"
POSTGRES_IMAGE="${POSTGRES_IMAGE:-postgres:18-alpine}"
# Set in the first-start wizard, with the code from the server's log (F4a).
ADMIN_PASSWORD="console-install-admin"
RUN_ID="sard-console-$$"
NET="$RUN_ID"
HOST="$RUN_ID-host"

# The first start goes through the wizard with the code from the server's log (F4a,
# scripts/lib/setup-wizard.sh); the server is reachable from the host container only.
# shellcheck source=lib/setup-wizard.sh
source "$ROOT/scripts/lib/setup-wizard.sh"
sard_curl() { docker exec -i "$HOST" curl "$@"; }
server_log() { docker logs "$RUN_ID-server" 2>&1; }
export SARD_WIZARD_COOKIES=/root/jar
WORK="$(mktemp -d)"
# Not /tmp: Debian 13 mounts a tmpfs there at boot, over what docker cp writes.
WORKDIR=/root/install

# use_family <distro image>: the format the console is asked for and the host image of its family.
use_family() {
  case "$1" in
    *rockylinux:* | *almalinux:*)
      FAMILY=rpm
      HOST_DOCKERFILE=host-rpm.Dockerfile
      OLD_GLOB='sard-agent-*.x86_64.rpm'
      INSTALL_OLD='rpm -Uvh'
      ;;
    *)
      FAMILY=deb
      HOST_DOCKERFILE=host.Dockerfile
      OLD_GLOB='sard-agent_*_amd64.deb'
      INSTALL_OLD='DEBIAN_FRONTEND=noninteractive apt-get install -y -q'
      ;;
  esac
}

die() { echo "test-console-install: FAIL: $*" >&2; exit 1; }
say() { echo "test-console-install: $*"; }
command -v jq >/dev/null || die "jq is required"

cleanup() {
  docker rm -f "$HOST" "$RUN_ID-server" "$RUN_ID-db" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
}
trap '[ -n "${KEEP:-}" ] || { cleanup; rm -r "$WORK"; }' EXIT

on_host() { docker exec -w "$WORKDIR" "$HOST" sh -c "$1"; }

# wait_for <what> <host command>: up to 120 s.
wait_for() {
  for _ in $(seq 120); do
    on_host "$2" >/dev/null 2>&1 && return 0
    sleep 1
  done
  die "timed out waiting for $1"
}

# api <method> <path> [<json body>]: the server's REST API with the admin session, called from the host.
api() {
  local args=(-sSf -b /root/jar -c /root/jar -X "$1" -H 'Content-Type: application/json')
  [ -z "${3:-}" ] || args+=(-d "$3")
  docker exec "$HOST" curl "${args[@]}" "http://sard-server:8080$2"
}

start_server() {
  docker network create --internal "$NET" >/dev/null
  docker run -d --name "$RUN_ID-db" --network "$NET" --network-alias db \
    -e POSTGRES_DB=sard -e POSTGRES_USER=sard -e POSTGRES_PASSWORD=sard "$POSTGRES_IMAGE" >/dev/null
  docker run -d --name "$RUN_ID-server" --network "$NET" --network-alias sard-server \
    -e SARD_DB_URL=jdbc:postgresql://db:5432/sard -e SARD_DB_USER=sard -e SARD_DB_PASSWORD=sard \
    -e SARD_PKI_SERVER_NAMES=sard-server -e SARD_AGENT_ENDPOINT=sard-server:9090 \
    "$SERVER_IMAGE" >/dev/null
}

start_host() {
  local distro="$1"
  docker build -q ${HOST_BUILD_FLAGS:-} --build-arg DISTRO="$distro" -t "sard-pkgtest:${distro//:/-}" \
    -f "$ROOT/test/packages/$HOST_DOCKERFILE" "$ROOT/test/packages" >/dev/null
  docker run -d --name "$HOST" --hostname agent-host --network "$NET" --privileged \
    --cgroupns=host --tmpfs /run --tmpfs /run/lock -v /sys/fs/cgroup:/sys/fs/cgroup:rw \
    "sard-pkgtest:${distro//:/-}" >/dev/null
  # Before any on_host: it runs in WORKDIR, and docker exec fails while that is missing.
  docker exec "$HOST" mkdir -p "$WORKDIR"
  wait_for "systemd on $distro" 'systemctl is-system-running | grep -Eq "running|degraded"'
  # An internal network: the host reaches the server and nothing else.
  ! docker exec "$HOST" curl -sS -m 5 -o /dev/null https://github.com 2>/dev/null || die "the host reaches the internet"
}

sign_in() {
  wait_for "sard-server" "curl -sf http://sard-server:8080/api/v1/status"
  wait_for "sard-server gRPC" "bash -c '</dev/tcp/sard-server/9090'"
  sard_complete_wizard http://sard-server:8080 "$ADMIN_PASSWORD" server_log || die "the first-start wizard failed"
}

# steps <path with query>: the steps of the answer as TSV lines "kind<TAB>command", one per command.
steps() { api GET "$1" | jq -r '.steps[] | .kind as $k | .commands[] | [$k, .] | @tsv'; }

# run_steps <kind>...: the commands of those kinds from $STEPS, in the server's order, on the host;
# the placeholder <TOKEN> becomes the real token.
run_steps() {
  local kind command
  while IFS=$'\t' read -r kind command; do
    [[ " $* " == *" $kind "* ]] || continue
    command="${command//<TOKEN>/$TOKEN}"
    say "[$kind] ${command//$TOKEN/<token>}"
    on_host "$command" || die "step $kind failed: ${command//$TOKEN/<token>}"
  done <<<"$STEPS"
}

# kinds_of <steps>: the kinds in order, each once.
kinds_of() { cut -f1 <<<"$1" | awk '!seen[$0]++' | paste -sd' ' -; }

# edit_repository: what the operator does after configure: points the repository at a local directory.
# The server's address stays as configure set it.
edit_repository() {
  on_host "grep -q 'address: sard-server:9090' /etc/sard/agent.yaml" ||
    die "configure did not put the server's address into agent.yaml"
  docker exec -i "$HOST" sh -c 'cat >/etc/sard/agent.yaml' <<'YAML'
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
YAML
  on_host "chown root:sard-agent /etc/sard/agent.yaml && chmod 0640 /etc/sard/agent.yaml &&
    install -d -o sard-agent -g sard-agent -m 0700 /srv/sard-repo &&
    mkdir -p /etc/systemd/system/sard-agent.service.d &&
    printf '[Service]\\nReadWritePaths=/srv/sard-repo\\n' >/etc/systemd/system/sard-agent.service.d/repo.conf"
}

# wait_online <version>: the one agent is online and reports that version; sets AGENT_ID.
wait_online() {
  local agents
  for _ in $(seq 90); do
    agents="$(api GET /api/v1/agents || true)"
    if [ "$(jq -r '.items | length' <<<"$agents" 2>/dev/null || echo 0)" -eq 1 ] &&
      [ "$(jq -r '.items[0] | "\(.status) \(.agentVersion)"' <<<"$agents")" = "online $1" ]; then
      AGENT_ID="$(jq -r '.items[0].id' <<<"$agents")"
      say "ok: agent $AGENT_ID online with $1"
      return 0
    fi
    sleep 2
  done
  on_host "systemctl status sard-agent --no-pager; journalctl -u sard-agent --no-pager | tail -30" || true
  die "agent not online with $1: $agents"
}

new_token() {
  TOKEN="$(api POST /api/v1/enrollment-tokens '{}' | jq -r .token)"
  [ -n "$TOKEN" ] || die "no token"
}

# check_signature <format>: the signature step on the runner's copy of the downloaded files, if the release is signed.
check_signature() {
  local info dir="$WORK/signature" command version
  info="$(api GET "/api/v1/agent-install?format=$1")"
  [ "$(jq -r .signed <<<"$info")" = true ] || { say "unsigned release: signature step skipped"; return 0; }
  command -v minisign >/dev/null || { say "no minisign here: signature step skipped"; return 0; }
  mkdir -p "$dir"
  for f in SHA256SUMS SHA256SUMS.minisig; do docker cp "$HOST:$WORKDIR/$f" "$dir/$f"; done
  command="$(grep -P '^signature\t' <<<"$STEPS" | cut -f2)"
  version="$(jq -r .agentVersion <<<"$info")"
  (cd "$dir" && sh -c "$command" 2>&1 | grep -q "sard-agent $version") ||
    die "signature step: no trusted comment sard-agent $version"
  echo x >>"$dir/SHA256SUMS"
  ! (cd "$dir" && sh -c "$command" >/dev/null 2>&1) || die "signature step accepted an altered SHA256SUMS"
  say "ok: the signature step accepts the release and refuses an altered SHA256SUMS"
}

# check_tampered_package: the checksum step fails on a package with one changed byte and names the file.
check_tampered_package() {
  local pkg checksum out
  pkg="$(grep -P '^download\t' <<<"$STEPS" | head -1 | cut -f2 | sed 's|.*/||')"
  checksum="$(grep -P '^checksum\t' <<<"$STEPS" | cut -f2)"
  on_host "cp $pkg $pkg.good && printf x | dd of=$pkg bs=1 seek=100 conv=notrunc 2>/dev/null"
  ! out="$(on_host "$checksum" 2>&1)" || die "the checksum step accepted a changed package"
  grep -q "$pkg" <<<"$out" || die "the checksum step does not name $pkg: $out"
  on_host "mv $pkg.good $pkg"
  say "ok: the checksum step fails on a changed package and names it"
}

expect_unit_idle() {
  [ "$(on_host 'systemctl is-enabled sard-agent 2>&1 || true')" = disabled ] || die "the unit is enabled before enrollment"
  [ "$(on_host 'systemctl is-active sard-agent 2>&1 || true')" = inactive ] || die "the unit runs before enrollment"
}

check_archive_layout() {
  [ "$(on_host "stat -c '%a %U' /etc/sard/tls /etc/sard/secrets /var/cache/sard/restic | sort -u")" = "700 sard-agent" ] ||
    die "tls, secrets and the restic cache are not 0700 sard-agent"
  [ "$(on_host "stat -c '%U:%G %a' /etc/sard")" = "root:sard-agent 750" ] || die "/etc/sard is not root:sard-agent 0750"
  [ "$(on_host 'readlink /usr/bin/sard-agent')" = /usr/libexec/sard/sard-agent ] ||
    die "/usr/bin/sard-agent is not the link of the deb"
  say "ok: the archive gives the layout of the deb"
}

install_flow() {
  local distro="$1" format="$2" before after
  say "=== $distro: $format from the console's steps"
  start_server
  start_host "$distro"
  sign_in
  STEPS="$(steps "/api/v1/agent-install?format=$format")"
  say "steps: $(kinds_of "$STEPS")"
  new_token
  run_steps download checksum
  check_tampered_package
  check_signature "$format"
  run_steps install
  expect_unit_idle
  run_steps configure
  edit_repository
  before="$(on_host 'sha256sum /etc/sard/agent.yaml')"
  run_steps configure # an edited agent.yaml stays as it is
  after="$(on_host 'sha256sum /etc/sard/agent.yaml')"
  [ "$before" = "$after" ] || die "configure overwrote agent.yaml"
  run_steps enroll repo-init start
  wait_online "$(api GET "/api/v1/agent-install?format=$format" | jq -r .agentVersion)"
  if [ "$format" = tar ]; then check_archive_layout; fi
  cleanup
}

# etc_sard_sums prints the checksums of the administrator's files in /etc/sard.
# agent.example.yaml is the package's own file, not the administrator's: a new
# version may ship a new one (an unedited conffile is replaced).
etc_sard_sums() {
  on_host 'find /etc/sard -type f ! -name agent.example.yaml -exec sha256sum {} + | sort'
}

upgrade_flow() {
  local distro="$1" old_pkg old_version before after id new_version
  old_pkg="$(ls "${OLD_DIST:?OLD_DIST not set}"/$OLD_GLOB)"
  old_version="$(sed -n 's/^  "version": "\(.*\)",$/\1/p' "$OLD_DIST/manifest.json")"
  say "=== $distro: upgrade from $old_version with the steps of the agent's card"
  start_server
  start_host "$distro"
  docker cp "$old_pkg" "$HOST:$WORKDIR/old.$FAMILY"
  sign_in
  on_host "$INSTALL_OLD $WORKDIR/old.$FAMILY >/dev/null"
  STEPS="$(steps "/api/v1/agent-install?format=$FAMILY")"
  new_token
  run_steps configure
  edit_repository
  run_steps enroll repo-init start
  wait_online "$old_version"
  # Only releases (vX.Y.Z) are compared; the old package of CI is a build of the same commit, never marked.
  if [[ "$old_version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    [ "$(api GET /api/v1/agents | jq -r '.items[0].outdated')" = true ] || die "the old agent is not marked outdated"
  fi
  before="$(etc_sard_sums)"
  id="$AGENT_ID"
  STEPS="$(steps "/api/v1/agents/$id/upgrade?format=$FAMILY")"
  say "upgrade steps: $(kinds_of "$STEPS")"
  new_version="$(api GET "/api/v1/agents/$id/upgrade?format=$FAMILY" | jq -r .agentVersion)"
  run_steps download checksum upgrade
  wait_online "$new_version"
  [ "$AGENT_ID" = "$id" ] || die "the upgrade changed the agent: $id -> $AGENT_ID"
  after="$(etc_sard_sums)"
  [ "$before" = "$after" ] || die "the upgrade changed files of /etc/sard"
  [ "$(api GET /api/v1/agents | jq -r '.items[0].outdated')" = false ] || die "the upgraded agent is still marked outdated"
  say "ok: same agent, new version, configuration and keys unchanged"
  cleanup
}

[ "$#" -gt 0 ] || set -- ubuntu:24.04 debian:12
for distro in "$@"; do
  use_family "$distro"
  if [ -n "${UPGRADE:-}" ]; then
    upgrade_flow "$distro"
  elif [ "$FAMILY" = rpm ]; then
    install_flow "$distro" rpm
  elif [[ "$distro" == ubuntu* ]]; then
    install_flow "$distro" deb
  else
    install_flow "$distro" tar
  fi
done
say "all hosts passed"
