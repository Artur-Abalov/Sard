<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# J1d — отсрочка после старта сервера (coder)

Дата: 2026-10-27. Спецификация — `docs/specs/server/run-cancel-deadlines.feature`
(сценарии `# J1d`). ADR — черновик `docs/adr/00XX-draft-run-cancel-deadlines.md`.

## Что сделано
- `sard.run.dispatch.start-grace` (`SARD_RUN_START_GRACE`, по умолчанию 2 мин) в
  `DispatchProperties`/`DispatchSettings`; не положительное значение или не длительность —
  сервер не стартует, сообщение называет настройку.
- `StepDispatcher.onStart`: срок потери `старт + max(окно, отсрочка)`.
- Тесты: `StepDispatcherTest` (вернулся в пределах отсрочки, не вернулся, отсрочка короче
  окна), `DispatchPartsTest` (настройка, отказ, привязка), `DispatchIntegrationTest`
  (вернулся на 110 с, регрессия простоя сервера 3 ч — запуск принимает итог).
- e2e: `ServerRestartGraceTest` (перезапуск сервера посреди бэкапа, окно 10 с, шаг не `lost`);
  `StepLossTest` получил `SARD_RUN_START_GRACE` = окно (его случай 9 мерит окно от старта).
- Документы: `application.yaml`, `docs/operator/03-configuration.md`, OQ-138 закрыт.

## Решения по ходу
1. Сценарии «Отсрочка короче окна…» и «Недопустимая отсрочка…» проверены тестами
   настроек (`DispatchProperties.settings`, `Binder`), а не запуском приложения.
2. Конец сессии внутри отсрочки не менялся (см. «Не решено» в ADR).

## Открытое
- e2e не запускался в этом окружении: образы `sard-server:e2e` и `sard-agent-stand:e2e`
  не собраны; тест компилируется (`:e2e:compileTestKotlin`).
- Сессия агента, оборвавшаяся внутри отсрочки, получает срок раньше её конца (ADR, «Не решено»).
