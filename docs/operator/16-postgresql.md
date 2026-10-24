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
    configs: !reset []
    volumes:
      - ./postgresql.conf:/etc/postgresql/postgresql.conf:ro
EOF
docker compose up -d --wait postgres
```

Строки `listen_addresses = '*'`, `timezone` и `log_timezone` оставьте: без
первой сервер не подключится к базе.

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
   docker compose exec -T postgres pg_restore -U sard -d sard --no-owner < sard-db-<STAMP>.dump
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
   Роль самобэкапа `sard_self` сервер создаёт сам при старте.

Это та же процедура, что восстановление сервера из самобэкапа (F7), только
на той же машине и с тем же ключом CA. Дамп `pg_dump` без `--create` не несёт
параметров старой базы: база `sard` нового тома уже создана с новыми.
