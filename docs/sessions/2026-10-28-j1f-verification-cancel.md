<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# J1f — отмена проверки восстановления (coder)

Дата: 2026-10-28. Спецификация `docs/specs/server/run-cancel-deadlines.feature` (правила J1f, утверждена 2026-10-28,
ВJf1–ВJf7), веб — `docs/specs/web/run-cancel-deadlines.feature`, QA — `docs/qa/run-cancel-deadlines.md`, часть 6.

## Что сделано (тест первым, красный прогон — потом код)
1. `AttemptOutcomeTest`: попытка без вердикта после отмены — `cancelled`, без повтора и без третьей попытки; вердикт,
   пришедший раньше, остаётся итогом; собственный `infrastructure_failure` верификатора после отмены — тоже `cancelled`.
   Красный прогон: не компилировался (нет параметра и статуса). Затем `AttemptOutcome.of(..., cancelRequested)`,
   `VerificationStatus.CANCELLED`.
2. `VerificationCancelIntegrationTest` (PostgreSQL, настоящие запуски): отмена задания без попытки и с попыткой в очереди,
   идущей попытки (`CancelStep`), через `RunCancels` по запуску-попытке, повторная отмена, подтверждение верификатора (запись
   `cancelled`, `cancelledBy`, `cancelledAt`, новой попытки нет), две неудачи до отмены, вердикт раньше отмены (три вердикта),
   окно потери (`unconfirmed`), закрытое задание, чужой тенант, вердикт пришёл, но не разобран (побеждает, отмена 409), отмена
   цикла и «проверить сейчас», `recovered` через отменённую. Реализация: миграция `V202610281200__verification_cancel.sql`,
   `VerificationCancels`, `CancelMarks` (вынесены из `RunCancels`), `AttemptCancels`, `VerificationSettlement.settleJob`.
3. `VerificationCancelApiIntegrationTest` (REST и настоящий gRPC-верификатор): отмена в очереди, идущая попытка и `CancelStep`,
   подтверждение, отмена по `/runs/{id}/cancel`, повтор, 409, 404, 401/403, вердикт раньше отмены; 503 — в
   `UnavailableApiIntegrationTest`. Красный: 9 из 9 (нет эндпоинта). Реализация: `POST /verification-jobs/{jobId}/cancel`,
   `VerificationJob.cancellation`, `Verification.cancelledBy/cancelledAt`, `ReportItem.cancelledBy`, `ReportSummary.cancelled`.
4. Тесты записи: обзор не считает `cancelled` и смотрит сквозь неё, отчёт (строка и число), уведомления (молчит; серия
   `failed, cancelled, failed`; `failed, cancelled, passed` — recovery; «последний итог» эпизода), `FailureEpisodes`,
   `VerificationTelling`, слова формата. `run_not_cancellable` удалён из `ErrorCode`, `RunErrors`, обработчика, OpenAPI,
   моков, словарей, `errors.ts`, тестов.
5. Агент (ВJf3): `TestACancelledAttemptRemovesTheSandboxAndTheJobDirectory` и
   `TestTheCleanupOfACancelledAttemptEndsAtItsOwnLimit` — красные (`removed true under a context that was context canceled`,
   текст «cannot remove the sandbox … context canceled» вместо «ran out of its time limit of 10m0s»); затем
   `context.WithoutCancel` перед стадией `cleanup`.
6. Консоль: `CancelVerificationButton` (один диалог, один запрос), «Проверка отменяется», запись «отменена» (кто и когда,
   роль `neutral`, значок отмены запуска), отчёт; чистые функции `jobCancellable`, `jobCancelMessageKey` (+ тесты); моки MSW
   (`POST /verification-jobs/{jobId}/cancel`, запись `cancelled`, метрика источника смотрит сквозь неё), тесты моков.
   `make openapi` выполнен.
7. e2e `VerificationCancelTest` (компилируется, не запускался: образов нет — OQ-263).
8. Документы: раздел J1f в `docs/adr/00XX-draft-run-cancel-deadlines.md`, «Отмена проверки» и агент N−1 в
   `docs/operator/17-verifier.md`, OQ-263 (дополнен), OQ-264, OQ-265.

## Замечания (спецификация не говорит или говорит неточно)
- Сценарий «Отмена задания в гонке с вердиктом»: «200 с cancellation result finished_first» недостижим: пока вердикт не
  разобран, у задания `pending`. Реализовано: если конец попытки уже в базе, отмена сперва разбирает задание (вердикт
  побеждает, 409 `run_finished`); `finished_first` записывается в задание, закрытое вердиктом при отметке (тик).
- Последний статус источника (`status` в `SourceVerificationState`, счётчики обзора) смотрит сквозь `cancelled`: «не входит в
  счётчики, прозрачна» спецификация сказала, а как показать источник, у которого последняя запись — `cancelled`, — нет.
- `clear_verification_evidence` и срок уборки верификатора — OQ-264, OQ-265.
