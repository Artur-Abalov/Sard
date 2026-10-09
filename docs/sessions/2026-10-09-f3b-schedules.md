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
  догоняющим запуском — явная колонка `schedule_fires.catch_up_fire_id` (ADR 0054): порядок по
  `recordedAt` не годится, у строки простоя и срабатывания догоняющего бывает один момент.
- **Уведомления.** Строка триггера, «Бэкап снова работает» с числом ошибок подряд,
  `notifyOnSuccess` (читается при отправке), алерт о пропусках через ту же очередь
  (`notification_deliveries.fire_id`, миграция `V202610101300`, ADR 0054).
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

- Хранение доставки алерта — обобщение `notification_deliveries` (ADR 0054).
- Признак «счёт обрезан» хранится в строке, а не выводится из `missedCount == 10000`:
  ровно 10 000 пропусков без продолжения обрезанными не считаются.
- Если пояс не выбран, а предпросмотр недоступен, консоль сохраняет расписание в UTC
  (PUT требует пояс; консоль не знает пояса сервера без предпросмотра).
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
