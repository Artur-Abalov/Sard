# Сессия 2026-10-04: журнал принятых команд и логи транспорта агента (FXa)

Исправляет Д3 (решение владельца D13) и Д4 из `t3-defects.md` (сверка
2026-10-04, `main` @ `2a21a0f`). Рабочая ветка сессии — `ccr-73c94bfe-3r1um1`
(в промпте — `fix/agent-journal-logging`). Proto не меняется.

## Фаза 1: точки аварийной остановки, формат журнала, строки лога

Всё ниже — по чтению кода; ни одной строки кода ещё не написано, тесты не
запускались.

### Как сейчас (проверено чтением)

- На диске только `results/` и `acked/` (`agent/internal/executor/store.go:21-35`),
  каждый файл с `"version":1`; неизвестная версия → файл оставлен и записан в
  лог (`store.go:181-201`, `executor.go:308-311`).
- `accept` кладёт команду только в память и сразу шлёт ACCEPTED
  (`command.go:81-93`); `start` — `command.go:145-152`.
- `restore` поднимает только результаты и надгробия (`executor.go:306-324`);
  `RunningIDs` после перезапуска пуст.
- В `transport.Options` нет логгера (`transport.go:99-113`); Hello
  (`transport.go:303-307`), `startStream` с повтором результатов (`:308`),
  `receive` (`:326-343`), `sendNext` (`:384-394`) молчат; `Run` не пишет
  ни обрыв, ни задержку повтора (`:163-180`).
- restic не знает command_id: backup зовётся из
  `pluginhost/handler.go:98` → `source.go:74` → `restic/backup.go:129` →
  `restic.go:201` (`start`, код выхода — `res.code`). `OnStderr` пуст
  (`restic.go:116-117`), в `main.go` не задаётся.

### Точки аварийной остановки и ожидаемый исход

| # | Где остановились | На диске после | Что сделает агент при старте | Что увидит сервер |
|---|---|---|---|---|
| 0 | до записи журнала (в т. ч. посреди `writeAtomic`) | ничего или `journal/.tmp-*` | `.tmp-*` удаляется; команды нет | `dispatched`, нет в Hello → повторная отправка → один запуск |
| 1 | после записи журнала, до ACCEPTED | `journal/<key>` | FAILED D13 (без `started_at`) | результат после Hello; повторная отправка → тот же результат |
| 2 | после ACCEPTED, в очереди | `journal/<key>` | FAILED D13 (без `started_at`) | то же |
| 3 | обработчик выполняется (restic идёт) | `journal/<key>` со `started_at` | FAILED D13 со `started_at` | то же |
| 4 | результат записан, журнал не удалён | `results/` + `journal/` | результат важнее; остаток журнала удаляется | прежний результат |
| 5 | журнал удалён, ResultAck не пришёл | `results/` | как сейчас (A3) | прежний результат |
| 6 | надгробие записано, результат не удалён | `acked/` + `results/` | как сейчас | — (подтверждено) |
| 7 | при старте: результат D13 записан, журнал не удалён | `results/` + `journal/` | как 4 | результат D13 |

Во всех строках обработчик для command_id вызывается не больше одного раза:
начиная с точки 1 id есть в журнале или результатах, а `restore` делает его
`finished` до первого Hello, поэтому повторный RunStep идёт в `repeat`.

### Формат (предложение)

```
<dir>/journal/<key>.json  принят, результата нет
  {"version":1,"command_id":"…","accepted_at":"…","started_at":"…"}
```

- `<key>` — тот же hex(SHA-256(command_id)); запись — тем же `writeAtomic`
  (временный файл 0600 → fsync → rename → fsync каталога); каталог 0700.
- В записи нет ни `config_json`, ни имени репозитория, ни тегов: журналу
  нужен только id и время.
- Версия — в каждой записи, как у `results/`; неизвестная версия → файл
  оставлен, предупреждение (как сейчас для результатов).
- Старое хранилище: `openStore` создаёт `journal/`, если его нет; старые
  `results/`/`acked/` читаются без изменений (формат v1 не трогается).
  Откат на старый агент: он не читает `journal/` — прерванные команды
  снова станут `lost` на сервере, результаты не теряются.
- Порядок при старте: `results`, `journal`, `acked` (надгробие важнее всего,
  результат важнее журнала).

### Строки лога (info, все — ключ/значение slog)

| Событие | msg | атрибуты |
|---|---|---|
| соединение установлено | `connected to the server` | `heartbeat` |
| соединение потеряно | `connection to the server lost` | `code` (gRPC), `error` |
| попытка переподключения | `reconnecting` | `attempt`, `delay` |
| Hello | `hello sent` | `running`, `pending_results` |
| результат ушёл в стрим | `result sent` | `command_id`, `status` |
| ResultAck | `result acknowledged` | `command_id` |
| повторная команда | `repeated command` | `command_id`, `answer` (`result`/`progress`/`none`) |
| приём | `step accepted` | `command_id`, `plugin`, `action` |
| отказ при приёме | `step rejected` | `command_id`, `reason` |
| старт шага | `step started` | `command_id` |
| завершение шага | `step finished` | `command_id`, `status` |
| старт restic | `restic started` | `command_id`, `command` (`backup`…) |
| выход restic | `restic exited` | `command_id`, `command`, `exit_code` |
| прерванные при старте | `interrupted steps reported as failed` | `count`, `command_ids` |

Не логируются: прогресс, строки шага, stderr restic, `config_json`, пути
файлов секретов и паролей.

### Ответы владельца (2026-10-04)

1. Реестр — `t3-defects.md` прислан; кладу в `docs/qa/t3-defects.md`.
2. Приём результата для шага в `lost` — отдельная серверная задача (FXs).
3. Журнал перезаписывается при старте обработчика (`started_at` — настоящий).
4. Журнал не записался → REJECTED «cannot record the command: …».
5. Логгер с `command_id` доходит до `restic.CLI`; старт и код выхода — для
   всех команд restic.
6. `restic.Options.OnStderr` удалить.
7. Сценарий — новый `docs/specs/agent/step-execution.feature`, связь с
   тестами комментариями `// Scenario:`.
8. Без `/ship-feature`; полный гейт `agent` с мутациями.
9. Ошибки соединения логируются как есть (код gRPC и текст).
10. Уровень — info.

## Фаза 2: журнал, прерванные при старте, дедупликация

### Что сделано

| Что | Где |
|---|---|
| `journal/` в хранилище, запись `entry` (version 1), общий `scan` для трёх каталогов | `agent/internal/executor/store.go` |
| приём: `check` → `record` (журнал) → очередь → ACCEPTED; ошибка журнала → REJECTED | `command.go`, `accept`, `record` |
| старт: перезапись журнала со `started_at`; ошибка → warn, шаг идёт | `command.go`, `start` |
| результат: `save` — сначала результат, потом удаление записи журнала; результат не сохранился → запись остаётся | `command.go`, `save` |
| при старте: запись без результата → FAILED D13 (`started_at` из журнала, `finished_at` — момент старта), сохраняется и уходит как неподтверждённый; запись рядом с результатом или надгробием — остаток, удаляется | `executor.go`, `restore`, `interrupted` |
| комментарий `ShutdownAbort` | `executor.go` |

Дедупликация отдельного кода не потребовала: прерванная команда при старте
сразу `finished`, повторный RunStep идёт в существующий `repeat`.

### Тесты (`agent/internal/executor/journal_test.go`)

Написаны до кода и на старом коде падали (проверил: `git stash` кода,
`go test` — 13 FAIL; потом один тест разбит на два ради gocyclo ≤ 8), на
новом проходят.

| Проверка стратегии | Тест |
|---|---|
| 1, точка 0 (до записи журнала, оборванный `.tmp-*`) | `TestAStepThatCrashedBeforeItsJournalEntryRunsOnceWhenSentAgain` |
| 1, точка 1 (журнал есть, ACCEPTED не ушёл) | `TestAJournalEntryWrittenJustBeforeTheCrashBecomesAFailure` |
| 1, точки 2–3 (в очереди и во время выполнения): FAILED D13, `started_at`, нет в `RunningIDs` | `TestAStepInterruptedByARestartFailsOnceWithTheD13Message` |
| 2 (повторный RunStep, в том числе после второго перезапуска), обработчик не вызывается дважды | `TestAStepInterruptedByARestartNeverRunsAgain` |
| 1, точки 4 и 7 (результат + остаток журнала) | `TestAResultSavedJustBeforeTheCrashWinsOverItsJournalEntry` |
| 1, надгробие + остаток журнала | `TestAnAcknowledgedCommandWinsOverItsJournalEntry` |
| 1, точки 5–6 | прежние `TestAnUnacknowledgedResultSurvivesARestartAndIsNeverRunAgain`, `TestAckedWinsOverResultsLeftByACrashBetweenTheTwoWrites` |
| 3, хранилище без `journal/` | `TestAStateDirWithoutAJournalIsReadAndKeepsItsResults` |
| права 0700/0600; в записи нет конфига, тегов, репозитория, плагина | `TestJournalEntriesAreOwnerOnly`, `TestAJournalEntryHoldsNoStepConfig` |
| сбои записи | `TestACommandThatCannotBeJournaledIsRejectedWithoutRunning`, `TestAJournalThatBreaksAfterAcceptLetsTheStepsRunAndIsReported`, `TestAnInterruptedStepWhoseFailureCannotBeSavedIsStillReported` |
| повреждённые и будущие версии записей, каталог-файл | `TestCorruptOrUnknownJournalEntriesAreKeptAndReported`, `TestAnUnreadableJournalDirStopsTheStart` |

«Аварийная остановка» в тестах — `ShutdownAbort` и новый исполнитель на том
же каталоге (как `fixture.restart`): результатов не пишет, как убитый
процесс. Остановка посреди `writeAtomic` смоделирована оставленным
`.tmp-*`; сам `writeAtomic` не менялся.

### Известное поведение

- Если restic успел записать снимок, а агент умер до сохранения результата,
  шаг всё равно FAILED D13 — снимок в репозитории останется (связать его с
  шагом — Д5, вне задачи).
- Один лишний fsync на шаг (перезапись при старте) под замком исполнителя.

### Гейт и мутации (фаза 2)

- `cd agent && go test -race ./...` — exit 0.
- `./scripts/gate.sh agent` (полный) — `gate: PASSED (agent, full)`, mutation
  score 0.926749. Первый прогон оборвался на лимите фоновой задачи и оставил
  мутант в `repo_list_run.go`; файл восстановлен из резервной копии
  go-mutesting (совпадала с HEAD), гейт перезапущен целиком.
- go-mutesting с `--debug` по `store.go`, `command.go`, `executor.go`:
  0.895 (257/287). Выжившие в новом коде: `continue`→`break` в
  `interrupted`, лог `dropLeftover`, две ошибки `readEntry` — добавлены
  `TestAJournalLeftoverDoesNotHideTheInterruptedStepsAfterIt`,
  `TestALeftoverThatCannotBeRemovedIsReported`,
  `TestAnUnreadableJournalEntryIsReportedWithItsOwnError`; все четыре мутанта
  проверены вручную — убиты.
- Не убиты (нельзя без внедрения сбоя под root, тот же класс, что прежние
  выжившие в `write`/`restore`): ошибка `json.Marshal` записи журнала
  (`store.go`, `journal`), сбой `ReadDir` при успешном `MkdirAll`
  (`store.go`, `scan`; `executor.go`, `restore` после `loadJournal`).

## Фаза 3: журнал агента, спецификация, реестр (2026-10-05)

CI `server` на `9d7ec8b` упал на mutflow (4 из 11085, имён в логе нет). PR
сервер не трогает, на `2e70ef2` с той же базой `server` прошёл — комментарий
в PR #41, упавший job перезапущен один раз после конца run.

### Что сделано

| Что | Где |
|---|---|
| `transport.Options.Logger`: `connected to the server` (heartbeat), `hello sent` (running, pending_results), `result sent` (command_id, status), `result acknowledged` (command_id), `connection to the server lost` (code, error), `reconnecting` (attempt, delay), ERROR при окончательном отказе | `agent/internal/transport/transport.go` |
| исполнитель: `step accepted` (command_id, plugin, action), `step started`, `step finished` (status), `repeated command` (answer: result/progress), `interrupted steps reported as failed` (count, command_ids, отсортированы) | `agent/internal/executor/command.go`, `executor.go` |
| restic: `Options.Logger`, `restic started`/`restic exited` (command, exit_code, error при сбое запуска); `CLI.ForStep(commandID, stderr)`; `Options.OnStderr` удалён (ответ 6) | `agent/internal/restic/restic.go` |
| `pluginhost.Repositories` получает command_id шага | `agent/internal/pluginhost/handler.go` |
| сборка: `slog.Default()` в транспорт и restic, `get` → `ForStep` | `agent/cmd/sard-agent/main.go` |
| спецификация: 11 сценариев, каждый связан с тестом `// Scenario:` (проверено скриптом в обе стороны) | `docs/specs/agent/step-execution.feature` |
| реестр: Д3, Д4 закрыты со ссылками | `docs/qa/t3-defects.md` |
| ADR (черновик, номер при слиянии) | `docs/adr/00XX-draft-agent-command-journal.md` |

Отказ при приёме отдельной строкой не пишется: `step finished
status=STEP_STATUS_REJECTED` уже есть, причина (текст результата) в журнал
агента не идёт. Это отступление от таблицы фазы 1.

### Тест 4 стратегии

- `agent/internal/transport/logs_test.go` — записывающий `slog.Handler`;
  строки на подключение, Hello, отправку результата, ResultAck, обрыв (код),
  переподключение (попытка 1, 1 s); текст результата («hunter2») в журнал не
  попал; код Unavailable при отказе Register; ERROR при PermissionDenied.
- `agent/internal/executor/logs_test.go` — приём, повтор (progress, result),
  старт, завершение, REJECTED, прерванные при старте; конфиг шага в журнал не
  попал.
- `agent/internal/restic/restic_test.go` — старт и код выхода с command_id;
  stderr restic, путь репозитория и аргументы в журнал не попали.

Тесты, проверявшие удалённый `OnStderr` (`TestStderrLinesReachTheCallback`,
`TestStderrCallbackIsOptional`, счётчик строк в
`TestBackupWithUnreadableFilesReturnsSummaryAndPartialError`), заменены
проверками журнала: удалена сама возможность, не проверка. Интеграционные
тесты restic и pluginhost пишут stderr restic в `t.Output()`.
