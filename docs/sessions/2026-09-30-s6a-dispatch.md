<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-09-30 — S6a: запуски и отправка команд агенту

Ветка: `claude/s6a-dispatch-7oa35k` (в постановке — `feat/s6a-dispatch`; ветку задаёт среда сессии).
Предусловие выполнено: X2 фаза 1 — ADR 0022 (D6) принят, правка схемы внесена в ADR 0013 (`docs/adr/0022-one-active-run-per-source.md:3`, `docs/adr/0013-multitenancy-and-schema.md:3`).

## Фаза 1 — исследование и вопросы (до кода)

### Проверенные факты (file:line)
- `AgentConnections.send(agentId, ConnectResponse): SendResult` — не `suspend`, «never waits» (`server/.../agents/stream/AgentConnections.kt:22-25`); `Queued | NotConnected | QueueFull` (`SendResult.kt`); `Queued` ≠ «доставлено», очередь теряется при закрытии (`docs/adr/0026-agent-stream-manager.md:44`).
- `CommandReconciliation.onHello(agent: ConnectedAgent, runningCommandIds: List<String>)`, не `suspend`, вызывается на корутине чтения потока, на `Dispatchers.IO`, в gRPC Context агента; исключение закрывает поток (`AgentStreamExtensions.kt:24-36`, `AgentStreams.kt:85-97`). Реализация по умолчанию подменяется единственным бином (`AgentStreamConfiguration.kt:20-40`).
- Реестр сессий в памяти, один узел (`AgentSessionRegistry.kt`, ADR 0026:50-54); `sessions()` — список живых сессий (`AgentSessionRegistry.kt:67`); онлайн = сообщение за `offlineAfter` = 3 heartbeat = 90 с; heartbeat по умолчанию 30 с (`enrollment/AgentEndpointConfiguration.kt`).
- Тенантность — Hibernate `@TenantId`; `TenantSessions.inTenant(tenantId)` и `system` — **только чтение** (`persistence/TenantSessions.kt:28-41`); вызовы `system` сверяются со списком ADR 0013 (`server/src/test/kotlin/dev/sard/server/ArchitectureTest.kt:75-79`).
- `TenantSchemaRulesTest`: FK `tenant_id → tenants`; `UNIQUE (tenant_id, id)`; составные FK между тенантными таблицами.
- Периодическая работа — `AgentStreamSweeper` (`SmartLifecycle`, однопоточный планировщик), тесты двигают `MovableClock` и вызывают проверку руками.
- Нарушения уникальности в коде нигде не разбираются (grep по `DataIntegrityViolation`/`23505` в `server/src/main` пуст).
- Контракт S8a: `StepStatus` = queued, dispatched, running + финальные proto + **`lost`**, тест `ApiContractIntegrationTest.kt:89-94`; у `RunStep`/`RunSummary` есть только текстовое `message` «Why it failed, was rejected or lost» (`RunsController.kt:21`); кода причины в REST нет. Ответ владельца 2 в S8a: `rejected/timed_out/lost` шага дают `failed` запуска (`docs/sessions/2026-09-27-s8a-openapi.md:54`).
- Агент: `Hello.running_command_ids` — только queued/running на агенте, завершённые без ResultAck туда не входят (`agent/internal/executor/executor.go:309-322`); сразу после Hello агент ставит в очередь все неподтверждённые результаты (`agent/internal/transport/transport.go:304-308`). Повторный `command_id` не исполняется: агент шлёт сохранённый результат или прогресс (`agent/internal/executor/command.go:120-128`); результаты и надгробия (7 дней) — на диске.
- Агент после A6a: встроенные плагины postgresql, mysql, files, network (`agent/plugins/builtin.go:18-25`), «unknown plugin %q» — для прочих имён (`command.go:93`); timeout 0 → максимум агента 24 ч (`command.go:153-158`).
- E2E: состояние сервера — только через SQL (`test/e2e/.../SardEnvironment.kt:138`), тестовые ручки запрещены (ADR 0020:22); заготовка `TransportExecutorPending` ждёт S6 (`Pending.kt:12-28`).

### Расхождения постановки с контрактом — остановка
1. **Статус потерянного шага.** Постановка: `failed` с причиной `AGENT_LOST_STEP`; комментарий proto `Hello` — «as failed ("agent lost the step")» (`proto/sard/agent/v1/agent.proto:165-167`). ADR 0013 — `lost` («не failed: агент не сообщал об ошибке», `docs/adr/0013-multitenancy-and-schema.md:156-158`), контракт S8a — `lost` в `StepStatus` с тестом. `AGENT_LOST_STEP` в репозитории нет.
2. **`workflow_id` без таблицы `workflows`.** Столбец с FK невозможен, без FK нарушает правило 2 ADR 0013.

### Вопросы — в сообщении владельцу; ответы и схема фазы 1 — после ревью

### Ответы владельца (2026-09-30)
1. Потерянный шаг — `lost`, `message = 'agent lost the step'`, запуск `failed`; без кода причины.
2. `workflows` создаётся в этой миграции (без сервиса), `runs.workflow_id` — с FK.
3. Захват `queued → dispatched` до `send`; случай «упал между захватом и отправкой» — в журнал и счётчик, смотреть частоту в релизе.
4. `dispatched`, которого нет в Hello, отправляется повторно (дедупликация A4); потерянным считается только `running`.
5. Кандидаты в памяти, отсчёт от Hello, окно 2 heartbeat, запись в тенанте агента.
6. Результат после `lost` — `lost` остаётся, ResultAck, запись в журнал (S7).
7. Стык для S7 — условные переходы `StepTransitions`, «результат пришёл» = статус в базе.
8. Статус запуска повторяет единственный шаг; `lost/rejected/timed_out` → `failed`.
9. Тик повторной доставки по живым сессиям.
10. Метрика через `system` на тике — новый вызывающий в ADR 0013 и `ArchitectureTest`.
11. `deleted_at` явно, блокировка строки источника при удалении и запуске.
12. S6 проверяет агента, плагин, репозиторий; `config` по схеме — S8b; `timeout` не задаётся, `tags` пустые.
13. E2E: строки вставляются SQL с `plugin = 'absent'`, результат наблюдается по журналу сервера (debug для логгера обработчика).

### Проект (на ревью)
Решения, машина состояний и правило сверки — `docs/adr/0037-run-dispatch.md`.

**Миграция `V202609301200__runs.sql`** (правила ADR 0013: `tenant_id` FK, `UNIQUE (tenant_id, id)`, составные FK без CASCADE, `TIMESTAMPTZ`, `TEXT CHECK IN`):
- `workflows`: `id, tenant_id, name, definition JSONB NOT NULL, created_at, updated_at, deleted_at`; `UNIQUE (tenant_id, name) WHERE deleted_at IS NULL`.
- `sources`: `id, tenant_id, agent_id, name, plugin, config JSONB NOT NULL CHECK (jsonb_typeof(config) = 'object'), repository_name, created_at, updated_at, deleted_at`; `plugin` и `repository_name` — те же регулярные выражения, что в `agent_plugins`/`agent_repositories`; `(tenant_id, agent_id) → agents`; `UNIQUE (tenant_id, name) WHERE deleted_at IS NULL`; `INDEX (tenant_id, agent_id)`.
- `runs`: `id, tenant_id, source_id NOT NULL, workflow_id NULL, trigger, status, definition NULL, message NULL, queued_at NOT NULL, started_at, finished_at`; CHECK из ADR 0022 и `(status IN активных) = (finished_at IS NULL)`; `runs_active_source_key UNIQUE (tenant_id, source_id) WHERE status IN ('queued','dispatched','running')`; индексы `(tenant_id, source_id, queued_at DESC)`, `(tenant_id, workflow_id, queued_at DESC)`.
- `run_steps`: столбцы и CHECK ADR 0013; `(tenant_id, run_id) → runs`, `(tenant_id, agent_id) → agents`, `(tenant_id, source_id) → sources`; `UNIQUE (tenant_id, run_id, ordinal)`; `INDEX (tenant_id, agent_id, queued_at, id) WHERE status IN ('queued','dispatched','running')` — доставка и сверка.

**Пакеты**
- `sources/`, `runs/` — домен, без gRPC и `agents/` (добавляются в `ISOLATED_PACKAGES` `ArchitectureTest`).
- `agents/dispatch/` — отправка, сверка (`CommandReconciliation`), тик, метрика; зависит от `runs/` и `agents/stream/`.

**Стыки для S8b** (тенант передаёт вызывающий из `TenantResolver`, как `EnrollmentTokens`)
```kotlin
class Sources(sessions: TenantSessions, clock: Clock, ids: UuidV7) {
    fun create(tenantId: UUID, draft: SourceDraft): SourceView
    fun replace(tenantId: UUID, sourceId: UUID, draft: SourceDraft): SourceView
    fun delete(tenantId: UUID, sourceId: UUID)
    fun get(tenantId: UUID, sourceId: UUID): SourceView
    fun list(tenantId: UUID, agentId: UUID?, after: UUID?, limit: Int): List<SourceView>
}
class Runs(sessions: TenantSessions, clock: Clock, ids: UuidV7, queued: StepsQueued) {
    fun start(tenantId: UUID, sourceId: UUID): RunView   // после фиксации — queued.onQueued(...)
}
sealed class SourceException : RuntimeException() {
    SourceNotFound(sourceId)          // в т. ч. удалённый  → 404
    UnknownAgent(agentId)             // → 422 unknown_agent
    UnknownPlugin(plugin)             // → 422 unknown_plugin
    UnknownRepository(repositoryName) // → 422 unknown_repository
    RunActive(activeRunId)            // → 409 run_active
}
fun interface StepsQueued { fun onQueued(tenantId: UUID, agentId: UUID) }  // реализует agents/dispatch
```

**Стык для S7**
```kotlin
class StepTransitions(sessions: TenantSessions, clock: Clock) {
    fun accepted(tenantId: UUID, stepId: UUID, phase: String): Boolean             // dispatched → running
    fun finished(tenantId: UUID, stepId: UUID, outcome: StepOutcome): Boolean      // dispatched|running → финальный
    fun lost(tenantId: UUID, stepId: UUID): Boolean                                // running → lost (окно)
}
```
Каждый переход — один условный `UPDATE` шага и запуска в одной транзакции; `false` — статус уже сменил другой путь.

**Настройки**: `sard.run.dispatch.lost-after` (по умолчанию 2 × `sard.agent.heartbeat-interval`), `sard.run.dispatch.check-interval` (по умолчанию `sard.agent.stream.check-interval`).
**Метрики**: gauge `sard.run.steps.queued`, `sard.run.steps.dispatched`; счётчики `sard.run.steps.redispatched`, `sard.run.steps.dispatch.failed`.

Фаза 1 закончена — СТОП до ревью проекта.

## Фаза 2 — миграция, сервисы источников и запусков (2026-09-30)

Ревью проекта фазы 1 — «Пошел» (владелец).

### Окружение сессии
- Docker не был запущен — `dockerd` поднят вручную; JDK 25 поставлен из apt (`openjdk-25-jdk-headless`, после `apt-get update`); Gradle требует `LC_ALL=C.UTF-8`. Maven Central один раз ответил 429 — прошло само при повторе.

### Сделано
- `V202609301200__runs.sql`: `workflows` (без сервиса), `sources`, `runs`, `run_steps` — как в проекте фазы 1. Сверх проекта: `sources.name` 1..200 символов (как метка токена), `run_steps_repository_check` (`repository_name` NULL ровно для `run`, как в REST `RunStep.repositoryName`), неотрицательные `bytes_*`, `ordinal >= 0`.
- `persistence/RunRecords.kt`: `SourceRecord`, `RunRecord`, `RunStepRecord`; изменяемые поля шага сущностью не пишутся (`insertable/updatable = false`) — только условными обновлениями (фаза 3).
- Пакет `runs/` (изолирован в `ArchitectureTest`): `Sources` (create, replace, delete, get, list), `Runs.start`, `StepsQueued`, ошибки `RunsException`: `SourceNotFound` (в т. ч. удалённый), `SourceNameTaken`, `UnknownAgent` (нет или отозван), `UnknownPlugin`, `UnknownRepository`, `RunActive(activeRunId)`.
- `Runs.start`: одна транзакция — блокировка строки источника `FOR SHARE` (`PESSIMISTIC_READ`), проверка снимка агента, предварительный поиск активного запуска, вставка `runs` + шага `backup` в `queued` с копией `config`, `flush`. Нарушение `runs_active_source_key` → `RunActive` с id победителя из новой транзакции; если победитель уже завершился — повтор запуска. `StepsQueued.onQueued` — после фиксации. Удаление источника — `FOR UPDATE` и отказ `RunActive`.
- ADR 0013: таблицы отмечены реализованными, отличия схемы, правило `lost` по решению 4.
- `SardServerIntegrationTest`: список миграций дополнен новой.

### Проверка
- `RunSchemaIntegrationTest` (9): D6 для каждого активного статуса, финальные не мешают, CHECK статусов и времён, `workflow_id`/`trigger`, повтор имени после удаления, `config` — объект.
- `SourcesIntegrationTest` (12): хранение и чтение, отказы по снимку агента (в т. ч. агент другого тенанта и отозванный), имя, замена целиком, удаление, удаление при активном запуске, чужой тенант, список.
- `RunsIntegrationTest` (14) — тесты постановки 1, 7, 8:
  - 1: 8 параллельных запусков — ровно один run и один шаг, остальные `RunActive` с его id; после финального статуса новый запуск разрешён. В одном прогоне один из семи проигравших дошёл до индекса (в журнале теста `duplicate key ... "runs_active_source_key"`), остальных остановила проверка — поэтому добавлены два детерминированных теста индекса: незакоммиченная вставка в соседней транзакции, `start` ждёт на индексе (`pg_stat_activity`), после `commit` — `RunActive` с id соседа, после `rollback` — запуск создан.
  - Контроль: с неверным именем ограничения в `Runs` падают тест гонки и тест индекса (2 из 14); код восстановлен.
  - 7: источник другого тенанта — `SourceNotFound`, запусков нет; тенанты запускаются независимо.
  - 8: удалённый источник, репозиторий или плагин, которых нет в последнем Register, отозванный агент — типизированные ошибки, запуск не создан, `onQueued` не вызван.
- `RunModelTest` (`@MutFlowTest`, 6): хранимые значения перечислений = списки CHECK, обратное чтение, неизвестное значение — ошибка, `toString` без `config`.
- `./scripts/gate.sh server fast` — `gate: PASSED (server, fast)`: 628 тестов, покрытие 95.0% (instructions), CRAP ≤ 6 (новый код — максимум 6.0, `AgentOffer.require`).
- `./gradlew --no-daemon -q -Pmutflow.enabled=true :server:test --rerun` — exit 0, 7555 прогонов тестов, выживших нет.
- По пути: detekt `ThrowsCount` в `AgentOffer.require` — один `throw` после `when`; ktlint (предел 140) склеивал переносы, detekt (120) их требовал — строки переписаны без переносов выражений.

### Стыки для S8b (реализованы)
```kotlin
class Sources {
    fun create(tenantId: UUID, draft: SourceDraft): SourceView
    fun replace(tenantId: UUID, sourceId: UUID, draft: SourceDraft): SourceView
    fun delete(tenantId: UUID, sourceId: UUID)
    fun get(tenantId: UUID, sourceId: UUID): SourceView
    fun list(tenantId: UUID, agentId: UUID?, after: UUID?, limit: Int): List<SourceView>  // по id
}
class Runs { fun start(tenantId: UUID, sourceId: UUID): RunView }
// SourceDraft.config / SourceView.config — JSON-текст объекта; S8b сериализует Map через Jackson.
// Ошибки → S8a: SourceNotFound 404; SourceNameTaken 422 validation_failed (name); UnknownAgent/
// UnknownPlugin/UnknownRepository 422 unknown_*; RunActive 409 run_active + activeRunId.
// Не сделано (S8b): проверка config по configSchema, курсор списка поверх `after`, чтение runs.
```

Фаза 2 закончена — СТОП до ревью.

## Фаза 3 — отправка, доставка при подключении, сверка по Hello (2026-09-30)

Ревью фазы 2 — «Пошел» (владелец).

### Сделано
- `runs/StepTransitions` (реализует `DispatchLedger`): каждый переход — один нативный `UPDATE` шага с `tenant_id` и ожидаемым статусом в `WHERE` (ADR 0013, правило 8) и, если строка сдвинулась, `UPDATE` запуска в той же транзакции по `RunState.following`: `claim` (queued → dispatched), `release` (dispatched → queued), `redispatch` (dispatched, отправлен раньше Hello → dispatched_at = now), `lost` (running → lost, `LOST_MESSAGE`), для S7 — `accepted` (dispatched → running) и `finished` (dispatched|running → финальный). `active(tenant, agent)` — активные шаги агента по `queued_at, id`.
- `runs/StepCounts.waiting()` — четвёртый вызов `TenantSessions.system` (ADR 0013, список; `ArchitectureTest`).
- `agents/dispatch/`: `StepDispatcher` (onQueued, onHello, tick; замок на агента; отправка вне транзакций), `LostStepWatch` (кандидаты в памяти), `RunSteps.of` (шаг → proto `RunStep`: command_id = id шага, plugin, config_json, action, repository_name; без timeout и tags), `ConnectionLinks` (над `AgentConnections` и реестром), `MicrometerDispatchMetrics`, `DispatchConfiguration` — бины `CommandReconciliation`, `StepsQueued`, второй `AgentStreamSweeper` для тика.
- Настройки: `sard.run.dispatch.lost-after-heartbeats` (2; в проекте фазы 1 называлась `lost-after` — переименована по образцу `sard.agent.stream.*-heartbeats`), `sard.run.dispatch.check-interval` (не задана — `sard.agent.stream.check-interval`). В `application.yaml`.
- Тест S5a: `StreamTestConfiguration.recordingExtensions` помечен `@Primary` — иначе при двух `CommandReconciliation` `getIfUnique` отдаёт `LoggingInbound`, и тесты S5a не видят событий Hello. Поведение тестов S5a не менялось.
- e2e: `AgentContainer` вынесен из `AgentConnectTest` (без изменения его проверок), `EnrollmentTokens.DEFAULT_TENANT` открыт, `RunStepSeamTest` (тест 9), README и `Pending.kt` обновлены.

### Проверка (тесты постановки)
- 2 — `DispatchIntegrationTest` (настоящий gRPC-сервер S5a, клиент в тесте, mTLS): онлайн-агент получает `RunStep` с command_id = id шага, плагином, конфигом, репозиторием и `ACTION_BACKUP`; шаг и запуск — `dispatched`, `dispatched_at` = часы.
- 3 — офлайн: три шага в `queued`; после Hello — три `RunStep` в порядке создания, все `dispatched`, больше ничего.
- 4 — отказ сессии: `StepDispatcherTest` (фейки) — `QueueFull` и `NotConnected` возвращают шаг в `queued`, раунд останавливается, счётчик; интеграционно — шаг, возвращённый в `queued` у онлайн-агента, доставляется следующим тиком. Настоящий `QueueFull` через gRPC не воспроизводился: маленькие `RunStep` не заполняют окно HTTP/2 (S5a заполнял сообщениями по 64 KiB).
- 5 — сверка: перечисленный в Hello шаг не отправляется повторно; `dispatched`, которого нет в Hello, отправляется снова (`dispatched_at` обновлён, `sard.run.steps.redispatched` + 1); `running` без Hello — `running` за секунду до окна, `lost` и запуск `failed` по окну, повторно не отправляется; с результатом в окне (`finished`) — остаётся `succeeded`.
- 6 — гонка: `StepTransitionsIntegrationTest` — оба порядка детерминированы (результат первым — `lost` ничего не меняет; `lost` первым — поздний результат ничего не меняет), 10 раундов параллельных `lost` и `finished`: ровно один `true`, статусы шага и запуска — победителя.
- 7 — тенант: агент тенанта B после Hello и тика не получает шаг тенанта A, тот остаётся `queued`; все переходы и `active` с чужим тенантом — `false`/пусто.
- Метрика: после тика `sard.run.steps.queued` = 1, `sard.run.steps.dispatched` = 1.
- Контроли: без фильтра `running_command_ids` в `onHello` сначала упал только 1 тест — выяснилось, что юнит-тест делал Hello и тик на двух разных экземплярах диспетчера, а интеграционный не двигал часы перед повторным Hello (время отправки = время Hello, повтор не срабатывал и без фильтра). Тесты исправлены; повторный контроль — падают 3 теста (юнит и интеграционный), код восстановлен.
- `./scripts/gate.sh server fast` — `gate: PASSED (server, fast)`: 674 теста, 0 упавших, покрытие 95.4% (instructions), CRAP ≤ 6 (новый код — максимум 6.0: `AgentOffer.require`, `RunState.following`).
- mutflow (`-Pmutflow.enabled=true :server:test --rerun`): первый прогон — 4 выживших: граница `lost-after-heartbeats >= 1` (вызов вне `MutFlow.underTest`) и две записи в журнал (сбой `onQueued`, отметка «потерян») — журнал теперь проверяется через `ListAppender`, добавлен тест «результат закрыл шаг до окна — в журнале ничего». Второй прогон — exit 0, 8376 запусков, выживших нет.
- 9 — `RunStepSeamTest` (e2e, настоящий агент): строки `sources`/`runs`/`run_steps` (плагин `absent`) вставлены SQL до запуска агента; после Register и Hello шаг — `dispatched`; в журнале сервера — `agent <id> result of command <step>: STEP_STATUS_REJECTED` (`LoggingInbound`, debug через `LOGGING_LEVEL_DEV_SARD_SERVER_AGENTS_STREAM`). Текст «unknown plugin» сервер до S7 не журналирует (только id и статус), агент тоже; что отказ именно по плагину — по порядку проверок `agent/internal/executor/command.go:93` (плагин раньше репозитория) — вывод из кода, не наблюдение.
- `make e2e` (с `E2E_SERVER_BUILD_FLAGS='--network host --secret id=build-ca,src=/root/.ccr/agent-proxy-ca.crt --build-arg JAVA_TOOL_OPTIONS=…proxy…'`, как в T2a) — exit 0, версия `ec26fe4`: 15 тестов в 7 классах, 0 упавших (`AgentConnectTest` 1, `AgentImageSmokeTest` 2, `EnrollmentTokenFormatTest` 3, `FailureLogsTest` 2, `RegistrationTest` 3, `RunStepSeamTest` 1, `ServerSmokeTest` 3). Первая сборка образа без CA прокси упала на PKIX в загрузке Gradle.
- `make license-check` — 435 files OK.

### Стыки для S7
```kotlin
class StepTransitions : DispatchLedger {
    fun accepted(tenantId: UUID, stepId: UUID, phase: String): Boolean        // dispatched → running; run → running, started_at
    fun finished(tenantId: UUID, stepId: UUID, outcome: StepOutcome): Boolean // dispatched|running → final; run → RunState.following
    // false: шаг уже закрыт (другим результатом или окном lost) — S7 отвечает ResultAck и ничего не пишет (proto ResultAck; решение 6)
}
data class StepOutcome(val status: StepState, val message: String?)          // только финальный status
const val LOST_MESSAGE = "agent lost the step"
```
- «Результат пришёл» — статус шага в базе: окно делает `running → lost` условно, после `finished` оно ничего не меняет.
- Прогресс (`phase`, `bytes_*`) — отдельные условные обновления S7 по `running`; столбцы есть, сущность их не пишет.
- S7 заменяет `StepResultHandler`/`StepProgressHandler` из `LoggingInbound` своими бинами; `CommandReconciliation` уже занят диспетчером.
- До S7 шаг, на который агент ответил, остаётся `dispatched`, и источник не запускается снова (D6).

### Стыки для S8b
- Сервисы и ошибки — раздел фазы 2; `Runs.start` после фиксации вызывает `StepsQueued.onQueued` — диспетчер отправляет шаг онлайн-агенту, ошибки отправки не доходят до вызывающего.
- Чтение запусков (`RunsApi.listRuns/getRun`) — `RunViews.of`/`RunViews.step` над `RunRecord`/`RunStepRecord`; сервиса чтения нет.

### Открыто
- Кандидаты в потерянные живут в памяти одного узла (ADR 0026); при HA — вместе с реестром сессий.
- Шаги, созданные в одну миллисекунду, упорядочены по UUIDv7 со случайной частью — порядок создания внутри миллисекунды не гарантирован.
- Черновик ADR `0037-run-dispatch.md` — номер при слиянии.
