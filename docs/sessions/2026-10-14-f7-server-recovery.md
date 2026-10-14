<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-14 — F7: восстановление Sard без Sard

Ветка `ccr-8640ef61-xz98yz`. Продуктовая фича: идёт через `/ship-feature`. Эта
запись — этап specifier; кода нет. Спецификация —
`docs/specs/server/server-recovery.feature`, ручная проверка —
`docs/qa/server-recovery.md`.

Статус: утверждено владельцем 2026-10-14. Слова владельца: «Решения. Пароль
администратора же бекапится вместе с базой. Если он нужен для восстановления -
все ок, просто нужно записать. Остальное верно». Все предложения OQ-211…OQ-222
приняты, кроме OQ-220: пароль администратора не четвёртая вещь — его хэш
возвращается с базой, вход прежним паролем; страница пишет это в «Что нужно»,
`admin-reset` — запасной путь (Р43, новый сценарий `@doc`). OQ перенесены в
«Закрыто при сверке».

Поправка specifier 2026-10-14 (ревью architect): сценарий `@oq-222`
«Догоняющее срабатывание источника с активным в дампе запуском…» говорил
«планировщик выполняет первый проход», а догоняющее срабатывание (здесь —
`skipped_active`) пишет только второй проход: первый записывает простой и
назначает `catch_up_at` (`RestoredDatabaseStartupIntegrationTest.kt:196-200`).
Шаг заменён на «два прохода», как в соседнем сценарии. Поведение и решение
OQ-222 не меняются.

## Specifier: что прочитано (file:line)

- Самобэкап F6: состав и теги снимков — `docs/operator/06-data-and-backup.md:12-32`,
  `docs/adr/00XX-draft-self-backup.md:21-37` (`include_globals: false`);
  e2e — `test/e2e/src/test/kotlin/dev/sard/e2e/SelfBackupTest.kt:86-123`
  (теги `sard.run`, `sard.step`, `sard.source`, `postgresql.part=database`;
  `pg_restore --no-owner` дампа в чистый PostgreSQL — код 0).
- Карточка F4b: `server/src/main/kotlin/dev/sard/server/api/SelfBackupApiImpl.kt:66-75`
  (отпечаток — `CaFingerprint.hex`), Р28 `docs/specs/server/onboarding-self-backup.feature:176-182`.
- Лог отпечатка CA: `server/src/main/kotlin/dev/sard/server/pki/FileCertificateAuthority.kt:91-117`;
  пара CA — база: `pki/CaDirectory.kt:90-154`.
- Импорт CA F8: `docs/operator/08-migrate-and-remove.md:10-101`.
- Роль `sard_self`: только миграция
  `server/src/main/resources/db/migration/V202610071200__self_dump_role.sql:15-34`;
  при старте сервер лишь задаёт пароль, роли нет — WARN
  (`selfagent/SelfAgentSetup.kt:35-42`). В восстановленной базе роли нет —
  самобэкап базы на новой ВМ упадёт (OQ-214).
- Встроенный агент: токен только без живого встроенного агента
  (`docs/specs/server/self-agent.feature:48-71`); прежний агент X в
  восстановленной базе жив (OQ-216).
- Перепривязка: `selfbackup/SelfBackups.kt:223-322` — `boundAt` и `location`
  меняются при переносе источников на нового агента; `keys_unconfirmed` —
  `:419-421` (OQ-217).
- Flyway: настроек в `server/src/main/resources/application.yaml` нет; по
  умолчанию Flyway игнорирует будущие миграции — сервер стартует на базе от
  более новой версии без отказа (правило 12 не выполнено).
- CERT_UNKNOWN: агент — `agent/cmd/sard-agent/main.go:42-44,104-112`,
  `agent/internal/transport/refusal.go:26-33`; сервер —
  `test/e2e/src/test/kotlin/dev/sard/e2e/CaImportWithoutDatabaseTest.kt`.
- Потерянные шаги и догоняющий запуск: `docs/adr/0053-scheduler.md:63-79`,
  `server/src/main/kotlin/dev/sard/server/runs/StepRevocation.kt`.
- Харнесс e2e: Testcontainers (`SardEnvironment`, `SelfAgent`), реального
  `docker compose` в нём нет; compose гоняет `scripts/test-self-agent.sh`, в CI
  он не входит.
- Офлайн-архив содержит и образ агента (`.github/workflows/release.yml:16`),
  таблица `docs/operator/11-offline.md:5-10` его не называет.

## Решения specifier

Р31–Р43 — в заголовке спецификации; вопросы владельцу OQ-211…OQ-222 закрыты
2026-10-14. Следующий этап — coder.

## Coder: что сделано (TDD)

Порядок: сначала красный тест, потом код; метрики — только из инструментов.

### Сервер
- `DatabaseNewerThanServerIntegrationTest` (8 тестов, реальные серверы над одной
  базой): красный на «сервер стартовал», затем `persistence/DatabaseVersionGuard.kt` —
  `FlywayMigrationStrategy` смотрит `flyway.info()` до `migrate()`; ADR 0059.
- `SelfRoleRestoredDatabaseIntegrationTest` (9 тестов, свой кластер PostgreSQL с
  `log_statement=all`, `RecoveryCluster`), `AdvisoryLockTest`, добавки в
  `SelfAgentSetupTest`: красный на входе роли `sard_self`, затем
  `selfagent/DumpRole.kt` (`DumpRole`, `SetupLock`, `PostgresDumpRole`,
  `AdvisoryLock`) и вызов в `SelfAgentSetup`; ADR 0060. Блокировку проверил
  мутацией: без `pg_advisory_lock` `AdvisoryLockTest` краснеет (гонку двух
  стартов интеграционный тест сам не ловит — окно слишком мало).
- `ServerRecoveryDocsTest` (22 теста `@doc`) — красный до написания страниц.

### Документация
- `docs/operator/12-server-recovery.md` (RU) и `docs/en/operator/12-server-recovery.md`
  (EN, блоки учений совпадают байт в байт — проверяет `ServerRecoveryDocsTest`);
  правки README раздела, 06 (якорь «Восстановление из самобэкапа (F7)» цел),
  09, 11, `docs/operations/self-agent.md`.
- Состояние между блоками лежит в `~/sard-recovery` (`state.env`: адрес
  restic, образ агента, D и K; обёртка `restic`), а не в подстановках id
  снимков: блоки идут отдельными процессами `bash`. Подстановки в блоках:
  `repositoryName`, `location`, `repositoryId`, `caFingerprint`, `sardVersion`,
  `адрес-restic`, `образ-агента`, `имя-сервера`.

### Учения
- `DrillPageTest`, `DrillResultTest`, `JsonTest` — без Docker, красные до кода.
- `ServerRecoveryDrillTest` + `RecoveryDrillStand`, `RecoveryOldVm`,
  `RecoveryNewVm`, `RecoveryVm`, `VmApi`; образ ВМ `sard-recovery-vm:e2e`
  (Ubuntu, Docker в Docker, без openssl; `test/e2e/recovery-vm`).
- Что нашли учения в первых прогонах (исправлено в странице): `restic cat config`
  печатает JSON с пробелами (`"id": "…"`), и блок молча падал; проверка
  `repo add` совпадала с уже привязанным старым репозиторием (`repository`), а не
  с `repositories` соседа, и блок возвращался до появления репозитория.
- Прогон 2026-10-14 на машине разработчика (не CI), сборка `0.0.0-e2e`:
  27 тестов `ServerRecoveryDrillTest` зелёные, `BUILD SUCCESSFUL in 8m 10s`;
  `result.json`: дамп 135403 байта (0,1 MiB), база 9967295 байт (9,5 MiB),
  до сервера 0,9, до агентов 2,2, до самобэкапа 1,5 минуты — строка в таблице
  «Сколько это займёт» страницы взята оттуда.
  Второй прогон на тех же коммитах: 27 зелёных, 8 мин 45 с; дамп 135414 байт,
  база 9967295 байт, 0,9 / 1,9 / 1,6 минуты — разброс по времени порядка 0,3
  минуты, строка таблицы остаётся от первого прогона.

### Отклонения от спецификации
1. Р31: сказано, что неудачные строки (`success false`) проверяет Flyway.
   Проверено: с `*:future` Flyway игнорирует и неудачную строку будущей версии,
   сервер стартует. Сценарий требует остановки, поэтому неудачная строка версии
   больше известной тоже даёт `DATABASE_NEWER_THAN_SERVER` («recorded as
   failed»). ADR 0059.
2. Сценарий `@doc` «Страница поднимает сначала только PostgreSQL»: строка
   `docker compose up -d --wait postgres` — в прозе; блок с `--pull never`
   (Р37), потому что учения выполняют офлайн-вариант.
3. Сценарий «Каждый помеченный блок… выполняется» стоит последним по порядку
   методов (он подводит итог по всем блокам, остальные идут по ходу страницы).
4. Артефакт CI: к `e2e-logs` (только при падении) добавлен `recovery-drill-<шард>`
   (`always()`), иначе строка таблицы пропадала бы при успехе.
5. В `SardEnvironment` добавлен необязательный `internalNetwork`, в `AgentHost` —
   `endpoint`, `cache` и `configText()` (аддитивно, прочие классы не меняются).
6. Хранилище учений — Garage по IP в сети учений (у вложенных контейнеров ВМ нет
   DNS-имён внешней сети); адрес в карточке поэтому с IP.
7. Не проверялись учениями: SFTP и `--provider aws|b2` (страница описывает их,
   проверено чтением кода `repo add`, не запуском); `docker-compose.override.yml`
   в снимке K.

### Среда этого прогона
Docker Hub отвечал 429, базовые образы взяты с `mirror.gcr.io` и перетегированы;
образ агента `-pg18` собран без шага `apt-get install openssh-client` (Debian-зеркала
недоступны; для учений SFTP не нужен) — только локально, Dockerfile репозитория не менялся.

### Cleaner

`./scripts/crap.sh server`: единственная функция F7 выше порога 6 —
`DatabaseVersionGuardKt.refuseNewerDatabase` (CRAP 7.8, CC 4, покрытие 38.0%).
Выделены `newestFuture` и `newestKnown` (поведение прежнее). После: `refuseNewerDatabase`
2.0 (CC 2, 100%), `newestFuture` 3.0, `newestKnown` 1.0. Остальные функции F7 (DumpRole,
SelfAgentSetup, AdvisoryLock) — CRAP <= 2.5, не менялись. Код `test/e2e` не измеряется
CRAP-ом и не менялся. `./scripts/gate.sh server fast`: PASSED. Далее — architect.

### Правки после ревью architect
- `DrillPage.blocks()` отказывает на метке `<!-- drill: id -->` без блока `bash`
  сразу за ней (тест первым, красный); `ServerRecoveryDocsTest` требует равенства
  числа меток и блоков в обеих страницах.
- ADR 0059 называет `FUTURE_SUCCESS` и `FUTURE_FAILED`; ADR 0061 — `state.env` и
  доступ к S3 по IP; KDoc `AdvisoryLock` — один контакт пула (пул не меньше 2).
