# Сквозные тесты

Настоящие контейнеры, собранные из текущего кода: PostgreSQL, `sard-server`,
`sard-agent`. Тесты смотрят на систему снаружи — как агент или оператор — и
зависят только от контракта (`:proto-jvm`), не от кода сервера. Решение и
устройство — ADR 0020.

```bash
make e2e            # собрать артефакты один раз, собрать из них образы, прогнать :e2e:test (нужен Docker)
make e2e-images     # только артефакты и образы: sard-server:e2e, sard-agent:e2e, sard-agent-stand:e2e
make e2e-assemble   # только образы, из готовых DIST, STAND_DIST, SERVER_JAR_DIR (ничего не компилирует)
make e2e-test       # только тесты, на уже собранных образах
```

Собрать один раз, тестировать собранное (ADR 0045): тесты получают те же пакеты
агента и тот же jar, что уходят в выпуск. Сборки внутри тестов нет. В CI задача
`e2e` скачивает образ сервера из `server-image` и пакеты из `packages`.

- Образ сервера — `deploy/server/Dockerfile`, собранный из готового jar
  (`make server-jar`, `--build-context server-jar=…`) и релизных пакетов агента.
  Флаги сборки jar (кэш CI, прокси) — `SERVER_BUILD_FLAGS`.
- Образ агента `sard-agent:e2e` — `test/e2e/agent/Dockerfile` из tar.gz `make package`
  (для `E2E_ARCH`, по умолчанию архитектура хоста): тот самый агент, что
  поставляется, статический, и restic версии из
  `agent/internal/restic/restic-version`, проверенный по SHA-256 при упаковке. На
  нём идут все классы, кроме T3. Во время теста ничего не скачивается.
  База — хост, каким его ждёт пакет (ADR 0047): Ubuntu 24.04 с `openssh-client`
  (зависимость пакета, `sftp:` restic запускает `ssh`) и пользователь службы
  `sard-agent` (uid 65532) с домашним каталогом `/var/lib/sard-agent`.
- Хранилища стенда (ADR 0047): S3 — Garage `dxflrs/garage:v2.1.0` (`GarageS3`:
  бакеты и ключи через `garage` CLI), SFTP — образ `sard-sftp:e2e` из
  `test/e2e/sftp/Dockerfile` (`SftpServer`), его собирает `make e2e-agent-images`.
- Сборка образов стенда за прокси с подменой TLS: `E2E_BUILD_FLAGS="--network host
  --build-arg https_proxy=… --secret id=build-ca,src=<CA прокси>"`. Локально
  BuildKit может взять старый бинарник из контекста при той же длине и mtime
  (воспроизводимые tar.gz): после пересборки пакетов с другой `VERSION` —
  `touch` по `test/e2e/build/agent-image`.
- Образ стенда `sard-agent-stand:e2e` — тот же Dockerfile из tar.gz
  `make package-stand` (`GO_TAGS=e2e`): релизный агент плюс плагин `e2e-slow`
  (ADR 0036). Его запускают только `T3Agent` и `SlowStreamTest`. В выпуск он не
  попадает: `make package` его не линкует, и скрипт это проверяет.
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
| `StorageChainTest` | F2, цепочка `FullChainTest` (`Chain`) с репозиторием на удалённом хранилище: S3 (бакет Garage, ключ только в `env_file` агента, 0600) и SFTP (ключ `ssh-keygen` пользователя службы и `known_hosts` в `/var/lib/sard-agent/.ssh`). `repo init` — backend `s3`/`sftp` на сервере; бэкап через REST `succeeded`, снимок; `restic restore` — дерево побайтово равно эталону |
| `StorageFailureTest` | F2, отказы хранилища: S3 — неверный секрет (`Access Denied` в сообщении), ключ без записи (`Operation is not allowed for this key` в журнале), бакета нет (`repository does not exist`); SFTP — ключ не принят, ключ хоста неизвестен, сервер выключен (сообщение `unable to start the sftp session`), каталог только на чтение (`permission denied` в журнале), каталога нет. Каждый — `failed` за 60 с; после исправления следующий запуск того же источника `succeeded`. Причина в сообщении есть не всегда — OQ-155 |
| `StorageOutageTest` | F2, хранилище пропадает посреди бэкапа `e2e-slow` (20 с, образ стенда; прогресс > 2 МиБ — restic уже пишет): S3 остановлен на 30 с — шаг ждёт (`running` всё время) и `succeeded`; SFTP остановлен — `failed` с `ssh command exited`, после старта — `succeeded`; SFTP вне сети с `ServerAliveInterval` в `~/.ssh/config` — `failed`, после возврата — `succeeded`; без него — `running` 80 с после отключения, после возврата сети тот же шаг `succeeded` (OQ-154) |
| `StorageLockTest` | F2, блокировки restic в репозитории SFTP: бэкап, убитый вместе с агентом (`docker kill`), оставляет блокировку — следующий бэкап `succeeded`; убитый `restic check` оставляет исключительную — шаг сразу `failed`, `restic cat: repository is locked by another process` (OQ-156), после `restic unlock --remove-all` — `succeeded` |
| `ServerRecreateTest` | T3s, С1: контейнер сервера пересоздан (`recreateServer`): отпечаток CA прежний; агент, не перезапускавшийся и не регистрировавшийся заново, снова выполняет Register и открывает поток (`last_register_at`, `last_seen_at` после пересоздания), агент в базе один |
| `SlowStreamTest` | T3s, С3: шаг `e2e-slow` (24 МиБ, 1 МиБ/с, порции 64 КиБ) — `succeeded`, длительность `started_at`→`finished_at` 24 с ±10%, снимок есть, восстановленный файл побайтово равен данным seed |
| `StreamBreakTest` | T3, окно потери 100 с (heartbeat 5 с × 20), посреди шага `e2e-slow`: (1) обрыв сети агента (`docker network disconnect`) дольше порога офлайна, короче окна — `succeeded`; (2) `docker restart` сервера и (3) пересоздание сервера — `succeeded`, шаг не отправлен повторно; (4) перезапуск агента в `running` — `failed` с сообщением D13 задолго до окна, следующий запуск источника `succeeded`; (5) перезапуск агента в `dispatched`: команда в журнале (ACCEPTED не дошёл, iptables режет агент → сервер) — `failed` D13 без второго `restic backup`; не в журнале (агент вне сети) — сервер отправляет повторно, шаг выполнен один раз; (6) ResultAck не доходит (iptables режет сервер → агент): результат повторён один раз, записан один раз, надгробие. «Ровно один» — три счётчика `ExactlyOnce`: запуски `restic backup` в логе агента, снимки с тегом `sard.step`, строка `run … finished` в логе сервера и `runs.finished_at` |
| `StepLossTest` | T3, окно потери 10 с (heartbeat 5 с × 2): (7) агент остановлен и не вернулся — шаг `lost` через окно, запуск `failed`, новый запуск источника принят (`queued`); (8) агент перезапускается чаще окна, журнал исполнителя стёрт между перезапусками — `lost` по первому сроку, следующий запуск `succeeded`; (9) агента нет, сервер перезапущен — `lost` через окно от старта сервера (FXs, Д1, Д2) |
| `TreeDiffTest` | сравнитель деревьев `FullChainTest` без контейнеров: испорченный байт, пропавший, лишний, усечённый файл, файл вместо каталога — различия |
| `AgentConnectTest` | настоящий агент после `sard-agent enroll` выполняет Register и открывает Connect (`agents.last_register_at`, `last_seen_at`) |
| `RunStepSeamTest` | шаг в `queued` доходит до настоящего агента на Hello; агент отклоняет неизвестный плагин, сервер записывает `REJECTED`: шаг `rejected` с `unknown plugin "absent"`, запуск `failed` (S7a). Агент набора — релизная сборка: `e2e-slow` он тоже отклоняет как неизвестный (ADR 0045) |
| `ResultAckSeamTest` | S7a, тест 8: отклонённый шаг записан, `ResultAck` дошёл до агента (надгробие `acked/<sha256>.json` в каталоге исполнителя, `results/` пуст), после перезапуска контейнера агент результат не повторяет |
| `StepLogRedactionTest` | A7c, тест 4, A7b: шаг files с настоящим restic; путь несуществующего репозитория содержит значение секрета агента как есть и в стандартном base64; restic падает, в `step_logs` строка с `repo-[REDACTED]`, значения нет ни в `step_logs`, ни в сообщении шага |
| `EnrollmentTokenFormatTest` | помощник токена — тестовый вектор `docs/specs/enrollment-token.md` |
| `FailureLogsTest` | сломанная конфигурация сервера роняет старт, логи собраны, секреты замаскированы |

## Хост агента и регистрация

`AgentHost` — диск хоста с пакетом агента: два именованных тома Docker в
`/var/lib/sard-agent` (состояние, файлы TLS, репозиторий, данные) и
`/var/cache/sard/restic`. Каждая команда оператора — отдельный контейнер образа
на этих томах от имени пользователя службы с программой в точке входа:
`sard-agent enroll`, `sard-agent repo init`, `restic`, `ssh-keygen`, и сам агент.

Регистрация — только `sard-agent enroll` (`AgentEnroller`, токен файлом, не в
командной строке); файлы TLS вручную никто не кладёт. Токены, источники и
запуски — REST (вход администратором паролем стенда, `SardApi`). Строки SQL
ставят только швы S6a/S7a (`RunRows`): REST отклоняет источник с плагином,
которого у агента нет. Ожидание статусов — `Await` (опрос с таймаутом).

## Заготовки (`Pending.kt`)

Сейчас заготовок нет: последняя, `StreamBreakT3Pending`, заменена на
`StreamBreakTest` и `StepLossTest` (T3). Сценарий, который ждёт функции
сервера, добавляется классом без тестовых методов в `Pending.kt` с шагами в
KDoc: ничего не отключено, запускать пока нечего. Проверка восстановления
сервером (`lastVerifiedRestoreAt`) в контракт этапа 1 не входит и заготовки не имеет.
