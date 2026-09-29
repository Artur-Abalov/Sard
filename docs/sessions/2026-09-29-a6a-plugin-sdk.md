# Сессия 2026-09-29: SDK плагинов агента (A6a)

Ветка `claude/agent-plugin-sdk-0q6asa` (назначена окружением; в промпте —
`feat/a6a-plugin-sdk`).

## Фаза 1: исследование и дизайн (СТОП, ждёт ответов владельца)

### Что уже есть (проверил чтением)
- `agent/plugins/sdk/plugin.go:31-45` — `Plugin{Name, ConfigSchema, Prepare,
  Dump, Stream, Verify}`, `Dump{Paths}`; реестр `registry.go`. Четыре
  встроенных плагина — заглушки `ErrNotImplemented`.
- `executor.Handler` (`agent/internal/executor/executor.go:38-47`):
  `Actions()` + `Run(ctx, *RunStep, Reporter) (*StepResult, error)`;
  `ErrRejected` (`:73`) даёт REJECTED, только до побочных эффектов.
  Реестр обработчиков — по имени плагина; `app.NoHandlers`
  (`agent/internal/app/app.go:97-102`) подставлен в `cmd/sard-agent/main.go:148`.
- Register (`app.go:68-77`): версия плагина = версия агента, actions =
  BACKUP, RESTORE, VERIFY для всех плагинов.
- Обёртка restic: `Command` без stdin (`agent/internal/restic/exec.go:26-36`),
  `BackupRequest{Paths, Excludes, Tags}` (`backup.go:17-21`), отмена —
  SIGTERM группе, SIGKILL через 10 с (`exec.go:40-51`).
- Секреты: `internal/secrets` только проверяет права
  (`secrets.go:107`); чтения значения по имени нет.
- Ограничения Register (`server/.../registration/SnapshotRules.kt`): имя
  `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`, версия `^[!-~]{1,64}$`, ≤ 64
  плагина, `config_schema` ≤ 64 КиБ и один JSON-документ, actions без
  UNSPECIFIED и повторов.
- Ключ/сертификат: `tls.LoadX509KeyPair` вызывается лениво в рукопожатии
  (`agent/internal/transport/transport.go:213-236`), при старте не
  проверяется (OQ-027).
- Окружение: restic 0.19.1 и buf собраны в `.bin/`; Docker-демона нет —
  e2e (проверка 6) здесь не запускается, в CI есть job `e2e`.

### Черновик дизайна
- `sdk.Plugin`: те же четыре шага + `Version()`; шаги получают `sdk.Host`
  (секрет по имени, лог). `Dump` возвращает либо пути (+ исключения), либо
  потоковый дамп (`Stream: true`, имя файла в снимке) — тогда хост вызывает
  `Stream(ctx, d, w)`, `w` — stdin restic. `Verify` — необязательный
  интерфейс `sdk.Verifier`; actions выводит хост.
- Валидация — на стороне агента (`agent/internal/pluginhost`, AGPL),
  `santhosh-tekuri/jsonschema/v6` (уже в go.mod для тестов, Apache-2.0,
  2020-12, без транзитивных зависимостей) переходит в runtime; ADR.
  Ошибки: `*sdk.ConfigError{Violations[{Path, Message}]}`,
  `*sdk.SecretError{Name}` (`errors.Is(err, sdk.ErrUnknownSecret)`);
  адаптер оборачивает их в `executor.ErrRejected`.
- Поток: `restic.Command.Stdin io.Reader` и `BackupRequest.Stdin` +
  `StdinFilename` (`--stdin --stdin-filename`). Если `Stream` вернул ошибку,
  restic убивается до закрытия stdin — иначе restic примет EOF и сохранит
  обрезанный снимок.
- Фазы: Prepare → PREPARING, Dump/Stream → DUMPING, restic → UPLOADING,
  Verify → VERIFYING.
- `BackupOutput.repository_id = 4` (аддитивно).

### Ответы владельца
1. Секреты помечаются `"format": "sard-secret"`, имена проверяются при
   валидации, до `Prepare`.
2. Адаптер регистрируется для всех встроенных плагинов; actions выводит
   агент: BACKUP и RESTORE всегда, VERIFY — только у `sdk.Verifier`.
3. `Plugin.Version()`; встроенные плагины отдают версию агента.
4. RESTORE в адаптере — да (общий `restic restore`).
5. Текст об OQ-027 — предложенный: «tls.key_file не соответствует
   tls.cert_file: регистрация не завершена — повторите `sard-agent enroll
   --force`»; прогнать через specifier в фазе 3.
6. Проверка 6: контрактный тест в агенте + job `e2e` в CI.
7. Теги `k=v`; секрет — байты как есть, читается при каждом запросе;
   `bytes_total = 0`, когда неизвестно.

## Фаза 2: SDK, stdin в обёртке, тестовый плагин (СТОП)

### Сделано
- `agent/plugins/sdk`: `Plugin{Name, Version, ConfigSchema, Prepare, Dump,
  Stream}` с `sdk.Host`, `Verifier`, `Dump{Paths, Excludes, Filename}`,
  `SecretFormat`, `ConfigError{Violations}`, `SecretError`; реестр
  отклоняет то, что отклонит Register (S4a). `Stream` получает и конфиг —
  без него потоковый дамп не построить (изменение против фазы 1).
- Встроенные плагины: `Version()` = версия агента, `Verify` убран (до
  этапа 2 они не умеют проверять), `plugins.Registry(version)`.
- `internal/restic`: `Command.Stdin`, `BackupRequest{Stdin, StdinFilename}`,
  `--stdin --stdin-filename=`; golden `testdata/backup-stdin.stdout`
  снят с restic 0.19.1.
- `internal/pluginhost`: `Secrets`, `CompileSchema`/`Schema.Validate`,
  `Source.Backup`/`Verify` с фазами; `testplugin` — оба способа, секрет,
  Verify побайтно.
- Гейт: интеграционная часть агента запускает и `./internal/pluginhost/...`,
  с `-race`.
- ADR 0027; `docs/dependencies.md`: jsonschema — runtime.

### Проверено (`go test -race`, restic 0.19.1)
- EOF после частичного потока → restic сохраняет обрезанный снимок;
  SIGTERM до EOF → exit 130 без снимка, но restic ждёт EOF и после
  SIGTERM (18 с в опыте с FIFO). Отсюда: stdin закрывается только после
  успешного потока или после строки restic о SIGTERM.
- Тесты стратегии: 1 — `schema_test.go` (пути `/host`, `/replicas/1/password`,
  `~0`/`~1`); 2 — `secrets_test.go`, `source_test.go`; 3 —
  `pluginhost/integration_test.go` (пути и поток → restore → Verify);
  4 — `TestIntegrationCancellingDuringTheDumpStopsTheDumpAndRestic`
  (дамп вернул ошибку, снимков 0, блокировок 0, restic не запущен) и
  restic-уровень `integration_stdin_test.go`.
- `./scripts/gate.sh sdk fast`, `./scripts/gate.sh agent fast` — PASSED
  (agent: покрытие 96.9%, CRAP ≤ 6, golangci-lint 0 issues).

### Найдено по дороге
- Проверка «restic не запущен» в интеграционных тестах видела процессы
  параллельно идущего пакета; теперь считаются только дочерние процессы
  теста.

### Открыто
- Правило 7 CLAUDE.md (runtime-зависимости агента) не упоминает валидатор
  JSON Schema — решение владельца.
