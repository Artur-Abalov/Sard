# 2026-10-04 — FXs: потеря шага по сроку в БД, теги снимков, след RunFinished

Исправляет Д1, Д2, Д5, Д6 из `t3-defects.md` (сверка 2026-10-04, `main` @ `2a21a0f`).
Решение владельца D13 по Д3: перезапуск агента посреди шага — честный провал (FXa).

## Фаза 1 — исследование (ждёт ревью)

Проверено чтением кода. Номера строк — по `main` @ `2a21a0f`. Кода не написано.

### Как сейчас

- Срок потери живёт только в памяти: `LostStepWatch`, `ConcurrentHashMap`
  по агенту (`agents/dispatch/DispatchParts.kt:67`). Заполняется только в
  `StepDispatcher.onHello` (`StepDispatcher.kt:53-62`), разбирается в
  `tick()` (`:65-68`). `expect` заменяет срок при каждом Hello (Д2).
- `tick` вызывает `AgentStreamSweeper` (`SmartLifecycle`,
  `scheduleWithFixedDelay`) с интервалом `sard.run.dispatch.check-interval`
  или `sard.agent.stream.check-interval` (30 с)
  (`DispatchConfiguration.kt`, строки `checkInterval ?: streamCheckInterval`).
- Переход в lost — `LOSE = "$CLOSE and status = 'running'"`
  (`runs/StepTransitions.kt:33`). Запуск уходит в `failed` через
  `RUN_FINISHED`, затем `announcer.announce` публикует RunFinished.
- Черновик ADR явно **отвергает** `dispatched → lost`
  (`docs/adr/00XX-draft-run-dispatch.md`, «Отвергнуто», первый пункт) и
  срок в БД («Кандидаты в базе»). Эта задача меняет оба решения, см. В1.
- `TenantSessions.system` только читает (`persistence/TenantSessions.kt:38`,
  `SET TRANSACTION READ ONLY`). Его вызовы перечислены в ADR 0013 и
  `ArchitectureTest`.
- RLS нет. Изоляция — `tenant_id` в каждом WHERE (ADR 0013, правило 8).

### Конец сессии (S5a)

- Единая точка — `AgentStreams.ended`: `if (!registry.release(stream)) return`
  (`agents/stream/AgentStreams.kt:153`), затем
  `AgentSessionListener.disconnected(agent, reason)`
  (`AgentStreamExtensions.kt:68`). Срабатывает при EOF, ошибке, отмене,
  SESSION_EXPIRED (пропуск heartbeat, `AgentSessionRegistry.sweep`),
  SERVER_SHUTTING_DOWN, AGENT_REVOKED.
- **Не срабатывает** для старой сессии, вытесненной SESSION_REPLACED:
  `release` вернёт false. Это не дыра: вытеснение означает, что новая сессия
  уже заняла слот, и её Hello сверит шаги.
- Существующий слушатель-образец — `DuplicateSessionMarks`
  (`agents/DuplicateSessionMarks.kt`): `runCatching`, ошибка в лог.
  Слушатель, который бросает исключение, рвёт поток.

### Точки выставления срока (`least(coalesce(lost_deadline, :d), :d)`)

| Событие | Где | Шаги | Срок |
|---|---|---|---|
| сессия закончилась | новый `AgentSessionListener.disconnected` | агента, `dispatched`/`running` | конец + окно |
| старт сервера | новый `SmartLifecycle` до старта gRPC (см. В2) | все `dispatched`/`running` | старт + окно |
| Hello без шага | `onHello` | `running`, которых нет в Hello | Hello + окно |

`dispatched`, которого нет в Hello, по-прежнему отправляется повторно
(S6a). Успешная повторная отправка срок снимает.

### Точки снятия срока (`lost_deadline = null`)

| Событие | SQL |
|---|---|
| шаг в Hello | новый `update … where agent_id = :agent and id in (:listed)` |
| прогресс | `ACCEPT` и UPDATE в `StepProgressWrites` |
| результат | `FINISH` (после финала шаг уходит из частичного индекса, снятие — для ясности) |
| повторная отправка | `REDISPATCH` |

### Проверка

`tick`:
1. `system`: `select tenant_id, id from run_steps where status in
   ('dispatched','running') and lost_deadline <= :now`.
2. По каждому шагу в его тенанте — условный переход
   `… and status in ('dispatched','running') and lost_deadline <= :now`.
3. Запуск → `failed`, затем `announce`.

Гонка с результатом решается строкой: выигрывает тот, чьё условное
обновление прошло первым. Второй путь получает 0 строк (поздний результат —
`Late`, правила S7a). `LostStepWatch` удаляется.

### Черновик миграции `V202610041200__lost_deadline.sql`

```sql
alter table run_steps add column lost_deadline timestamptz;
create index run_steps_lost_deadline_idx on run_steps (lost_deadline)
    where status in ('dispatched', 'running') and lost_deadline is not null;
```

### Д5, Д6

- `RunSteps.of` (`DispatchParts.kt:43`) тегов не задаёт. Proto:
  `map<string, string> tags = 9` (`proto/sard/agent/v1/agent.proto`).
- В `StepView` нет `runId` (`runs/RunModel.kt`, класс `StepView`). Поле есть
  в `RunStepRecord.runId` и теряется в `RunViews.step`. Его нужно добавить.
- `sourceId` в `StepView` может быть null (action RUN). См. В5.
- Агент отклоняет пустой ключ и запятую в ключе или значении
  (`agent/internal/pluginhost/handler.go`, `resticTags`;
  `restic/backup.go`, `badTag`). UUID и `sard.step` допустимы.
- RunFinished: `RunFinishedPublisher` (`runs/RunFinished.kt`). Лог — на
  уровне файла (`private val log`), метрики — `Counter.builder(...)` за
  доменным интерфейсом, как `MicrometerDispatchMetrics`.

### Окружение

- `dockerd` запущен вручную, `postgres:18-alpine` скачался. Testcontainers
  работают.
- Базовый `./gradlew :server:test --offline` на чистом `main` — результат
  ниже, когда закончится.

## Вопросы владельцу

См. сессию. Ответы записать сюда.
