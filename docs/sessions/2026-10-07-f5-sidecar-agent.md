<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# Сессия 2026-10-07: агент-сосед для самобэкапа (F5)

Ветка `ccr-0643b936-tocsl7` (в задаче — `feat/f5-sidecar-agent`; работа идёт в
назначенной), база — `main` @ `8870781`.

## Фаза 1: исследование

Всё ниже — прочитано в репозитории (file:line), не проверено прогоном, если не
сказано иное.

### Что есть сейчас

- **Образ агента не публикуется.** `release.yml` (job `image`) пушит только
  `ghcr.io/<owner>/sard-server`, amd64 + arm64. Образ агента есть только в e2e
  (`test/e2e/agent/Dockerfile`): уже не distroless, а `ubuntu:24.04` +
  `openssh-client` (ADR 0047), uid/gid 65532, `sard-agent` и `restic` из
  распакованного tar.gz пакета (`make package`), ничего не компилируется.
- **Подписи образа нет.** Образ сервера не подписан (cosign отвергнут, ADR 0043);
  подписан minisign только `SHA256SUMS` пакетов, которые лежат в образе.
  «Подпись как у сервера» для образа агента = собрать его из тех же подписанных
  пакетов, проверив `SHA256SUMS.minisig` при сборке.
- **pg_dump в образе агента нет.** Для самобэкапа базы (F6, плагин — F1) нужен
  клиент PostgreSQL не старше сервера (в compose — `postgres:18-alpine`).
- **Каталог CA:** `/var/lib/sard/pki/ca/{ca.crt,ca.key}`, каталоги 0700, файлы
  0600, владелец uid 10001; с более широкими правами сервер не стартует
  (`pki/CaDirectory.kt:31-49`). Значит, сосед читает CA, только работая под
  uid 10001, либо правило прав меняется.
- **Сертификат сервера** — имена из `SARD_PKI_SERVER_NAMES`, по умолчанию
  `localhost,127.0.0.1,::1`. Сосед, звонящий на `server:9090` по сети compose,
  не пройдёт проверку имени.
- **Токены (S2):** `enrollment/EnrollmentTokens.kt` — 32 байта секрета, в базе
  только SHA-256, TTL 5 мин…7 дней, одноразовость — условный UPDATE в
  `Enrollment.kt:145-165`; токен несёт отпечаток CA. Логирования токена нет,
  защита — `toString()`. Стартовых хуков, выпускающих что-либо, нет
  (`SmartLifecycle` для фоновых задач).
- **Агенты:** таблица `agents` без признака «встроенный». Удаления агента нет
  вообще (только `POST /agents/{id}/revoke`, `fleet/Agents.kt:139-168`).
  Повторный `enroll` без `--force` — отказ, exit 4 (ADR 0023).
- **sard-agent enroll** уже умеет `--token-file` (`enroll_flags.go:47-118`).
  `sard-agent` без сертификата завершается с exit 1, не ждёт.
- **agent.d (A8)** в `main` нет.
- **Роль PostgreSQL:** `POSTGRES_USER=sard` в compose — суперпользователь.
  `docker-entrypoint-initdb.d` выполняется только на пустом томе — для
  обновления с 0.0.1-rc1 не годится. Миграция Flyway с `CREATE ROLE` упадёт на
  внешнем PostgreSQL, где `sard` без `CREATEROLE`.
- **0.0.1-rc1:** тег `v0.0.1-rc1` есть; compose отличается от текущего на две
  строки, тома те же (`sard_postgres-data`, `sard_sard-pki`).
- D8, D14, D17 в `docs/` не записаны.

### Предложения на контрольную точку 1

См. ответ владельцу в сессии; решения будут записаны ниже.

## Решения владельца (контрольная точка 1)

1. Ключ CA у соседа — риск принят сейчас; в F6 — крупное предупреждение.
2. Сосед — под uid сервера 10001, том CA `:ro`.
3. Канал токена — общий том сервера и соседа, файл 0600, токен вида `self`.
4. Роль PostgreSQL — отдельной миграцией; скрипт — только если миграция не
   выйдет.
5. Пароль роли — файловый секрет, который получает только сосед (вариант а).
6. Ограничение D8 — в F6, не в F5.
7. После отзыва `sard-self` — повторная регистрация автоматически; отзыв —
   с отдельным подтверждением.
8. Имя сервиса compose — в сертификате сервера.
9. Клиента PostgreSQL в образе нет.
10. Один образ агента для поставки и e2e.
11. Конфиг соседа — в образе, том конфига заполняется из него.
12. Обновление проверять с образа 0.0.1-rc1 из ghcr, иначе — из тега.

## Фаза 2: образ, compose, роль

Окружение: `dockerd` вручную, базовые образы — с `mirror.gcr.io`. Сборка jar в
Docker упала на 429 от Maven Central. jar собран на хосте
(`./gradlew :server:bootJar`) и передан в образ через `make server-image`, как
в CI (ADR 0045). Версия образов — `0.0.0-f5`.

### Сделано

- `deploy/agent/Dockerfile` — один образ агента: перенесён из `test/e2e/agent/`.
  - Контекст — каталог `make package`. Ставка `unpack` проверяет `SHA256SUMS`
    и распаковывает tar.gz архитектуры цели.
  - Добавлен пользователь `sard-self` (10001), конфиг `/etc/sard/self/agent.yaml`
    и каталоги состояния.
  - `Makefile` (`agent_image`) собирает из каталога tar.gz без распаковки.
- `release.yml`, job `image`: сборка и публикация `sard-agent` amd64 и arm64
  из проверенного `dist`, затем проверка `--version` на обеих архитектурах.
  `scripts/offline-archive.sh` добавляет образ агента в офлайн-архив.
- `deploy/docker-compose.yml`:
  - сервис `self-agent` (hostname `sard-self`, uid 10001, `read_only`,
    `cap_drop: ALL`, `no-new-privileges`);
  - тома `sard-self-config`, `sard-self-state`, `sard-pki:…:ro`;
  - `,server` в `SARD_PKI_SERVER_NAMES`.
  - `docker-compose.build.yml` собирает `self-agent` из `dist/`.
- Миграция `V202610071200__self_dump_role.sql`, тест
  `SelfDumpRoleIntegrationTest` (сначала красный: роли нет).

### Проверки (прогон)

| Что | Результат |
|---|---|
| Сборка образа агента, amd64 | собран; `sard-agent 0.0.0-f5`, `restic 0.19.1` |
| Подменённый tar.gz | сборка падает: `sard-agent_0.0.0-f5_linux_amd64.tar.gz: FAILED` |
| arm64 локально | **не проверено**: в этой машине нет binfmt/QEMU; в release.yml — `setup-qemu-action` |
| `make e2e-agent-images` | оба образа агента собраны; образ SFTP стенда упал на CA прокси в apt — окружение, не изменение |
| `SelfDumpRoleIntegrationTest` | 6/6 зелёные |
| Проверка 1 (частично): чистый `docker compose up` | postgres и server healthy; `self-agent` перезапускается: без сертификата агент выходит с кодом 1 — самостоятельная регистрация в фазе 3 |
| Проверка 3: CA в соседе | uid 10001 читает `ca.key`; `touch`, дописать, `rm` в `ca/` → `Read-only file system`; корневая ФС тоже только на чтение |
| Проверка 3: роль на стенде (пароль задан вручную на время пробы) | `select` — да; после `set default_transaction_read_only=off`: INSERT/UPDATE/DELETE/TRUNCATE → `permission denied for table`, CREATE TABLE → `permission denied for schema public`, CREATE TEMP → `permission denied to create temporary tables`, `lo_create` → `permission denied for function` |

### Найдено: файловый секрет Compose не годится для пароля роли

Compose вне Swarm монтирует файловый секрет как bind mount с владельцем и
правами файла хоста. `uid`, `gid` и `mode` в длинной записи он молча
игнорирует. Проба: файл 0600 root на хосте, контейнер под 10001 с `uid: "10001"`,
`mode: 0400` → `-rw------- 0 0`, `Permission denied`.

Агент требует, чтобы файл секрета принадлежал ему и был закрыт для группы и
остальных (`agent/internal/secrets`). Значит, решение 5(а) работает, только
если на хосте сделать `chown 10001` (нужен root) или открыть файл всем на
чтение. Вопрос — владельцу.

### Состояние ветки после фазы 2

- `./gradlew :server:spotlessCheck :server:detekt :server:test` — exit 0,
  1372 теста, 0 упавших. Первый прогон дал 3 падения:
  - `SardServerIntegrationTest` — список миграций, исправлен;
  - два `BindException` в `EnrollmentTokenSchemaIntegrationTest` — порт 9090
    держал мой стенд compose. После его остановки тест зелёный.
- `license-check` — OK.
- **Ветку нельзя выпускать до фазы 3.** `docker compose up --wait` с этим
  compose не дождётся `self-agent`: без самостоятельной регистрации агент
  без сертификата выходит, и контейнер перезапускается.
- `make e2e-test` в этой фазе не запускался (проверка — в фазе 3).
