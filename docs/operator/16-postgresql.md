<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 16. PostgreSQL сервера

Сервер Sard хранит управляющие данные в PostgreSQL: агентов, источники,
расписания, историю запусков. Данных бэкапов в базе нет. Устройство и
обоснования — в [docs/sard-postgresql-architecture.md](../sard-postgresql-architecture.md).

## Версия

`docker-compose.yml` запускает PostgreSQL 18.6, образ
`postgres:18.6-alpine`, закреплённый по digest. Образ агента-соседа
`sard-agent:<версия>-pg18` несёт `pg_dump` той же версии: им делается
самобэкап.

Внешний PostgreSQL (`SARD_DB_URL` на свой сервер) поддерживается в версиях
16, 17 и 18. На PostgreSQL 15 и старше сервер не стартует:

```text
Database startup refused: DATABASE_VERSION_UNSUPPORTED: PostgreSQL 15.14, Sard needs PostgreSQL 16 or newer (docs/operator/16-postgresql.md)
```

## Параметры новой базы

На пустом томе образ создаёт базу с такими параметрами:

| Параметр | Значение |
|---|---|
| контрольные суммы страниц | включены |
| кодировка | `UTF8` |
| локаль | встроенная `C.UTF-8` |
| часовой пояс | `UTC` |

Эти параметры задаются один раз, при создании базы, и потом не меняются.
Поменять их можно только переносом данных в новый том (см. ниже).

## Как проверить свою базу

```bash
cd ~/sard
docker compose exec -T postgres psql -U sard -d sard -c "select version()" \
  -c "show data_checksums" -c "show server_encoding" -c "show log_timezone" \
  -c "select datlocprovider, datlocale, datcollate, datctype from pg_database where datname = 'sard'"
```

Новая установка показывает `on`, `UTF8`, `UTC` и `b | C.UTF-8 | C | C.UTF-8`.

Сервер проверяет то же самое при каждом старте. Если база отличается,
в журнале сервера одна строка `WARN`:

```text
the database differs from what a new installation creates: locale=libc en_US.utf8/en_US.utf8 (want C.UTF-8); the server works on it, see docs/operator/16-postgresql.md
```

и метрика `sard_database_settings_mismatch{reason="locale"}` равна `1`
([раздел 14](14-monitoring.md)). Сервер при этом работает как обычно.

| `reason` | Что не так | Чем это плохо |
|---|---|---|
| `data_checksums` | контрольные суммы выключены | повреждение страницы на диске не будет замечено при чтении |
| `encoding` | не `UTF8` | имена не на латинице могут не сохраниться |
| `locale` | локаль не `C.UTF-8` | порядок строк зависит от libc образа; индексы по тексту может понадобиться перестроить после смены образа или libc |
| `timezone` | `log_timezone` не UTC | `psql` и журнал PostgreSQL показывают время не в UTC; данные сервера от этого не зависят |
| `version` | PostgreSQL новее 18 | версия не проверялась |

## Профиль настроек

Настройки PostgreSQL — профиль small в самом `docker-compose.yml`
(секция `configs: postgresql-conf`). Они монтируются в контейнер файлом
`/etc/postgresql/postgresql.conf` и действуют на любой базе, в том числе
созданной раньше. Посмотреть действующие значения:

```bash
docker compose exec -T postgres psql -U sard -d sard -Atc \
  "select name, setting, unit from pg_settings where name in ('shared_buffers', 'max_connections', 'work_mem', 'wal_level')"
```

Чтобы поменять значение, не правьте `docker-compose.yml`: при обновлении его
заменит файл нового релиза. Положите рядом свой файл настроек и подключите его
через `docker-compose.override.yml`:

```bash
cd ~/sard
docker compose exec -T postgres cat /etc/postgresql/postgresql.conf > postgresql.conf
# правка postgresql.conf
cat > docker-compose.override.yml <<'EOF'
services:
  postgres:
    # The other files of the service stay: pg_hba.conf and the entrypoint (db/ next to docker-compose.yml)
    configs: !override
      - source: postgresql-hba
        target: /etc/postgresql/pg_hba.conf
      - source: postgres-entrypoint
        target: /etc/sard/postgres-entrypoint.sh
    volumes:
      - ./postgresql.conf:/etc/postgresql/postgresql.conf:ro
EOF
docker compose up -d --wait postgres
```

Строки `listen_addresses = '*'`, `timezone` и `log_timezone` оставьте: без
первой сервер не подключится к базе.

## Роли

Сервер Sard не работает суперпользователем. В базе `sard` пять ролей, их создаёт
`bootstrap.sql` (во встроенной установке — служба `db-init` при каждом
`docker compose up`; на внешнем кластере — вы, [ниже](#внешний-кластер)). Все
объекты Sard лежат в схеме `sard`; ни у одной роли нет прав суперпользователя,
`CREATEROLE`, `CREATEDB`, `REPLICATION` и `BYPASSRLS`.

| Роль | Что ей можно |
|---|---|
| `sard_owner` | владеет схемой `sard` и всем в ней; входить под ней нельзя (`NOLOGIN`) |
| `sard_migrator` | входит, становится `sard_owner` на время миграций; ими занята служба `migrate` (ждёт блокировку не дольше 5 с) |
| `sard_app` | сервер: читает и пишет строки таблиц, вызывает служебные функции. Не создаёт и не меняет таблицы, индексы, функции, не усекает таблицы, не создаёт временные таблицы и large objects, не меняет и не удаляет записи проверок восстановления |
| `sard_backup` | агент-сосед: читает всё (`pg_read_all_data`), сессии только на чтение, не больше 4 соединений. Ничего не пишет и при `default_transaction_read_only = off` |
| `sard_monitor` | входит без пароля, член `pg_monitor`: для вашей системы мониторинга, если вы зададите ей пароль и откроете `pg_hba.conf` |

Следствия для оператора:

- **Миграции применяет служба `migrate`, а не сервер.** Сервер на базе, где
  применены не все миграции его версии, не стартует
  (`Database startup refused: MIGRATIONS_NOT_APPLIED`). В обычном
  `docker compose up -d` `migrate` идёт до сервера сам.
- **Таблица новой миграции доступна серверу без `GRANT`** и принадлежит
  `sard_owner`.
- **Записи проверок восстановления неизменны для сервера.** Они хранятся
  всегда; улики проверки (`stages`, `mismatches`) очищает база по
  `sard.retention.verifications` (не меньше 180 дней), кроме последней
  успешной и последней неуспешной проверки источника
  ([раздел 15](15-data-retention.md)). `sard.retention.verifications` меньше
  180 дней — сервер не стартует и называет свойство.
- **Предупреждение о роли сервера.** Если сервер подключён суперпользователем
  или владельцем схемы `sard` (или членом её владельца), он стартует, пишет
  в журнал одну строку `WARN` со ссылкой на этот раздел и поднимает метрику
  `sard_database_role_excess{reason="superuser"}` или
  `sard_database_role_excess{reason="schema_owner"}` равной `1`
  ([раздел 14](14-monitoring.md)). Под `sard_app` обе метрики равны `0`.

## Где пароли

Паролей базы нет ни в `.env`, ни в `docker-compose.yml`, ни в окружении
контейнеров, ни в журналах. При первом `docker compose up` служба `db-init`
создаёт для каждой роли, которая входит, случайный пароль (64 hex-символа) и
кладёт его файлом `password` (владелец uid 10001, права `0400`) в том, который
подключён только к той службе, которой этот пароль нужен:

| Роль | Том | Кто его видит | Путь в контейнере |
|---|---|---|---|
| `sard_migrator` | `sard_sard-db-migrate` | `migrate` | `/run/sard-db/password` |
| `sard_app` | `sard_sard-db-app` | `server` | `/run/sard-db/password` |
| `sard_backup` | `sard_sard-db-backup` | `self-agent` | `/run/sard-self/db-password` |

Долгоживущие контейнеры (`server`, `self-agent`, `postgres`) не знают пароля
`sard_migrator`; сервер не знает пароля `sard_backup`, сосед — пароля
`sard_app`. В PostgreSQL пароли хранятся как SCRAM. **Пароль суперпользователя
`sard` случаен и не хранится нигде**; по сети суперпользователь не входит
(`pg_hba.conf` в `docker-compose.yml`), его путь — [аварийный доступ](#аварийный-доступ).

Пароли **не входят в самобэкап**: томов `sard-db-*` в его источниках нет.
Восстановление на новой машине ([раздел 12](12-server-recovery.md)) создаёт
новые пароли, и это правильно.

## Смена пароля роли

Пароль роли меняется удалением его файла: `db-init` при следующем запуске
делает новый файл и задаёт его роли. Для `sard_app` (сервера):

```bash
cd ~/sard
docker compose run --rm --no-deps --entrypoint rm db-init /run/sard-db/app/password
docker compose up -d
docker compose restart server
```

`up -d` запускает `db-init` заново (пароль в томе новый, в базе тоже) и
`migrate`; `restart` нужен тому, кто читает пароль при старте. Для
`sard_backup` вместо `app` — `backup` и `docker compose restart self-agent`;
для `sard_migrator` — `migrate`, перезапуск не нужен: `migrate` читает пароль при
каждом запуске. Старый пароль после этого не подходит; сервер после перезапуска
отвечает на `GET /api/v1/status`.

## Аварийный доступ

Суперпользователь `sard` входит только по локальному сокету контейнера
`postgres`, и официальный образ не просит там пароль:

```bash
cd ~/sard
docker compose exec postgres psql -U sard -d sard
```

Вход суперпользователя по сети закрыт: из других контейнеров compose `sard` не
входит ни с каким паролем (`host all sard all reject` в `pg_hba.conf`). Это путь для осмотра и
починки базы; сервер им не пользуется. В базе у суперпользователя `search_path`
— `sard`, поэтому таблицы Sard видны без префикса.

## Внешний кластер

Свой PostgreSQL 16, 17 или 18 (`SARD_DB_URL`) готовят одним скриптом
[`deploy/db/bootstrap.sql`](../../deploy/db/bootstrap.sql) (берите файл из
репозитория по тегу вашей версии:
`curl -fsSLO https://raw.githubusercontent.com/Artur-Abalov/Sard/<тег>/deploy/db/bootstrap.sql`). Внешний кластер как
поддерживаемый режим — после 0.1.0; скрипт и его проверка в CI на 16, 17 и 18 —
уже сейчас. Нужны суперпользователь (или роль с `CREATEROLE` без `SUPERUSER`,
которая владеет базой `sard`, имеет право `ADMIN` на `pg_read_all_data` и
`pg_monitor`, а ролей `sard_*` в кластере ещё нет или на них у неё тоже есть
`ADMIN`) и пустая база `sard`. Роль без `SUPERUSER` не может отозвать у `PUBLIC`
`EXECUTE` на функции больших объектов (`lo_create`, `lo_import` и другие): через них
сервер PostgreSQL читает и пишет файлы на своём диске. Пока у `PUBLIC` есть `EXECUTE`
хотя бы на одну из них, скрипт под такой ролью завершается ошибкой и ничего не меняет;
суперпользователь выполняет один раз в базе `sard` (права на функции хранятся в каждой базе
отдельно; в `template1` это покрывает все создаваемые после этого базы):

```sql
REVOKE EXECUTE ON FUNCTION lo_create(oid), lo_creat(integer), lo_from_bytea(oid, bytea),
    lo_import(text), lo_import(text, oid) FROM PUBLIC;
```

После этого скрипт запускается ролью без `SUPERUSER`. Скрипт также снимает все
членства `sard_app` в других ролях (сервер не член ничего). Если роль без `SUPERUSER`
не может снять какое-то из них (нет `ADMIN` на этой роли), скрипт останавливается и называет
роль: суперпользователь выполняет `REVOKE <роль> FROM sard_app` и запускает скрипт снова.

```bash
# 1. роли и схема (повторимо: второй запуск ничего не меняет); пароли скрипт не задаёт
psql -h db.example.com -U postgres -d sard -f bootstrap.sql
# 2. пароли ролей, которые входят
psql -h db.example.com -U postgres -d sard -c '\password sard_migrator' \
  -c '\password sard_app' -c '\password sard_backup'
# 3. миграции: команда образа сервера под sard_migrator
printf '%s' '<пароль sard_migrator>' > migrator.pass
docker run --rm -v "$PWD/migrator.pass:/run/secrets/migrator:ro" \
  -e SARD_DB_URL=jdbc:postgresql://db.example.com:5432/sard \
  -e SARD_MIGRATE_PASSWORD_FILE=/run/secrets/migrator \
  ghcr.io/artur-abalov/sard-server:<версия> migrate
```

Сервер запускают с `SARD_DB_URL`, `SARD_DB_USER=sard_app` и
`SARD_DB_PASSWORD_FILE` (или `SARD_DB_PASSWORD`) — пароль `sard_app`, и только
он: пароль `sard_migrator` серверу не нужен. После каждого обновления версии
сервера сначала снова выполняют `migrate`. Без прав скрипт не создаёт ничего и
называет недостающее право. Роли и схема должны остаться как у скрипта:
владелец схемы `sard` — `sard_owner`, у сервера нет прав на её изменение.

## Переход на enterprise

Расширения enterprise несут свои миграции. Они лежат отдельно от миграций ядра,
в `classpath:db/migration-extensions` (`SARD_MIGRATE_EXTENSION_LOCATIONS`), и
применяются командой `migrate` после всех миграций ядра, в той же схеме `sard`,
под теми же ролями, в свою таблицу истории `sard.flyway_schema_history_extensions`.
Без расширений этой таблицы нет. **Переход на enterprise — применение
дополнительных миграций ядра базы, обратного пути нет:** убрать расширение из
образа можно, но его таблицы и история остаются в базе. Миграции расширения не
должны менять объекты ядра; базой данных этого не проверить, проверяет CI
модуля enterprise.

## Перенос базы, созданной до DB1

Установки, сделанные до этой версии, получили локаль образа
(`en_US.utf8`). Такая база работает, обновление её не трогает. Чтобы перейти
на параметры новой установки, данные переносят в новый том. Ключ CA и
`.env` остаются на месте.

1. Сделайте бэкап базы и убедитесь, что он разворачивается:
   [раздел 6](06-data-and-backup.md), «Ручной бэкап» и «Проверка бэкапа».
2. Остановите сервер и агента-соседа, удалите том базы:

   ```bash
   cd ~/sard
   docker compose stop server self-agent
   docker compose rm -sf postgres
   docker volume rm sard_postgres-data
   ```

3. Поднимите PostgreSQL на новом томе и залейте дамп
   ([раздел 12](12-server-recovery.md), шаг 3):

   ```bash
   docker compose up -d --wait postgres
   docker compose exec -T postgres pg_restore -U sard -d sard --no-owner --no-acl < sard-db-<STAMP>.dump
   ```

4. Запустите остальное и проверьте:

   ```bash
   since=$(date -u +%Y-%m-%dT%H:%M:%SZ)
   docker compose up -d --wait
   docker compose logs --since "$since" server | grep 'the database differs' || echo "нет отличий"
   ```

   `--since` нужен: без него `logs` покажет и предупреждение прошлого старта
   того же контейнера.

   Агенты, источники и история запусков на месте; строки `WARN` об отличиях нет.
   Роли, владельца объектов и права `db-init` возвращает сам при запуске, а
   пароли ролей новые ([Где пароли](#где-пароли)).

Это та же процедура, что восстановление сервера из самобэкапа (F7), только
на той же машине и с тем же ключом CA. Дамп `pg_dump` без `--create` не несёт
параметров старой базы: база `sard` нового тома уже создана с новыми.
