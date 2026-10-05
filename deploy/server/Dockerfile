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

FROM eclipse-temurin:25-jre AS agent-packages
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

FROM eclipse-temurin:25-jdk AS build
WORKDIR /src
COPY gradlew settings.gradle.kts build.gradle.kts gradle.properties ./
COPY gradle gradle
COPY proto/sard proto/sard
COPY proto/jvm proto/jvm
COPY server server
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
    ./gradlew --no-daemon :server:bootJar -PsardVersion="${SARD_VERSION}"

FROM eclipse-temurin:25-jre
RUN useradd --system --uid 10001 --no-create-home sard \
 && install -d -o sard -g sard -m 0700 /var/lib/sard/pki
COPY --from=build /src/server/build/libs/sard-server.jar /app/sard-server.jar
COPY --from=agent-packages /out/ /usr/share/sard/agent-packages/
USER sard
# 8080: REST API and Actuator; 9090: gRPC for agents.
EXPOSE 8080 9090
ENTRYPOINT ["java", "-jar", "/app/sard-server.jar"]
