# Сессия 2026-09-28: T2a — каркас e2e-тестов

Ветка `claude/e2e-tests-harness-3v08yp` (назначена средой; в задании — `feat/t2a-e2e-harness`) от `main` @ `47dab77`.

## Фаза 1 — исследование

Два субагента только на чтение (сборка/CI/e2e; регистрация/TLS). Отдельно проверено руками: guard-скрипты, BouncyCastle, тенант по умолчанию, Docker.

### Расхождения с заданием (проверено)

- **S4a и S5a уже в `main`** (`47dab77`, `04f96cf`): `AgentGrpcService.kt:26-60` реализует Register и Connect; UNIMPLEMENTED остался только `RenewCertificate`.
- **`SARD_ADMIN_PASSWORD` в репозитории нет** (grep по всем файлам пуст); Spring Security не подключён, `/api/v1/status` публичный (`StatusController.kt:30-45`).
- **`SARD_AGENT_ENDPOINT` не задаёт SAN.** SAN берётся из `SARD_PKI_SERVER_NAMES` (`application.yaml:48`); `SARD_AGENT_ENDPOINT` только строит команду enroll, и сервер не стартует, если её хост не покрыт server-names (`AgentEndpoint.kt:77-107`).
- **Guard блокирует `@Disabled`/`@Ignore` и `t.Skip`** (`scripts/claude/guard-edit.sh:29-30`) — «заготовки, помеченные как пропущенные» в прямом виде запрещены.

### Находки — сборка

- Образ сервера: `deploy/server/Dockerfile`, двухстадийный, jar собирается Gradle внутри Docker; порты 8080 (REST, Actuator) и 9090 (gRPC). CA в `SARD_PKI_DIR` (`/var/lib/sard/pki`), `ca/ca.crt` + `ca/ca.key`, создаётся при первом старте.
- Образа агента нет. `make package` → `dist/sard-agent_<ver>_linux_<arch>.tar.gz` (статический агент + restic из `.bin/restic/<ver>/linux_<arch>/`, ADR 0018); `fetch-restic.sh` всегда качает с github.com, проверяет SHA-256 по `agent/internal/restic/restic-version` (0.19.1).
- `test/e2e` — только `README.md` (план полного цикла), ни в `go.work`, ни в `settings.gradle.kts`.
- Testcontainers-java 2.0.5 уже в `docs/dependencies.md:55-56` (через BOM Spring Boot 4.1.1); testcontainers-go нет.
- CI (`.github/workflows/ci.yml`): задачи licenses/proto/go/server/web/image; e2e нет; job `image` уже собирает образ сервера.
- `gate.sh`: `ALL_MODULES=(proto gen sdk tools agent cli server web)`; новый модуль придётся зарегистрировать.
- Docker: в контейнере сессии демона не было, `dockerd` запущен вручную — работает, образы тянутся через прокси.

### Находки — регистрация

- Токен `sard_<43 base64url>.<64 hex>`; отпечаток — SHA-256 DER SPKI корня; в БД `enrollment_tokens.token_hash = SHA-256(32 сырых байт)`, `label TEXT NOT NULL`, `tenant_id` (open core: `00000000-0000-0000-0000-000000000001`, `TenantResolver.kt:18`). Тестовый вектор — `docs/specs/enrollment-token.md:66-73`.
- Сервис `EnrollmentTokens.create(tenantId, ttl, label)` — Spring-бин, доступен только внутри JVM сервера; REST `/api/v1/enrollment-tokens` — 501 (`UnimplementedApi.kt:32-45`).
- Enroll: `EnrollmentService` на том же порту 9090, `client-auth: optional`; интерцептор S3 пропускает Enroll и Health (`AgentAuthConfiguration.kt:20-21`), остальное — UNAUTHENTICATED с ErrorInfo. CSR — только ECDSA P-256/P-384.
- У агента нет `enroll` и нет проверки отпечатка (A2 не в `main`); версия печатается `sard-agent --version` → `sard-agent <ver>`.

Вопросы и предложение — в ответе на контрольной точке 1.
