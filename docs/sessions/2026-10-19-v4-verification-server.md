<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-19 — V4: сервер проверки восстановления (очередь, расписание, выдача)

Ветка `ccr-44680043-8al2ss`: её назначила сессия. В задаче указана
`feat/v4-verification-server`, переименовать можно по решению владельца. Это
продуктовая возможность: после контрольной точки 1 работа идёт по
`/ship-feature` (specifier → coder → cleaner → architect → hardener).

## Фаза 1 — исследование

«Проверил» — видел в коде (file:line). «Полагаю» — не проверено.

### Чего нет в репозитории (проверил)

- `sard-restore-verification.md` и `docs/spikes/v0-restore-verification.md`:
  нет ни в дереве, ни в истории (`git log --all -- '*restore-verification*'`
  пусто). Копию в `docs/design/` положить нечем. Цифры V0 (запас диска,
  `restic check` с учёткой только на чтение) учесть нельзя — параметры
  остаются настраиваемыми, значения по умолчанию ниже помечены как мои.
- Таблицы `restore_verifications` нет; есть только цель её FK —
  `snapshots (tenant_id, id, source_id)` (`V202609301800__results.sql:65-66`).
- На агенте нет измерения свободного места и определения Docker/Podman;
  `agent/internal/sandbox/sandbox.go` — заглушка (`ErrNotImplemented`).

### Что есть (проверил)

| Тема | Факт | Где |
|---|---|---|
| `ACTION_VERIFY` | `= 3`, сервер маппит его в обе стороны; VERIFY-шаги нигде не создаются | `agent.proto:379`; `DispatchParts.kt:68`; `RegisterRequests.kt:21` |
| Ключи верификатора | Register уже несёт `RepositoryInfo.repository_id` (restic id, не секрет) | `agent.proto:418-429`; `app/app.go:87-99` |
| Плагины проверки | `Plugin.actions` содержит VERIFY, только если плагин реализует `sdk.Verifier`; встроенные (files, postgresql) — нет | `pluginhost/handler.go:67-72`; `sdk/plugin.go:135-140` |
| Исполнение VERIFY сегодня | restore в `<state_dir>/restore/<command_id>` → `Verify` → `VerifyOutput{snapshot_id, checks}` с одной проверкой | `pluginhost/handler.go:163-182` |
| `VerifyOutput` | `snapshot_id=1, checks=2`; стадий и уровня нет | `agent.proto:326-338` |
| N−1 | Незнакомое сообщение сервера агент молча пропускает; незнакомое действие — `REJECTED` | `transport.go:362-377`; `executor/command.go:128-145` |
| Роль | `AgentRole.allows`: verifier — только VERIFY; бэкап на verifier отвергается во всех слоях | `RunModel.kt:94-109`; `AgentOffer.kt:314-356`; `StepDispatcher.kt:195,264` |
| Машина | `agents.machine_id` (64 hex), `MachineAgents.withMachineId` — без вызовов, оставлен для V4 | `V202610141200__agent_role.sql:20-22`; `fleet/MachineAgents.kt` |
| Выбор агента | Нет: `run_steps.agent_id` = `source.agentId` при создании запуска | `runs/Runs.kt:204` |
| D6 | `runs_active_source_key ON runs (tenant_id, source_id) WHERE status IN ('queued','dispatched','running')` | `V202609301200__runs.sql:62-63` |
| Триггер | `Trigger.VERIFICATION` уже есть, уведомления о таких запусках отключены | `RunModel.kt:66`; `notify/Notifications.kt:81` |
| Снимок | `snapshots`: `snapshot_id, repository_id, total_bytes, agent_id, partial, forgotten_at` | `V202609301800__results.sql:51-73` |
| Потеря шага | `lost_deadline`, `StepDispatcher.tick` → `LOST`; окно = 2 × heartbeat | `StepTransitions.kt:39`; `DispatchConfiguration.kt:44-58` |
| Расписание | `Scheduler`: `for update skip locked`, `Clock` внедряется | `scheduler/Scheduler.kt:29-31,122` |
| Уведомления | Нет шины: планировщик над таблицами-субъектами; эпизоды с частичным уникальным индексом «один открытый» | `notify/Deliveries.kt`; `V202610121200__source_freshness.sql` |
| `lastVerifiedAt()` | Единственный потребитель — `GET /status` (`lastVerifiedRestoreAt`); в источнике и обзоре поля нет | `api/StatusController.kt:26,34,39` |
| ADR 0013 | `restore_verifications`: snapshot/source FK, step, verifier, `key_holders`, `status IN ('passed','failed')`, `checks`, `verified_at` | `0013-multitenancy-and-schema.md:195-204` |
| e2e | Настоящие агенты в контейнерах; стенд-образ с тегом `e2e` и плагином `e2e-slow` | `test/e2e/.../T3Agent.kt`; ADR 0036 |

Полагаю: до V5 в боевой сборке ни один агент не объявит плагин с VERIFY, и
каждая проверка будет заканчиваться «нет верификатора: нет плагина». Это
честное состояние, но его стоит показать явно (вопрос 3).

### Предложение к контрольной точке 1

Подробности — в ответе владельцу в сессии. Кратко:

- Доставка через существующие `runs`/`run_steps` (`trigger = verification`,
  шаг VERIFY на верификаторе): dispatch S6a, прогресс, результаты S7a и потеря
  FXs переиспользуются. `runs_active_source_key` пересоздаётся с
  `AND trigger <> 'verification'`, чтобы проверка не блокировала бэкап (D6).
- Две таблицы: `verification_jobs` (очередь, одно открытое на источник) и
  `restore_verifications` (история, статусы V-D10).
- Расписание цикла — своя таблица на тенант, SKIP LOCKED как у F3a.
- proto: `RegisterRequest.verifier = 11`, `Heartbeat.verifier = 3`,
  `StepProgress.stage = 8`, `VerifyOutput.stages/level/verdict/...`.

Решения владельца внесу сюда после ревью.

### Решение владельца 1 (2026-10-19)

1. Доставка — через `runs`/`run_steps` (`trigger = verification`, шаг VERIFY на
   верификаторе). `runs_active_source_key` пересоздаётся с
   `AND trigger <> 'verification'`; проверки исключаются из свежести (F6) и из
   истории бэкапов в REST.
2. Перечень уровней и дизайн-документ владелец приложил к сообщению, но в
   контейнер файл не попал (`/mnt/user-data/uploads` пуст, в репозитории и
   `origin/main` его нет). Запрошен повторно; до него контракт стадий не
   фиксируется.
3. e2e для V4 не делается: без исполнителя V5 он ничего не доказывает. Пункт 10
   стратегии проверки снят; поведение проверяется серверными тестами с
   фейковым агентом на стриме (`StreamIntegrationSupport`).
4. Всё, что зависит от V5, — заглушка: агент сообщает свободное место и
   песочницу как «неизвестно», `pluginhost` не меняется. Неизвестное место не
   исключает верификатор, а идёт в причины как предупреждение.
5. `/status` — самая старая из последних успешных проверок по источникам с
   включённой проверкой; в DTO источника — `lastVerifiedAt`,
   `lastVerifiedLevel`, `verificationStatus`.
6. Доставка уведомлений откладывается до V6. Полагаю, что субъекты событий
   (пометка перехода в записи проверки, эпизоды «не проверялся N дней»)
   остаются в V4 — уточняю у владельца.
7. Значения по умолчанию приняты: проверка включена; цикл 04:30 по поясу
   установки, срок задания — до следующего цикла; таймаут шага 6 ч; место =
   `total_bytes` × 2 + 1 ГиБ; N = 7 дней; одна проверка на верификатор, выбор —
   меньше проверок за 24 ч, затем id; `config_json` источника уходит как есть.
