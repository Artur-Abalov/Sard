<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-15 — C1: продление сертификата агента

Ветка `ccr-1a57e1dc-ha8h4a`: её назначила сессия. В задаче указана
`feat/c1-cert-renewal`, переименовать можно по решению владельца. PR #61, #62 и
V1 (#70) уже в `main` (`2881821`), поэтому роль из V1 сохраняется при продлении.
Продуктовая возможность: после контрольной точки 1 работа идёт по
`/ship-feature`.

## Фаза 1 — исследование

«Проверил» — видел в коде. «Полагаю» — не проверено.

### Чего нет в репозитории

- D19 и D4.4 в `docs/` не найдены (они в основном плане). Есть OQ-022
  (`docs/open-questions.md:32`) и раздел «Отложено» ADR 0014.
- `sard-agent check` (A8d) не реализован: `run()` знает только enroll, status,
  repo, secret (`agent/cmd/sard-agent/main.go:61-73`). Срок показывает только
  `status`.

### Сервер (проверил)

| Тема | Факт | Где |
|---|---|---|
| Выпуск | `issueAgentCertificate(csrDer, AgentIdentity, AgentRole)`, Clock и SecureRandom внедрены | `pki/CertificateAuthority.kt`, `pki/FileCertificateAuthority.kt:28-29,75` |
| Срок | `AGENT_VALIDITY = Duration.ofDays(365)`, константа, `notBefore = now`. С `notAfter` CA не сверяется | `pki/Certificates.kt:32,68` |
| Атрибуты | `CN=<agentId>`, SAN: `sard://tenants/<t>/agents/<a>` и `sard://roles/<role>`, EKU clientAuth, serial 128 бит | `pki/Certificates.kt:68,115-129`, `pki/AgentRoleUri.kt` |
| CSR | только ECDSA P-256/P-384, подпись проверяется, остальное игнорируется | `pki/AgentCsr.kt` |
| БД | `agent_certificates(serial, tenant_id, agent_id, issued_at, not_after, revoked_at)`; несколько сертификатов на агента схема допускает | `V202609271200__enrollment.sql:27-38` |
| S3 | поиск по serial на каждый вызов, проверки: SAN = БД, роль = `agents.role`, `CERT_REVOKED`, `CERT_EXPIRED`, `AGENT_REVOKED` | `agents/AgentAuthentication.kt:100+` |
| RenewCertificate | в `AgentService` (под mTLS), не переопределён — UNIMPLEMENTED. Истёкший сертификат до него не доходит (`CERT_EXPIRED`) | `agent.proto:83-85,140-147`; `agents/AgentGrpcService.kt:29` |
| Отзыв | `revokeIn` ставит `revoked_at` агенту и всем его сертификатам | `fleet/Agents.kt:240` |
| Дубликат сессии | второй стрим того же сертификата — `AGENT_DUPLICATE_SESSION` | `agents/stream/AgentStreams.kt:90-135` |
| Уведомления | событий-«push» нет; `NotificationService.tick()` планирует из строк БД, дедупликация уникальными ключами `notification_deliveries`, `num_nonnulls(run_id, fire_id, episode_id) = 1`. Тексты в Kotlin (`NoticeWording.kt`, `OverdueNoticeFormatter.kt`) | `notify/` |
| Периодика | образец — `FreshnessLoop` + `FreshnessCheck.tick()` | `freshness/` |
| REST | `AgentSummary`, `AgentDetails` — полей сертификата нет | `api/AgentsController.kt:38,86` |
| Ограничение частоты | `LoginAttemptTracker` (по адресу, константы не настраиваются); Enroll не ограничен | `auth/LoginAttemptTracker.kt` |
| sard-self | тот же Enroll по встроенному токену; токен пишется в `SARD_SELF_DIR/enroll-token`, пока нет живого встроенного агента | `selfagent/SelfAgentCheck.kt:62-66` |

### Агент (проверил)

| Тема | Факт | Где |
|---|---|---|
| Файлы | `/etc/sard/tls/{ca.pem,agent.pem,agent.key}`; каталог 0700 `sard-agent` | `deploy/agent/agent.example.yaml:13-15`, `postinstall.sh:43-44` |
| Запись при enroll | temp рядом + fchown/chmod/fsync + rename, порядок CA → ключ → сертификат, fsync каталога | `internal/enroll/write.go:24-260` |
| Ключ | ECDSA P-256, PKCS#8 | `internal/enroll/csr.go`, `enroll_run.go:290` |
| Срок при старте | не проверяется; истёкший даёт Unauthenticated на Register → `ErrNotAuthorized` → 78 | `internal/transport/transport.go:89-98`, `main.go:111-117` |
| Коды демона | 0, 1, 2, 78; коды A2b (0–7) — только у enroll | `main.go:42-47`, `enroll_flags.go:20-27` |
| systemd | `RestartPreventExitStatus=78`, `ProtectSystem=strict`, **без** `ReadWritePaths=/etc/sard/tls` — демон сейчас не может записать продлённую пару | `deploy/agent/sard-agent.service:22-30` |
| Транспорт | `GetClientCertificate` читает пару при каждом рукопожатии; новый `grpc.NewClient` на каждое подключение; часы `transport.Clock` внедрены | `transport.go:54-57,131,233-283` |
| Журнал FXa | запись до ACCEPTED, удаление после сохранения результата; при старте незавершённые — FAILED «interrupted» | `internal/executor/store.go`, `executor.go:314-359` |
| sard-self | тот же бинарник, файлы в `/var/lib/sard-self/tls`, compose `restart: unless-stopped` — защиты от бесконечного перезапуска нет | `deploy/docker-compose.yml:87-112`, `deploy/agent/Dockerfile:87-106` |
| SELinux | в коде и `deploy/` не упоминается; проверки в enforcing нет (ADR 0048) | `docs/specs/agent/agent-install.feature:20` |

### Предварительный дизайн (на ревью)

- Ротация ключа обязательна: сервер отказывает, если ключ CSR совпадает с
  ключом предъявленного сертификата.
- Подтверждение нового сертификата — отдельный унарный вызов по новой паре на
  отдельном соединении; после него старый отклоняется, но открытый стрим не
  рвётся и не дублируется, переход — при следующем переподключении.
- Пара `agent.key.new` / `agent.pem.new` рядом со старой; при старте берётся
  согласованная пара с самым поздним `notAfter`.
- Порог продления — 2/3 срока от `notBefore`; предупреждение и уведомления —
  см. вопросы.

### Решения контрольной точки 1 (владелец принял все рекомендации)

- Р1. Старый сертификат отклоняется после подтверждения нового: новый унарный
  `ConfirmCertificate` по новой паре на отдельном соединении, сервер ставит
  старому `superseded_at`. Открытый стрим на старом не рвётся; переход — при
  следующем переподключении. Неподтверждённый выпуск заменяется следующим
  запросом.
- Р2. Пара `agent.key.new` / `agent.pem.new` рядом со старой; при старте —
  согласованная пара с самым поздним `notAfter`; в основные имена — после
  подтверждения.
- Р3. Истёкший сертификат: локальная проверка при старте, понятная причина и
  команда перерегистрации, код 78.
- Р4. sard-self (встроенный агент) при постоянном отказе не выходит, а ждёт в
  состоянии «отклонён», раз в час пишет причину.
- Р5. Пороги — доли срока: продление с 2/3; предупреждение в консоли при
  остатке ≤ 30/365; уведомления на 30, 7, 1 день (масштабируются), одно на
  порог на сертификат.
- Р6. Частота: не больше одного выпуска на агента за
  `sard.pki.agent-renewal-min-interval` (1 ч, не больше 1/10 срока), иначе
  `RESOURCE_EXHAUSTED`. Агент повторяет с паузами 1 мин → 1 ч с разбросом.
- Р7. Ключ CSR обязан отличаться от текущего (`INVALID_ARGUMENT`).
- Р8. `ReadWritePaths=/etc/sard/tls` в unit.
- Р9. `notAfter` листа обрезается по `notAfter` CA; OQ о сроке CA.
- Р10. В C1 расширяется только `status` (дни и состояние); `check` — A8d.

## Фаза 2 — сервер и агент

### specifier

- `docs/specs/server/agent-certificate-renewal.feature`,
  `docs/specs/agent/agent-certificate-renewal.feature`,
  `docs/qa/agent-certificate-renewal.md`; поправки C1 в О2
  (`agent-start-refusals.feature`) и С6 (`agent-role.feature`).
- Владелец в чате принял П1–П26 и поправки 1–6, затем П27–П31.
- Поправки: S5a закрывает поток только при отзыве и истечении (не при
  «заменён» и «не подтверждён»); пара выбирается по самому позднему
  `notBefore`; `ReadWritePaths=-/etc/sard/tls`.
- Хвост: шаг 11 `docs/qa/agent-role.md` ещё ждёт четыре строки `status`.

## Фаза 2 — coder, сервер и proto

Спецификация утверждена владельцем в чате (П1–П31, поправки 1–6); строки «ждут
подтверждения» в шапках `.feature` устарели, файлы спецификации не менялись.

- proto: `rpc ConfirmCertificate` и два пустых сообщения; комментарий
  `RenewCertificateRequest.csr_der`; `make proto`, `buf lint`, `buf breaking` зелёные.
- Сервер: `AgentCertificatePolicy` (настройки срока и интервала, ошибка старта),
  `notAfter` листа по `notAfter` CA, миграция `V202610151200`,
  `CertificateRenewal` + `RenewalStatus`, причины S3 `CERT_SUPERSEDED` и
  `CERT_NOT_CONFIRMED` (`AuthScope`: вызов, подтверждение, открытый поток),
  сверка S5a с областью `OPEN_STREAM`.
- CA теперь берёт внедрённые часы (`Clock`), а не `Clock.systemUTC()`: иначе
  управляемые часы тестов не доходят до выпуска.
- Проверка в тестах: JVM проверяет срок клиентского сертификата по реальным
  часам при рукопожатии TLS, поэтому T0 тестов лежит на сутки в прошлом (или
  равен «сейчас» при срок PT10M и короче).
- Расхождение, о котором сказано в отчёте: сценарий «Поток со старым
  сертификатом закрывается при отзыве агента» ждёт `CERT_REVOKED`, а
  `Agents.revoke` закрывает поток сразу с `AGENT_REVOKED` (поведение до C1).
  Тест воспроизводит записи отзыва в базе и проверяет вердикт периодической
  сверки.

## Фаза 2 — coder, агент

Спецификация `docs/specs/agent/agent-certificate-renewal.feature` (все сценарии,
кроме `@phase3`/`@e2e`), поправки О2 и С6. Шлюз `./scripts/gate.sh agent fast` — зелёный.

- Пакеты: `internal/certpair` (выбор пары П21, запись `.new`, переименование,
  уборка), `internal/certrenew` (порог, паузы, состояние П25, `Manager`:
  продление, подтверждение, `Adopt` на старте, `Superseded` для П23),
  `internal/certtest` (тестовый CA и ручные часы), `transport.CertClient`
  (RenewCertificate и ConfirmCertificate по отдельным соединениям),
  хуки `Options.OnRegistered` и `OnSuperseded`, `Describe`, `RetryDelay`, `Backoff`.
- `cmd/sard-agent`: порядок старта П29 (`loadLocal`), `refusedError` (код 78,
  команда перерегистрации), встроенный агент в состоянии «отклонён» (П24),
  `status` (П26), `agentRuntime` (часы, случайность, журнал — внедряются через
  `selfEnrollDeps.runtime`).
- Решения: если выбор пары не находит согласованной, сначала сообщаются права
  файлов (как раньше, А1), затем ошибка пары. UNIMPLEMENTED на RenewCertificate
  не пишет файл состояния и не растит счётчик. `Superseded` отвечает «да» и тогда,
  когда продление этого процесса переименовало пару между рукопожатием и отказом.
  Первая попытка ждёт первого успешного Register (П17).
- Не сделано: два сценария `@executor` (шаг во время продления) — продление не
  обращается к исполнителю и потоку, но теста с блокируемым обработчиком нет.

## Cleaner

- `loadLocal` (main.go): the checks that come before the identity moved to `selectPair`; `certificateState` (status_cmd.go): reading the renewal state file moved to `renewalRecord`. CRAP of loadLocal 6.0 -> 3.0, certificateState 6.0 -> 5.0 (`./scripts/crap.sh agent`). `./scripts/gate.sh agent fast` passes.
- Not done: certpair's `OSFS.WriteFile`/`syncDir` resemble `enroll.stage`/`syncParent`, but differ in prefix, owner handling and rename step; sharing them would couple two packages for no behaviour gain.
- Server: no change. Without Docker the renewal code is covered only by Docker-only integration tests (0% in the local JaCoCo report), so no refactor could be verified here; left to CI. architect runs next.
- Review of 4bdfc108 and a9d3f1d2: no touched agent function is above CRAP 5.4 (`adoptPending`) and none is at the limit (`./scripts/crap.sh agent`); no duplicated mapping (`transport.finalCode` and `certrenew.finalRefusal` sit on two sides of the `CallError` seam on purpose).
- Only change: `transport.callError` renamed `wrapCallError`, since `certrenew.callError` does the opposite (extracts). No behavior change; `./scripts/gate.sh agent fast` passes.
- Server: no change (Docker unavailable, so nothing could be verified locally). architect runs next.

## Hardener

Mutation testing of what the branch added or changed in `agent/` and `server/`. Tests only: no production file was edited.

### Agent (go-mutesting through `scripts/mutate-exec.sh`, 4 shards)

- Target: the new and changed sources (`internal/certpair`, `internal/certrenew`, `internal/transport/{certclient,refusal,transport}.go`, `internal/identity/identity.go`, `cmd/sard-agent/{main,renewal,runtime,self_enroll,status_cmd,refused}.go`).
- Before: 109 survivors (shards 17 / 34 / 27 / 31, scores 0.86 / 0.82 / 0.86 / 0.78).
- After: 25 survivors (4 / 8 / 7 / 6, scores 0.967 / 0.958 / 0.964 / 0.958).
- Tests added (all behaviour, none names a mutant): `certpair/hardening_test.go`, `sync_internal_test.go`; `certrenew/hardening_test.go`, `describe_internal_test.go`, `response_test.go` (exact refusal details, intermediate CA, non-CERTIFICATE block); `transport/certclient_hardening_test.go`, `spare_internal_test.go`; `cmd/sard-agent/renewal_hardening_test.go`, `status_hardening_test.go`; `identity_test.go` and `certtest.CA.Intermediate`.
- Noteworthy: a write that fails after the temporary file exists is tested in a child process under `RLIMIT_FSIZE` (the limit is lifted again before the coverage file is written). `//go:debug cryptocustomrand=1` in `certrenew/hardening_test.go` makes Go 1.26+ honour `Options.Random`, which otherwise is ignored by `ecdsa.GenerateKey`.
- Full `./scripts/gate.sh agent` (with mutation), exit 0: 5490 mutants, 4129 killed (293 by timeout), 390 survived, 971 did not compile, 0 errors; score 0.928962 (threshold 0.80). A first run failed on a full disk (`no space left on device`, go build cache); after `go clean -cache` the rerun passed.
- No bug found: no mutant revealed wrong behaviour.

Survivors left, none killable by a behavioural test:

- Equivalent: `certpair/select.go` `err != nil ||` in `candidate.read` (a non-role error leaves `who` empty, which never equals the owner) and the `ReadCertificate` error return right after `tlsid.Check` has parsed the same certificate; `certrenew/schedule.go` `n <= 6` vs `n <= 7` (capped by `min` anyway); `certrenew/state.go` `err != nil &&` (`return err` with nil is `return nil`); `certrenew/manager.go` `retryAfter` returning 0 vs 1 or -1 (the jitter delay is never below 30 s); `transport/transport.go` `Backoff` `attempt < 6` vs `<= 6`/`< 7` (capped by `min`); `transport/refusal.go` `st == nil` returns (`(*status.Status)(nil).Details()` is nil-safe).
- Dead error paths: `certrenew/manager.go` `marshalKey` (PKCS#8 of a P-256 key cannot fail) and the CSR creation error (would need an entropy source that works for key generation and fails for signing). Proposal to coder: drop both error returns or accept them as defensive.
- Not in this branch (lines untouched by it, survivors before C1): `cmd/sard-agent/main.go` `shutdownTimeout`, `stop()` before `os.Exit`, `args[1:]` of `runEnroll`, the two `return err` after `transport.New` and `executor.New`; `identity.go` `uriNames` error return.

### Server (mutflow, ADR 0006)

Docker is unavailable here, so only the `@MutFlowTest` classes that need no container ran: `./gradlew --offline :server:test -Pmutflow.enabled=true --rerun --tests` for `RenewalChecksTest`, `AgentAuthenticatorTest`, `SessionRevalidationTest`, `AgentCertificatePolicyTest`, `AgentCertificateValidityTest`, `FileCertificateAuthorityTest`, `CertificatesTest`, `RenewalStatusTest`, `AgentAuthStatusTest`, `AgentRefusalMessageTest`, `StepResultReceiverTest`, `EnrollmentStatusTest`: exit 0, 12 classes, 0 survivors, 0 timeouts.

- Before: 0 survivors in the existing C1 classes. `RenewalStatus` had no `@MutFlowTest` (0 mutations discovered): added `agents/RenewalStatusTest.kt`, 5 mutations, 5 killed. `AgentAuthStatusTest` discovers 0 mutations: it is not hardened by that run.
- Not mutation-tested, because they have no `@MutFlowTest` and need PostgreSQL: `CertificateRenewal`, `AgentGrpcService`, `AgentAuthInterceptor`, `AgentCertificateStandings`, `AgentCertificateRecord`. Zero discovered mutations is not hardening; the full `./scripts/gate.sh server` in CI is the judge.

## Итог фазы 2

- Пайплайн: specifier → coder (сервер, агент) → cleaner → architect
  (CHANGES REQUIRED: страж импортов бинарника, `certrenew` без gRPC, срок в
  шве CA) → coder → cleaner → architect (APPROVED) → hardener.
- CI на `7f6ac7c` (проверил): `server` (полный гейт с Docker и mutflow),
  `go (agent)`, `agent-mutation-total`, proto, web, пакеты, e2e-шарды 1–2 —
  зелёные.
- Попутно найдено и исправлено по корню: гонка счётчика в
  `StepResultReceiver` (счёт до ResultAck); тест дескрипторов, считавший
  процесс параллельно с другими; гонки в новых тестах агента.
- Красный только e2e-шард 3: `ServerRecoveryDrillTest`, догоняющий запуск
  A1. Журнал срабатываний (диагностика `756a775`) показывает простой короче
  одного слота cron: строки `skipped_downtime` нет, догон не положен. Это
  допущение теста F7 о длительности простоя, не C1; исправление теста — на
  решение владельца (комментарий в PR #73).
- Тесты задачи: 1, 2, 3, 4, 5, 7 — модульные и интеграционные (проверил в
  CI). 6 и 8 — фаза 3.
- Открыто для фазы 3: консоль, уведомления S9a/S9b, документация оператора,
  ADR; предложение hardener убрать мёртвые ветки `marshalKey`/`newRequest`;
  повтор генерации ключа P-256 в `certrenew` и `enroll` (architect,
  необязательно).

## Фаза 3 — coder, агент, документация и ADR (2026-10-19)

Спецификация утверждена владельцем 2026-10-19 (П45, П46, ответы 1–4).

- `sard-agent check`: строки `tls.identity`, `tls.chain`, `tls.expiry` берут пару
  из `certpair.Select` (П45) через файловую систему только для чтения: ничего не
  удаляется и не переименовывается. `Selection.Latest` — выпущенная последней
  пара, когда истекли все. Без согласованной пары действуют файлы конфига и
  прежнее сообщение.
- `tls.expiry` (П46): сравнение `R × 365 <= L × 30` в `math/big`; текст WARN и
  подсказка `sudo sard-agent status`; `RenewWithin` и фраза «automatic renewal is
  not available yet (C1)» удалены. Старые тесты порога 30 суток переписаны под
  долю срока (сертификат на год, `certEnding`).
- Документация: `docs/operator/13-agent-certificates.md`, строка 13 в README,
  правки 03, 05, 09 (`tls.expiry`), `docs/qa/host-check.md` (шаг 38), ADR
  `00XX-draft-agent-certificate-renewal.md`, ADR 0014 («Отложено»), реестр:
  OQ-022, OQ-228 (срок CA), OQ-229 (допущение `ServerRecoveryDrillTest`).
- Не сделано, чтобы не выходить за сценарии: `server.tls` (`clientTLS`) и
  `files.permissions` (`secretFiles`) по-прежнему берут файлы `tls.*` из
  конфига, а не выбранную пару. Для хоста с годной парой `.new` и истёкшей
  основной `server.tls` предъявит истёкший сертификат.

## Фаза 3 — coder, сервер и e2e (2026-10-19)

Коммит `73393055`: поле `certificate` в REST, проверка срока, события и
уведомления S9a, минимум `PT2M`. Docker недоступен: интеграционные тесты
проходили на локальном PostgreSQL 16 в копии репозитория, `gate.sh server fast`
не запускался.

- Решение coder для владельца. В сценарии «Брошенный после перерегистрации
  агент получает одно уведомление об истечении» сертификат нового агента в
  тесте выдан в T0 плюс 300 суток: выданный вскоре после T0, он в T0 плюс 365
  суток сам попал бы в предупреждение 30/365 и дал бы второе сообщение.
  Сценарий этого не оговаривает.
- Хранение событий записано в ADR `00XX-draft-agent-certificate-renewal.md`;
  `fleet/AgentCertificateCheck.kt` добавлен в список `sessions.system` ADR 0013.
- e2e @t8: `CertificateLifetimesTest` (стенд с `SARD_PKI_AGENT_CERTIFICATE_VALIDITY=PT2M`,
  агент X и sard-self, наблюдение 7 минут, сценарии @t1, @t7, @t8). Компилируется
  (`:e2e:compileTestKotlin`), не запускался.

## Фаза 3 — cleaner (2026-10-19)

Замеры `./scripts/crap.sh agent`: в затронутых фазой 3 файлах выше предела
только `certpair.latest` (CRAP 6.0, CC 6, флаг `anyExpiry`). Разделена на
`newest` и `unexpired`, поведение прежнее; `latest` из списка ушла. Сервер и web
не менялись: дублирования внутри модуля и логики в компонентах
(`CertificateMark.tsx` использует только `certificateMarkTone` из `theme.ts`)
замеры не выявили. Серверный CRAP (`NotificationService.plan`, 6.0) судит CI.

## Фаза 3 — hardener (2026-10-19)

Go: `go-mutesting` через `scripts/mutate-exec.sh` по `certpair/pair.go`,
`certpair/select.go`, `hostcheck/tls.go`, `cmd/sard-agent/check_files.go`,
`check_server.go`: 9 выживших (оценка 0.960870) до, после усиления тестов
остались 5, все эквивалентные, расчёт ниже. Новые тесты: выбор пары один раз на
запуск `check` (`TestCheckSelectsThePairOnceForAllItsLines`), пустой путь не
файл для `files.permissions`, сбой чтения сертификата на любом шаге выбора пары
(`TestACertificateThatCannotBeReadAtAnyStepMakesNoPair`).

Эквивалентные, исходный код не менялся: `ConfigLoadLine(path, 0, err)` (при
`err != nil` число фрагментов не читается; двое мутантов); `var pem [3][]byte`
в `clientTLS` (лишний пустой элемент); `chain[1:]` в `hostcheck/tls.go`
(лист как промежуточный не меняет проверку цепочки); `err != nil ||` в
`certpair.candidate.read` (при ошибке чтения личности `who` пуст и не равен
владельцу, тот же отказ). Предложение coder: убрать параметр `fragments` из
`ConfigLoadLine` для ветки ошибки и `pem` сделать срезом по числу файлов.

Сервер (mutflow, без Docker): `CertificateEventsTest` обнаруживал 0 мутаций
(цель `CertificateEvent::class` не включает `companion`); добавлен
`CertificateEvent.Companion::class`, теперь 4 мутации, 0 выживших.
`AgentCertificateLoopTest` стал `@MutFlowTest` (3 мутации; одна выживала, пока
тест не проверил имя и daemon-статус потока). Новый `AgentCertificateMappingTest`
(`toApi`, 17 мутаций, 4 выживали до границ состояний). Остальные чистые классы:
`AgentCertificateLifetimeTest` 13, `CertificateNoticeFormatterTest` 18,
`AgentCertificatePolicyTest` 5, 0 выживших. Классы с БД
(`AgentCertificates`, `AgentCertificateCheck`, нотификации) проверяет только CI
по `./scripts/gate.sh server`.

## Слияние с `main` @ `fa7be76b` (2026-10-19)

PR #73 был в конфликте с `main` (`mergeable_state: dirty`), поэтому CI не
запускался. Конфликт один: `ServerRecoveryDrillTest`. `main` закрыл ту же
гонку учений F7 своим способом (`6848fb2a`,
`RecoveryOldVm.awaitTwoMissedFires`, корень разобран в
`docs/sessions/2026-10-19-release-beta1-fixes.md`). Файл взят из `main`, а
ожидание C1 (`awaitTwoMissedSlotsOfA1`) снято: два ожидания одной гонки не
нужны. OQ-229 перенесён в «Закрыто при сверке».
