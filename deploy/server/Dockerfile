# syntax=docker/dockerfile:1
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# sard-server image. Build context: repository root.
#   docker build -f deploy/server/Dockerfile -t sard-server:dev .
# Multi-arch (release.yml): docker buildx build --platform linux/amd64,linux/arm64 ...
# The jar does not depend on the CPU, so it is built once, on the builder's
# own platform; only the runtime stage exists per target platform.

FROM --platform=$BUILDPLATFORM eclipse-temurin:25-jdk AS build
WORKDIR /src
COPY gradlew settings.gradle.kts build.gradle.kts gradle.properties ./
COPY gradle gradle
COPY proto/sard proto/sard
COPY proto/jvm proto/jvm
COPY server server
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
ARG SARD_VERSION=dev
LABEL org.opencontainers.image.title="sard-server" \
      org.opencontainers.image.description="Sard backup orchestrator: control-plane server" \
      org.opencontainers.image.source="https://github.com/Artur-Abalov/Sard" \
      org.opencontainers.image.licenses="AGPL-3.0-only" \
      org.opencontainers.image.version="${SARD_VERSION}"
RUN useradd --system --uid 10001 --no-create-home sard \
 && install -d -o sard -g sard -m 0700 /var/lib/sard/pki
COPY --from=build /src/server/build/libs/sard-server.jar /app/sard-server.jar
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
