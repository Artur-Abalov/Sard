# Сессия 2026-09-28: S4a — Register на сервере

Ветка `claude/funny-wozniak-itvm0a` (назначена средой; в задании — `feat/s4a-register`) от `main` @ `54ecc4d`.

## Фаза 1 — исследование

Два субагента только на чтение (сервер: S3, сущности, миграции, тесты схемы; агент: A3, модель ошибок, e2e). Ключевые места перечитаны напрямую: ADR 0013:74-110, `00XX-draft-grpc-error-model.md:1-40`, `AgentAuthIntegrationTest.kt:238-245`, `ArchitectureTest.kt:62-67`.

### Находки — сервер

- `AgentGrpcService.kt:13-14` — пустой `AgentServiceCoroutineImplBase`, Register не переопределён (UNIMPLEMENTED).
- Принципал: `AgentPrincipal(agentId, tenantId, serial)`, ключ `AgentPrincipal.KEY` (`AgentAuthentication.kt:26-35`). `AgentSessions.inTenant(work)` — блокирующий, одна сессия и одна транзакция на вызов (`AgentSessions.kt:19-22` → `TenantSessions.kt:47-48`).
- Тенантность — Hibernate `@TenantId` + резолвер, не `@Filter` и не RLS (ADR 0013:183-184). В обработчиках агента Spring Data не используется (ADR 0013:54).
- `Agent.kt:16-37`: все поля `val`, `@Version` нет. Enrollment создаёт агента с `agentVersion = null` (`enrollment/Enrollment.kt:114-118`).
- Диспетчер внедряется: образец — `@Qualifier("enrollmentDispatcher")` (`EnrollmentGrpcService.kt:27-31`).
- Ошибки: `ERROR_DOMAIN = "sard.dev"` (`AgentAuthStatus.kt:13`); общего помощника нет, `EnrollmentStatus.kt:18` дублирует домен.
- Настроек heartbeat нет нигде; `@ConfigurationProperties` — только `sard.pki` и `sard.agent` (`endpoint`, `AgentEndpointConfiguration.kt:14-17`).
- Jackson 3 (`tools.jackson.*`) на classpath (`server/build.gradle.kts:55`) — хватит для проверки синтаксиса `config_schema`, новой зависимости не нужно.
- Ограничения тестов на миграцию:
  - `TenantSchemaRulesTest.kt:29-67` — FK `tenant_id → tenants`; при колонке `id` — UNIQUE `{id, tenant_id}`; FK между таблицами тенанта составные с `tenant_id`.
  - `TenancyIntegrationTest.kt:23,92` — все таблицы, кроме `tenants` и `flyway_schema_history`, обязаны иметь `tenant_id NOT NULL`. **Глобальная `plugin_schemas` из ADR 0013:97-99 этот тест роняет** — см. вопрос 1.
  - `ArchitectureTest.kt:62-67` — `sessions.system` вызывают только два файла. **Предупреждение о `repository_id` в разных тенантах требует межтенантного чтения** — см. вопрос 2.
- `AgentAuthIntegrationTest.kt:238-245` утверждает UNIMPLEMENTED для **каждого** метода AgentService живым агентом. После Register тест неверен для Register — см. вопрос 6.

### Находки — агент (A3)

- Register вызывается перед каждым стримом: `agent/internal/transport/transport.go:262`; запрос строит `agent/internal/app/app.go:57-79`.
- `protocol_version` — константа `1` (`app.go:17`). Версия плагинов = версия агента; actions = BACKUP, RESTORE, VERIFY (`app.go:21-25,68-77`).
- `repository_id` — из `restic ... ID`, ошибка игнорируется, id тогда пустой (`main.go:98-107`, `app.go:82,86`). `backend` — префикс URL restic или `local` (`config.go:170-180`), т. е. не только `s3|sftp|local`.
- `secret_names`/`script_names` — отсортированные ключи YAML-карт, **без проверки формата** (`config.go:183-198`). Ограничений на число плагинов/репозиториев/имён у агента нет.
- `config_schema` — встроенный `schema.json`, ~0,7 КБ у postgresql (оценка по числу строк, не измерено).
- Реакция на коды (`transport.go:273-284`, `94-96`, `164-192`): FAILED_PRECONDITION, UNAUTHENTICATED, PERMISSION_DENIED — останов без повторов; **всё остальное, включая INVALID_ARGUMENT, — повтор с экспоненциальной задержкой до 1 мин** (`transport.go:67-71,204-210`). `ErrorInfo` агент не читает.
- Это расходится с черновиком модели ошибок: «Агент повторяет только `UNAVAILABLE`» (`00XX-draft-grpc-error-model.md`, раздел «Повтор»). Для S4a: отклонённый целиком Register → агент бесконечно повторяет раз в ≤1 мин.
- `heartbeat_interval` ≤ 0 или отсутствует → 30 с (`transport.go:63-64,286-291`).
- e2e: в `test/e2e/` только README. У Go-агента нет Enroll-клиента — сертификат читается из файлов (`config.go:64-68`). Go 1.27.1 в среде есть; доступность Docker-демона не проверена.

### Предлагаемый дизайн (ждёт ревью)

Миграция `V202609281400__agent_register.sql`:
- `agents`: `os TEXT`, `arch TEXT`, `protocol_version INTEGER CHECK (> 0)`, `secret_names TEXT[] NOT NULL DEFAULT '{}'`, `script_names TEXT[] NOT NULL DEFAULT '{}'`, `last_register_at TIMESTAMPTZ` (NULL — ещё не регистрировался; os/arch/protocol_version NULL по той же причине).
- `agent_plugins (tenant_id, agent_id, name, version, config_schema JSONB, actions TEXT[])`, PK `(agent_id, name)`, FK `tenant_id → tenants`, FK `(tenant_id, agent_id) → agents (tenant_id, id)` без CASCADE (ADR 0013, правило 3), CHECK формата имени и `actions <@ ARRAY['backup','restore','verify','run']`.
- `agent_repositories (tenant_id, agent_id, name, backend, repository_id NULL, crypto_provider NULL)`, PK `(agent_id, name)`, те же FK, `INDEX (tenant_id, repository_id)`, CHECK `repository_id ~ '^[0-9a-f]{64}$'`.
- `backend` — CHECK на формат (`^[a-z][a-z0-9]{0,15}$`), не на перечисление: агент шлёт любой префикс restic.

Транзакция Register (одна `AgentSessions.inTenant`):
1. Совместимость и ограничения проверяются **до** транзакции — снимок не трогается.
2. `SELECT … FROM agents WHERE id = :principal FOR UPDATE` (`LockModeType.PESSIMISTIC_WRITE`); второй Register того же агента ждёт на этой строке.
3. Обновить колонки агента, `DELETE` плагинов и репозиториев агента, `INSERT` новых.
4. COMMIT. В READ COMMITTED каждый оператор берёт новый снимок, поэтому второй после получения блокировки удаляет уже зафиксированные строки первого — в БД остаётся ровно последний снимок целиком.
5. После фиксации — поиск `repository_id` в других тенантах и WARN (без значений из запроса, только имя репозитория, id и число тенантов).

Детерминированная гонка в тесте без крючков в продакшн-коде: тест держит `SELECT … FOR UPDATE` на строке агента в отдельном JDBC-соединении, запускает два Register, ждёт по `pg_stat_activity` (`wait_event_type = 'Lock'`), что оба заблокированы, отпускает; проверка — в БД ровно один из двух снимков целиком.

Протокол: `object ProtocolVersions { val SUPPORTED = 1..1 }`; вне диапазона — FAILED_PRECONDITION, `ErrorInfo(reason=PROTOCOL_UNSUPPORTED, metadata={min_supported: "1", max_supported: "1"})`.

Настройка: `sard.agent.heartbeat-interval: 30s` (`Duration`) в `AgentEndpointProperties` — S5a берёт её же.

Предлагаемые ограничения (все — INVALID_ARGUMENT, весь Register отклоняется):

| Что | Предел | reason |
|---|---|---|
| плагинов | 64 | `SNAPSHOT_TOO_LARGE` |
| репозиториев | 256 | `SNAPSHOT_TOO_LARGE` |
| имён секретов / скриптов | 1024 / 1024 | `SNAPSHOT_TOO_LARGE` |
| `config_schema` | 64 КиБ UTF-8, синтаксически JSON | `SNAPSHOT_TOO_LARGE` / `CONFIG_SCHEMA_INVALID` |
| имя плагина / репозитория / секрета / скрипта | `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$` | `NAME_INVALID` |
| повтор имени в одном наборе | — | `NAME_DUPLICATE` |
| `agent_version`, `os`, `arch`, `version` плагина | ≤ 64, печатные ASCII | `FIELD_INVALID` |
| `actions` | без UNSPECIFIED, без повторов | `FIELD_INVALID` |
| `repository_id` | пусто или 64 hex | `FIELD_INVALID` |

В `ErrorInfo.metadata` — `field` (например `plugins[2].name`) и `limit`, без значения из запроса.

### Открытые вопросы
Перечислены в ответе владельцу; ответы — в начале фазы 2.
