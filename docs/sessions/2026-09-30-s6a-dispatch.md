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
Решения, машина состояний и правило сверки — `docs/adr/00XX-draft-run-dispatch.md`.

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
