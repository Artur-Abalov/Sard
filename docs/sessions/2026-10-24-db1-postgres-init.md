# 2026-10-24 — DB1: инициализация встроенного PostgreSQL

Конфигурация развёртывания и проверка при старте сервера, решение D25. Роли
(DB2) и подключения (DB3) не трогаются.

## Фаза 1 — исследование (СТОП, ждёт ревью)

Два агента только на чтение (развёртывание; сервер и схема), HEAD `13e2e8c`.
Пометки: «прочитано» — видно в файле; «полагаю» — вывод, не проверенный запуском.
Docker в окружении нет, поэтому ни одна база из образа не запускалась.

### Что есть сейчас (прочитано)

- Версия PostgreSQL одна, 18, с первого коммита (`8ff2234`) до `v0.0.1-rc1` и
  `v0.1.0-beta.1`. Везде стоит плавающий тег, digest нет нигде в репозитории:
  - `deploy/docker-compose.yml:14` — `${SARD_POSTGRES_IMAGE:-postgres:18-alpine}`;
  - `scripts/offline-archive.sh:23`;
  - `test/e2e/.../E2e.kt:55`;
  - четыре контейнера Testcontainers в `server/src/test`;
  - `scripts/test-agent-install.sh:37`, `scripts/test-console-install.sh:36`;
  - документация: `06:166`, `08:108`, `12:104`, `en/12:117`, `03:17`,
    `dependencies.md:89`, ADR 0039.
- Образ агента `-pg18` собирается на `postgres:18` (Debian), тоже по тегу:
  `release.yml:384`, `Makefile:141,148`, `docker-compose.build.yml:158`.
  - Путь `/usr/lib/postgresql/18/bin/pg_dump` зашит в
    `SelfBackupProperties.kt:55`.
  - F1 проверен на 14 и 18 отдельными тестами, а не матрицей.
- `initdb`: `POSTGRES_INITDB_ARGS` нет, `postgresql.conf` нет, `-c` нет,
  init-скриптов нет. Скрипты `docker-entrypoint-initdb.d` уже отвергнуты в
  `00XX-draft-self-agent.md:48`. `TZ` задан только у сервера
  (`docker-compose.yml:54`). Том — `sard_postgres-data:/var/lib/postgresql`.
- Самобэкап F6 делает `pg_dump --format=custom` без `--create` от роли
  `sard_self`, без глобальных объектов.
  - Восстановление F7 (`12-server-recovery.md` шаг 3) заливает дамп в пустую
    базу `sard`, созданную образом: `pg_restore -U sard -d sard --no-owner`.
  - Свойства кластера в дамп не попадают, поэтому новый том с другими
    параметрами initdb процедуре подходит. Это полагаю; косвенно подтверждает
    `SelfBackupTest` (alpine → Debian).
  - Плагин отказывает, если `pg_restore` или целевой сервер старше источника
    (`restore_check.go`).
- Проверки при старте:
  - единственная проверка базы до миграций — `FlywayMigrationStrategy` в
    `DatabaseVersionGuard.kt:66-73` (ADR 0060);
  - отказ оформлен исключением с префиксом `"... startup refused: CODE: ..."`;
  - предупреждение — один `log.warn` со ссылкой на документацию
    (`SelfAgentSetup.kt`).
- Метрики: имена только в `SardMetricNames.kt`; `reason` есть среди
  разрешённых меток; образец 0/1-gauge — `DispatchConfiguration.kt:78-90`.
- Сортировка имён в базе: только `agent_plugins.name` и
  `agent_repositories.name` (`Agents.kt:23-24`, `Repositories.kt:18`,
  `RestoreChoice.kt:24`, `RestoreTargets.kt:20`, `VerifierPool.kt:16`).
  - Оба столбца по CHECK только ASCII.
  - `sources.name`, `workflows.name`, `tenants.name`, `agents.hostname` в SQL
    не сортируются.
  - `hostname` сортирует Kotlin (`RestoreTargets.kt:48`).
  - Консоль сама ничего не сортирует.
  - `LIKE`, `ILIKE`, `lower()` нет. Регулярные CHECK есть на столбцах `name`;
    с недетерминированной ICU-коллацией `~` не работает.
- Время: все столбцы `timestamptz`; секции `step_logs` явно по UTC. pgjdbc
  ставит `TimeZone` сессии по JVM, поэтому `SHOW TimeZone` из сервера покажет
  `TZ` контейнера сервера, а не настройку базы.

### Найдено попутно

- Две миграции `V202610211200` (`repository_recovery_key`,
  `retention_indexes`). Во flyway-core 12.4.0 есть ошибка
  `DUPLICATE_VERSIONED_MIGRATION` (прочитано в jar). Полагаю, что сервер на
  `main` не стартует и тесты сервера красные; не запускалось — нет Docker.
- Обновление с `v0.0.1-rc1` документировано как переустановка
  (`07-upgrade.md:8-15`). Это противоречит пункту must_preserve промпта.

### Предложения к контрольной точке 1

См. ответ в сессии: версия 18 (закрепить digest), локаль через встроенный
провайдер `C.UTF-8`, ICU — в запросе, а не миграцией. Ждут решения владельца.

## Решения владельца (контрольная точка 1)

- PostgreSQL 18; старше 16 не поддерживается (16–18).
- Существующих установок нет; `0.0.1-rc1` и, вероятно, `0.1.0-beta.1` не
  предназначены для обновления, следующий пре-релиз поднимет минор.
- Дубль версии миграции исправить здесь.
- Документа `sard-postgresql-architecture.md` нет: создать
  (`docs/sard-postgresql-architecture.md`).
- Docker запустить в окружении; ветка — текущая (`ccr-7bc9c9f9-137c5x`).
- ICU: владелец не понял вопроса. Решено по фактам (ниже): без миграции,
  правило `COLLATE "und-x-icu"` в запросе.

## Фаза 2 — реализация

### Окружение

- `dockerd` запущен вручную (Docker 29.9.1, хранилище containerd).
- Maven Central отвечает этому хосту 429 и иногда 404. Gradle кэширует
  отсутствие артефакта, поэтому недостающие jar и pom докачивались в `~/.m2`
  и подключались init-скриптом в `~/.gradle/init.d`. Это только на этой
  машине, в репозитории ничего нет.
- Первый полный `:server:test` упал на 1209 тестах: class-файлы пропали во
  время прогона. Причина: параллельная сборка Gradle в том же `server/build`
  (Makefile: `BUILD_LOCK`). Дальше все прогоны Gradle шли под
  `flock .gradle/sard-build.lock`.

### Сделано

- Дубль: `V202610211200__retention_indexes` → `V202610211300`, тест
  `ArchitectureTest` «every migration has a version of its own». Красный до
  переименования, зелёный после.
- `deploy/postgres.env` — единственное место версии:
  - `postgres:18.6-alpine@sha256:898c…d6a5` для сервера;
  - `postgres:18.6-trixie@sha256:edb0…ddb6` как основа агента `-pg18`;
  - аргументы `initdb`.

  Его читают Makefile (`include`, экспорт для `docker-compose.build.yml`),
  `offline-archive.sh`, `test-agent-install.sh`, `test-console-install.sh`,
  `release.yml` (основа `-pg18`, офлайн-`.env`), тесты сервера
  (`EmbeddedPostgres`) и e2e (`E2e.postgresImage`). Копии лежат в compose и
  `.env.example`. `DeployPostgresTest` сверяет копии и запрещает другие образы
  сервера.
- Compose:
  - `POSTGRES_INITDB_ARGS`, `TZ: UTC`;
  - профиль small в `configs: postgresql-conf` (inline);
  - запуск с `-c config_file=/etc/postgresql/postgresql.conf`.
- Офлайн: digest после `docker load` не сохраняется. Проверено на containerd
  (`save --platform` и без него) и на классическом overlay2. Поэтому архив
  несёт тег `postgres:18.6-alpine`, офлайн-`.env` — строку
  `SARD_POSTGRES_IMAGE=postgres:18.6-alpine`. Изменены `release.yml`
  (verify-offline), `11-offline.md` и учения F7 (`RecoveryDrillStand`,
  `RecoveryOldVm`).
- Проверка при старте (`persistence/DatabaseSettings.kt`):
  - вызывается из `FlywayMigrationStrategy` до проверки ADR 0060;
  - отказ `DATABASE_VERSION_UNSUPPORTED` ниже 16;
  - при других отличиях — WARN и `sard.database.settings.mismatch{reason}`.
- Документация: `docs/sard-postgresql-architecture.md`,
  `docs/operator/16-postgresql.md`, ADR `00XX-draft-embedded-postgresql`;
  правки в разделах 1, 3, 6, 8, 11, 12 (ru, en), 14 и `dependencies.md`.
- `scripts/test-self-agent.sh`: проверка 9 (DB1) для чистой установки и
  обновления.

### Проверено

- Чистый compose (`up --wait postgres`):
  - PostgreSQL 18.6, `data_checksums=on`, UTF8, builtin `C.UTF-8`;
  - `timezone` и `log_timezone` UTC;
  - `shared_buffers=256MB`, `max_connections=50`, `wal_level=replica`,
    `track_io_timing=on`, `log_min_duration_statement=1s`;
  - `pg_hba.conf` берётся из тома.
- Тот же образ без аргументов: libc `en_US.utf8`; контрольные суммы и UTC уже
  есть (PostgreSQL 18).
- ICU в образе есть (908 коллаций). Сравнение порядков C.UTF-8 и `und-x-icu`
  — в документе архитектуры.
- `docker-compose.override.yml` с `configs: !reset []` и своим файлом:
  `work_mem` 8MB, остальное из файла.
- Перенос на уровне базы:
  - база с initdb образа, все миграции и строка с кириллицей →
    `pg_dump -Fc` → новый том нового compose → `pg_restore --no-owner`;
  - rc=0, строка на месте, builtin `C.UTF-8`, суммы `on`.

  Без сервера; полная проверка 5 — в фазе 3.
- `DatabaseSettingsTest` (14) и `DatabaseSettingsIntegrationTest` (7) зелёные.
  Интеграционный покрывает:
  - базу новой установки;
  - роль без прав суперпользователя;
  - initdb образа;
  - внешние PostgreSQL 16 и 15;
  - старт с предупреждением и метрикой;
  - старт без предупреждения;
  - отказ на 15 без созданных таблиц.

  Без вызова проверки в стратегии падают три теста старта (проверено
  временной правкой).
- `DeployPostgresTest` (6): красный до правок копий, зелёный после.
- Первый быстрый гейт сервера: 3031 тест, 2 красных.
  - `PackageCycleTest`: цикл `persistence → metrics → runs → persistence`.
    Исправлен по образцу `RunDurations`: в `persistence` остался интерфейс
    `DatabaseMismatchListener`, реализация `DatabaseSettingsGauges` перешла
    в `metrics`, бин регистрируется в `prometheus`.
  - `MonitoringDocsTest`: ссылка на `16-postgresql.md` — страница появилась
    после старта гейта. При повторном прогоне зелёный.
- Сеть окружения: `deb.debian.org` и `apt.postgresql.org` запрещены
  (`connect_rejected`), apt по HTTP прокси отклоняет (405). Поэтому не
  собираются:
  - образ агента `-pg18` (основа trixie, apt `openssh-client`);
  - образ ВМ учений (apt по HTTP).

  Не запускались: `test-self-agent.sh`, e2e-классы `SelfBackupTest`,
  `SelfRecoveryKeyTest`, `HostRecoveryKeyTest`, `CertificateLifetimesTest`,
  `ServerRecoveryDrillTest` и verify-quickstart/verify-offline (работают
  только в `release.yml`). Самобэкап и восстановление F6/F7 здесь проверить
  нельзя.
- Jar собран локально (`bootJar`, консоль `npm run build`), образ
  `sard-server:e2e` — `make server-image`.
- Чистая установка по compose релиза (`postgres` + `server`, без агента-соседа):
  - база: `18.6 on UTF8 b C.UTF-8 UTC 256MB 50 replica on`;
  - `/api/v1/status` отвечает, предупреждений 0;
  - `sard_database_settings_mismatch` — пять серий, все 0.
- Обновление (проверка 4). Старая база получена тем же compose без
  `POSTGRES_INITDB_ARGS` и без файла настроек (так создавались базы до DB1),
  в ней строка агента. После подъёма нового compose на том же томе:
  - строка на месте, Flyway без ошибок;
  - `shared_buffers` 128MB → 256MB;
  - одна строка WARN `locale=libc en_US.utf8/en_US.utf8 (want C.UTF-8)`;
  - метрика `reason="locale"` 1, остальные 0.
- Перенос (проверка 5), шаги раздела 16 как написаны:
  - дамп → `rm` тома → `pg_restore` (rc 0) → старт;
  - «нет отличий», строка на месте.

  Найдено и исправлено: без `--since` `docker compose logs` показывает WARN
  прошлого старта того же контейнера.
- actionlint по `release.yml` — 0 замечаний. ShellCheck: новые только SC1091
  (note), как у существующих `source`. markdownlint по новым страницам —
  0 ошибок. `license-check` — OK.
