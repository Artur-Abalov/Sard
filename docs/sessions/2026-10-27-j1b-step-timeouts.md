<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# J1b — сроки шагов (coder)

Дата: 2026-10-27. Спецификация — `docs/specs/server/run-cancel-deadlines.feature` (сценарии `# J1b`), ADR —
черновик `docs/adr/00XX-draft-run-cancel-deadlines.md`, раздел «J1b». proto и агент не менялись.

## Что сделано
- Миграция `V202610271300__step_timeouts.sql`: `sources.backup_timeout_minutes`, `restore_timeout_minutes`;
  `run_steps.timeout_seconds`, `timed_out_by`; таблица `run_defaults` (по тенанту); частичный индекс молчащих шагов.
- `TimeoutSettings` (`GET/PUT /api/v1/run-defaults`, 360 и 720 минут до первой записи); поля срока у `Source` и
  `SourceInput`, проверка 1–1440 (`RunTimeouts.kt`); дробное число для целого поля отклоняется (yaml).
- Срок берётся при создании запуска (`Runs.create`, `Restores.create`), хранится в шаге, уходит в
  `RunStep.timeout` (`RunSteps.of`); шаг VERIFY — срок попытки проверки.
- Серверный срок: `StepDeadlines.silent`, `StepTransitions.timedOutBySilence`, `SilentSteps`, тик диспетчера;
  закрытому шагу онлайн-агент получает `CancelStep`.
- `timed_out_by = agent` при итоге `TIMED_OUT` агента; `server` — при закрытии сервером.
- Поздний итог обобщён: `lost`, `timed_out` от сервера, `cancelled` без подтверждения (`KEEP_LATE`,
  `late_status`, вывод, снимок); `lateStatus` теперь и у `lost`; `restoreOutcome` для позднего вывода.
- Уведомления: срок и две подсказки у `timed_out` (бэкап, восстановление; ru, en).
- REST: `RunStep.timeoutSeconds`, `timedOutBy`; `make openapi`; моки MSW (`run-defaults`, поля), словарь
  `StepTimedOutBy` (ru, en). Экраны — J1e.
- e2e `StepTimeoutTest` (хранилище SFTP пропало — `timed_out`).

## Решения по ходу
1. Серверный срок применяется только к `running` без срока потери: пропавший агент остаётся `lost`, а срок потери
   после старта сервера (J1d) защищает шаг до Hello. Отдельной «отсрочки» для серверного срока не нужно.
2. `spring.jackson.deserialization.accept-float-as-int: false` — иначе `1.5` минуты молча стало бы `1`.
3. Ограничения detekt решены выделением `SilentSteps`, `TimeoutSettingsConfiguration` и выносом функции из класса.

## Расхождения со спецификацией
- Р-J7 говорит «шаг ещё dispatched или running», но срок считается от `started_at` (ACCEPTED), у `dispatched` его
  нет: серверным сроком закрывается только `running`; `dispatched` ведут окно потери и повторная отправка.
- КВJ5: `timeoutSeconds` — `null` «только у старых записей», но у шага VERIFY он `null` и сейчас (срок проверки хранит
  задание, сценарий «срок шага проверки задаёт проверка» оставляет V4 как есть).
- Сценарий «Превышение срока по расписанию — ошибка серии» проверен на формулировке `Telling` и тесте
  заголовка по расписанию, отдельного интеграционного сценария с историей запусков нет.
- Тела запросов теперь отклоняют дробные числа для всех целых полей, не только для сроков.

## Открытое
- e2e `StepTimeoutTest` не запускался (образы не собраны, OQ-262); компилируется (`:e2e:compileTestKotlin`).
- Мутационное тестирование не запускалось (`gate.sh server fast` его не включает).
