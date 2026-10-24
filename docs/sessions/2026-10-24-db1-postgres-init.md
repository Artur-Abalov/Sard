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
- e2e, 39 классов, которым не нужны `-pg18` и ВМ учений:
  - 146 тестов, 145 прошли, 2 пропущены, 1 упал;
  - упал `StepLossTest`: Docker Hub ответил 429 на загрузку
    `nicolaka/netshoot`. После загрузки образа класс прошёл (3 из 3).
- Быстрый гейт сервера на итоговом коде: `gate: PASSED (server, fast)`,
  покрытие 96.8% (инструкции), CRAP ≤ 6.
  - В предыдущем прогоне один раз упал `OnboardingDatabaseDownIntegrationTest`:
    `BindException: Address already in use` в `DatabaseRelay.up()`.
  - Причина — существующая гонка теста: ретранслятор берёт порт через
    `ServerSocket(0)`, закрывает его и после «падения» базы занимает тот же
    номер. В промежутке порт может достаться другому соединению.
  - Отдельно класс зелёный (5 из 5); DB1 эту часть не трогает, не
    исправлялось.
- mutflow по `DatabaseSettingsTest`: 24 мутации, 350 прогонов, выживших нет.
- actionlint по `release.yml` — 0 замечаний. ShellCheck: новые только SC1091
  (note), как у существующих `source`. markdownlint по новым страницам —
  0 ошибок. `license-check` — OK.

### Итог фазы 2 (СТОП)

Сделано и проверено здесь: проверки 1, 3, 4 и 5 стратегии (5 — без
агента-соседа), тесты и гейт сервера, e2e без самобэкапа.

Не проверено здесь, остаётся на CI PR:
- проверка 2: в DB1 сортировки по ICU нет, есть только правило; проверять
  нечего;
- проверка 6 (F6/F7 на новой версии): `SelfBackupTest` и
  `ServerRecoveryDrillTest` в `ci.yml`;
- проверка 7: verify-quickstart и verify-offline идут только в `release.yml`
  на тег; сам офлайн-путь (тег вместо digest) проверен вручную на двух
  хранилищах образов;
- `scripts/test-self-agent.sh` с проверкой 9: ни в одном workflow, нужен
  запуск руками на машине, где собирается `-pg18`.

## CI PR #86

- `ci.yml` не запускался: конфликт с `main` в `docs/sessions/INDEX.md`
  (после #85). `main` влит, обе строки оставлены.
- `server-image`: `docker-compose.build.yml` не разбирался без `make`. В
  значении `${…:?run make up: …}` стоял `: `, и переменной в CI нет. По
  умолчанию теперь копия пина, `DeployPostgresTest` её сверяет.
- `go (agent)`: нестабилен
  `TestARecoveryKeyThatHangsEndsWithTheConnectTimeoutAndSaysTheKeyMayExist`.
  На `main` джоба зелёная; локально с `-cpu 1,2` тест падал 28 раз из 600.
  - Причина: `splitClock` отдавал всем `After(d)` одной длительности один
    канал. Наблюдатель предыдущего шага (`repoconnect.Bound.limit`) иногда
    забирал тик, предназначенный зависшему `key add`.
  - Исправлено по решению владельца в этом PR: каждому `After` новый канал,
    `fireTimer` срабатывает на последнем.
  - После правки 0 падений из 600, `gate.sh agent fast` — PASSED,
    покрытие 98.7%.
- `server` (mutflow): два выживших в `MetricsConfiguration.kt:45`
  (`exposedMetersOnly`) под `BuiltinEnrollmentTokensIntegrationTest`.
  - Причина: фоновый тик `StateLoop` регистрирует серии во время окна
    `underTest` (ADR mutflow isolation), это код H1.
  - Локально прогон класса без выживших.
  - Предложение — в комментарии PR, в этом PR не исправлялось.
- `agent-mutation-total`: `mutate-exec ERROR` — тесты `cmd/sard-agent` без
  мутаций повисли на 600 с в одном шарде. Локально 60 прогонов под нагрузкой
  без зависаний. Причина не установлена.
- Прогон перезапущен один раз целиком: всё зелёное, PR без конфликтов.

## После CI (решения владельца)

- ADR принят: `00XX-draft-embedded-postgresql` → `0068-embedded-postgresql`,
  статус «принято», строка в `docs/adr/README.md`.
- `scripts/test-self-agent.sh` внесён в CI: джоба `self-agent` в `ci.yml`,
  после `packages` и `server-image`. Образ `-pg18` даёт новая цель Makefile
  `e2e-agent-pg-image`, на неё опирается и `e2e-agent-images`. Обновление
  (`UPGRADE_FROM`) по-прежнему запускается вручную: нужны образы выпуска.
- Локально скрипт не запускался (нет образа `-pg18`); первый прогон — CI
  этого PR.

## Нестабильные тесты (решение владельца: чинить в этом PR)

- Агент, `cmd/sard-agent`: тест
  `TestThePasswordGoesToAFileWithMode0600OwnedByRootWhateverTheUmask` (V3)
  был параллельным и ставил `syscall.Umask(0)`. Umask общий для процесса,
  поэтому каталоги параллельных SFTP-тестов создавались с `0777` и получали
  `SSH_HOME_INVALID` (код 2). Отсюда «exit code = 2» в CI и локально.
  - Найдено зондом, читавшим `Umask` из `/proc/self/status` во время
    параллельной фазы: до правки он сработал в 14 полных прогонах из 18,
    после — в 0 из 18.
  - Тест сделан последовательным. Новый `TestATestThatChangesTheUmaskIsNotParallel`
    разбирает исходники пакета и падает, если тест меняет umask и вызывает
    `t.Parallel()`; на старом коде он красный.
  - `TestIdentityFileModesDoNotDependOnTheUmaskUnderSudo` возвращает umask
    через `defer` (`enrollUnderUmask`).
  - `assertRefusal` при неверном коде печатает stderr.
- Сервер, порты: `TestPorts` резервирует порт ниже эфемерного диапазона на
  время жизни JVM — в своей JVM множеством, между JVM блокировкой файла
  `<tmp>/sard-test-ports/<port>.lock`.
  - Им пользуются `FreeMetricsPort`: прежняя проверка «свободен сейчас» не
    защищала от второй JVM (`maxParallelForks = 2`), отсюда `BindException`
    в CI.
  - И `DatabaseRelay`: он заново занимал эфемерный порт после «падения»
    базы, а этот порт могло взять любое исходящее соединение.
- Сервер, mutflow, выживший `MetricsConfiguration.kt:45` (фильтр метров).
  - Первая правка: `FreeMetricsPort` ждёт первого тика `StateLoop` при старте
    контекста (до 60 с). Ожидание кончалось на первой серии тика, а `publish`
    регистрирует ещё ~30 после неё. Теперь ожидание берёт монитор
    `StateGauges`. С регистрацией, замедленной на 50 мс: 44 вызова фильтра
    внутри тестов до правки, 0 после.
  - Выживший остался: полный изолированный прогон — `RestorePostgresMutationTest`,
    в CI ещё и `W2MutationTest`; поодиночке классы проходят.
  - Трассировка полного прогона показала регистрации метров во время тестов
    из чужих потоков: поток уведомлений GC (`jvm.gc.pause`,
    `jvm.gc.concurrent.phase.time` с новой причиной) и исполнители gRPC
    (`grpc.server.*` на первый вызов метода). Каждая шла через фильтр.
    Это первопричина: момент случайный, класс-«владелец» тоже.
  - Решение: фильтра при регистрации больше нет. Экспозицию отбирает
    `ExposedMeters` при scrape (префиксы `sard_`, `jvm_`, `process_`, как
    раньше). Чужие метры живут в реестре, но не выходят наружу.
    `ExposedMetersTest` (mutflow) и `MetricsScrapeIntegrationTest` (нет
    `http_`/`grpc_`) зелёные. Вывод записан в черновик ADR об изоляции mutflow.
- `scripts/test-self-agent.sh`, первый прогон в CI. Проверка 9 (DB1) прошла:
  `18.6 on UTF8 b C.UTF-8 UTC 256MB 50 replica on`, без предупреждения.
  Скрипт отстал от F4b и V3:
  - повторный отзыв встроенного агента до завершения онбординга — 409
    `onboarding_incomplete` (Р22), а не 200. Проверка исправлена:
    отказ и неизменный `revokedAt`;
  - команды `repository-command` оператор запускает в терминале, где
    `docker compose run` даёт TTY и `repo add` показывает ключ
    восстановления (V3). Без TTY он отказывает
    `RECOVERY_KEY_OUTPUT_MISSING`, поэтому скрипт запускает их через
    `script -qec`.
  - Локально прогон с образом агента без `pg_dump` проходит до самобэкапа
    проверки 8, где шаг падает на `pg_dump_path` (ожидаемо: это только
    локальная подмена образа).
