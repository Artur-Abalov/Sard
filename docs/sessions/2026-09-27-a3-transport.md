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

## Ответы владельца (начало фазы 2)
1. `ResultAck { command_id }` — поле 3 в `ConnectResponse`; сервер шлёт после
   надёжной записи, в том числе для неизвестного ему `command_id`; повторный
   или неизвестный ack агент игнорирует. — **да**.
2. Логи **не отбрасываются**: при заполненной очереди — обратное давление
   (`Log` ждёт места). **Требование к A4 при интеграции:** в дизайне A4
   `Sink` вызывается под блокировкой исполнителя и «must not block» — `Log`
   (и только он) должен вызываться вне этой блокировки, иначе заполненная
   очередь остановит исполнитель целиком. `Progress` и `Result` не
   блокируют никогда.
3. `Register` — на каждом подключении, не только при старте. — **да**.
4. `UNAUTHENTICATED` / `PERMISSION_DENIED` — типизированная ошибка и
   остановка, как `FAILED_PRECONDITION`.
5. Задержка: база 1 с, ×2, потолок 60 с, полный джиттер, сброс после стрима
   ≥ 30 с. — **да**.
6. keepalive клиента 30 с / 10 с. — **да** (учесть разрешение keepalive на
   сервере в S3).
7. Сертификат и ключ читаются при каждом рукопожатии. — **да**.
8. Имя сервера — только хост из `server.address`, без `tls.server_name`. — **да**.

## Фаза 2: proto, mTLS, Register, Connect с Hello, heartbeat

### Решения и причины
- **Proto:** `ResultAck result_ack = 3` в `ConnectResponse`, `message ResultAck
  { string command_id = 1; }` с семантикой из ответа 1; комментарий
  `StepResult` переписан: результат уходит один раз на стрим и повторяется на
  каждом новом стриме до `ResultAck`. Сервер обязан считать повторный
  `StepResult` закрытой команды no-op и снова отвечать `ResultAck`.
- **Пакет `agent/internal/session` удалён** (решение владельца). Его
  интерфейс `Session.Serve(stream)` и заглушка нигде, кроме `app` и `main`,
  не использовались; в документации не упоминались. Стрим целиком ведёт
  `transport`: единственный отправитель в стрим — горутина heartbeat
  (после `Hello`), приём — отдельная горутина.
- `transport.Client`, `transport.GRPC` и их заглушки заменены на
  `transport.Transport` (`New(Options)`, `Connect(ctx)` — одно подключение:
  Register → стрим до конца). `Enroller` оставлен для A2.
- `app.Agent`: вместо `Client` + `Session` — `Link interface{ Connect(ctx) error }`;
  `registerRequest` стал публичным `RegisterRequest(ctx)` — его вызывает
  транспорт перед каждым стримом (ответ 3). `app.NoExecutor` — заглушка
  исполнителя до интеграции с A4: ничего не выполняется, команды
  отбрасываются.
- `main`: `transport.New` c `cfg.TLS`; без `tls.*` агент не стартует
  (`invalid transport options: tls.ca_file is required`) — раньше тест ждал
  `register: not implemented`.
- mTLS: `RootCAs` — только `tls.ca_file` (сервер отдаёт лист без корня),
  `ServerName` — хост `server.address`, TLS ≥ 1.2, клиентский сертификат
  читается при каждом рукопожатии; ошибка называет пути, не содержимое.
- `Register`: `FAILED_PRECONDITION` → `ErrIncompatibleProtocol`,
  `UNAUTHENTICATED`/`PERMISSION_DENIED` → `ErrNotAuthorized`;
  `IsPermanent(err)` — для цикла переподключения фазы 3.
- Интервал heartbeat — из `RegisterResponse`; ≤ 0 или нет → 30 с
  (`DefaultHeartbeat`). keepalive клиента 30 с / 10 с.
- `grpc` стал прямой зависимостью `agent/go.mod` (была `// indirect`) —
  новых модулей нет.

### Отвергнуто
- Оставить `session` как слой между стримом и исполнителем — лишняя граница:
  у исполнителя (A4) уже есть нужные методы.
- Проверять TLS-пути в `config.validate` — транспорт всё равно читает файлы
  и сообщает точнее; конфиг без `tls.*` по-прежнему разбирается (A2 запишет
  файлы позже).

### Изменённые файлы
- `proto/sard/agent/v1/agent.proto`, `proto/gen/go/sard/agent/v1/agent.pb.go`
- `agent/internal/transport/{transport.go,transport_test.go,helpers_test.go,export_test.go}`
- `agent/internal/app/{app.go,app_test.go}`, `agent/cmd/sard-agent/{main.go,main_test.go}`
- удалены `agent/internal/session/{session.go,session_test.go}`; `agent/go.mod`

### Проверено (команды запускались)
- `./scripts/gate.sh proto` — PASSED (buf lint, buf breaking против
  `origin/main`, сгенерированный код совпадает).
- `./gradlew :server:compileKotlin` — exit 0 (JDK 25 поставлен в окружение;
  первая попытка упала на 429 от Maven Central).
- `go test -race ./internal/transport` (count=2) — ok; `./scripts/gate.sh agent`
  — PASSED: покрытие 98.0%, CRAP ≤ 6, mutation score 0.944223.
- Тесты на фейковом gRPC-сервере с TLS от тестового CA и
  `VerifyClientCertIfGiven`: подключение с проверенным клиентским
  сертификатом; сервер чужого CA — `Unavailable … certificate signed by
  unknown authority`, до сервера не дошло ни одного вызова; `Hello` первым с
  running ids; `RunStep`/`CancelStep`/`ResultAck` доходят до исполнителя;
  heartbeat по интервалу `RegisterResponse` с временем фейковых часов;
  типизированные отказы `Register` без `Connect`.
- Тест битого клиентского ключа сначала проходил по неверной причине
  (файлы агента от другого CA — падала проверка сервера); исправлен: тот же
  CA, ошибка называет `tls.key_file`, содержимое ключа в ней нет.

### Выжившие мутанты в `transport` (осознанно)
- `errs` с буфером 1/3 и пропуск `<-errs`, `conn.Close` без вызова — дают
  только утечку горутины/соединения, тестом без goleak не видно.
- `return streamError(...)` после `client.Connect` и отправки `Hello` —
  ошибки практически недостижимы в тесте.
- `return err` после неудачной отправки heartbeat — эквивалентен: приём
  тоже получает ошибку и отменяет стрим.
- `return ctx.Err()` в heartbeat — мутация зацикливает горутину, go-mutesting
  засчитывает свой таймаут как «выжил».

### Открытые вопросы
- Нет (фаза 3: цикл переподключения, очередь исходящих, повторная отправка).
