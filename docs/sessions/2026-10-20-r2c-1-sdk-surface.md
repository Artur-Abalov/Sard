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

- Метрики до: все функции из файлов R2c-1 не выше CRAP 6.0
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

## coder: замечания architect (APPROVED)

- `stepJournal` получил флаг `closed` (под тем же мьютексом, что и проверка
  секретов); `Save` после закрытия возвращает ошибку и запись не меняется.
  Закрывается при выходе из `recoverWithin` (в том числе по сроку, после
  grace и по панике) и при выходе из `restoreByPlugin`. Тесты вперёд: плагин,
  игнорирующий контекст и сохраняющий после срока, оставил запись `late`;
  плагин, сохранивший из горутины после конца шага, воссоздал удалённую
  запись (дыра в Restore была настоящей). Теперь оба `Save` отказывают.
- Обёртки `newAgent` и `newRecovery` удалены; тесты зовут `buildAgent` и
  `newRecoveryWith`.
- ADR 0066 п. 7: Extractor — R2c-3; записаны пустой этап -> `restore`,
  замена на `PLUGIN_FAILED` (теряет `Detail`, сохраняет `Leftover`, `Retain`)
  и закрытие журнала шага.
- `./scripts/gate.sh sdk fast` и `./scripts/gate.sh agent fast`: PASSED
  (0 issues линтера; CRAP не выше 6.0, худшие функции `repo_restore_run.go:
  restoreRun.restore` и прочие по 6.0; процент покрытия agent в этом прогоне не печатался).
- Дальше: `cleaner`.

## hardener

Мутанты считались инструментом (`go-mutesting` через `scripts/mutate-exec.sh`),
по пакетам и файлам изменённого кода; числа ниже из его вывода.

Выживших до -> после:

| Модуль / файлы | до | после |
|---|---|---|
| sdk: `restore_run.go`, `restore_error.go` | 7 (оценка 0.890625) | 0 (полный gate: 184 мутанта, 151 убит, 0 выжило, 1.000000) |
| agent/internal/restorejournal | 11 (0.803571) | 0 (1.000000) |
| agent/internal/refusal (пакет) | 3 (0.921053) | 0 (1.000000) |
| agent/internal/pluginhost: `restore_run.go`, `journal.go`, `journal_recovery.go`, `source.go`, `hint.go`, `handler.go` | 40 (0.865772) | 0 (1.000000) |
| agent/internal/pluginhost/journaltest | 4 (у пакета не было тестов, 0.000000) | 0 (1.000000) |
| agent/internal/consolerestore/resticfail.go | 0 (1.000000) | 0 |
| agent/cmd/sard-agent: `main.go`, `recovery.go`, `runtime.go` | 10 (0.916667) | 8, все в строках до R2c-1 (см. ниже) |

Тесты (поведение, не мутанты): границы `ValidReason`/`ValidStage` ('a', 'z',
'{', '`', '@'), значения `PhasePreparing/PhaseRestoring`; идентификаторы
команд `.hidden`, `a/..`, `/abs` в `Save` и `Remove`, скрытые файлы и
`name.json.tmp-*` без точки при `List`, причина ошибки чтения записи
(`syscall.EISDIR`), а не «unreadable»; журнал без логгера, текст и уровень
строк о (не)удалённой записи, оставленная записью `Retain` удаляется только
успешным результатом; значения по умолчанию и границы `Timeout`/`Grace`
(5 минут, 10 секунд, 1 нс сохраняется, 0 и отрицательные заменяются);
журнал, который нельзя прочитать, провал и исходы разбора пишутся в лог с
плагином, командой и путём; уровни `Log` плагина, неизвестный уровень -> INFO;
исход разбора отдаётся только действию restore, а команда, о которой
исполнитель не спрашивал, не ждёт; остановленный поток собственным контекстом
плагина возвращает причину плагина, а не `*RestoreError`; поток, который
оборвал шаг, -> `INTERRUPTED`; хост без журнала отклоняет собственное
восстановление плагина; остаток не пишется в лог, если ничего не осталось;
секрет из одного байта блокирует запись; `verify` проверяет конфиг и
snapshot_id до восстановления и называет каталог восстановления, а не каталог
команды; исход плагина (`Restored`) — результат прерванной команды в
`recovery.interrupted`; у `journaltest` появились свои тесты.

Изменения кода (поведение прежнее, избыточное убрано), каждое — по мутанту:

- `restorejournal.usable`: проверки `id != ""`, `"."`, `".."` избыточны при
  `filepath.Base(id) == id` и запрете префикса `.` (`Base("")` это `"."`).
  `Remove`: `err != nil ||` избыточно, `RemoveFile` при ошибке всегда даёт
  `removed == false`.
- `sdk`: `allBytes(s[1:], ...)` вместе с проверкой `s[0]` заменены одной
  `matches(s, first, rest)`; срез `s[0:]` и начало цикла с 1 были
  эквивалентными мутантами.
- `pluginhost`: `recovererOf` (`reg.Get` даёт nil для отсутствующего плагина,
  nil не `Recoverer`); `Interrupted` отмечает `notified` без условия
  `out == nil` (для команды без исхода отметка ни на что не влияет);
  `ExecutorStarted` берёт список под `defer`-разблокировкой в `unnotified`;
  `Log` использует нулевой `slog.Level` (INFO) для неизвестного уровня;
  `RecoverJournal` не делает `return rec` после ошибки чтения журнала (списки
  пусты); `pluginFailure.Unwrap` без ветки `cause == nil` (причина всегда
  задана); `startRecovery`/`awaitRecovery` ждут закрытия канала и читают
  результат после него — буфер канала (мутант 1->2 эквивалентен, 1->0 был
  утечкой горутины) больше не нужен; `Source.upload`: `streamErr != nil &&`
  избыточно (`errors.Is(err, nil)` ложно для `err != nil`); `withCheckHint`:
  `err == nil ||` избыточно (проверено тестом `withCheckHint(nil)`).
- `refusal`: `SameFileAtPath` без раннего `return false` (`os.SameFile(nil, x)`
  ложно); `ClassAgentError Class = iota + 1` -> `_ Class = iota` и далее,
  мутант `iota + 2` был эквивалентным (номера классов нигде не наблюдаемы).

Исключений и подавлений нет. Не убито, всё вне изменений R2c-1 (строки
`cmd/sard-agent/main.go` до R2c-1): возврат `err` при создании транспорта и
исполнителя в `serve`, `args[0:]` вместо `args[1:]` для `enroll` и `check`,
`shutdownTimeout` 44/46 с, `stop()` перед `os.Exit` (уже помечен как
эквивалентный), строка `Usage of sard-agent:`. Они учтены в общем счёте
gate, но не в этой работе.

Замечание вне задачи: `usable("/")` истинно (`filepath.Base("/") == "/"`),
запись получила бы путь `<dir>/.json`; обхода каталога нет, поведение не
менялось, на решение coder.

Gate:

- `./scripts/gate.sh sdk`: PASSED (full), 184 мутанта, 151 убит (0 по таймауту),
  0 выжило, оценка 1.000000, код выхода 0.
- `./scripts/gate.sh agent`: PASSED (full), код выхода 0; gofmt/vet/golangci-lint
  0 issues, тесты и покрытие, CRAP, интеграционные тесты с restic, сборка e2e
  пройдены; мутации: 7799 мутантов, 5921 убито (317 по таймауту), 431 выжил,
  оценка 0.944737 (шарды: 0.929896, 0.958844, 0.955711, 0.936647). Выжившие в
  файлах этой работы: только 8 в `cmd/sard-agent/main.go`, описанные выше.
- Заметка об окружении: первый полный прогон `gate.sh agent` остановился на
  «no space left on device» (кэш сборки Go, 26 ГБ); кэш очищен (`go clean
  -cache`), прогон повторён целиком.
