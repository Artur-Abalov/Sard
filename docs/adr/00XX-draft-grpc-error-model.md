# 00XX — Модель ошибок gRPC: `google.rpc.ErrorInfo` с доменом `sard.dev`

- Статус: черновик (S2b); номер присваивается при слиянии
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
- **Повтор.** Агент повторяет только `UNAVAILABLE`. Остальные коды окончательны для данного запроса. **Расхождение (S4a):** транспорт агента A3 на Register останавливается только на `FAILED_PRECONDITION`, `UNAUTHENTICATED` и `PERMISSION_DENIED`, а `INVALID_ARGUMENT` повторяет с задержкой до минуты (`agent/internal/transport/transport.go:273-284`). Невалидную конфигурацию повторять нельзя — исправляется в агенте; серверной защиты от частых повторов нет (решение владельца).
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
