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

## Не автоматизировано

`@vm` «Напечатанные команды восстанавливают базу в новом кластере»,
`@vm` SELinux (QA часть 4, шаги 29–38, часть 6) — ручной QA.

## Cleaner

Повторяющийся заголовок итога (`printRestored`, `printPostgres`) вынесен в
`printSnapshotHeader`, общий конец неудачной записи (`write`, `restorePostgres`)
— в `restoreRun.writeFailed`. Поведение и тексты не менялись. Остальные
функции среза ниже порога CRAP 6; `restoreRun.restore` — CRAP 6.0, CC 6, покрытие 100%.
