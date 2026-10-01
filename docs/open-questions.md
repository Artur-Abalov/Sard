# Открытые вопросы

Единый реестр того, что не решено или не сделано: вопросы владельцу, известные дыры, отложенные решения ADR. Журналы сессий (`docs/sessions/`) остаются историей. Открытый вопрос сессии попадает сюда в той же сессии, а закрывается здесь же ссылкой на коммит, ADR или file:line (решение владельца, 2026-09-28).

Статусы:
- **вопрос** — ждёт решения владельца;
- **дыра** — известная ошибка или риск, исправление не сделано;
- **отложено** — решение сознательно перенесено на этап, указанный в «Кому / когда»;
- **задача** — работа определена, не сделана.

Сверено с кодом 2026-09-28, `main` @ `f4de483`. OQ-004, OQ-023 и REST API токенов регистрации (OQ-037) пересверены 2026-09-29, `main` @ `ff97ae1` (X2).

## Открыто

| ID | Суть | Статус | Кому / когда | Источник |
|---|---|---|---|---|
| OQ-001 | Агент повторяет Register на `INVALID_ARGUMENT` бесконечно, с задержкой до 1 мин. Повторять невалидную конфигурацию нельзя: код должен быть окончательным, как требует модель ошибок. Серверной защиты от частых повторов нет (решение владельца). | дыра | A3 (агент) | `agent/internal/transport/transport.go:273-284`; журнал S4a, «Дыра A3»; ADR 0025, «Повтор» |
| OQ-002 | Неопознанное падение одного теста (`329 tests completed, 1 failed`) в фазе 2 S4a. Имя не сохранилось, в 13 повторах не воспроизвелось. Если повторится — сохранить `server/build/test-results` до следующего запуска. | дыра | сервер, при повторе | журнал S4a, фаза 2, «Открытое» |
| OQ-003 | Один `repository_id` у агентов разных тенантов: вероятная ошибка конфигурации MSP. Поиск идёт сквозь тенанты (ещё один вызов `system`), поэтому в Register не включён. | отложено | отдельная системная проверка или отчёт | ADR 0013, «Отложено»; журнал S4a, ответ 2 |
| OQ-005 | Толкования ответов владельца 1, 2 и 4 в A4 (исполнитель) явно не подтверждены. | вопрос | владелец | журнал A4:329, :402 |
| OQ-007 | Ключ подписи релизов restic в репозитории и `REQUIRE_SIGNATURE=1` в CI: ключ не удалось получить из окружения разработки. | отложено | при доступе к keys.openpgp.org / restic.net | ADR 0017, «Отложено»; `scripts/fetch-restic.sh:15,46` |
| OQ-008 | Путь модулей `github.com/Artur-Abalov/sard` при имени репозитория `Artur-Abalov/Sard`: внешний `go get` не проверен, модули не опубликованы. | задача | первая публикация модулей | журнал каркаса:79, :140; ADR 0005 |
| OQ-009 | CLA — черновик 0.1, нужна проверка юристом. | вопрос | владелец, до первого внешнего PR | `docs/legal/CLA.md:3` |
| OQ-010 | Код, который проверяют только интеграционные тесты (`Enrollment`, `TenantSessions`, `Registration`), mutflow не мутирует; его покрытие даёт только JaCoCo. | вопрос | владелец: принять или искать способ | журнал S2a:106; ADR 0006 |
| OQ-011 | Срок хранения завершённых токенов регистрации (О2). Сейчас по спецификации — бессрочно. | отложено | владелец | журнал S2b:19; `docs/specs/server/agent-enrollment.feature`, решение 9 |
| OQ-012 | Правило Redocly `operation-4xx-response` отключено. | вопрос | владелец / S8 | `web/redocly.yaml:10`; журнал S8a:82 |
| OQ-013 | Страница 404 рендерится без AppShell, потому что маршрут вне `_app`. Нужна ли на ней оболочка? | вопрос | владелец / W-этап | журнал W1a:69 |
| OQ-014 | Обработчики и фикстуры моков для эндпоинтов этапа 1 из S8. | задача | W2 | журнал W1a:127; `web/src/mocks/README.md` |
| OQ-015 | Скан планировщика по всем тенантам: кандидат — ещё один вызов `TenantSessions.system`. | отложено | планировщик | ADR 0013, «Отложено» |
| OQ-016 | Пользователи и роли (`users`, `memberships`). Аутентификация этапа 1 (W1b) сделана без них: один администратор, пароль из окружения; пользователи и роли ушли в enterprise RBAC. | отложено | enterprise RBAC | ADR 0013, «Отложено»; ADR 0021:129 |
| OQ-017 | Где хранить учётные данные каналов уведомлений (Telegram, SMTP). | отложено | этап уведомлений | ADR 0013, «Отложено» |
| OQ-019 | RLS вторым рубежом под `@TenantId`. | отложено | если появится нативный SQL | ADR 0013, «Отложено» |
| OQ-020 | ГОСТ: `crypto_provider` зарезервирован, нужен отдельный ADR. | отложено | позже | ADR 0008, «Отложено» |
| OQ-021 | Эскроу ключей репозиториев у доверенного хранителя. | отложено | позже | ADR 0008, «Отложено» |
| OQ-022 | `RenewCertificate`, промежуточный CA, серверный сертификат из корпоративной PKI, Vault/PKCS#11, ГОСТ. | отложено | — | ADR 0014, «Отложено» |
| OQ-023 | API и UI отзыва агентов и сертификатов. Закрытие открытых стримов отозванного агента сделано в S5a: сверка с БД раз в `check-interval` (`server/src/main/kotlin/dev/sard/server/agents/stream/AgentConnections.kt:39-45`, тесты `AgentStreamIntegrationTest.kt:187`, `:197`), для мгновенного закрытия из API есть `AgentConnections.close` (`AgentConnections.kt:29-37`). REST отзыва нет: в `AgentsController.kt` только `GET` (:105, :113). | задача | S8b (REST), W2 (UI) | ADR 0014, «Отложено»; ADR 0026, «Сверка открытых сессий с БД» |
| OQ-024 | Подпись пакетов и репозиториев apt/yum; публикация пакетов в релизах GitHub. | отложено | — | ADR 0018, «Отложено» |
| OQ-025 | Литерал в конструкторе перечисления причин (`enum class Reason(val code: String)`) — если понадобится независимость имён причин от провода. | отложено | владелец | ADR 0025, «Отложено» |
| OQ-026 | `redactIfToken` в `sard-agent enroll` вычищает токен по шаблону `sard_\S*` и съедает знак сразу за токеном (двоеточие, закрывающую кавычку) и безобидные пути вида `/etc/sard_agent/…`. Утечки нет, портится только текст сообщения. Шаблон по алфавиту токена: `sard_[A-Za-z0-9_-]*(?:\.[0-9a-f]*)?`. | дыра | A2 (агент) | `agent/cmd/sard-agent/enroll_flags.go:215-219`; журнал A2b, третье ревью architect |
| OQ-028 | e2e-тест кладёт ключ, сертификат и CA в контейнер агента вручную, а не через настоящий `sard-agent enroll`. 8 сценариев `@e2e` спецификации `enroll` не автоматизированы, есть только ручные шаги QA. Ручная процедура QA целиком не проходилась; поведение от имени не-root и с IPv6 проверено только тестами на подменах (среда разработки — root, без IPv6). | задача | T2 | `test/e2e/src/test/kotlin/dev/sard/e2e/AgentConnectTest.kt:26`; `docs/specs/agent/agent-enroll.feature:144`; `docs/qa/agent-enroll.md` |
| OQ-029 | CLAUDE.md, правило 7: агенту разрешены «stdlib, gRPC, protobuf и YAML-парсер», а A2a объявил прямую зависимость `google.golang.org/genproto/googleapis/rpc` (`errdetails.ErrorInfo`, часть модели статусов gRPC, уже была транзитивной). Architect счёл её допустимой; уточнить формулировку правила («gRPC, включая типы статусов `google.rpc`»). | вопрос | владелец | `docs/dependencies.md:15`; журнал A2b, первое ревью architect |
| OQ-030 | `go-mutesting`, прерванный снаружи (таймаут, перезапуск контейнера), может оставить в дереве мутированный файл и `*.go.tmp`; гейт после этого даёт ложный счёт или зависает. Сейчас защищает только ручная проверка `git status --porcelain agent/ \| grep -v _test.go` перед доверием к счёту и перед коммитом. Кандидат — проверка чистоты дерева в начале и в конце шага мутаций `scripts/gate.sh` (защищённый файл, правит владелец). | дыра | владелец (инструменты) | журнал A2b, раздел hardener, «pitfalls» |
| OQ-031 | За TLS-терминирующим обратным прокси или прокси, переписывающим `Host`, консоль не может войти: вход и все изменяющие запросы получают 403 `origin_rejected` (сервер сверяет `Origin` со схемой и `Host` своего соединения), а cookie сессии — без `Secure`. Этап 1 поддерживает только прямой доступ к :8080. Нужно доверие к `X-Forwarded-Proto`/`Forwarded`/`X-Forwarded-For` от настроенного прокси (Р4). | задача | до первой установки за прокси | ADR 0021:109-128; README, «Запуск»; журнал W1b:207 |
| OQ-032 | Что показывает консоль, получив 403 `origin_rejected`, спецификация не определяет. С того же origin такого ответа быть не должно. | вопрос | владелец / W-этап | `docs/specs/web/admin-login.feature`; журнал W1b:212 |
| OQ-033 | Флаг `Secure` у cookie `sard_session` при настоящем HTTPS проверен только юнит-тестами (`SessionCookiesTest`, `SessionControllerTest`): на HTTP-порту сервера TLS не настраивается, интеграционного теста нет. | дыра | вместе с OQ-031 или TLS на HTTP-порту | журнал W1b:61, :215 |
| OQ-034 | Ручная QA входа (`docs/qa/admin-login.md`: сценарии `@qa-only`, сбой базы через `docker compose`) не прогонялась. Адрес клиента в интеграционных тестах — `127.0.0.1`, не `203.0.113.10` из спецификации: проверена механика, не значение. | задача | владелец, перед выпуском этапа 1 | журнал W1b:65-72, :217 |
| OQ-035 | `SESSION_REQUEST_ATTRIBUTE` выставляется `SessionAuthFilter`, но в production-коде не читается — оставлен как точка расширения для принципала enterprise `TenantResolver`. Если к RBAC не понадобится — убрать. | отложено | enterprise RBAC | `server/src/main/kotlin/dev/sard/server/auth/SessionAuthFilter.kt:24,59`; ADR 0021:89; журнал W1b:222 |
| OQ-036 | `scripts/gate.sh`, `scripts/crap.sh` и прямой `./gradlew` не берут `flock` на `.gradle/sard-build.lock`, в отличие от целей `make`. Параллельный `make gate-fast` из хука SubagentStop рвёт им `server/build/test-results` (`EOFException`, `NoSuchFileException`). Обход — `flock -w 1800 .gradle/sard-build.lock <команда>`. Кандидат — брать блокировку в самих скриптах (`scripts/gate.sh` защищён, правит владелец). | дыра | владелец (инструменты) | `Makefile:16-19`; журнал W1b:224 |
| OQ-037 | REST API токенов регистрации не реализован: `EnrollmentTokensController` — контракт S8a, единственная реализация `EnrollmentTokensApi` — `UnimplementedEnrollmentTokensApi`, все четыре операции отвечают 501. W1b снял только блокер `@blocked-d2` (сессия администратора); сценарии с этим тегом ждут S8b. Прежняя запись «закрыто» в этом реестре была ошибкой. | задача | S8b | `server/src/main/kotlin/dev/sard/server/api/UnimplementedApi.kt:24-37`; `EnrollmentTokensController.kt:79-83`; `docs/specs/server/agent-enrollment.feature:86` |
| OQ-038 | RESTORE агента восстанавливает только в новый каталог `<executor.state_dir>/restore/<command_id>` (0700) и требует `snapshot_id`: цель восстановления (путь, база) и «последний снимок по тегам» из `RunStep` не определены. VERIFY восстанавливает туда же и удаляет копию. Для files вопрос на этапе 1 не наступает: по решению владельца (A6b, Ф5) RESTORE у files объявлен, но пустой — FAILED «не реализовано», без restic и без каталога восстановления. Для files вопрос переходит на этап восстановления; для остальных встроенных плагинов (общий `restic restore`) остаётся открытым. | вопрос | поведенческая схема восстановления | `agent/internal/pluginhost/handler.go:159,173`; ADR 0027; `docs/specs/agent/files-plugin.feature:112` |
| OQ-045 | Ручная QA шагов плагина files (`docs/qa/files-plugin.md`, часть 2: BACKUP и пустой RESTORE) невыполнима до S6a/S7: RunStep агенту отправить нечем (REST прогонов — заглушки 501). До этого их проверяют только тесты `@unit`/`@restic`. Принято владельцем 2026-09-30 как есть. | задача | S6a, S7; T2b | `server/src/main/kotlin/dev/sard/server/api/UnimplementedApi.kt:40,68`; `docs/specs/agent/files-plugin.feature:168` |
| OQ-046 | Обязательное требование к S7/S8 (решение владельца по OQ-040, 2026-09-30): шаг BACKUP, завершённый FAILED со снимком (нечитаемые файлы), приходит от агента с `BackupOutput`; сервер обязан записать этот снимок и отдавать его в REST: `RunStep.backup` заполняется и у FAILED-шага (сейчас описан как «Set when a backup step succeeded»), в REST `BackupOutput` добавляется `repositoryId`. В A6b не входит. | задача | S7, S8 | `server/src/main/kotlin/dev/sard/server/api/RunsController.kt:40-45,67`; `docs/specs/agent/files-plugin.feature:89` |
| OQ-047 | `RunFinished` публикуется после фиксации не более одного раза: падение сервера между фиксацией и публикацией теряет событие. Уведомлениям нужно «хотя бы раз» (ответ владельца 4 в S7a): outbox в транзакции завершения запуска и повторная доставка, ключ идемпотентности — `runId`. | задача | S9b | `server/src/main/kotlin/dev/sard/server/runs/RunFinished.kt`; черновик ADR `docs/adr/00XX-draft-step-results.md` |
| OQ-048 | Удаление истёкших секций `step_logs` (`sard.logs.retention`, по умолчанию 90 дней, `DROP` целиком — ADR 0013) не сделано: S7a только создаёт секции вперёд. | задача | вместе с REST логов (S8b, часть C) или отдельной задачей хранения | `server/src/main/kotlin/dev/sard/server/persistence/StepLogPartitions.kt`; черновик ADR `00XX-draft-step-results.md` |
| OQ-049 | `StepProgress.files_processed`/`files_total` (proto, A6b Ф4, OQ-043) сервер не хранит: в `run_steps` нет столбцов, S7a пишет только фазу и байты. Нужны ли они в REST и консоли — тогда столбцы и запись в `StepProgressWrites`. | вопрос | владелец; S8b (часть C), W2 | `server/src/main/kotlin/dev/sard/server/agents/results/ProtoReports.kt` (`progress`); `proto/sard/agent/v1/agent.proto:189-193` |
| OQ-050 | `crypto_provider` при старте агента не проверяется: конфиг принимает любое значение, агент всегда шифрует встроенным AES restic и сообщает серверу в Register настроенное значение (например, `gost`). `repo init` / `repo list` (A5b) отказывают с `CRYPTO_PROVIDER_UNSUPPORTED`; старт агента — отдельная задача (решение владельца В9, 2026-09-30). | задача | агент (A1), после A5b | `agent/cmd/sard-agent/main.go:131`; `agent/internal/app/app.go:84`; `agent/internal/config/config.go:80`; `docs/specs/agent/repo-init.feature`, В9 и П6 |
| OQ-051 | Последний отчёт прогресса фазы, пришедший раньше чем через `ProgressInterval` (1 с) после предыдущего, отбрасывается без досылки: сервер может показать фазу незавершённой (например, не 100 %) до прихода результата. Остаток OQ-006 (решение владельца 2026-10-01). | задача | агент, исполнитель | `agent/internal/executor/reporter.go:23-34` |
| OQ-052 | Набор маскируемых значений A7c — временное правило до спецификации A7b (`features/A7b-log-masking.md` нет в репозитории): все секреты агента, `env_file` и пароль из URL репозитория шага; без base64 и других кодировок. Значение, разорванное между двумя вызовами `sdk.Host.Log`, не маскируется. | задача | A7b | `agent/internal/stepsecrets/source.go`; ADR 0031, «Отложено» |

## Закрыто при сверке

### 2026-10-01, A7c (маскирование логов шага)

| Суть | Чем закрыто |
|---|---|
| OQ-006. Колбэк прогресса restic срабатывает около 10 раз в секунду; прореживать его перед отправкой на сервер (журнал A5a:212). | Сделано исполнителем A4: `reporter.ProgressFiles` пропускает не больше одного отчёта на шаг за `ProgressInterval` (1 с по умолчанию) в пределах фазы, новая фаза проходит сразу (`agent/internal/executor/reporter.go:23-34`, `executor.go:124`; тест `TestProgressIsLimitedToOnePerIntervalExceptPhaseChanges`). Дальше `outbox.Progress` держит только последний отчёт команды (`agent/internal/transport/outbox.go:58-69`), сервер пишет не чаще 5 с (S7a). Остаток — OQ-051. Журнал A7c, фаза 1. |

### 2026-09-30, спецификация A6b (плагин files)

| Суть | Чем закрыто |
|---|---|
| OQ-040. Нечитаемые файлы внутри дерева: FAILED со снимком или SUCCEEDED с предупреждением (Ф1). | Владелец утвердил 2026-09-30: FAILED, сообщение называет число и первые десять путей, снимок сохраняется и передаётся в gRPC `BackupOutput`. REST-сторона — OQ-046. `docs/specs/agent/files-plugin.feature:12,89`. |
| OQ-041. Настройка «не переходить на другие ФС» (Ф2). | Владелец утвердил 2026-09-30 как предложено: необязательное `one_file_system`, false по умолчанию, true → `--one-file-system`. `docs/specs/agent/files-plugin.feature:98`. |
| OQ-042. Локализация `title`/`description` config_schema (Ф3). | Владелец утвердил 2026-09-30 как предложено: английский в стандартных ключах, русский — `x-sard-i18n.ru` поля, W2 выбирает по языку интерфейса с откатом на английский. ADR о соглашении `x-sard-i18n` пишет coder в A6b. `docs/specs/agent/files-plugin.feature:101`. |
| OQ-043. Счётчик файлов в прогрессе (Ф4). | Владелец утвердил 2026-09-30 как предложено: `StepProgress.files_processed = 6`, `files_total = 7`, аддитивно. `docs/specs/agent/files-plugin.feature:108`. |
| OQ-044. Решения specifier Ф5–Ф17 по плагину files. | Владелец утвердил 2026-09-30 Ф6–Ф17 как предложено; Ф5 изменил: files объявляет BACKUP и RESTORE (решение 2 A6a, ADR 0027 и `AgentSeamIntegrationTest` без изменений), RESTORE на этапе 1 — пустой: FAILED «restore for the files plugin is not implemented yet», restic не запускается, на диске ничего не меняется; VERIFY и RUN — REJECTED. `docs/specs/agent/files-plugin.feature:111-167`. |

### 2026-09-29, A6a (ветка `claude/agent-plugin-sdk-0q6asa`)

| Суть | Чем закрыто |
|---|---|
| OQ-018. `BackupOutput.repository_id` нет в proto, а `snapshots.repository_id NOT NULL`. | `proto/sard/agent/v1/agent.proto:275` (`repository_id = 4`, аддитивно, `buf breaking` проходит); агент заполняет его из итогов restic: `agent/internal/pluginhost/handler.go:104`; тест `TestBackupOutputCarriesTheSnapshotAndRepositoryID`. Запись в `snapshots` — S7. |
| OQ-039. Решения specifier С1–С5 по проверке ключа и сертификата ждали утверждения. | Владелец утвердил 2026-09-30: `docs/specs/agent/agent-tls-identity.feature:13,24`. |
| OQ-027. Ключ не от сертификата после прерванного `enroll --force` агент при старте не распознавал. | `agent/internal/tlsid/tlsid.go:22,43`, вызов до подключения — `agent/cmd/sard-agent/main.go:120`; схема `docs/specs/agent/agent-tls-identity.feature`, тесты `agent/cmd/sard-agent/identity_test.go`, `agent/internal/tlsid/tlsid_test.go`. |

### 2026-09-29, `ff97ae1` (X2)

| Суть | Чем закрыто |
|---|---|
| OQ-004. Сервер не был настроен на keepalive-пинги агента раз в 30 с (grpc-java по умолчанию разрывает стрим по `too_many_pings`). | S5a: `server/src/main/resources/application.yaml:36-41` (`permit.time: 20s`, `without-calls: true`, серверный `time: 30s`, `timeout: 10s`); `server/src/test/kotlin/dev/sard/server/agents/stream/KeepaliveIntegrationTest.kt:58`; `AgentStreamIntegrationTest.kt:69` |

### 2026-09-28, `f4de483`

Эти пункты были открыты в журналах, но на `f4de483` уже решены.

| Суть | Чем закрыто |
|---|---|
| Выбор инструмента мутаций для Kotlin (mutflow / kaputt) | ADR 0006 — mutflow |
| CI не запускался | CI зелёный на [Artur-Abalov/Sard#15](https://github.com/Artur-Abalov/Sard/pull/15) |
| `Repository.password` / `RunStep.secrets` против ADR 0008 | ADR 0008; в `agent.proto:19-21` только имена, значений нет |
| Интеграция A3 с A4 | `agent/cmd/sard-agent/main.go:117-144` |
| Подтверждение результата в proto и `Ack` (A4) | `ResultAck`, `proto/sard/agent/v1/agent.proto:312-316` |
| Опции исполнителя в конфиге агента | `executor.state_dir`, `executor.max_parallel` — `agent/internal/config/config.go` |
| Упаковка агента с restic и `LICENSE.restic` | ADR 0018 |
| Остатки `.tmp-*` в каталоге CA | `CaDirectory.removeStaleStaging`, `server/src/main/kotlin/dev/sard/server/pki/CaDirectory.kt:85-92` |
| Сборка образа из `deploy/server/Dockerfile` не проверялась | job `image` в CI зелёный |
| Незакоммиченная правка `scripts/gate.sh` (`LC_ALL`) | коммит владельца `ac6cfb4` |
| Актуальность `routeTree.gen.ts` в CI | `.github/workflows/ci.yml:121` |
| Тенант агента из сертификата; системный поиск по serial | S3; ADR 0013, «Явный тенант и системный доступ» |
| hostname с NUL-символом | коммит `c45821f` |
| CA в тестах на `Clock.systemUTC()` | решение 6 P3, не вопрос: `PkiAutoConfiguration.kt:54` |
| Maven Central 429 | только окружение сессии, в CI не проявляется |
