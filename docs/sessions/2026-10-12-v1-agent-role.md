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

### Поправка к решению 3 (2026-10-12): роль — второй URI в SAN

coder (сервер) обнаружил: `x509.ParseCertificate` в Go отвергает сертификат,
если в нём есть OID с дугой ≥ 2³¹ («x509: malformed extension OID field»).
Координатор проверил это на сертификате openssl: с дугой 2147483647 сертификат
разбирается, с OID `2.25.73832348518563859722830588044060276920` — нет. Дуга
`2.25.<UUID>` для Go непригодна.

Решение владельца: роль записывается вторым URI в subjectAltName —
`sard://roles/source` или `sard://roles/verifier` — рядом с URI личности.
Спецификация исправлена (specifier, Р1/Р3 и сценарии); ждёт подтверждения
владельца.
Поправка утверждена владельцем («пошёл»). Схема и хост URI роли сравниваются
без учёта регистра (RFC 3986). Неразбираемый URI в SAN — общая ошибка разбора
сертификата, отдельного сценария для него нет.

### coder (сервер, URI роли)

Расширение X.509 и его OID удалены из сервера (`AgentRoleExtension` -> `AgentRoleUri`, `AgentRole.kt`).
`Certificates.agent` пишет SAN из двух URI: личность и `sard://roles/<role>`. Разбор
(`AgentRoleUri.read`): URI роли — любой URI SAN со схемой `sard` и хостом `roles` (без учёта регистра,
userinfo и порт отбрасываются при определении хоста); нет — `Missing`, больше одного или не ровно две
допустимые строки — `Invalid`. `AgentIdentity.of` уже брал первый URI, который разбирается как личность, —
порядок SAN не важен, изменений не потребовалось (тест порядка добавлен). Значение URI роли в лог не
попадает (строка отказа берёт agent и tenant из личности). CSR по-прежнему даёт только ключ: добавлены
сценарии «Имена SAN и расширения CSR не попадают в сертификат», «URI роли verifier в CSR ...» и
«Порядок имён в SAN не влияет на проверку S3». Фикстура `ForgedRole` теперь описывает URI в SAN.

### coder (агент)

Go-агент: роль из сертификата, шлюз роли в исполнителе, `sard-agent status`, `machine_id`. Тесты
написаны по сценариям `docs/specs/agent/agent-role.feature`, названия сценариев процитированы в
комментариях над тестами.

**Что сделано**
- `internal/identity` (новый): разбор первого блока CERTIFICATE из `tls.cert_file`. URI личности
  ищется среди всех URI SAN в любом порядке; URI роли — любой URI со схемой `sard` и хостом `roles`
  (без учёта регистра, userinfo и порт игнорируются); допустим ровно один и ровно
  `sard://roles/source|verifier`. URI читаются из сырого расширения SAN (`encoding/asn1`), а не из
  `x509.Certificate.URIs`: разбор нормализует строку (`sard://roles/verifier#`), а требование — точное
  совпадение. Ошибки: `*RoleError` (CERT_ROLE_MISSING / CERT_ROLE_INVALID, значение URI в текст не
  попадает) и `*tlsid.FileError` для нечитаемого файла или сертификата без личности.
- `internal/machineid` (новый): HMAC-SHA256 по С7–С9; файловая система, источник случайных байт и список
  источников передаются в конструктор (`Host`), реальная — `machineid.OS`. Запись атомарна
  (временный файл, fsync, rename).
- `internal/executor`: опция `Role` (обязательна, `ErrInvalidOptions` для пустой или неизвестной);
  шлюз роли — первая проверка принятого шага (`refuse` в `command.go`), результат REJECTED с
  `reject_reason = ROLE_MISMATCH`, остальные отказы — UNSPECIFIED. В журнал шаг не пишется.
- `internal/app`: поле `MachineID` уходит в каждый Register; роль агент не отправляет.
- `cmd/sard-agent`: `identifyHost` в `start` между проверкой пары ключа и restic (С1), строка старта
  `... connecting to <addr> as <role>`; подкоманда `status` (`status_cmd.go`).
- `internal/tlsid`: `ReadCertificate` стал экспортируемым; текст «no parsable CERTIFICATE PEM block»
  заменён на «no parsable PEM certificate»: сценарии `status` требуют, чтобы слова CERTIFICATE не было
  ни в stdout, ни в stderr.
- `internal/transport`: `CheckFiles` — проверка наличия tls.* до чтения сертификата, с тем же текстом,
  что у `transport.New`.

**Решения, не прописанные в спецификации (для ревью)**
1. Пути `/etc/machine-id` и `/var/lib/dbus/machine-id` в тестах `cmd/sard-agent` подменяются
   пакетной переменной `machineIDSources` (в `TestMain` она указывает на отсутствующие файлы), потому что
   `run(...)` вызывается из десятков существующих тестов, а сигнатуру менять нельзя. Файлы хоста тесты не читают.
2. Тесты прав 0000 (`/etc/machine-id`, `tls.cert_file`) не используют реальные права: гейт работает
   от root, где права не действуют. Для `machine-id` недоступность проверена фейковой ФС (@unit) и
   подставленным каталогом (@start), для `status` — фейковым `readFile`.
3. Существующие тесты, которым нужен запуск агента, получили сертификат с личностью и ролью source
   (`identityPEM`, `issueAgentCert`); `TestHostnameFailure` и `TestStartStopsWhenResticCannotBeLocated`
   получили `tls.*`, потому что роль читается раньше restic и имени хоста (С1).
   `TestAnUnusableStateDirStopsBeforeDialing` теперь видит отказ `machine-id` (С8) раньше «invalid
   executor options»: каталог состояния создаётся при вычислении machine_id.
4. Тесты плагинов files и postgresql проверяют `ACTION_VERIFY` как «неподдерживаемое» теперь на агенте
   роли verifier (`newRigAs`): на source то же действие отклоняется по роли (сценарий «Источник отклоняет
   проверку»).
5. Сценарий «Агент не сообщает роль серверу» проверен по сериализованным байтам RegisterRequest и Hello.

## cleaner

Behaviour-preserving cleanup of the V1 diff (`928507d..HEAD`), `crap.sh` before and after, `gate.sh agent fast` and `gate.sh server fast` green.

| Function | CRAP before | CRAP after |
|---|---|---|
| agent `cmd/sard-agent/main.go:start` | 6.0 (CC 6) | 4.0 (CC 4); the new `loadLocal` is 3.0 |
| agent `internal/identity/identity.go:exactRole` | 4.0 (CC 4) | 3.0 (CC 3) |
| server `AgentAuthenticator.judge` | 6.0 (CC 6) | 3.0 (CC 3); split into `unknown`, `rejected`, `accepted` (3.0, 2.0, 1.0) |

Changes: `start` takes config load, local checks and role/machine id from `loadLocal`; `exactRole` checks the prefix and `Role.Valid` instead of looping over the roles; `judge` is split into three helpers (the log-only tenant id of the two role refusals is kept). Tests: the role refusal rows of `role_start_test.go` and `status_test.go` moved to `roleRefusalRows`; the inexact role URIs of `AgentRoleUriTest` and `AgentRoleAuthIntegrationTest` moved to `INEXACT_ROLE_URIS` (`ForgedCertificates.kt`). The two shared lists now run in both tests, so each covers a few more rows, none fewer.

Left as is: `RoleLedger` (interface) and `RoleRefusals` (database) stay split, like `DispatchLedger`, because `StepDispatcherTest` fakes the interface; `StepTransitions.kt` grew by ~80 lines for them and could take its own file, which is a module-structure question for architect. The role-URI rule exists once per module (`AgentRoleUri` in the server, `identity.roleOf` in the agent); the two implementations mirror each other by design across the wire and are not shared code.

## coder (правки architect)

Задачи 1-6 из CHANGES REQUIRED, поведение не менялось, кроме оговорённого.

1. Агент: новый листовой пакет `agent/internal/role` (`Role`, `Source`, `Verifier`, `Valid`, `Allows`); `identity` оставил разбор SAN и `RoleError`; `identity.Identity.Role`, `executor.Options.Role`, `cmd/sard-agent` и тесты используют `role.Role`. Тесты зависимостей: `role/imports_test.go` (только себя и `proto/gen/go/*`), `executor/imports_test.go` (не зависит от `identity`, `tlsid`, `config`, `transport`), `machineid/imports_test.go` (только себя). Тесты `Allows`/`Valid` переехали в `role/role_test.go`.
2. Одно исполняемое правило URI роли: `test/vectors/agent-role-uri.tsv`, читают `identity_test.go` (Go) и `AgentRoleUriTest` / `AgentRoleAuthIntegrationTest` через `ROLE_URI_VECTORS` (ищется от рабочего каталога вверх). `INEXACT_ROLE_URIS` удалён; случаи с несколькими URI и порядком имён остались локальными. Расхождение: `sard://[roles]/source` - Go `x509.ParseCertificate` отказывается разбирать такой сертификат, поэтому строка не попала в файл (в сертификате не встречается; правило Р1 для хоста со скобками не нужно). Других расхождений нет. `license-check.sh` файлы `.tsv` не проверяет; заголовок SPDX всё равно есть.
3. Сервер: `AgentRole` в листовом пакете `dev.sard.server.role`; `CertificateRole` и `AgentRoleUri` остались в `pki` (файл `AgentRoleUri.kt`). `architecture/PackageDependencyTest.kt`: `role` ничего не импортирует из сервера; `pki` - только `role`; `runs`, `fleet`, `scheduler`, `registration` не импортируют `pki`.
4. `AgentOffer.require(..., action, ...)` проверяет `role.allows(action)`; `requireBackup` переименован в `requireListed` (плагин должен перечислять именно это действие). Вызовы (`Runs`, `Sources` x2, `SelfBackups`) передают `Action.BACKUP`.
5. `TokenOwner.role` и `EnrollmentTokens.create(role)` без значений по умолчанию; REST оставляет null -> source, встроенный токен передаёт SOURCE.
6. Миграция V202610121300: `agents_builtin_source_check`, `agents_machine_id_check` (`^[0-9a-f]{64}$`), поправлен устаревший комментарий; тесты в `AgentRoleMigrationTest`.

## cleaner (разделение StepTransitions)

Задача 7 из обзора architect, поведение не менялось. `RoleLedger` и `RoleRefusals` переехали из `StepTransitions.kt` в `runs/RoleRefusals.kt` (интерфейс и реализация остались раздельными: `StepDispatcherTest` подменяет `RoleLedger`); `Session.execute` с `TEXT_VALUES` - в `runs/NativeMutations.kt` как `internal`. Минимальное расширение видимости: `STEP`, `NO_DEADLINE`, `MESSAGE`, `RUN_FINISHED` стали `internal` (их используют оба файла), `REFUSE` переехал с `RoleRefusals` и остался `private`. Лишние импорты `Agent` и `AgentRole` убраны из `StepTransitions.kt`. Комментарий к миграции V202610121300 проверен: он уже исправлен в 51ba6e6.

## hardener

Both full gates, with mutation, PASSED at the end: `./scripts/gate.sh agent` (exit 0, mutation score 0.958146) and `./scripts/gate.sh server` (exit 0, mutflow: 1854 mutations discovered, 1854 killed, 0 survived, summed over the XML reports of `test` and `isolatedTest`).

### Agent (go-mutesting)

Before: `mutation score: 0.954770`, 201 `FAIL` lines for the whole module. After: `mutation score: 0.958146`, 186 `FAIL` lines. V1 files only, per `go-mutesting --verbose` on them: 17 survivors before (`app.go`, `identity.go`, `machineid.go`, `tlsid.go`) and 6 after. `cmd/sard-agent`, `internal/role` and `executor/command.go` (the role gate) had no survivors. The other `FAIL` lines are in code V1 did not touch (`config`, `enroll`, `hostsetup`, `redact`, `repoconnect`, `restic`, `transport/outbox`, ...) and are left as they were.

Killed by tests (behavior, not mutants):
- `identity`: a role URI written in a SAN name of another kind (dNSName, rfc822Name, application-class tag 6) is `CERT_ROLE_MISSING`, not a role (`TestARoleURIInANameOfAnotherKindIsNoRole`; `certPEMNames` builds any raw SAN names).
- `machineid`: the fake FS now fails `MkdirAll` and `WriteFileAtomic` separately, so a failed directory creation and a failed write each refuse with the path; stored ids with a character below `0` (`/`) or above `f` (`g`) are refused.
- `app`: the fake `Registry` returns a nil handler for an unknown plugin as the real one does, so an announced plugin without a handler offers no actions (this also covered a survivor in pre-V1 `actions`).
- `tlsid`: a certificate or key that cannot be read keeps its cause (`errors.Is`); with only one of `cert_file`/`key_file` set `Check` reads nothing.
- `executor`: `TestNewRequiresHandlersSinkAndStateDir` asserts the whole message ("state dir is required", "role must be source or ...") instead of a word that other errors also contain.
- `transport.CheckFiles` (new in V1) had no test: complete configuration passes, each missing file is named, `ErrInvalidOptions` wraps it.

Left, with reasons (for coder or the owner):
- `identity.go.5` (`uriNames`: the `asn1.Unmarshal` error branch `return nil`): unreachable, `x509.ParseCertificate` has already rejected a malformed SAN, and `asn1.Unmarshal` leaves `names` empty on error. Equivalent. Proposal to coder: read the URIs from `cert.URIs`-independent raw bytes without the dead branch, or ignore the error with a comment; not done, `errcheck` would flag an ignored error.
- `machineid.go.4` and `.31` (`rawID`: `continue` -> nothing or `break` in the `ErrNotExist` case): equivalent, a missing file gives empty `data`, and the empty-content check right below also continues. Proposal to coder: remove the `ErrNotExist` case and test `err != nil && !errors.Is(err, fs.ErrNotExist)` once.
- `machineid.go.15`, `.18`, `.39` (`osFS.WriteFileAtomic`/`fill`: cleanup after a failed `Chmod`/`Write`, and the `Sync`): need fault injection in a real `*os.File` (a chmod, write or sync that fails on a local temp file); `Sync` is a durability guarantee no test can observe. The seam is missing in production code; not tailored around.

### Server (mutflow)

Before (first full run): 2 survivors in `isolatedTest`, and a Gradle `java.io.EOFException` in `:server:test` that hid 4 more. After the EOF was cleared (below) the 4 showed: 6 in total, all V1:
- `AgentRole.kt:23` `==` -> `!=` (`AgentRole.parse`), in `AgentsBuiltinLiveIntegrationTest`: the card's role was never asserted. New test `the card carries the role of the agent` (source and verifier).
- `AgentOffer.kt:80` `requireAllowed()` removed, in `W2MutationTest`: new test `a source needs an agent whose role allows backup` (a verifier agent, `AgentRoleMismatch` with its id, no source row).
- `DispatchParts.kt:67` `==` -> `!=` (RESTORE) and `RunModel.kt:98` `||` -> `&&` (`AgentRole.allows`), in `StepDispatcherTest`: new test `every action a role allows is sent as its own wire action` (BACKUP, RESTORE, RUN to a source, VERIFY to a verifier).
- `Certificates.kt:68` `+` -> `-` (agent certificate `notAfter`) and `Certificates.kt:122` `-` -> `+` (serial bit), in `AgentRoleUriTest`: new test asserts `notBefore == now`, `notAfter == now + 365 days` and a 128-bit serial of an issued agent certificate.

After: 0 survivors. No `mutflow:falsePositive`, `mutflow:ignore` or `@SuppressMutations` added.

### Pitfalls met

- OQ-030 happened: `go-mutesting` interrupted by the 30-minute limit of a background command left `enroll_run.go` (and later `repo_flags.go`) mutated plus a `*.go.tmp`. Restored from the `.tmp` and checked with `git status --porcelain agent/ | grep -v _test.go` (empty) before trusting each score. The mutation step of the agent gate takes about 2 hours here, so the final gates ran detached (`setsid nohup`), outside the command time limit.
- OQ-130 did not show: no `cmd/sard-agent` survivor in the final run (the gates ran one at a time, no parallel build).
- Flaky server failures: the Gradle `java.io.EOFException` recurred and was not a worker death this time but stable: `Test.getPreviousFailedTestClasses` could not read a truncated `server/build/test-results/test/binary/results.bin` left by an earlier killed run (stack trace in `./gradlew :server:test --stacktrace`). Fixed by `./gradlew :server:cleanTest :server:cleanIsolatedTest` (deleting the directory by hand is blocked by `guard.sh`). "Not a managed type: Agent" did not occur. One run failed with HTTP 429 from Maven Central while downloading `mutflow-compiler-plugin-1.5.0.jar`; a rerun succeeded.
- `detekt` MaxLineLength in a new test line, fixed.

### Слияние main и мутации по новой схеме (2026-10-14)

- Влит `origin/main` (11366b4, PR #65: Go-мутации через `go test -overlay`, шарды,
  измеряемый таймаут). Конфликт был только в импортах
  `agent/cmd/sard-agent/identity_test.go`. Слияние — `d39c000`.
- Поправка к коммиту `da69327`: в его сообщении указан счёт 0.954770, но он
  относится к прогону до усиленных тестов. После тестов hardener'а по старой
  схеме — 0.958146.
- Первый полный гейт агента после слияния упал: `cover: bufio.Scanner: token
  too long`. В `.cover.out` оказалось 7 МБ нулевых байтов, а на диске оставалось
  4.1G. После `go clean -cache` (освобождено 20G) гейт прошёл: coverage 98.2%,
  CRAP ≤ 6, интеграционные тесты, сборка e2e-стенда.
- Этап мутаций не укладывается в двухчасовой лимит фоновых команд сессии.
  Поэтому шарды `./scripts/gate.sh agent mutation i/4` запущены отдельно от
  сессии, итог посчитан через `mutation-total`. Результат: «mutants: 4407 total,
  3310 killed (224 of them by timeout), 340 survived, 757 did not compile,
  0 errors, 217 duplicates skipped», «mutation score: 0.922850»,
  «gate: PASSED (agent, mutation)».
- В файлах, которые затронул V1, по новой схеме выжили: `executor.go` — 10,
  `status_cmd.go` — 6, `main.go` — 6, `machineid.go` — 5, `enroll/identity.go`
  — 2, `identity.go` — 1. Старая схема их не показывала. Какие из них приходятся
  на строки V1, не разбиралось.
- `AgentSeamIntegrationTest` после слияния: tests=1, failures=0.

## coder (консоль и e2e)

Фаза 3 по `docs/specs/web/agent-role.feature` (решения РК1–РК8) и сценарий 7
`docs/specs/server/agent-role.feature` (`@e2e @phase3`).

- Консоль, тесты первыми (Vitest): `AgentRole` в `enums.test.ts`; `role` в
  `fieldPath.test.ts`; `offeredSourceAgents` в `sourceDraft.test.ts` (4 строки
  таблицы, порядок, пустой выбор); `canCreateSourceOn` в `tokens.test.ts`;
  моки в `handlers.test.ts` (роль токена, 5 недопустимых значений, фикстура
  верификатора, `agent_role_mismatch` на POST и PUT, порядок Р7).
- Код: `offeredSourceAgents` (рядом с `offeredAgents`, выбор агента формы
  источника), `canCreateSourceOn` (кнопка «Создать источник» на карточке
  агента), `role` в `fieldPath.ts`; выбор роли (по умолчанию source, отправляется
  всегда), колонка роли и строка «Роль агента: …» в окне токена по GET —
  `Tokens.tsx`; колонка и строка роли — `Agents.tsx`, `Agent.tsx`; ключи
  `enum.AgentRole.*`, `tokens.role|roleHint|roleOf`, `agents.role` в ru и en.
- Моки: токен хранит `role` запроса, `validateRole` (null и отсутствие — source,
  иное — 422 у `role`), `validateToken` (вынесено ради сложности ≤ 8), источник
  верификатора — 422 `agent_role_mismatch` после проверки отзыва и до плагина и
  репозитория; фикстура `verify6.example.com` (верификатор, offline, плагин
  files с backup, репозиторий `local`).
- e2e: `EnrollmentTokens.create/issue` получили параметр `role` (по умолчанию
  `source`, отправляется всегда, как в консоли); класс `VerifierRoleTest`.
  Новый класс в списки шардов вносить не нужно: `scripts/e2e-shard.sh` находит
  все `*Test.kt`, а вес берёт из `e2e-weights.tsv` (у нового класса его нет —
  берётся среднее до следующего измерения CI).
- Среда сборки e2e-образов: `make e2e-images` упал на `npm ci` (перехватывающий
  прокси; помог секрет `build-ca` с корнем шлюза через `SERVER_BUILD_FLAGS` и
  `E2E_BUILD_FLAGS`), затем на лимите Docker Hub (429, окно 3600 с) и на
  Gradle внутри docker (не нашёл плагин mutflow). Jar собран на хосте
  (`./gradlew :server:bootJar`) и подставлен в `make server-image`; собраны
  только `sard-agent:e2e` и `sard-server:e2e`. Образы stand, pg18/pg14 и sftp
  не собирались (в контейнере сборки не резолвится deb.debian.org), поэтому
  полный `make e2e-test` не запускался. Диск: кэш Go (19 ГБ) лишил Postgres
  места; помог `go clean -cache`.
