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

### Решения владельца по контрольной точке 1
1. Системный доступ — вариант A (`isRoot` + явные сессии `inTenant`/`system`), проверить тестом.
2. `agent_version` снимать с `NOT NULL` нельзя: агент обязан регистрироваться с версией. В `EnrollRequest` её нет — это дефект контракта; решение (поле в proto) — вопрос контрольной точки 2.
3. `issued_at` (= `notBefore`), `serial TEXT` 32 hex с `CHECK`, `created_at` у токенов.
4. Версия миграции — метка времени, `out-of-order` не включается.
5. UUIDv7 — свой генератор, раз его требует ADR 0013 (правило 6).
6. Бин `Clock` с `@ConditionalOnMissingBean`; PKI не трогаем.
7. Без `/ship-feature`, но `make gate M=server` обязателен.
8. Истечение при `now >= expires_at`; чужой отпечаток — неверный токен; `certificate_chain_pem` = лист агента; цепочка TLS — `arrayOf(leaf, ca)`.

## Фаза 2 — миграция, формат токена, сервис токенов

### Сделано
- `V202609271200__enrollment.sql`: `enrollment_tokens` (`token_hash BYTEA UNIQUE CHECK (octet_length = 32)`, `CHECK (expires_at > created_at)`, `CHECK (agent_id IS NULL OR used_at IS NOT NULL)` — Enroll сначала захватывает токен, потом создаёт агента) и `agent_certificates` (`serial TEXT PK CHECK ~ '^[0-9a-f]{32}$'`, `issued_at`, `not_after`, `revoked_at`, индекс `(tenant_id, agent_id)`); составные FK на `agents (tenant_id, id)`.
- `enrollment/EnrollmentToken.kt`: `EnrollmentSecret` (копия байтов, `hash()`, `toString` без секрета), `EnrollmentToken.encode/parse`, `MalformedEnrollmentTokenException.Reason` — 7 типов ошибок; текст ошибки не содержит входа.
- `persistence/TenantSessions.kt`: `inTenant` (отказывает `SYSTEM_TENANT_ID`) и `system` (`SET TRANSACTION READ ONLY` + `isDefaultReadOnly`); `HibernateTenantBridge.isRoot(id) = id == SYSTEM_TENANT_ID` (нулевой UUID).
- `persistence/UuidV7.kt`, `persistence/EnrollmentTokenRecord.kt`, `enrollment/EnrollmentTokens.kt` (`create(tenant, ttl)` → `IssuedEnrollmentToken` с `reveal()`; `ownerOf(hash)` — единственный вызов `system`), `ClockAutoConfiguration`.
- ADR 0013: раздел «Явный тенант и системный доступ (S2a)» со списком вызовов `system`; «Отложено» сужено до планировщика и S3.
- `docs/specs/enrollment-token.md` — формат, типы ошибок, тестовый вектор (из фазы 3 перенесено раньше: A2a нужен формат сейчас).

### Проверено (команды запускались)
- Тесты писались первыми; до реализации не компилировались (`Unresolved reference 'EnrollmentTokenRecord'` и др.).
- `./gradlew :server:test` — 85 тестов, 0 падений (из XML-отчётов). Миграции применяются к чистой PostgreSQL 18 (Testcontainers), `ddl-auto=validate` проходит.
- Тест 1: `EnrollmentTokenTest` — вектор даёт ту же строку и `token_hash`, разбор возвращает части, 16 испорченных строк → ожидаемый `Reason`.
- Тест 6: `EnrollmentTokensIntegrationTest` — системный поиск находит токены двух тенантов; `EntityManager` резолвера и `inTenant(acme)` видят только свои токены; строка токена не попадает в строку таблицы.
- Контрольные прогоны: `isRoot = false` → падает «system lookup finds a token of any tenant»; `READ WRITE` вместо `READ ONLY` → падает «system session cannot write».
- Найдено контрольным прогоном: первая версия теста «cannot write» проходила по чужой причине — Hibernate отвергал `createNativeMutationQuery` ещё до базы. Теперь запись идёт прямо в JDBC-соединение сессии, а тест требует SQLSTATE 25006.
- `scripts/gate.sh server`: spotless, detekt, тесты — OK; покрытие 97.2% (инструкции); CRAP ≤ 6 (худший новый — `parseSecret` 4.0).
- Мутации (`-Pmutflow.enabled=true :server:test --rerun`, шаг шлюза, повторён отдельно из-за 429): exit 0, выживших нет. Запусков: `EnrollmentTokenTest` 90 (9 тестов + 81 мутант), `HibernateTenantBridgeTest` 8. `UuidV7Test` — 3, то есть мутантов в `UuidV7` mutflow не породил (полагаю: побитовые `shl`/`or`/`and` вне его набора операторов, как `?:` в ADR 0006); маски проверяют точные значения в тестах.
- `make license-check` — 171 файл OK.

### Окружение
- JDK 25 из apt, `dockerd` вручную, `postgres:18-alpine` с `mirror.gcr.io`, `TESTCONTAINERS_RYUK_DISABLED=true`; Maven Central отвечает 429 — повторы с паузой.

### Открытый вопрос к контрольной точке 2
- `agent_version` в Enroll: нужен аддитивный `string agent_version = 4;` в `EnrollRequest` — это нарушает ограничение задания «proto не менять» и затрагивает A2a.
