<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-24 — V2: отпечаток источника и интерфейс проверки в плагинах

Ветка `ccr-33655666-la3jwf`: её назначила сессия. В задаче указана
`feat/v2-source-fingerprint`; переименовать можно по решению владельца.

## Фаза 1 — исследование

«Проверил» — видел в коде (file:line). «Полагаю» — не проверено.

### Чего нет (проверил)

- `docs/spikes/v0-restore-verification.md` нет ни в дереве, ни в истории
  (`git log --all`); то же записано в журналах V1 и V4. Цифр V0 (цена
  `count(*)`, длительность открытой транзакции на 15 ГБ) нет — лимиты будут
  консервативными и настраиваемыми, значения по умолчанию помечены как мои.
- `sdk.Verifier` (`agent/plugins/sdk/plugin.go:137-140`) не реализует ни один
  плагин; `plugins/builtin_test.go:66-67` это утверждает. Путь
  `pluginhost/handler.go:306-322` (restore → `Verify` → `VerifyOutput{checks}`)
  живой код без пользователей.
- Go-драйвера PostgreSQL в агенте нет (`agent/go.mod`); правило 7 CLAUDE.md
  разрешает агенту только stdlib, gRPC, protobuf, YAML и JSON Schema.
- `BackupOutput` (`agent.proto:369-379`) — snapshot_id, total_bytes,
  added_bytes, repository_id; отпечатка и числа файлов нет.
- Сводка restic: `total_files` разбирается в прогрессе
  (`internal/restic/backup.go:112`), в `BackupSummary` (`:43-55`) числа файлов
  нет — только files_new/changed/unmodified.
- Статуса «с предупреждением» у запуска нет: `runs.status` CHECK
  (`V202609301200__runs.sql:47-48`); в `snapshots` нет числа файлов
  (`V202609301800__results.sql:51-73`).
- V4 не пишет отпечаток в задание: `verify/VerificationCycles.kt:28-32`
  (`ENQUEUE` без `fingerprint`); колонки `fingerprint BYTEA`,
  `fingerprint_sha256` с CHECK на 64 hex и CHECK «оба или ни одного» есть
  (`V202610191300__restore_verification.sql:55-56,73`). В `VerifyTask`
  (`agent.proto:562-576`) уже есть `bytes fingerprint = 3`,
  `string fingerprint_sha256 = 4`.

### Что есть и на что опираться (проверил)

| Тема | Факт | Где |
|---|---|---|
| Восстановление после TD1 (R2c) | `sdk.RestoreRunner.Restore(ctx, RestoreHost, cfg, RestoreRequest)`; реализует только postgresql | `sdk/restore_run.go:20-52`; `postgresql/restore_run.go:14` |
| files | только `Restorer.PlanRestore`, дерево восстанавливает ядро | `files/plugin.go:36`; `sdk/restore.go:18-43` |
| pg_dump | `--format=custom --compress=0 --no-password …`, поток прямо в stdin restic | `postgresql/stream.go:22-48` |
| Подключение | conninfo с кавычками, `sslmode` из `tls_mode`; пароль только через `pgclient.Env` (`PGPASSWORD`) | `postgresql/conninfo.go:55-63`; `pgclient/pgclient.go:33-49`; `plugins/pgclient_test.go` |
| psql | уже используется (prepare, restore), находится рядом с pg_dump | `postgresql/tools.go:46-58` |
| Теги снимка | шаговые `sard.*` + плагинные `<plugin>.<key>`, задаются `--tag` при `backup` | `pluginhost/handler.go:177-188`; `source.go:178-183`; `restic/backup.go:328-342` |
| Глобальные объекты | отдельный снимок через `sdk.ExtraFile` | `postgresql/dump.go:61-87`; `source.go:153-161` |
| restic | закреплён 0.19.1, минимум 0.19.0 | `internal/restic/restic-version` |
| Часы | агент: `executor.Clock`; сервер: бин `Clock` | `executor/executor.go:93`; `ClockAutoConfiguration.kt:12-16` |
| Форма источника | `schemaForm.ts`: строки, числа, bool, enum, массив строк, secret; иное — JSON-редактор | `web/src/schemaForm.ts:10-78` |
| N−1 | возможности определяются наличием поля (`RegisterRequest.verifier`) | `agent.proto:143-148`; `RegisterRequests.kt:61` |

### Выводы (полагаю, проверить в фазе 2)

- Тег `sard.fp=<hex>` ставится только при `restic backup`, значит всё
  хешируемое должно быть известно до запуска restic: для PostgreSQL
  отпечаток снимается до старта pg_dump и удлиняет бэкап на время `count(*)`
  (параллельно с дампом его не снять — тег уже нужен).
- restic с 0.17 хранит `summary` (в том числе `total_files_processed`,
  `total_bytes_processed`) в самом снимке — проверить на 0.19.1 на стенде.
- Серверу не нужно канонизировать отпечаток: агент присылает канонические
  байты, сервер и верификатор считают SHA-256 от байтов.

Вопросы к контрольной точке 1 — в сообщении владельцу; решения запишу сюда
после ответа.

## Контрольная точка 1 — решения владельца (2026-10-25)

1. Без конвейера `/ship-feature`: работу делает одна сессия, тест первым.
2. psql-сопроцесс, без драйвера Go (правило 7 не трогаем).
3. Отпечаток целиком хранится на сервере: V4 уже держит его в
   `verification_jobs.fingerprint`.
4. Список файлов, выброшенных из выборки после бэкапа, вне хеша —
   **неприемлемо**. Решение (моё, отчитаться): для files окончательный
   отпечаток (выборка, итоги restic, выброшенные файлы) пишется после
   основного снимка **отдельным снимком** `sard-fingerprint.json` с тегами
   `sard.fp=<хеш>`, `files.main_snapshot=<id>`, `files.part=fingerprint` —
   тем же механизмом, что снимок глобальных объектов PostgreSQL. Всё, на что
   опирается верификатор, защищено репозиторием, а не сервером; основной
   снимок не переписывается. У PostgreSQL отпечаток известен до restic, тег
   `sard.fp` ставится на основной снимок при создании.
5. Лимиты `count(*)` — 60 с на таблицу, 10 мин на все; превышение — факт
   «не посчитано» (`info`), бэкап идёт.
6. Статуса запуска не будет: `warnings` у шага, `snapshots.files_total`.
7. Выборка: резервуар по полному обходу; 100 файлов, ≤ 64 МиБ на файл,
   ≤ 1 ГиБ чтения, обход ≤ 10 мин — по исчерпании берётся набранное с отметкой.
8. `sdk.Verifier` удалён вместе с путём verify в `pluginhost`.
9. Обобщённый отпечаток, сравнение в ядре. Отступление от вопроса 9: правил
   два — `exact` и `info`; `nonempty` и `sha256` оказались частными случаями
   `exact`, потому что копию меряет тот же код и даёт те же значения
   («nonempty», hex).
10. Поля `BackupOutput` 5–7, тег `sard.fp=<64 hex>`.

## Фаза 2 — интерфейс, плагины, ядро, тег

### Что сделано

- SDK (`agent/plugins/sdk`, Apache-2.0, только stdlib):
  - `fingerprint.go` — `Fingerprint{Facts, Format, Plugin}`, `Fact{Name, Rule,
    Value}`, канонический вид (`Encode`: ключи и факты отсортированы, без
    пробелов, ≤ 1 МиБ), `Hash`/`HashBytes`, `DecodeFingerprint` (принимает
    только канонические байты);
  - `verify.go` — `Fingerprinter`, `FingerprintTarget{Root, Reference}`,
    `FingerprintCompleter`, `BackupTotals`, `CopyRestorer.RestoreInto`,
    `RestoredCopy`; `RestoreHost.RestoreTree`;
  - `Dump.Fingerprint` и `Dump.Release`; `Verifier` удалён.
- Ядро: `agent/internal/fingerprint.Compare` — расхождения словами
  («rows:public.users: 10 in the source, 9 in the copy»), `Unverified` для
  фактов, которые копия смогла снять только как `info`.
- Хост (`pluginhost`): тег `sard.fp` на основной снимок (или отдельный снимок
  отпечатка для `FingerprintCompleter`), отказ шага с тегом `sard.fp` от
  сервера, `Release` ровно один раз сразу после основного снимка,
  `restoreHost.RestoreTree`, `BackupOutput.fingerprint/fingerprint_sha256/
  total_files`; restic: `total_files_processed` в `BackupSummary`.
- proto: `BackupOutput` 5 `fingerprint`, 6 `fingerprint_sha256`, 7
  `total_files`; buf lint и buf breaking (FILE, против origin/main) — чисто.
- postgresql: сессия psql (`session.go`) — `os.Pipe` на stdin, каждый запрос
  под SAVEPOINT, маркер `\echo`; `BEGIN … REPEATABLE READ READ ONLY`,
  `idle_in_transaction_session_timeout = 0`, `pg_export_snapshot()`, отпечаток,
  `pg_dump --snapshot`, транзакция открыта до `Release`; смерть сессии во время
  pg_dump — провал шага. Факты: `major_version`, `extension:*` (exact),
  `tables` (exact), `rows:<schema>.<table>` (exact или info), версия, размер,
  версии расширений, `key_tables` (info). Исключения pg_dump переводятся в
  регулярные выражения тем же разбором, что `processSQLNamePattern`, и
  сопоставляются самим PostgreSQL. Поле `key_tables` в схеме. `RestoreInto` —
  `Restore` R2b + конфиг источника на новую базу.
- files: `control_paths`, `sample_files`, `sample_max_file_bytes` в схеме;
  контрольные пути проверяются в Prepare (до restic); выборка — резервуар,
  шаблоны `--exclude` по правилам `filter.Match` restic (отрицания
  игнорируются: выборка может пропустить файл, но не взять исключённый);
  `CompleteFingerprint` добавляет `files`/`bytes` из сводки restic и
  переводит в `info` файлы, у которых изменились размер или mtime; копия
  меряется по `Root` и фактам источника; `RestoreInto` — через
  `RestoreTree`.

### Проверки 1–4 стратегии

| # | Что | Как | Итог |
|---|---|---|---|
| 1 | Запись во время pg_dump не попадает ни в дамп, ни в отпечаток; count = count копии | Вручную, локальный PostgreSQL 16.15: Dump → `insert` 50 строк → Stream → `pg_restore` в новую базу → `Fingerprint` копии | **Проверил**: в источнике 150 строк, в отпечатке и копии 100; `t2` исключена и там, и там |
| 2 | Отпечаток копии тем же кодом совпадает; удалённая строка — расхождение с именем таблицы | То же; юнит-тест `TestTheFingerprintOfACopyIsComparedWithTheSource` | **Проверил**: `rows:public.users: 100 … 99` |
| 3 | files: нет/пуст контрольный путь — бэкап падает; изменённый во время бэкапа файл — нет ложного расхождения; подменённый — расхождение | Юнит-тесты на `fstest.MapFS`, сквозной тест через агента, интеграционный на restic 0.19.1 | **Проверил** |
| 4 | Хеш в теге = хеш канонических байтов; снимок не переписывается | Интеграционные тесты на restic 0.19.1 (files и postgresql) | **Проверил**: ID основного снимка = ID результата; итоги restic лежат в `summary` самого снимка и равны фактам `files`/`bytes`; ссылка и пустой каталог не считаются файлами ни restic, ни обходом копии |

Ручная проверка 1–2 шла временным тестом вне репозитория (сервер PostgreSQL в
гейте не поднимается); в репозиторий она войдёт e2e-тестом фазы 3.

### Замер цены отпечатка (стенд — локальный PostgreSQL 16.15, данные в памяти)

База `bench` 876 МБ: таблица на 8 млн строк и на 1 млн. Все запросы отпечатка в
одной транзакции REPEATABLE READ, включая `count(*)` по обеим таблицам, —
**0,43 с**; `pg_dump --format=custom --compress=0` той же базы — **4,3 с**.
`count(*)`: 0,20–0,23 с на 8 млн строк, 0,03 с на 1 млн. Это нижняя оценка: на
15 ГБ, которые не лежат в памяти, count читает таблицу с диска, и добавка к
бэкапу растёт вместе с ней — цифр V0 по-прежнему нет. Отсюда лимит 60 с на
таблицу и 10 мин на все.

### Отступления и долги

- `exclude.go` (files) написан на шаг раньше своего теста.
- Шаблоны `--exclude` с переменными окружения restic не раскрывает, плагин тоже.
- e2e на стенде, сервер, REST, V4, документация и ADR — фаза 3.
