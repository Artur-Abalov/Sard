<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-21 — R2c-3: repo restore через выгрузку плагина, чистка SDK, архитектурные тесты

Ветка `claude/epic-albattani-e099w7`. Спецификация `docs/specs/agent/restore-ownership.feature`
одобрена владельцем (О1–О5, рекомендованные ответы на В1–В14; В1 — по решению владельца
необязательный интерфейс SDK проверки программ, который может реализовать любой плагин).
Реализованы все сценарии с тегом `@r2c-3`. Принцип владельца: в агенте нет кода конкретной базы.

## Что сделано

- **SDK** (`agent/plugins/sdk`, Apache-2.0): `Extractor` (`PlanExtract(ExtractRequest)
  (ExtractPlan, error)`, `ExtractFile`, `ExtractPlan.Validate`: имя — одна составляющая, не
  `.`/`..`, без `/` и NUL, до 255 байт, без повторов); `ProgramChecker`/`ProgramHost`/
  `ProgramsCheck` (`CheckPrograms`: `PATH` агента без пустых записей и запуск программы от
  пользователя службы). Удалены `PostgresRestore`, `RestorePlan.Postgres`, `MaxDatabaseName`,
  `pgclient`. `imports_test.go`: SDK не зависит ни от одного пакета Sard вне себя, в том числе от
  `proto/gen/go`; тест «ни одно экспортируемое имя не содержит Postgres».
- **Плагин postgresql**: `PlanExtract` (`extract.go`; разбор тегов и подсказки переехали из
  `internal/restore`, используют `planLayout` из R2c-2 — дубль разбора меток снят),
  `CheckPrograms` (`check_programs.go`; перенос `hostcheck/postgres.go`), `restoreSettings`
  вместо `RestorePlan.Postgres` (`PlanRestore` читает конфиг и возвращает пустой план),
  пакет `plugins/postgresql/pgclient` (был `plugins/sdk/pgclient`, лицензия AGPL-3.0; ПГ3, ПГ4 и
  тесты сохранены). `tagValue` теперь пропускает метку с пустым значением, как делал
  `restore.valueOf`.
- **internal/restore**: удалены `postgres.go` (`Kind`, `KindOf`, `PlanPostgres`, `Hints`, имена
  меток `postgresql.*`) и дубль разбора меток; `postgres_write.go` → `extract_write.go`
  (`WriteFiles`, `File`, `Dumper`); `postgres_test.go`/`pg_fs_test.go` → `extract_write_test.go`/
  `extract_fs_test.go` (тесты записи сохранены, тесты плана и подсказок — в плагине);
  `PlanExtraction` (`extraction.go`): плагин снимка — тот из зарегистрированных с `Extractor`,
  чьё имя стоит приставкой `<плагин>.` у метки (О3); без такой метки или при плане без файлов —
  дерево; два претендента — `PLUGIN_FAILED`; ошибка плана — `RESTIC_OUTPUT_UNEXPECTED` класса
  «агент» с текстом плагина (В10); недопустимый план — `PLUGIN_FAILED`. `latest` пропускает снимок
  с меткой `<плагин>.main_snapshot=<значение>` (О2).
- **cmd/sard-agent**: `repo restore` идёт через `PlanExtraction`; `hostDeps.plugins` (реестр
  встроенных плагинов, тесты подставляют свой); `sard-agent check` спрашивает у каждого плагина
  `ProgramChecker`, иначе пишет «the plugin needs no program of the host»
  (`hostcheck.ProgramsLine`); из `check_host.go` ушла строка PostgreSQL, из `hostcheck` —
  `postgres.go` и его тесты (перенесены в плагин).
- **Архитектурные тесты**: `internal/nopostgres_test.go` — полная версия R2c-3 (вся
  `agent/internal` и `agent/cmd/sard-agent`, кроме `_test.go` и `testdata`; подложенные
  нарушения и допустимые строки — файлы `internal/testdata/nopostgres/{violations,allowed}`,
  нарушение называет файл и строку; коды без `PG_` — О4); `hostcheck/nopostgres_test.go`
  (нет `pg_dump`, `psql`, `pg_dumpall`, `pg_restore`); `refusal` (нет причин PostgreSQL);
  `plugins/pgclient_test.go` (клиент лежит в плагине, его импортирует только плагин,
  `PGPASSWORD` задаёт только он).
- **Документы**: поправки ADR 0065 (п. 1, 2, 4, 5), 0027, 0051 (п. 2), 0059 (Р62); ADR 0066 —
  «Решения R2c-3»; `docs/plugins/postgresql.md`, `docs/qa/restore-ownership.md` (шаг 20),
  `docs/open-questions.md` (OQ-213: новое имя файла).

## Отклонения и решения

- Сообщение `--path` со снимком плагина — «`--path does not apply to a PostgreSQL snapshot: its
  files are laid out whole`»: слово «PostgreSQL» даёт плагин (`ExtractPlan.Label`), хост его не
  знает. Раньше после «snapshot:» стояло «it holds one file and is laid out whole» (неверно для
  дампа с глобальными объектами). Сценарий требует только «--path» и «PostgreSQL».
- Порядок проверок в `repo restore`: план выгрузки строится сразу после выбора снимка,
  `--path` проверяется после плана (раньше — до него). Различие видно лишь у снимка
  неожиданной формы вместе с `--path`.
- План без файлов означает «это дерево» (снимок плагина другой части, например
  `postgresql.part=other`, остаётся деревом, как с `KindDirectory`). Решение записано в ADR 0066.
- Справка `repo restore --help` и `check --help` остались как были (справка `repo restore`
  описывает работу плагина; текст про отсутствие программ в `check --help` обобщён).
  Строки справки не нарушают архитектурный тест (он смотрит начало и конец строки).
- Статус ADR 0066 и шапка спецификации остаются «предложено»: в ADR статус станет «принято»,
  «когда владелец её одобрит», а не вместе с R2c-3. Менять статус — specifier.
- Мутационное тестирование не запускалось (`fast`); hardener — один раз после R2c-3.

## Проверка трёх изменений поведения R2c-2

1. Шаг PostgreSQL без `sard.source` — `TARGET_INVALID (check)` от плагина:
   `plugins/postgresql/restore_run_test.go` (`hasPrefix(..., "TARGET_INVALID (check)")`).
   Спецификация (ВК5) говорит лишь о теге, не равном источнику шага (`SNAPSHOT_UNKNOWN`), и
   сценария «шаг без sard.source» в ней нет; менять нечего.
2. Хост без журнала — `REJECTED` с текстом «the restore journal is not enabled on this host»:
   `pluginhost/own_hardening_test.go`, `plugins/postgresql/actions_test.go`. Сценариев R2a/R2b
   на этот текст нет.
3. `Snapshots` хоста даёт все снимки репозитория, источник фильтрует плагин
   (`chooseSnapshot`): сценарии ВК5 («снимок другого источника — SNAPSHOT_UNKNOWN») проходят;
   тесты `console_restore_pg_test.go` и плагина зелёные.

## Что осталось в агенте про PostgreSQL

Из кода (не тестов) вне `agent/plugins/postgresql`:

- `internal/config/restore.go`, `overlay.go`: ключ `restore.postgresql_lock_share` и проверка при
  старте (РВ-7).
- `cmd/sard-agent/main.go`: `Plugin: "postgresql"` для чтения прежнего журнала (ADR 0066 п. 4,
  В9, В13) и `plugins.WithPostgresLockShare(...)` (РВ-7); `plugins/builtin.go`: импорт плагина и
  параметр сборки `WithPostgresLockShare` (корень сборки). Строка `"postgresql"` и ключ
  архитектурным тестом разрешены явно (допустимые строки спецификации).
- `cmd/sard-agent/host_help.go`: справка `repo restore` описывает файлы `<база>.dump`,
  `<база>.globals.sql` и `pg_restore` (текст для оператора, сценарий A8c-3 требует эти слова).
- `sdk`: комментарии-примеры (`"postgresql"`, `postgresql.server_version`) и упоминание
  jsonb PostgreSQL в `registry.go` (комментарий).
Исключений в архитектурных тестах сверх списка спецификации нет.

## Результаты (из вывода инструментов)

- `./scripts/gate.sh sdk fast`: PASSED; покрытие 100.0%; CRAP не выше 6.0.
- `./scripts/gate.sh agent fast`: PASSED (gofmt, vet, golangci-lint; тесты, покрытие 98.6%;
  CRAP не выше 6.0; интеграционные тесты с restic; сборка стенда e2e).
- `./scripts/gate.sh cli fast`: PASSED.
- `make license-check`: 1666 файлов в порядке.

Дальше: `cleaner` (метрики), затем architect и hardener (мутационное тестирование — один раз).

## cleaner

Объём: `git diff 6075d52..HEAD -- agent` (R2c-2 и R2c-3), прочитаны все файлы кроме тестов
в `plugins/postgresql` (включая `pgclient`), `plugins/sdk` (`extract`, `programs`, `restore*`),
`internal/restore`, `internal/restorejournal`, а также диффы `pluginhost`, `consolerestore`,
`refusal`, `hostcheck`, `cmd/sard-agent`; тесты просмотрены на дубли помощников (общий стенд
`restore_rig_test.go` уже общий, дублей нет).

Метрики до: `crap.sh agent` и `crap.sh sdk` — максимум 6.0 (порог), ни одна функция выше.

Сделано (поведение не менялось):
- `plugins/postgresql/restore_prepare.go` (269 строк) разделён по ответственности: потоковый
  запуск `pg_restore` с `restic dump` (`stream`, `dumpRun`, `countingWriter`, `streamFailure`)
  вынесен в `restore_stream.go`.
- `outcome.number(bits)` заменил четыре одинаковых разбора «последняя строка psql — число»
  (`chooseMode`, `recordRoles`, `recordDatabase`, `countTables`).
- `parseProbeRow` — общий разбор строки зонда для резервной копии (`parseProbe`) и восстановления
  (`parseRestoreProbe`).
- `dump.go`: значения тега `part` — константы `partDatabase`/`partGlobals`, а не повтор литералов.
- `plugin.go`: устаревший комментарий типа `Plugin` («только копирует») приведён к фактическому.
- `internal/restore/extract_write.go`: `countingWriter`, который ничего не считает, переименован
  в `errWriter`; длинная строка комментария `WriteFiles` перенесена.
- `internal/restorejournal/journal.go`: `collectDir` и `collectLegacy` повторяли чтение каталога
  с «нет каталога — не ошибка»; общая функция `Store.names`.

Не тронуто, в отчёте: `DefaultLockShare` есть и в `plugins/postgresql`, и в `internal/config`
(плагин не импортирует `internal/`; граница, не дубль для чистки). Остатков hostcheck/refusal,
мёртвых экспортов и устаревших имён удалённых файлов в комментариях не найдено.

Результаты: `gate.sh sdk fast` PASSED, `gate.sh agent fast` PASSED (CRAP не выше 6.0,
golangci-lint 0 issues). `cli` не затрагивался.

Дальше: `architect`.

## Правки по замечаниям architect (CHANGES REQUIRED)

Принцип владельца: в агенте нет кода конкретной базы данных.

- Архитектурный тест `nopostgres_test.go` ищет `postgresql.`, `PG_`, `pg_restore`, `pg_dump`,
  `psql`, `createdb` в любом месте строки; подложенный файл `violations/help_text.go`. Тест
  сначала упал на `cmd/sard-agent/host_help.go`.
- Абзац справки о PostgreSQL ушёл из `host_help.go`: SDK получил `ExtractHelper`, плагин
  postgresql возвращает абзац слово в слово, `repo restore --help` дописывает абзацы
  плагинов из `hostDeps.plugins`. Тест: чужой плагин-выгрузчик с абзацем.
- Тег `sard.source` проверяется для каждого шага восстановления (снято исключение для
  плагина с собственным восстановлением); проверка и её тест в плагине postgresql убраны
  как недостижимые; `RestoreRequest.Source` — «никогда не пуст». Рабочий шаг тестового
  стенда `ownRig` теперь несёт тег.
- `config.DefaultLockShare` удалён; `PostgresqlLockShare()` возвращает `*float64`; 0.5 только
  в плагине.
- `plugins.Option` не называет плагин (`func(*builtinSet)`).
- Комментарий-трассировка сценария в архитектурном тесте; поправлен неверный комментарий
  в `refusal/lock.go` (`os.SameFile` для nil даёт false); ADR 0027 и 0066 обновлены.
