<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-01 — S9a: доставка уведомлений в Telegram

Ветка: `claude/wonderful-goldberg-9qq0gq` — ветка среды (в постановке — `feat/s9a-telegram`; ответ владельца В1). База — `main` @ `993456b`.

## Фаза 1 — исследование и вопросы (до кода)

### Проверенные факты (file:line)
- `notify/Notifier.kt:7-12` — `interface Notifier { fun send(subject: String, body: String) }`; реализаций, вызовов и бинов нет.
- `runs/RunFinished.kt:20-30` — `RunFinished(tenantId, runId, sourceId, agentId, trigger, status, message, finishedAt)`. Имён источника и агента в событии нет.
- `RunFinished.kt:33-35` — `fun interface RunFinishedListener`, «called on the thread that finished the run, so it must be quick». `RunFinishedPublisher` (`:43-57`) глотает исключения слушателя; KDoc: «Delivery is at most once for now».
- `RunAnnouncer.announce` (`:60-85`) открывает **отдельную** транзакцию чтения уже после фиксации и публикует после неё. Spring-событий, `@TransactionalEventListener` и `TransactionSynchronization` нет: транзакции — `TenantSessions.inTenant` (Hibernate `fromTransaction`).
- Единая точка финализации — `StepTransitions.Session.move` (`StepTransitions.kt:176-195`): условный `UPDATE` шага, при 1 строке — `RUN_FINISHED` в той же транзакции. Через неё идут все пути к финальному статусу запуска:
  1. `StepTransitions.finish(session, …)` внутри транзакции `StepResults.record` (`StepResults.kt:68-69, 87`);
  2. `StepTransitions.lost` (`:158-165`), вызывается из `StepDispatcher.tick` (`StepDispatcher.kt:65-67`), — своя транзакция;
  3. `StepTransitions.finished` (`:136-141`) — без вызовов в `main`, только тесты.
  Отмены со стороны сервера нет: `cancelled` приходит только как StepResult.
- Статус запуска выводится из шага (`RunModel.kt:26-34`): `FAILED, TIMED_OUT, REJECTED, LOST → FAILED`. Причина ошибки — только `runs.message`: сообщение агента, `invalid result: …` (`ResultCheck.kt:9,125`) или `agent lost the step` (`StepTransitions.kt:14`).
- ADR 0024:10 — ручной запуск уведомляет всегда; :15 — бот не настроен → сервер стартует, уведомления выключены, предупреждение в логе. О повторах и идемпотентности ADR молчит.
- OQ-047 (`docs/open-questions.md:54`) — предложено «outbox в транзакции завершения запуска и повторная доставка, ключ идемпотентности — `runId`», ответ владельца 4 в S7a: «Нужна гарантия at least once». OQ-017 (`:32`) — отложено до этапа уведомлений.
- Исходящих HTTP-клиентов в `main` нет. В тестовых зависимостях нет MockWebServer и WireMock (`server/build.gradle.kts:56-80`); `java.net.http.HttpClient` уже используется в тестах auth/api.
- Фоновые циклы — `AgentStreamSweeper(interval, check)` (SmartLifecycle, `scheduleWithFixedDelay`, `tick` не бросает); тесты зовут `tick()`/`check()` напрямую с `MovableClock` (`PkiFixtures.kt:28`).
- Метрики — доменный интерфейс + адаптер `Micrometer*` (`MicrometerStreamMetrics.kt:34-48`, `DispatchConfiguration.kt:73-94`), имена `sard.<домен>…`.
- Системная сессия `TenantSessions.system` — только чтение, вызовы перечислены в ADR 0013:48-54 и проверяются `ArchitectureTest` по файлам.
- `TenantSchemaRulesTest`: FK `tenant_id → tenants`, `UNIQUE (tenant_id, id)`, составные FK между тенантными таблицами. Миграции — `V<YYYYMMDDHHmm>__name.sql`, список версий в `SardServerIntegrationTest.kt:87`.
- `features/S9b-notifications.md` в репозитории нет; спецификаций S9/S9b нет.

### Ответы владельца (контрольная точка 1)

| № | Вопрос | Ответ |
|---|---|---|
| В1 | Ветка | ветка среды |
| В2 | Чтение очереди сквозь тенанты | `sessions.system` (только чтение), запись — `inTenant`; правка списка ADR 0013 и `ArchitectureTest` |
| В3 | Без бота | «Второе» (не терять сразу, истекать по сроку); «потенциально не только телеграм, учти это» |
| В4 | Вставка в транзакции финализации | «Алерты — кандидат на асинхронную обработку, а не на синхронную передачу» |
| В5 | Ключ идемпотентности | запуск + канал |
| В6 | Текст по умолчанию | ждать S9b |
| В7 | Fake Bot API | `com.sun.net.httpserver.HttpServer` из JDK, без новых зависимостей |
| В8 | Числа повторов | приняты: 8 попыток, 24 ч, 10 с → 10 мин, потолок `retry_after` 1 ч, таймауты 5/10 с |
| В9 | Очистка | пока без очистки |
| В10 | Один чат на все тенанты | да, записать ограничение в ADR |
| В11 | Метрики | да, с тегом `channel` |
| В12 | Тест падения через `tick()` | да |

### Решения фазы 1 (по ответам)
**OQ-047 — асинхронно, без вмешательства в транзакцию S7a (В4).** Надёжный факт уже есть: S7a фиксирует `runs.status` и `runs.finished_at` в транзакции финализации. Уведомления выводятся из него фоновым процессом, а не передаются из пути завершения:
1. *Планировщик* (каждый тик) читает сквозь тенанты завершённые запуски с `finished_at` не старше срока `sard.notify.ttl` (24 ч), для которых нет доставки по каналу, и создаёт строки `notification_deliveries` (`INSERT … ON CONFLICT DO NOTHING`, ключ — тенант + запуск + канал, В5). Строки создаются по каждому настроенному каналу (В3: каналов может быть несколько).
2. *Отправитель* берёт строки, у которых подошло время, «арендует» строку (сдвигает `next_attempt_at` на `sard.notify.lease`), форматирует, отправляет вне транзакции и записывает исход условным `UPDATE`.
3. `RunFinishedListener` только будит цикл раньше тика; надёжность от него не зависит.

Следствия: `StepTransitions`, `StepResults` и транзакция финализации не меняются; ошибка уведомлений не может откатить или задержать запуск. Падение сервера в любой точке после фиксации запуска не теряет уведомление: запуск остаётся завершённым и непокрытым доставкой до следующего тика; падение во время отправки — повтор после аренды (возможен дубликат, у `sendMessage` нет ключа идемпотентности).

**Без бота (В3, «второе»).** Сервер стартует, в логе предупреждение. Планировщик без каналов ничего не создаёт, но запуски, завершённые в пределах `sard.notify.ttl`, будут уведомлены, когда канал появится; старше — нет. Очередь не копится: её строки появляются только для настроенных каналов, а неотправленная строка истекает (`expired`) через тот же срок. Форматтера нет до S9b (В6) — уведомления тоже выключены с предупреждением.

**OQ-017 (этап 1).** `SARD_TELEGRAM_BOT_TOKEN` и `SARD_TELEGRAM_CHAT_ID` из окружения, один чат на все тенанты (В10); хранение в БД — этап 2.

**Политика повторов (В8).** 2xx — `delivered`; 429 — ждать `retry_after` (не больше 1 ч), попытка не считается; 5xx, сеть, таймаут — задержка 10 с·2ⁿ⁻¹ до 10 мин, не больше 8 попыток; 4xx кроме 429 — `failed` с причиной; не успели за 24 ч от создания — `expired`.

**Схема** `notification_deliveries`: `tenant_id`, `id`, `run_id`, `channel` (`CHECK` по регулярному выражению: каналы расширяемы), `status` (`pending|delivered|skipped|failed|expired`), `attempts`, `next_attempt_at`, `created_at`, `finished_at`, `last_error`; `UNIQUE (tenant_id, run_id, channel)`, FK `(tenant_id, run_id) → runs`. Частичный индекс по `runs.finished_at` для планировщика.

## Фаза 2 — очередь, подписка, клиент Bot API

### Сделано
- Миграция `V202610021200__notifications.sql` (при слиянии с `main` переименована из `V202610011200`: эту версию занял S8b): `notification_deliveries` (ключ `UNIQUE (tenant_id, run_id, channel)`, FK `(tenant_id, run_id) → runs`, `CHECK` финала по `finished_at`), частичные индексы `notification_deliveries_due_idx` и `runs_finished_at_idx`. `TenantSchemaRulesTest` проходит без правок.
- `notify/`:
  - `Notifications.kt` — `RunNotice` (вход форматтера), `Message` из частей `Text|Bold|Code` (только простой текст), `NotificationFormatter` (S9b; `null` — уведомлять не нужно → `skipped`), `NotificationChannel`, `SendOutcome`.
  - `RetryPolicy.kt` — политика В8, чистая функция.
  - `Deliveries.kt` — очередь: `unplanned`/`due`/`pending` через `sessions.system` (В2; ADR 0013 пункт 5, `ArchitectureTest`), `plan`/`claim`/`record` — условные операторы в `inTenant`.
  - `NotificationService.kt` — `tick()`: план по каждому каналу, затем отправка; аренда строки на время попытки.
  - `NotificationLoop.kt` — свой поток `sard-notifications`, `wake()` из `RunFinishedListener`.
  - `telegram/` — `TelegramBotApi` (JDK `HttpClient`, `BotToken`), `TelegramHtml` (экранирование и лимит 4096), `TelegramChannel`.
  - `NotifyConfiguration.kt` — `sard.notify.*`, `SARD_TELEGRAM_BOT_TOKEN`/`SARD_TELEGRAM_CHAT_ID` через `@Value`, метрики `sard.notify.{sent,retries,undelivered}` с тегом `channel` и датчик `sard.notify.pending`.
- Заготовка `notify/Notifier.kt` (`send(subject, body)`) удалена: её заменили `NotificationChannel` и `Message`.
- `StepTransitions`, `StepResults`, `RunFinished` — без изменений (S7a сохранён).

### Отступления от плана фаз
- Рендер HTML (экранирование, лимит) сделан в фазе 2: без него канал не отправляет. Его тесты (`TelegramHtmlTest`) — это тест 4 стратегии, уже зелёный.
- Проверка «без бота» на уровне настроек (`TelegramSettingsTest`) — тоже здесь: без неё `telegramChannel` не проходил CRAP. Предупреждение в логе при старте и отсутствие строк без канала — фаза 3.
- `NotificationLoop` повторяет устройство `AgentStreamSweeper` (поток с фиксированной задержкой), но имеет `wake()` и своё имя потока. Обобщать чужой класс — не роль coder; кандидат для cleaner.

### Проверено (команды и результат)
- `./gradlew :server:test --tests 'dev.sard.server.notify.*'` — exit 0: `RetryPolicyTest`, `TelegramBotApiTest`, `TelegramHtmlTest`, `TelegramSettingsTest`, `NotificationLoopTest`, `NotificationsIntegrationTest` (Testcontainers).
  - Тест 1: 429 с `retry_after` (попытка не считается, ждёт 37 с), 500 — задержки 10, 20, 40, 80, 160, 320, 600 с и `failed` после 8 попыток, 400 — `failed` после одного запроса, истечение через 24 ч.
  - Тест 2: запуск зафиксирован без работающего отправителя → новый экземпляр сервиса отправляет; отправитель «упал» после аренды → повтор после `sard.notify.lease`.
  - Тест 3: транзакция финализации (`StepTransitions.finish` в `inTenant`) откатилась → запуск остался `running`, строк и запросов нет.
- `./gradlew :server:detekt` — exit 0. `./gradlew :server:spotlessApply` применён.
- Полный `./gradlew :server:test`: 1 падение — `AgentSeamIntegrationTest` («the real agent registers its snapshot…»): агент выходит с `RESTIC_NOT_FOUND`, потому что тест намеренно указывает `restic.path` на несуществующий файл, а с A5b (`59775f4`) агент проверяет restic при старте. **На `origin/main` падает так же** (запущено в отдельном worktree). К S9a не относится; не чинил — вопрос владельцу.
- Покрытие и CRAP сняты с отчёта JaCoCo полного прогона (`:server:test` с `ignoreFailures` только локально, через init-скрипт вне репозитория, чтобы отчёт собрался несмотря на падение выше): инструкции сервера 94,86 %, `notify` 94,47 %, `notify/telegram` 97,07 %; `.bin/crap -threshold 6` — ни одной функции `notify` выше порога (выше были `TelegramBotApi.outcome`, `telegramChannel`, `RetrySettings.validated`, `TelegramCredentials.configured` — разделены).
- Мутационное тестирование (mutflow) не запускалось — фаза hardener.

### Не проверено / полагаю
- Поведение за HTTP-прокси в проде: клиент берёт `ProxySelector.getDefault()` (системные свойства `https.proxyHost`); тестами не покрыто.
- Нагрузка: планировщик берёт до `sard.notify.batch` запусков на канал за тик; при большом потоке завершений очередь догоняет за несколько тиков — не измерялось.

## Фаза 3 — экранирование, защита токена, режим без бота, метрики

Владелец на контрольной точке 2: «пофиг на этот тест» (`AgentSeamIntegrationTest`, красный и на `main`), делать фазу 3.

### Сделано
- **Токен.** `TelegramBotApi` строит запрос внутри `try`: `IllegalArgumentException` клиента JDK печатает URI целиком, с токеном, — теперь это исход `Rejected("invalid request: …")`, очищенный от токена. `sard.notify.telegram.api-url` — только `http`/`https`. `TelegramCredentials.toString` прячет токен; токен читается через `@Value`, не через `@ConfigurationProperties`.
- **Без бота.** `activeChannels(channels, formatter)` — нет канала или нет форматтера (S9b) → активных каналов нет, предупреждение в журнале, план ничего не создаёт.
- **Метрики** проверены и модульно (`SimpleMeterRegistry`), и в интеграции (приращения `sent`, `retries`, `undelivered{reason=failed}`, датчик `pending`).
- **Развёртывание.** `deploy/.env.example` (`SARD_TELEGRAM_BOT_TOKEN=` пустой, `SARD_TELEGRAM_CHAT_ID` закомментирован; `DeployEnvExampleTest`), `deploy/docker-compose.yml`, раздел `sard.notify` в `application.yaml`, `docs/operations/notifications.md`.
- **Документы.** Черновик ADR `docs/adr/00XX-draft-notifications.md`. Реестр: OQ-047 и OQ-017 закрыты (раздел «2026-10-01, S9a»), открыт OQ-071 (очистка `notification_deliveries`, ответ В9). Ссылки в ADR 0013 («Отложено», каналы уведомлений) и в черновике S7a (OQ-047 закрыт иначе).

### Проверено (команды и результат)
- Тест 4: `TelegramHtmlTest` — экранирование `&`, `<`, `>` во всех частях, лимит 4096 видимых символов с `…`, суррогатная пара не разрывается, теги парные.
- Тест 5: `NotificationsIntegrationTest` «the bot token never reaches the logs or the database…» — журнал захвачен `OutputCaptureExtension` при `logging.level.dev.sard.server.notify=TRACE`; ответы 500 и 401 с токеном в `description`, 429, затем успешная доставка. Токена (и формы `%3A`) нет ни в журнале, ни в `last_error`; что ошибка вообще залогирована, проверено (`HTTP 401: Unauthorized: bot[REDACTED]`). Модульно — `TelegramBotApiTest` (описание с токеном, непригодный URI, `toString`).
- Тест 6: `NotificationsWithoutBotIntegrationTest` — контекст без `SARD_TELEGRAM_*` стартует, в журнале предупреждение, после трёх завершённых запусков и трёх тиков строк в `notification_deliveries` нет, `sard.notify.pending` = 0. `NotifySetupTest` — без канала, без форматтера, с обоими.
- `./gradlew :server:spotlessCheck :server:detekt` — exit 0; `make license-check` — 538 файлов OK.
- Полный `./gradlew :server:test` (через локальный init-скрипт с `ignoreFailures`, чтобы собрать JaCoCo): 838 тестов, 1 падение — `AgentSeamIntegrationTest` (то же, что на `main`, см. фазу 2). Без init-скрипта `./gradlew :server:test` завершается с ошибкой из-за этого теста.
- JaCoCo (инструкции): сервер 94,97 %, `notify` 95,02 %, `notify/telegram` 97,35 %. `.bin/crap -threshold 6`: функций `notify` выше порога нет (максимум 5,4 — `closedStatus`).

### Не проверено / открыто
- **Мутационное тестирование.** mutflow оценивает только классы с `@MutFlowTest`; в тестах `notify` его нет, значит mutflow этот код не проверяет. Это работа hardener (`/ship-feature`, последний шаг).
- Работа за HTTP-прокси в эксплуатации (`ProxySelector.getDefault()`) тестами не покрыта.
- Настоящий Telegram не вызывался: только fake Bot API.
- Сервис рассчитан на один экземпляр сервера (ADR 0026); аренда защищает строку от двойной отправки и при нескольких, но это не проверялось.

## Слияния с `main` (2026-10-02)
- После S8b: миграция `V202610011200__notifications.sql` → `V202610021200__notifications.sql` (версию занял S8b); вопрос об очистке очереди — с OQ-051 на следующий свободный номер.
- После A7c: A7c занял OQ-069 и OQ-070, вопрос об очистке `notification_deliveries` — **OQ-071**. Серверный код `main` не менялся.
