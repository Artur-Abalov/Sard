# 2026-10-04 — T3: обрыв связи посреди бэкапа (e2e)

Задача: на настоящих контейнерах показать, что обрыв сети, перезапуск сервера
и перезапуск агента посреди бэкапа не дают ни повторного бэкапа, ни
потерянного или задвоенного результата. Поведение агента и сервера не
меняется; дефекты — в `docs/open-questions.md`.

## Фаза 1 — исследование (ждёт ревью)

Проверено чтением кода (file:line), Docker и тесты не запускались.

### Предпосылки

- S7a в `main` (`fd4c31f`, `0e530e2`). T2b в `main` нет: `FullChainT2Pending`
  ещё в `Pending.kt`, готовых помощников полной цепочки нет.
- Шаг, который реально делает бэкап, есть: плагин `files` отдаёт restic пути
  (`agent/plugins/files/plugin.go`, `Dump` → `sdk.Dump{Paths: ...}`).
- Запуск через REST: `POST /api/v1/sources` и `POST /api/v1/sources/{id}/runs`
  (`server/.../api/SourcesController.kt`). Вход —
  `POST /api/v1/session` с паролем администратора, как в `EnrollmentTokens.kt:37`.
- Docker в облачной среде не запущен (`/usr/bin/dockerd` есть). `make e2e`
  здесь падает на 429 Maven Central (OQ-132). В A7b это обошли вручную:
  jar собирали на хосте (`docs/sessions/2026-10-02-a7b-log-masking.md:71`).

### Как замедлить бэкап

- У restic нет `--limit-upload` и дополнительных аргументов: командная строка
  фиксирована (`agent/internal/restic/backup.go:321-335`), `RESTIC_*` в
  `env_file` запрещены (`restic/env.go:83-87`). Агент не меняем, так что
  остаётся только **объём данных**.
- Предложение: в контейнере агента создаётся несжимаемый файл (случайные
  байты, порядка сотен МБ, подобрать по времени на CI) в пути источника.
  Хранилище — локальный путь restic в томе агента. Перед действием тест
  ждёт фазы UPLOADING, читая `run_steps.phase`/`bytes_processed`.
- Риск: на быстром диске бэкап может кончиться раньше, чем случится обрыв.
  Поэтому каждый случай проверяет, что действие пришлось на шаг в
  `running`: если шаг завершился до обрыва, тест падает с понятным
  сообщением и не проходит молча.

### Как прерывать

- Сеть, случаи 1 и 4: `docker network disconnect/connect` контейнера агента.
  Новых зависимостей не нужно. Toxiproxy в проекте нет
  (`test/e2e/build.gradle.kts:35-36`, `docs/dependencies.md:73`). Ему нужен
  алиас из `SARD_PKI_SERVER_NAMES` (агент сверяет ServerName,
  `transport.go:126-130`), а это уже новая зависимость с записью в
  `docs/dependencies.md`.
- Перезапуск сервера, случай 2: только `docker restart` того же контейнера.
  CA лежит в файловой системе контейнера (`SARD_PKI_DIR=/var/lib/sard/pki`,
  `application.yaml:47`). Если контейнер пересоздать, появится новый CA, и
  агент больше не подключится. Реестр сессий в памяти (S5a), RunFinished
  тоже в памяти (`runs/RunFinished.kt:20-57`).
- Перезапуск агента, случай 3: `docker restart` контейнера агента, каталог
  состояния executor сохраняется.

### Тайминги стенда

- `SARD_AGENT_HEARTBEAT_INTERVAL` по умолчанию 30 с (`application.yaml:53`).
  Окно потери = heartbeat × `sard.run.dispatch.lost-after-heartbeats` (2)
  (`DispatchConfiguration.kt:53`). Проверка — `sard.agent.stream.check-interval`,
  30 с. Офлайн — после 3 heartbeat.
- Для e2e предлагается heartbeat 5 с и check-interval 1 с, то есть окно
  потери 10 с. Переопределение — через `SardEnvironment(serverEnv)`, как в
  `ResultAckSeamTest.kt:201-205`.
- Агент: переподключение 1 с ×2 до 60 с, полный джиттер, сброс после
  потока, прожившего 30 с (`transport.go:67-71,204-210,173-175`).
  Обрыв короче окна потери (случай 1) должен укладываться в окно вместе с
  джиттером, поэтому длительность обрыва надо выбирать с запасом.

### Источники счётчиков «ровно один»

| Что | Источник | Где |
|---|---|---|
| запись результата | `run_steps.status/finished_at` по `id = command_id`; guarded update `status in ('dispatched','running')` | `StepTransitions.kt:30-32`, `StepResults.kt:62-106` |
| снимок | `snapshots` по `step_id`, UNIQUE(tenant_id, step_id) | `V202609301800__results.sql:157-178` |
| повторная отправка шага | лог сервера `step {} sent again to agent {}` | `StepDispatcher.kt:122-126` |
| повтор результата | лог сервера debug `agent {} repeated the result of step {}` | `StepResultReceiver.kt:114` |
| поздний результат | лог сервера warn `result for lost step {}` | `StepResultReceiver.kt:96` |
| lost | лог сервера warn `step {} lost`, `runs.status = failed` | `StepDispatcher.kt:67`, `RunModel.kt:33` |
| RunFinished | **таблицы событий нет**, только в памяти | `RunFinished.kt` |
| запуск restic backup | **в логе агента нет**, stderr restic попадает только в `step_logs` | `restic.go:58,116`, `main.go:131-138,237-242` |
| ResultAck | лога нет. На диске агента `results/<sha>.json` переходит в `acked/<sha>.json` | `ResultAckSeamTest.kt:109-146` |

Сервер не передаёт теги шага (`putTags` нет), поэтому в restic-снимке нет
`command_id` (`pluginhost/handler.go:94,128`). Число *завершённых* запусков
restic можно посчитать как число снимков в репозитории (`restic snapshots
--json` из отдельного контейнера restic на томе агента: образ агента
distroless). Прерванный второй запуск снимка не оставляет.

Счётчиков, которых задача требует «по БД и по журналу агента», для двух
пунктов в коде нет. Об этом вопросы 1 и 2.

## Вопросы владельцу

См. ответ в сессии. Решения записать сюда после ревью.
