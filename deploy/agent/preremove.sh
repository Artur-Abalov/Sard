#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Package pre-remove, shared by deb ("remove", "upgrade", ...) and rpm
# (0 = remove, 1 = upgrade): stop and disable the service on removal only.
# An upgrade leaves it running; postinstall of the new version restarts it.
# The user, /etc/sard and the state and cache directories are left in place.
set -e
case "$1" in
  remove | 0) ;;
  *) exit 0 ;;
esac
if command -v systemctl >/dev/null && [ -d /run/systemd/system ]; then
  systemctl disable --now sard-agent.service || true
fi
