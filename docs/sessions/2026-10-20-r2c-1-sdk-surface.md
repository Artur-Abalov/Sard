<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-20 — R2c-1: поверхность SDK восстановления, журнал хоста, разбор при старте

Ветка `claude/epic-albattani-e099w7`. Спецификация
`docs/specs/agent/restore-ownership.feature` одобрена владельцем 2026-10-20,
реализованы все сценарии с тегом `@r2c-1`; ADR 0066 (этапы), QA
`docs/qa/restore-ownership.md`. PostgreSQL по-прежнему идёт через движок
`consolerestore`; кода конкретной базы в новом коде нет.

## Что сделано

- **SDK** (`agent/plugins/sdk`, Apache-2.0, только stdlib): `RestoreRunner`,
  `RestoreHost` (`Phase`, `Snapshots`, `Dump`, `Journal`), `Journal` (только
  `Save`), `RestoreRequest`, `RestoreResult`, `SnapshotInfo`, `Phase`,
  `RestoreError` с `Retain` и `Err`, `AtStage`, `ValidReason`/`ValidStage`,
  общие коды причин (`GenericReasons`), `Recoverer`, `RecoveryHost`,
  `Recovery` (`Restored`, `NothingRestored`, `LeftBehind`).
- **Журнал хоста** (`agent/internal/restorejournal`): каталог
  `<executor.state_dir>/restore-journal` (0700), файл `<command_id>.json` (0600)
  с конвертом `{plugin, command_id, entry}`; запись через `hostsetup.WriteFile`
  (временный файл, fsync, rename, fsync каталога); пределы 1 МиБ и 64 записи на
  плагин (переписать существующую можно); перечень при старте с разбором
  испорченных файлов; уборка временных файлов, оставленных падением.
- **pluginhost**: выбор пути (`Source.runner`): план без `RestoreRunner` — дерево
  через `consolerestore.Engine`, как раньше; плагин с `RestoreRunner` — без
  временного каталога, через `restoreHost`. Классификация ошибок `Snapshots` и
  `Dump` (`Engine.SnapshotsFailure`/`DumpFailure`), проверка образца кода
  причины и стадии (иначе `PLUGIN_FAILED`, добавлен в `internal/refusal`),
  отказ записи со значением секрета, прочитанного плагином в шаге (О5),
  подсказка check при `sdk.ErrProgramMissing` в цепочке ошибки.
  `Journal` хоста удаляет запись после записи результата (`Journaled`),
  кроме `Retain` при неуспехе (О1). `RecoverJournal` при старте до исполнителя и
  без сервера отдаёт каждому плагину только его записи, срок 5 минут + 10 секунд
  на плагин, который не слушает контекст, паника плагина — исход
  `CLEANUP_FAILED`.
- **cmd/sard-agent**: журнал и разбор подключены в `startWith`; `recovery`
  объединяет исходы движка и журнала.
- **Тестовый плагин** `internal/pluginhost/journaltest` (не в `Register`; тест
  `TestNoCodeOfTheAgentImportsThePluginOfTheTests`).

## Отклонения и решения

- `Extractor` в SDK не добавлен: перечень этапа R2c-1 владельца его не содержит
  (сценарии выгрузки — `@r2c-3`). Сценарий «files не реализует ... выгрузки»
  проверяет отсутствие методов `Restore`, `Recover`, `PlanExtract` по имени.
- Стадия не задана — `restore`; стадия или причина вне образца — `PLUGIN_FAILED`
  со стадией `restore` (В3, В11); ошибка плагина не `*RestoreError` —
  `PLUGIN_FAILED (restore): <текст>`. Текст в `Detail` при недопустимой причине не
  передаётся.
- Плагин с собственным восстановлением: тег `sard.source` не требуется, план не
  проверяется правилами плана (его не используют).
- Журнал хоста в отдельном пакете `restorejournal`: `pluginhost` по
  `imports_test.go` не может импортировать `hostsetup`.
- Ошибка получателя `Dump` отменяет контекст restic (SIGTERM группе): restic мог бы
  не заметить закрытый вывод.
- Тесты отмены и таймаута идут через настоящий `restic.CLI` и фальшивый
  `Executor`: «SIGTERM своей группе, через 10 секунд SIGKILL» — поведение
  `ProcessExecutor` (его тесты в `internal/restic`), здесь проверяется, что
  контекст restic отменён. Таймаут шага в тесте 100 мс вместо 2 с.
- `agentRuntime.journalPlugins` — точка подмены для теста порядка «разбор до
  первого подключения».
- ADR 0066 и шапка спецификации остаются «предложено» — статус меняет specifier.
- Мутационное тестирование не запускалось (`fast`).

## Результаты

- `./scripts/gate.sh sdk fast`: пройден; покрытие 100.0%, CRAP не выше 6.0.
- `./scripts/gate.sh agent fast`: пройден (включая интеграционные тесты с
  restic и сборку e2e); покрытие 98.5%, CRAP не выше 6.0.
- `make license-check`: 1639 файлов в порядке.

Дальше: `cleaner` (метрики), затем architect и hardener (мутационное
тестирование — на их шаге).

## cleaner

- Метрики до: все функции из файлов R2c-1 не выше CRAP 6.0, покрытие 100.0%
  (sdk: `ValidReason` 6.0, `ValidStage` 5.0, остальные не выше 3.0; agent:
  `run` 6.0, `agentRuntime.withDefaults` 6.0, прочее ниже порога).
- Просмотрены `restore_run.go`, `journal.go`, `journal_recovery.go`,
  `restorejournal/journal.go`, `sdk/restore_error.go`: длинных функций, мёртвого
  кода и значимого дублирования нет. Единственное повторение — запись
  `noteSecret` после успешного `Secret` в `restoreHost` и `recoveryHost`
  (три строки); вынос не оправдан, не тронуто.
- Код и тесты не менялись. `./scripts/gate.sh sdk fast` и
  `./scripts/gate.sh agent fast`: пройдены.
- Дальше: `architect`.
