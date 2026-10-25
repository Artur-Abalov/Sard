#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Writes the Gradle lock files and gradle/verification-metadata.xml anew
# (SC1, ADR 00XX-draft-supply-chain): after a dependency or plugin changes,
# by hand or in a Renovate PR (.github/workflows/gradle-lock.yml). The
# metadata is written from scratch, so it holds the checksums of what the
# build uses now and nothing older; the checksums are what Maven Central and
# the Gradle Plugin Portal serve at that moment, and the diff is reviewed in
# the PR like any other change.
#
# The build runs with mutation testing on, which resolves a superset of the
# configurations (the mutated source set), and runs the tasks whose tools come
# from detached configurations (Spotless, detekt).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

rm -f gradle/verification-metadata.xml
./gradlew --no-configuration-cache -Pmutflow.enabled=true \
  --write-locks --write-verification-metadata sha256 \
  resolveAndLockAll :server:spotlessCheck :server:detekt
echo "gradle-lock: lock files and gradle/verification-metadata.xml written"
