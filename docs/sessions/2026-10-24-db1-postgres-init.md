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
