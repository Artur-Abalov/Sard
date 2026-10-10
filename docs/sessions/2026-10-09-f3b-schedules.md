<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-09 — F3b: расписания бэкапов (coder)

Ветка `claude/exciting-hypatia-trmvax`. Продукт идёт по конвейеру `/ship-feature`:
спецификация утверждена владельцем (`docs/specs/server/schedules.feature`,
`docs/specs/server/schedule-notifications.feature`, `docs/specs/web/schedules.feature`,
`docs/qa/schedules.md`). Роль — coder: реализация по утверждённым сценариям.

## Сервер

- **К0.** `CronSchedule.parse` отклоняет нормализованный cron длиннее 200 символов
  (`InvalidSchedule`, поле `cron`, «cron is longer than 200 characters», значение не
  повторяется): 422 в PUT расписания и в предпросмотре вместо 503 «база недоступна».
- **К1.** `GET /api/v1/schedule-preview`: `SchedulePreviews` (пояс сервера — JVM, смещение
  читается как UTC), `ScheduleDescription` (таблица, остальное — «Особое расписание»),
  «слишком часто» — два из ближайших 100 срабатываний ближе 15 минут.
- **К2/К3.** `Schedule.lastRun`, `notifyOnSuccess` (миграция `V202610101200`); смена только
  `notifyOnSuccess` `nextRunAt` не сдвигает.
- **К5/К6.** `catchUp` у запусков и `missedCountCapped` у строк журнала. Связь простоя с
  догоняющим запуском — явная колонка `schedule_fires.catch_up_fire_id` (ADR 0055): порядок по
  `recordedAt` не годится, у строки простоя и срабатывания догоняющего бывает один момент.
- **Уведомления.** Строка триггера, «Бэкап снова работает» с числом ошибок подряд,
  `notifyOnSuccess` (читается при отправке), алерт о пропусках через ту же очередь
  (`notification_deliveries.fire_id`, миграция `V202610101300`, ADR 0055).
- **К7.** `TZ: ${TZ:-UTC}` в `deploy/docker-compose.yml`, строка в `deploy/.env.example`.

## Консоль

- Блок «Расписание» на карточке источника (`ScheduleBlock`): расписание словами (от сервера),
  следующий и последний запуск, догоняющий запуск, пропуски подряд, предупреждение о частоте,
  журнал со ссылками; редактор на месте (`ScheduleEditor`: «Сохранить» — один PUT, «Отмена» —
  без запросов), предпросмотр с паузой 300 мс и языком консоли.
- Чистые функции: `schedule.ts` (вариант ↔ cron, поле ошибки), `format.ts` (время в поясе
  расписания, склонение, период простоя), `polling.ts` (опрос по `lastRun`).
- Колонка «Запущен» в списке запусков и на обзоре, период простоя на карточке запуска.
- Моки: предпросмотр по фикстурам сервера, проверка длины cron, `notifyOnSuccess`,
  фикстуры состояний F3b (`web/src/mocks/README.md`).
- `src/api/schema.d.ts` и `openapi.json` получены через `make openapi`.

## Решения и отклонения

- Хранение доставки алерта — обобщение `notification_deliveries` (ADR 0055).
- Признак «счёт обрезан» хранится в строке, а не выводится из `missedCount == 10000`:
  ровно 10 000 пропусков без продолжения обрезанными не считаются.
- Если пояс не выбран, а предпросмотр недоступен, консоль ничего не отправляет: кнопка
  «Сохранить» отключена (`inputOf` возвращает null). Пояс по умолчанию — серверный, консоль
  его не выбирает (исправлено после architect, находка 4; прежнее отклонение «UTC» снято).
- Мутационное тестирование сервера (полный гейт) не запускалось: критерий — `fast`.

## Cleaner pass

- Server: `Schedules.replace` split into `changes` and `reschedule` (CRAP 6.0 -> below the list's top); the three
  `closed*` functions of `Deliveries` became one `Closing.of`; `unplanned`/`unplannedAlerts` and
  `plan`/`planAlerts` share one query helper each; both notice formatters use `Message.ofLines`; `Notices`
  builds the previous-statuses query once; unused imports of `Deliveries.kt` removed.
- Web: `ScheduleFields` (8 -> 6), `SaveError` (8 -> 6) and `ScheduleSummary` (8 -> 5) decomposed; the mock PUT
  handler lost its inline storage code (`save`).
- Noted, not changed: `Verdicts.failure` and `ScheduleDescription.stepOf` stay at CRAP 5.x (source-level, well covered).
- Behavior unchanged; `gate.sh server fast` and `gate.sh web fast` pass. Next: architect.

## Architect must-fix pass

- Находка 1: `runs/CatchUps.kt` удалён; период читает `scheduler/CatchUpPeriods` (пачкой, только `run_created`),
  `CatchUpPeriod` переехал в модель планировщика, `RunView.catchUp` убран, `RunsApiImpl` собирает периоды и отдаёт
  их `RunMapping`. Тест A (`ArchitectureTest`) и тест B (`SchedulerIntegrationTest`: активный догоняющий запуск и
  поздний догоняющий, пропущенный на нём) написаны первыми; тест B до исправления падал `DataException` (подзапрос
  вернул две строки). REST-контракт не менялся.
- Находка 4: `inputOf` возвращает `ScheduleInput | null`, «Сохранить» отключена без пояса.
- Находка 5: пять сценариев `schedule-notifications.feature` покрыты в `ScheduleNotificationsIntegrationTest`;
  все прошли без правок кода.
- Находка 3: `Deliveries.unplannedAlerts` занесён в ADR 0013, п. 5, и в ADR 0055.
- Р15 (решение владельца, c7df8a3): `schedules.catch_up_owed_since`, `CLAIM` берёт строки с `recorded_at >= since`;
  миграция V202610101200 исправлена на месте (`CatchUpClaimMigrationTest` гоняет Flyway по схеме до и после).
  Ограничение переноса F3a записано в ADR 0055.

## Cleaner pass

- Область: `git diff d688e20..HEAD`. `./scripts/crap.sh server`: максимум CRAP 6.0 (порог 6); затронутые функции
  (`Firing.catchUp` 5.0, `Firing.journal` 5.0, `RunsApiImpl.listRuns` 5.0, `CatchUpPeriods.periods` 2.0, `RunMapping.*` 1.0-3.0)
  при покрытии 100%, разложение не нужно. Кода не менял; веб (`inputOf`, `ScheduleEditor`) без находок.
- `gate.sh server fast` и `gate.sh web fast`: PASSED.

## Повторная проверка архитектора (две обязательные правки)

- Миграция V202610101200 (на месте): простой F3a принадлежит первому догоняющему, записанному строго после него
  (`c.recorded_at > d.recorded_at`): в одном тике догоняющий пишется раньше простоя, поэтому простой того же момента
  принадлежит следующему. Комментарий исправлен. Два новых теста `CatchUpClaimMigrationTest` до правки падали.
- Сценарий «Изменение расписания отменяет назначенный догоняющий» (три примера) покрыт в `SchedulerIntegrationTest`
  общим телом `pendingCatchUpCancelledBy`; код не менялся, тесты прошли сразу.
- Убрано лишнее `val run` в `Runs.listRuns`; в ADR 0055 (Р15) записано, что догоняющий, пропущенный на идущем запуске,
  тоже забирает свои простои и они не попадают в период ни одного запуска.
- Cleaner: проверка коммита 8737295 без изменений кода (CRAP затронутых функций не выше 3.0, `gate.sh server fast` зелёный).

## Hardener (мутационная проверка)

- До: `-Pmutflow.enabled=true :server:test --rerun` на ветке F3b: 26 выживших (`CronScheduleTest` 1: граница длины
  cron 200 -> 201; `DueTest` 2: `<=` и `0` в признаке «обрезано» при следующем срабатывании ровно в `now`;
  `ScheduleDescriptionTest` 1: `<` в `number` (минута 60); `SchedulePreviewsTest` 22: мутанты `CronSchedule` и
  `ScheduleDescription`, достигнутые через превью и уже убитые их собственными тестами). После: 0.
- Тесты, не код: `CronScheduleTest` (201 символ), `DueTest` (следующее срабатывание ровно в `now` за пределом счёта),
  `ScheduleDescriptionTest` (`60 * * * *`, `0 24 * * *`). `SchedulePreviewsTest` получил
  `includeTargets = [SchedulePreviews::class]`, как `SchedulerIntegrationTest` (ADR 0006): cron и слова мутируются в
  своих тестах, превью проверяет только своё.
- Под mutflow приведён код F3b, который раньше проверялся только интеграционными тестами без `MutFlow.underTest`:
  `Deliveries` и `Notices` (в `ScheduleNotificationsIntegrationTest`, плюс `NotificationService`: тест аренды
  оборванной отправки и записи в журнал), `CatchUpPeriods` и `Schedules.fires` (в `SchedulerIntegrationTest`, новый тест
  журнала с постраничным чтением), `SchedulePreviewApiImpl`, `SchedulesApiImpl`, `RunsApiImpl` (новые
  `SchedulePreviewApiImplTest`, `SchedulesApiImplTest`, `RunsApiImplTest`), `RunMapping.catchUp` (`RunMappingTest`).
- Не под mutflow остались классы без исполняемой логики или с логикой, которую mutflow не мутирует: DTO контроллеров
  (`SchedulesController`, `RunsController`, `SchedulePreviewController` кроме `SchedulePreviewApiImpl`),
  `ScheduleRecords`, `NotificationDeliveryRecord`, `ScheduleModel`; `RunsApiImpl.listStepLogs` F3b не менялся.
- Исключений и подавлений нет; порог не менялся; продакшен-код не менялся.
