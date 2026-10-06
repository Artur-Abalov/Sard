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
  с `GO_TAGS=e2e` (для `E2E_ARCH`, по умолчанию архитектура хоста): агент стенда
  дополнительно содержит плагин `e2e-slow` (ADR 0036; в `make package` его нет,
  скрипт это проверяет), статический агент и restic
  версии из `agent/internal/restic/restic-version`, проверенный по SHA-256 при
  упаковке. Во время теста ничего не скачивается.
- Каждый класс получает свою установку (`SardEnvironment`): своя сеть, порты
  хоста случайные, CA создаётся сервером при старте. Тесты не зависят от
  порядка и от уже запущенных контейнеров.
- CA сервера (`/var/lib/sard/pki`) и данные PostgreSQL лежат в томах установки, как
  в `deploy/docker-compose.yml`. `sard.recreateServer()` заменяет контейнер сервера
  новым с теми же томами: CA и база прежние, порты хоста новые.
- Долгий бэкап задаётся конфигом, а не объёмом данных: источник плагина `e2e-slow`
  `{"size": байт, "rate": байт/с, "chunk": байт, "seed": n}` идёт через stdin restic
  `size/rate` секунд и сообщает прогресс после каждой порции. Данные — блоки
  SHA-256(seed ‖ i), big-endian, обрезанные до `size` (см. `SlowStreamTest.seeded`).
- При падении теста или старта логи всех контейнеров — в
  `test/e2e/build/e2e-logs/<класс>/<тест|start>/<контейнер>.log`; токены,
  PEM-ключи и зарегистрированные секреты замаскированы. В CI — артефакт `e2e-logs`.
- Модуль — тесты: в `scripts/gate.sh` не входит, метрики к нему не применяются.

## Что проверяется

| Класс | Что |
|---|---|
| `ServerSmokeTest` | `/api/v1/status` — 200 и версия сборки; порт gRPC — TLS, SAN `sard-server`, корень = `ca/ca.crt` сервера; Enroll доступен по TLS |
| `ConsoleImageTest` | сценарии `@e2e` `docs/specs/server/console-serving.feature`: `GET /` образа — HTML с `meta sard-version` = `VERSION` и `/api/v1/status` с той же версией; каждый `src`/`href` под `/assets/` страницы отдаётся с `immutable`; воркера моков `/mockServiceWorker.js` нет |
| `AgentImageSmokeTest` | `sard-agent --version` = `VERSION`; `restic version` = `restic-version` |
| `RegistrationTest` | перехватчик S3: без сертификата клиента `AgentService` — UNAUTHENTICATED |
| `AgentEnrollTest` | сценарии `@e2e` `docs/specs/agent/agent-enroll.feature` (имена тестов — названия сценариев): `sard-agent enroll` пишет ключ, сертификат и бандл, агент в тенанте токена с именем хоста, сервер принимает выданный сертификат; испорченный токен, существующая идентичность, чужой отпечаток не расходуют токен; `--force`; отказы `TOKEN_USED/UNKNOWN/EXPIRED/REVOKED` |
| `AgentEnrollRetryableTest` | сценарий `@e2e` «После INTERNAL_RETRYABLE тем же токеном можно зарегистрироваться»: PostgreSQL остановлен — код 6, запущен — код 0 (своя установка) |
| `FullChainTest` | T2b, полная цепочка: `sard-agent enroll`, агент до репозитория, `repo init` и перезапуск — сервер знает `repository_id` (сценарий `@e2e` `repo-init.feature`); источник и запуск через REST; шаг files `succeeded`, снимок; `restic restore` — дерево побайтово равно эталону, снятому до бэкапа (размеры от 0 до 3 МиБ, вложенность, не-ASCII имя; исключённые каталог и файл отсутствуют). Транспорт + исполнитель: прогресс дошёл, `ResultAck` дошёл (надгробие). Отрицательные пути: несуществующий путь — `failed` с путём, без снимка; нечитаемый файл — `failed` с путём, снимок `partial`, восстановление даёт читаемые файлы |
| `ServerRecreateTest` | T3s, С1: контейнер сервера пересоздан (`recreateServer`): отпечаток CA прежний; агент, не перезапускавшийся и не регистрировавшийся заново, снова выполняет Register и открывает поток (`last_register_at`, `last_seen_at` после пересоздания), агент в базе один |
| `SlowStreamTest` | T3s, С3: шаг `e2e-slow` (24 МиБ, 1 МиБ/с, порции 64 КиБ) — `succeeded`, длительность `started_at`→`finished_at` 24 с ±10%, снимок есть, восстановленный файл побайтово равен данным seed |
| `TreeDiffTest` | сравнитель деревьев `FullChainTest` без контейнеров: испорченный байт, пропавший, лишний, усечённый файл, файл вместо каталога — различия |
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

Классы без тестовых методов: ничего не отключено, запускать пока нечего. Шаги — в KDoc. Проверка восстановления сервером (`lastVerifiedRestoreAt`) в контракт этапа 1 не входит и заготовки не имеет.

| Сценарий | Условие включения |
|---|---|
| `StreamBreakT3Pending` — обрыв стрима T3; перезапуск агента посреди шага | задача T3 |
