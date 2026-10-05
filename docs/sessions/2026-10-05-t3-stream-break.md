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

## Решения владельца (фаза 1)

1. Односторонний обрыв — iptables во вспомогательном контейнере в сетевом
   пространстве агента. Образ не назван; выбран `nicolaka/netshoot:v0.14`
   (Apache-2.0, есть `iptables`; в облаке тянется через `mirror.gcr.io`),
   записан в `docs/dependencies.md`.
2. Случай 8: между перезапусками агента стирать журнал исполнителя
   вспомогательным контейнером на томе состояния (потеря диска хоста).
3. Окно потери — по классу: 7–9 — heartbeat 5 с, окно 10 с; 1, 2, 3, 6 —
   heartbeat 5 с × 20 = 100 с. Ненастраиваемый backoff агента — в реестр как
   находка.
4. Случай 1: обрыв дольше порога офлайна (15 с), возврат до конца окна.
5. 10 прогонов — здесь, обходом OQ-132; CI — черновым PR по разрешению.
6. Случай 7: повторный `POST …/runs` — 201, новый запуск в `queued`.
7. `t3-defects.md` и дубль OQ-133 поправить в этой задаче; старый журнал
   2026-10-04 — оставить как историю.

## Фаза 2 — случаи 1, 2, 3, 6

Стенд: `dockerd` поднят вручную; образы — обходом OQ-132 (jar сервера
`bootJar --offline` с хоста вместо build-стадии `deploy/server/Dockerfile`,
агент — `package-agent.sh` с `GO_TAGS=e2e`, базовые образы — `mirror.gcr.io`).
Базовый `SlowStreamTest` на этих образах зелёный.

### Что сделано

- `Interruptions.kt`: `cut`/`reconnect` (`docker network disconnect/connect`,
  алиас — имя хоста), `block`/`unblock` (iptables в пространстве агента,
  направление `TO_SERVER`/`TO_AGENT`), `restart`, `stop`.
- `ExactlyOnce.kt`: три счётчика — `restic started … command=backup` в логе
  агента; `restic snapshots --tag sard.step=<id>` и строки `snapshots`;
  `run <id> finished:` в логе сервера и `runs.finished_at`.
- `SardEnvironment`: `restartServer()` (ждёт health), `serverLogs()` — лог всех
  контейнеров сервера (до `recreateServer` сохраняется), порты хоста читаются
  из Docker при каждом обращении (рестарт может их сменить), логи упавшего
  теста — `docker logs` (consumer Testcontainers обрывается на рестарте).
- `StreamBreakTest`: случаи 1, 2, 3, 6; окно 100 с; `@Timeout` 5 мин на тест.
  Каждый тест — свой агент; кроме счётчиков проверяется, что агент принял шаг
  один раз (`step accepted`) и повторной команды не было, а сервер не писал
  `sent again`.

### Найдено на прогонах (проверено)

- Первый прогон: 1, 2, 3 — зелёные; 6 — красный: сервер не получил результат.
  Причина в стенде: правило iptables резало **все** пакеты сервера, включая
  голые TCP-ACK; без них агент отправил не больше окна перегрузки и встал,
  результат остался в буфере ядра (лог агента: `result sent` 10:52:46, затем
  `read: connection timed out`). Исправление — резать только сегменты с
  данными (PSH или длина > 80 байт). После него случай 6 зелёный (49 с).
- Лог сервера в артефактах упавшего теста обрывался на рестарте сервера
  (consumer Testcontainers не переподключается) — исправлено, см. выше.

### Проверка (реальные прогоны, обход OQ-132)

- `./gradlew :e2e:test --tests dev.sard.e2e.StreamBreakTest` **10 раз подряд**
  (скрипт с остановкой на первом сбое): 10/10, в каждом 4 теста, 0 падений.
  Время теста, мин–макс по 10 прогонам: случай 1 — 37,7–39,0 с; 2 —
  38,0–39,7 с; 3 — 38,6–52,8 с; 6 — 48,1–49,1 с.
- `license-check`: 722 files OK. Линтеров Kotlin у `:e2e` нет (spotless и
  detekt — только `:server`, `Makefile:95`); код компилируется.
- `make e2e` целиком и CI не запускались (OQ-132; CI — по черновому PR).

### Состояние

- Случаи 4, 5, 7, 8, 9 не написаны: FXa и FXs уже в `main`, отключать нечего,
  они пишутся сразу рабочими в фазе 3. `StreamBreakT3Pending` пока на месте.
- Находка для реестра (решение 3): backoff агента (1 с … 60 с, полный jitter)
  не настраивается; окно потери короче худшей задержки переподключения
  делает шаг `lost` после обычного перезапуска сервера. В реестр — в фазе 3
  вместе с правкой `t3-defects.md` и OQ-133.
