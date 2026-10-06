#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Package post-install, shared by deb ("configure [<old version>]") and rpm
# (1 = install, 2 = upgrade):
#
# - the unprivileged user the systemd unit runs as;
# - restic's cache directory (restic.cache_dir default), which "sard-agent repo
#   init" needs before the service has ever started (docs/adr/0030-repo-init-lock.md),
#   owner and mode reset on every install;
# - on the first install only: /etc/sard readable by the group sard-agent, and
#   the directories "sard-agent enroll" (tls/) and "repo init
#   --generate-password" (secrets/) write into as the service user. An upgrade
#   never touches them: they hold the agent's keys.
#
# The service is not enabled: it needs /etc/sard/agent.yaml and an enrolled
# identity first. An upgrade restarts it only if it is running. The first
# install prints the next step (U1b); an upgrade says nothing about enrollment.
set -e

first_install=no
case "$1" in
  configure) [ -n "$2" ] || first_install=yes ;;
  1) first_install=yes ;;
esac

if ! getent passwd sard-agent >/dev/null; then
  useradd --system --no-create-home --home-dir /var/lib/sard-agent \
    --shell /usr/sbin/nologin --user-group sard-agent
fi
group="$(id -gn sard-agent)"

mkdir -p /var/cache/sard/restic
chown sard-agent:"$(id -gn sard-agent)" /var/cache/sard/restic
chmod 0700 /var/cache/sard/restic

if [ "$first_install" = yes ]; then
  chown root:"$group" /etc/sard
  chmod 0750 /etc/sard
fi
for dir in /etc/sard/tls /etc/sard/secrets; do
  [ -d "$dir" ] || install -d -o sard-agent -g "$group" -m 0700 "$dir"
done

if command -v systemctl >/dev/null && [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
  [ "$first_install" = yes ] || systemctl try-restart sard-agent.service || true
fi

if [ "$first_install" = yes ]; then
  cat <<'NEXT'

sard-agent is installed and not started: it needs a configuration and an identity first.
Next, take a token in the Sard console (Tokens, Create a token) and register this host
as the service user (the console shows the full command):

  sudo -u sard-agent sard-agent enroll --server <address of the Sard server> --token <token>

The steps from the configuration to the start of the service:
https://github.com/Artur-Abalov/sard/blob/main/docs/operations/agent-install.md
NEXT
fi
