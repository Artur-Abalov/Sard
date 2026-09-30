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
| `RegistrationTest` | токен → Enroll → сертификат проходит перехватчик S3 (`RenewCertificate` → UNIMPLEMENTED); без сертификата — UNAUTHENTICATED; токен одноразовый |
| `AgentConnectTest` | настоящий агент после регистрации выполняет Register и открывает Connect (`agents.last_register_at`, `last_seen_at`) |
| `RunStepSeamTest` | шаг в `queued` доходит до настоящего агента на Hello (`run_steps.status = dispatched`); агент отклоняет неизвестный плагин, StepResult `REJECTED` доходит до обработчика сервера (строка журнала до S7) |
| `EnrollmentTokenFormatTest` | помощник токена — тестовый вектор `docs/specs/enrollment-token.md` |
| `FailureLogsTest` | сломанная конфигурация сервера роняет старт, логи собраны, секреты замаскированы |

## Точки замены

| Помощник | Сейчас | Заменить на | Когда |
|---|---|---|---|
| `EnrollmentTokens.create` | строка в `enrollment_tokens` строго по спецификации | `POST /api/v1/enrollment-tokens` | S8b в `main` |
| `AgentEnroller.enroll` | минимальный клиент Enroll: ключ P-256, CSR, сверка отпечатка | `sard-agent enroll` в контейнере агента, файлы читаются обратно | A2 в `main` |

## Заготовки (`Pending.kt`)

Классы без тестовых методов: ничего не отключено, запускать пока нечего. Шаги — в KDoc.

| Сценарий | Условие включения |
|---|---|
| `TransportExecutorPending` — транспорт + исполнитель с настоящим сервером (хвост 5) | S7 в `main` (отправка S6a — `RunStepSeamTest`) |
| `FullChainT2Pending` — полная цепочка T2: бэкап, снапшот, проверенное восстановление, `lastVerifiedRestoreAt` | S7 и A2 в `main` |
| `StreamBreakT3Pending` — обрыв стрима T3 | S7 в `main` |
