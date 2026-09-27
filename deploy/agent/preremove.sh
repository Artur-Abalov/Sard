#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Package pre-remove: stop the service. The user, /etc/sard and the
# state and cache directories are left in place.
set -e
if command -v systemctl >/dev/null && [ -d /run/systemd/system ]; then
  systemctl disable --now sard-agent.service || true
fi
