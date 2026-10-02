<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-02 — W2: страницы консоли этапа 1

Ветка: `claude/pensive-knuth-29ap8b`. База — `8365fa4` (спецификация W2 утверждена владельцем).
Спецификации: `docs/specs/server/rest-api-w2.feature` (К14–К16),
`docs/specs/web/console-pages.feature`, `docs/specs/web/agent-enrollment.feature` (Г8),
ручной QA — `docs/qa/console-pages.md`. Решения — `docs/open-questions.md` (OQ-072…OQ-084).
ADR: 0034.

## Что сделано
- **Сервер.** К16: `BackupOutput.partial` хранится в `run_steps.output` и ставится в `ResultCheck`
  по статусу результата. К15: `sourceName` и `sourceDeleted` у `RunSummary` и `Run` (имя из строки
  источника, мягко удалённый сохраняет имя). К14: `GET /api/v1/overview` — `fleet.Overviews`
  считает агентов и пять «первых шагов» при каждом запросе.
- **Контракт.** `make openapi` (тест `SardServerIntegrationTest` + `gen:api`) пересоздал
  `web/src/api/openapi.json` и `schema.d.ts`; руками они не правились.
- **Веб, чистые функции с Vitest.** `format.ts` (байты, процент, длительность, файлы),
  `polling.ts`, `errors.ts` (код → ключ сообщения, поля 422), `fieldPath.ts` и `fieldErrors.ts`
  (JSON Pointer → поле формы), `schemaForm.ts` (схема → модель формы, `x-sard-i18n`, сборка
  config), `sourceDraft.ts`, `runFilter.ts`, `logs.ts`, `stepNotes.ts`, `checklist.ts`,
  `tokens.ts`, `searchParams.ts`, статусы и значки — `theme.ts`; тест покрытия перечислений
  контракта словарями `ru` и `en`.
- **Веб, страницы.** Обзор, агенты и карточка, токены (окно с командой и опросом токена),
  источники, форма источника по схеме плагина (редактор JSON для схем вне подмножества),
  карточка источника со снимками, запуски с фильтром в адресе, карточка запуска с шагами и
  логами. Компонентных тестов нет; логики в страницах нет.
- **Моки.** Фикстуры покрывают состояния W2; отзыв агента переводит активные шаги в `lost`;
  мок `GET /overview` считает по правилам сервера; запуски называют источник; усечённый лог.
- **Клиент.** `call()` (ошибки API по коду, 401 → `UnauthenticatedError`), `MutationCache`
  так же, как `QueryCache`.

## Проверки
- `./scripts/gate.sh server fast` и `./scripts/gate.sh web fast` — `gate: PASSED` после стадии coder
  (сервер: 1037 тестов, покрытие 96,2 % инструкций; веб: 343 теста Vitest). Итог — в конце журнала.
- Страницы просмотрены в Chromium на моках (`VITE_API_MOCKS=1`): создание токена, источника,
  запуск, карточки запусков во всех состояниях. Полный ручной проход `docs/qa/console-pages.md`
  (против сервера и агента) не выполнялся.

## Окружение
JDK 25 поставлен из `apt` (`openjdk-25-jdk-headless`): в образе был только 21, а
`jvmToolchain(25)`. Maven Central отвечал 429, сборка повторялась до успеха.

## Стадии после coder
- **cleaner (`92cdce3`).** `FirstSteps.complete` и `ResultCheck.of` (оба CRAP 6.0) упрощены и ушли
  из верхушки таблицы `./scripts/crap.sh server`. Веб не менялся: CRAP для веба не измеряется,
  повтор вызова постраничного списка в пяти страницах оставлен (обёртка потребовала бы тяжёлых
  дженериков).
- **architect, первое ревью — CHANGES REQUIRED.** P1: правило «плагин источника объявляет
  `backup`» было только в консоли (OQ-085). P2: `partial` решался в двух местах (`ResultCheck` и
  `StepResults`). P3: 38 новых литералов цвета в `.tsx` вне `theme.ts`. P4: тест изоляции тенантов
  обзора покрывал только агентов.
- **coder, исправления.** `dc205fa` — P2 (`StepResults` берёт `backup.partial`, тест в
  `ArchitectureTest`), P3 (`tones` в `theme.ts`, 43 литерала в 23 файлах, `design.test.ts`), P4.
  `6ca2337` — спецификация К17 (вариант владельца А: 422 `unknown_plugin` у поля `plugin`),
  `7d3c78e` — мок, `d68b664` — сервер (`AgentOffer.require(requireBackup)`).
- **cleaner, второй проход.** Без правок; `AgentOffer.announcedPlugin` и `StepResults.snapshot` —
  CRAP 4.0, остальные затронутые функции 1.0–2.0 (`.bin/crap` по отчёту JaCoCo).
- **architect, повторное ревью — APPROVED.**
- **hardener (`cdc7532`).** До: 706 мутантов, 706 убито, 0 выжило — но `Overviews.kt`, `Runs.kt`,
  `AgentOffer.kt`, `StepResults.kt`, `Sources.kt` не попадали ни в один `@MutFlowTest`. Добавлен
  `W2MutationTest` (38 мутантов; по пути убиты 5 выживших в `ConfigCheck`, `AgentOffer.requireConfig`,
  `RunFilter.bind`). После: 744 найдено, 744 убито, 0 выжило; исключений нет.

## Итог
- `./scripts/gate.sh server` — `gate: PASSED (server, full)`, покрытие 96,3 % инструкций, худший
  CRAP 6.0 (все такие функции вне W2; из W2 в верхушке — `RunViews.backupOf`, 5.0).
- `./scripts/gate.sh web` — `gate: PASSED (web, full)`, 347 тестов Vitest.

## Открытое
Внесено в `docs/open-questions.md`: OQ-086 (проверка `backup` при запуске), OQ-087 (шаг 10 QA S2b
против S8b В1), OQ-088 (ручная QA W2), OQ-089 (красная фаза TDD), OQ-090 (код без мутантов
mutflow), OQ-091–OQ-093 (неблокирующие замечания architect), OQ-094 (размер чанка Vite),
OQ-095 (окружение: Docker, JDK 25). OQ-014 закрыт.

Docker-демон в облачном контейнере запущен вручную с разрешения владельца; один раз сам
остановился посреди сессии и был перезапущен.
