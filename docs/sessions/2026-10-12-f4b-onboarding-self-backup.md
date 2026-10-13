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
