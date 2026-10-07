# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# A clean RPM host (Rocky Linux 9, AlmaLinux 9) with systemd as PID 1,
# for scripts/test-agent-install.sh. The tools are what a minimal server
# install has; SELinux cannot be enforcing in a container, the host kernel
# decides (docs/adr/0047-release-versions.md).
ARG DISTRO=rockylinux/rockylinux:9
FROM ${DISTRO}
RUN dnf install -y --setopt=install_weak_deps=False \
      systemd sudo /usr/bin/curl findutils procps-ng shadow-utils && \
    dnf clean all
STOPSIGNAL SIGRTMIN+3
CMD ["/usr/sbin/init"]
