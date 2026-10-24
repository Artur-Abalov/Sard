#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Offline installation archive: the sard-server, sard-agent and PostgreSQL images of one
# release for one CPU architecture, as `docker save` output, gzip-compressed.
# Loaded on a host without registry access with
#   gunzip -c sard-<version>-images-linux-<arch>.tar.gz | docker load
# PostgreSQL is pulled by the digest pinned in deploy/postgres.env and saved under its tag alone
# (postgres:18.6-alpine): docker load gives an image no registry digest, so an offline .env names the tag
# (docs/operator/11-offline.md).
#
# Usage: scripts/offline-archive.sh <version> <arch> <outdir>
#   version  image tag without "v" (0.0.1-rc.1); arch: amd64 | arm64
# SARD_IMAGE, SARD_AGENT_IMAGE and SARD_POSTGRES_IMAGE override the image names exactly as in
# deploy/.env. SKIP_PULL=1 saves images already present locally (local test).
set -euo pipefail

[ $# -eq 3 ] || { echo "usage: $0 <version> <arch> <outdir>" >&2; exit 2; }
version="$1" arch="$2" outdir="$3"
case "$arch" in amd64 | arm64) ;; *) echo "unknown arch: $arch" >&2; exit 2 ;; esac

server="${SARD_IMAGE:-ghcr.io/artur-abalov/sard-server}:$version"
# The agent next to the server in docker-compose.yml is the -pg18 variant (F6).
agent="${SARD_AGENT_IMAGE:-ghcr.io/artur-abalov/sard-agent}:$version-pg18"
override="${SARD_POSTGRES_IMAGE:-}"
# shellcheck source=../deploy/postgres.env
. "$(dirname "${BASH_SOURCE[0]}")/../deploy/postgres.env"
postgres="${override:-$SARD_POSTGRES_IMAGE}"
postgres_tag="${postgres%@*}"

if [ "${SKIP_PULL:-0}" != 1 ]; then
  for image in "$server" "$agent" "$postgres"; do
    docker pull --quiet --platform "linux/$arch" "$image"
  done
fi
[ "$postgres_tag" = "$postgres" ] || docker tag "$postgres" "$postgres_tag"

mkdir -p "$outdir"
archive="$outdir/sard-$version-images-linux-$arch.tar.gz"
# gzip -n: no file name or timestamp in the header.
docker save --platform "linux/$arch" "$server" "$agent" "$postgres_tag" | gzip -n >"$archive"
echo "$archive"
