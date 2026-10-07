# syntax=docker/dockerfile:1
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# sard-agent image for the end-to-end tests. Build context: the unpacked
# tarball of `make package` (release) or `make package-stand` (T3's stand,
# ADR 0036): static sard-agent + pinned, checksum-verified restic, ADR 0017/0018,
# with an empty directory "empty/" added — `make e2e-agent-images` prepares it
# from packages already built (ADR 0045). Nothing of Sard is compiled here.
#
# The base is a host as the package expects it (ADR 0047): the package depends
# on openssh-client (restic's sftp backend runs ssh), and the service user
# sard-agent has its home in /var/lib/sard-agent, as postinstall.sh creates
# it; ssh reads ~/.ssh from there, whatever HOME the agent gives restic.

FROM ubuntu:24.04
# Builds behind a TLS-intercepting proxy: the standard https_proxy build
# argument and the proxy CA as the optional "build-ca" secret, read by apt as
# _apt (hence 0444) over https sources.
RUN --mount=type=secret,id=build-ca,required=false,mode=0444 \
    if [ -f /run/secrets/build-ca ]; then \
      sed -i 's|http://|https://|' /etc/apt/sources.list.d/ubuntu.sources; \
      echo 'Acquire::https::CaInfo "/run/secrets/build-ca";' > /etc/apt/apt.conf.d/99build-ca; \
    fi \
 && apt-get update && apt-get install -y --no-install-recommends openssh-client \
 && apt-get clean && rm -f /etc/apt/apt.conf.d/99build-ca \
 && groupadd --system --gid 65532 sard-agent \
 && useradd --system --uid 65532 --gid 65532 --no-create-home --home-dir /var/lib/sard-agent \
      --shell /usr/sbin/nologin sard-agent
# The layout of the deb/rpm packages (ADR 0018).
COPY sard-agent restic /usr/libexec/sard/
COPY LICENSE LICENSE.restic THIRD_PARTY_LICENSES NOTICE /usr/share/doc/sard-agent/
COPY agent.example.yaml /etc/sard/
# Default state and cache directories of the agent config, writable by the agent.
COPY --chown=65532:65532 --chmod=0700 empty/ /var/lib/sard-agent/
COPY --chown=65532:65532 --chmod=0700 empty/ /var/cache/sard/restic/
USER 65532:65532
ENTRYPOINT ["/usr/libexec/sard/sard-agent"]
CMD ["--config", "/etc/sard/agent.yaml"]
