<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# J1a — отмена запуска (coder)

Дата: 2026-10-27. Спецификация — `docs/specs/server/run-cancel-deadlines.feature` (сценарии
`# J1a`), консоль — `docs/specs/web/run-cancel-deadlines.feature` (только то, что нужно сервером
контракту). ADR — черновик `docs/adr/00XX-draft-run-cancel-deadlines.md`, раздел «J1a».
proto и агент не менялись.

## Что сделано
- Миграция `V202610271200__run_cancel.sql`: `runs.cancel_requested_at/by`, `cancel_result`;
  `run_steps.late_status`; `run_steps_dispatched_check` допускает `cancelled` без отправки.
- `RunCancels` (`POST /api/v1/runs/{runId}/cancel`): очередь закрывается условным UPDATE, запуск в
  полёте получает отметку; повторная отмена и гонки дают одну запись; 404, 409 `run_finished`,
  409 `run_not_cancellable` (проверка).
- `StepDispatcher.onCancelled`, тик и Hello шлют `CancelStep` (`CancelSteps`, по разу за сессию),
  отменяемый шаг агенту повторно не отправляется; `release` отменяемого шага закрывает его.
- `StepTransitions.lost`: срок потери отменяемого шага даёт `cancelled` «the agent did not confirm
  the cancellation» (`cancel_unconfirmed`); `RUN_FINISHED` пишет `cancel_result`.
- `StepResults`: поздний итог неподтверждённой отмены хранится один раз (`late_status`, вывод,
  снимок по ДJ1: `SUCCEEDED` — полный, `CANCELLED` — `partial`).
- Уведомления: отмена человеком не уведомляет (`Telling.of`, `RunNotice.cancelRequested`).
- REST: `Run`/`RunSummary.outcome`, `Run.cancellation`, `RunStep.lateStatus`, `ErrorCode` +
  `run_finished`, `run_not_cancellable`; `restoreOutcome late` и для отменённого шага с поздним выводом.
- Веб: `make openapi`, `errors.ts`, словари `RunOutcome` и `CancelResult` (ru, en), моки (`outcome`,
  `cancellation`, `lateStatus`, обработчик отмены), тест словарей. Экраны — J1e.
- e2e `RunCancelTest` (отмена медленного бэкапа и восстановления по REST).

## Решения по ходу
1. Отметка отмены — колонки `runs`, не статус (Р-J1); итог отмены пишет тот же UPDATE запуска.
2. Потеря и неподтверждённая отмена — два взаимоисключающих условных UPDATE; `lost()` пробует
   неподтверждённую отмену повторно, иначе отметка между попытками ждала бы следующего тика
   (найдено тестом гонки отмены со сроком потери).
3. Отправленные `CancelStep` помнит память диспетчера, а не база: Hello и конец сессии сбрасывают.
4. Запуск проверки — 409 `run_not_cancellable` всегда (до J1f), в том числе завершённый.
5. Отзыв агента при отменяемом запуске — `lost` «agent revoked» и `cancel_result = finished_first`.
6. Ограничения detekt (число функций класса, возвраты) решены выделением `CancelSteps`,
   `RunExceptionHandler`, `CancelMapping`, а не подавлением.

## Расхождения со спецификацией
- Сценарий «Отмена проверки… 409 run_not_cancellable» говорит о запуске в `running`; для завершённой
  проверки ответ не определён (выбран тот же 409).
- `cancel_result` при отзыве агента спецификация не называет (выбран `finished_first`).
- КВJ5 `lateStatus` целиком — J1b; здесь поле заполняется только у шага, закрытого неподтверждённой
  отменой, у `lost` оно пустое.
- Сценарий «Отмена в гонке с отправкой» проверен на уровне переходов (`release` отменяемого шага) и
  порядка `RunStep` → `CancelStep` под блокировкой агента, а не гонкой двух потоков через gRPC.

## Открытое
- e2e `RunCancelTest` не запускался: образы `sard-server:e2e` и `sard-agent-stand:e2e` не собраны
  (OQ-262); компилируется (`:e2e:compileTestKotlin`). Ручная проверка `docs/qa/run-cancel-deadlines.md`,
  часть 2, не выполнялась.
- Мутационное тестирование не запускалось (`gate.sh server fast` его не включает); тесты размечены
  `@MutFlowTest` там, где проверяется решение.
