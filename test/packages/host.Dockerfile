# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# A clean Debian/Ubuntu host with systemd as PID 1, for installing and
# upgrading the sard-agent deb (scripts/test-agent-install.sh). Only the
# distribution's own packages: systemd, sudo, curl and openssh-client, the
# deb's one dependency (ADR 0047): dpkg -i, as the console runs it, resolves
# nothing, and the test network has no mirrors for apt to resolve it from.
ARG DISTRO=debian:12@sha256:854dd8ab51d62866dbd8f5431d15ce77b12983a2a4a1e1fe43a2395dc7ef9f61
FROM ${DISTRO}
RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      systemd systemd-sysv sudo curl ca-certificates openssh-client && \
    apt-get clean
STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
