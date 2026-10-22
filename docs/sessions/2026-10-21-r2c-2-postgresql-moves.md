<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-21 — R2c-2: восстановление PostgreSQL переехало в плагин postgresql

Ветка `claude/epic-albattani-e099w7`. Спецификация
`docs/specs/agent/restore-ownership.feature` одобрена владельцем (с отклонениями О1–О5
и рекомендованными ответами на В2–В11, В13, В14); реализованы все сценарии с тегом
`@r2c-2`. ADR 0066 (этапы, журнал, прежний журнал), QA `docs/qa/restore-ownership.md`.
Принцип владельца: в агенте нет кода конкретной базы данных.

## Что переехало

- `agent/internal/consolerestore/pg*.go` (около 1 900 строк: проверки ВК22, режим
  `pg_restore` ВК28, глобальные объекты ВК26, порядок и запись журнала ВК27, откат по OID,
  политика разбора при старте, запуск программ, `Password`, типы) удалены; логика теперь в
  `agent/plugins/postgresql`: `restore_run.go` (`Restore`, `sdk.RestoreRunner`),
  `restore_check.go`, `restore_prepare.go`, `restore_steps.go`, `restore_rollback.go`,
  `restore_recover.go` (`Recover`, `sdk.Recoverer`), `restore_record.go` (схема записи:
  поля прежней записи `pg-restore/<id>.json` без изменений), `restore_snapshot.go`
  (выбор снимка, раскладка файлов, как `restore.PlanPostgres`), `restore_conn.go`,
  `restore_globals.go`, `reasons.go` (коды причин PG_*, GLOBALS_*, DATABASE_EXISTS,
  CREATEDB_REQUIRED, SUPERUSER_REQUIRED, ROLES_EXIST — константы плагина).
- Слияние с `exec.go` и `tools.go` плагина: один `outcome` и один `quoter` (теперь строка
  `ERROR:` — тоже ошибка), один `run` с `call{}` (у `Cmd` появился `Stdin`), один
  `limitedBuffer` вместо `limited` и `limitedBuffer`, один `checkPassword`, один `target`
  с `conninfo`/`sslmode` для бэкапа и восстановления, общие `lookPath`, `neighbour`,
  `runnable`, `reason`, `env`, `packageHint`, `minServerMajor`, `serverVersion`,
  `isDigits`; `locateRestore` рядом с `locate`. Сообщения R2b не менялись.
- `pluginhost/handler_database.go` удалён; PostgreSQL идёт путём `RestoreRunner`; разбор
  при старте — через `Recoverer`. Из `internal/refusal` ушли PG_*, GLOBALS_*, ROLES_EXIST,
  DATABASE_EXISTS, CREATEDB_REQUIRED, SUPERUSER_REQUIRED (потребителей не осталось);
  подсказку check для `PG_CLIENT_MISSING` даёт `sdk.ErrProgramMissing` в цепочке ошибки.
- Журнал: запись плагина лежит в `state_dir/restore-journal` под именем `postgresql`.
  `restorejournal` читает прежний `state_dir/pg-restore/<id>.json` как запись плагина
  (весь файл — запись; `Options.Legacy`, каталог и имя плагина задаёт корень сборки),
  никогда не пишет; `Remove` убирает запись команды в обоих журналах (и `*.json.tmp`
  прежнего); при одной команде в двух журналах действует новая (в том числе испорченная).
  `usable("/")` теперь ложно (замечание hardener R2c-1).
- Доля таблицы блокировок: ключ YAML `restore.postgresql_lock_share` и проверка при старте
  остались в `internal/config`; значение идёт в плагин параметром сборки
  `plugins.WithPostgresLockShare` (`Plugin.LockShare`, nil — 0.5).
- Замечания architect R2c-1: классификатор сбоев restic — тип `consolerestore.Classifier`
  (`Snapshots`, `Dump`), в `pluginhost` — `WithRepositoryWords`; условие `console != nil`
  снято с пути `RestoreRunner`.

## Добавки SDK (Apache-2.0, без кода базы)

`RestoreRequest.Repository` и `RestoreRequest.Source` (имя репозитория шага и тег
`sard.source` шага: плагин берёт снимок своего источника и называет репозиторий в словах
отказа, как R2b; без источника шаг PostgreSQL отказывается `TARGET_INVALID (check)` — раньше
хост отклонял его `REJECTED`), `SnapshotInfo.Paths` (путь файла дампа в снимке).

## Тесты

- Перенесены с теми же названиями (`pg_*_test.go` движка → `restore_*_test.go` плагина,
  внешний пакет с фальшивым хостом SDK и моделью сервера, плюс внутренний
  `restore_internal_test.go`): ВК22, ВК26, ВК27, ВК28, откат по OID, политика разбора,
  сообщения. Не перенесены тесты механики файловой системы журнала (fsync, rename,
  временные файлы, права, обход каталога): их держит `restorejournal` с R2c-1.
- `pluginhost/console_restore_pg_test.go` переписан на настоящий плагин через хост;
  интеграционный тест с настоящим restic — тоже (`go vet -tags integration` и `gate`).
- `cmd/sard-agent/restore_upgrade_test.go` и `testdata/r2b-journal/*.json`: файлы
  сняты кодом R2b (коммит перед переездом: `created`, `completed`, `intent`;
  `corrupted` — обрыв JSON) — все сценарии `@upgrade` и `@config` доли блокировок.
- `internal/nopostgres_test.go` — архитектурный тест `@arch @r2c-2` для `consolerestore`
  и `pluginhost` (go/parser: импорт `sdk/pgclient`, строки с приставкой или окончанием
  `postgresql.`, `PG_`, `pg_restore`, коды без `PG_`); проверяет себя на исходниках в
  памяти. R2c-3 расширит область.

## Отклонения и решения

- Шаг PostgreSQL на хосте без журнала: `REJECTED` теперь называет
  журнал («the restore journal is not enabled on this host»), а не движок консоли.
- Запись из одних намерений плагин не помечает `Retain`: хост удаляет её вместе с
  результатом (как `Engine.Abandon`); остаток с OID — `Retain`, запись остаётся.
- Если после отката остался только остаток, который совпадает с записью, плагин не
  переписывает её (`Recover`); иначе сохраняет остаток через `Journal().Save`.
- Версии схемы записи не вводил (поля прежних записей читаются как есть).
- Время разбора одной записи — 5 минут, даёт хост (`RecoveryTimeout`); плагин берёт
  контекст хоста, откат шага — `WithoutCancel` и 5 минут, как в R2b.
- `Snapshots` хоста даёт все снимки репозитория, источник фильтрует плагин (в R2b
  фильтровал `restic --tag`).
- Дублирование до R2c-3: `internal/restore/postgres.go` (repo restore) и
  `restore_snapshot.go` плагина разбирают метки одинаково; `sdk.PostgresRestore` и
  `sdk/pgclient` остаются (`PlanRestore` плагина по-прежнему отдаёт `Postgres`).
- Документация: `docs/operations/agent-host-setup.md` (В14: откат версии агента при
  непустом `restore-journal`), `docs/plugins/postgresql.md` (журнал), ADR 0066 («Решения
  R2c-2»). Статус ADR и шапка спецификации остаются «предложено» — меняет specifier.
- Мутационное тестирование не запускалось (`fast`); hardener — после R2c-3.

## Что осталось в агенте про PostgreSQL (для R2c-3)

`internal/restore/postgres.go`, `postgres_write.go`, `select.go` (пропуск снимка
глобальных объектов), `cmd/sard-agent/repo_restore_run.go` и `host_help.go` (repo restore),
`internal/hostcheck/postgres.go` и `cmd/sard-agent/check_host.go` (строка
`plugin.programs`, В1), `internal/restic` (комментарий), `internal/config/restore.go` и
`overlay.go` (ключ `restore.postgresql_lock_share` — разрешён РВ-7); корень сборки:
`main.go` (`Plugin: "postgresql"` для прежнего журнала, `plugins.WithPostgresLockShare`),
`plugins/builtin.go` (опция доли и импорт плагина). `agent/plugins/sdk`: `PostgresRestore`,
`RestorePlan.Postgres`, `pgclient`.

## Результаты (из вывода инструментов)

- `./scripts/gate.sh sdk fast`: PASSED; покрытие 100.0%; CRAP не выше 6.0.
- `./scripts/gate.sh agent fast`: PASSED (gofmt, vet, golangci-lint — 0 issues, в том числе
  с тегами `integration` и `e2e`; тесты, покрытие 98.6%, CRAP не выше 6.0; интеграционные
  тесты с restic; `go vet -tags e2e ./plugins/...`).
- Покрытие пакета `plugins/postgresql` отдельно: 99.7% (`go test -cover`).
- `make license-check`: 1641 файл в порядке.

Дальше: `cleaner` (метрики, повторы между `restore_*` и `exec.go`), затем architect и hardener
(мутационное тестирование — один раз после R2c-3).
