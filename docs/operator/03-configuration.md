# 3. Справочник конфигурации

Все настройки — переменные окружения в `~/sard/.env`. После правки:
`docker compose up -d` (пересоздаёт только изменившиеся контейнеры). Сервер
проверяет значения при старте и с неверным значением не запускается, называя
переменную в логе (`docker compose logs server`). Источник истины —
`server/src/main/resources/application.yaml` и `deploy/docker-compose.yml`.

## Переменные compose

Читает только `docker compose`, сервер их не видит.

| Переменная | Назначение | По умолчанию | Пример |
|---|---|---|---|
| `SARD_VERSION` | версия образа сервера: тег без `v` | обязательна, в `sard.env.example` релиза уже стоит | `0.0.1-rc.1` |
| `SARD_IMAGE` | репозиторий образа (зеркало, локальный образ) | `ghcr.io/artur-abalov/sard-server` | `registry.local/sard-server` |
| `SARD_POSTGRES_IMAGE` | образ PostgreSQL ([раздел 16](16-postgresql.md)); офлайн-установка указывает тег из архива, без `@sha256:…` ([раздел 11](11-offline.md)) | `postgres:18.6-alpine@sha256:898c60cb390d1085bc97bdd6a1ae968757421df394b071848b52b1c09c89d6a5` | `postgres:18.6-alpine` |
| `SARD_HTTP_PUBLISH` | где слушает HTTP-порт на хосте, `[адрес:]порт` | `127.0.0.1:8080` | `0.0.0.0:8080` |
| `SARD_GRPC_PUBLISH` | где слушает порт агентов на хосте, `[адрес:]порт` | `9090` (все адреса) | `0.0.0.0:9090` |

## Переменные сервера

| Переменная | Назначение | По умолчанию | Пример |
|---|---|---|---|
| `SARD_DB_URL` | JDBC-адрес базы; в compose задан | `jdbc:postgresql://localhost:5432/sard` | `jdbc:postgresql://postgres:5432/sard` |
| `SARD_DB_USER` | роль базы, под которой работает сервер; в compose задана; только чтение и запись строк ([раздел 16](16-postgresql.md#роли)) | `sard_app` | `sard_app` |
| `SARD_DB_PASSWORD_FILE` | файл с паролем этой роли; в compose это файл тома, который `db-init` создаёт при первом запуске, в `.env` его не задают | пусто | `/run/sard-db/password` |
| `SARD_DB_PASSWORD` | то же паролем в переменной: для внешнего кластера, где файл неудобен; если задан `SARD_DB_PASSWORD_FILE`, берётся файл ([раздел 16](16-postgresql.md#внешний-кластер)) | пусто | |
| `SARD_PKI_SERVER_NAMES` | DNS-имена и IP через запятую, на которые выпускается сертификат порта агентов; каждое — имя хоста или IP-литерал | `localhost,127.0.0.1,::1` | `sard.example.com,203.0.113.10` |
| `SARD_AGENT_ENDPOINT` | адрес `хост[:порт]`, который агенты набирают; хост обязан быть одним из `SARD_PKI_SERVER_NAMES`, иначе сервер не стартует; попадает в команду регистрации в консоли | первое имя из `SARD_PKI_SERVER_NAMES` и порт 9090 | `sard.example.com:9090` |
| `SARD_AGENT_HEARTBEAT_INTERVAL` | интервал heartbeat агентов (передаётся им при подключении); агент считается отключённым после 3 интервалов, шаг — потерянным после 2 | `30s` | `15s` |
| `SARD_TELEGRAM_BOT_TOKEN` | токен бота Telegram; секрет, в логи и базу не попадает | пусто | `123456:ABC…` |
| `SARD_TELEGRAM_CHAT_ID` | чат, куда бот пишет; задаются обе переменные или ни одной, иначе сервер не стартует; обе пустые — уведомления выключены, в логе предупреждение | пусто | `-1001234567890` |
| `SARD_CONSOLE_PUBLIC_URL` | адрес, по которому открывают консоль; уведомление заканчивается ссылкой `<адрес>/runs/<id>`; абсолютный `http`/`https` без запроса и фрагмента, иначе сервер не стартует; из заголовков запроса не берётся | пусто (без ссылки) | `https://sard.example.com` |
| `SARD_NOTIFY_LANGUAGE` | язык уведомлений: `en` или `ru`, иначе сервер не стартует | `en` | `ru` |
| `SARD_METRICS_BIND` | адрес слушателя метрик внутри контейнера, IP-литерал; не loopback — только с `SARD_METRICS_TOKEN`, иначе сервер не стартует ([раздел 14](14-monitoring.md)) | `127.0.0.1` | `0.0.0.0` |
| `SARD_METRICS_PORT` | порт слушателя метрик внутри контейнера, 1–65535, не 8080 и не 9090; compose его не публикует | `8081` | `8081` |
| `SARD_METRICS_TOKEN` | токен bearer слушателя метрик, не короче 32 символов; секрет, в логи и метки не попадает; пусто — без токена (только loopback) | пусто | `openssl rand -hex 24` |
| `SARD_METRICS_STATE_INTERVAL` | как часто читается состояние установки для метрик | `60s` | `30s` |
| `TZ` | часовой пояс сервера (IANA, например `Europe/Berlin`): в нём читается расписание без своего пояса, и консоль называет его поясом по умолчанию; не идентификатор IANA (например, смещение `+03:00`) читается как UTC | `UTC` | `Europe/Berlin` |
| `SARD_AGENT_DOWNLOADS` | раздача пакетов агента по `/downloads/agent/` без сессии; `false` — путь отвечает 404 | `true` | `false` |
| `SARD_AGENT_DOWNLOADS_URL` | адрес, с которого хосты скачивают пакеты в командах установки консоли; абсолютный `http`/`https` без запроса и фрагмента | пусто: `http://<хост из SARD_AGENT_ENDPOINT>:8080` | `https://sard.example.com` |
| `SARD_FORWARD_HEADERS` | `native` — сервер за обратным прокси, завершающим TLS: схема и адрес клиента берутся из `X-Forwarded-Proto` и `X-Forwarded-For` прокси с loopback или частного адреса ([раздел 4](04-tls-and-names.md), ADR 0046); `none` — эти заголовки игнорируются; другие значения не поддерживаются | `none` | `native` |
| `SARD_AGENT_PACKAGES_DIR` | каталог пакетов агента внутри контейнера; менять не нужно | `/usr/share/sard/agent-packages` | — |
| `SARD_PKI_IMPORT_DIR` | каталог с CA старого сервера (`ca/ca.crt`, `ca/ca.key`, права только владельцу), который сервер принимает при первом старте, пока в `SARD_PKI_DIR` нет CA; пусто — сервер создаёт новый CA. Монтируется через `docker-compose.override.yml`, [раздел 8](08-migrate-and-remove.md) | пусто | `/var/lib/sard/pki-import` |
| `SARD_PKI_AGENT_CERTIFICATE_VALIDITY` | срок сертификата агента при регистрации и продлении (ISO 8601); не меньше `PT2M`, меньше или неразбираемое значение — сервер не стартует; сертификат обрезается по сроку CA. Compose пробрасывает из `.env`. [Раздел 13](13-agent-certificates.md) | `P365D` | `P90D` |
| `SARD_PKI_AGENT_RENEWAL_MIN_INTERVAL` | не чаще одного выпуска сертификата на агента за этот срок (действует меньшее из него и 1/10 срока сертификата), лишний запрос получает `RENEWAL_TOO_FREQUENT`; больше нуля. Compose пробрасывает из `.env` | `PT1H` | `PT30M` |
| `SARD_PKI_AGENT_CERTIFICATE_CHECK_INTERVAL` | как часто сервер проверяет сроки сертификатов агентов для уведомлений «скоро истечёт» и «истёк»; больше нуля. Compose пробрасывает из `.env` | `PT1H` | `PT15M` |
| `SARD_PKI_DIR` | каталог CA внутри контейнера; в compose на нём том `sard_sard-pki` | `/var/lib/sard/pki` | — |
| `SARD_HTTP_PORT` | HTTP-порт внутри контейнера | `8080` | — |
| `SARD_GRPC_PORT` | порт агентов внутри контейнера | `9090` | — |

Последние четыре — внутренние: в compose их не трогают, иначе перестанут
совпадать тома, порты и healthcheck.

## Тонкие настройки

Свойства без собственной переменной задаются по правилам Spring Boot: имя
свойства в верхнем регистре, точки и дефисы — подчёркивания
(`sard.agent.stream.offline-after-heartbeats` →
`SARD_AGENT_STREAM_OFFLINEAFTERHEARTBEATS`). Нужны редко; значения по
умолчанию и проверки — в `application.yaml`:

| Свойство | По умолчанию | Смысл |
|---|---|---|
| `sard.agent.stream.offline-after-heartbeats` | `3` | агент «не в сети» после стольких пропущенных heartbeat |
| `sard.agent.stream.clock-skew-threshold` | `30s` | порог предупреждения о расхождении часов агента |
| `sard.run.dispatch.lost-after-heartbeats` | `2` | шаг «потерян», если агент молчит столько интервалов |
| `sard.run.dispatch.start-grace` (`SARD_RUN_START_GRACE`) | `2m` | после старта сервера шаг в полёте не становится «потерянным» раньше, чем через эту отсрочку или окно потери (что длиннее): агент успевает переподключиться. Положительная длительность, иначе сервер не стартует |
| `sard.logs.max-line-bytes` | `8192` | предел строки журнала шага |
| `sard.logs.max-step-bytes` | `16777216` | предел журнала одного шага (16 МиБ) |
| `sard.notify.max-attempts` | `8` | попыток доставки уведомления |
| `sard.notify.ttl` | `24h` | сколько уведомление ждёт доставки |
| `sard.retention.step-logs` | `30d` | срок журналов шагов, переменная `SARD_RETENTION_STEPLOGS` ([раздел 15](15-data-retention.md)) |
| `sard.retention.runs` | `180d` | срок запусков, `SARD_RETENTION_RUNS` |
| `sard.retention.verifications` | `180d` | срок записей проверок, `SARD_RETENTION_VERIFICATIONS` |
| `sard.retention.schedule-fires` | `180d` | срок журнала срабатываний, `SARD_RETENTION_SCHEDULEFIRES` |
| `sard.retention.notification-deliveries` | `30d` | срок доставок, `SARD_RETENTION_NOTIFICATIONDELIVERIES` |
| `sard.retention.interval` | `1h` | как часто идёт уборка, `SARD_RETENTION_INTERVAL` |
| `sard.retention.batch-size` | `500` | строк в пачке удаления, `SARD_RETENTION_BATCHSIZE` |

Для этих переменных compose ничего не пробрасывает: добавьте строку в секцию
`environment` сервера в `docker-compose.yml` (и повторите правку после
обновления файла из нового релиза).

## Пароль администратора

Пароль администратора — не настройка: его задают в мастере первого запуска и меняют
на странице «Настройки» ([раздел 2](02-install.md), [раздел 10](10-security.md)).
Сервер не читает его из окружения.
