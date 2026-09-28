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
AgentSessionRegistry.online(agentId: UUID): Boolean   // в фазе 3 — AgentConnections.online
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

## Фаза 3 — send, сверка с БД, остановка, метрики, ADR (тесты 5–8)

Тесты писались первыми (`AgentStreamTest`, `AgentSessionRegistryTest`, `SessionRevalidationTest`, `AgentStreamsTest`, затем интеграционные).

### Код
- `AgentConnections` — фасад для S6/S7/S8b: `send`, `online`, `close(agentId, failure)`, `check()` (сверка с БД, затем `sweep`), `shutdown()`, `reopen()`. Выделен из `AgentStreams` по `TooManyFunctions` (detekt, порог 11): `AgentStreams` — только Connect.
- `SendResult` (`Queued`, `NotConnected`, `QueueFull`); `AgentStream.offer` — `trySend` в ограниченную очередь сессии; закрытый стрим — `NotConnected`.
- `SessionRevalidation` — один `BatchStandings.of(serials)` на все сессии, каждую судит `AgentAuthenticator` S3 (те же причины и порядок); `AgentAuthFailure.close()` — `UNAUTHENTICATED` через `AgentAuthStatus`.
- `AgentCertificateStandings` реализует и `BatchStandings`: HQL `… where c.serial in (:serials)`; строки запроса S3 собраны из общих констант (SQL тот же). Третий вызов `system` — в том же файле: `ArchitectureTest` не меняется (уточнён комментарий), в ADR 0013 — пункт 3.
- `AgentStreamShutdown` — `ApplicationListener<GrpcServerLifecycleEvent>`: `GrpcServerShutdownEvent` → `shutdown()`, `GrpcServerStartedEvent` → `reopen()`. `GrpcServerLifecycle.getPhase()` = `Integer.MAX_VALUE` (байткод `spring-grpc-core-1.1.1`), то есть gRPC-сервер останавливается первым из `SmartLifecycle`, поэтому своя фаза не годится; событие публикуется в `stopAndReleaseGrpcServer` прямо перед `Server.shutdown()` (там же, байткод).
- `MicrometerStreamMetrics` — `sard.agents.connected` (gauge по реестру), `sard.agent.duplicate.sessions`, `sard.agent.clock.skew` (модуль, секунды); `MeterRegistry` actuator, без экспортёра.
- Сборщик вызывает `AgentConnections.check`.

### Точки расширения — итог (для S6, S7)
```kotlin
// AgentConnections — бин
fun send(agentId: UUID, message: ConnectResponse): SendResult   // не ждёт; Queued ≠ доставлено
fun online(agentId: UUID): Boolean
fun close(agentId: UUID, failure: AgentAuthFailure): Boolean     // для API отзыва (S8b)
sealed interface SendResult { Queued; NotConnected; QueueFull }
// входящие и события — как в фазе 2: CommandReconciliation, StepProgressHandler,
// StepResultHandler, LogChunkHandler, AgentSessionListener (бины заменяют умолчания)
```

### Найдено и исправлено: общий статус ломал одновременное закрытие
Тест 7 падал примерно в каждом третьем прогоне пакета (2 из 6): вместо `UNAVAILABLE`/`SERVER_SHUTTING_DOWN` клиент получал `CANCELLED` через 30 s. Диагностика (временные `println`, откачены): слушатель закрывал оба стрима, корутины ловили отмену с `closedBy=SERVER_SHUTTING_DOWN` и бросали статус; в stderr — `ArrayIndexOutOfBoundsException` в `io.grpc.Metadata.discardAll` из `NettyServerStream$Sink.writeTrailers` при `ServerCallImpl.close` (вызов из grpc-kotlin `ServerCalls.kt:256`). Причина: `closeAll` создавал один `StreamClose` — одно `StatusRuntimeException` с одним объектом трейлеров — на все стримы; grpc-kotlin берёт трейлеры из исключения, gRPC меняет их при записи, `Metadata` не потокобезопасен; `close()` падал, вызов висел до `shutdownNow`. Исправление: `StreamClose.status()` создаёт статус заново на каждый бросок. Регрессия: `StreamStatusTest` (разные трейлеры на каждый вызов), `AgentSessionRegistryTest`, тест 7 с 8 стримами. Контроль: с кэшированным статусом тест 7 падает в 2 из 2 прогонов; с исправлением пакет — 4 из 4 зелёных подряд, затем зелёные прогоны шлюза и mutflow.

Там же найден второй дефект: после остановки gRPC-сервера реестр отклонял новые стримы навсегда, а Spring 7 останавливает и снова запускает lifecycle-бины кэшированных тестовых контекстов (в журнале — «server stopping: 0 agent streams closed» при переключении классов). Исправлено `reopen()` на `GrpcServerStartedEvent`; тесты в `AgentSessionRegistryTest` и `AgentStreamsTest`.

### Тесты 5–8
- 5 (`AgentStreamIntegrationTest`): `agents.revoked_at` → `UNAUTHENTICATED`/`AGENT_REVOKED` и `disconnected AGENT_REVOKED`; `agent_certificates.revoked_at` → `CERT_REVOKED`; `not_after` = сейчас + 1 s: за секунду до — открыт, после — `CERT_EXPIRED`; сессия другого агента при этом открыта и онлайн. Юнит (`SessionRevalidationTest`, `AgentStreamsTest`): все причины S3, один запрос на все сессии, без сессий — без запроса, отзыв выигрывает у просрочки.
- 6: `send` доходит в порядке; не подключён — `NotConnected`; агент, не читающий ответы (`disableAutoRequestWithInitial(0)`), с сообщениями по 64 KiB: окно HTTP/2 и буфер gRPC заполняются, затем очередь — первый не-`Queued` ответ `QueueFull`; сообщение другому агенту доходит сразу; после `request(n)` медленный получает свои сообщения, начиная с первого. Первая версия теста проверяла `QueueFull` ещё одной отправкой; в прогоне mutflow писатель успел забрать сообщение, и она вернула `Queued`. Утверждение было неверным (очередь не обязана оставаться полной); тест проверяет первый отказ, без повторных отправок.
- 7 (`ServerShutdownIntegrationTest`): 8 агентов на связи, `GrpcServerLifecycle.stop()` (тот же путь, что при остановке приложения) → каждый стрим `UNAVAILABLE`/`SERVER_SHUTTING_DOWN`, остановка короче отсрочки 30 s (класс целиком со стартом контекста — около 3 s). Закрыть контекст из теста нельзя — колбэки Spring TestContext падают на закрытом контексте; `@DirtiesContext` выбрасывает его после класса.
- 8 — ручной прогон шва с настоящим A3 (каркаса `test/e2e` нет). Временный harness (в репозиторий не попал): тест Spring Boot на порту 19443 с `sard.agent.heartbeat-interval=5s`, `check-interval=2s`; **тестовая заглушка Register** — глобальный перехватчик после перехватчика S3 (отозванный агент по-прежнему получает `UNAUTHENTICATED`), отвечает `agent_id` и `heartbeat_interval`; сертификат выпущен настоящим `Enrollment` (у агента пока нет команды enroll — A2), PEM и `agent.yaml` записаны во временный каталог. Агент собран `go build ./cmd/sard-agent` (go1.27.1) и запущен с этим конфигом. Вывод:
  ```
  09:21:12.399 enrolled agent 01a0e751-…; waiting for it to connect
  09:21:13.369 Register from agent 01a0e751-…
  09:21:13.514 connected
  09:21:13.550 Hello from 01a0e751-…, running=[]
  09:21:23.517 last_seen_at 09:21:13.515324Z -> 09:21:23.426590Z
  09:21:23.517 restarting the gRPC server
  09:21:23.526 AgentStreams: server stopping: 1 agent streams closed
  09:21:23.807 disconnected SERVER_SHUTTING_DOWN
  09:21:26.825 gRPC Server started … port: 19443
  09:21:28.708 Register from agent 01a0e751-…
  09:21:28.724 connected
  09:21:28.729 Hello from 01a0e751-…, running=[]
  09:21:38.755 heartbeats after restart; revoking the agent
  09:21:40.494 agent 01a0e751-…: session closed, AGENT_REVOKED
  агент: sard-agent: register: server refused the agent certificate: rpc error: code = Unauthenticated desc = agent certificate rejected
  агент: 09:21:53 sard-agent exited with 1
  ```
  Проверено: A3 проходит Register → Connect → Hello, шлёт heartbeat (`last_seen_at` движется), переживает перезапуск gRPC-сервера (переподключение через ~2 s после старта) и после отзыва останавливается на отказе Register; поведение A3 менять не нужно. Наблюдение: первый heartbeat A3 приходит чуть раньше полного интервала после Hello (таймер агента стартует после ответа Register), поэтому при троттлинге «не чаще интервала» `last_seen_at` фактически обновляется раз в два интервала (здесь 10 s при 5 s). Онлайн-статус от этого не зависит (он по памяти); для консоли (S5b) `last_seen_at` отстаёт до двух интервалов — решить там, нужен ли допуск.
  После слияния S4a в `main` заглушку заменит настоящий Register; harness стоит перенести в `test/e2e`, когда появится каркас.

### Проверка
- `./gradlew :server:test --rerun` — exit 0, 367 тестов, 0 упавших, 0 пропущенных (пакет `stream` — 86).
- `./scripts/gate.sh server fast` — `PASSED`; покрытие 94.7% (instructions); CRAP ≤ 6 (новый код — максимум 5.0). По пути: `TooManyFunctions` у `AgentStreams` (14) и `AgentSessionRegistry` (12) — выделен `AgentConnections`, два помощника времени слиты в `since`.
- `./gradlew -Pmutflow.enabled=true :server:test --rerun` — exit 0, 5149 запусков тестов. По пути: выживший мутант в `opened()` (учёт стрима до Hello вызывался вне `MutFlow.underTest`) — добавлен тест.
- `make license-check` — 272 files OK.

### Итог по Definition of done
- `./gradlew :server:test` проходит — выше.
- Агент с пингами каждые 30 s не отключается: `permit.time 20s`, `permit.without-calls true`; тест 1 (пропорция 10 s / 6 s) и контроль с 5 min (`RESOURCE_EXHAUSTED`).
- Одна сессия на агента; правило дубликата и устаревшей сессии — тесты 3 (юнит и интеграция), ADR 00XX-draft менеджера стримов.
- Закрытие при отзыве и истечении сертификата без перезапуска — тест 5 и шов (отзыв закрыл сессию за ≤ `check-interval`).
- Точки расширения S6/S7 с сигнатурами — фазы 2 и 3 выше.

### Хвосты
- Пометка дубликата в БД и консоли, онлайн-статус в REST — S5b/W2 (решение владельца 2).
- Register — S4a (`sard.agent.heartbeat-interval` общий); после слияния — шов с настоящим Register.
- Задержка закрытия после отзыва — до `check-interval` (30 s); мгновенно — через `AgentConnections.close` из будущего API отзыва (S8b).

## Слияние `main` (S4a Register, PR #15)

Конфликты — три, все по местам стыка с S4a:
- `AgentGrpcService.kt` — оба метода: `register` (S4a) и `connect` (делегирование в `AgentStreams`); конструктор S4a + `streams`.
- `application.yaml` — строка S4a `heartbeat-interval: ${SARD_AGENT_HEARTBEAT_INTERVAL:30s}` и блок `sard.agent.stream`.
- `AgentAuthIntegrationTest` — таблица `handlerAnswersToEmpty` из `main` (S4a заменил ту же проверку, что правила фаза 2), в неё добавлен Connect → `FAILED_PRECONDITION`/`HELLO_REQUIRED`.

Интервал heartbeat теперь читается из `AgentEndpointProperties.heartbeatInterval` (привязка S4a), а не через `@Value`: один источник для Register и менеджера стримов.

Проверка после слияния: `./scripts/gate.sh server fast` — `PASSED`, 420 тестов, 0 упавших, 0 пропущенных, покрытие 94.5%; `./gradlew -Pmutflow.enabled=true :server:test --rerun` — exit 0 (6508 запусков); `make license-check` — 292 files OK.

### Шов с настоящим Register (после слияния S4a)
Тот же временный harness, без заглушки Register (+ `sard.agent.endpoint=localhost:19443`), агент A3 из `agent/cmd/sard-agent`:
```
09:47:07.847 enrolled agent 01a0e769-…; waiting for it to connect
09:47:09.337 connected
09:47:09.374 Hello from 01a0e769-…, running=[]
09:47:19.361 last_seen_at 09:47:09.338448Z -> 09:47:19.323258Z
09:47:19.366 AgentConnections: server stopping: 1 agent streams closed
09:47:19.567 disconnected SERVER_SHUTTING_DOWN
09:47:22.589 gRPC server started again
09:47:27.180 connected
09:47:27.183 Hello from 01a0e769-…, running=[]
09:47:37.294 revoking the agent
09:47:37.971 agent 01a0e769-…: session closed, AGENT_REVOKED
агент: sard-agent: register: server refused the agent certificate: rpc error: code = Unauthenticated desc = agent certificate rejected
агент: 09:48:02 sard-agent exited with 1
```
Настоящий Register (S4a) → Connect → Hello → heartbeat, перезапуск gRPC-сервера, отзыв — как с заглушкой. Заглушка больше не нужна; harness по-прежнему вне репозитория до каркаса `test/e2e`.
