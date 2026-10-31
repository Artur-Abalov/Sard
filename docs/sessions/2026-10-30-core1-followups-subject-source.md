<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# CORE1 (SARD-7) — доработки: SubjectSource, контроллеры ядра, run.started, ABI (coder)

Дата: 2026-10-30. Спецификация `docs/specs/server/server-core-extensions.feature` (редакция 4, Р26, Р27, Р29–Р31,
Р33), ADR 0070.

## Сделано
1. Публичный шов `SubjectSource` (`extension`), реализация ядра `CoreSubjectSource`, автоконфигурация
   `SubjectSourceAutoConfiguration` с проверкой «одна реализация». `AccessPolicyInterceptor` берёт субъекта у шва
   (без сессии ядра — 401, `null` — 401, сбой — 403 и одна строка WARN). `ActorSource` остался внутренним адаптером
   `SubjectActors` (сбой шва — `actor` null, событие доставляется); `signout` и `run.created` берут `actor` у шва.
2. Проверка контроллеров: `ExtensionControllers` считает контроллером ядра класс из того же `CodeSource`, что и ядро;
   обработчик без пути — вне `/api/v1`.
3. `run.started` по появлению `started_at` (`RunEvents.Watch.started`).
4. `@InternalSardApi` и исключение из ABI-выгрузки; конструктор `ExtensionRegistry` — `internal`.
5. Образец: `sard.sample.subject`, `sard.sample.stray-controller`.

## Что узнали
- В Spring Framework 7 `RequestMappingInfo.pathPatternsCondition` может быть `null` только у условия, собранного
  через устаревший конструктор с `PatternsRequestCondition`; построителем `paths(...)` такого не получить.
- `PublicApiOnlyTest` образца ищет только строки `import dev.sard.server.*`; контроллер в пакете
  `dev.sard.server.sample` не импортирует ничего из ядра, поэтому тест не менялся.
