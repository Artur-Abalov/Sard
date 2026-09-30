#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Package post-install: the unprivileged user the systemd unit runs as, and
# restic's cache directory (restic.cache_dir default), which "sard-agent repo
# init" needs before the service has ever started (docs/adr/0028-repo-init-lock.md).
# The service is not enabled: it needs /etc/sard/agent.yaml first.
set -e
if ! getent passwd sard-agent >/dev/null; then
  useradd --system --no-create-home --home-dir /var/lib/sard-agent \
    --shell /usr/sbin/nologin --user-group sard-agent
fi
# Runs on install and on upgrade: contents are kept, owner and mode are reset.
# Not a package file: removing the package leaves the directory (ADR 0018).
mkdir -p /var/cache/sard/restic
chown sard-agent:sard-agent /var/cache/sard/restic
chmod 0700 /var/cache/sard/restic
if command -v systemctl >/dev/null && [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
fi
