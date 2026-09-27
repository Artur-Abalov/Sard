#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Package post-install: the unprivileged user the systemd unit runs as.
# The service is not enabled: it needs /etc/sard/agent.yaml first.
set -e
if ! getent passwd sard-agent >/dev/null; then
  useradd --system --no-create-home --home-dir /var/lib/sard-agent \
    --shell /usr/sbin/nologin --user-group sard-agent
fi
if command -v systemctl >/dev/null && [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
fi
