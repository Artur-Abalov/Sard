# Сессия 2026-10-01: подключение маскирования к логам шагов (A7c)

Ветка `claude/cool-cray-h11smy` (назначена окружением; в промпте —
`feat/a7c-log-redaction`).

## Фаза 1: карта путей вывода, жизненный цикл, OQ-006 (СТОП, ждёт ответов владельца)

### Спецификация A7b
`features/A7b-log-masking.md` в репозитории нет — ни в `main`, ни в одной
удалённой ветке (проверил `git ls-tree` по всем веткам). Набор значений и
то, что видит пользователь, не определены.

### Пути, по которым текст шага уходит на сервер (проверил чтением)

| Источник | Путь | Маскируется сейчас | Попадает в `LogChunk` |
|---|---|---|---|
| Плагин, `sdk.Host.Log` | `pluginhost/source.go:144-150` → `executor/reporter.go:46-55` → `Sink.Log` → `transport/outbox.go:87-100` (обратное давление, 1024 строки) → `takeChunk` `:148-162` (до 64 строк одного command_id) | нет | да |
| restic stderr | `restic/exec.go:55-75` (`lineWriter`, разбиение по `\n`) → `restic.go:227-245` → `Options.OnStderr` | нет | **нет**: `OnStderr` в `main.go` и `repo_cmd.go` не задан, по умолчанию пустая функция (`restic.go:113-115`) |
| restic stdout | `backup.go:247-258` разбирает JSON: прогресс и итог; `restore` stdout отбрасывает | — | нет |
| Ошибки шага | `executor/command.go:198-230` → `StepResult.message` (сохраняется на диск, `store.saveResult`); `pluginhost/handler.go:170-173` → `CheckResult.detail` | нет | нет, но уходит на сервер. Несёт `ExitError` restic (`restic.go:84-89`), пути (`backup.go:71-89`, `files/check.go:35-90`), паника плагина (`command.go:201`) |

- Ни один рабочий плагин не вызывает `Host.Log`: `files` игнорирует `Host`
  (`plugins/files/plugin.go:52,61`), mysql/postgresql — заглушки. Вызывает
  только тестовый плагин (`pluginhost/testplugin/plugin.go:93`, имя и длина
  секрета, без значения).
- Комментарий в `proto/sard/agent/v1/agent.proto:218` («the agent redacts
  known secret values») сейчас не соответствует коду.
- Плагин `files` не может сослаться на секрет: схема без `sard-secret`,
  `additionalProperties:false`.

### Как подключён redact в repoinit
`repoinit/backend.go:146-171` (`Scrubber`): на каждый вызов `redact.New`
+ `Write` + `Close` в буфер. Значения: правые части `KEY=VALUE` из
`env_file`, пароль из userinfo URL репозитория, сгенерированный пароль.
`password_file` не читается. `OnShortValue` нигде не используется.

### Жизненный цикл шага
- Старт: `command.go:144-151` (`start`: контекст с причиной, таймер
  `expire`, `go e.run`).
- Завершение: `run` `:167-176` → `finish` `:272-284` (под мьютексом
  исполнителя).
- Отмена и таймаут: `interrupt` `:233-240` → `watch`/`recheck`
  `:244-263` → через 30 с `giveUp` `:266-269` (результат без ожидания
  обработчика; поздние строки отбрасываются `reporter.Log` по `live()`).
- Паника: `recover` в `call` `:186-195`, только в горутине обработчика.
- `Sink.Log` может блокироваться и вызывается без мьютекса; `finish` и
  `giveUp` — под мьютексом. Значит, `Close` маскировщика (выдаёт хвост в
  `Sink.Log`) нельзя звать из `finish` как есть.
- `redact.New` с 42 значениями — 7,5 мс (журнал A7a); на каждую строку
  строить автомат заново нельзя.

### OQ-006
Исходный вопрос (журнал A5a:211-213): колбэк прогресса restic ~10 раз в
секунду, прореживать перед отправкой на сервер. Это и делает
`reporter.ProgressFiles` (`executor/reporter.go:23-34`): не чаще
`ProgressInterval` (1 с по умолчанию, `executor.go:124`) на шаг в пределах
фазы, новая фаза проходит сразу. Дополнительно: `outbox.Progress` держит
только последний отчёт команды (`outbox.go:58-69`), сервер пишет в БД не
чаще 5 с (S7a). Вопрос тот же — к закрытию в фазе 3.
Остаток (не входил в исходный вопрос): последний отчёт фазы, пришедший
меньше чем через 1 с после предыдущего, теряется (нет досылки).

### Вопросы владельцу — см. ответ в чате; ответы записать сюда.

### Ответы владельца (2026-10-01)
1. Набор значений — временное правило (б): все `secrets:` агента, `env_file`
   и пароль из URL репозитория шага; за интерфейсом, A7b заменит.
2. stderr restic подключить к логам шага; строка сырая, уровень WARN для
   ошибок, INFO для остального.
3. `StepResult.message` и `CheckResult.detail` маскировать.
4. stderr — один поток на шаг, маскирование до разбиения на строки;
   `Host.Log` — каждый вызов отдельно.
5. `redact.Compile → *Set`, `Set.NewWriter` — да.
6. Значения не прочитались — шаг не запускать, в тексте имя секрета, шаг
   падает. (Толкование: FAILED, не REJECTED: это неисправность хоста, а не
   неверный конфиг с сервера.)
7. Маскировщик живёт в исполнителе; хвост — вне мьютекса, до `Result`.
8. Короткие значения — предупреждение в журнал агента с именем секрета.
9. OQ-006 закрыть; потерю последнего отчёта фазы завести отдельным OQ.
10. Работать в `claude/cool-cray-h11smy`.

## Фаза 2: подключение, поставщик значений, тесты 1–3 (СТОП)

Решения и причины — ADR 0031.

### Сделано
- `redact`: `Compile(values, opts) (*Set, error)`, `Set.NewWriter`,
  `Set.Mask` (nil-набор возвращает текст как есть); `New` — обёртка, repoinit
  не менялся по поведению.
- `steplog.Lines` (новый пакет): потоковое маскирование → разбиение на строки
  (без `\r`, ≤ 8192 байт, невалидный UTF-8 → U+FFFD); после `Close` запись
  отбрасывается без ошибки; потокобезопасен.
- `executor`: `Options.Secrets` (`Secrets.For(step)`), `Options.OutputLevel`,
  `Reporter.Output()`; `prepare` до обработчика, `Lines.Close` после него вне
  мьютекса и до результата; асинхронный `Close` в `giveUp`; `maskResult` в
  `finish` (сообщение и `CheckResult.detail`) до записи на диск.
- `restic`: `Command.StderrCopy` (сырой stderr через `io.MultiWriter`),
  `Options.Stderr`, `CLI.WithStderr`, `ErrorLine`.
- `pluginhost`: `Repositories(name, stderr)`, `OutputLevel`.
- `stepsecrets` (новый пакет): реализация `executor.Secrets` по правилу (б).
- `config.URLPassword` перенесён из `repoinit` (общий для двух мест).
- `main`: `Secrets: stepsecrets.New(cfg, os.ReadFile)`,
  `OutputLevel: pluginhost.OutputLevel`, `get(name, stderr)` →
  `CLI.WithStderr`.

### Тесты по стратегии (проверил запуском)
1. Источники: `Host.Log` (`TestThePluginsLogLinesAreMasked`), вывод
   инструмента с разрывом значения между порциями и по байту
   (`TestToolOutputIsMaskedAcrossWritesAndCutIntoWholeLines`,
   `TestASecretSplitAcrossLogChunksNeverReachesTheSink`), сообщение
   результата, в том числе сохранённое на диск
   (`TestTheResultMessageIsMaskedBeforeItIsSentOrStored`), паника
   (`TestAPanicValueIsMasked`), `CheckResult.detail`. Настоящий restic 0.19.1:
   `TestResticStderrReachesTheStepLogMasked` (тег integration) — путь
   несуществующего репозитория содержит секрет агента; в логе
   `Fatal: … repo-[REDACTED]/config …` с уровнем WARN, значения нет ни в
   строках, ни в результате.
2. Хвост: отмена, таймаут, паника — строка с маркером приходит до
   результата; `giveUp` — после результата, поздний вывод отброшен.
3. Порядок и неизменность строк без секретов — `steplog`
   (`TestLinesWithoutSecretsPassUnchangedAndInOrder`,
   `TestConcurrentCloseNeverLeaksOrTearsALine`), исполнитель
   (`TestWithoutValuesLinesPassUnchanged`).
- Поставщик: все секреты, `env_file` и URL только репозитория шага,
  `password_file` не открывается, ошибки без путей и значений — 100%
  покрытия пакета.

### Проверено
- `go test -race ./...` в `agent/` — ok; исполнитель `-race -count=10` — ok.
- `./scripts/gate.sh agent fast` — PASSED (покрытие 98.0%, CRAP ≤ 6,
  integration-тесты с restic 0.19.1).
- go-mutesting по новым и изменённым пакетам: `steplog` + `stepsecrets` —
  выживших нет после правок (убраны две эквивалентные ветки: проверка
  `closed` в `Close`, ранний возврат без репозитория); `redact` 0.955
  (211/221; 10 выживших — те же эквивалентные, что в A7a; новая
  эквивалентная `err != nil` в `Compile` убрана упрощением условия);
  `config.URLPassword`: эквивалентная проверка `u.User == nil` убрана
  (`(*Userinfo).Password` безопасен для nil).

- `./scripts/gate.sh agent` (полный) — PASSED: покрытие 98.0%, CRAP ≤ 6,
  integration-тесты, mutation score 0.925285.
- go-mutesting по изменённым файлам (`redaction.go`, `reporter.go`,
  `command.go`, `executor.go`, `handler.go`, `exec.go`, `restic.go`,
  `backend.go`, `main.go`): выжившие в моих строках — только в
  `executor/redaction.go` (фильтр пустых значений `len > 0` → `>= 0`,
  `> -1`, `> 1`). Это была дыра в тестах: пустое значение рядом с
  настоящим отключало бы весь набор. Добавлен
  `TestAnEmptySecretDoesNotDisableTheOthers`; `redaction.go` — 1.0.
  Остальные выжившие (`executor.go`: константа `defaultRetention`, логи
  ошибок удаления; `handler.go:199,203`; `exec.go:77-78`) — в строках,
  которые A7c не менял.
- После добавления теста: `./scripts/gate.sh agent fast` — PASSED.

### Попутно
- `transport.TestOutboxLogWaitsForSpace` упал один раз в гейте:
  `sent ["log c1 1 2" "log c1 3"]`. Причина в самом тесте: `drain` читает
  очередь циклом, а `next()` освобождает место и будит ожидающий `Log("3")`
  между двумя чтениями. Тест теперь берёт одно сообщение. Повтором (7500
  прогонов старой версии под нагрузкой) не воспроизвелось — причина выведена
  из вывода и кода, не из воспроизведения.
- Прерванный таймаутом прогон гейта (10 мин) и мой прерванный go-mutesting
  оставили мутанты в `cmd/sard-agent/enroll_run.go` и
  `internal/executor/command.go` (рядом `.go.tmp`). Оригиналы восстановлены,
  сверены с `HEAD`/текущей правкой; повторный прогон гейта шёл только после
  этого. Вывод: go-mutesting не прерывать и не запускать параллельно с
  тестами.
