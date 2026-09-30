# Сессия 2026-09-30: A5b — `sard-agent repo init`, `repo list`, проверка restic при старте

Спецификация: `docs/specs/agent/repo-init.feature` (решения владельца В1–В9
получены; решения specifier С1–С11 и Л1–Л10 утверждены владельцем — по
заданию). Строится на A5a (`agent/internal/restic`), A1 (`internal/secrets`,
`internal/config`), A2b (соглашения CLI, ADR 0025). `crypto_provider` при
старте агента (OQ-040) вне задачи.

## Что сделано

- `agent/internal/restic`: `CLI.Check` (найден, запускается, версия не ниже
  `min_version`; `*CheckError` с видом `NotFound`/`TooOld`/`Unusable`, версия
  в сообщении — как её напечатал restic, например `0.18.1-dev`); разбор
  выводов restic 0.19.1, которых A5a не знал: `ErrRepositoryExists`,
  `ErrEmptyPassword`, `ErrNetwork` (рядом с `*ExitError`); `ParseEnvFile`.
- `agent/internal/secrets`: `CheckFile` (одна проверка A1, теми же
  текстами), `Info.Size`.
- `agent/internal/repoinit` (новый пакет): причины и классы отказов, проверки
  до бэкенда (`Preflight`), `Inspect`/`Create` (cat config → init), разбор
  ответов restic, маскирование секретов (`Scrubber` поверх `internal/redact`),
  генерация пароля, блокировка `flock` рядом с файлом пароля.
- `agent/cmd/sard-agent`: `repo init`, `repo list` (`repo_*.go`), проверка
  restic при старте (`restic_check.go`, `start` в `main.go`); restic рядом с
  агентом ищется по настоящему файлу (`EvalSymlinks`).
- Гейт: `./cmd/sard-agent/...` добавлен к интеграционным пакетам
  (`scripts/gate.sh`).
- Документация: разделы A5b в ADR 0025 (таблицы кодов, П5) и ADR 0017 (П4);
  `docs/operations/repo-init.md`.

## Проверено на настоящем restic 0.19.1 (`scripts/fetch-restic.sh`)

Спецификация помечала это непроверенным; golden-вывод — в
`agent/internal/restic/testdata/` (`init-exists.stderr`,
`init-empty-password.stderr`, `cat-config-empty-password.stderr`,
`cat-config-unreachable.stderr`), интеграционные тесты —
`agent/cmd/sard-agent/repo_integration_test.go`.

1. `restic init` поверх существующего репозитория — код 1 и одна строка
   `exit_error` в stderr: `Fatal: Fatal: create repository at … failed: config
   file already exists`; данные не тронуты, пароль не важен. Своего кода
   выхода нет (П3 подтверждён), поэтому агент сначала выполняет `cat config`
   и распознаёт эту фразу только для гонки между проверкой и init.
2. Пустой пароль (файл нулевого размера или из одного перевода строки) —
   код 1, `an empty password is not allowed by default` и для `init`
   (каталога репозитория не создаётся), и для `cat config` существующего
   репозитория. В спецификации для этого нет причины (С4 говорит только «его
   отклоняет restic»): реализовано как `PASSWORD_FILE_EMPTY`, код 2.
3. **Сетевая недоступность.** restic не завершается сразу: он повторяет запрос
   с нарастающей паузой (0.6 с, 2 с, 2.4 с, 5 с, 20 с, 21 с … около 15 минут
   по умолчанию), печатая в stderr `… returned error, retrying after …:
   connection refused`, и только после этого выходит с кодом 1. Поэтому с
   `--timeout` по умолчанию (2 минуты) настоящий недоступный бэкенд даёт
   `TIMEOUT` (код 6, класс тот же), а не `BACKEND_UNAVAILABLE`.
   `BACKEND_UNAVAILABLE` выдаётся, когда restic сам завершился с сетевой
   причиной (проверено на фейке; настоящий restic — лишь при `--timeout`
   больше времени его повторов). Сообщение `TIMEOUT` называет тип бэкенда.
   Код и класс сценария «Недоступный REST-бэкенд» выполняются; расхождение —
   причина в сообщении. Пароль из адреса restic сам маскирует как `***`.

Противоречий, требующих остановки, нет: коды выхода и классы спецификации
выполняются; отличается только достижимость причины `BACKEND_UNAVAILABLE`.

## Решения, которых в спецификации нет

- `RESTIC_OUTPUT_UNEXPECTED` — причина для «непредусмотренный вывод restic»
  (в перечне С8 её нет, код 1 по спецификации).
- `PASSWORD_FILE_WRITE` также для ошибки создания файла блокировки (в
  каталоге файла пароля): код 7.
- Блокировка — `flock` на файле `.sard-init-<имя>.lock` рядом с файлом пароля
  (файл удаляется при выходе); каталог файла пароля должен существовать.
- `repo list` проверяет restic до строк и при пустом конфиге.
- Тексты отказов — на английском, формат `sard-agent repo <sub>: REASON: …`;
  для строк списка — `<имя>: REASON: …` без префикса программы.

## Не покрыто автоматически

- `@e2e` «После repo init и перезапуска агента сервер знает repository_id» —
  нужен настоящий sard-server и служба агента: только `docs/qa/repo-init.md`.

## Тесты и порядок работы

Тест сначала: для каждого правила спецификации писались тесты (имена — по
сценариям), проверялось, что они падают не из-за опечатки, затем код. Часть
тестов после реализации правила прошла с первого запуска (группы «отказ
бэкенда», «существующий репозиторий»: логика уже была написана как одно целое
вместе с `Create`/`FromRestic`); их состоятельность проверена ниже гейтом и
интеграционными тестами с настоящим restic.

## Правки по замечаниям architect (CHANGES REQUIRED)

- F1: `repo_password_read_test.go` — repo init (обычный, `--generate-password`
  с новым и существующим файлом) и repo list не читают файл пароля через
  `deps.readFile`; каждый вызов restic, кроме `version`, получает
  `RESTIC_PASSWORD_FILE`.
- F2: `crypto.ResticAESName` — единственное имя встроенного провайдера;
  `repoinit` использует его, тест Preflight принимает `Name()` провайдера.
- F3: `repoinit/restic_test.go` (FromCheck/notFound, все ветки) и таблица
  `CheckError.Error`/`Unwrap` в `restic/check_test.go`.
- F6: снятие префикса `Fatal: ` — метод `restic.ExitError.Cause()`;
  `Message` не менялся, вывод команд прежний.
- F7: `RESTIC_OUTPUT_UNEXPECTED` в строке кода 1 ADR 0025 и в справке
  `repo list` (вместе с `BACKEND_REFUSED`); спецификация не менялась
  (сценарий справки не фиксирует точный список).

## Поправка владельца: блокировка в restic.cache_dir (В8а, В8б, С12–С14, ADR 0028)

Сделано тестом вперёд.

- `repoinit.LockPath(cacheDir, name)` и `AcquireLock(cacheDir, repo)`: файл
  `<cache_dir>/.sard-init-<имя>.lock`; новая причина `LOCK_WRITE`, класс
  «запись» (код 7); сообщение называет `restic.cache_dir`, путь и ошибку ОС.
  Агент каталог не создаёт. `repoDeps.defaultCacheDir` (в бою
  `restic.DefaultCacheDir`) внедряется в тестах — `/var/cache` хоста не
  трогается. В тестовом хосте `restic.cache_dir` (K) существует, 0700.
- Тесты: `agent/cmd/sard-agent/repo_init_lock_test.go` (расположение файла,
  удаление при завершении, устаревший файл, существующий файл пароля в
  каталоге 0500 с флагом и без, LOCK_WRITE для «нет каталога / обычный файл /
  0500», каталог не создаётся, каталог по умолчанию, пароль не создаётся,
  list без блокировки и без каталога), `repoinit/lock_test.go`,
  `repoinit/preflight_test.go`.
- Ограничение: тесты запускаются от root, а root игнорирует права каталога,
  поэтому вариант «0500» для LOCK_WRITE проверяется только не от root; ветка
  отказа покрыта вариантами «нет каталога» и «обычный файл».
- Пакет (В8б): `deploy/agent/postinstall.sh` создаёт каталог (chown
  sard-agent:sard-agent, chmod 0700; каталог не файл пакета, поэтому
  переустановка сохраняет содержимое, а удаление его оставляет);
  `CacheDirectoryMode=0700` в unit; `scripts/package-agent.sh` проверяет:
  в tar.gz нет `var/cache`, deb/rpm не владеют каталогом, скрипт установки
  содержит mkdir/chown/chmod, в unit есть `CacheDirectoryMode=0700`. Локально
  проверены deb и tar.gz; rpm без утилиты rpm не проверялся.
- Не автоматизировано (только `docs/qa/repo-init.md`): все @package сценарии,
  требующие настоящей установки, — владелец/права после установки,
  repo init до первого старта службы, повторная установка, удаление.
- Документы: ADR 0025 (строка 7), `docs/operations/repo-init.md`, справка
  `repo init` (код 7).
