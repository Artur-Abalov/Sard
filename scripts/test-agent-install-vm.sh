#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# scripts/test-agent-install.sh on a virtual machine instead of a container,
# for what only a real kernel shows: SELinux enforcing, and no denial while
# the rpm is installed, enrolled, started and upgraded
# (docs/adr/0047-release-versions.md). The machine is Oracle Linux 9
# (test/packages/vm-image.env), booted by qemu with KVM when /dev/kvm is
# usable, else emulated (slow, minutes), configured by cloud-init: root
# reaches it by a throwaway ssh key, sard-server is this host.
#
#   OLD_DIST=dist-n NEW_DIST=dist-n1 scripts/test-agent-install-vm.sh
#
# VM_CACHE (default ~/.cache/sard-vm) keeps the downloaded image; SERVER_IMAGE
# as for test-agent-install.sh. Ports 8080, 9090 and 2222 of this host's
# loopback must be free. Needs qemu-system-x86_64, qemu-img, cloud-localds.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=../test/packages/vm-image.env
. "$ROOT/test/packages/vm-image.env"
VM_CACHE="${VM_CACHE:-$HOME/.cache/sard-vm}"
SSH_PORT=2222

die() { echo "test-agent-install-vm: FAIL: $*" >&2; exit 1; }
say() { echo "test-agent-install-vm: $*"; }

for tool in qemu-system-x86_64 qemu-img cloud-localds ssh ssh-keygen; do
  command -v "$tool" >/dev/null || die "$tool required"
done

WORK="$(mktemp -d)"
QEMU_PID=""
cleanup() {
  [ -z "$QEMU_PID" ] || kill "$QEMU_PID" 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT

# The image, once, checked against the pinned SHA-256.
mkdir -p "$VM_CACHE"
image="$VM_CACHE/$(basename "$VM_IMAGE_URL")"
if ! echo "$VM_IMAGE_SHA256  $image" | sha256sum --check --status 2>/dev/null; then
  say "downloading $VM_IMAGE_URL"
  curl -fsSL --retry 3 -o "$image.part" "$VM_IMAGE_URL"
  echo "$VM_IMAGE_SHA256  $image.part" | sha256sum --check --status ||
    die "$VM_IMAGE_URL: SHA-256 differs from test/packages/vm-image.env"
  mv "$image.part" "$image"
fi

# A throwaway key for root; sard-server is the host's address in user networking.
ssh-keygen -q -t ed25519 -N '' -f "$WORK/key"
cat >"$WORK/user-data" <<CLOUD
#cloud-config
disable_root: false
ssh_pwauth: false
users:
  - name: root
    ssh_authorized_keys:
      - $(cat "$WORK/key.pub")
bootcmd:
  - grep -q sard-server /etc/hosts || echo "10.0.2.2 sard-server" >>/etc/hosts
CLOUD
printf 'instance-id: sard-pkgtest\nlocal-hostname: agent-host\n' >"$WORK/meta-data"
cloud-localds "$WORK/seed.iso" "$WORK/user-data" "$WORK/meta-data"
# The size is the image's own: a smaller disk cuts the LVM volume short.
qemu-img create -q -f qcow2 -F qcow2 -b "$image" "$WORK/disk.qcow2"

accel=tcg
if [ -r /dev/kvm ] && [ -w /dev/kvm ]; then accel=kvm; fi
say "booting $VM_DISTRO ($accel)"
qemu-system-x86_64 -machine "accel=$accel" -cpu max -m 3072 -smp 2 -display none \
  -serial "file:$WORK/console.log" -daemonize -pidfile "$WORK/qemu.pid" \
  -drive "file=$WORK/disk.qcow2,if=virtio" -drive "file=$WORK/seed.iso,if=virtio,format=raw" \
  -netdev "user,id=net0,hostfwd=tcp:127.0.0.1:$SSH_PORT-:22" -device virtio-net-pci,netdev=net0
QEMU_PID="$(cat "$WORK/qemu.pid")"

SSH_ARGS="-i $WORK/key -p $SSH_PORT -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"
SSH_ARGS="$SSH_ARGS -o LogLevel=ERROR -o ConnectTimeout=10 root@127.0.0.1"
# Emulated, the first boot takes minutes: wait up to 20.
up=no
for _ in $(seq 240); do
  # shellcheck disable=SC2086 # SSH_ARGS is a list of ssh arguments
  if ssh -n $SSH_ARGS 'cloud-init status --wait >/dev/null 2>&1; grep -q sard-server /etc/hosts' 2>/dev/null; then
    up=yes
    break
  fi
  kill -0 "$QEMU_PID" 2>/dev/null || break
  sleep 5
done
if [ "$up" != yes ]; then
  tail -50 "$WORK/console.log" >&2 || true
  die "no ssh to the machine"
fi
say "machine up: $(ssh -n $SSH_ARGS 'cat /etc/oracle-release 2>/dev/null || cat /etc/os-release | head -1; uname -r')"

AGENT_HOST_SSH="$SSH_ARGS" EXPECT_SELINUX=enforcing "$ROOT/scripts/test-agent-install.sh" "vm/$VM_DISTRO"
