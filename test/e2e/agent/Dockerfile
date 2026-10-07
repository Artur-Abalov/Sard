# syntax=docker/dockerfile:1
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# sard-agent image for the end-to-end tests. Build context: the unpacked
# tarball of `make package` (release) or `make package-stand` (T3's stand,
# ADR 0036): static sard-agent + pinned, checksum-verified restic, ADR 0017/0018,
# with an empty directory "empty/" added — `make e2e-agent-images` prepares it
# from packages already built (ADR 0045). Nothing is compiled or downloaded here.

FROM gcr.io/distroless/static-debian12:nonroot
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
