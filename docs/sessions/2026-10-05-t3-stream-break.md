# 2026-10-05 — T3: обрывы связи и перезапуски посреди бэкапа (e2e)

Ветка `ccr-91f4cc3b-b4qp6b` (назначена окружением; в постановке —
`feat/t3-stream-break`). База — `main` @ `585a513`: FXs, FXa и T3s влиты.
Предыдущая попытка — `2026-10-04-t3-stream-break.md` (фаза 1 до FXs/FXa/T3s);
её выводы о счётчиках и замедлении устарели.

## Фаза 1 — исследование (ждёт ревью)

Проверено чтением кода (file:line). Docker и тесты не запускались:
`dockerd` есть, сокета нет; `make e2e` в облаке упирается в OQ-132,
обход из T3s (`2026-10-04-t3s-stand.md`, «Проверка») сработал.

### Что уже есть на стенде

- Тома PKI и PostgreSQL, `SardEnvironment.recreateServer()` = stop + start
  того же контейнера (`SardEnvironment.kt:136-137,151-154`). Окружение сервера —
  `SardEnvironment(serverEnv)` (`:48-49`).
- Медленный шаг: плагин `e2e-slow` (`agent/plugins/e2eslow`, тег `e2e`),
  конфиг `size/rate/chunk/seed`; образец — `SlowStreamTest.kt:328-387`
  (24 МиБ при 1 МиБ/с ≈ 24 с). Запуск — `Backups.start(..., plugin = "e2e-slow")`.
- Ожидание — `Await.until/value` (опрос 500 мс, по умолчанию 60 с).
- Перезапуск агента — inline `restartContainerCmd` (`ResultAckSeamTest.kt:45`).
  Помощников обрыва сети, остановки агента, счётчиков нет. Таймаута на
  тест нет (`test/e2e/build.gradle.kts`, `@Timeout` нигде нет).

### Источники трёх счётчиков

| Счётчик | Источник | Где |
|---|---|---|
| запуски `restic backup` на command_id | лог агента: `restic started command=backup command_id=…` и `restic exited … exit_code=…` (текстовый slog в stderr) | `agent/internal/restic/restic.go:136-139,215,242` |
| снимки шага | `restic snapshots --json --tag sard.step=<command_id>` в контейнере агента (`AgentHost.run`) + строка `snapshots` по `step_id` | `DispatchParts.kt:47-61`, `handler.go:95,129` |
| RunFinished | лог сервера `run {} finished: {}` + `runs.finished_at` | `runs/RunFinishedTrace.kt:23` |

Вспомогательные строки: агент — `step accepted`, `step started`,
`step finished`, `repeated command answer=…`, `interrupted steps reported as
failed`, `connected to the server`, `connection to the server lost`,
`reconnecting`, `hello sent running=… pending_results=…`, `result sent`,
`result acknowledged` (`command.go:101,153-156,179,331`, `executor.go:341`,
`transport.go:189,207,326-327,358,416`). Сервер — `step {} sent again to
agent {}` (`StepDispatcher.kt:156`), `step {} lost: its agent did not report
it` (`:98`), `result for lost step {}` (`StepResultReceiver.kt:96`),
`agent {} repeated the result of step {}` (`:114`, только debug).

Лог сервера переживает `docker restart`, но не пересоздание: Testcontainers
удаляет контейнер. Помощник должен сохранять лог перед `recreateServer`.

### Механика сервера (FXs) и агента (FXa)

- Срок потери — `run_steps.lost_deadline`, сохраняется самый ранний
  (`StepDeadlines.kt:18`). Ставится при конце сессии (кроме
  SERVER_SHUTTING_DOWN), при Hello без `running`-шага, при старте сервера
  (заменяет, В2) (`StepDispatcher.kt:60-93`). Снимается прогрессом,
  результатом, повторной отправкой (`StepTransitions.kt:22-37`).
- `dispatched` → `running` — первый же StepProgress, а агент шлёт ACCEPTED
  сразу при приёме (`StepTransitions.kt:29-31`, `command.go:102`). Журнал
  пишется до ACCEPTED.
- Агент после рестарта сразу отдаёт каждую запись журнала как FAILED D13
  (`executor.go:333-341`).
- Агент: backoff 1 с × 2 до 60 с, полный jitter, не настраивается
  (`transport.go:67-71`); keepalive 30 + 10 с (`:79-81`). Сервер: офлайн после
  3 heartbeat, окно потери 2 heartbeat (`application.yaml:58-73`).

### Способы прерывания (предложение)

| Случай | Действие |
|---|---|
| 1 | `docker network disconnect/connect` агента |
| 2 | `docker restart` сервера |
| 3 | `recreateServer()` |
| 4, 8 | `docker restart` агента |
| 5а (в журнале) | блокировка только направления агент → сервер (см. вопрос 1) |
| 5б (не в журнале) | отключить сеть агента, запустить шаг, пока сессия на сервере ещё жива (RunStep уходит в мёртвое соединение), рестарт агента |
| 6 | блокировка только направления сервер → агент: результат доходит, ResultAck нет; через keepalive агент переподключается и повторяет |
| 7, 9 | `docker stop` агента (+ `docker restart` сервера для 9) |

### Найдено по ходу

- `docs/qa/t3-defects.md`: Д1, Д2, Д5, Д6 исправлены FXs, но не помечены
  закрытыми; С1 и С3 сняты T3s.
- `docs/open-questions.md`: номер OQ-133 занят дважды (открытый пункт про
  блокировку restic и закрытый Д1 в разделе FXs).
- `agent/plugins/e2eslow/plugin.go:7` ссылается на несуществующий
  `plugins/builtin_e2e.go` (файл называется `stand_e2e.go`).
- CI запускается только на `push` в `main` и на `pull_request`
  (`ci.yml:10-19`): без PR job e2e на ветке не идёт.

## Вопросы владельцу

Заданы в сессии; решения — сюда после ревью.
