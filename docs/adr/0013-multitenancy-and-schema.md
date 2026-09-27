# 0013 — Мультитенантность и схема БД сервера

- Статус: принято (основа тенантности реализована; остальная схема — целевая, таблицы появляются вместе с фичами)
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

### Правила схемы (для всех будущих таблиц)
1. **Составные внешние ключи.** У каждой таблицы тенанта есть `UNIQUE (tenant_id, id)`; ссылки на другие таблицы тенанта — `FOREIGN KEY (tenant_id, x_id) REFERENCES x (tenant_id, id)`. База сама не даст сослаться на строку чужого тенанта, даже если ошибся код. Уникальный индекс заодно служит индексом по `tenant_id`.
2. **Уникальность — внутри тенанта:** `UNIQUE (tenant_id, name)`, а не `UNIQUE (name)`.
3. **Индексы начинаются с `tenant_id`**, кроме индексов для системных сканов (планировщик, см. «Отложено»).
4. `ON DELETE SET NULL` на составном ключе — только со списком колонок: `ON DELETE SET NULL (source_id)` (PostgreSQL 15+), иначе обнулится и `tenant_id`.
5. Первичные ключи — `UUID`, генерирует приложение (UUIDv7: упорядочены по времени, дружат с B-деревом).
6. Время — `TIMESTAMPTZ`. Перечисления — `TEXT` с `CHECK`, не `CREATE TYPE ... AS ENUM` (новое значение — обычная миграция).
7. Данные тенанта читаются через JPA. `JdbcTemplate` и нативный SQL обходят фильтр Hibernate; такой запрос обязан содержать `tenant_id = ?` явно и проходит ревью как исключение.
8. Секретов в базе нет (ADR 0008): `config` источников содержит ссылки (`password_ref`), репозитории — только имена.

Правила 1 и «каждая таблица тенанта имеет `tenant_id NOT NULL`» + «каждая сущность имеет `@TenantId`» проверяет `TenancyIntegrationTest`: новая таблица или сущность без тенанта роняет тест.

### Целевая схема
Реализовано сейчас: `tenants`, `agents.tenant_id`. Остальные таблицы создаёт миграция той фичи, которой они нужны, и уточняет её спецификация.

```
tenants                       глобальная
  id PK, name UNIQUE, created_at

agents                        тенант · реализовано
  id PK, tenant_id, hostname, agent_version, registered_at, last_seen_at
  + (этап «регистрация») os, arch, protocol_version, cert_serial, revoked_at,
    secret_names TEXT[], script_names TEXT[]        -- снимок из Register, только имена
  UNIQUE (tenant_id, id)

enrollment_tokens             тенант
  id PK, tenant_id, token_hash BYTEA UNIQUE, expires_at, used_at, agent_id NULL
  -- UNIQUE по token_hash глобальный: токен ищется до того, как тенант известен

agent_plugins                 тенант, снимок из Register
  (agent_id, name) PK, tenant_id, version, config_schema JSONB, actions TEXT[]

agent_repositories            тенант, снимок из Register (ADR 0008)
  (agent_id, name) PK, tenant_id, backend, repository_id NULL, crypto_provider
  INDEX (tenant_id, repository_id)  -- сколько агентов держат ключ репозитория

sources                       тенант — что бэкапим
  id PK, tenant_id, agent_id, name, plugin, config JSONB, created_at, updated_at
  UNIQUE (tenant_id, name); FK (tenant_id, agent_id) → agents

workflows                     тенант
  id PK, tenant_id, name, definition JSONB, created_at, updated_at
  UNIQUE (tenant_id, name)

schedules                     тенант
  id PK, tenant_id, workflow_id, cron, timezone, enabled, next_run_at
  INDEX (next_run_at) WHERE enabled   -- системный скан планировщика

runs                          тенант — запуск workflow
  id PK, tenant_id, workflow_id, schedule_id NULL, trigger, status,
  definition JSONB (снимок workflow на момент запуска), queued_at, started_at, finished_at
  INDEX (tenant_id, workflow_id, queued_at DESC)

run_steps                     тенант — одна команда агенту
  id PK (= RunStep.command_id), tenant_id, run_id, ordinal, agent_id, plugin, action,
  repository_name, status, phase, bytes_processed, bytes_total, message,
  output JSONB (BackupOutput | RestoreOutput | VerifyOutput | RunOutput), started_at, finished_at

step_logs                     тенант — LogChunk
  (step_id, seq) PK, tenant_id, time, level, text   -- кандидат на партиционирование по времени

snapshots                     тенант — снимки restic, созданные Sard
  id PK, tenant_id, source_id NULL, step_id, agent_id, repository_name, repository_id,
  snapshot_id, total_bytes, added_bytes, created_at

restore_verifications         тенант — доказательство восстановимости (ADR 0008, п. 3)
  id PK, tenant_id, snapshot_id → snapshots, step_id, verifier_agent_id,
  on_origin_host BOOL, status, checks JSONB, verified_at
```

Глобальный `UNIQUE` на `enrollment_tokens.token_hash` и скан `schedules` без `tenant_id` — единственные места, где системе нужно найти строку до того, как тенант известен.

### Расширения и их таблицы
Enterprise-модуль хранит свои таблицы в собственной схеме PostgreSQL (`sard_<id>`) со своей историей Flyway и может ссылаться на `public.tenants`. Нумерация миграций ядра и модулей не пересекается; удаление модуля не трогает схему ядра.

## Отвергнуто
- **База или схема PostgreSQL на тенанта.** Сильнейшая изоляция, но N миграций на каждый релиз, пул соединений на тенанта и кросс-тенантные отчёты MSP через `UNION`. Для управляющей плоскости, где бэкапы и ключи всё равно не на сервере (ADR 0008), не окупается. Если крупному клиенту нужна физическая изоляция — ему ставят отдельный экземпляр.
- **Колонка `tenant_id` только в enterprise-сборке.** Две схемы, две ветки миграций, переход с бесплатной версии — перенос данных.
- **Ни тенантов, ни `tenants` в ядре, только `tenant_id` без FK.** Без FK ничто не мешает строке сослаться на несуществующего тенанта.
- **Row-Level Security как основной механизм.** Требует `SET app.tenant_id` на каждое соединение из пула и отдельной роли без `BYPASSRLS`; ошибка в сбросе переменной между запросами хуже, чем её отсутствие. Остаётся кандидатом на второй рубеж (см. «Отложено»).
- **Hibernate `@Filter`.** Его надо включать на каждой сессии и он не действует на `find` по id; `@TenantId` лишён обоих недостатков.
- **`DEFAULT` на `agents.tenant_id` в базе.** Ядро молча писало бы в тенант по умолчанию даже из enterprise-сборки с ошибкой в резолвере. Значение по умолчанию используется только для заполнения существующих строк в V2 и сразу снимается.

## Отложено
- **Системный доступ и контекст тенанта вне HTTP-запроса.** Планировщику нужен скан всех тенантов, регистрации — поиск токена до тенанта, gRPC-потоку агента — тенант из сертификата. В ядре все три тривиальны (тенант один). Для enterprise решим вместе с планировщиком и регистрацией: `CurrentTenantIdentifierResolver.isRoot` для системных операций; тенант агента — из его сертификата (например, URI SAN `sard://tenants/<tenant>/agents/<agent>`, дополнение к ADR 0009), а не из запроса к базе; контекст — элемент контекста корутины, а не `ThreadLocal`.
- **Пользователи и роли.** В ядре — вместе с аутентификацией; вероятная форма — глобальная `users` и `memberships (tenant_id, user_id, role)`: оператор MSP видит нескольких тенантов.
- **Каналы уведомлений** (токены Telegram, SMTP) — где хранить учётные данные сервера, решим на этапе уведомлений в духе ADR 0008.
- **RLS вторым рубежом** под `@TenantId` — если появится нативный SQL в объёме, который не проверить ревью.
