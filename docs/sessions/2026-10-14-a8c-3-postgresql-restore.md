<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# Сессия 2026-10-14: A8c-3 — `repo restore` снимков PostgreSQL (coder)

Ветка `ccr-bb1484f1-25wm55`. Спецификация (утверждена 2026-10-12, поправки
Р72–Р82 от 2026-10-13) — `docs/specs/agent/repo-restore.feature`: правило Р62,
Р60 для `postgresql.part=database` и `globals`, сценарии с пометкой `# A8c-3`.
`--overwrite` для PostgreSQL — A8c-2b (Р74), не реализован.

## Конфликтов спецификации нет

Р62 (временный файл `.<имя>.tmp-*` в цели) и Р73 (каталог `.sard-restore-*`) не
спорят: Р73 нужен, когда restic сам пишет в цель, а файлы PostgreSQL пишет
агент, и restic ничего в цель не пишет. Поэтому каталога `.sard-restore-*` для
PostgreSQL нет (ADR 0059, Р62). Задание просит распространить на `restic dump`
ограничения restic под root; ADR 0059 (Н27, строка «PostgreSQL») и Р62 говорят,
что `restic dump` идёт **от имени пользователя службы**, root получает только
байты. Реализовано по ADR: разрешённый список `env_file`, канал пароля и
`setpriv` относятся к запуску restic под root, а root restic здесь не запускает;
`restic dump` — `--no-lock`, кэш пользователя службы остаётся его.

## Что сделано (тест первым, красный прогон на каждый шаг)

- `internal/restic`: `Command.StdoutTo` (сырой стандартный вывод, не построчно) и
  `CLI.Dump` (`dump --no-lock -- <id> <путь>`). Тесты: байты `PGDMP\r\n\x00…`
  без перевода строки доходят целиком (настоящий процесс), аргументы,
  отказ на пустом id и пути, ошибка restic несёт `ErrNetwork`. Красный прогон:
  `undefined: Dump`, `undefined: StdoutTo`.
- `internal/restore/postgres.go`: `KindOf` (по тегам, Р60), `PlanPostgres`
  (самый новый снимок глобальных объектов с `postgresql.main_snapshot=<id>`,
  разбор имени базы из `%XX`, проверка вида снимка), `WritePostgres` (временный
  файл `O_EXCL`, `fchown`/`fchmod` до содержимого, `fsync`, `RENAME_NOREPLACE`,
  `fsync` каталога; сбой не оставляет файлов, законченный первый файл пары
  берётся назад), `PGPlan.Hints` (команды, кавычки `'\''`, строка о версии, вариант
  без глобальных объектов без слова `psql`). Фейк файловой системы в
  `pg_fs_test.go` записывает порядок chown, chmod и первой записи.
- `package main`: `restorePostgres` в `repo_restore_run.go` (проверка `--path`
  после выбора снимка и до создания цели, аудит, итог), справка `repo restore`
  (файлы, `root:root 0600`, команды печатаются, `--path` не применяется), фейк
  `dump` в `restore_fake_test.go`, тест `@integration` на настоящем restic
  (бинарные данные 1,9 МиБ без перевода строки, байт в байт).
- Документация: ADR 0059 (Р62, SELinux, последствия), раздел «Снимок
  PostgreSQL» в `docs/operations/agent-host-setup.md`, «Восстановление руками» в
  `docs/plugins/postgresql.md` (одна команда вместо двух с `--overwrite`).

## Соответствие сценариев и тестов

См. итоговый отчёт в сообщении сессии: сценарии правила «Снимок PostgreSQL
выкладывается файлами…» — тесты `repo_restore_postgres_test.go` (main) и
`postgres_test.go` (restore) с названиями по смыслу сценария.

## Ответы владельца (Р83–Р86, 2026-10-14)

Вопросы первого захода решены спецификацией; код и тесты приведены в
соответствие (тест первым).

- **Р83** — снимок PostgreSQL неожиданной формы (в том числе найденный к базе
  снимок глобальных объектов) отклоняется до создания цели:
  `RESTIC_OUTPUT_UNEXPECTED`, код 1, сообщение называет короткий id, что
  «cannot be restored as a directory» и «nothing was written». Тест:
  `TestAMalformedPostgreSQLSnapshotIsRefusedWithoutAnyWrite` (пять примеров
  сценария, ни dump, ни restore, цель не создана, аудита нет). Красный прогон: в
  сообщении не было фразы про каталог (прежний текст).
- **Р84** — `TestWithoutTheVersionTagTheSummaryNamesNoVersion` (прошёл сразу:
  поведение уже было; цифр в строке нет).
- **Р85** — `TestAFileThatCannotBeMadeInACreatedTargetIsWrittenToTheAuditLog`
  (EROFS на создании временного файла; прошёл сразу).
- **Р86** — `TestAFailureToWriteTheGlobalsRemovesTheDumpToo` в main и
  `TestAFailureOfTheSecondFileTakesTheFirstBack` в restore (прошли сразу).
- Дефект от cleaner: `PGPlan.fileOf` индексировал пустой список; теперь пустой
  план даёт пустое имя. Тест `TestHintsOfAnEmptyPlanDoNotPanic` (красный:
  паника index out of range).

## Ревью architect (третий заход)

- ADR 0059: ссылка на Р83 вместо «причина выбрана при реализации», Р85 и Р86 в
  абзаце о сбое, строка статуса. `agent-host-setup.md`: когда начинается запись
  (Р85) и отказ Р83.
- Комментарий `quote` в `postgres.go` исправлен.
- Фейк `dump` падает, если стандартный вывод — `*os.File` (root передаёт
  байты каналом, пользователь службы не получает дескриптор файла root).
- O1: `restore.Select` (`latest`) пользуется `KindOf(s) == KindGlobals`, своего
  `globalsTag` больше нет. Тест первым: `TestLatestAndTheKindOfASnapshotAgree…`
  (снимок с двумя метками `postgresql.part`; красный — `latest` пропускал его как
  глобальные, а `KindOf` раскладывает как базу). Заданное поведение не меняется.
- O3 и O4 — владельцу, не делались.

## Р87 и Р88 (четвёртый заход)

- **Р87.** `hostsetup.File.Stat` и `hostsetup.SameFile` (устройство и inode);
  сверку по имени делает уже существующий `Dir.Lstat` (`O_PATH|O_NOFOLLOW`, то же,
  что `fstatat` с `AT_SYMLINK_NOFOLLOW`). `WritePostgres` разобран на `pending`
  (`fill`, `commit`, `abandon`) и `takeBack`; удаление по имени всегда после
  сверки. Находка при красном прогоне: если записанный файл закрыт, подмена
  «удалить и создать заново» получает тот же номер inode и сверку обманывает, —
  поэтому записанные файлы держатся открытыми до конца команды (тест
  `TestAForeignFileUnderTheNameOfAFinishedDumpIsNotRemovedWhenTakingBack` красный
  до этого). `SameFile` читает `Stat_t`, а не `os.SameFile`: обёртка fake с
  другим владельцем остаётся сравнимой.
- **Тесты Р87** (`restore/postgres_test.go`): порядок «lstat имя, затем fsync
  каталога» у каждого файла; подменённый дамп; пропавшее имя; подменённые
  глобальные объекты берут дамп назад; чужой файл под именем законченного
  дампа не удаляется; чужой файл под временным именем не удаляется при сбое
  дампа; файл, который нельзя осмотреть. `main`:
  `TestASwappedPostgreSQLFileIsATargetWriteFailureWithAnAuditLine` (код 7, путь,
  строка аудита).
- **Р88.** `isRemains` в `restore/target.go`: `.sard-restore-*` или
  `.<имя>.tmp-*`. Тесты: `restore` — `TestATempFileOfAKilledPostgreSQLRestore…`,
  `TestOtherNamesAreNotCalledTheRemains…`, `TestTheFirstOfSeveralRemains…`;
  `main` — `TestAnInterruptedPostgreSQLRestoreLeftoverIsNamedAndKept` (база и
  каталог, файл остаётся, restic не вызывался, аудита нет),
  `TestANameNotOfTheFormTempIsNotCalledTheRemains`.
- Примечание: тесты `main` для Р87 и Р88 прошли сразу после кода (код написан
  после красных тестов уровня `restore`).
- O6: `gofmt` переписывает `'\''` в комментарии документации в типографскую
  кавычку, поэтому комментарий `quote` теперь словами.

## Не автоматизировано

`@vm` «Напечатанные команды восстанавливают базу в новом кластере»,
`@vm` SELinux (QA часть 4, шаги 29–38, часть 6) — ручной QA.

## Cleaner

Повторяющийся заголовок итога (`printRestored`, `printPostgres`) вынесен в
`printSnapshotHeader`, общий конец неудачной записи (`write`, `restorePostgres`)
— в `restoreRun.writeFailed`. Поведение и тексты не менялись. Остальные
функции среза ниже порога CRAP 6; `restoreRun.restore` — CRAP 6.0, CC 6, покрытие 100%.

### Cleaner, второй проход (629ecdb)

Затронутые функции (`malformed`, `PGPlan.fileOf`) в отчёте CRAP не
значатся, то есть ниже 6. Новые тесты уже используют общие помощники
(`assertRefusal`, `assertAbsent`, `dumpCalls`, `restoreCalls`);
`assertNothingWritten` не дублирует существующих. Код не менялся.

Клинер (третий раунд): `newest`, `fakeRestic.dump` и новый тест в отчёте CRAP не значатся (ниже 6); менять нечего, код не менялся.

## Hardener

Мутационное тестирование по схеме ADR 0057 (`go-mutesting --exec
scripts/mutate-exec.sh`, по файлам). Счёт, как его печатает инструмент:

| Файл | До | После |
|---|---|---|
| `internal/restore/postgres.go` | 0.863636 (60 passed, 15 failed) | 0.981818 (73 passed, 2 failed, оба эквивалентны) |
| `internal/restore/select.go` | 1.000000 (0 failed) | без изменений |
| `internal/restic/restic.go` | 0.990385 (1 failed) | выживший вне среза |
| `internal/restic/restore.go` | 0.972973 (1 failed) | выживший вне среза |
| `internal/restic/exec.go` | 0.916667 (3 failed) | выжившие вне среза |
| `cmd/sard-agent/repo_restore_run.go` | 0.916667 (6 failed) | 1.000000 (63 passed, 0 failed) |
| `cmd/sard-agent/host_help.go` | 1.000000 (0 failed) | без изменений |

Убиты тестами (поведение, не мутант):

- `outcome`: причина restic (`the command can be repeated`) не теряется при
  дописывании `; there is no dump file in <dir>`.
- `addGlobals`/`newestGlobals`: повреждённый снимок глобальных объектов отказывает
  всё восстановление; снимок без `postgresql.main_snapshot` и снимок другого
  `postgresql.part` с этой меткой не берутся.
- `createTemp`: сбой создания файла — `TARGET_WRITE`, дамп не вызывался;
  восемь попыток имени (семь занятых — успех, восемь — отказ).
- `finish`: сбой `fsync` файла не оставляет файлов.
- `writeFile`: файл закрыт при любом исходе; `countingWriter` сообщает первую
  ошибку записи, а не последнюю.
- `versionLine`: «pg_restore and psql must be not older than 16.4».
- `restorePostgres`: цель, которую нельзя создать, — `TARGET_WRITE` без вызова
  `dump`; цель отпущена до записи в аудит (и для каталога); итог печатает
  заголовок и строку `file:` на каждый файл.

Эквивалентные мутанты (код не менялся):

- `KindOf`, `case part == nil: return KindDirectory` без `return`: пустой
  `case` выходит из `switch` на тот же `return KindDirectory`; ветка нужна
  как защита от `*part` с nil, мутант её сохраняет.
- `countingWriter.Write`, `err != nil` → `true`: при `err == nil` и
  `c.err == nil` присваивается `nil`, то есть то же значение.

Выжившие вне среза A8c-3 (`restic.go` — канал пароля, `restore.go` —
`incomplete`, `exec.go` — ветка `ProcessState == nil`) не трогались; строки
не менялись срезом. Мутационные прогоны — `agent`, `./scripts/gate.sh agent
fast` проходит.

## Cleaner, раунд Р87/Р88

`./scripts/crap.sh agent` до и после: изменённых функций (`WritePostgres`,
`takeBack`, `pending.*`, `writeFile`, `isRemains`, `leftover`,
`SameFile`) в списке нет, то есть CRAP ниже 6; список из 30 строк не
изменился. Поведение, тексты ошибок и тесты не менялись.

- Проверка «имя ведёт к записанному файлу» (`Lstat` + `SameFile`) была
  в `takeBack` и в `pending.commit`; вынесена в `isWritten`.
- Строка `.tmp-` в `createTemp` берётся из `tempMarker`, которым пользуется
  `isRemains`: имя временного файла и его распознавание не расходятся.
- Запись файлов (`written` … `countingWriter`) перенесена из `postgres.go`
  (428 строк) в `postgres_write.go`; в `postgres.go` остались план и подсказки
  (221 и 226 строк).
