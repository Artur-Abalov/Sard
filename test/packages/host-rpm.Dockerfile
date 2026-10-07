# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# A clean RPM host (Rocky Linux 9, AlmaLinux 9) with systemd as PID 1,
# for scripts/test-agent-install.sh. The tools are what a minimal server
# install has; SELinux cannot be enforcing in a container, the host kernel
# decides (docs/adr/0048-release-versions.md). openssh-clients, which a
# minimal server install has, is the rpm's one dependency (ADR 0047): rpm -U,
# as the console runs it, resolves nothing from repositories.
ARG DISTRO=rockylinux/rockylinux:9
FROM ${DISTRO}
RUN dnf install -y --setopt=install_weak_deps=False \
      systemd sudo /usr/bin/curl findutils procps-ng shadow-utils openssh-clients && \
    dnf clean all && \
    printf 'Defaults !pam_acct_mgmt\n' >/etc/sudoers.d/container && chmod 0440 /etc/sudoers.d/container
# sudo (1.9.17p2-3.el9_8.3) in this container on GitHub's runners fails PAM
# account management for every user ("Authentication service cannot retrieve
# authentication info") while su and runuser pass the same stack, and the
# same sudo build passes locally (OQ-161). Only that check is turned off: the
# commands of the docs and the console still run through sudo as written.
STOPSIGNAL SIGRTMIN+3
CMD ["/usr/sbin/init"]
