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

## Правки по замечаниям architect (CHANGES REQUIRED)
- Заголовок шага — исчерпывающий `when` по `StepState` (`Wording.headline` и `Verdicts` в `NoticeWording.kt`) вместо
  карты; `outcome()` без `else`, активные состояния — `error(...)`. Тексты не менялись.
- Тесты `RunNoticeFormatterTest`: у каждого завершённого состояния свой заголовок на каждом языке;
  активное состояние даёт `null`; список в «Без значка…» — `StepState.entries.filterNot { it.active }`.
- `ArchitectureTest`: файлы notify без привязки к Telegram; `notify` добавлен в `ISOLATED_PACKAGES`.
- ADR 00XX: устранено устаревшее утверждение про отсутствие форматтера, добавлен раздел «S9b: форматтер».
- KDoc `RunNotice.startedAt` исправлен. Пункты 4, 5, 7 оставлены cleaner.

## Cleaner, раунд 2 (e4aea6e)
- Убрана недостижимая ветка «без форматтера»: `notificationService` принимает `NotificationFormatter` напрямую.
- `WEB_SCHEMES` вынесен в общее место и используется повторно.
- Общие тестовые заготовки каналов вынесены в `NoticeFixtures.kt`.

## Cleaner, раунд 3 (N1: `Verdicts.failure()`) — без изменений кода
- Замер до: `Verdicts.failure` CRAP 5.5 (CC 5), `Wording.headline` 4.0 (CC 4), `RunNoticeFormatter.outcome` 5.1 (CC 5).
- Прямое отображение FAILED/REJECTED/LOST/TIMED_OUT в `Wording.headline` даёт 8 ветвей одного `when`
  (раньше 7 ветвей дали CRAP 7 и провалили gate), поэтому `failure()` оставлен; замена на таблицу `Map` убрала бы
  проверку исчерпывающности компилятором.
- Сужение `outcome()` не делалось: потребовался бы отдельный тип «завершённое состояние», это не упрощение.
- `./scripts/gate.sh server fast`: PASSED.

## Hardener (мутационное тестирование S9b)
- До (`-Pmutflow.enabled=true :server:test --rerun`, exit 0): `RunNoticeFormatterTest` 56 мутантов / 56 убито / 0 выжило;
  `NoticeSettingsTest` 12 / 12 / 0. Выживших нет.
- Пробел покрытия: `activeChannels` (NotifyConfiguration.kt) тестировался `NotifySetupTest` без `@MutFlowTest` — 0 мутаций.
  Тест помечен `@MutFlowTest`, вызовы обёрнуты в `MutFlow.underTest { }`: 3 мутанта, 3 убито, 0 выжило.
- `Deliveries.notice` (обогащение `RunNotice`: stepStatus, startedAt, backup) и `NoticeFormatterConfiguration.noticeFormatter`:
  в добавленных S9b строках нет операторов, которые мутирует mutflow (присваивания, `?.let`, вызовы конструктора).
  Пробный `@MutFlowTest` над `Deliveries.claim` нашёл 11 мутантов, из них 2 выжили, оба вне S9b и недостижимы через
  `RunNotice`: `Runs.kt:244` (`partial` у BackupResult, уже убивается в `W2MutationTest`) и `HibernateTenantBridge.kt:25`
  (`isRoot`). Пробный класс удалён: он не проверял бы S9b. Поведение обогащения проверяют интеграционные тесты
  (`RunNotificationsIntegrationTest`). Мутанты `Deliveries.kt:144,151` (S9a, `claim`) не покрыты mutflow — вне объёма S9b.
- После: выживших 0; `./scripts/gate.sh server` — `gate: PASSED (server, full)`, coverage 96.4% (instructions), CRAP максимум 6.0.
- Продакшен-код не менялся.
