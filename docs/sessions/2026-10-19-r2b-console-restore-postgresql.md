<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# Сессия 2026-10-19: R2b — восстановление PostgreSQL из консоли (coder)

Ветка `claude/branch-and-pull-request-yaccmu`. Спецификации:
`docs/specs/{agent,server,web}/console-restore.feature`, `docs/qa/console-restore.md`;
сценарии с пометкой `# R2b` и решения владельца РВ1–РВ8 (2026-10-19). R2c (поверх
данных) и наблюдатель D21 не реализованы. Решения — ADR 0065 (и поправка к ADR 0060, п. 7a).

## Что сделано
- SDK: `RestorePlan.Database *DatabaseRestore` (аддитивно, Apache-2.0); `Validate` для плана базы.
- Плагин `postgresql`: `restore_schema.json`, `RestoreConfigSchema`, `PlanRestore`; бэкап
  пишет метку `postgresql.system_identifier` (запрос `pg_control_system()`, нет права или
  ответ не число — INFO в лог, бэкап идёт).
- Агент: `consolerestore.Engine.RestoreDatabase` (проверки ВК22 по порядку, режим `pg_restore`
  по таблице блокировок ВК28, глобальные объекты одной транзакцией ВК26, `CREATE DATABASE …
  TEMPLATE template0`, `restic dump` → `pg_restore`, откат по OID); журнал отката
  `executor.state_dir/pg-restore` (ВК27) и разбор при старте (`CleanDatabases`, `Settle`,
  `Abandon`); `restore.postgresql_lock_share` в конфиге (`agent.yaml`, `agent.d`); 13 причин
  в `refusal`; подсказка check и для `PG_CLIENT_MISSING`; `restic.Repository.Dump`.
- Сервер: реестр `RestorePlugins` (ВК32a, закрывает OQ-220), postgresql в `RestoreSupport`,
  умолчания из копии конфига бэкапа, новое имя базы до 63 байт, предупреждения
  `pg_owners_not_restored`, `pg_same_cluster` (ВК32b), строка уведомления «База: …, таблиц: N».
  `make openapi` перегенерировал `openapi.json` и `schema.d.ts` (два кода предупреждений).
- Web: итог базы («N таблиц», «N ролей» при > 0), три текста предупреждений ru/en, секреты
  выбранного агента в форме, строка каталога восстановления только для форм с `destination`;
  моки по плагинам (схемы postgresql — копии файлов агента, тест сравнивает), источник
  `db1 app` и его снимок в фикстурах.
- e2e: `ConsoleRestorePostgresTest` (бэкап → восстановление в новую базу; `DATABASE_EXISTS`;
  `PG_SERVER_TOO_OLD`; сбой `pg_restore` удаляет базу) — компилируется, нужны образы
  `make e2e-images` и `make e2e-pg-agent-images`.
- Документация: `docs/plugins/postgresql.md` («Восстановление из консоли»),
  `docs/operations/agent-host-setup.md`, ADR 0065, OQ-220.

## Находка: право на `pg_control_system()`
На PostgreSQL 14 (14.24), 16 (16.15) и 18 (18.6) `EXECUTE` на `pg_control_system()` выдано
`PUBLIC` (`proacl` пуст, `has_function_privilege('plain', …)` истинно для роли только с
`LOGIN`). Отдельный `GRANT` не нужен. Если администратор отозвал право, метки нет, бэкап
идёт, в лог шага — INFO. Версии 15 и 17 не проверялись: каталог функции в них тот же.

## Проверено вручную на настоящих программах
Ad-hoc тест (не в репозитории, зависит от кластера) с настоящим restic, `pg_dump`,
`pg_restore`, `psql` 16 и кластером на сокете: бэкап → восстановление в новую базу (2 таблицы,
по 1000 строк), имя базы с пробелом, глобальные объекты (роли, владелец таблицы), пропуск
роли начального суперпользователя, режим одной транзакции, сбой `pg_restore` после
`CREATE DATABASE` (база удалена), таймаут шага (база удалена, SIGTERM группе), `pg_restore -l`
из канала. Оглавление из канала читается: 6 записей у базы с 2 таблицами.

## Отклонения и замечания
- Откат, когда база переименована под записанным OID: сценарий спецификации говорит и
  «DROP только для базы с записанным OID», и «leftover называет базу с записанным OID».
  Принято: объект удаляется, только если по OID есть объект с записанным именем, иначе
  `leftover` с OID (не удаляем то, что ушло под другое имя).
- Откат создаёт OID сам, если шаг уверен, что создал объект (psql ответил 0): отмена между
  созданием и чтением OID не оставляет мусор.
- Не реализовано: сервер не возвращает имена секретов целевого агента в `RestoreTarget`;
  страница берёт их запросом агента (`agentQuery`).
- Дублирование: поиск клиента повторяет плагин, потому что (conninfo и среда вынесены в sdk/pgclient)
  плагин из `internal` не импортируется (ADR 0065, п. 2).
- Окно «psql умер после COMMIT»: роли остаются и не удаляются по имени; журнал их помнит (правка architect).
- Версии 15 и 17 PostgreSQL в интеграционной проверке не участвовали.

## Правки по замечаниям architect (CHANGES REQUIRED)
- P1. `applyGlobals`: psql, завершившийся не отказом скрипта (убит после COMMIT, обрыв
  связи), больше не стирает намерения ролей из журнала. Отказ скрипта (код 3, без ошибки) —
  как раньше. Иначе цель read-only спрашивает, какие роли есть, и в журнале остаются только
  существующие (ВК26: до транзакции их не было, значит они свои); если цель не отвечает —
  остаются все. Откат называет их как остаток (`CLEANUP_FAILED`), а не «ничего не
  восстановлено» (ВК8, ВК15, РВ6). Окно из ADR 0065 п. 4 закрыто для журнала.
- P1 (безопасность). Файл глобальных объектов: отказ для любой строки с обратной косой
  чертой, кроме точных `\restrict <ключ>` и `\unrestrict <ключ>` (ADR 0065 п. 6). Обратная
  косая черта в литерале тоже отказ — безопаснее, чем разбирать кавычки psql.
- P2. `agent/plugins/sdk/pgclient` (Apache-2.0, stdlib): `Conninfo` и `Env` — один код для
  плагина и движка; `PGPASSWORD` задаёт только он (тест).
- P3. `sdk.DatabaseRestore` и `RestorePlan.Database` переименованы в `PostgresRestore` и
  `Postgres`.
- P3. Сервер говорит, куда идёт восстановление: `RestoreTarget.restoreDir` — null для
  плагина, который восстанавливает не в каталог (запись `RestorePlugin.intoDirectory`).
  `restoresIntoDirectory` из веба удалена, моки обновлены.
- OQ-228: целевой дизайн — восстановление принадлежит плагину postgresql, SDK даёт
  возможности хоста; закрывает принятый долг ADR 0065 п. 1.
- Секрет записи журнала читается так же, как секрет шага (`consolerestore.Password`:
  пустой и NUL — отказ).
- Не менялось: причина `RESTIC_OUTPUT_UNEXPECTED` для «не снимок базы» названа в ВК21 спецификации.
- R2b: правило «PGPASSWORD только через sdk/pgclient» перенесено из SDK-модуля в agent/plugins/pgclient_test.go (архитектурный тест через `go list`, как imports_test.go; SDK исключён).

## 2026-10-19, hardener: мутационное тестирование R2b (только тесты)

Результаты, как их напечатали инструменты (HEAD de4bd252 плюс тесты):

- sdk: 2 выживших из 129 (0.984496) -> 0 из 129 (1.000000); границы имени базы (1 и 2 байта).
- agent, `consolerestore` (пакет, go-mutesting): 144 выживших (0.822002) -> 10, все эквивалентны (ниже);
  `plugins/postgresql`: 6 -> 0; `pluginhost`: 2 -> 1 (эквивалентен); `cmd/sard-agent/restore_postgres.go`: 2 -> 0.
  Весь модуль agent, полный прогон: 7189 мутантов, 367 выживших, счёт 0.948950, ворота пройдены
  (первый полный прогон потерян: заполнился диск, кэш go-build; повторён с охраной диска).
- server (mutflow): `RestorePostgresMutationTest` 9 выживших из 61 -> 0 (ворота пройдены).

Новые тесты: `consolerestore/pg_hardening_test.go`, `pg_pure_internal_test.go` (читалки и писатели на границах),
расширенный стенд `pg_rig_test.go` (`failOut`, `globalsExit`, `createExit`, `exitErr`, `Deadline`, `StdinReader`),
`plugins/postgresql/hardening_test.go`, `pluginhost` (секрет не читается), `restore_postgres_test.go`,
`RestorePostgresMutationTest` (длина имени, sourceAgent, restoreDir, вывод восстановления базы, схема,
отозванный и verifier, `allows`, `isRoot`).

Эквивалентные мутанты R2b (тестом не убить; coder может убрать избыточность):

1. `pg_check.go` (`checkConnection`, `compareCluster`) и `pg_prepare.go` (`streamFailure`): ранний
   `return a.interrupted(...)` по `ctx.Err()` дублирует правило `a.fail`: следующий же запрос или проверка с
   завершённым контекстом даёт тот же отказ INTERRUPTED той же стадии.
2. `pg_prepare.go` `checkRoles`: `f == nil &&` (при `f != nil` список `existing` пуст).
3. `pg_prepare.go` `FormatFloat(share, 'g', -1, 64)`: любое отрицательное число знаков значит «кратчайшее»;
   предложение: `%g`.
4. `pg_prepare.go` `countObjects` и `pg_sequence.go` `countTables`: число при отказе (`return 0, f`) не читается.
5. `pg_sequence.go` строки `_ = a.save(...)` после `unsettledRoles` и после отказанного CREATE: следующий
   откат (`resolveOIDs`/`rollback`) переписывает журнал тем же состоянием до любого действия.
6. `pluginhost/hint.go` `err == nil ||`: при nil остальные условия уже возвращают err.
