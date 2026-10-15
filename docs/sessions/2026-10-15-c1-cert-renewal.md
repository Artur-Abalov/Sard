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
