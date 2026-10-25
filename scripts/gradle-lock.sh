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

# An empty Gradle home: with a warm cache Gradle leaves out of the metadata what
# a clean build downloads (the .module and .pom files of the versions that
# conflict resolution considers and drops), and CI builds clean.
home="${GRADLE_LOCK_HOME:-$(mktemp -d)}"
# Only the Gradle distribution is taken from the usual home, if it is there
# (the wrapper checked its SHA-256 when it downloaded it).
dists="${GRADLE_USER_HOME:-$HOME/.gradle}/wrapper/dists"
if [ -d "$dists" ] && [ ! -e "$home/wrapper/dists" ]; then
  mkdir -p "$home/wrapper" && ln -s "$dists" "$home/wrapper/dists"
fi
rm -f gradle/verification-metadata.xml
GRADLE_USER_HOME="$home" ./gradlew --no-configuration-cache -Pmutflow.enabled=true \
  --write-locks --write-verification-metadata sha256 \
  resolveAndLockAll :server:spotlessCheck :server:detekt
echo "gradle-lock: lock files and gradle/verification-metadata.xml written"
