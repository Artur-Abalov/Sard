# syntax=docker/dockerfile:1
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# sard-server image. Build context: repository root.
#   docker build -f deploy/server/Dockerfile -t sard-server:dev .

FROM eclipse-temurin:25-jdk AS build
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
RUN --mount=type=cache,target=/root/.gradle \
    --mount=type=secret,id=build-ca,required=false \
    if [ -f /run/secrets/build-ca ]; then \
      keytool -importcert -noprompt -cacerts -storepass changeit -alias build-ca -file /run/secrets/build-ca; \
    fi && \
    ./gradlew --no-daemon :server:bootJar -PsardVersion="${SARD_VERSION}"

FROM eclipse-temurin:25-jre
RUN useradd --system --uid 10001 --no-create-home sard \
 && install -d -o sard -g sard -m 0700 /var/lib/sard/pki
COPY --from=build /src/server/build/libs/sard-server.jar /app/sard-server.jar
USER sard
# 8080: REST API and Actuator; 9090: gRPC for agents.
EXPOSE 8080 9090
ENTRYPOINT ["java", "-jar", "/app/sard-server.jar"]
