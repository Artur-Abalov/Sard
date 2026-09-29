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
