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

## Вопросы владельцу — см. ответ в чате; ответы впишу сюда.
