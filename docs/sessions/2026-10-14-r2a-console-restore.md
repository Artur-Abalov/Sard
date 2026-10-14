<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# Сессия 2026-10-14: R2a — восстановление из консоли (coder)

Ветка `claude/branch-and-pull-request-yaccmu`. Спецификации:
`docs/specs/{agent,server,web}/console-restore.feature`, `docs/qa/console-restore.md`;
сценарии с пометкой `# R2a`. R2b (PostgreSQL), R2c (поверх) и наблюдатель D21 не
реализованы. Решение — ADR 0060.

## Что сделано
- proto: `restore_dir`, `restore_config_schema`, `RestoreOutput` (только добавления).
- SDK: `Restorer`, `RestorePlan.Validate`. Агент: `restore.dir`, шаг RESTORE для
  `files`, staging + одно переименование, очистка при старте, откат с `leftover`.
- Сервер: `restore-options`, `preview`, запуск; `Snapshot.restorable`;
  `Run.kind/requestedBy/restore`; фильтр `kind`; уведомления с текстами восстановления.
- Web: кнопки, страница восстановления, список и страница запуска.
- Документация: `docs/operations/agent-host-setup.md`, раздел «Восстановление из консоли».

## Отклонения и замечания
- postgresql в R2a не объявляется для восстановления.
- Проверка владельца в интеграционном тесте слаба под root.
- Каталоги с ограниченными правами могут помешать откату (сообщается `leftover`).
- Для существующих ручных запусков `requestedBy` заполнен значением `admin`.
