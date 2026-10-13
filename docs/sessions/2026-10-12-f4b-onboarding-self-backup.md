<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-12 — F4b: онбординг, самобэкап и подтверждение копии ключей

Ветка `ccr-ee6b499f-31e44v`, база — `main` с F4a (#61) и F6 (#62). Продуктовая
фича: идёт через `/ship-feature`. Эта запись — этап specifier; кода нет.

## Specifier: что прочитано (file:line)

**Онбординг F4a.**
- Шаги `self_backup` и `keys_confirmed` — заготовки `UPCOMING`
  (`server/src/main/kotlin/dev/sard/server/onboarding/OnboardingService.kt:76-82`).
- Записанные шаги — таблица `onboarding_steps (step, completed_at)`, сейчас
  только `ca` (`onboarding/OnboardingSteps.kt:10-31`); состояние — на
  установку, не на тенант.
- Спецификации F4a, которые F4b меняет:
  `docs/specs/server/onboarding-setup.feature:63-65,697,713`,
  `docs/specs/web/onboarding-setup.feature:363-377,107-112,474-477`.

**Самобэкап F6.**
- `SelfBackups` — на тенант; привязка, «запустить сейчас», состояние
  (`selfbackup/SelfBackups.kt:109-282`). Привязка без изменений ничего не
  пишет, новая — двигает `boundAt` (`:233-245`).
- REST: `GET /api/v1/self-backup`, `PUT …/repository`, `POST …/runs`
  (`api/SelfBackupController.kt:119-160`). В ответе нет репозиториев соседа,
  состава, команды и признака «первый бэкап прошёл».
- Предупреждения и здоровье — `selfbackup/SelfBackupHealth.kt:22-28,106-138`.
  Баннер F6 показывается только при предупреждениях; `not_configured` баннера
  не даёт (`web/src/selfBackupBanner.ts:13`).
- Расписание — `0 3 * * *` в поясе сервера, отдельное на каждый источник
  (`selfbackup/SelfBackupProperties.kt:46`; `PUT /sources/{id}/schedule`,
  `api/SchedulesController.kt:187-200`).
- Имена системных источников — английские: «Sard: database», «Sard: keys and
  configuration» (`docs/operator/06-data-and-backup.md:14-15`), а не «Sard:
  база» и «Sard: ключи и конфигурация» постановки F4b.

**Агент.**
- `repo add` (A8b): флаги `--provider aws|b2`, `--region`,
  `--access-key-id` (обязателен для s3), секретный ключ — только stdin, файл
  или терминал; адреса sftp `sftp:[user@]host:path` и
  `sftp://[user@]host[:port]/path` (`agent/cmd/sard-agent/host_help.go:98-187`).
- `repo password <имя> --reveal` — от root или пользователя службы
  (`host_help.go:22,234-246`). У соседа пользователь службы — `sard-self`,
  uid 10001 (`deploy/agent/Dockerfile:91-92`), compose запускает его под
  `10001:10001` (`deploy/docker-compose.yml:93`).
- Неизвестную причину Enroll агент печатает как «unforeseen reason from the
  server» с самой причиной (`agent/internal/enroll/classify.go:119-120`): новая
  причина `ONBOARDING_INCOMPLETE` работает без правки агента.

**Сервер, изменяющие запросы** (кандидаты на 409 `onboarding_incomplete`):
`POST/PUT/DELETE /sources`, `POST /sources/{id}/runs`,
`PUT /sources/{id}/schedule`, `POST /agents/{id}/revoke`,
`POST /enrollment-tokens`, `POST /enrollment-tokens/{id}/revoke`, плюс
сессия, пароль, онбординг и самобэкап.

**Register** несёт про репозиторий только `name`, `backend`,
`repository_id`, `crypto_provider` (журнал F6, `agent.proto:383-395`) —
адреса хранилища сервер не знает.

## Specifier: расхождения постановки с кодом и ADR

1. **Команда `repo add`.** Постановка: `docker compose exec sard-self
   sard-agent repo add …`. F6 проверил e2e: `exec -u 0` в работающего соседа не
   работает (`cap_drop: ALL`, каталоги 0700), сервис называется `self-agent`.
   Рабочая форма — разовый `docker compose run … self-agent repo add …` и
   `restart self-agent` (`docs/adr/00XX-draft-self-backup.md`, «Последствия»).
   OQ-204.
2. **ADR 0008 и карточка.** «Где хранилище» на карточке: адреса сервер не
   знает, а ADR 0008 держит репозитории «по имени». OQ-203.
3. **D8 в журнале F5** (`docs/sessions/2026-10-07-f5-sidecar-agent.md:65`)
   отнесено к F6; постановка F4b забирает его — записано в заголовке
   спецификации.
4. **Правило 13 и граница совместимости F4a.** Обновление поддерживается только
   с 0.1.0-beta.N, а беты нет (OQ-145). Если F4b войдёт в первую бету,
   «обновлённых установок» не будет. OQ-201.
5. **Правило 15 и правило 14.** «Снова подтверждение» после смены хранилища
   против «без блокировки после онбординга». OQ-206.
6. **Идентификатор ключа S3.** `repo add` требует `--access-key-id`, постановка
   запрещает ключи в браузере. OQ-205.
7. **Локальный путь.** Корневая ФС соседа только на чтение: «любой локальный
   путь» невозможен. OQ-208.
8. **Сервер без соседа** (`SARD_SELF_DIR` пуст): онбординг не завершить
   никогда. OQ-207.
9. **Имена системных источников** — английские из F6; постановка говорит
   «состав и параметры — F6», значит, остаются как есть; консоль может
   подписывать их по `systemRole`.
10. **Процедура восстановления F7** ещё не написана: ссылка карточки ведёт на
    раздел «Восстановление из самобэкапа (F7)» `docs/operator/06`.

## Specifier: результат

- `docs/specs/server/onboarding-self-backup.feature`,
  `docs/specs/web/onboarding-self-backup.feature`,
  `docs/qa/onboarding-self-backup.md`.
- Контракт К10–К17, решения Р20–Р30 (сервер), Рк9–Рк18 (консоль) — нумерация
  продолжает F4a.
- Открытые вопросы OQ-201…OQ-210 внесены в `docs/open-questions.md`.
- Утверждённые спецификации F4a не правились: правки перечислены в заголовке
  серверной спецификации F4b и вносятся после утверждения.

Последствия для следующих этапов (coder):
- Каждый тест, скрипт и e2e, который после мастера F4a выпускает токены или
  создаёт источники (`SardApi`, `smoke-server.sh`, `test-agent-install.sh`,
  `test-console-install.sh`, `test-self-agent.sh`, quickstart README и
  `verify-quickstart`, `docs/demo.md`), получит 409 `onboarding_incomplete`.
  Предложение (OQ-207): фикстуры сервера засевают завершённый онбординг в
  базе, e2e и скрипты проходят шаги 3–4 через REST с локальным репозиторием.
- Образ соседа `-pg18` с openssh-client в этой среде не собирался (журнал F6):
  SFTP-путь мастера проверит только CI.

Статус: ждёт утверждения владельца.

### Утверждение владельца 2026-10-12 (specifier)

Владелец 2026-10-12 утвердил спецификацию F4b и все рекомендованные ответы
OQ-201…OQ-210 («Делай. Со всем согласен»). Решения Р20–Р30, Рк9–Рк18 и
контракт К10–К17 приняты как написаны; альтернатив в сценариях не было —
сценарии с тегами @oq-NNN уже написаны по рекомендациям и оставлены как есть.

Сделано:
- `СТАТУС` обеих спецификаций F4b и процедуры QA — «утверждено владельцем
  2026-10-12»; тег `@oq-NNN` описан как «написан по решению OQ-NNN».
- OQ-201…OQ-210 перенесены из «Открыто» в «Закрыто при сверке»
  `docs/open-questions.md` (раздел «2026-10-12, спецификация F4b») с решением.
- Правки утверждённых спецификаций F4a:
  - `docs/specs/server/onboarding-setup.feature`: поправка в заголовке;
    «Вне фичи (F4b)» ссылается на F4b; сценарий «Администратор после мастера
    видит выполненные шаги и CA» — шаг self_backup pending; точное тело
    «Состояние чистой установки без cookie…» содержит `"restriction": "full"`.
  - `docs/specs/web/onboarding-setup.feature`: поправка в заголовке, Рк5 и Рк8
    пересмотрены, новый тег `@заменено-f4b`; правило «Шаги F4b показаны как
    предстоящие, не обязательные» заменено правилом «Шаги самобэкапа и копии
    ключей следуют за шагом администратора» (сценарий «Завершение мастера не
    требует шагов F4b» — `@заменено-f4b`); сценарий «Защищённая страница с
    действующей сессией не запрашивает состояние онбординга» —
    `@заменено-f4b` (Рк9 F4b); мок по умолчанию — онбординг завершён;
    «Заданный пароль сразу открывает консоль без страницы входа» и «Консоль на
    моках чистой установки начинает с мастера» ведут на шаг самобэкапа.
  - `docs/qa/onboarding-setup.md`: шаги 3, 22 (части 1), 3 и 12 (части 2)
    приведены к F4b.

Следующий этап — coder: реализует F4b по утверждённым спецификациям.

## Coder: реализация F4b (2026-10-12)

Ветка `ccr-ee6b499f-31e44v`; тесты сначала, затем код; сервер — Kotlin, консоль — web.
ADR: `docs/adr/00XX-draft-f4b-onboarding-self-backup.md`.

**Сервер.**
- Миграция `V202610121300__onboarding_self_backup.sql`: `self_backups.location`,
  `self_backups.keys_confirmed_at`, таблица `onboarding_marks` и отметка `upgraded`
  (Р21, один раз при первом старте).
- Шаги и ограничение: `onboarding/StepStates.kt` (Р20, Р21), `OnboardingService`
  (`confirmKeys`, `restriction`), `OnboardingSteps` (шаг `keys_confirmed`, отметка).
- Самобэкап: `SelfBackups` (`firstBackupDone`, `confirmKeys`, репозитории, состав,
  `location`, команда показа пароля, предупреждение `keys_unconfirmed`),
  `RepositoryCommands` (Р23–Р25, Р29), `RecoveryCards` (адрес процедуры, проверка адреса).
- REST: `GET /self-backup/repository-command`, `POST /self-backup/keys-confirmation`,
  `GET /self-backup/recovery-card`; `OnboardingGuard` (D8, Р22) и описание 409 в OpenAPI.
- gRPC: `Enrollment` + `RegistrationGate` (`ONBOARDING_INCOMPLETE`, К17).
- Порты `OnboardingCompletion`, `RegistrationGate`, `GuardTargets` убирают круговые
  зависимости пакетов (`PackageCycleTest`).

**Тесты сервера.** Контекст интеграционных тестов стартует с завершённым онбордингом
(`TestcontainersConfiguration.completedOnboarding`); тесты первого запуска и F4b убирают шаг и
возвращают его. Новые: `OnboardingServiceTest` (раздел F4b), `SelfBackupOnboardingIntegrationTest`,
`RepositoryCommandsTest`, `RecoveryCardsTest`, `OnboardingGuardApiIntegrationTest`,
`OnboardingSelfBackupApiIntegrationTest`, `EnrollmentOnboardingIntegrationTest`,
`OnboardingMarksMigrationTest`, `OnboardingDocsTest`, дополнения `FirstStartRestartIntegrationTest`,
`AdminResetCommandIntegrationTest`. Изменены ожидания F4a/F6 (шаг `self_backup` pending,
`restriction`, предупреждение `keys_unconfirmed` после онбординга, порядок удаления строк в
`SelfBackupFixtures`).

**Консоль.** `onboarding/state.ts` (экраны шагов, баннер), `auth/guard.ts` (Рк9),
`auth/loginGuard.ts` (`setupRedirect`, Рк10), `onboarding/selfBackup.ts` (запрос команды,
привязка, расписание, первый бэкап, подтверждение), `onboarding/schedule.ts`,
`onboarding/recoveryCard.ts`; экраны `pages/setup/*`, `pages/RecoveryCard.tsx`,
`pages/settings/SelfBackupSection.tsx`, `components/OnboardingBanner.tsx`; моки Рк18.
Тест F4a «Защищённая страница с действующей сессией не запрашивает состояние онбординга»
(`@заменено-f4b`) удалён из `auth/guard.test.ts`; `Завершение мастера не требует шагов F4b` —
`@qa-only`, теста не было.

**Расхождения со спецификацией (для владельца).**
1. Сервер-спецификация, таблица «Обновлённая установка разрешает прочие изменения»: запуск
   источника `POST /sources/{S}/runs` указан как 200; в контракте он 201 (как у всех запусков).
   Тест ожидает 201.
2. Сценарий «Состав самобэкапа виден до привязки и не содержит секретов»: «тело не содержит
   password», но поле К12 называется `passwordRevealCommand`. Проверено отсутствие
   `sard-db`, `password_ref` и ключа `"password"`.
3. Рк18: мок по умолчанию «самобэкап привязан к self s3» не реализован привязкой: тесты F6
   привязывают сами; завершённый онбординг и все шаги `done` в моке по умолчанию есть.
4. Ripple скриптов и e2e: вместо шагов 3–4 по REST со стендом локального репозитория скрипты без
   `sard-self` и e2e-стенды пишут шаг `keys_confirmed` в базу (`sard_confirm_onboarding_in_database`,
   `SardEnvironment.confirmOnboarding`); `smoke-server.sh`, `test-self-agent.sh` после мастера
   токены не выпускают и не менялись. Ни один стенд здесь не запускался (нет образов).
5. Проверка `registrationOpen` в `Enrollment` стоит до транзакции (внутри неё десять
   одновременных регистраций встали на пуле соединений).

## Cleaner stage

- `./scripts/crap.sh server`: worst row 6.0 (CC 6, 100% coverage), none above the threshold of 6; the F4b rows (`OnboardingService.confirmCa/state`, `SetupCodes.accepts/normalize`, `RepositoryCommandsKt.sftp`, `SelfBackupPropertiesKt.networkTarget`, `SelfBackups.confirmIn`, `SelfBackupsKt.viewOf`, `StorageParametersKt.validEndpoint`) sit at the threshold, not over it.
- `./scripts/gate.sh server fast` and `./scripts/gate.sh web fast` pass (oxlint complexity clean).
- No refactor warranted: no function over the threshold, no duplication of F4a/F6 helpers found worth extracting. No code changed.
- Next: `architect`.

## Specifier: поправка спецификации (2026-10-13)

По ревью architect закрыты расхождения coder 1 и 2 — только формулировки, решения не меняются:
1. `docs/specs/server/onboarding-self-backup.feature`, таблица «Обновлённая установка разрешает
   прочие изменения…»: `POST /api/v1/sources/{S}/runs` → 201 (контракт до F4b).
2. Там же, «Состав самобэкапа виден до привязки и не содержит секретов»: тело не содержит
   значения пароля, ключа `"password"`, `sard-db` и `password_ref`; `passwordRevealCommand` (К12)
   допустим. `docs/qa/onboarding-self-backup.md`, часть 1, шаг 3 — так же.
Поправка записана в заголовках обоих файлов. Часть 5 QA статус запуска не называет — без изменений.

## Coder: правки по ревью architect (CHANGES REQUIRED, 2026-10-13)

Тесты писались раньше кода; наблюдавшиеся падения указаны.
1. F1 (P0, ADR 0008). `isSafeLocation` перенесён из `api/RecoveryCards.kt` в `selfbackup/StorageLocation.kt`
   и вызывается в `SelfBackups.bind` (`LocationInvalid`, 422 `validation_failed` у `location` в
   `SelfBackupExceptionHandler`). Учётные данные ищутся после любой цепочки префиксов схемы (`s3:`,
   `rest:`, `https:`, `foo://`) в любом регистре; пароль в `user:password@` отклоняется всегда, а любое имя
   пользователя в адресе http(s) — как токен (`https://ghp_TOKEN@host/r`); `sftp://backup@host` допустим.
   Тесты: `StorageLocationTest` (падал на `SFTP:backup@…`, `https://ghp_TOKEN@host/r`,
   `s3:HTTPS://AKIA123:secret@…`), `SelfBackupIntegrationTest` («Привязка с адресом, в котором учётные
   данные, отклоняется…»: падал «Expected an exception … LocationInvalid»), адреса добавлены в
   `OnboardingSelfBackupApiIntegrationTest` (422). Известный предел: секрет с `/` внутри пароля в
   неэкранированном виде (`user:pa/ss@host`) режется по первому слэшу и не распознаётся.
2. F2 (P0, ADR 0013 п. 8). Тест `ArchitectureTest` «native SQL on a tenant table names tenant_id» упал на
   `JdbcGuardTargets` (две строки); `JdbcGuardTargets` заменён `TenantGuardTargets` над
   `SelfBackups.isSystemSource` и `Agents.isLiveBuiltin` (JPA, фильтр тенанта); тесты в
   `SelfBackupIntegrationTest` и `AgentsBuiltinLiveIntegrationTest` (падали на заглушке `false`).
   В ADR 0013 п. 8 названо единственное исключение `TokensRevokedOnCaReplacement`; `selfbackup` добавлен в
   `ISOLATED_PACKAGES`.
3. F3 (P1). `SelfBackupSingleConnectionIntegrationTest` (пул из одного соединения, таймаут 2 с) упал
   `CannotGetJdbcConnectionException` в `viewIn` → `JdbcOnboardingSteps`; `onboardingComplete()` теперь
   читается до `inTenant` в `bind`/`get`/`confirmKeys` и передаётся булевым значением.
4. F4 (P1, Р27) НЕ сделано: конфликт со спецификацией. `docs/specs/server/onboarding-self-backup.feature`
   (Р27 и сценарий «Команда показа пароля появляется после привязки») требует точную строку
   `docker compose exec self-agent sard-agent repo password … --reveal --config …`, а `exec` обходит
   ENTRYPOINT образа (`/usr/libexec/sard/sard-agent`, в PATH его нет). Нужна поправка спецификации
   (specifier), затем одна строка в `SelfBackupCommands.kt` и ожидаемые строки. Форма `repo add`
   (`docker compose run … self-agent repo add`) проходит через ENTRYPOINT и разрешается верно.
5. F5 (P1). `scripts/test-self-agent.sh`, группа 8: путь шагов 3–4 по REST с локальным репозиторием; не
   запускалась (нет образов), `bash -n` чисто, shellcheck недоступен.
6. Рк18. Мок по умолчанию привязан к `self` (s3), оба запуска `succeeded`, копия ключей подтверждена
   (`web/src/mocks/selfBackupSources.ts`, `state.ts`); тесты F6, привязывающие сами, начинают с `unbind()`.
7. F6 (P2). Абзац-исключение (черновик, «pending owner sign-off») в ADR 0008 и в последствиях
   `00XX-draft-f4b-onboarding-self-backup.md`.

## F4b: F4 закрыт вариантом (b) (владелец)

- Команда показа пароля и .feature не менялись. `deploy/agent/Dockerfile`: `RUN ln -s /usr/libexec/sard/sard-agent
  /usr/local/bin/sard-agent` после COPY бинарника; база Debian/Ubuntu (в том числе `postgres:18`, вариант `-pg18`,
  тот же Dockerfile), `ln` есть. `read_only` и `cap_drop` в compose на разрешение через PATH не влияют (ссылка
  в слое образа, не в томе).
- Тест `selfbackup/RevealCommandImageTest` читает Dockerfile и проверяет, что программа из `passwordRevealCommand`
  равна ENTRYPOINT или есть ссылка в стандартном каталоге PATH на него. Падал: `links: {}`; после правки зелёный.
- `docs/operator/02-install.md` упоминает путь. `scripts/test-self-agent.sh` группа 8 выполняет команду как есть
  (`bash -c "$reveal"`), правки не нужны.
- Образ здесь не собирался (нет демона Docker/образов): ссылка проверена только тестом по тексту Dockerfile,
  не в рантайме.

## Specifier: поправка Рк19 (2026-10-13, находка F6, владелец принял исключение из ADR 0008)

- Команду `repo add` собирает сервер; скомпрометированный сервер не может сам за неё поручиться. Решение
  владельца: шаг 3 мастера (и те же подэкраны смены хранилища в настройках) показывает рядом с командой
  короткую подсказку проверить её перед запуском — в ней должны быть только `docker compose run …
  self-agent repo add` со своим адресом, бакетом и путём и `docker compose restart self-agent`.
- `docs/specs/web/onboarding-self-backup.feature`: поправка в заголовке, решение Рк19, сценарий `@qa-only`
  «Рядом с командой repo add есть подсказка проверить её перед запуском».
- `docs/qa/onboarding-self-backup.md`: поправка в заголовке, часть 2 шаг 5, часть 4 шаг 4.
- Команда S3 начинается строкой `read -r -p "S3 access key ID: " SARD_S3_KEY_ID` (OQ-205, утверждено владельцем);
  подсказка называет и её, иначе законная первая строка выглядела бы подозрительной. Это прямое следствие
  OQ-205, отдельного решения не требует.
- Ключ локали `setup.selfBackup.command.verify` (ru, en) рядом с `setup.selfBackup.command.hint`.

## Coder: подсказка Рк19 и принятие исключения ADR 0008 (2026-10-13)

- Ключ локали `setup.selfBackup.command.verify` (ru, en) добавлен рядом с `command.hint` и выведен в
  `web/src/pages/setup/CommandStage.tsx` под блоком команды; компонент общий для шага 3 мастера и
  подэкранов смены хранилища в настройках (Рк17). Обратные кавычки в локалях не используются, код
  выделен кавычками («…» в ru, "…" в en). Тест паритета ключей локалей покрывает новый ключ.
- `docs/adr/0008-crypto-provider.md` и черновик F4b: исключение принято владельцем 2026-10-13; прямо
  сказано, что защита от скомпрометированного сервера — только чтение команды владельцем (подсказка Рк19),
  а Р29 и кавычки защищают от ошибок ввода и инъекции через ввод пользователя.

## Cleaner, второй проход (2026-10-13)

- F7 архитектора: `SelfBackupApiImpl` и его мапперы (`selfBackupOf`, `planItemOf`, `sourceOf`, `runOf`) и
  константы `REJECTED_PARAMETER`, `UNKNOWN_VERSION` перенесены из `api/SelfBackupController.kt` в
  `api/SelfBackupApiImpl.kt` (как `RunsApiImpl.kt`, `SourcesApiImpl.kt`). Контроллер остался с DTO, интерфейсом
  `SelfBackupApi` и маршрутами. Поведение не менялось.
- `web/src/mocks/commands.ts` не сокращён до заготовленных ответов: тесты мока
  (`onboardingSelfBackup.test.ts`) требуют адрес, собранный из введённых параметров, и имя
  поля в 422 (`bucket`); заготовки изменили бы наблюдаемое поведение мока. Дрейф от Р29 остаётся
  риском, на него указано архитектору.
- Новый код после eebccba (`StorageLocation.kt`, `SystemSourceLookup.kt`, `TenantGuardTargets`,
  `SelfBackups`, `selfBackupSources.ts`, `CommandStage.tsx`): все функции сервера CRAP <= 6 (худшие 6.0
  при покрытии 100%), дублирования и лишней сложности не найдено.

## Specifier: известные пределы проверки адреса (2026-10-13, повторное ревью architect)

- Обещание «адрес с `user:password@` отклоняется» сильнее, чем может гарантировать проверка в
  `selfbackup/StorageLocation.kt`. Два предела не закрываются без отказа законным адресам:
  неэкранированный `/` в пароле (`user:pa/ss@host`) и имя пользователя sftp, равное слову бэкенда
  (`sftp:rest:pw@host`). Записаны как известные пределы: комментарий перед структурой сценария
  «Адрес с учётными данными или управляющими символами отклоняется» в
  `docs/specs/server/onboarding-self-backup.feature` и одна вставка в абзац исключения F4b в
  `docs/adr/0008-crypto-provider.md`. Решения, сценарии и примеры не менялись; консоль всегда шлёт
  адрес, собранный сервером из проверенных параметров, поэтому пределы касаются только API-клиентов,
  пишущих адрес вручную.
