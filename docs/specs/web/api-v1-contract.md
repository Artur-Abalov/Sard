# REST API v1, этап 1 — схема контракта (S8a)

Черновик для `web/src/api/openapi.yaml` (OpenAPI 3.1, рукописный источник
истины, решение 2 журнала `docs/sessions/2026-09-27-s8a-openapi.md`).
Сценарии — `docs/specs/web/s8a-api-contract.feature`. Значения, помеченные
**[П]**, — предложения спецификатора, ждут подтверждения (см. открытые
вопросы в отчёте S8a); до подтверждения их не реализовывать.

## Соглашения

| Тема | Правило |
|---|---|
| Префикс | все пути под `/api/v1`; тенант берётся из сессии, в пути и параметрах его нет |
| Идентификаторы | UUID, `format: uuid` |
| Время | RFC 3339, `format: date-time`, поля с суффиксом `At` |
| Списки | `GET` со `cursor` (непрозрачная строка) и `limit`; ответ `{items, nextCursor}`, `nextCursor: null` на последней странице; пустой список — `{items: [], nextCursor: null}` |
| `limit` | [П] по умолчанию 50, допустимо 1..200; вне диапазона → 400 `invalid_parameter` |
| Порядок | [П] новые сначала: агенты по `registeredAt`, токены и источники по `createdAt`, запуски по `queuedAt`, снимки по `createdAt` (все по убыванию) |
| Ошибки | `application/problem+json` (RFC 9457), схема `Problem` |
| Неизвестный id в пути | 404 `not_found`; [П] id не в формате UUID → 400 `invalid_parameter` |
| Тела запросов | [П] `additionalProperties: false`; лишнее поле → 422 `validation_failed` |
| Аутентификация | схема `cookieAuth` (`apiKey`, `in: cookie`, `name: sard_session`) глобально; `security: []` только у `POST /api/v1/session` и `GET /api/v1/status`; каждая защищённая операция документирует 401 |

### Problem

`type` (uri, [П] по умолчанию `about:blank`), `title`, `status`, `detail`,
`instance`, **`code`** (обязательное, машинное). [П] обязательные: `code`,
`status`, `title`. Дополнительные поля по месту: `activeRunId` (uuid),
`agentId` (uuid), `errors[] {pointer, message}` (для `validation_failed`,
`config_invalid`).

### Коды ошибок

| `code` | HTTP | Где |
|---|---|---|
| `unauthenticated` | 401 | любая защищённая операция без сессии |
| `invalid_credentials` | 401 | `POST /session` |
| `too_many_attempts` | 429 + `Retry-After` | `POST /session` |
| `not_found` | 404 | неизвестный id в пути |
| `invalid_parameter` [П] | 400 | `limit`, `afterSeq`, фильтр со значением вне перечисления, id не UUID |
| `invalid_cursor` [П] | 400 | `cursor` не выдан сервером |
| `validation_failed` [П] | 422 | тело не по схеме (нет обязательного поля, неверный тип, лишнее поле, `action` не `backup`/`verify`) |
| `ttl_out_of_range` [П] | 422 | `ttlSeconds` вне 300..604800 |
| `agent_not_found` [П] | 422 | `agentId` в теле не существует |
| `plugin_unknown_to_agent` [П] | 422 | плагина нет в последнем `Register` агента |
| `repository_unknown_to_agent` [П] | 422 | репозитория нет в последнем `Register` агента |
| `config_invalid` [П] | 422 | `config` не проходит `configSchema` плагина |
| `immutable_field` [П] | 422 | `PATCH` источника с `agentId`, `plugin` или `repositoryName` |
| `source_name_taken` [П] | 409 | имя занято живым источником тенанта (ADR 0013, правило 4) |
| `source_not_found` [П] | 422 | `sourceId` в теле не существует или удалён |
| `action_not_supported` [П] | 422 | плагин источника не объявляет действие |
| `no_snapshots` [П] | 422 | `verify` для источника без снимков |
| `run_already_active` [П] | 409 + `activeRunId` | D6 |
| `source_has_active_run` [П] | 409 + `activeRunId` | `DELETE` источника |
| `token_already_used` [П] | 409 + `agentId` | отзыв использованного токена |
| `token_expired` [П] | 409 | отзыв просроченного токена |

## Эндпоинты

`auth`: `—` — без сессии, `S` — сессия обязательна (401 `unauthenticated`).

| Метод, путь | auth | Запрос | Ответы |
|---|---|---|---|
| `GET /api/v1/status` | — | — | 200 `StatusResponse` (без изменений) |
| `POST /api/v1/session` | — | `{password}` | 204 + `Set-Cookie`; 401 `invalid_credentials`; 429 `too_many_attempts` + `Retry-After` |
| `GET /api/v1/session` | S | — | 200 `Session`; 401 |
| `DELETE /api/v1/session` | S | — | 204 (+ `Set-Cookie` с `Max-Age=0`); 401 |
| `GET /api/v1/agents` | S | `cursor`, `limit` | 200 `Page<AgentSummary>` [П]; 400; 401 |
| `GET /api/v1/agents/{agentId}` | S | — | 200 `Agent`; 400; 401; 404 |
| `POST /api/v1/enrollment-tokens` | S | `{ttlSeconds?}` | 201 `EnrollmentTokenCreated`; 401; 422 `ttl_out_of_range`, `validation_failed` |
| `GET /api/v1/enrollment-tokens` | S | `status?`, `cursor`, `limit` | 200 `Page<EnrollmentToken>`; 400; 401 |
| `GET /api/v1/enrollment-tokens/{tokenId}` | S | — | 200 `EnrollmentToken`; 400; 401; 404 |
| `POST /api/v1/enrollment-tokens/{tokenId}/revoke` | S | — | 200 `EnrollmentToken`; 400; 401; 404; 409 `token_already_used` (+`agentId`), `token_expired` |
| `GET /api/v1/sources` | S | `agentId?` [П], `cursor`, `limit` | 200 `Page<Source>`; 400; 401 |
| `POST /api/v1/sources` | S | `SourceCreate` | 201 `Source`; 401; 409 `source_name_taken`; 422 `validation_failed`, `agent_not_found`, `plugin_unknown_to_agent`, `repository_unknown_to_agent`, `config_invalid` |
| `GET /api/v1/sources/{sourceId}` | S | — | 200 `Source`; 400; 401; 404 |
| `PATCH /api/v1/sources/{sourceId}` | S | `SourceUpdate` | 200 `Source`; 400; 401; 404; 409 `source_name_taken`; 422 `validation_failed`, `immutable_field`, `config_invalid` |
| `DELETE /api/v1/sources/{sourceId}` | S | — | 204; 400; 401; 404; 409 `source_has_active_run` (+`activeRunId`) |
| `GET /api/v1/sources/{sourceId}/snapshots` | S | `cursor`, `limit` | 200 `Page<Snapshot>`; 400; 401; 404 |
| `POST /api/v1/runs` | S | `{sourceId, action}` | 201 `RunDetail`; 401; 409 `run_already_active` (+`activeRunId`); 422 `validation_failed`, `source_not_found`, `action_not_supported`, `no_snapshots` |
| `GET /api/v1/runs` | S | `sourceId?`, `status?` (повторяемый), `action?` [П], `cursor`, `limit` | 200 `Page<Run>`; 400; 401 |
| `GET /api/v1/runs/{runId}` | S | — | 200 `RunDetail`; 400; 401; 404 |
| `GET /api/v1/runs/{runId}/steps/{stepId}/logs` [П] | S | `afterSeq` (≥0, по умолчанию 0), `limit` [П 500, 1..1000] | 200 `LogPage`; 400; 401; 404 (шага нет или он из другого запуска) |

Отмены запуска, `workflowId`, удаления и отзыва агента в API этапа 1 нет.

## Схемы

Поле со знаком `?` может быть `null`, но присутствует всегда (обязательное,
тип `[T, "null"]`).

| Схема | Поля |
|---|---|
| `Session` | `tenantId` uuid, `expiresAt` |
| `AgentSummary` [П] | `id`, `hostname`, `online` bool, `lastSeenAt?`, `registeredAt`, `agentVersion`, `os?`, `arch?` |
| `Agent` | поля `AgentSummary` + `protocolVersion?` int, `plugins[] Plugin`, `repositories[] Repository`, `secretNames[] string`, `scriptNames[] string`. Ровно этот набор, значений секретов нет (ADR 0008) |
| `Plugin` | `name`, `version`, `actions[] Action`, `configSchema` (объект JSON Schema) |
| `Repository` | `name`, `backend`, `repositoryId?`, `cryptoProvider?` ([П] `null` — встроенный AES restic) |
| `EnrollmentTokenCreate` | `ttlSeconds?` integer, min 300, max 604800, default 86400 |
| `EnrollmentTokenCreated` | `id`, `token` (pattern `^sard_[A-Za-z0-9_-]{43}\.[0-9a-f]{64}$`, `docs/specs/enrollment-token.md`), `enrollCommand` (`sard-agent enroll --server <адрес от сервера> --token <token>`), `expiresAt` |
| `EnrollmentToken` | `id`, `status` (`active`, `used`, `expired`, `revoked`), `createdAt`, `expiresAt`, `usedAt?`, `revokedAt?`, `agentId?`. Ровно этот набор: ни `token`, ни `enrollCommand`, ни комментария |
| `SourceCreate` | все обязательны: `agentId`, `name`, `plugin`, `repositoryName`, `config` (объект) |
| `SourceUpdate` | только `name`, `config`, оба необязательны |
| `Source` | `id`, `agentId`, `name`, `plugin`, `repositoryName`, `config`, `createdAt`, `updatedAt` |
| `Snapshot` | `id`, `snapshotId` (id restic), `sourceId`, `runId`, `stepId`, `agentId`, `repositoryName`, `repositoryId`, `totalBytes`, `addedBytes`, `createdAt`, `forgottenAt?` |
| `RunCreate` | `sourceId`, `action` (`RunAction`) |
| `Run` | `id`, `sourceId`, `action` (`RunAction`), `trigger` (readOnly: `schedule`, `manual`, `verification`), `status` (`RunStatus`), `queuedAt`, `startedAt?`, `finishedAt?` |
| `RunDetail` | `Run` + `steps[] Step` |
| `Step` | `id`, `ordinal`, `action` (`Action`), `status` (`StepStatus`), `phase?` (`StepPhase`), `agentId`, `plugin`, `repositoryName`, `bytesProcessed`, `bytesTotal?`, `message?`, `output?` [П], `queuedAt`, `dispatchedAt?`, `startedAt?`, `finishedAt?` |
| `StepOutput` [П] | `oneOf`: `BackupOutput {snapshotId, totalBytes, addedBytes}`, `VerifyOutput {snapshotId, checks[] {name, passed, detail}}` |
| `LogLine` | `seq` (int64, ≥1, растёт внутри шага), `time`, `level` (`LogLevel`), `text` |
| `LogPage` [П] | `items[] LogLine`, `nextAfterSeq` (seq последней строки или переданный `afterSeq`, если строк нет) |
| `Page<T>` | `items[] T`, `nextCursor?` string |

### Перечисления

| Перечисление | Значения | Сверка с proto |
|---|---|---|
| `Action` | `backup`, `restore`, `verify`, `run` | равно `Action` без `UNSPECIFIED` |
| `RunAction` | `backup`, `verify` | подмножество `Action` |
| `StepPhase` | `accepted`, `preparing`, `dumping`, `uploading`, `restoring`, `verifying` | равно `StepPhase` |
| `LogLevel` | `debug`, `info`, `warn`, `error` | равно `LogLevel` |
| `StepStatus` | `queued`, `dispatched`, `running`, `succeeded`, `failed`, `cancelled`, `timed_out`, `rejected`, `lost` | каждое значение proto `StepStatus` входит; остальные не сверяются (S8b) |
| `RunStatus` | [П] `queued`, `dispatched`, `running`, `succeeded`, `failed`, `cancelled` | не сверяется (S8b) |
| `EnrollmentTokenStatus` | `active`, `used`, `expired`, `revoked` | — |

Правило сопоставления: имя значения proto без префикса перечисления
(`STEP_PHASE_`, `ACTION_`, `LOG_LEVEL_`, `STEP_STATUS_`) в нижнем регистре;
`*_UNSPECIFIED` не участвует.

### Статус токена

[П] Приоритет при вычислении: `used` (есть `usedAt`) → `revoked` (есть
`revokedAt`) → `expired` (`now >= expiresAt`) → `active`.

## Cookie сессии

`sard_session`; `HttpOnly`; `SameSite=Strict`; `Path=/api`. CSRF-заголовка
нет. `Secure` и срок жизни — W1b (открытый вопрос).

## Моки (MSW)

- Обработчики — для каждой операции выше; состояние (сессия, созданные
  токены, источники, запуски) хранится в памяти мока. Сервис-воркер не может
  выставить `HttpOnly`-cookie, поэтому мок хранит сессию своим флагом, а не
  через cookie.
- Vitest: перед каждым тестом состояние сбрасывается к фикстурам, сессия
  закрыта. Dev (`VITE_API_MOCKS=1`): при загрузке страницы сессия открыта.
- Пароль, который принимает мок, — константа из фикстур [П].
- Созданный моком запуск остаётся в `queued` [П].
- Мок не проверяет `config` по JSON Schema [П] (иначе нужен валидатор).
- Время активных токенов в фикстурах отсчитывается от момента сброса
  состояния, а не задано датой, иначе фикстура со временем «истечёт».

### Фикстуры

| Объект | Содержимое |
|---|---|
| Агенты | `A1` онлайн: плагин `files` (actions `backup`, `restore`, `verify`, `configSchema` — объект), репозиторий `local-main` (`local`, с `repositoryId`), `secretNames`, `scriptNames`. `A2` офлайн, `lastSeenAt` в прошлом: плагин `files` только с `backup`, репозиторий `nas` (`sftp`) |
| Источники | `S1` на `A1`/`local-main` — есть снимок; `S2` на `A1` — идёт запуск; `S3` на `A2`/`nas` — без запусков и снимков |
| Запуски | `R1` `succeeded` (`S1`, backup, снимок, шаг с 750 строками лога); `R2` `failed` (`S1`, у шага `message`); `R3` `running` (`S2`, фаза `uploading`, байты) |
| Токены | по одному `active`, `used` (`agentId = A1`), `expired`, `revoked` |

## Инструменты и CI

- `web/src/api/openapi.yaml` — источник; `npm run gen:api` генерирует из него
  `schema.d.ts`. [П] `web/src/api/openapi.json` удаляется, `make openapi`
  только перегенерирует типы.
- Линтер Redocly — в `npm run lint` (значит, в `gate web`). Новые
  зависимости — в `docs/dependencies.md` с лицензией.
- Job `server`: вместо `git diff` над `openapi.json` — проверка «каждая пара
  метод+путь из `/v3/api-docs` есть в `openapi.yaml`». Решение — ADR 0019.
- Сверка перечислений с proto ломает сборку при расхождении.
