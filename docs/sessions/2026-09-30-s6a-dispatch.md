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
