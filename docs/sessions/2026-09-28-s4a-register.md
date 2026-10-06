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

### Ответы владельца (контрольная точка 1)
1. `config_schema` — колонка JSONB в `agent_plugins` (таблица тенанта); глобальной `plugin_schemas` нет.
2. Предупреждение об общем `repository_id` у разных тенантов — **убрано из S4a**: межтенантного чтения в пути Register нет. Отложено как отдельная задача (отчёт/системная проверка).
3. Защита от частых повторов на сервере не нужна. Записано как дыра агента — см. «Дыра A3» ниже.
4. Шов с агентом — интеграционный тест в `server/`, агент собирается **один раз на все тесты**.
5. Формат имён `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$` — принят.
6. Тесты S3 правятся по мере реализации методов.
7. Ключи metadata `min_supported`/`max_supported` — да.
8. `hostname` из Register перезаписывается по правилу Enroll — да.
9. `sard.agent.heartbeat-interval`, 30s — да.

### Дыра A3: повтор Register при невалидном снимке
Агент повторяет `INVALID_ARGUMENT` бесконечно с задержкой до 1 мин (`agent/internal/transport/transport.go:273-284`, `IsPermanent` — `:94-96`). Сервер отклоняет снимок целиком, поэтому агент с невалидной конфигурацией будет повторять один и тот же отказ, пока его не остановят. Нельзя повторять с невалидной конфигурацией: `INVALID_ARGUMENT` на Register должен быть окончательным (как требует черновик модели ошибок: «повторяет только `UNAVAILABLE`»). Серверная защита от частых повторов не делается (решение владельца). Исправление — в агенте (A3), вне S4a.

## Фаза 2 — миграция и Register (тесты 1–6)

### Окружение сессии (не в репозитории)
- JDK 25 из apt (`openjdk-25-jdk-headless` 25.0.4.1), как в журнале каркаса. `LC_ALL=C.UTF-8` — требование `server/build.gradle.kts:34-40`.
- Docker-демон запущен вручную (`dockerd`); `postgres:18-alpine` взят с `mirror.gcr.io` и перетегирован — Docker Hub отвечал 429. Testcontainers — с `TESTCONTAINERS_RYUK_DISABLED=true`.
- Maven Central отвечал 429 — init-скрипт Gradle `~/.gradle/init.d/mirror.gradle.kts` с зеркалом `maven-central.storage-download.googleapis.com`, только в этой сессии.
- Базовый прогон до изменений: `./gradlew :server:test` — exit 0.

### Сделано
- Миграция `V202609281400__agent_register.sql`: колонки агента `os`, `arch`, `protocol_version` (CHECK > 0), `secret_names`/`script_names TEXT[] NOT NULL DEFAULT '{}'`, `last_register_at`, CHECK согласованности (после Register все поля снимка заданы); таблицы `agent_plugins` и `agent_repositories` — PK `(agent_id, name)`, FK `tenant_id → tenants`, составной FK на `agents (tenant_id, id)` без CASCADE, CHECK формата имён/backend/repository_id/crypto_provider, `actions <@ ARRAY[...]`; индекс `(tenant_id, repository_id)`.
- Домен `registration/` (без gRPC, добавлен в `ArchitectureTest.ISOLATED_PACKAGES`): `AgentSnapshot`, `ProtocolVersions.SUPPORTED = 1..1` (единственное место), `SnapshotRules` (ограничения), `RegistrationRejectedException`, `Registration` (транзакция).
- Транзакция: правила до транзакции → `session.find(Agent, id, PESSIMISTIC_WRITE)` → колонки агента → HQL `delete` плагинов и репозиториев → `persist` новых → COMMIT. Любое исключение БД — `INTERNAL_RETRYABLE` (UNAVAILABLE).
- gRPC: `AgentGrpcService.register` (принципал из Context, диспетчер `agentServiceDispatcher` внедряется), `RegisterRequests.kt` (proto → домен, `uint32` читается беззнаково), `RegistrationStatus` (одна точка перевода: exhaustive `when` без `else`, одно `ErrorInfo`, домен `sard.dev`, metadata `field`/`limit` или `min_supported`/`max_supported`, текст статуса «register rejected» без значений).
- `sard.agent.heartbeat-interval` (`SARD_AGENT_HEARTBEAT_INTERVAL`, 30s) в `AgentEndpointProperties`, положительность проверяется при старте.
- Правило hostname вынесено в `enrollment/Hostnames` — Enroll и Register используют одно.
- Логи: отказ — WARN с agent_id, причиной и metadata (имя поля и предел, без значений); сбой БД — ERROR с исключением.

Ограничения (все — `INVALID_ARGUMENT`, весь Register отклоняется; порядок проверок = порядок полей, протокол первым):

| Что | Предел | reason, metadata |
|---|---|---|
| protocol_version | 1..1 | `PROTOCOL_UNSUPPORTED` (FAILED_PRECONDITION), `min_supported`, `max_supported` |
| hostname | 1..253 символа (как Enroll) | `HOSTNAME_INVALID`, `field` |
| agent_version, os, arch, версия плагина | `^[!-~]{1,64}$` | `FIELD_INVALID`, `field` |
| плагинов / репозиториев | 64 / 256 | `SNAPSHOT_TOO_LARGE`, `field`, `limit` |
| имён секретов / скриптов | 1024 / 1024 | `SNAPSHOT_TOO_LARGE`, `field`, `limit` |
| config_schema | ≤ 65536 байт UTF-8; одно JSON-значение без хвоста; без `\u0000` (jsonb его не хранит) | `SNAPSHOT_TOO_LARGE` / `CONFIG_SCHEMA_INVALID` |
| имена (плагин, репозиторий, секрет, скрипт) | `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`, без повторов в наборе | `NAME_INVALID` / `NAME_DUPLICATE`, `field` = `plugins[2].name` и т. п. |
| actions | без UNSPECIFIED/неизвестных и повторов | `FIELD_INVALID` |
| backend | `^[a-z][a-z0-9]{0,15}$` | `FIELD_INVALID` |
| repository_id | пусто (→ NULL) или `^[0-9a-f]{64}$` | `FIELD_INVALID` |
| crypto_provider | пусто (→ NULL) или формат имени | `FIELD_INVALID` |

Общий предел сообщения — 4 МиБ gRPC по умолчанию (не менялся).

### Тесты (сначала тест, потом код)
- `registration/SnapshotRulesTest` (29, `@MutFlowTest`): каждый предел — ровно на пределе проходит, за ним отказ с причиной и metadata.
- `agents/RegistrationStatusTest` (9, `@MutFlowTest`): причина → код, литералы строк причин, замкнутое множество причин.
- `agents/RegisterRequestsTest` (2, `@MutFlowTest`): proto → домен, все значения `Action`.
- `agents/RegisterIntegrationTest` (8; Testcontainers, gRPC на случайном порту, сертификаты через Enroll, фиксированный `Clock`):
  1. первый Register сохраняет всё; повторный заменяет наборы целиком; пустой снимок опустошает наборы;
  2. гонка: тест держит `SELECT … FOR UPDATE` строки агента, запускает два Register с разными снимками, ждёт двух ожидающих по `pg_stat_activity`, отпускает — в БД ровно A или B целиком;
  3. protocol 0, 2 и `uint32` 4294967295 → FAILED_PRECONDITION, `PROTOCOL_UNSUPPORTED`, диапазон в metadata, снимок не изменился;
  4. шесть видов нарушений через gRPC → INVALID_ARGUMENT с причиной и `field`, снимок не изменился;
  5. агенты двух тенантов и сосед в том же тенанте: Register одного не меняет чужие строки; `tenant_id` строк = тенант агента;
  6. ответ: `agent_id` из сертификата, `heartbeat_interval` = 17s из `sard.agent.heartbeat-interval=17s`.
- `AgentGrpcFixtures` — хелперы Enroll + mTLS-канал для тестов AgentService (тест S3 не трогал сверх п. 6).
- Изменены существующие тесты: `AgentAuthIntegrationTest` — Register на пустое сообщение теперь отвечает `FAILED_PRECONDITION`/`PROTOCOL_UNSUPPORTED` (обработчик, а не перехватчик), остальные методы — UNIMPLEMENTED (решение 6); `SardServerIntegrationTest` — список миграций + `202609281400`; `ArchitectureTest` — `registration` в изолированных пакетах.

### Проверка, что тест гонки ловит ошибку (временная правка, откачена)
- `PESSIMISTIC_WRITE` → `NONE`: тест падает — «the Registers never waited for the agent's row lock» (транзакции ждут не на `agents`, а на FK-блокировке при `insert into agent_plugins`).
- То же при расширенном условии ожидания (`'%agent%'`): второй Register — `UNAVAILABLE: register rejected` (нарушение PK при вставке после устаревшего delete). Т. е. без блокировки снимки конфликтуют; с ней — 8/8 зелёных.

### Результаты
- `./scripts/gate.sh server fast` — `gate: PASSED (server, fast)`; покрытие 94.6% (instructions); CRAP ≤ 6, максимум нового кода 5.0 (`pluginActionOf`, `Registration.repositoryRecord`).
- `./gradlew -Pmutflow.enabled=true :server:test --rerun` — exit 0 (STRICT: выживший мутант роняет сборку). `SnapshotRulesTest` — 1015 запусков в отчёте JUnit, `RegistrationStatusTest` — 126, `RegisterRequestsTest` — 16.
- `make license-check` — 260 files OK.
- detekt нашёл 17 замечаний (длина строк, `ThrowsCount`, `TooManyFunctions`, `serialVersionUID`) — исправлены в коде, без подавлений.
- mutflow не компилировал два `private typealias Reason` в одном пакете `agents` (обычная компиляция их принимала) — в `RegistrationStatus` заменено импортом с псевдонимом.

### Открытое
- **Неопознанное падение одного теста**: в одном из прогонов шлюза (`329 tests completed, 1 failed`) — имя теста не сохранилось: отчёты перезаписал следующий запуск. Не воспроизвелось в 13 последующих прогонах (шлюз, 4 × полный `:server:test --rerun`, 8 × `RegisterIntegrationTest` + `AgentAuthIntegrationTest`). Прогон шёл сразу после mutflow-прогона одного класса. Полагаю, но не проверил: остаток состояния сборки. Не списываю на «флейк»; если повторится — сохранить `server/build/test-results` до следующего запуска.
- ~~hostname с NUL-символом~~ — исправлено, см. «Hostname без управляющих символов».
- `config_schema` хранится как jsonb: пробелы и порядок ключей не сохраняются, дубликаты ключей — последний. Для UI достаточно; тест сравнивает с `?::jsonb::text`.

### Hostname без управляющих символов (решение владельца после фазы 2)
- Проблема подтверждена тестом до исправления: hostname `"db1\u0000"` — Enroll отвечал `INTERNAL_RETRYABLE` (UNAVAILABLE), Register — `UNAVAILABLE`/`INTERNAL_RETRYABLE`: PostgreSQL не хранит NUL в TEXT, запись падала как внутренняя ошибка, которую агент повторяет.
- Спецификация `docs/specs/server/agent-enrollment.feature`: решение 8 и таблица отказов — hostname без управляющих символов (U+0000–U+001F, U+007F–U+009F); новый сценарий. Изменение поведения Enroll: такие hostname теперь `INVALID_ARGUMENT`/`HOSTNAME_INVALID`, токен остаётся активным.
- `Hostnames.isValid` — плюс `none(Char::isISOControl)`; одно правило для Enroll и Register.
- Тесты: `enrollment/HostnamesTest` (4, `@MutFlowTest`; границы U+001F/U+0020, U+007E/U+007F, U+009F/U+00A0, кириллица допустима), сценарий в `EnrollmentContractIntegrationTest` (NUL, `\n`, DEL), NUL в `SnapshotRulesTest` и `RegisterIntegrationTest`.
- `./scripts/gate.sh server fast` — PASSED, 334 теста, покрытие 94.6%. mutflow по `HostnamesTest` и `SnapshotRulesTest` — exit 0 (48 и 1160 запусков).

## Фаза 3 — шов с агентом (тест 7), ADR, журнал

PR [Artur-Abalov/Sard#15](https://github.com/Artur-Abalov/Sard/pull/15) (фазы 1–2 и hostname) слит в `main` (`f4de483`). Фаза 3 — новая ветка `claude/s4a-register-phase3` от `main`: пересоздать прежнюю ветку система прав не дала, другая ветка — решение владельца.

### Сделано
- `server/build.gradle.kts`: задача `buildTestAgent` (`go build -ldflags "-X main.version=seam-test" ./agent/cmd/sard-agent` в `build/test-agent/`), входы — исходники `agent/`, `proto/gen/go`, `go.work`; `test` от неё зависит и получает путь в `sard.test.agent-binary`. Агент собирается **один раз на сборку** для всех тестов, повторно — только при изменении исходников (решение 4). Go нужен на PATH; в CI у job `server` уже есть `setup-go`.
- `agents/AgentSeamIntegrationTest`: тест выпускает агенту сертификат через Enroll, пишет CA, сертификат, ключ и YAML-конфиг во временный каталог, запускает настоящий `sard-agent` и ждёт `last_register_at`. Проверяются hostname (как `/proc/sys/kernel/hostname`), версия `seam-test`, `linux`/GOARCH, протокол 1, секрет `pg-prod`, скрипт `pre-dump`, четыре встроенных плагина (`files`, `mysql`, `network`, `postgresql`; версия агента; `backup,restore,verify`; схема — JSON-объект; `tenant_id` агента) и репозиторий `main`/`local` с пустым id (restic намеренно отсутствует — агент объявляет репозиторий без id, `app.go:82-86`).
- Проверка, что шов настоящий (временная правка, откачена): `ProtocolVersions.SUPPORTED = 2..2` → тест падает с выводом агента:

  ```text
  sard-agent seam-test: connecting to localhost:37427
  sard-agent: register: server does not support this agent's protocol version: rpc error: code = FailedPrecondition desc = register rejected
  ```

  Агент (A3) на FAILED_PRECONDITION останавливается без повторов — как и ожидалось.
- ADR 0013: фактическая схема (`agent_plugins.config_schema JSONB` вместо `plugin_schemas`), колонки Register, как Register получает тенант и блокирует агента; в «Отвергнуто» — глобальная `plugin_schemas` по хешу; в «Отложено» — общий `repository_id` у разных тенантов.
- Черновик модели ошибок: таблица причин Register с кодами и ключами metadata, правило «metadata называет поле и предел, не значение», расхождение A3 с правилом повтора, `HOSTNAME_INVALID` с управляющими символами.

### Найдено и исправлено: гонка в очистке тестов
- Первый полный шлюз (`./scripts/gate.sh server`, с mutflow): `5509 tests completed, 1 failed` — `AgentSeamIntegrationTest`, в `@AfterTest`: `delete from agents` → `violates foreign key constraint "agent_plugins_agent_fkey"`. Отчёт сохранён.
- Причина: агент после Register получает UNIMPLEMENTED на Connect и регистрируется снова; процесс остановлен, но Register, уже начатый на сервере, коммитит плагины между `delete from agent_plugins` и `delete from agents` (отдельные автокоммит-операторы).
- Исправление (`AgentGrpcFixtures.deleteTenant`): удаление тенанта — одна транзакция, которая сначала `select … for update` строки агентов тенанта, как Register: Register в полёте либо закоммитил до блокировки (его строки удаляются), либо ждёт и затем не находит агента.
- После: 5 × (`AgentSeamIntegrationTest` + `RegisterIntegrationTest`) — exit 0; полный шлюз — `gate: PASSED (server, full)`.
- Неопознанное падение фазы 2 было до появления теста шва, поэтому эта гонка его не объясняет; пункт остаётся открытым.

### Результаты
- `./scripts/gate.sh server` (полный, как в CI) — `gate: PASSED (server, full)`: spotless, detekt, тесты, покрытие 94.6% (instructions), CRAP ≤ 6, mutflow без выживших.
- `make license-check` — OK.

### Итог S4a по definition of done
- `./gradlew :server:test` проходит; миграция применяется с чистой БД (Testcontainers, `SardServerIntegrationTest` — список миграций).
- Снимок заменяется атомарно, гонка не смешивает снимки (`RegisterIntegrationTest`, проверено и отключением блокировки).
- Несовместимый протокол и нарушения ограничений — коды и причины по модели ошибок, записаны в черновике ADR модели ошибок.
- Настоящий агент проходит Register (`AgentSeamIntegrationTest`).

### Открытое (переносится) — в реестре `docs/open-questions.md`
- OQ-001 — A3: `INVALID_ARGUMENT` на Register должен быть окончательным (дыра агента).
- OQ-002 — неопознанное падение одного теста в фазе 2 (не воспроизводится).
- OQ-003 — общий `repository_id` у разных тенантов, отдельная задача.

## Реестр открытых вопросов (решение владельца)
Открытые вопросы больше не остаются только в журналах. Заведён `docs/open-questions.md`. В него перенесено всё незакрытое из 13 журналов `docs/sessions/` и из разделов «Отложено» всех ADR — 25 пунктов, OQ-001…OQ-025. Каждый пункт сверен с кодом на `main` @ `f4de483`. 16 пунктов оказались уже закрыты; они собраны в разделе «Закрыто при сверке» с доказательствами.

При сверке найдено новое: OQ-004. Агент шлёт keepalive раз в 30 с, а в сервере настройки keepalive нет. Полагаю, что стрим разорвётся по `too_many_pings`; тестом не проверено. Передано S5a.

Правило в CLAUDE.md (п. 8) агенту изменить нельзя: `guard-edit.sh` защищает файл. Предложенный текст передан владельцу.
