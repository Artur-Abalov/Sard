# Сессия 2026-09-27: S2a — регистрация агента, серверный механизм

Ветка `claude/youthful-ramanujan-2ws2l0` (назначена средой; в задании — `feat/s2a-enrollment`) от `main` @ `56532c8`.

## Фаза 1 — исследование и дизайн

Исследование — два субагента только на чтение (тенантность; CA/TLS/Enroll), ключевые места перечитаны напрямую.

### Находки (проверено чтением)

Тенантность:
- `extension/TenantResolver.kt:13-20` — `fun interface`, в ядре константа `DEFAULT_TENANT_ID` (`TenancyAutoConfiguration.kt:15-19`). Контекста запроса, `ThreadLocal`, gRPC-контекста нет.
- `persistence/HibernateTenantBridge.kt:15-21` — `isRoot` не переопределён; `HibernateTenantBridgeTest.kt:30-34` проверяет `isRoot == false` с комментарием «cross-tenant access is not designed yet (ADR 0013)». Механизма системного доступа нет.
- ADR 0013:54 — JDBC и нативный SQL обходят фильтр и обязаны содержать `tenant_id = ?`; :158 — глобальные `enrollment_tokens.token_hash`, `agent_certificates.serial` — единственные места поиска до тенанта; :177 — отложено: `isRoot` для системных операций, контекст — элемент корутины, не `ThreadLocal`.
- ADR 0013:72-78 — целевые колонки: `agent_certificates (serial TEXT PK, agent_id → agents, issued_at, not_after, revoked_at)`, `enrollment_tokens (id PK, token_hash BYTEA UNIQUE, expires_at, used_at, agent_id NULL → agents)`.
- `TenantSchemaRulesTest.kt:28-67` — три правила: FK `tenant_id → tenants`; `UNIQUE (tenant_id, id)` для таблиц с `id` (только `pg_constraint`, частичный индекс не засчитывается); FK между таблицами тенантов — по паре.
- Миграции: `V1__agents.sql`, `V2__tenants.sql`, нумерация последовательная; `SardServerIntegrationTest.kt:86-89` проверяет версии `["1","2"]`.
- `agents.agent_version TEXT NOT NULL` (`V1__agents.sql`), а `EnrollRequest` несёт только `enrollment_token`, `csr_der`, `hostname` (`proto/sard/agent/v1/agent.proto:46-54`).
- Бина `Clock` нет; PKI создаёт `Clock.systemUTC()` в `PkiAutoConfiguration.kt:54`. `@Transactional`/`TransactionTemplate` в основном коде нет. Генератора UUIDv7 нет (ADR 0013, правило 6).

CA и TLS:
- `pki/CertificateAuthority.kt:41-44` — `issueAgentCertificate(csrDer, AgentIdentity(tenantId, agentId)): IssuedCertificate(chainPem, serial: BigInteger, notAfter)`; `InvalidCsrException` (:74-77). Срок агента 365 дней (`Certificates.kt:30`), серийник 128 бит (`Certificates.kt:28,119`).
- `CaFingerprint` (`CertificateAuthority.kt:60-71`) — SHA-256 от DER SPKI корня, lowercase hex, 64 символа; совпадает с форматом токена из задания (ADR 0014:56).
- **Цепочка TLS сервера не содержит CA**: `FileCertificateAuthority.kt:58` — `ServerKey(..., arrayOf(certificate))`, только лист. Агент без CA не может посчитать SPKI корня по рукопожатию — нужна правка (фаза 3).
- `agents/EnrollmentGrpcService.kt:14-15` — `@GrpcService`, без зависимостей, UNIMPLEMENTED; перехватчиков нет. `SardServerIntegrationTest.kt:141-145` и `GrpcTlsIntegrationTest.kt:74-99` ожидают UNIMPLEMENTED от Enroll — будут обновлены.

Тестовый вектор токена пересчитан (`python3 hashlib/base64`): строка, отпечаток `SHA-256("sard-test-ca")` и `token_hash` совпадают с заданием.

### Предлагаемый дизайн (ждёт ревью)

Системный доступ — явные сессии Hibernate вместо глобального переключателя:
- `TenantSessions` (`persistence/`): `inTenant(tenantId) { session -> }` открывает сессию `withOptions().tenantIdentifier(tenantId)` с собственной транзакцией; `system { session -> }` — сессия с зарезервированным `SYSTEM_TENANT_ID`, только чтение.
- `HibernateTenantBridge.isRoot(id) = id == SYSTEM_TENANT_ID`; резолвер никогда не возвращает этот id, в `tenants` его нет. Системная сессия доступна только через `TenantSessions.system`, единственный вызов — поиск токена по хэшу.

Транзакция Enroll:
1. Разбор строки токена, сверка отпечатка с `ca.fingerprint()`.
2. `system`: `select id, tenantId from EnrollmentToken where tokenHash = ?` → `(tokenId, tenantId)` или «неизвестен».
3. `inTenant(tenantId)`, одна транзакция: условное `update ... set usedAt = :now where id = :id and usedAt is null and expiresAt > :now`; 0 строк → перечитать строку и вернуть «использован»/«истёк». Параллельный Enroll ждёт блокировку строки и после коммита первого получает 0 строк (READ COMMITTED перепроверяет условие).
4. Создать `Agent` (UUIDv7), подписать CSR, записать `AgentCertificate`, `token.agentId = agent.id`; коммит. Любое исключение — откат, `used_at` возвращается к NULL.
5. Блокирующая работа — в `withContext` с внедрённым диспетчером.

Схема — одна миграция: `enrollment_tokens`, `agent_certificates`, составные FK `(tenant_id, agent_id) → agents (tenant_id, id)`.

Вопросы владельцу — в ответе сессии; после ответа сюда записываются решения.
