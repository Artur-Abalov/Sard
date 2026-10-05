# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# A clean Debian/Ubuntu host with systemd as PID 1, for installing and
# upgrading the sard-agent deb (scripts/test-agent-install.sh). Only the
# distribution's own packages: systemd, sudo, curl.
ARG DISTRO=debian:12
FROM ${DISTRO}
RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      systemd systemd-sysv sudo curl ca-certificates && \
    apt-get clean
STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
