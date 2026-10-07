# syntax=docker/dockerfile:1
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# The SFTP server of the end-to-end stand (ADR 0047): OpenSSH, internal-sftp,
# public keys only. The user "sard" logs in with the keys a test copies to
# /etc/ssh/authorized_keys/sard; its repositories go below /srv/sftp/repo
# (writable by it) or /srv/sftp/ro (read-only to it). The host key is the one
# openssh-server generates on install; a test reads its public half.

FROM ubuntu:24.04
# Builds behind a TLS-intercepting proxy: as deploy/agent/Dockerfile.
RUN --mount=type=secret,id=build-ca,required=false,mode=0444 \
    if [ -f /run/secrets/build-ca ]; then \
      sed -i 's|http://|https://|' /etc/apt/sources.list.d/ubuntu.sources; \
      echo 'Acquire::https::CaInfo "/run/secrets/build-ca";' > /etc/apt/apt.conf.d/99build-ca; \
    fi \
 && apt-get update && apt-get install -y --no-install-recommends openssh-server \
 && apt-get clean && rm -f /etc/apt/apt.conf.d/99build-ca \
 && useradd --create-home --shell /usr/sbin/nologin sard \
 # sshd refuses the keys of a locked account (no password): "*" is unlocked, and no password.
 && usermod --password '*' sard \
 && mkdir -p /run/sshd /etc/ssh/authorized_keys /srv/sftp/repo /srv/sftp/ro \
 && chown sard:sard /srv/sftp/repo && chmod 0755 /srv/sftp/ro
COPY sshd_config /etc/ssh/sshd_config
EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
