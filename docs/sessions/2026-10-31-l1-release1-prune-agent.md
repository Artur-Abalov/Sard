<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# L1 выпуск 1 — шаг PRUNE, встроенные действия в Register, документация: proto и агент (coder)

Дата: 2026-10-31. Спецификации `docs/specs/proto/retention-prune.feature`,
`docs/specs/agent/retention-prune.feature` (сценарии `@выпуск2` не входят).

## Сделано
1. proto (добавочно, закоммичено и отправлено первым): `ACTION_PRUNE = 6`, `RunStep.prune = 13`
   (`PruneTask {}`), `StepResult.prune = 16` (`PruneOutput`: optional `freed_bytes`,
   `repository_size_bytes`, `failure`, `lock_holder`), `RegisterRequest.builtin_actions = 14`.
   Тесты `@wire` — `agent/internal/app/prune_wire_test.go`.
2. Реестр действий без плагина `executor.Builtin` (действие → обработчик) вместо `Options.Forget`;
   `Builtin.Actions()` — один источник правды и для исполнителя, и для Register
   (`app.Agent.Builtin`, `app.Agent.Role`; роль отсеивает действия, которые ей запрещены).
3. `restic.CLI.Prune` (`prune --retry-lock 5m`, без других флагов) и разбор «total prune» и «remaining»
   из сводки; `retention.PruneHandler` (unlock без `--remove-all`, затем prune; «prune accepts no
   overrides», «prune task missing»; классы сбоев и тексты общие с FORGET); роль: PRUNE только у source;
   маскирование `lock_holder` у PRUNE.
4. Документация оператора: `docs/operator/18-snapshot-retention.md`, ссылки из README и
   `03-configuration.md`; тест `agent/cmd/sard-agent/retention_docs_test.go`.

## Что узнали
- Формат сводки restic 0.19.1 снят с настоящего restic: `total prune:   0 blobs / 0 B`,
  `remaining:  26 blobs / 20.004 MiB` (stdout, до них идут строки прогресса).
- Убитый `restic check` оставляет блокировку процесса этого хоста; `restic unlock` снимает её сразу, так
  что сценарий «блокировка снимается через 10 секунд» с настоящим restic проверяет прежде всего это.

## Не сделано
- `@restic`: «Устаревшая блокировка снимается без remove-all» (покрыто unit-тестом на аргументы и
  интеграционным сценарием выше, но не отдельной проверкой аргументов настоящего restic), «Прерванный
  PRUNE оставляет репозиторий целым» (200 снимков) и «Бэкап во время PRUNE ждёт блокировку».
