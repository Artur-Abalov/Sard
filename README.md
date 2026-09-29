# Sard

Sard — open-core оркестратор бэкапов. Одна консоль управляет бэкапами разнородной инфраструктуры (PostgreSQL, MySQL, файлы, конфигурации MikroTik/Eltex, позже 1С и другие) и **доказывает, что из них можно восстановиться**. Главная метрика дашборда — «последнее проверенное восстановление N часов назад».

Свой формат хранения Sard не пишет: хранение, дедупликацию и шифрование делает [restic](https://restic.net). Sard отвечает за источники, сценарии, расписания, проверку восстановления, отчёты и мультитенантность.

> Статус: **каркас**. Всё собирается и проходит тесты, бизнес-логики нет — методы плагинов, транспорт агента и gRPC-сервис отвечают «не реализовано».

## Архитектура

```
             браузер / sardctl
                    │ REST /api/v1
                    ▼
           ┌─────────────────┐       PostgreSQL
           │   sard-server   │──────── (Flyway)
           │ Kotlin, Spring  │
           └─────────────────┘
                    ▲ gRPC + mTLS, соединение открывает агент
                    │
           ┌─────────────────┐   restic    ┌──────────────────────┐
           │   sard-agent    │────────────▶│ S3 / SFTP / локально │
           │  Go, на хосте   │             └──────────────────────┘
           └─────────────────┘
```

- Агент сам подключается к серверу; входящие порты на хостах не открываются.
- Данные идут от агента напрямую в хранилище, минуя сервер.
- Плагины-источники реализуют один интерфейс: prepare → dump → stream → verify.
- Enterprise-модули подключаются как Spring Boot-стартеры через `server/.../extension`; открытое ядро работает без них.

| Каталог | Что это |
|---|---|
| `server/` | `sard-server`: Kotlin, Spring Boot 4.1, PostgreSQL, Flyway, gRPC |
| `agent/` | `sard-agent`: Go, один бинарник; `agent/plugins/sdk` — SDK плагинов |
| `cli/` | `sardctl`: Go, один бинарник |
| `proto/` | gRPC-контракты сервер ↔ агент (buf); `proto/gen/go` — сгенерированный Go-код, `proto/jvm` — JVM-привязки |
| `web/` | SPA: React, TypeScript, Vite, Mantine |
| `deploy/` | docker-compose, Dockerfile сервера, systemd-юнит агента |
| `examples/workflows/` | примеры YAML-сценариев |
| `tools/` | инструменты разработки (CRAP, закреплённые Go-утилиты) |
| `docs/` | ADR, лог сессий, зависимости, CLA |

## Требования

JDK 25, Go 1.27, Node.js 24, Docker (для тестов сервера через Testcontainers и `make up`), GNU make. buf, golangci-lint и остальные Go-инструменты ставить не нужно: `make tools` собирает закреплённые версии в `.bin/`.

## Сборка и проверка

```bash
make proto    # сгенерировать Go-код из proto/ (результат коммитится)
make build    # agent/bin/sard-agent, cli/bin/sardctl, server/build/libs/sard-server.jar, web/dist/
make test     # все тесты
make lint     # SPDX-заголовки, buf lint, gofmt/vet/golangci-lint, spotless/detekt, oxlint/prettier/tsc
make gate     # полный шлюз качества: покрытие ≥ 80%, CRAP ≤ 6, сложность ≤ 8, мутационное тестирование
```

## Запуск

```bash
make up                                   # PostgreSQL + sard-server (deploy/.env создаётся из .env.example)
curl -s localhost:8080/api/v1/status      # {"version":"…","lastVerifiedRestoreAt":null}
cd web && npm install && npm run dev      # консоль на http://localhost:5173, API проксируется на :8080
make down
```

Пароль администратора — переменная `SARD_ADMIN_PASSWORD` в `deploy/.env`
(рядом с `SARD_AGENT_ENDPOINT`), не короче 12 символов; без неё сервер не
стартует. При первом `make up`, если `deploy/.env` ещё нет, туда записывается
случайный пароль длиной от 24 символов; команда печатает путь к файлу, но не
сам пароль (ADR 0021). Консоль открывается со страницы входа; выйти можно
кнопкой в шапке.

Этап 1 поддерживает только прямой доступ к порту 8080: за TLS-терминирующим
обратным прокси (или любым, что переписывает `Host`) вход и любой изменяющий
запрос отвечают 403, а cookie сессии не получает `Secure` (ADR 0021).

Агент и CLI:

```bash
./agent/bin/sard-agent --version
./agent/bin/sard-agent --config deploy/agent/agent.example.yaml
./cli/bin/sardctl version
```

## Лицензия

Sard распространяется под гибридной лицензией. Сервер, агент, CLI и веб-интерфейс — [AGPL-3.0-only](LICENSE): кто запускает изменённую версию как сервис, обязан открыть свои изменения. gRPC-контракты (`proto/`, включая сгенерированный код) и SDK плагинов (`agent/plugins/sdk`) — [Apache-2.0](proto/LICENSE), чтобы сторонние плагины, в том числе закрытые, можно было писать без обязательств AGPL. Внешние вклады принимаются после подписания [CLA](docs/legal/CLA.md) — подробности в [CONTRIBUTING.md](CONTRIBUTING.md) и [ADR 0004](docs/adr/0004-hybrid-license-and-cla.md).
