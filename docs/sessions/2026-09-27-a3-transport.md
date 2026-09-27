# Сессия 2026-09-27: транспорт агента (A3)

Ветка `feat/a3-transport` от `main` @ `7bfb245`.

## Фаза 1: исследование и дизайн

### Что есть (проверено чтением кода)
- Контракт: `proto/sard/agent/v1/agent.proto` — `Register` (FAILED_PRECONDITION
  при несовместимом `protocol_version`, :71-86), `Connect` — двунаправленный
  стрим, «first message must be Hello … reconnects with backoff and sends Hello
  again» (:77-80). `ConnectRequest.message`: heartbeat=1, step_result=2,
  hello=3, step_progress=4, log_chunk=5. `ConnectResponse.message`:
  run_step=1, cancel_step=2 — сообщения подтверждения нет. `StepResult`:
  «if the stream breaks before the server acknowledges it by closing the
  command, the agent resends it after reconnecting» (:225-248).
- `RegisterResponse`: `agent_id = 1`, `google.protobuf.Duration
  heartbeat_interval = 2`.
- Сервер (P3): один порт, TLS всегда, `client-auth: optional`
  (`application.yaml:16-32`); отдаёт **только листовой** сертификат
  (`FileCertificateAuthority.kt:58`), ECDSA P-256, SAN — `sard.pki.server-names`
  (по умолчанию `localhost,127.0.0.1,::1`); серверный сертификат живёт 90 дней
  и перевыпускается без рестарта (ADR 0014:53) — доверять надо CA, не листу.
  `AgentGrpcService` пуст (UNIMPLEMENTED) — добавление в oneof не ломает сервер.
- Агент: `transport.GRPC` — заглушки (`transport.go:30-59`), `session.Stub`
  (`session.go:18-23`), `app.Agent.Run` = Register → Connect → Serve без
  повторов, ответ Register выбрасывается (`app.go:50-62`). `cfg.TLS` нигде не
  используется, пути не проверяются (`config.go:52-58`). grpc в `agent/go.mod`
  — `// indirect`.
- A4 (`origin/feat/a4-executor`, только журнал дизайна, кода нет):
  `Sink{Progress, Result, Log}` — вызывается под блокировкой исполнителя,
  «must not block»; `Submit(*RunStep)`, `Cancel(id)`, `Ack(id) error`,
  `RunningIDs()`, `PendingResults()` — результаты хранятся на диске до `Ack`.

### Дизайн (на ревью)

**Proto.** В `ConnectResponse.message` — `ResultAck result_ack = 3;`,
`message ResultAck { string command_id = 1; }`. Семантика: сервер отправляет
его, когда результат команды надёжно записан (транзакция закрыта); агент
после этого удаляет сохранённый результат и больше его не отправляет.
Повторный или неизвестный `command_id` в `ResultAck` агент игнорирует.
Комментарий `StepResult` («closing the command») заменяется ссылкой на
`ResultAck`. Изменение аддитивное.

**Пакет `session` удаляется** (решение владельца): стрим целиком ведёт
`transport`, отдельный слой между ним и исполнителем не нужен.

**Границы `transport`** (совпадают с методами `*executor.Executor` из A4, чтобы
он подключался без адаптера):
```go
// Commands receives what the server sends; called from the receive loop.
type Commands interface {
    Submit(step *agentv1.RunStep)
    Cancel(commandID string)
    Ack(commandID string) error
}
// State is read before every Hello and after every reconnect.
type State interface {
    RunningIDs() []string
    PendingResults() []*agentv1.StepResult
}
```
`transport.Link` реализует `Sink` A4 (`Progress`, `Result`, `Log`) — не
блокируя.

**Отправка — одна горутина.** Всё исходящее идёт через `Link`:
- прогресс — «последний на `command_id`» (перезапись, не очередь): при
  переполнении старый прогресс теряется без вреда;
- логи — ограниченная очередь строк; при переполнении строки отбрасываются,
  в поток уходит отметка «dropped N lines» (A7 уточнит);
- результаты — множество по `command_id` без ограничения по числу (его
  ограничивает исполнитель: результаты живут до `Ack`), никогда не
  отбрасываются; при каждом новом стриме заново наполняется из
  `State.PendingResults()`.

**Модель соединения.** Цикл: dial (mTLS) → `Register` → `Connect` → `Hello`
(первым, из `State.RunningIDs()`) → неподтверждённые результаты → heartbeat по
интервалу из `RegisterResponse` + исходящие; параллельно — приём
`RunStep`/`CancelStep`/`ResultAck`. Обрыв стрима → задержка → снова dial.

**Задержки.** Экспонента с полным джиттером: база 1 с, ×2, потолок 60 с;
сбрасывается, если стрим прожил ≥ 30 с. Часы и источник случайности
внедряются.

**Ошибки Register.** `FAILED_PRECONDITION` → `ErrIncompatibleProtocol`, цикл
завершается, повторов нет. Недоступность/обрыв — повтор с задержкой.

**mTLS.** `tls.Config`: `RootCAs` — только `tls.ca_file`, `ServerName` — хост из
`server.address`, `MinVersion` TLS 1.2, клиентский сертификат через
`GetClientCertificate`, читающий файлы при каждом рукопожатии (обновлённый
сертификат подхватывается при переподключении без рестарта). gRPC keepalive
клиента (30 с / таймаут 10 с) — чтобы «зависший» стрим обнаруживался.

### Открытые вопросы
См. отчёт фазы 1 (ответ владельцу); ответы — в начале фазы 2.
