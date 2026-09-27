# Сессия 2026-09-27: обёртка над restic, техническая часть (A5a)

Ветка `claude/restic-wrapper-technical-sg7hwt` (назначена окружением; в
промпте — `feat/a5a-restic-wrapper`).

## Фаза 1: исследование, версия, скрипт, golden-вывод, дизайн

### Окружение
- github.com сначала отвечал 403 (политика сети), restic 0.19.1 для
  исследования собирался из исходников через `proxy.golang.org`. После
  открытия github.com golden-вывод переснят на официальном бинарнике
  (`restic 0.19.1 compiled with go1.26.4 on linux/amd64`).
- `restic.net`, `keys.openpgp.org`, `github.com/<user>.gpg`,
  `raw.githubusercontent.com` — 403. Открытый ключ релизов restic получить
  не удалось; подпись `SHA256SUMS.asc` сделана ключом
  `CF8F18F2844575973F79D4E191A6868BD3F7A907` (`gpg --list-packets`).

### Решения владельца (ответы на вопросы)
1. SHA-256 архивов закреплены в файле версии и сверяются и с ним, и с
   `SHA256SUMS` релиза. Подпись — если есть gpg и ключ.
2. `Repository.Backup` меняется несовместимо: пути/исключения/теги +
   колбэк прогресса → итоги. Режим stdin для дампов плагинов — A6.
3. Частично прочитанная копия (restic exit 3, снимок создан): итоги +
   `*PartialError` (`errors.Is(err, ErrUnreadableSource)`).
4. `TotalBytes` = `total_bytes_processed`, `AddedBytes` = `data_added_packed`,
   `AddedBytesRaw` = `data_added`.
5. Минимальная версия 0.19.0: с неё отмена даёт 130, а отсутствующий путь — 3.
6. `restic.cache_dir` необязательна, по умолчанию `/var/cache/sard/restic`.
7. Опасные переменные из `env_file` в restic не передаются (список ниже).
8. Интеграционные тесты входят в гейт.

### Решения и причины
- Версия restic **0.19.1** — последний стабильный релиз
  (`proxy.golang.org/github.com/restic/restic/@latest`: `"Version":"v0.19.1","Time":"2026-07-05T07:52:50Z"`).
- Единственный файл версии — `agent/internal/restic/restic-version`
  (`key=value`): версия, минимальная версия, SHA-256 архивов linux/amd64 и
  linux/arm64, отпечаток ключа подписи. Читают скрипт, пакет `restic`
  (`//go:embed`) и интеграционный тест.
- `scripts/fetch-restic.sh [amd64] [arm64]` → `.bin/restic/<ver>/linux_<arch>/restic`
  + `LICENSE.restic`. Каталог `.bin/` уже в `.gitignore`.
- Текст BSD-2 лицензии restic закоммичен в `third_party/restic/LICENSE`
  (из модуля `github.com/restic/restic@v0.19.1`, sha256 `6f08a01a…f897`):
  raw.githubusercontent.com закрыт, а при поставке текст нужен рядом с бинарником.
- Golden-вывод снят реальным restic от пользователя `nobody` (от root права
  000 не мешают чтению), в `agent/internal/restic/testdata/`.

### Формат вывода restic 0.19.1 (проверено запуском, файлы в testdata)
- `version`: `restic 0.19.1 compiled with go1.26.4 on linux/amd64`.
- `init --json`: `{"message_type":"initialized","id":"…","repository":"…"}`,
  `id` равен `id` из `cat config`.
- `cat config`: `{"version":2,"id":"…","chunker_polynomial":"…"}` — без ключей.
- `backup --json`: `status` и `summary` → stdout; `error` и `exit_error` →
  **stderr** (`internal/ui/backup/json.go:34,38`). Строка
  `signal terminated received, cleaning up` в stderr — не JSON.
- `status`: поля с нулём опускаются (`omitempty`, напр. `files_done` в начале).
  Без TTY в `--json` — ~10/с (`internal/ui/progress.go:15-26`); на копии
  быстрее первого тика `status` нет совсем.
- `summary`: **поля `total_bytes` нет** — есть `total_bytes_processed`,
  `data_added`, `data_added_packed`, `snapshot_id` (опускается, если снимок
  пропущен, `--skip-if-unchanged`). Расхождение с формулировкой задачи.
- Нечитаемые файлы: `error` с `during:"scan"`/`"archival"`, затем `summary`
  со снимком, `exit_error` код 3, процесс выходит с 3.
- Повторная копия тех же файлов: `data_added:348`, `data_added_packed:287`
  (метаданные дерева), не 0.
- Коды выхода (`cmd/restic/main.go:221-240`, проверены 3, 10, 12, 130):
  0, 1 (прочее), 3, 10 нет репозитория, 11 блокировка, 12 неверный пароль,
  130 отмена. Неверный пароль/нет репозитория — текстом, даже при `--json`.
- SIGTERM во время backup: штатное завершение, 130, `locks/` пуст.
- `restore --json`: `summary` c `total_files`, `files_restored`,
  `total_bytes`, `bytes_restored`.

### Дизайн (на ревью)
```go
// Executor запускает процесс; настоящий — ProcessExecutor (Setpgid,
// отмена: SIGTERM группе, через Grace — SIGKILL группе). Тесты — фейк.
type Executor interface {
    Run(ctx context.Context, cmd Command) (exitCode int, err error)
}
type Command struct {
    Path   string
    Args   []string
    Env    []string            // полное окружение, ничего не наследуется
    Stdout func(line []byte)   // построчно
    Stderr func(line []byte)
}

type Options struct {
    Binary   string            // restic.path; пусто → рядом с агентом (решает main)
    CacheDir string            // restic.cache_dir
    PATH     string            // значение PATH агента, единственное наследуемое
    Exec     Executor
    Keys     crypto.Provider
    ReadFile func(string) ([]byte, error) // env_file
    OnStderr func(line string)            // A7: логи
}
func New(o Options, r config.Repository) *CLI

type Repository interface {
    ID(ctx) (string, error)
    Init(ctx) (string, error)
    Backup(ctx, BackupRequest, func(Progress)) (BackupSummary, error)
    Restore(ctx, snapshotID, target string) error
}
func (*CLI) Version(ctx) (Version, error) // ErrUnsupportedVersion, ErrBadVersionOutput

type BackupRequest struct{ Paths, Excludes, Tags []string }
type Progress struct{ BytesDone, TotalBytes, FilesDone, TotalFiles uint64; PercentDone float64 }
type BackupSummary struct {
    SnapshotID, RepositoryID string
    TotalBytes, AddedBytes, AddedBytesRaw uint64
    FilesNew, FilesChanged, FilesUnmodified uint64
    Start, End time.Time
}
type PartialError struct{ Items []ItemError } // Is(ErrUnreadableSource)
// Коды 10/11/12 → ErrNoRepository, ErrLocked, ErrWrongPassword; иначе *ExitError{Code}.
```
- Окружение restic: `PATH=<PATH агента>`, `HOME=<cache_dir>`,
  `RESTIC_CACHE_DIR=<cache_dir>`, `Key.Env` от `crypto.Provider`
  (`RESTIC_PASSWORD_FILE`), переменные `env_file`. Репозиторий — флагом `-r`.
- `env_file`: `KEY=VALUE`, `#` — комментарий, пустые строки; без `export`,
  кавычек, подстановок. Читается при каждом запуске. Запрещены (ошибка с
  именем переменной, без значения): `RESTIC_*`, `PATH`, `HOME`,
  `XDG_*`, `TMPDIR`, `LD_*`, `GO*` (`GODEBUG`, `GOTRACEBACK`…), `DEBUG_LOG`,
  `TERM`, `SSH_AUTH_SOCK`. Ошибки не содержат значений и содержимого файла.
- `RepositoryID` в итогах берётся `cat config` перед `backup` — заодно пароль
  проверяется до долгой копии.
- Конфиг: секция `restic: {path, cache_dir}`; пустой `path` → `main`
  подставляет `filepath.Dir(os.Executable())/restic`.
- Тесты `ProcessExecutor` (включая отмену и отсутствие живых дочерних
  процессов) — через повторный запуск тестового бинарника, как
  `agent/cmd/sard-agent/main_test.go:108`.
- Гейт: `gate.sh agent` вызывает `fetch-restic.sh` и
  `go test -tags integration ./internal/restic/...`.

### Отвергнуто
- Доверять только `SHA256SUMS` релиза — защищает лишь от битой загрузки.
- `restic version --json` для проверки версии — текстовый формат одинаков во
  всех версиях.
- Пропуск интеграционного теста без restic — `t.Skip` запрещён; тест падает.
- Хранить ключ подписи restic в репозитории — получить его не удалось (403).

### Изменённые файлы
- `agent/internal/restic/restic-version`, `agent/internal/restic/testdata/*`
- `scripts/fetch-restic.sh`, `third_party/restic/LICENSE`
- `docs/sessions/2026-09-27-a5a-restic-wrapper.md`

### Проверено (команды запускались)
- `./scripts/fetch-restic.sh amd64 arm64` — exit 0, SHA-256 обоих архивов
  совпали с закреплёнными и с `SHA256SUMS`; arm64 — `ELF 64-bit … ARM aarch64`.
- Подмена закреплённой суммы → exit 1 с обеими суммами в сообщении.
- `REQUIRE_SIGNATURE=1` без ключа → exit 1.

### Открытые вопросы
- Открытый ключ релизов restic (`CF8F…A907`) — положить в репозиторий,
  когда будет доступен, и включить `REQUIRE_SIGNATURE=1` в CI.

## Фаза 2: Version, Init, ID, Backup, отмена

### Решения и причины
- `env_file`: опасные переменные не отбрасываются молча, а останавливают запуск
  restic ошибкой `ErrInvalidEnvFile` с именем переменной и номером строки,
  без значения (ответ владельца на п. 7 — «не отдавать»; отказ заметнее
  тихого пропуска). Список — как в дизайне фазы 1; префикс `GO*` сужен до
  `GODEBUG`, `GOTRACEBACK`, `GOMAXPROCS`, `GOGC`, `GOMEMLIMIT`, чтобы не
  запретить `GOOGLE_APPLICATION_CREDENTIALS` бэкенда gs.
- Репозиторий передаётся через `RESTIC_REPOSITORY`, а не флагом `-r`
  (отличие от дизайна фазы 1): URL с логином (`rest:https://user:pass@…`)
  не виден в `ps` другим пользователям.
- `Version()` не получает ни ключа, ни репозитория, ни `env_file` — только
  `PATH`, `HOME`, `RESTIC_CACHE_DIR`.
- Последний `status` restic 0.19.1 не бывает 100% (golden
  `backup-progress.stdout`: последний — `percent_done` 0.987); итоговые
  цифры берутся только из `summary`.
- Повторная копия тех же файлов на официальном бинарнике:
  `data_added_packed` 288 (в сборке из исходников было 287).
- `Backup` отклоняет пустые пути и теги с запятой (`ErrInvalidRequest`):
  restic делит `--tag a,b` на два тега — это было бы молча неверно.
  Пути идут после `--`, путь `-x` не станет флагом.
- `ProcessExecutor`: своя группа процессов (`Setpgid`); отмена — SIGTERM
  группе, через `Grace` (по умолчанию 10 с) — SIGKILL; после выхода restic
  SIGKILL всегда получает остаток группы, а удержанный кем-то stdout
  отпускается через `Grace` (`exec.Cmd.WaitDelay`).
- Окружение процесса задаётся всегда непустым срезом: `cmd.Env = nil`
  в Go означает «унаследовать всё».

### Отвергнуто
- Тихо выкидывать опасные переменные из `env_file` — оператор не узнает,
  почему бэкенд ведёт себя иначе.
- Добивать группу только при отмене — процесс, оставленный restic после
  обычного выхода (ssh/rclone), жил бы дальше.

### Изменённые файлы
- `agent/internal/restic/{restic,exec,env,backup,version}.go`
- `agent/internal/restic/{restic,exec,backup,version,export}_test.go`
- `agent/internal/restic/testdata/backup-verbose.stdout` (golden `-vv`)
- `agent/cmd/sard-agent/main.go` — новый конструктор `restic.New(Options, Repository)`

### Проверено (команды запускались)
- `./scripts/gate.sh agent` — PASSED: покрытие 99.2%, CRAP ≤ 6,
  mutation score 0.969697.
- Тест отмены сперва проходил и без добивания группы (дочерний процесс
  получал SIGTERM раньше, чем успевал его игнорировать). После ожидания
  готовности дочернего процесса тест падает, если убрать SIGKILL группы, и
  проходит с ним.
- После `go test ./internal/restic` живых процессов-помощников нет
  (`ps … | grep TestHelperProcess`).
- Выжившие мутанты в `internal/restic`: три — в ветке `cmd.ProcessState == nil`
  (Wait упал до завершения процесса; тестом не воспроизводится), один —
  `buf.WriteByte('\n')` в `collect` (эквивалентен: JSON и строка версии
  разбираются и без переводов строк).

### Открытые вопросы
- Колбэк прогресса вызывается ~10 раз в секунду; прореживание для отправки
  на сервер — забота A7.
