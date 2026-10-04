<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-04 — S9b: уведомления о запусках в Telegram

Ветка: `claude/focused-cray-bjc0dv`. Спецификация `docs/specs/server/run-notifications.feature`
(утверждена владельцем 2026-10-04, Р1–Р10); процедура QA — `docs/qa/run-notifications.md`.
Роль: coder, TDD.

## Что сделано
- `RunNotice` дополнен: `stepStatus`, `startedAt` (из `runs.started_at`), `backup`
  (`BackupSizes`: общий размер и добавленный). `Deliveries.notice` читает первый шаг запуска
  целиком и берёт вывод бэкапа через `RunViews.step` (partial-вывод неуспешного шага сохраняется).
- `notify/NoticeFormatter.kt`: `RunNoticeFormatter` (шаблоны RU/EN как в спецификации,
  заголовки по статусу шага, размеры и длительность как в `web/src/format.ts`, предел причины
  500 кодовых точек), `NoticeLanguage` (`en` по умолчанию, `ru`, пустое значение — по умолчанию,
  иное — `IllegalArgumentException` с `sard.notify.language` и допустимыми значениями),
  `ConsoleUrl` (http/https, хост, без query и fragment, завершающие `/` отбрасываются; иначе
  ошибка с `sard.console.public-url`), бин `NoticeFormatterConfiguration`.
- `application.yaml`: `sard.console.public-url: ${SARD_CONSOLE_PUBLIC_URL:}`,
  `sard.notify.language: ${SARD_NOTIFY_LANGUAGE:en}`; `deploy/docker-compose.yml` передаёт обе
  переменные серверу; `deploy/.env.example` и `docs/operations/notifications.md` описывают их и тексты.
- Тесты: `RunNoticeFormatterTest`, `NoticeSettingsTest` (оба `@MutFlowTest`),
  `NoticeFormatterConfigurationTest` (контекст без базы, настоящий `application.yaml`),
  `RunNotificationsIntegrationTest`, `RunNotificationsRestIntegrationTest`; усилен
  `NotificationsWithoutBotIntegrationTest` (ровно одно предупреждение, других WARN/ERROR нет);
  в `NotificationsIntegrationTest` — три теста на обогащённый `RunNotice`, тестовый форматтер
  помечен `@Primary`, чтобы он вытеснял настоящий.

## Решения в рамках спецификации
- Перевод строки между строками сообщения — отдельная часть `Text("\n")`; первая строка — одна
  часть `Bold`.
- Пустое значение `SARD_NOTIFY_LANGUAGE` равно «не задан» (compose передаёт пустую строку).
- Схема адреса консоли сравнивается как есть (`http`, `https` в нижнем регистре).
- Активный статус шага у завершённого запуска невозможен; форматтер возвращает `null`
  (строка `skipped`) — спецификация этого случая не описывает.
- «Сбой базы во время отправки»: в тесте база «недоступна» переименованием
  `notification_deliveries` на время первого тика (тик падает до отправки, повтор отправляет
  ровно одно сообщение).

## Проверка
- `./scripts/gate.sh server fast`: `coverage: 96.5% (instructions)`, CRAP без функций выше 6 в коде S9b,
  `gate: PASSED (server, fast)`, код 0. Тесты сервера шли с Docker (Testcontainers, `postgres:18-alpine`).
- Мутационные тесты S9b: `./gradlew -Pmutflow.enabled=true :server:test --tests '*RunNoticeFormatterTest'
  --tests '*NoticeSettingsTest'` — без выживших. Полный `gate.sh server` (с мутациями) не запускался.
- `make license-check`: 673 files OK.
- Ручная процедура `docs/qa/run-notifications.md` не проходилась (по постановке задачи).
