<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-12 — V1: роль агента в токене и сертификате, идентификатор машины

Ветка `ccr-6c6f4c08-945jg7`: её назначила сессия. В задаче указана
`feat/v1-agent-role`, переименовать можно по решению владельца. Это продуктовая
возможность, поэтому после контрольной точки 1 работа идёт по `/ship-feature`.
#61 и #62 уже в `main` (`928507d`).

## Фаза 1 — исследование

«Проверил» — видел в коде. «Полагаю» — не проверено: Docker в окружении
недоступен.

### Чего нет в репозитории

- `sard-restore-verification.md` (V-D4, V-D7, V-D8) и
  `docs/spikes/v0-restore-verification.md`: файлов нет, поиск по `docs/`
  ничего не нашёл. Предложения спайка V0 учесть нельзя.

### Что есть (проверил)

| Тема | Факт | Где |
|---|---|---|
| Токен | `enrollment_tokens`: hash, expires/used/revoked, label, builtin. Роли нет | `persistence/EnrollmentTokenRecord.kt:15-42`, `V202609271200__enrollment.sql` |
| Встроенный токен | `replaceBuiltin()`, в REST не виден. При регистрации `builtin` копируется в агента | `EnrollmentTokens.kt:175,244`; `Enrollment.kt:123` |
| Сертификат агента | BouncyCastle. В нём `CN=<agentId>`, один URI SAN `sard://tenants/<t>/agents/<a>`, EKU clientAuth, срок 365 дней. Своих расширений нет. Из CSR берётся только ключ | `pki/Certificates.kt:58-111`, `pki/AgentCsr.kt:15-29` |
| Перехватчик S3 | Открытый набор: Enrollment и Health. Принципал — `AgentPrincipal(agentId, tenantId, serial)`, сверка серийного номера и SAN с БД | `AgentAuthInterceptor.kt:44-80`, `AgentAuthentication.kt:88-127` |
| Register | Хранится hostname, версия, os, arch, protocol, имена секретов и скриптов, плагины, репозитории. Hello не хранится, в нём только `running_command_ids` | `Registration.kt:49-56`; `agent.proto:89,165` |
| Dispatch | Агент шага — `source.agentId`, выбора агента нет. Возможности проверяются в `AgentOffer.require` при запуске и при POST/PUT источника, в самом dispatch проверки нет | `runs/Runs.kt:179`, `runs/AgentOffer.kt`, `StepDispatcher.kt:132` |
| `ACTION_VERIFY` | Уже есть (`= 3`). Сегодня это проверка плагином (`sdk.Verifier`) снимка, восстановленного во временный каталог на том же агенте. Ни один встроенный плагин её не реализует, только testplugin | `pluginhost/handler.go:66-72,162-185` |
| `ACTION_RUN` | Обработчика нет: есть только allowlist `scripts` и `script_names` в Register | `executor/command.go:142` |
| Отказ шага | `STEP_STATUS_REJECTED` со свободным текстом, машинного кода нет | `executor/command.go:88-123`, `agent.proto:258` |
| Агент и сертификат | `tlsid.Check` проверяет только пару ключ/сертификат. `agent_id` из SAN читает лишь `enroll.InspectIdentity` | `tlsid/tlsid.go:43`, `enroll/identity.go:41,100` |
| `status` | Команды нет ни у `sard-agent`, ни у `sardctl` (там только `version`) | `cmd/sard-agent/main.go:57-66`, `cli/internal/commands/root.go:27` |
| machine-id | В коде нигде не используется | grep |
| sard-self | `read_only: true`, `hostname: sard-self`, состояние на томе `/var/lib/sard-self` | `deploy/docker-compose.yml:87-115` |
| e2e | Testcontainers: каждая команда (enroll, repo, сам агент) — новый контейнер на тех же томах | `test/e2e/.../AgentHost.kt:19-25,260-268` |
| Совместимость | `protocol_version = 1`, `buf breaking` (FILE) в CI. Теста «агент N−1 с сервером N» нет | `app/app.go:17`, `AgentSnapshot.kt:55`, `ci.yml:52` |
| Продление | `RenewCertificate` в proto есть, на сервере возвращает UNIMPLEMENTED | `AgentGrpcService.kt:29` |

Полагаю: в образах `ubuntu:24.04` и `sard-agent` нет `/etc/machine-id` (или он
пустой), а корень sard-self только для чтения. Проверю e2e в фазе 2.

### Предложения к контрольной точке 1

См. ответ владельцу в сессии: способ записи роли, идентификатор машины,
контракт proto и REST, вопросы. Решения внесу сюда после ревью.

### Решение владельца 1 (2026-10-12)

«Обратной совместимости не должно быть — релизов нет». Следствия для V1:

- Совместимость с агентом N−1 не нужна; вопрос о `supported_roles` в
  EnrollRequest снят.
- Роль в сертификате обязательна. Сертификат без роли сервер отклоняет (новая
  причина отказа S3), агент при старте не запускается. По умолчанию source не
  подставляется. Агентов, зарегистрированных до V1, нужно зарегистрировать
  заново, в том числе sard-self.
- Колонка `role` в `agents` и `enrollment_tokens` обязательна (`NOT NULL`).
  Существующим строкам миграция ставит `source` только для того, чтобы схема
  применилась; их сертификаты всё равно будут отклонены.
- `machine_id` в Register обязателен: пустое значение — отказ Register.
- proto: изменения и так добавочные, гейт `buf breaking` из CLAUDE.md остаётся
  зелёным; ломающих правок не делаю, раз они не нужны.

Открыты вопросы 1 и 3–7 (семантика `ACTION_VERIFY`, расширение X.509, соль
идентификатора машины, код отказа, `status`, мелочи).

### Решение владельца 2 (2026-10-12): вопросы 1, 3–7 — «со всем согласен»

1. `ACTION_VERIFY` выполняет только verifier. BACKUP, RESTORE и RUN выполняет
   только source. Запасной вариант ADR 0008 п. 3 «проверка на исходном хосте»
   отменён. `Verify` сторонних плагинов на verifier до V4 не вызывается.
3. Роль записывается некритичным расширением X.509 с OID в дуге `2.25.<UUID>`.
   Значение — DER UTF8String. S3 сверяет роль сертификата с ролью в БД.
4. `machine_id` — HMAC-SHA256 с ключом `"sard-machine-id/v1" ‖ tenant_id`.
   Источники по порядку: `/etc/machine-id`, `/var/lib/dbus/machine-id`, затем
   случайное значение в `executor.state_dir`.
5. Код отказа передаётся в `StepResult.reject_reason`
   (`REJECT_REASON_ROLE_MISMATCH`).
6. Команда `sard-agent status` работает офлайн.
7. Привязка источника к verifier — 422 `agent_role_mismatch`. CHECK в БД:
   builtin → source. `machine_id` до V4 не выводится в REST. Требование к C1:
   при продлении сертификата роль сохраняется.

## Фаза 2 — /ship-feature

### specifier — готово, спецификация утверждена

Файлы: `docs/specs/server/agent-role.feature`,
`docs/specs/agent/agent-role.feature`, `docs/qa/agent-role.md`.
Ответы владельца на О1–О11 — «согласен» с предложениями координатора:

- О1: код выхода при отсутствии роли — 1, а не 78 (как О2 agent-start-refusals).
- О2: неверная роль в REST — 422 `validation_failed`.
- О3: `"role": null` равносилен отсутствию поля, то есть source.
- О4–О11: приняты так, как их сформулировал specifier.

### coder (сервер) — proto и сервер готовы, Go-агент — отдельным прогоном

Ветка `ccr-6c6f4c08-945jg7`. Консоль, e2e и ADR — фаза 3, не тронуты (кроме
моков и таблицы кодов ошибок, см. ниже).

**proto** (`agent.proto`, добавочно): `RegisterRequest.machine_id = 10`,
`enum RejectReason`, `StepResult.reject_reason = 14` (поле 14 свободно, `oneof output` — 10–13).
Поля роли в сообщениях агента нет. `make proto`, `buf lint`, `buf breaking` зелёные.

**Сервер.** Миграция `V202610121300__agent_role.sql`: `role` NOT NULL в `agents` и
`enrollment_tokens` (существующим — `source`, значение по умолчанию снято), CHECK множества,
CHECK «builtin влечёт source», `agents.machine_id TEXT NULL` + индекс по (tenant, machine_id),
`agent_role_mismatch` добавлен в CHECK причин `schedule_fires`.

- `pki/AgentRole.kt`: `AgentRole`, `CertificateRole` (Missing / Invalid / Valid), `AgentRoleExtension`
  (OID, DER UTF8String, некритичное, разбор). `Certificates.agent` и `issueAgentCertificate`
  принимают роль; расширения и атрибуты CSR по-прежнему игнорируются (берётся только ключ).
- Enroll: роль токена пишется в сертификат и в `agents.role` в той же транзакции.
- S3 (`AgentAuthenticator`): порядок по Р4; новые причины `CERT_ROLE_MISSING`, `CERT_ROLE_INVALID`;
  роль сертификата ≠ роли в базе — `CERT_IDENTITY_MISMATCH`. `AgentPrincipal`, `CertificateStanding`,
  `PresentedCertificate` несут роль. Строка лога для двух новых причин — формат CERT_UNKNOWN
  (агент, тенант, метод, «re-enroll the agent»), значение роли в лог не попадает.
  Повторная проверка открытых потоков берёт роль из записи (роль неизменна).
- REST: `AgentRole` (с `@JsonCreator`: Jackson принимает число `1` за порядковый номер константы),
  `CreateEnrollmentTokenRequest.role`, `EnrollmentToken.role`, `AgentSummary.role`, `AgentDetails.role`,
  `ErrorCode.agent_role_mismatch`, `ScheduleFireReason.agent_role_mismatch`. `make openapi` выполнен.
- Register: `machine_id` проверяется последним (ровно 64 строчных hex, иначе `FIELD_INVALID`
  с `field=machine_id`), сохраняется при каждом Register, в логи и REST не попадает
  (`AgentSnapshot.toString` его скрывает).
- Источники: `AgentOffer` проверяет роль после «нет агента» и «отозван», до плагина и репозитория —
  `AgentRoleMismatch` (422 у `agentId` при POST и PUT, 409 при запуске, `refused` у расписания).
  Правило «что разрешено роли» — `AgentRole.allows(Action)` в `runs/RunModel.kt`.
- `StepDispatcher`: защита при отправке (`RoleGuard`); шаг, не допустимый роли, не отправляется,
  переходит в `failed` с сообщением «agent role mismatch: ...», в лог — WARN с id шага, агента,
  ролью и действием. Новый интерфейс `RoleLedger` (`roleOf`, `refuse`) и `RoleRefusals`.
- Поиск агентов по `machine_id`: `fleet/MachineAgents.withMachineId(tenant, machineId)`.

**Сверх спецификации, но без чего падает сборка или шлюз:**
- `ScheduleAlertFormatter`: слова для причины `agent_role_mismatch` (en, ru); без них `getValue`
  бросал бы исключение при тревоге о пропусках.
- `web/src/errors.ts`, `web/src/errors.test.ts`, `web/src/locales/{en,ru}.json`,
  `web/src/mocks/**`: код ошибки, подписи и поле `role` в моках, иначе `tsc` падает на новом
  `schema.d.ts`. Компонентов нет.

**Блокеры для Go-агента (сообщено координатору).**
1. OID `2.25.73832348518563859722830588044060276920` нельзя разобрать стандартной библиотекой Go:
   `x509.ParseCertificate` отвечает `x509: malformed extension OID field` на любой OID с дугой
   >= 2^31 (проверено: дуга 2147483647 разбирается, 2147483648 — нет; `cryptobyte.readBase128Int`).
   Агент не сможет загрузить собственный сертификат (`tls.LoadX509KeyPair`), не то что прочитать роль.
   Нужно решение владельца о другом OID; в сервере он в одном месте (`AgentRoleExtension.OID`) и
   в одной константе теста (`AgentRoleExtensionTest.ROLE_OID`).
2. `AgentSeamIntegrationTest` запускает настоящий агент, собранный из `agent/`: до работы агента
   (`machine_id`) он не пройдёт Register. Это единственный упавший тест серверного набора.

**Гейты (вывод инструментов).** `gate.sh proto fast` и `gate.sh gen fast` — PASSED; `gate.sh web` — PASSED;
`license-check`: 1176 files OK. `gate.sh server fast` — FAILED только на `AgentSeamIntegrationTest`
(п. 2 выше): spotless и detekt без замечаний, 2221 тестов (`test` + `isolatedTest`), 1 упал;
`gradlew --continue :server:check` довёл до отчёта: coverage 96.7% (instructions), `crap` -threshold 6 — код выхода 0.
Мутационный прогон (`gate.sh server`, без `fast`) не запускался.
