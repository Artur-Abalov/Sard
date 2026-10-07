<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-07 — F1: плагин postgresql (реализация)

Ветка `ccr-879211c4-z3mifb`, база — `main` @ `73f5be5` (утверждённая спецификация F1).
Спецификация — `docs/specs/agent/postgresql-plugin.feature`, процедура —
`docs/qa/postgresql-plugin.md`, решения — `docs/adr/0048-postgresql-plugin.md`.

## Что сделано

- **sdk** (Apache-2.0): аддитивные `Dump.Tags` и `Dump.Extra` (`ExtraFile`).
- **agent/internal/pluginhost**: метки плагина `<плагин>.<ключ>=<значение>` после
  меток шага, отказ при совпадении с меткой шага; дополнительные файлы отдельными
  снимками после основного с `<плагин>.main_snapshot`, суммы байт; сбой потока
  плагина больше не называет репозиторий.
- **agent/plugins/postgresql**: схема конфига (12 полей, `password_ref` с
  `format: sard-secret`), Prepare (секрет, поиск `pg_dump`, `psql`, `pg_dumpall`
  в одном каталоге, версии, подключение, роль), Dump (глобальные объекты в
  память), Stream (`pg_dump` потоком в restic, проверка `PGDMP`), собственный
  запуск процессов, RESTORE отложен.
- **упаковка**: `Suggests` в deb и rpm; проверка в `scripts/package-agent.sh`.
- **docs**: `docs/plugins/postgresql.md`, ADR 0048, пример
  `examples/workflows/postgres-nightly.yaml`, `docs/dependencies.md`.
- **e2e**: образ агента на `postgres:18` и `postgres:14`
  (`test/e2e/agent/Dockerfile.postgres`, `make e2e-pg-agent-images`),
  `PostgresqlSourceTest`, `PostgresqlRestoreTest`.

## Как шла работа

Тест первым: `Dump.Tags` и `Dump.Extra` в pluginhost, затем схема и отказы
конфига через настоящий исполнитель с фейковыми `psql`, `pg_dump`, `pg_dumpall` и
restic, затем интеграционные тесты `@restic` с закреплённым restic и
исполняемыми файлами-фейками. Вывод `psql` и `pg_dump` в `testdata/` снят с
настоящего PostgreSQL 16.15 (локальный сервер среды); сквозная проверка настоящих
инструментов на нём же: дамп и глобальные объекты через плагин, `pg_restore` в
новую базу — строки, md5, последовательность, владелец совпали, исключённая
схема отсутствует.

## Что не проверено

- **e2e не запускались**: в среде нет Docker. Код Kotlin компилируется
  (`./gradlew :e2e:compileTestKotlin`), образы и контейнеры не собирались.
- Вывод с PostgreSQL 14 и 18 (`testdata/` по спецификации) не снят: в среде
  только 16.15. Формулировки libpq проверят e2e на 14 и 18.
- Сценарий «остановленный во время загрузки шаг» (отмена, таймаут 3 с) как e2e:
  сервер не умеет ни отменять прогон, ни задавать шагу таймаут. Сторона агента
  покрыта тестами `@unit` и `@restic`.
- Ручная QA `docs/qa/postgresql-plugin.md` не проходилась.

## Замечания к спецификации и QA

- Шаг 13 QA ждёт в сообщении `secret_t`, но после `REVOKE pg_read_all_data` у роли
  `backup` нет `SELECT` и на `t`: `pg_dump` назовёт первую таблицу, на которую права
  нет (на PostgreSQL 16 в `testdata/pg_dump-pg16-denied.stderr` это `t`). Для e2e
  роль без `pg_read_all_data` и с `SELECT` на всё, кроме `secret_t`.
- Сценарий e2e с `secret_t` требует роль без `pg_read_all_data`: член этой роли
  читает все таблицы.
- Правило «2–4 поля схемы» `TestEveryConfigSchemaIsValidJSONSchema` снято
  (у postgresql 12 полей).
