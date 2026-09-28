# Сессия 2026-09-28: S5a — менеджер стримов на сервере

Ветка `claude/sweet-babbage-zdc1zj` (назначена средой; в задании — `feat/s5a-stream-manager`) от `main` @ `54ecc4d`. S3 в `main` (PR #13).

## Фаза 1 — исследование, модель сессии, точки расширения

Два субагента только на чтение (сервер: gRPC/S3/тенантность/тесты; агент A3: keepalive, heartbeat, коды закрытия, очередь). Ключевые места перечитаны напрямую: `agent/internal/transport/transport.go:60-96,160-212,250-345`, `server/.../persistence/TenantSessions.kt:15-50`, `agents/AgentSessions.kt:15-22`, `application.yaml:18-48`, `persistence/Agent.kt:14-40`, `agents/AgentGrpcService.kt`, `ArchitectureTest.kt:62-68`, `metrics/SardMetrics.kt`, `proto/sard/agent/v1/agent.proto`.

### Находки: агент (A3), проверено по коду

- Keepalive клиента: `Time 30 s`, `Timeout 10 s`, `PermitWithoutStream` не задан (= `false`) — `transport.go:78-81,269-271`. Новый `ClientConn` на каждую попытку.
- Попытка = dial → **`Register`** → `Connect` → `Hello` первым `Send`, без `Recv` до него (`transport.go:252-305`). То есть **без Register (S4a) агент до Connect не доходит**: сейчас Register → `UNIMPLEMENTED` → повтор с задержкой.
- Hello несёт только `running_command_ids` (`agent.proto:165-170`). После Hello — неподтверждённые результаты, затем прочее (`transport.go:307`, `outbox.go`).
- Heartbeat: интервал из `RegisterResponse.heartbeat_interval`, по умолчанию 30 s (`transport.go:64,286-291`); первый — через полный интервал после Hello; таймер не сбрасывается другими сообщениями (журнал A3:254-256).
- Переподключение: полный джиттер `[0, min(1 s·2ⁿ, 60 s))`; рост сбрасывается, только если попытка (от dial) прожила ≥ 30 s (`transport.go:164-181,204-210`).
- Коды: окончательный отказ — **только на Register**: `FAILED_PRECONDITION` → `ErrIncompatibleProtocol`, `UNAUTHENTICATED`/`PERMISSION_DENIED` → `ErrNotAuthorized` (`transport.go:273-284`). **Любой код закрытия стрима, включая `OK`, `UNAUTHENTICATED`, `ALREADY_EXISTS`, — повтор с задержкой** (`streamError`, `transport.go:318-323`). `ErrorInfo` агент не читает.
- Неизвестное сообщение сервера — молча пропускается (`switch` без `default`, `transport.go:332-341`).
- `test/e2e` — только `README.md` («будущее»); каркаса нет.

Следствия (вывод, не код):
- Отзыв во время стрима: сервер закрывает стрим `UNAUTHENTICATED` → агент переподключается → перехватчик S3 отклоняет **Register** `UNAUTHENTICATED` → агент останавливается. С A3 согласуется, изменений в агенте не требует.
- Дубликат: клон получает `ALREADY_EXISTS` и повторяет попытки бесконечно, не чаще раза в ~60 s после разгона, каждый раз вызывая Register (последствия для записи агента — S4a).
- Ложный дубликат при «полумёртвом» соединении: агент замечает обрыв за ≤ 40 s (keepalive 30+10), переподключается через 0–1 s. К этому моменту прежняя сессия получала сообщения 10–40+ s назад — **свежее порога 2×30 = 60 s**, то есть легитимный агент получит `ALREADY_EXISTS` и будет «помечен» дубликатом. См. вопрос 1.

### Находки: сервер, проверено по коду

- gRPC — стартер Spring Boot 4.1.1 (`build.gradle.kts:47`), настроек keepalive нет нигде (grep `keepAlive|permitKeepAlive|ServerBuilderCustomizer` пуст). grpc-netty не shaded (журнал S3:12) → доступен `ServerBuilderCustomizer<NettyServerBuilder>`. Имена свойств `spring.grpc.server.keep-alive.*` не проверены (jar'ов Spring gRPC в локальном кэше нет) — проверю в фазе 2 по метаданным; при сомнении — кастомайзер.
- `AgentGrpcService` пуст (`AgentGrpcService.kt:13-14`) — S4a параллельно добавит сюда Register.
- `AgentPrincipal(agentId, tenantId, serial)` в `Context` (`AgentAuthentication.kt:26-35`); `notAfter` не сохраняется. `AgentAuthenticator` — порядок проверок и причины (`CERT_*`, `AGENT_REVOKED`), `CertificateStandings` — функциональный интерфейс по serial.
- `TenantSessions.system` — **только чтение** (`TenantSessions.kt:34-41`); список вызывающих закреплён в `ArchitectureTest.kt:62-68` и ADR 0013.
- `Agent` неизменяем (`val` везде), `last_seen_at` есть, столбцов статуса/дубликата нет; REST-перечисление статуса — `online | offline` (`web/src/api/openapi.json:1795`).
- `Clock` — бин с `@ConditionalOnMissingBean` (`ClockAutoConfiguration.kt:12-17`); `MovableClock` в тестах (`pki/PkiFixtures.kt:28`). Планировщика (`TaskScheduler`) нет. Диспетчеры — квалифицированные бины (`EnrollmentDispatcherConfiguration.kt:20-21`).
- Actuator есть (Micrometer core), `MeterRegistry` нигде не используется, Prometheus-реестра нет; `SardMetrics` — заглушка.
- Интеграционные тесты gRPC — настоящий Netty с TLS на порту 0, Testcontainers PostgreSQL, агенты выпускаются через настоящий `Enrollment` (`AgentAuthIntegrationTest.kt`).

### Модель сессии (предложение)

Состояния стрима `Connect`:

```
Opened ──Hello──▶ Active ──▶ Closed(reason)
  │  (первое сообщение не Hello / нет Hello за hello-timeout)
  └──────────────────────────▶ Closed(HELLO_REQUIRED)
```

- Слот реестра `agent_id → AgentSession` занимается **на Hello**, не при открытии: до Hello стрим — не сессия.
- `AgentSession`: `principal` (agent, tenant, serial), `openedAt`, `lastMessageAt` (любое входящее), `lastSeenWrittenAt`, исходящая очередь, `close(status)`.
- Правило второго стрима (на Hello нового, под блокировкой слота): `now - old.lastMessageAt < duplicateWindow` (по умолчанию 2 × heartbeat) → новый закрыт `ALREADY_EXISTS`/`AGENT_DUPLICATE_SESSION`; иначе прежний закрыт `UNAVAILABLE`/`SESSION_REPLACED`, новый принят.
- Онлайн: сессия в реестре и `now - lastMessageAt < N × heartbeat` (N = 3). Проверка «просрочки» — периодическая (тот же тик, что и сверка с БД), закрытие `UNAVAILABLE`/`SESSION_EXPIRED`.
- `last_seen_at`: HQL `update` через `AgentSessions.inTenant` на Hello и затем не чаще раза в интервал на любое входящее сообщение.
- Расхождение часов: `sent_at` Heartbeat − `clock.instant()` → распределение `sard.agent.clock.skew`; `|skew| > skew-threshold` → WARN (один раз на сессию).

Коды закрытия (`ErrorInfo`, домен `sard.dev`, образец — ADR 00XX-draft):

| `reason` | Код | Когда |
|---|---|---|
| `HELLO_REQUIRED` | `FAILED_PRECONDITION` | первое сообщение не Hello или нет Hello за hello-timeout |
| `AGENT_DUPLICATE_SESSION` | `ALREADY_EXISTS` | живая прежняя сессия |
| `SESSION_REPLACED` | `UNAVAILABLE` | прежняя, устаревшая, закрыта новой |
| `SESSION_EXPIRED` | `UNAVAILABLE` | нет сообщений N интервалов |
| `CERT_REVOKED`, `CERT_EXPIRED`, `AGENT_REVOKED` | `UNAUTHENTICATED` | периодическая сверка с БД (переиспользует причины S3) |
| `SERVER_SHUTTING_DOWN` | `UNAVAILABLE` | остановка сервера |

### Точки расширения (сигнатуры, предложение)

```kotlin
/** S6 заменит: сверка по Hello и доставка ожидающих команд. Вызов — после занятия слота. */
fun interface CommandReconciliation {
    fun onHello(agent: ConnectedAgent, runningCommandIds: List<String>)
}

/** Входящие; реализации по умолчанию — debug-лог без содержимого. S6/S7 заменят бинами. */
fun interface StepProgressHandler { fun handle(agent: ConnectedAgent, progress: StepProgress) }
fun interface StepResultHandler   { fun handle(agent: ConnectedAgent, result: StepResult) }
fun interface LogChunkHandler     { fun handle(agent: ConnectedAgent, chunk: LogChunk) }

/** Отправка агенту; не блокирует и не приостанавливает отправителя. */
interface AgentStreams {
    fun send(agentId: UUID, message: ConnectResponse): SendResult
    fun isOnline(agentId: UUID): Boolean
}
sealed interface SendResult {
    data object Queued : SendResult          // в очереди сессии; не значит «доставлено»
    data object NotConnected : SendResult
    data object QueueFull : SendResult
}

/** Подключение и отключение — для других компонентов (S6, позже S5b). */
interface AgentSessionListener {
    fun connected(agent: ConnectedAgent)
    fun disconnected(agent: ConnectedAgent, reason: String)
}

data class ConnectedAgent(val agentId: UUID, val tenantId: UUID, val serial: String)
```

- Один писатель на сессию: корутина, читающая `Channel(capacity)` и отдающая в `Flow` ответа grpc-kotlin — обратное давление даёт сам `Flow` (готовность стрима); `send` — `trySend`, при полной очереди `QueueFull`.
- `Queued` не гарантирует доставку: при закрытии сессии очередь теряется, S6 восстанавливает состояние по следующему Hello.
- Обработчики вызываются в контексте вызова (тенант через `AgentSessions`), на внедрённом диспетчере.

### Решения владельца по вопросам фазы 1

1. Ложный дубликат: (а) сервер сам шлёт keepalive (30 s / 10 s) и закрывает полумёртвое соединение; (б) новый стрим при свежей прежней сессии отклоняется, но **факт дубликата фиксируется, только если прежняя сессия получила сообщение после отказа**.
2. Пометка дубликата — событие `AgentSessionListener`, счётчик, WARN; столбец в БД и REST — S5b.
3. Шов с A3 — пока тестовая заглушка Register (только в тестах); после слияния S4a в `main` — синхронизация.
4. Интервал heartbeat — `sard.agent.heartbeat-interval` (им пользуется S4a); остальные настройки — `sard.agent.stream.*`.
5. Сверка с БД — пакетный запрос через `system` раз в 30 s, проверки S3 в том же порядке; `close(agentId, reason)` для будущего API отзыва — да.
6. Коды закрытия — по таблице выше.
7. Слот реестра — на Hello; стрим без Hello закрывается по `hello-timeout`.
8. `last_seen_at` — на Hello и далее на любое входящее, не чаще интервала; метрики — `MeterRegistry` actuator, без Prometheus.
9. `send` — `trySend` и `SendResult`, очередь 64.

## Фаза 2 — keepalive, реестр, Hello, heartbeat, онлайн, дубликат (тесты 1–4)

Окружение сессии (в репозиторий не попадает): JDK 25 из apt (`openjdk-25-jdk-headless`), запущен `dockerd`, `LC_ALL=C.UTF-8`. Maven Central отвечал 429; зеркало через init-скрипт Gradle запрещено классификатором среды — зависимости скачались повторным запуском сборки.

Базовый прогон до изменений: `./gradlew :server:test` — exit 0, 281 тест, 0 упавших, 0 пропущенных.

Тесты писались первыми (красный — не компилировались без кода): `StreamStatusTest`, `AgentStreamTest`, `AgentSessionRegistryTest`, `AgentStreamsTest`, затем интеграционные.

### Код (`server/.../agents/stream/`)
- `StreamCloseReason` — причины закрытия с кодом (таблица фазы 1), `close()` → `StreamClose(reason, status)`; форма статуса — `ErrorInfo` домена `sard.dev`, как в ADR 00XX-draft.
- `AgentStreamExtensions.kt` — точки расширения (сигнатуры ниже) и `LoggingInbound` (debug, только id и счётчики, без содержимого).
- `AgentStreamSettings` / `AgentStreamProperties` — `sard.agent.heartbeat-interval` + `sard.agent.stream.*`, окна в интервалах heartbeat, проверка значений при старте.
- `AgentStream` — одно открытое соединение: `lastMessageAt`, подозрение на дубликат, троттлинг `last_seen_at`, однократный отчёт о расхождении часов, ограниченная очередь исходящих (`offer` для `send` фазы 3), `close` — первая причина выигрывает и отменяет корутину стрима (в том числе если закрытие пришло раньше привязки).
- `AgentSessionRegistry` — `agent_id → AgentStream` в памяти под одной блокировкой; `opened` (до Hello), `claim` (правило дубликата), `release` (только своего слота), `online`, `sweep` (просрочка и hello-timeout).
- `AgentStreams` — реализация Connect: читатель на `agentStreamDispatcher` (Dispatchers.IO, бин) обрабатывает сообщения по порядку; поток ответа — единственный писатель, отдаёт очередь по готовности gRPC. Закрытие сервером — отмена корутины и `StatusRuntimeException` с причиной; принципал — из gRPC `Context` (S3), вне аутентифицированного вызова — `IllegalStateException`.
- `AgentLastSeen` — HQL `update … where last_seen_at is null or < :at` через `AgentSessions.inTenant` (S3), назад не двигает.
- `AgentStreamSweeper` — `SmartLifecycle`, `check-interval`; сейчас — `registry.sweep()`, в фазе 3 — ещё сверка с БД.
- `AgentGrpcService.connect` — одна строка делегирования (S4a добавит `register` рядом — конфликт минимален).
- `application.yaml` — `spring.grpc.server.keepalive.{time: 30s, timeout: 10s, permit.time: 20s, permit.without-calls: true}` (имена проверены по `META-INF/spring-configuration-metadata.json` в `spring-boot-grpc-server-4.1.1.jar`), `sard.agent.heartbeat-interval: 30s`, `sard.agent.stream.*`.

### Точки расширения (для S6, S7)

Вызываются на корутине стрима, по порядку сообщений, на `agentStreamDispatcher`, внутри gRPC `Context` агента (`AgentSessions.inTenant` работает). Исключение из обработчика закрывает стрим; медленный обработчик задерживает только своего агента. Бин заменяет реализацию по умолчанию (`ObjectProvider.getIfUnique`).

```kotlin
data class ConnectedAgent(val agentId: UUID, val tenantId: UUID, val serial: String)
fun interface CommandReconciliation { fun onHello(agent: ConnectedAgent, runningCommandIds: List<String>) }  // S6; после занятия слота
fun interface StepProgressHandler { fun handle(agent: ConnectedAgent, progress: StepProgress) }  // S6
fun interface StepResultHandler   { fun handle(agent: ConnectedAgent, result: StepResult) }      // S7; ResultAck — его задача
fun interface LogChunkHandler     { fun handle(agent: ConnectedAgent, chunk: LogChunk) }         // S7
interface AgentSessionListener {                                                                // любое число бинов
    fun connected(agent: ConnectedAgent) {}
    fun disconnected(agent: ConnectedAgent, reason: String) {}   // имя StreamCloseReason или STREAM_ENDED
    fun duplicateDetected(agent: ConnectedAgent) {}
}
AgentSessionRegistry.online(agentId: UUID): Boolean
```
`send(agentId, ConnectResponse): SendResult` — фаза 3.

### Правило дубликата (как реализовано)
На Hello нового стрима: если держатель слота получал сообщение меньше `duplicate-window` назад — новый закрыт `ALREADY_EXISTS`/`AGENT_DUPLICATE_SESSION`, держатель помечен подозрением; **первое же следующее сообщение держателя** — `duplicateDetected` + WARN (один отказ — один отчёт). Иначе держатель закрыт `UNAVAILABLE`/`SESSION_REPLACED`, новый занимает слот; `release` старого слот не освобождает.

### Тесты
- Юнит (`@MutFlowTest` там, где логика): `StreamStatusTest` (2), `AgentStreamTest` (11), `AgentSessionRegistryTest` (14), `AgentStreamsTest` (11; фейки всех точек расширения, `MovableClock`, без сна — ожидание событий), `AgentStreamPropertiesTest` (6), `AgentStreamSweeperTest` (1).
- Интеграционные (TLS на случайном порту, PostgreSQL в Testcontainers, агенты через настоящий `Enrollment`, `MovableClock`, `check-interval=1h` — метёт сам тест):
  - `AgentStreamIntegrationTest` (8): настройки keepalive и heartbeat; тест 2 (не-Hello → `FAILED_PRECONDITION`/`HELLO_REQUIRED`; Hello → сверка получает `cmd-1,cmd-2`, агент онлайн); тест 3 (клон → `ALREADY_EXISTS`/`AGENT_DUPLICATE_SESSION`, после сообщения первого — ровно одно `duplicate`; через `duplicate-window` — прежний `UNAVAILABLE`/`SESSION_REPLACED`, новый принят, событий дубликата и отключения нет); тест 4 (`last_seen_at` в БД: на Hello, не меняется через 29 s, меняется через 30 s; `sweep` за 1 s до `offlineAfter` — жив, на `offlineAfter` — `UNAVAILABLE`/`SESSION_EXPIRED`, `disconnected SESSION_EXPIRED`, не онлайн).
  - `KeepaliveIntegrationTest` (1), тест 1: клиент grpc-java не пингует чаще 10 s, поэтому пропорция агента (30 s против 20 s) сохранена как 10 s против 6 s; ждёт 40 s реального времени (это часы транспорта, не наши). **Контрольный прогон:** с `permit.time=5m` (значение grpc-java по умолчанию) тест падает — стрим закрыт `RESOURCE_EXHAUSTED` (GOAWAY `too_many_pings`), то есть ровно тот обрыв, который чинит задача; изменение откачено.
- Hello-timeout проверен в `AgentStreamsTest` (стрим без сообщений закрывается `HELLO_REQUIRED` на границе, за 1 ms до неё — нет) и в `AgentSessionRegistryTest`; интеграционно — нет (нет детерминированного способа дождаться регистрации стрима без сообщений).

### Изменён тест S3
`AgentAuthIntegrationTest` «a live agent's certificate passes the interceptor on every AgentService method» ожидал `UNIMPLEMENTED` от каждого метода; ADR 0009:17 оговаривал «до S4/S5». Connect теперь реализован, и одно пустое сообщение теста — не Hello: ответ `FAILED_PRECONDITION`/`HELLO_REQUIRED`, который выставляет только обработчик, то есть перехватчик пройден. Ожидание для Connect заменено на него (`pastInterceptor`), остальные методы — `UNIMPLEMENTED`; утверждение не ослаблено. S4a столкнётся с тем же для Register.

### Проверка
- `./scripts/gate.sh server fast` — `PASSED`; 335 тестов, 0 упавших, 0 пропущенных (из них 54 — пакет `stream`); покрытие 94.0% (instructions); CRAP ≤ 6 (новый код — максимум 5.0, `AgentStreamProperties.validateWindows`). Первые прогоны: CRAP 9.3/8.0 у проверки настроек — разделена на две функции; `tick()` сборщика без покрытия — тест `AgentStreamSweeperTest`.
- `./gradlew -Pmutflow.enabled=true :server:test --rerun` — exit 0, 4785 запусков тестов. Первый прогон: 5 выживших (`<`→`<=` в `online`, `compareAndSet` в `close`, возвраты `received`/`lastSeenDue`, затем `due`→`!due`) — граничные вызовы стояли вне `MutFlow.underTest`, а отмена корутины при закрытии не проверялась; тесты разделены и усилены, код не менялся.
- `make license-check` — 264 files OK.

### Для фазы 3
`send` с `SendResult`; сверка с БД (пакетный `system`-запрос, `ArchitectureTest` + ADR 0013) и `close(agentId, reason)`; остановка сервера (`SERVER_SHUTTING_DOWN` до остановки gRPC); метрики (`sard.agents.connected`, `sard.agent.clock.skew`, счётчик дубликатов); ADR; тесты 5–8 (8 — с тестовой заглушкой Register).
