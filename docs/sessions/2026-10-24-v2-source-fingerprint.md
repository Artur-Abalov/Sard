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
