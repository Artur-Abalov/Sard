<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# J1 — правки по замечаниям architect (coder)

Дата: 2026-10-28.

## Что сделано
- `ArchitectureTest`: тест «every cross-tenant read of StepDeadlines is listed in ADR 0013» ищет функции `StepDeadlines.kt`
  с телом `= system(` (четыре: overdue, holders, waitsOver, silent) и требует `StepDeadlines.<имя>(` в ADR 0013. Красный
  прогон: «reads across tenants in [silent], not listed in ADR 0013»; затем `silent(now, window)` внесён в п. 6 ADR 0013.
- Черновик 00XX-run-cancel-deadlines: фраза о серии «ошибок подряд» (ДJ12: 1, а не 2), раздел J1e (ДJ10–ДJ12, поля
  `RunSummary`), статус (J1d, J1a, J1b, J1c, J1e; J1f ждёт V6), `cancelSent` заменён на `CancelSteps`, перечень правок
  по ADR 0038, 0022, 0024/0042, 0053, S9b Р7, R2 ВК39.
- Обратные ссылки «Изменён черновиком 00XX-run-cancel-deadlines (J1)» — строкой после «Дата» в ADR 0038, 0022, 0024, 0042,
  0053; в спецификациях S9b и R2 — комментарием в шапке, где они записывают поправки.

## Замечания
- Регулярное выражение теста исключает `=`, `{`, `}` между параметрами и `= system(`, иначе объявление в интерфейсе без тела
  захватило бы текст до первого `=` ниже.
