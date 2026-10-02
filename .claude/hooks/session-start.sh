#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# SessionStart hook for Claude Code on the web: installs the toolchains the
# README lists (JDK 25, Go from go.work, Node.js 24) and warms the Go, npm and
# Gradle caches so `make test`, `make lint` and `make gate` work in the session.
# Idempotent; does nothing outside the remote environment.
set -euo pipefail

if [ "${CLAUDE_CODE_REMOTE:-}" != "true" ]; then
  exit 0
fi

cd "${CLAUDE_PROJECT_DIR:-$(git rev-parse --show-toplevel)}"

NODE_MAJOR=24
JDK_HOME=/usr/lib/jvm/java-25-openjdk-amd64

log() { echo "session-start: $*" >&2; }

# JDK 25: Gradle toolchains (jvmToolchain(25)) find it under /usr/lib/jvm.
if [ ! -x "$JDK_HOME/bin/javac" ]; then
  log "installing openjdk-25"
  export DEBIAN_FRONTEND=noninteractive
  apt-get install -y -qq openjdk-25-jdk-headless >/dev/null ||
    { apt-get update -qq >/dev/null && apt-get install -y -qq openjdk-25-jdk-headless >/dev/null; }
fi

# Node.js 24 into /opt/node-<version>, linked as /opt/node24. The image puts
# an older node first on PATH, so /opt/node24/bin is prepended here and in
# CLAUDE_ENV_FILE; the /usr/local/bin links cover shells that skip both.
if ! /opt/node24/bin/node --version 2>/dev/null | grep -q "^v$NODE_MAJOR\."; then
  log "installing node $NODE_MAJOR"
  version="$(curl -fsSL https://nodejs.org/dist/index.json |
    jq -r --arg m "v$NODE_MAJOR." '[.[] | select(.version | startswith($m))][0].version')"
  if [ ! -x "/opt/node-$version-linux-x64/bin/node" ]; then
    curl -fsSL "https://nodejs.org/dist/$version/node-$version-linux-x64.tar.xz" | tar -xJ -C /opt
  fi
  ln -sfn "/opt/node-$version-linux-x64" /opt/node24
fi
for bin in node npm npx; do
  ln -sf "/opt/node24/bin/$bin" "/usr/local/bin/$bin"
done
export PATH="/opt/node24/bin:$PATH"

# Go: the toolchain from go.work is fetched on first use (GOTOOLCHAIN=auto).
log "building pinned Go tools into .bin/"
make tools
log "downloading Go module dependencies"
for m in agent agent/plugins/sdk cli proto/gen/go; do
  (cd "$m" && go mod download)
done

log "installing web dependencies"
(cd web && npm install --no-audit --no-fund)

# Kotlin test class names contain Cyrillic; see CONTRIBUTING.md. Maven Central
# sometimes answers 429 to the shared egress, so the download is retried.
log "warming the Gradle cache"
for delay in 10 30 60 0; do
  LC_ALL=C.UTF-8 ./gradlew --no-daemon -q :server:testClasses && break
  [ "$delay" -eq 0 ] && exit 1
  log "gradle failed, retrying in ${delay}s"
  sleep "$delay"
done

if [ -n "${CLAUDE_ENV_FILE:-}" ]; then
  {
    echo "export JAVA_HOME=$JDK_HOME"
    echo "export LC_ALL=C.UTF-8"
    echo 'export PATH="/opt/node24/bin:$PATH"'
  } >>"$CLAUDE_ENV_FILE"
fi

log "done"
