# syntax=docker/dockerfile:1
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# sard-server image. Build context: repository root.
#   make package VERSION=<v> && \
#   docker build --build-arg SARD_VERSION=<v> -f deploy/server/Dockerfile -t sard-server:dev .
#
# The image carries the sard-agent packages of its own version and serves
# them (docs/adr/00XX-draft-agent-release.md); nothing is downloaded at run
# time. They come from `make package` (AGENT_PACKAGES, default dist/): the
# build fails unless their version is SARD_VERSION and every file matches
# SHA256SUMS. A release adds SHA256SUMS.minisig, made by release.yml.
# Multi-arch (release.yml): docker buildx build --platform linux/amd64,linux/arm64 ...
# The jar does not depend on the CPU, so it is built once, on the builder's
# own platform; only the runtime stage exists per target platform.
# The console (web/) is built here too, from the same context, in the "web" stage and
# bundled into the jar (ADR 0040); Node and the sources never reach the final image.

# The packages are files for every architecture, so this stage, like the two
# build stages, runs once on the builder's platform.
FROM --platform=$BUILDPLATFORM eclipse-temurin:25-jre AS agent-packages
ARG SARD_VERSION=dev
ARG AGENT_PACKAGES=dist
COPY ${AGENT_PACKAGES}/ /in/
WORKDIR /in
RUN [ -f manifest.json ] && [ -f SHA256SUMS ] || \
      { echo "no agent packages in ${AGENT_PACKAGES}/: run make package VERSION=${SARD_VERSION}" >&2; exit 1; } && \
    version="$(sed -n 's/^  "version": "\(.*\)",$/\1/p' manifest.json)" && \
    { [ "$version" = "${SARD_VERSION}" ] || \
      { echo "agent packages are version '$version', the server is '${SARD_VERSION}'" >&2; exit 1; }; } && \
    sha256sum --check --strict --quiet SHA256SUMS && \
    mkdir /out && \
    for f in $(awk '{print $2}' SHA256SUMS) SHA256SUMS; do cp "$f" /out/; done && \
    if [ -f SHA256SUMS.minisig ]; then cp SHA256SUMS.minisig /out/; fi

FROM --platform=$BUILDPLATFORM node:24-slim AS web
WORKDIR /src/web
# Builds behind a TLS-intercepting proxy: the standard HTTPS_PROXY build argument and the
# same optional "build-ca" secret as the "build" stage (npm reads NODE_EXTRA_CA_CERTS).
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    --mount=type=secret,id=build-ca,required=false \
    if [ -f /run/secrets/build-ca ]; then export NODE_EXTRA_CA_CERTS=/run/secrets/build-ca; fi && \
    npm ci --no-audit --no-fund
COPY web ./
# The only file outside web/ that "tsc -b" (the typecheck in npm run build) reads: the files plugin's
# config schema, which the console's tests import.
COPY agent/plugins/files/schema.json /src/agent/plugins/files/schema.json
# The version is written into index.html (<meta name="sard-version">), as into the jar below.
ARG SARD_VERSION=dev
RUN SARD_VERSION="${SARD_VERSION}" npm run build

FROM --platform=$BUILDPLATFORM eclipse-temurin:25-jdk AS build
WORKDIR /src
COPY gradlew settings.gradle.kts build.gradle.kts gradle.properties ./
COPY gradle gradle
COPY proto/sard proto/sard
COPY proto/jvm proto/jvm
COPY server server
COPY --from=web /src/web/dist console-dist
# The key the console shows next to the signature step is a resource of the server (U1b).
COPY deploy/release/sard-release.pub deploy/release/sard-release.pub
ARG SARD_VERSION=dev
# Builds behind a TLS-intercepting proxy: pass JVM proxy settings in
# JAVA_TOOL_OPTIONS and the proxy CA as the optional "build-ca" secret.
ARG JAVA_TOOL_OPTIONS=""
# The Gradle cache mount is empty on a fresh CI runner, so the wrapper
# downloads its distribution in a single request that a reset connection
# fails. Only that download is retried (--version needs nothing but the
# distribution); the build itself runs once, so real failures stay failures.
RUN --mount=type=cache,target=/root/.gradle \
    --mount=type=secret,id=build-ca,required=false \
    if [ -f /run/secrets/build-ca ]; then \
      keytool -importcert -noprompt -cacerts -storepass changeit -alias build-ca -file /run/secrets/build-ca; \
    fi && \
    for attempt in 1 2 3 4 5; do \
      ./gradlew --no-daemon --version >/dev/null && break; \
      [ "$attempt" -eq 5 ] && exit 1; \
      echo "Gradle download failed, retrying in $((attempt * 5))s" >&2; sleep $((attempt * 5)); \
    done && \
    ./gradlew --no-daemon :server:bootJar -PsardVersion="${SARD_VERSION}" -PsardConsoleDist=/src/console-dist

FROM eclipse-temurin:25-jre
ARG SARD_VERSION=dev
LABEL org.opencontainers.image.title="sard-server" \
      org.opencontainers.image.description="Sard backup orchestrator: control-plane server" \
      org.opencontainers.image.source="https://github.com/Artur-Abalov/Sard" \
      org.opencontainers.image.licenses="AGPL-3.0-only" \
      org.opencontainers.image.version="${SARD_VERSION}"
RUN useradd --system --uid 10001 --no-create-home sard \
 && install -d -o sard -g sard -m 0700 /var/lib/sard/pki
COPY --from=build /src/server/build/libs/sard-server.jar /app/sard-server.jar
COPY --from=agent-packages /out/ /usr/share/sard/agent-packages/
USER sard
# The CA key and certificate (docs/operations/pki.md). Without a mounted volume
# Docker creates an anonymous one, so the CA outlives a container re-creation
# only when an operator names the volume, as deploy/docker-compose.yml does.
VOLUME ["/var/lib/sard/pki"]
# 8080: REST API and Actuator; 9090: gRPC for agents.
EXPOSE 8080 9090
# The JRE image has bash but no curl; SARD_HTTP_PORT moves the HTTP port.
# Health details are off (application.yaml), so the body carries only the
# overall status: {"groups":[...],"status":"UP"}.
HEALTHCHECK --interval=10s --timeout=3s --start-period=60s --retries=3 \
  CMD ["bash", "-c", "exec 3<>/dev/tcp/127.0.0.1/${SARD_HTTP_PORT:-8080} && printf 'GET /actuator/health HTTP/1.0\\r\\n\\r\\n' >&3 && grep -q '\"status\":\"UP\"' <&3"]
ENTRYPOINT ["java", "-jar", "/app/sard-server.jar"]
