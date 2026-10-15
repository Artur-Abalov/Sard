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

## Правки по замечаниям architect (F1, F3, F4, F6, claim)
- F1: сбой `Sync` каталога после успешного rename — предупреждение (WARN), восстановление считается состоявшимся. Тест `TestASyncFailureAfterTheMoveDoesNotDenyARestoreThatHappened`.
- F3: `Engine.Restore` сам проверяет план (`RestorePlan.Validate`) до записи; отказ `TARGET_INVALID (check)`. Тест на `../escape`, `a/../../escape`, `.sard-staging/x`, пустое назначение.
- F4: свой классификатор ошибок restic в `consolerestore` удалён; используются `repoinit.FromRestic`, `restore.ListFailure`, `restore.RequirePaths`, `restore.TargetFailure` (без пометки о частичном восстановлении, `restore.IncompleteText` общий). Движок получает `repoinit.Target` репозитория (`Options.Target`, в main — `repoconnect.TargetReading` читает env_file и пароль, чтобы скрыть значения). Причина «иная ошибка restic» для локального репозитория остаётся `RESTIC_FAILED` (сценарий спецификации), для удалённого — `BACKEND_REFUSED` как у A8c. Не тронуто: `missingOrInvalid` в `consolerestore/target.go` — другое сообщение (`NotDirError`), не классификатор restic.
- claim: назначения, вложенные друг в друга (`a` и `a/b`), считаются занятыми (`TARGET_BUSY`).
- F6: сервер отдаёт у шага `restoreOutcome` (`restored | nothing | rollback_failed | late`), решение принимает `RestoreMapping.outcome`; `restoreOutcome()` и его тест удалены из web, страница только сопоставляет значение строкам i18n. `make openapi` перегенерировал `openapi.json` и `schema.d.ts`.
- F2 и F5 ждали правки спецификации (см. ниже).

## Поправка владельца 2026-10-15: F2 и F5 (ВК13, ВК14)
- Временный каталог — `.sard-staging/<command_id>/root`; restic пишет в `root` через
  удерживаемый дескриптор (`RestoreRequest.Staging`, `Target` пуст) — F5. Интеграционный
  тест с настоящим restic проходит.
- Перед переносом в `<command_id>` пишется метка (`marker.go`: временный файл, fsync,
  rename, fsync каталога); переносится только `root`. `<command_id>` с меткой остаётся до
  записи результата в журнал исполнителя: новая опция `executor.Options.Journaled`
  (вызывается после успешного сохранения результата), `Interruption.Succeeded`.
- `Engine.CleanStaging` возвращает `[]Evidence` по каждой команде (`NothingRestored`,
  `Completed` с назначением и итогом из метки, `CleanupFailed`); `Engine.Settle(id)`
  удаляет каталог команды (имя из одного компонента). `pluginhost.Interrupted` формулирует
  исход по свидетельству; `cmd/sard-agent/recovery.go` связывает их с исполнителем и
  удаляет каталоги `Completed`, о которых исполнитель не спрашивал (неизвестная команда
  или результат уже сохранён).
- Тесты: все три случая ВК14, сбой уборки root, нечитаемая метка, метка до переноса,
  `Settle` не выходит из `.sard-staging` (поймал `".."`), запрос к restic (`Staging != nil`,
  `Target == ""`).
- Сервер: сценарий «Успех, о котором агент сообщил после перезапуска, записывается как
  успех» — добавлен интеграционный тест `RestoresGrpcApiIntegrationTest`.
