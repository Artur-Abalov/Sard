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

### Вопросы владельцу (трудные первыми)

См. ответ в чате; решения будут записаны здесь.
