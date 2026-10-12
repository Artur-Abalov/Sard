# Sard · [English version](README.md)

Sard — консоль резервного копирования для разнородной инфраструктуры малого
и среднего бизнеса, которую обслуживают интеграторы и MSP; его цель — бэкапы,
восстановление из которых доказано.

> Статус: **ранняя разработка, предварительные релизы.** Восстановления из
> консоли и автоматической проверки восстановления пока нет; см.
> [Чего пока нет](#чего-пока-нет). Для промышленной эксплуатации не готов.

## Что умеет сейчас

- Бэкап файлов и каталогов ([docs/plugins/files.md](docs/plugins/files.md)).
- Бэкап PostgreSQL: `pg_dump` базы в формате custom и роли и табличные
  пространства кластера ([docs/plugins/postgresql.md](docs/plugins/postgresql.md)).
- Хранение бэкапов через [restic](https://restic.net) в S3-совместимом
  хранилище, по SFTP или в локальном каталоге; дедупликацию и шифрование
  делает restic ([docs/operator/05a-storage.md](docs/operator/05a-storage.md)).
- Запуск бэкапов по расписанию cron в выбранном часовом поясе, с одним
  догоняющим запуском после простоя, или вручную из консоли
  ([docs/operations/scheduler.md](docs/operations/scheduler.md)).
- Уведомления о запусках и пропущенных срабатываниях расписания в Telegram
  ([docs/operations/notifications.md](docs/operations/notifications.md)).
- Установка агентов с сервера: deb, rpm и tar.gz для Linux amd64 и arm64,
  подписанные релизы ([docs/operator/05-agents.md](docs/operator/05-agents.md)).
- Самобэкап: каждую ночь база сервера, ключи CA и конфигурация установки
  уходят через встроенного агента в выбранный репозиторий, а консоль
  предупреждает, когда этот бэкап просрочен или падает
  ([docs/operator/06-data-and-backup.md](docs/operator/06-data-and-backup.md)).
  О просрочке любых источников сообщается и в Telegram.

### Чего пока нет

Восстановления из консоли (сейчас восстановление — ручной `restic restore`,
см. [docs/demo.md](docs/demo.md)), проверки восстановления, других источников (MySQL, сетевые устройства, 1С), больше одного
тенанта, команд `sardctl`, кроме `version`.

## Как устроено

```text
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
           │   sard-agent    │────────────▶│  S3 / SFTP / локально│
           │  Go, на хосте   │             └──────────────────────┘
           └─────────────────┘
```

На каждом хосте работает агент; он сам подключается к серверу по gRPC с
взаимным TLS, поэтому входящие порты на хостах не открываются. Данные бэкапа
идут от агента прямо в хранилище, минуя сервер. Репозитории, ключи хранилища
и скрипты описываются на хосте агента, а сервер ссылается на них только по
имени: ключей хранилища у сервера нет
([ADR 0008](docs/adr/0008-crypto-provider.md)).

- Плагины источников реализуют один интерфейс: prepare → dump → stream → verify
  ([ADR 0027](docs/adr/0027-plugin-sdk.md)).
- Корпоративные модули подключаются как Spring Boot starters через
  `server/.../extension`; открытое ядро работает без них.

## Быстрый старт: сервер за 10 минут

Нужен Linux-хост (amd64 или arm64) с Docker Engine и плагином Compose, `curl`
и `openssl`. Команды ниже запускают sard-server и PostgreSQL из
опубликованного образа релиза `SARD_TAG`
([релизы](https://github.com/Artur-Abalov/Sard/releases)); чтобы поставить
другой релиз, поменяйте только эту строку.

<!-- quickstart:begin -->
```bash
SARD_TAG=v0.0.1-rc.1
mkdir -p ~/sard && cd ~/sard
curl -fsSLO "https://github.com/Artur-Abalov/Sard/releases/download/$SARD_TAG/docker-compose.yml"
curl -fsSL -o .env "https://github.com/Artur-Abalov/Sard/releases/download/$SARD_TAG/sard.env.example"
sed -i -e "s/^SARD_DB_PASSWORD=.*/SARD_DB_PASSWORD=$(openssl rand -hex 24)/" .env
sudo chgrp 10001 .env && chmod 640 .env
docker compose up -d --wait
curl -fsS http://localhost:8080/api/v1/status
```
<!-- quickstart:end -->

Последняя команда печатает версию сервера. Администратора ещё нет: пароль
спрашивает первый запуск в консоли. Сервер печатает в журнал одноразовый код
настройки; найдите его командой

```bash
docker compose logs server | grep "SARD SETUP CODE"
```

и откройте страницу консоли `/setup`, <http://localhost:8080/setup> (консоль
сама переходит туда). Введите код, посмотрите на CA сервера (сохраните его
ключ: без него все агенты регистрируются заново) и задайте пароль
администратора. Код действует 24 часа; если он истёк,
`docker compose restart server` напечатает новый. Пароль потом меняется на
странице «Настройки» консоли; если он утерян, восстановление описано в
[docs/operator/10-security.md](docs/operator/10-security.md). REST API слушает только
`127.0.0.1:8080`, потому что это обычный HTTP; порт агентов 9090 (gRPC,
взаимный TLS) открыт в сеть. Прежде чем регистрировать агентов с других
хостов, впишите имя, по которому они подключаются, в `SARD_PKI_SERVER_NAMES` в
`.env` и снова выполните `docker compose up -d`: см. комментарии в `.env` и
руководство по эксплуатации [docs/operator/](docs/operator/README.md). Всё демо
этапа 1, от чистой ВМ до восстановленного файла, — [docs/demo.md](docs/demo.md).
К каждому релизу приложен офлайн-архив образов (sard-server, sard-agent,
PostgreSQL) для хостов без доступа к реестру:

```bash
gunzip -c sard-<VERSION>-images-linux-<ARCH>.tar.gz | docker load
docker compose up -d --wait --pull never
```

Рядом с сервером работает `self-agent` — агент `sard-self`, который
регистрируется сам; установку он пока не бэкапит. Базу он читает через роль
только для чтения, каталог CA — тоже только на чтение
([docs/operations/self-agent.md](docs/operations/self-agent.md)). Пока бэкапьте
сервер вручную: [docs/operator/06-data-and-backup.md](docs/operator/06-data-and-backup.md).
Ключ CA лежит в томе `sard_sard-pki`, база — в `sard_postgres-data`; оба
переживают `docker compose down` и удаляются только `docker compose down -v`.

### Проверка релизов

Релизы агента (tar.gz, deb, rpm для linux/amd64 и arm64) подписаны ключом
релизов Sard, [minisign](https://jedisct1.github.io/minisign/) (Ed25519),
поверх `SHA256SUMS`. Берите ключ из этого репозитория, а не с сервера, с
которого скачан пакет: `deploy/release/sard-release.pub`.

| | |
|---|---|
| Key ID | `DF5D5B6DB257DBFA` |
| Публичный ключ | `RWT621eybVtd38CL7B33xZrcc8ArYiPt3GlKXyJuk9ZQzDnoUV+kLi2d` |

```bash
minisign -Vm SHA256SUMS -p sard-release.pub && sha256sum -c --ignore-missing SHA256SUMS
```

Подробности и процесс выпуска: [docs/release.md](docs/release.md).

## Документация

- [docs/demo.md](docs/demo.md): демо этапа 1 по шагам: сервер, агент, бэкап, Telegram, восстановление
- [docs/operator/](docs/operator/README.md): эксплуатация сервера: требования, установка, настройка, TLS, бэкап, обновление, диагностика
- [docs/plugins/](docs/plugins/): плагины источников и их настройки
- [docs/adr/](docs/adr/README.md): архитектурные решения
- [docs/specs/](docs/specs/): спецификации Gherkin по модулям
- [docs/sessions/](docs/sessions/): журнал рабочих сессий
- [docs/dependencies.md](docs/dependencies.md): сторонние зависимости и их лицензии
- [CLAUDE.md](CLAUDE.md): конституция проекта и критерии готовности

## Лицензия

У Sard гибридная лицензия. Сервер, агент, CLI и веб-интерфейс —
[AGPL-3.0-only](LICENSE): тот, кто предоставляет изменённую версию как сервис,
обязан опубликовать изменения. gRPC-контракты (`proto/`, включая
сгенерированный код) и SDK плагинов (`agent/plugins/sdk`) —
[Apache-2.0](proto/LICENSE), поэтому сторонние плагины, в том числе
проприетарные, можно писать без обязательств AGPL. Внешний вклад принимается
после подписания [CLA](docs/legal/CLA.md); см.
[ADR 0004](docs/adr/0004-hybrid-license-and-cla.md).

## Как сообщить об уязвимости

Не открывайте публичный issue. Сообщите приватно, как описано в
[SECURITY.md](SECURITY.md).

## Как участвовать

Начните с [CONTRIBUTING.md](CONTRIBUTING.md): CLA, шлюз качества, язык
коммитов и pull request.

### Структура репозитория

| Каталог | Что это |
|---|---|
| `server/` | `sard-server`: Kotlin, Spring Boot 4.1, PostgreSQL, Flyway, gRPC |
| `agent/` | `sard-agent`: Go, один бинарный файл; `agent/plugins/sdk` — SDK плагинов |
| `cli/` | `sardctl`: Go, один бинарный файл |
| `proto/` | gRPC-контракты сервер ↔ агент (buf); `proto/gen/go` — сгенерированный код Go, `proto/jvm` — привязки JVM |
| `web/` | SPA: React, TypeScript, Vite, Mantine |
| `deploy/` | docker-compose, Dockerfile сервера, unit systemd агента |
| `examples/workflows/` | примеры YAML-workflow |
| `tools/` | инструменты разработки (CRAP, закреплённые Go-инструменты) |
| `test/e2e/` | сквозные тесты на собранных образах |
| `docs/` | ADR, журнал сессий, зависимости, спецификации, CLA |

### Требования

JDK 25, Go 1.27, Node.js 24, Docker (для тестов сервера на Testcontainers и
`make up`), GNU make. Ставить buf, golangci-lint и другие Go-инструменты не
нужно: `make tools` собирает закреплённые версии в `.bin/`.

Имена тестовых классов Kotlin содержат кириллицу, поэтому Gradle нужна
локаль UTF-8. `make` сам задаёт `LC_ALL=C.UTF-8` для своих gradle-целей; при
вызове `./gradlew` или `scripts/gate.sh` напрямую сначала выполните
`export LC_ALL=C.UTF-8` (см. [CONTRIBUTING.md](CONTRIBUTING.md)).

`.claude/hooks/session-start.sh` выполняется в начале каждой сессии Claude
Code on the web. Он ставит JDK 25 и Node.js 24, собирает закреплённые
Go-инструменты, скачивает зависимости Go и npm и прогревает кэш Gradle, так
что тесты, линтеры и шлюз качества работают сразу. Вне удалённого окружения
он ничего не делает.

### Сборка и проверка

```bash
make proto    # сгенерировать Go-код из proto/ (результат коммитится)
make build    # agent/bin/sard-agent, cli/bin/sardctl, server/build/libs/sard-server.jar, web/dist/
make test     # все наборы тестов
make lint     # SPDX-заголовки, buf lint, gofmt/vet/golangci-lint, spotless/detekt, oxlint/prettier/tsc
make gate     # полный шлюз качества: покрытие ≥ 80%, CRAP ≤ 6, сложность ≤ 8, мутационное тестирование
make gate M=server   # один модуль: proto gen sdk tools agent cli server web
make e2e      # собрать образы и прогнать сквозные тесты (нужен Docker)
```

Пороги заданы в одном месте, `scripts/gate.sh`. Pull request с красным
шлюзом не принимается.

### Запуск из исходников

```bash
make up                                   # PostgreSQL + sard-server, собранный из этого checkout (deploy/.env создаётся из .env.example)
curl -s localhost:8080/api/v1/status      # {"version":"…","lastVerifiedRestoreAt":null}
# саму консоль отдаёт сервер на http://localhost:8080 (она в образе)
cd web && npm install && npm run dev      # разработка: консоль на http://localhost:5173, API проксируется на :8080
make down
```

Пароль администратора спрашивает первый запуск в консоли: `make up` пароля не
пишет. Сервер печатает в журнал одноразовый код настройки
(`docker compose -f deploy/docker-compose.yml logs server | grep "SARD SETUP CODE"`);
откройте `/setup`, введите код, подтвердите CA и задайте пароль (от 12 до 1024
символов). Он хранится в базе как хеш Argon2id и меняется на странице
«Настройки» ([ADR 0021](docs/adr/0021-admin-password-login.md),
[ADR первого запуска](docs/adr/00XX-draft-f4a-first-start.md)). После этого
консоль открывается на странице входа; кнопка выхода — в шапке.

За обратным прокси, который завершает TLS (Cloudflare Tunnel, nginx, Caddy),
задайте `SARD_FORWARD_HEADERS=native` в `deploy/.env` и оставьте порт 8080 на
loopback; без этого вход и любой изменяющий запрос получают 403
`origin_rejected` ([ADR 0046](docs/adr/0046-reverse-proxy-forward-headers.md),
[docs/operator/04-tls-and-names.md](docs/operator/04-tls-and-names.md)).

Агент и CLI:

```bash
./agent/bin/sard-agent --version
./agent/bin/sard-agent --config deploy/agent/agent.example.yaml
./cli/bin/sardctl version
```
