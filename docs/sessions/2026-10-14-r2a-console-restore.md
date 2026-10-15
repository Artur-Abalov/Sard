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

## 2026-10-15, re-review architect (CHANGES REQUIRED): два замечания P1

- Уборка каталога команды, не завершившейся (`removeCommand` в `consolerestore/marker.go`): сначала
  удаляются `marker.tmp` и `marker`, потом fsync каталога, только потом `RemoveTree`. Если метку удалить
  нельзя, root остаётся и запись получает `CleanupFailed`. Применено в откате `attempt.rollback` и в
  `settleEntry`. Ошибка fsync не прерывает уборку (как в `move`): порядок относительно
  убийства процесса сохранён. Назначение в метке проверяется правилом плана; неверное — как нечитаемая метка.
- Тесты: `TestARollbackRemovesTheMarkerBeforeRoot`, `TestAStartThatStopsHalfwayNeverLeavesAMarkerWithoutRoot`,
  `TestAMarkerWithAnInvalidDestinationIsNotEvidenceOfSuccess`; в `rig_test.go` `Names` по убыванию, `removed`, `removeErr`.
- ADR 0008: `repoconnect.TargetReading` читает только env-файл, путь файла пароля не хранит; `Target.passwordFile()`
  называет файл пароля по имени репозитория, когда пути нет (команды `repo` печатают путь, как раньше).
  Тесты: `TestRestoreTargetsNeverReadThePasswordFile`, `TestAFailureSentToTheServerNamesNoPasswordFile`.
- OQ-220 и строка в ADR 0060: цель восстановления на сервере знает ключи плагина `files`.

## 2026-10-15, hardener: мутационное тестирование R2a (только тесты)

Результаты, как их напечатали инструменты (`./scripts/gate.sh <модуль>`):

- sdk: выжили 4 из 86 (счёт 0.953488) -> 0 (счёт 1.000000). Пустое назначение и пустой путь отклонялись
  другим правилом, сообщение не проверялось; повтор недопустимого пути терял свою причину. Тест
  `TestRestorePlanViolationMessages`.
- agent (весь модуль): 368 выживших (0.927459) -> 322 (0.938585); новый код `consolerestore`, `pluginhost`,
  `config`, `repoinit`, `cmd/sard-agent` — выжившие убиты, кроме перечисленных ниже. Первый прогон упёрся в
  заполненный диск (кэш go-build 25 ГБ): 170 ERROR «baseline build», кэш очищен, прогон повторён.
  Новые тесты: `consolerestore/hardening_test.go` (сообщения отказов, журнал, прогресс PREPARING/RESTORING,
  ошибки Stat/Names/Open/Create/Write/Sync/Read через расширенный `faultFS` в `rig_test.go`, порядок
  удаление метки -> fsync -> удаление root, закрытие дескрипторов при старте), `export_test.go`,
  `pluginhost/console_hardening_test.go` (границы snapshot_id, хост без консольного восстановления, ошибка
  плагина при PlanRestore, схема восстановления не компилируется), `cmd/sard-agent/recovery_test.go`
  (подключение `executorStarted` в `serve`), `config/restore_test.go`, `repoinit/backend_test.go`.
- server (mutflow): выжили 23 (W2MutationTest 8 из 52, RestoreNoticeFormatterTest 15 из 58) -> остались
  только эквивалентные мутанты (ниже). Новые классы: `RestoresMutationTest` (сервис `Restores`,
  `RestoreChoice`, `RestoreTargets`: до этого ни один класс mutflow их не затрагивал — 0 найденных мутаций),
  `RestoresApiImplTest`, `RestoreDefaultsTest`, общий `RestoreScene`; дополнены `W2MutationTest`
  (restorable, kind, restore-вывод) и `RestoreNoticeFormatterTest` (границы 500 знаков, 60 с, 3600 с,
  двоичные единицы, пустой leftover, восстановление всегда сообщается).

Не убито, ждёт coder (мутант эквивалентен или нашёл ошибку; тесты не подгонялись):

1. ОШИБКА, `consolerestore/staging.go`: `dir.Mkdir(rootName, ...)` возвращает открытый каталог, который
   отбрасывается и не закрывается — утечка одного дескриптора на каждое восстановление (до сборщика мусора).
   Найдено счётчиком открытых/закрытых дескрипторов в `rig_test.go` (`faults.noLeaks`); тест на весь
   `Restore` (`hardening_leak_test.go`) зелёный после правки `rd, err := dir.Mkdir(...); defer/Close`.
   Мутанты `_ = d.Close` в `release`, `target.go`, `move` ждут этого теста.
2. Эквивалентные, предложено удалить избыточный код: `engine.go: a.stagingMade = false` в `move` (после
   переноса откат не вызывается), `UsableName: id != ""` (`filepath.Base("") == "."`), `target.go: f != nil ||`
   в `inspect` (`walk` не возвращает оба), `osdir_linux.go: runtime.KeepAlive(target)` (GC, не наблюдаем),
   `ConsoleRestore.kt:58 byte >= 0 &&` (отрицательный байт всегда даёт символ вне SAFE),
   `NoticeFormatter.kt:47` (первая ветка `headline()` для restore лишняя: `RestoreNoticeFormatter.compose`
   считает заголовок сам), `NoticeFormatter.kt:196` (ветки QUEUED/DISPATCHED/RUNNING недостижимы;
   как у бэкапа — `error(...)`).
3. proto против origin/main: `restore_dir = 10` в `RegisterRequest` совпадает с `machine_id = 10` в main
   (V1), `reject_reason = 14` main'а удалён в ветке: нужен rebase и перенумерация. Против основы R2a
   (`PROTO_BASE=4daa01e`) ворота проходят.

## Coder, раунд после hardener (слияние с main)

1. Слияние origin/main (merge commit, без rebase/force). Конфликты и решения:
   - `proto/sard/agent/v1/agent.proto`: `machine_id = 10` main'а сохранён, `RegisterRequest.restore_dir`
     переехал `10` -> `11` (R2a не выпущен). Остальные поля R2a (`Plugin.restore_config_schema = 5`,
     `RestoreOutput` 2-8) не пересекаются с main (`reject_reason = 14`, `RejectReason`). `agent.pb.go`
     перегенерирован `make proto`; `make breaking-proto` (против origin/main) проходит.
   - `agent/cmd/sard-agent/main.go`: обе стороны (роль/machine_id main'а и движок восстановления,
     recovery, `RestoreDir` R2a): `newAgent(cfg, hostname, restic, machineID, engine)`,
     `serve(ctx, cfg, role, agent, recovery)`.
   - Сервер (`RegisterRequests`, `AgentSnapshot`, `Registration`, `SnapshotRules`, `Agent`, `AgentsController`,
     `AgentsApiImpl`, `Runs`): оба поля (`machineId` и `restoreDir`, `role` и `restoreDir`), обе проверки;
     `Runs` берёт `Action.BACKUP` main'а и `RunKind.BACKUP` R2a. Тесты: оба набора кейсов.
   - Миграция R2a и `agent_role` main'а были обе `V202610141200`: R2a переименована в
     `V202610151200__console_restore.sql` (по дате+HHMM написания, монотонно после 141200); список версий в
     `SardServerIntegrationTest` и комментарии обновлены.
   - web: локали и `enums.test.ts` — `AgentRole` и `RunKind` оба; `openapi.json` и `schema.d.ts` перегенерированы
     `make openapi`; в `fixtures.ts` у агента-верификатора добавлен `restoreDir: null`.
   - agent/executor: тест «unsupported action» опирался на то, что files не умеет RESTORE; теперь файлы умеют,
     а VERIFY отсекает роль source раньше — случай переведён на обработчик «backup-only» с действием RESTORE.
2. Утечка дескриптора в `staging.go` закрыта (тест hardener'а `hardening_leak_test.go`).
3. Удалён избыточный код (эквивалентные мутанты), поведение не менялось. `runtime.KeepAlive(target)` в
   `hostsetup/osdir_linux.go` оставлен: нужен для корректности, мутант виден только по срокам GC.
4. `removeCommand` пишет WARN с путём при сбое fsync каталога (тест), комментарий исправлен; ADR 0060 п. 8.
