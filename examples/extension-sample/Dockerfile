# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# The QA stand of the extension sample (docs/qa/server-core-extensions.md): the jar that
# `./gradlew -p examples/extension-sample bootJar` built, in the runtime image of the server.
# Test and documentation only; the released image is deploy/server/Dockerfile.
FROM eclipse-temurin:25-jre@sha256:dbb43d81099eec91e0800226ed2296d03146ee7857165b95112372ad0d67e87f
RUN useradd --system --uid 10001 --no-create-home sard \
 && install -d -o sard -g sard -m 0700 /var/lib/sard/pki /var/lib/sard/self
COPY build/libs/sard-server-sample.jar /app/sard-server.jar
USER sard
VOLUME ["/var/lib/sard/pki"]
EXPOSE 8080 9090
HEALTHCHECK --interval=10s --timeout=3s --start-period=60s --retries=3 \
  CMD ["bash", "-c", "exec 3<>/dev/tcp/127.0.0.1/${SARD_HTTP_PORT:-8080} && printf 'GET /actuator/health HTTP/1.0\\r\\n\\r\\n' >&3 && grep -q '\"status\":\"UP\"' <&3"]
ENTRYPOINT ["java", "-jar", "/app/sard-server.jar"]
