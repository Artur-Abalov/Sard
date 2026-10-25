# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# The "machine" of the recovery drill (F7, docs/specs/server/server-recovery.feature, Р39): an Ubuntu
# VM with its own Docker daemon, its own volumes and ports, started privileged. It has what the page
# docs/operator/12-server-recovery.md assumes of a new VM and nothing more: bash, coreutils, curl,
# Docker with compose. No restic, psql, jq, python or openssl; the page takes restic from the agent image.
# `make e2e-agent-images` builds it as sard-recovery-vm:e2e; at run time it has no way out of the
# drill's network.
FROM ubuntu:24.04@sha256:68ee22004a9109b44973313b02ff7b1de250f1de0f5ecb628d73f579aaff33ac
RUN apt-get update \
 && apt-get install -y --no-install-recommends docker.io docker-compose-v2 iptables curl ca-certificates \
 && dpkg --remove --force-depends openssl \
 && apt-get clean && rm -rf /var/lib/apt/lists/*
# The daemon's images and volumes: a volume of the container, so overlay2 has a real file system under it,
# and removing the container removes them with it (the machine is gone with its disks).
VOLUME /var/lib/docker
ENTRYPOINT ["dockerd", "--host=unix:///var/run/docker.sock"]
