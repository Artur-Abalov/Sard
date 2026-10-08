# syntax=docker/dockerfile:1
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# sard-agent image: the agent next to sard-server in deploy/docker-compose.yml
# (sard-self, docs/operations/self-agent.md) and the e2e tests' agent; one image
# for both. Build context: a directory of `make package` (release) or
# `make package-stand` (T3's stand, ADR 0036) output, i.e. the
# sard-agent_<version>_linux_<arch>.tar.gz of every target architecture and
# their SHA256SUMS:
#   make package VERSION=<v> && \
#   docker buildx build --build-arg SARD_VERSION=<v> -f deploy/agent/Dockerfile -t sard-agent:dev dist
# Multi-arch (release.yml): --platform linux/amd64,linux/arm64; each platform
# takes the tarball of its own architecture. Nothing of Sard is compiled here
# (ADR 0045): static sard-agent + pinned, checksum-verified restic (ADR 0017,
# 0018), and the build fails unless the tarball matches SHA256SUMS. A release
# verifies SHA256SUMS.minisig before building (release.yml, job "image").
#
# The base is a host as the package expects it (ADR 0047): the package depends
# on openssh-client (restic's sftp backend runs ssh), and the service user
# sard-agent has its home in /var/lib/sard-agent, as postinstall.sh creates
# it; ssh reads ~/.ssh from there, whatever HOME the agent gives restic.

# The tarballs are files for every architecture: unpacked once, on the builder's platform.
FROM --platform=$BUILDPLATFORM ubuntu:24.04 AS unpack
ARG SARD_VERSION=dev
ARG TARGETARCH
COPY SHA256SUMS sard-agent_${SARD_VERSION}_linux_${TARGETARCH}.tar.gz /in/
WORKDIR /in
RUN sha256sum --check --strict --ignore-missing --quiet SHA256SUMS \
 && grep -q " sard-agent_${SARD_VERSION}_linux_${TARGETARCH}.tar.gz\$" SHA256SUMS \
 && mkdir /out \
 && tar -xzf "sard-agent_${SARD_VERSION}_linux_${TARGETARCH}.tar.gz" --strip-components=1 -C /out \
 && mkdir /empty

FROM ubuntu:24.04
ARG SARD_VERSION=dev
LABEL org.opencontainers.image.title="sard-agent" \
      org.opencontainers.image.description="Sard backup orchestrator: the agent, with restic" \
      org.opencontainers.image.source="https://github.com/Artur-Abalov/Sard" \
      org.opencontainers.image.licenses="AGPL-3.0-only" \
      org.opencontainers.image.version="${SARD_VERSION}"
# Builds behind a TLS-intercepting proxy: the standard https_proxy build
# argument and the proxy CA as the optional "build-ca" secret, read by apt as
# _apt (hence 0444) over https sources.
# Two users: sard-agent (65532), as the packages create it, is the image's
# default; sard-self (10001, the uid of sard-server's image) is the compose
# file's agent, which reads the server's CA directory (0700, owner 10001).
RUN --mount=type=secret,id=build-ca,required=false,mode=0444 \
    if [ -f /run/secrets/build-ca ]; then \
      sed -i 's|http://|https://|' /etc/apt/sources.list.d/ubuntu.sources; \
      echo 'Acquire::https::CaInfo "/run/secrets/build-ca";' > /etc/apt/apt.conf.d/99build-ca; \
    fi \
 && apt-get update && apt-get install -y --no-install-recommends openssh-client \
 && apt-get clean && rm -rf /var/lib/apt/lists/* && rm -f /etc/apt/apt.conf.d/99build-ca \
 && groupadd --system --gid 65532 sard-agent \
 && useradd --system --uid 65532 --gid 65532 --no-create-home --home-dir /var/lib/sard-agent \
      --shell /usr/sbin/nologin sard-agent \
 && groupadd --system --gid 10001 sard-self \
 && useradd --system --uid 10001 --gid 10001 --no-create-home --home-dir /var/lib/sard-self \
      --shell /usr/sbin/nologin sard-self
# The layout of the deb/rpm packages (ADR 0018).
COPY --from=unpack /out/sard-agent /out/restic /usr/lib/sard/
COPY --from=unpack /out/LICENSE /out/LICENSE.restic /out/THIRD_PARTY_LICENSES /out/NOTICE /usr/share/doc/sard-agent/
COPY --from=unpack /out/agent.example.yaml /etc/sard/
# Default state and cache directories of the agent config, writable by the agent.
COPY --from=unpack --chown=65532:65532 --chmod=0700 /empty/ /var/lib/sard-agent/
COPY --from=unpack --chown=65532:65532 --chmod=0700 /empty/ /var/cache/sard/restic/
# sard-self: its config (the sard-self-config volume starts as a copy of it) and
# every directory it writes (the sard-self-state volume), created here because
# enroll never creates directories and a fresh volume takes the owner from the image.
COPY --from=unpack --chmod=0755 /empty/ /etc/sard/self/
COPY --chmod=0644 <<EOF /etc/sard/self/agent.yaml
# /etc/sard/self/agent.yaml — the agent next to sard-server in
# deploy/docker-compose.yml (sard-self, docs/operations/self-agent.md).
#
# No repositories: the self-backup repository is added separately (D17).
# Everything it writes lives in the sard-self-state volume, mounted at
# /var/lib/sard-self; it runs as the server's uid, 10001, so that it can read
# the CA directory the compose file mounts read-only.
server:
  address: server:9090 # the compose service; "server" is among the names of its certificate
tls: # written by the agent's own enrollment
  ca_file: /var/lib/sard-self/tls/ca.pem
  cert_file: /var/lib/sard-self/tls/agent.pem
  key_file: /var/lib/sard-self/tls/agent.key
restic:
  cache_dir: /var/lib/sard-self/restic-cache
executor:
  state_dir: /var/lib/sard-self/executor
secrets: # the password of the database role sard_self, written by the server into the channel volume
  sard-db: /run/sard-self/db-password
EOF
COPY --from=unpack --chown=10001:10001 --chmod=0700 /empty/ /var/lib/sard-self/
COPY --from=unpack --chown=10001:10001 --chmod=0700 /empty/ /var/lib/sard-self/tls/
COPY --from=unpack --chown=10001:10001 --chmod=0700 /empty/ /var/lib/sard-self/executor/
COPY --from=unpack --chown=10001:10001 --chmod=0700 /empty/ /var/lib/sard-self/restic-cache/
USER 65532:65532
ENTRYPOINT ["/usr/lib/sard/sard-agent"]
CMD ["--config", "/etc/sard/agent.yaml"]
