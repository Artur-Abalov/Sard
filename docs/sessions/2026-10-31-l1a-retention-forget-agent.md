<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# L1a — шаг FORGET и нижний предел хранения на хосте: proto и агент (coder)

Дата: 2026-10-31. Спецификации `docs/specs/proto/retention-forget.feature`,
`docs/specs/agent/retention-forget.feature` (срез L1a), QA `docs/qa/retention-forget.md`, ADR 0071.

## Сделано
1. proto (добавочно): `ACTION_FORGET = 5`, `RunStep.forget = 12` (`ForgetTask`, `RetentionPolicy`),
   `StepResult.forget = 15` (`ForgetOutput`, `MaintenanceFailure`), `RepositoryInfo.retention_floor = 6`
   (`RetentionFloor`, `RetentionFloorOrigin`). `make proto`, `buf lint`, `buf breaking` зелёные.
2. `agent/internal/config/retention.go`: раздел `retention_floor` только в основном конфиге, ошибки с
   полным именем ключа; `Config.FloorOf`; ключ внутри определения репозитория — ошибка.
3. Register несёт предел и происхождение; при старте строки `retention floor repository=…` и
   предупреждение для неизвестного репозитория (`cmd/sard-agent/retention_floor.go`).
4. `agent/internal/retention`: `Plan` (политика как у restic forget, части, неполные снимки по подсказке,
   предел) и `Handler` (отказы до обращения к хранилищу, unlock без `--remove-all`, пачки по 1000,
   итог по повторному перечислению, отмена и срок, классы сбоев).
5. `executor.Options.Forget`, шлюз роли (`role.Allows`), маскирование `lock_holder`.
6. `restic.CLI.Forget`, `LockedError`/`LockHolder`; OQ-156: `cat config --retry-lock 5m` у бэкапа.
7. Интеграционные тесты с настоящим restic (`internal/retention`, `internal/restic`), пакет добавлен в
   `AGENT_INTEGRATION_PKGS` шлюза.

## Что узнали
- `restic cat config --retry-lock` есть и работает, но печатает «repo already locked, waiting up to …» в
  stdout до JSON: разбор id пришлось сделать терпимым.
- `restic forget --keep-within` отсчитывает от `min(самый новый снимок, сейчас)`, а не просто от самого
  нового. Это же сделано в агенте и защищает предел от снимков «из будущего».
- Блокировка видна в `restic list locks`, а не в каталоге `locks/` (там остаются пустые подкаталоги и
  недописанные файлы): тест ждёт её через `list locks`, а держащий её `restic check` замедлен
  `--limit-download`.

## Не сделано / открыто
- «Бэкап на исключительной блокировке дольше пяти минут завершается провалом» (@restic): 5 минут ожидания в
  тесте; поведение следует из `--retry-lock 5m` и проверено на аргументах.
- Тесты «Бэкап/Восстановление работает как раньше при заданном пределе»: код этих путей не менялся, кроме
  чтения id.
