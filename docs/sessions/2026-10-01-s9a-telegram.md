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
