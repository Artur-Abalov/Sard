<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-01 — S9a: доставка уведомлений в Telegram

Ветка: в постановке — `feat/s9a-telegram`, среда сессии задаёт `claude/wonderful-goldberg-9qq0gq` (вопрос В1). База — `main` @ `993456b`.

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

### Предложения на контрольную точку 1
См. ответ в сессии: OQ-047 — outbox (строка в `notification_outbox` из `Session.move` при финализации), OQ-017 — окружение на этапе 1, схема, политика повторов. Ждут ответов владельца (В1–В12).
