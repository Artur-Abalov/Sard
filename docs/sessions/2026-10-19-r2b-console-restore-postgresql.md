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
- Дублирование: `conninfo`, среда программ и поиск клиента повторяют плагин, потому что
  плагин из `internal` не импортируется (ADR 0065, п. 2).
- Окно «psql умер после COMMIT»: роли остаются и не удаляются по имени (ADR 0065, п. 4).
- Версии 15 и 17 PostgreSQL в интеграционной проверке не участвовали.
