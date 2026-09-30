# 0025 — Модель ошибок gRPC: `google.rpc.ErrorInfo` с доменом `sard.dev`

- Статус: принято (S2b; таблица `Register` — S4a, коды выхода `enroll` — A2b); номер присвоен в X2
- Дата: 2026-09-27

## Контекст
S2a отдавал на `Enroll` временные коды: все отказы по токену — `UNAUTHENTICATED`, плохой CSR — `INVALID_ARGUMENT`, прочее — `INTERNAL`. Агенту (A2b) нужна машиночитаемая причина, по которой он строит сообщение оператору и решает, повторять ли вызов. Это первый RPC с осмысленными отказами; решение станет образцом для остальных.

## Решение
- **Форма отказа.** Каждый отказ — `google.rpc.Status` с ровно одним `google.rpc.ErrorInfo`, домен `sard.dev`, в деталях статуса. Сообщения proto не меняются. Текст статуса — для логов: в нём нет ни секрета токена, ни внутренней причины сбоя.
- **Таблица `Enroll`** (порядок проверок — как в строках; спецификация `docs/specs/server/agent-enrollment.feature`):

  | `reason` | gRPC-код |
  |---|---|
  | `TOKEN_MALFORMED` | `INVALID_ARGUMENT` |
  | `TOKEN_FOREIGN_CA` | `UNAUTHENTICATED` |
  | `TOKEN_UNKNOWN` | `UNAUTHENTICATED` |
  | `TOKEN_USED` | `UNAUTHENTICATED` |
  | `TOKEN_REVOKED` | `UNAUTHENTICATED` |
  | `TOKEN_EXPIRED` | `UNAUTHENTICATED` |
  | `HOSTNAME_INVALID` | `INVALID_ARGUMENT` |
  | `CSR_INVALID` | `INVALID_ARGUMENT` |
  | `INTERNAL_RETRYABLE` | `UNAVAILABLE` |

  Любое непредвиденное исключение становится `INTERNAL_RETRYABLE`; `InvalidCsrException` — `CSR_INVALID`. `HOSTNAME_INVALID` — hostname пуст, длиннее 253 символов или с управляющим символом (S4a: NUL раньше падал в базе как `INTERNAL_RETRYABLE`).
- **Таблица `Register`** (S4a; порядок проверок — протокол, затем поля по порядку сообщения; первое нарушение отклоняет весь снимок, снимок в базе не меняется):

  | `reason` | gRPC-код | `ErrorInfo.metadata` |
  |---|---|---|
  | `PROTOCOL_UNSUPPORTED` | `FAILED_PRECONDITION` | `min_supported`, `max_supported` |
  | `HOSTNAME_INVALID` | `INVALID_ARGUMENT` | `field` |
  | `FIELD_INVALID` | `INVALID_ARGUMENT` | `field` (например `plugins[0].actions`, `repositories[1].backend`) |
  | `NAME_INVALID` | `INVALID_ARGUMENT` | `field` |
  | `NAME_DUPLICATE` | `INVALID_ARGUMENT` | `field` |
  | `SNAPSHOT_TOO_LARGE` | `INVALID_ARGUMENT` | `field`, `limit` |
  | `CONFIG_SCHEMA_INVALID` | `INVALID_ARGUMENT` | `field` |
  | `INTERNAL_RETRYABLE` | `UNAVAILABLE` | — |

  Пределы — `registration/SnapshotRules`; поддерживаемые версии протокола — `registration/ProtocolVersions` (одно место). Одна точка перевода — `agents/RegistrationStatus.kt`, текст статуса «register rejected».
- **Metadata.** `ErrorInfo.metadata` называет поле и предел, но никогда не значение из запроса: запрос пишет хост, который может быть скомпрометирован, а статус попадает в логи. Ключи — `lower_snake_case`; поле — путь в сообщении proto с индексами (`plugins[2].name`).
- **Повтор.** `UNAVAILABLE` — единственный код, который стоит повторять; остальные коды окончательны для данного запроса. Транспорт агента (A3) повторяет его сам; `sard-agent enroll` (A2b) не повторяет регистрацию ни при каком коде, включая `UNAVAILABLE`, — спецификация `docs/specs/agent/agent-enroll.feature` требует ровно одного вызова `Enroll` за команду (правило «Отказы сервера объясняются по причине, повторов нет»; проверено `TestTheCommandDoesNotRetryAfterATemporaryFailure`): повторять или нет, решает оператор по сообщению команды. **Расхождение (S4a):** транспорт агента A3 на Register останавливается только на `FAILED_PRECONDITION`, `UNAUTHENTICATED` и `PERMISSION_DENIED`, а `INVALID_ARGUMENT` повторяет с задержкой до минуты (`agent/internal/transport/transport.go:273-284`). Невалидную конфигурацию повторять нельзя — исправляется в агенте; серверной защиты от частых повторов нет (решение владельца).
- **Одна точка перевода** на RPC — `agents/EnrollmentStatus.kt` (Enroll), `agents/RegistrationStatus.kt` (Register). Доменные пакеты (`enrollment`, `persistence`, `pki`, `extension`) не знают ни `io.grpc`, ни proto, ни `com.google.rpc`/`protobuf`, ни пакета `agents`; это проверяет `ArchitectureTest` сканированием исходников, включая полные имена без `import`. ArchUnit и Konsist не взяты — без новой зависимости.
- **Строка причины на проводе — `Reason.name`.** Переименование константы `EnrollmentRejectedException.Reason` — ломающее изменение контракта с агентом. Строки закреплены литералами в `EnrollmentStatusTest` (по одной на причину плюс замкнутое множество) и в интеграционных тестах контракта; переименование роняет их. Полноту gRPC-кода по причинам проверяет компилятор: исчерпывающий `when` без `else`.
- **Отмена.** Отмену корутины переводит в `() -> Boolean` только `EnrollmentGrpcService`; `Enrollment` о корутинах не знает. Точка фиксации — проверка отмены после подписи CSR и до записи сертификата: отмена раньше неё откатывает регистрацию, токен остаётся активным. Отмена после неё регистрацию не отменяет — окно между последней проверкой и `COMMIT` закрыть нельзя; это принято решением 7 спецификации (обрыв после фиксации расходует токен, агент остаётся в списке).
- **Зависимость.** `io.grpc:grpc-protobuf` (Apache-2.0) объявлена явно ради `StatusProto` и `ErrorInfo`; раньше приходила транзитивно через стартер. Запись — `docs/dependencies.md`.

## Отвергнуто
- **Таблица `mapOf(reason → wire)`.** Новая причина компилируется и падает только во время работы (`NoSuchElementException` → агент видит `UNKNOWN` без `ErrorInfo`).
- **`when` на 9 ветвей с литералами строк.** Цикломатическая сложность выше порогов шлюза (detekt 8, CRAP 6), а пороги не ослабляются.
- **Отдельный `enum WireReason` в `agents`.** Отображение `Reason → WireReason` — тот же `when` на 9 ветвей; `valueOf(reason.name)` возвращает связь по имени и проверку полноты во время работы.
- **Код `INTERNAL` для внутренних сбоев.** Для агента он неотличим от ошибки контракта; `UNAVAILABLE` явно говорит «повтори».

## Отложено
- **Литерал в конструкторе доменного перечисления** (`enum class Reason(val code: String)`): полнота и литеральные строки без роста сложности, ценой публикуемого идентификатора в домене. Решение владельца, если понадобится независимость имён от провода.

## A2b — коды выхода `sard-agent enroll`

Владелец решил (В3, `docs/specs/agent/agent-enroll.feature`), что номера кодов выхода команды `sard-agent enroll` фиксируются в этом ADR, рядом с моделью ошибок, которую они классифицируют: каждый класс кода выхода — прямое отображение `enroll.Class` (`agent/internal/enroll/classify.go`), а тот, в свою очередь, — причины `reason` из этого ADR.

| код | класс `enroll.Class` | когда |
|---|---|---|
| 0 | — (успех) | идентичность записана; также `--help` |
| 1 | `agent-error` | `CSR_INVALID`, `HOSTNAME_INVALID`, непредусмотренный gRPC-код или ответ |
| 2 | `usage` | флаги, источник токена, конфиг, формат токена (`TOKEN_MALFORMED`), конфликт адреса `--server`/`server.address` |
| 3 | `token-refused` | `TOKEN_UNKNOWN`, `TOKEN_USED`, `TOKEN_EXPIRED`, `TOKEN_REVOKED` |
| 4 | — (местная проверка A2b, не `enroll.Class`) | `tls.cert_file` или `tls.key_file` уже существует, `--force` не задан |
| 5 | `trust` | отпечаток CA сервера, имя хоста, прочие ошибки TLS-рукопожатия, `TOKEN_FOREIGN_CA` |
| 6 | `temporary` | сервер недоступен, таймаут, `INTERNAL_RETRYABLE`, прерывание (SIGINT/отмена контекста), второй параллельный `enroll` |
| 7 | `write` | каталог `tls.*` непригоден для записи (В12), сбой записи файлов после успешного `Enroll` |

Единственная точка перевода на стороне агента — `enrollExitCode` (`agent/cmd/sard-agent/enroll_report.go`), которая ищет `Class` построчно в таблице `enrollClassCodes`. Здесь это ровно противоположный выбор тому, что сделал сервер (раздел «Отвергнуто» выше отвергает `mapOf(reason → wire)` именно потому, что новая причина без записи в такой карте компилируется и падает только во время работы): в Go нет исчерпывающего `switch` по именованному строковому типу без `default`, поэтому компилятор в принципе не может здесь проверить полноту так, как это делает kotlinc для `when`. Вместо этого таблица `enrollClassCodes` и её единственный вызывающий код лежат рядом в одном файле, а полноту (каждый `enroll.Class` — ровно одна строка, с верным кодом) проверяет тест `TestEveryEnrollClassHasAnExitCodeAndTheHelpPrintsIt` (`agent/cmd/sard-agent/enroll_help_test.go`) — тот же результат, что и на сервере, но приходится держать его тестом, а не компилятором. Код 4 не приходит из `enroll.Class` вовсе — существующая идентичность проверяется самой командой (`enroll.InspectIdentity`) до любого обращения к серверу, поэтому у него нет соответствующей причины `google.rpc.ErrorInfo`; `--help` печатает его из той же таблицы `enrollHelpCodes`, дополненной кодами 0 и 4.

## A5b — коды выхода `sard-agent repo init` и `sard-agent repo list`

Номера кодов — те же, что у `enroll` (таблица выше, решение В3 A2b): у `repo init` и `repo list` нет своих номеров, только своё содержание классов (`docs/specs/agent/repo-init.feature`, решения владельца В3–В8 и решения Л8). Классы и их номера задаёт таблица `repoClassCodes` в `agent/cmd/sard-agent/repo_report.go`, причины отказов — `agent/internal/repoinit/failure.go`; справка каждой команды печатает свою таблицу из тех же констант номеров, что и `enroll`. Коды 3 и 5 (отказ по токену, доверие) ни одна из двух команд не возвращает.

### `repo init`

| код | класс | когда (причина в сообщении) |
|---|---|---|
| 0 | успех | репозиторий создан; также `--help` |
| 1 | ошибка агента | restic не найден, старый или непригоден (`RESTIC_NOT_FOUND`, `RESTIC_TOO_OLD`, `RESTIC_UNUSABLE`); несетевой отказ бэкенда — учётные данные, права, TLS бэкенда (`BACKEND_REFUSED`); непредусмотренный вывод restic (`RESTIC_OUTPUT_UNEXPECTED`) |
| 2 | использование | флаги; конфиг; неизвестное имя (`REPOSITORY_UNKNOWN`); `CRYPTO_PROVIDER_UNSUPPORTED`; `PASSWORD_FILE_MISSING`, `PASSWORD_FILE_EMPTY`; нарушение прав или владельца секретного файла (текст A1, без строки причины); `ENV_FILE_MISSING`, `ENV_FILE_INVALID`; пароль не открывает существующий репозиторий (`WRONG_PASSWORD`) |
| 4 | идентичность есть | репозиторий уже инициализирован, данные не тронуты, id в сообщении (`REPOSITORY_EXISTS`) |
| 6 | временная | бэкенд недоступен по сети (`BACKEND_UNAVAILABLE`); истёк `--timeout` (`TIMEOUT`); прерывание (`INTERRUPTED`); идёт другой init того же репозитория на хосте (`INIT_IN_PROGRESS`) |
| 7 | запись | файл пароля нельзя создать (`PASSWORD_FILE_WRITE`); файл блокировки в `restic.cache_dir` нельзя создать (`LOCK_WRITE`, ADR 0028) |

Проверки идут в порядке: флаги → конфиг → имя → `crypto_provider` → `password_file` → `env_file` → restic → блокировка → генерация пароля → `restic cat config` → `restic init`; первая сработавшая определяет код. Старт агента сохраняет код 1 для любой ошибки, в том числе для проблем с restic.

### `repo list`

| код | когда |
|---|---|
| 0 | состояние каждого репозитория известно (`initialized` или `not-initialized`; неинициализированный репозиторий — не ошибка); также `--help`; конфиг без репозиториев |
| 1 | restic не найден, старый или непригоден (список не печатается); либо первая по порядку конфига постоянная проблема строки — класса «ошибка агента» (`BACKEND_REFUSED`, `RESTIC_OUTPUT_UNEXPECTED`) |
| 2 | флаги или конфиг (список не печатается); либо первая постоянная проблема строки — класса «использование» (`CRYPTO_PROVIDER_UNSUPPORTED`, `PASSWORD_FILE_*`, `ENV_FILE_*`, `SECRET_FILE_REJECTED`, `WRONG_PASSWORD`) |
| 6 | все проблемы строк временные (`BACKEND_UNAVAILABLE`, `TIMEOUT`); прерывание (`INTERRUPTED`, таблица не печатается) |

Код один на запуск (Л8): код класса первой в порядке конфига проблемной строки, класс которой не «временная»; если все проблемы временные — 6. Обоснование: в A2b код 6 обещает, что повтор поможет, а это верно, только когда все проблемы строк временные; постоянная проблема (1 или 2) требует действия оператора и потому важнее. Коды 3, 4, 5, 7 `repo list` не возвращает. `SECRET_FILE_REJECTED` — причина, существующая только в колонке STATUS: у текстов A1 своей причины нет.
