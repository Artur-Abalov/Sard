# Сквозные тесты

Настоящие контейнеры, собранные из текущего кода: PostgreSQL, `sard-server`,
`sard-agent`. Тесты смотрят на систему снаружи — как агент или оператор — и
зависят только от контракта (`:proto-jvm`), не от кода сервера. Решение и
устройство — ADR 0020.

```bash
make e2e          # собрать оба образа, прогнать :e2e:test (нужен Docker)
make e2e-images   # только образы: sard-server:e2e, sard-agent:e2e
```

- Образ сервера — `deploy/server/Dockerfile`; флаги сборки (кэш CI, прокси) —
  `E2E_SERVER_BUILD_FLAGS`.
- Образ агента — `test/e2e/agent/Dockerfile` из tar.gz `scripts/package-agent.sh`
  (для `E2E_ARCH`, по умолчанию архитектура хоста): статический агент и restic
  версии из `agent/internal/restic/restic-version`, проверенный по SHA-256 при
  упаковке. Во время теста ничего не скачивается.
- Каждый класс получает свою установку (`SardEnvironment`): своя сеть, порты
  хоста случайные, CA создаётся сервером при старте. Тесты не зависят от
  порядка и от уже запущенных контейнеров.
- При падении теста или старта логи всех контейнеров — в
  `test/e2e/build/e2e-logs/<класс>/<тест|start>/<контейнер>.log`; токены,
  PEM-ключи и зарегистрированные секреты замаскированы. В CI — артефакт `e2e-logs`.
- Модуль — тесты: в `scripts/gate.sh` не входит, метрики к нему не применяются.

## Что проверяется

| Класс | Что |
|---|---|
| `ServerSmokeTest` | `/api/v1/status` — 200 и версия сборки; порт gRPC — TLS, SAN `sard-server`, корень = `ca/ca.crt` сервера; Enroll доступен по TLS |
| `AgentImageSmokeTest` | `sard-agent --version` = `VERSION`; `restic version` = `restic-version` |
| `RegistrationTest` | перехватчик S3: без сертификата клиента `AgentService` — UNAUTHENTICATED |
| `AgentEnrollTest` | сценарии `@e2e` `docs/specs/agent/agent-enroll.feature` (имена тестов — названия сценариев): `sard-agent enroll` пишет ключ, сертификат и бандл, агент в тенанте токена с именем хоста, сервер принимает выданный сертификат; испорченный токен, существующая идентичность, чужой отпечаток не расходуют токен; `--force`; отказы `TOKEN_USED/UNKNOWN/EXPIRED/REVOKED` |
| `AgentEnrollRetryableTest` | сценарий `@e2e` «После INTERNAL_RETRYABLE тем же токеном можно зарегистрироваться»: PostgreSQL остановлен — код 6, запущен — код 0 (своя установка) |
| `FullChainTest` | T2b: `sard-agent enroll` → `repo init` → источник и запуск через REST → шаг files с restic `succeeded`, прогресс дошёл (`started_at`), снимок с `repository_id` из Register, `ResultAck` дошёл (надгробие) |
| `AgentConnectTest` | настоящий агент после `sard-agent enroll` выполняет Register и открывает Connect (`agents.last_register_at`, `last_seen_at`) |
| `RunStepSeamTest` | шаг в `queued` доходит до настоящего агента на Hello; агент отклоняет неизвестный плагин, сервер записывает `REJECTED`: шаг `rejected` с `unknown plugin "absent"`, запуск `failed` (S7a) |
| `ResultAckSeamTest` | S7a, тест 8: отклонённый шаг записан, `ResultAck` дошёл до агента (надгробие `acked/<sha256>.json` в каталоге исполнителя, `results/` пуст), после перезапуска контейнера агент результат не повторяет |
| `StepLogRedactionTest` | A7c, тест 4, A7b: шаг files с настоящим restic; путь несуществующего репозитория содержит значение секрета агента как есть и в стандартном base64; restic падает, в `step_logs` строка с `repo-[REDACTED]`, значения нет ни в `step_logs`, ни в сообщении шага |
| `EnrollmentTokenFormatTest` | помощник токена — тестовый вектор `docs/specs/enrollment-token.md` |
| `FailureLogsTest` | сломанная конфигурация сервера роняет старт, логи собраны, секреты замаскированы |

## Хост агента и регистрация

`AgentHost` — диск хоста с пакетом агента: два именованных тома Docker в
`/var/lib/sard-agent` (состояние, файлы TLS, репозиторий, данные) и
`/var/cache/sard/restic`. В образе нет оболочки, поэтому каждая команда
оператора — отдельный контейнер образа на этих томах с программой в точке
входа: `sard-agent enroll`, `sard-agent repo init`, `restic`, и сам агент.

Регистрация — только `sard-agent enroll` (`AgentEnroller`, токен файлом, не в
командной строке); файлы TLS вручную никто не кладёт. Токены, источники и
запуски — REST (вход администратором паролем стенда, `SardApi`). Строки SQL
ставят только швы S6a/S7a (`RunRows`): REST отклоняет источник с плагином,
которого у агента нет. Ожидание статусов — `Await` (опрос с таймаутом).

## Заготовки (`Pending.kt`)

Классы без тестовых методов: ничего не отключено, запускать пока нечего. Шаги — в KDoc.

| Сценарий | Условие включения |
|---|---|
| `FullChainT2Pending` — полная цепочка T2: бэкап, снапшот, проверенное восстановление, `lastVerifiedRestoreAt` | S7 и A2 в `main` |
| `StreamBreakT3Pending` — обрыв стрима T3; перезапуск агента посреди шага | задача T3 |
