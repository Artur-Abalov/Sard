# 0013 — Мультитенантность и схема БД сервера

- Статус: принято (основа тенантности реализована; остальная схема — целевая, таблицы появляются вместе с фичами; пересмотрено 2026-09-27 по ревью владельца)
- Дата: 2026-09-27

## Контекст
Sard управляет бэкапами многих клиентов (MSP, холдинги), но открытое ядро работает в одном контуре. Если тенантность добавить позже, придётся мигрировать каждую таблицу, каждый запрос и каждый индекс, а переход установки с бесплатной версии на enterprise превратится в перенос данных. Нужна схема, мультитенантная с первого дня, в которой ядро без enterprise-модулей (ограничение 3 конституции) прибито к одному тенанту.

## Решение

### Модель: общая схема, дискриминатор `tenant_id`
- Каждая таблица с данными тенанта имеет `tenant_id UUID NOT NULL REFERENCES tenants(id)`. Глобальные таблицы перечислены явно: сейчас `tenants` и `flyway_schema_history`.
- Фильтрацию делает Hibernate: поле `@TenantId` в каждой сущности. Hibernate проставляет его при вставке и добавляет `tenant_id = ?` к каждому запросу, включая `find` по id.
- Ядро не умеет создавать тенантов: `tenants` содержит одну строку, засеянную миграцией V2, — `DEFAULT_TENANT_ID = 00000000-0000-0000-0000-000000000001`. Константа неизменна: её смена осиротит все строки.
- Переход на enterprise — это установка стартера, а не миграция данных: существующие строки уже принадлежат тенанту по умолчанию, который становится первым тенантом.

### Шов
```kotlin
// extension/TenantResolver.kt — точка расширения, как SardExtension
fun interface TenantResolver {
    fun currentTenantId(): UUID
    companion object { val DEFAULT_TENANT_ID: UUID = ... }
}

// extension/TenancyAutoConfiguration.kt — ядро
@AutoConfiguration
class TenancyAutoConfiguration {
    @Bean @ConditionalOnMissingBean
    fun tenantResolver() = TenantResolver { DEFAULT_TENANT_ID }
}

// persistence/HibernateTenantBridge.kt — мост к Hibernate, регистрируется
// HibernatePropertiesCustomizer'ом из TenancyPersistenceConfiguration в том же файле
class HibernateTenantBridge(private val resolver: TenantResolver) : CurrentTenantIdentifierResolver<UUID> {
    override fun resolveCurrentTenantIdentifier() = resolver.currentTenantId()
    override fun validateExistingCurrentSessions() = true
}
```
- `TenantResolver` живёт в `extension/`, потому что это контракт для enterprise-стартеров; Hibernate о нём знает только через мост в `persistence/`. Остальной код Hibernate не видит.
- **Порядок автоконфигураций — часть контракта.** `@ConditionalOnMissingBean` в автоконфигурации видит только бины, объявленные раньше. Enterprise-стартер обязан объявить `@AutoConfiguration(before = [TenancyAutoConfiguration::class])`, иначе в контексте окажутся два резолвера. Проверяется `TenancyAutoConfigurationTest` через `AutoConfigurations.of(...)`, который соблюдает порядок так же, как приложение.
- Enterprise-резолвер при отсутствии тенанта в контексте **бросает исключение**, а не возвращает значение по умолчанию: ошибка закрывает доступ, а не открывает чужие данные.
- Прибитый тенант — это не защита лицензии: ядро под AGPL, свой `TenantResolver` может написать любой. Ценность enterprise — управление тенантами, SSO, RBAC, аудит, а не колонка `tenant_id`.

### Явный тенант и системный доступ (S2a)
Резолвер отвечает на вопрос «чей это запрос». Для работы, где тенант берётся из данных, а не из запроса, есть `persistence/TenantSessions` — единственный путь в базу мимо резолвера:
- `inTenant(tenantId) { session -> }` — сессия Hibernate с `tenantIdentifier(tenantId)` и своей транзакцией: фильтр и проставление `tenant_id` работают как обычно, только тенант назван явно. Так Enroll пишет агента в тенант токена. Резолвер при этом не участвует, `ThreadLocal` не нужен — вызов совместим с корутинами.
- `system { session -> }` — сессия с зарезервированным `SYSTEM_TENANT_ID` (нулевой UUID; в `tenants` его нет, резолвер его не возвращает). `HibernateTenantBridge.isRoot` истинно только для него, поэтому фильтр снят только в этой сессии. Транзакция `READ ONLY` на уровне PostgreSQL: системная сессия не пишет ничего.
- Вызовы `system` перечислены здесь; новый вызов — правка этого списка на ревью:
  1. `EnrollmentTokens.ownerOf(hash)` — токен по хэшу до того, как известен тенант.
  2. `AgentCertificateStandings.of(serial)` — сертификат агента и отзыв его агента по serial при каждом вызове gRPC (S3, ADR 0009); только чтение, возвращает тенанта, агента, `not_after` и отметки отзыва.

  Список проверяет `ArchitectureTest` (S2b) с точностью до файла: вызов `sessions.system` вне `EnrollmentTokens.kt` и `AgentCertificateStandings.kt` роняет сборку; лишний вызов внутри этих файлов ловит ревью.
- Операции администратора над токенами (`EnrollmentTokens.create`, `list`, `get`, `revoke`, S2b) идут через `inTenant` с тенантом, который вызывающий получил от `TenantResolver`. Будущий REST-слой (D2 → W1b) никогда не берёт тенант из параметра пути.
- Тенант gRPC-вызова агента (S3) — из его сертификата: перехватчик кладёт `AgentPrincipal` в gRPC `Context`, обработчики `AgentService` ходят в базу через `agents/AgentSessions.inTenant { }` = `TenantSessions.inTenant(principal.tenantId)`. `Context` доходит до обработчика-корутины и всех диспетчеров, на которые он переключается (grpc-kotlin кладёт `GrpcContextElement` в контекст обработчика), в том числе до сообщений стрима, пришедших после открытия, — проверено `AgentAuthIntegrationTest`. Вне аутентифицированного вызова `AgentSessions` бросает исключение. Spring Data-репозитории в обработчиках агента не используются: они идут через резолвер, а не через принципал.
- Register (S4a) пишет снимок через `registration/Registration`, доменный пакет без gRPC: он не может зависеть от `agents/`, поэтому `AgentGrpcService` передаёт ему `principal.tenantId` и `principal.agentId`, а тот открывает `TenantSessions.inTenant(tenantId)` — то же, что `AgentSessions.inTenant`, тенант по-прежнему только из сертификата. Транзакция начинается с блокировки строки агента (`LockModeType.PESSIMISTIC_WRITE`): второй Register того же агента ждёт и заменяет снимок целиком. Наборы заменяются удалением и вставкой, а не слиянием.
- Глобальный переключатель фильтра не вводится: всё остальное по-прежнему идёт через резолвер.

### Правила схемы (для всех таблиц)
`@TenantId` в Hibernate фильтрует запросы, а правила 1 и 2 защищают сами данные. Это два независимых слоя: ошибка в коде, связавшая шаг одного тенанта с запуском другого, упрётся в базу.

1. **`tenant_id NOT NULL REFERENCES tenants (id)`** в каждой таблице тенанта.
2. **Составные ключи на всех связях.** Каждая таблица тенанта с колонкой `id` имеет `UNIQUE (tenant_id, id)`; каждая ссылка на другую таблицу тенанта идёт по паре: `FOREIGN KEY (tenant_id, run_id) REFERENCES runs (tenant_id, id)`. Одноколоночных ссылок между таблицами тенантов нет. Уникальный индекс заодно служит индексом по `tenant_id`.
3. **История неизменна и не удаляется вместе с объектами.** `runs`, `run_steps`, `snapshots`, `restore_verifications` — доказательства для аудита, `step_logs` живут до срока хранения. То, на что история ссылается (`agents`, `sources`, `workflows`, `schedules`), удаляется мягко — `deleted_at TIMESTAMPTZ`. Ссылки из истории — без `ON DELETE CASCADE` и `SET NULL` (по умолчанию `NO ACTION`): жёсткое удаление такого объекта база отвергнет.
4. **Уникальность — внутри тенанта** и среди живых строк: `UNIQUE (tenant_id, name) WHERE deleted_at IS NULL` (частичный индекс), чтобы имя удалённого источника можно было занять снова. Цель составного FK — полный `UNIQUE (tenant_id, id)`, не частичный.
5. **Индексы начинаются с `tenant_id`**, кроме индексов системных сканов (планировщик).
6. Первичные ключи — `UUID`, генерирует приложение (UUIDv7: упорядочены по времени, дружат с B-деревом).
7. Время — только `TIMESTAMPTZ`. Перечисления — `TEXT` с `CHECK (x IN (...))`, значения в `lower_snake_case`, полные списки ниже; не `CREATE TYPE ... AS ENUM` (новое значение — обычная миграция). Без `CHECK` через год в колонке окажутся `Failed`, `failed` и `FAILED`.
8. Данные тенанта читаются через JPA. `JdbcTemplate` и нативный SQL обходят фильтр Hibernate; такой запрос обязан содержать `tenant_id = ?` явно и проходит ревью как исключение.
9. Секретов в базе нет (ADR 0008): конфиги содержат ссылки (`password_ref`), репозитории — только имена. Поэтому отправленный агенту конфиг можно хранить как есть.

Правила 1 и 2 проверяет по каталогу PostgreSQL `TenantSchemaRulesTest` (с контрольным прогоном на заведомо плохих таблицах в откатываемой транзакции); `tenant_id NOT NULL` в каждой таблице кроме глобальных и `@TenantId` в каждой сущности — `TenancyIntegrationTest`. Миграция, нарушившая правило, роняет тесты.

Мягкое удаление: в Hibernate 7.4 есть `@SoftDelete(strategy = TIMESTAMP)`, но он скрывает строку отовсюду, включая загрузку ссылки из истории (отчёт о запуске удалённого агента должен показать его имя). Выбор между ним и явным фильтром «живых» строк — в спецификации первой фичи удаления, с тестом на загрузку истории.

### Целевая схема
Реализовано сейчас: `tenants`, `agents.tenant_id`, `enrollment_tokens`, `agent_certificates`, снимок Register — колонки `agents` и `agent_plugins`, `agent_repositories` (S4a, `V202609281400__agent_register.sql`). Остальные таблицы и колонки создаёт миграция той фичи, которой они нужны; её спецификация может уточнить детали, но не правила выше. В листинге `tenant_id` и `UNIQUE (tenant_id, id)` подразумеваются у каждой таблицы тенанта, `→ x` означает составной FK `(tenant_id, x_id) → x (tenant_id, id)`.

```
tenants                       глобальная
  id PK, name UNIQUE, created_at

agents                        тенант · реализовано: id, tenant_id, hostname, agent_version, registered_at, last_seen_at
  + revoked_at (S3: отзыв агента целиком, проверяет перехватчик)
  + (S4a, реализовано) os, arch, protocol_version, secret_names TEXT[], script_names TEXT[],
    last_register_at                                  -- снимок из Register, только имена;
                                                      -- NULL до первого Register, затем все сразу (CHECK)
  + deleted_at                                        -- ещё не реализовано

agent_certificates            тенант · реализовано (S2a) — во время RenewCertificate действуют два сертификата
  serial TEXT PK (глобальный: сертификат ищется при рукопожатии; 32 hex-цифры), agent_id → agents,
  issued_at, not_after, revoked_at
  -- отзыв отдельного сертификата здесь, отзыв агента целиком — agents.revoked_at

enrollment_tokens             тенант · реализовано (S2a, S2b)
  id PK, token_hash BYTEA UNIQUE (глобальный: токен ищется до тенанта), expires_at, used_at,
  agent_id NULL → agents, created_at, revoked_at, label TEXT NOT NULL ('' — без подписи, ≤ 200)
  CHECK (used_at IS NULL OR revoked_at IS NULL)   -- использован и отозван одновременно не бывает
  -- состояние вычисляется при чтении: использован > отозван > истёк > активен

agent_plugins                 тенант, снимок из Register · реализовано (S4a)
  (agent_id, name) PK, agent_id → agents, version, config_schema JSONB, actions TEXT[]
  CHECK (actions <@ ARRAY['backup','restore','verify','run'])
  -- схема хранится в строке своего тенанта: общей таблицы схем нет, подменить чужую нечего

agent_repositories            тенант, снимок из Register (ADR 0008) · реализовано (S4a)
  (agent_id, name) PK, agent_id → agents, backend, repository_id NULL, crypto_provider NULL
  CHECK backend ~ '^[a-z][a-z0-9]{0,15}$', repository_id ~ '^[0-9a-f]{64}$'
  INDEX (tenant_id, repository_id)  -- хранители ключа; удалённые и отозванные агенты не считаются
  -- repository_id NULL — агент не смог прочитать id; crypto_provider NULL — AES restic

sources                       тенант — что бэкапим
  id PK, agent_id → agents, name, plugin, config JSONB, created_at, updated_at, deleted_at
  UNIQUE (tenant_id, name) WHERE deleted_at IS NULL

workflows                     тенант
  id PK, name, definition JSONB, created_at, updated_at, deleted_at
  UNIQUE (tenant_id, name) WHERE deleted_at IS NULL

schedules                     тенант
  id PK, workflow_id → workflows, cron, timezone, enabled, next_run_at, deleted_at,
  misfire_policy TEXT CHECK IN ('run_once', 'skip') DEFAULT 'run_once'
  INDEX (next_run_at) WHERE enabled AND deleted_at IS NULL   -- системный скан
  -- выбор: SELECT ... WHERE next_run_at <= now() FOR UPDATE SKIP LOCKED — готово к HA;
  -- пропущенные за время простоя запуски: run_once — один запуск вместо всех, skip — ни одного.
  -- По умолчанию run_once: для бэкапа поздно лучше, чем никогда.

runs                          тенант — запуск workflow · история
  id PK, workflow_id → workflows, schedule_id NULL → schedules,
  trigger CHECK IN ('schedule', 'manual', 'verification'),
  status  CHECK IN ('queued', 'running', 'succeeded', 'failed', 'cancelled'),
  definition JSONB (снимок workflow на момент запуска), queued_at, started_at, finished_at
  INDEX (tenant_id, workflow_id, queued_at DESC)

run_steps                     тенант — одна команда агенту · история
  id PK (= RunStep.command_id), run_id → runs, ordinal, agent_id → agents,
  source_id NULL → sources, plugin, repository_name, snapshot_id NULL,
  config JSONB NOT NULL        -- RunStep.config_json как отправлен: sources.config может измениться
  action CHECK IN ('backup', 'restore', 'verify', 'run'),
  status CHECK IN ('queued', 'dispatched', 'running',
                   'succeeded', 'failed', 'cancelled', 'timed_out', 'rejected', 'lost'),
  phase  NULL CHECK IN ('accepted', 'preparing', 'dumping', 'uploading', 'restoring', 'verifying'),
  bytes_processed, bytes_total, message,
  output JSONB (BackupOutput | RestoreOutput | VerifyOutput | RunOutput),
  queued_at, dispatched_at, started_at, finished_at
  CHECK ((action = 'run') = (source_id IS NULL))
  CHECK ((status = 'queued') = (dispatched_at IS NULL))
  CHECK ((status IN ('queued', 'dispatched', 'running')) = (finished_at IS NULL))
  -- dispatched: отправлен в поток; running: агент прислал ACCEPTED. На Hello шаг в dispatched/running,
  -- которого нет в running_command_ids, становится lost (не failed: агент не сообщал об ошибке).
  -- Повторно не отправляется: restore не идемпотентен; повтор решает workflow.

step_logs                     тенант — LogChunk, секционирована PARTITION BY RANGE (received_at)
  (step_id, seq, received_at) PK, step_id → run_steps, received_at, time, level, text
  -- ключ секционирования — время сервера, а не агента: часы агента могут врать,
  -- и строка не нашла бы секцию. Секции месячные, сервер создаёт их заранее;
  -- срок хранения — sard.logs.retention (по умолчанию 90 дней), истёкшие секции удаляются
  -- целиком (DROP), без DELETE. Срок глобальный: секционирование по времени не делит тенантов.

snapshots                     тенант — снимки restic, созданные Sard · история
  id PK, source_id NOT NULL → sources, step_id NOT NULL → run_steps, agent_id → agents,
  repository_name, repository_id NOT NULL, snapshot_id, total_bytes, added_bytes, created_at,
  forgotten_at                 -- restic forget удалил снимок; проверка не выдаётся за живую
  UNIQUE (tenant_id, repository_id, snapshot_id)
  UNIQUE (tenant_id, id, source_id)   -- цель FK из restore_verifications

restore_verifications         тенант — доказательство восстановимости (ADR 0008, п. 3) · история
  id PK, (snapshot_id, source_id) → snapshots (id, source_id), step_id → run_steps,
  verifier_agent_id → agents, key_holders SMALLINT NOT NULL,
  status CHECK IN ('passed', 'failed'), checks JSONB, verified_at
  INDEX (tenant_id, source_id, verified_at DESC) WHERE status = 'passed'
```

Главная метрика — «последнее проверенное восстановление» по источнику — читается из одной таблицы по частичному индексу, без соединений: дашборд со ста источниками — сто коротких проходов по индексу. `source_id` здесь — копия `snapshots.source_id`, и составной FK `(tenant_id, snapshot_id, source_id) → snapshots (tenant_id, id, source_id)` не даёт копии разойтись с оригиналом.

`on_origin_host` не хранится: это `verifier_agent_id = snapshots.agent_id`, обе колонки неизменны. Хранится то, что потом не вычислить: `key_holders` — сколько агентов держали ключ репозитория в момент проверки. По нему отчёт помечает проверку «ключ на одном хосте», даже если позже хранителей стало больше.

Глобальные `enrollment_tokens.token_hash`, `agent_certificates.serial` и скан `schedules` без `tenant_id` — единственные места, где системе нужно найти строку до того, как тенант известен.

### Расширения и их таблицы
Enterprise-модуль хранит свои таблицы в собственной схеме PostgreSQL (`sard_<id>`) со своей историей Flyway и может ссылаться на `public.tenants`. Нумерация миграций ядра и модулей не пересекается; удаление модуля не трогает схему ядра.

## Отвергнуто
- **База или схема PostgreSQL на тенанта.** Сильнейшая изоляция, но N миграций на каждый релиз, пул соединений на тенанта и кросс-тенантные отчёты MSP через `UNION`. Для управляющей плоскости, где бэкапы и ключи всё равно не на сервере (ADR 0008), не окупается. Если крупному клиенту нужна физическая изоляция — ему ставят отдельный экземпляр.
- **Колонка `tenant_id` только в enterprise-сборке.** Две схемы, две ветки миграций, переход с бесплатной версии — перенос данных.
- **Ни тенантов, ни `tenants` в ядре, только `tenant_id` без FK.** Без FK ничто не мешает строке сослаться на несуществующего тенанта.
- **Row-Level Security как основной механизм.** Требует `SET app.tenant_id` на каждое соединение из пула и отдельной роли без `BYPASSRLS`; ошибка в сбросе переменной между запросами хуже, чем её отсутствие. Остаётся кандидатом на второй рубеж (см. «Отложено»).
- **Hibernate `@Filter`.** Его надо включать на каждой сессии и он не действует на `find` по id; `@TenantId` лишён обоих недостатков.
- **Одноколоночные FK между таблицами тенантов.** Проверяют только существование строки, а не её тенанта; `@TenantId` защищает запросы, но не данные, записанные ошибочным кодом.
- **Каскадное удаление или `SET NULL` из истории.** Отчёт «проверено 12 марта» теряет смысл, если источник проверки исчез или обнулился.
- **`on_origin_host` колонкой.** Вычисляется из двух неизменных колонок; хранить стоит то, что не вычислить потом, — `key_holders`.
- **Схемы плагинов с ключом `(name, version)`.** Общая для тенантов таблица с ключом, который присылает агент, позволяет одному тенанту подменить схему другому; ключ по хешу содержимого — нет.
- **Глобальная `plugin_schemas`, адресуемая хешем (S4a).** Экономит повторы схемы (порядка килобайта на плагин на агента), но это вторая глобальная таблица рядом с `tenants`, которую пишет агент, и исключение из `TenancyIntegrationTest`. Колонка `config_schema JSONB` в `agent_plugins` убирает межтенантную поверхность целиком — решение владельца.
- **Время агента как ключ секционирования логов.** Строка с неверными часами агента не нашла бы секцию.
- **`DEFAULT` на `agents.tenant_id` в базе.** Ядро молча писало бы в тенант по умолчанию даже из enterprise-сборки с ошибкой в резолвере. Значение по умолчанию используется только для заполнения существующих строк в V2 и сразу снимается.

## Отложено
- **Контекст тенанта вне HTTP-запроса.** Поиск токена до тенанта решён (S2a, «Явный тенант и системный доступ»). Тенант gRPC-вызова агента решён в S3 («Явный тенант и системный доступ»): из сертификата, подтверждённого записью `agent_certificates`. Остаётся скан планировщика по всем тенантам (кандидат — ещё один вызов `TenantSessions.system`).
- **Пользователи и роли.** В ядре — вместе с аутентификацией; вероятная форма — глобальная `users` и `memberships (tenant_id, user_id, role)`: оператор MSP видит нескольких тенантов.
- **Каналы уведомлений** (токены Telegram, SMTP) — где хранить учётные данные сервера, решим на этапе уведомлений в духе ADR 0008.
- **`BackupOutput.repository_id`.** `snapshots.repository_id NOT NULL`, а `BackupOutput` в контракте его не несёт; копировать из `agent_repositories`, где id может быть пустым, — значит терять снимки репозиториев с неизвестным id. Нужное аддитивное поле в proto — на этапе «первый бэкап».
- **Общий `repository_id` у агентов разных тенантов** (вероятная ошибка конфигурации MSP). Поиск идёт сквозь тенанты, то есть ещё один вызов `system`; в путь Register не включён (S4a, решение владельца) — кандидат на отдельную системную проверку или отчёт.
- **RLS вторым рубежом** под `@TenantId` — если появится нативный SQL в объёме, который не проверить ревью.
