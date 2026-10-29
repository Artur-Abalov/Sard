<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 14. Мониторинг

Сервер отдаёт метрики для Prometheus: бэкапы идут, проверки восстановления проходят,
агенты на связи, сертификаты не истекают. Метрики — на отдельном порту, не на порту
консоли: `/actuator/health` и `/actuator/info` остаются на 8080, метрик на 8080 нет
ни по какому пути. Решения и причины — ADR
[00XX-draft-metrics-prometheus](../adr/00XX-draft-metrics-prometheus.md).

## Что включено по умолчанию

Слушатель метрик открыт **внутри контейнера** сервера на `127.0.0.1:8081`, без
аутентификации: достучаться до него может только сам контейнер. `docker-compose.yml`
этот порт не публикует. Проверка изнутри (в образе нет `curl`, используется bash):

```bash
cd ~/sard
docker compose exec server bash -c 'exec 3<>/dev/tcp/127.0.0.1/8081; printf "GET /metrics HTTP/1.0\r\n\r\n" >&3; head -n 12 <&3'
```

Ответ — `200` и текст в формате Prometheus 0.0.4 (`Content-Type: text/plain; version=0.0.4`).
Слушатель отдаёт только `GET /metrics`: другой путь — `404`, другой метод — `405`,
оба без тела.

## Переменные

| Переменная | Назначение | По умолчанию |
|---|---|---|
| `SARD_METRICS_BIND` | адрес слушателя внутри контейнера: IP-литерал IPv4 или IPv6, не имя хоста | `127.0.0.1` |
| `SARD_METRICS_PORT` | порт слушателя внутри контейнера, от 1 до 65535, но не 8080 и не 9090 | `8081` |
| `SARD_METRICS_TOKEN` | токен bearer, не короче 32 символов; пусто — токена нет | пусто |
| `SARD_METRICS_STATE_INTERVAL` | как часто сервер читает из базы состояние установки (последние успехи, агенты, очереди, сроки сертификатов) | `60s` |

Сервер не запустится, и сообщение в `docker compose logs server` назовёт переменную,
если: адрес не IP-литерал; порт вне диапазона или занят; токен короче 32 символов;
интервал не положителен; адрес не loopback (`127.0.0.0/8`, `::1`), включая `0.0.0.0`
и `::`, а токена нет — сообщение называет `SARD_METRICS_TOKEN` и `SARD_METRICS_BIND`.
Токен из одних пробелов считается незаданным. Если токен задан, он требуется на любом
адресе, в том числе на loopback; без токена на loopback слушатель отвечает всем.

Compose передаёт серверу все четыре переменные из `.env`; пример — в `.env.example`.

## Открыть метрики наружу

> **Предупреждение.** Порт метрик — обычный HTTP без TLS: токен и метрики идут по сети
> открытым текстом. Открывайте его только в своей сети, на конкретном адресе хоста, и
> только с токеном. Токен лежит в `.env` и виден каждому, кто может выполнить
> `docker inspect` на хосте сервера (в разделе `Env` контейнера): доступ к Docker на
> хосте равен доступу к токену. Чтения токена из файла (`SARD_METRICS_TOKEN_FILE`)
> пока нет.

1. Сгенерируйте токен и впишите переменные в `~/sard/.env`:

   ```bash
   cd ~/sard
   printf 'SARD_METRICS_BIND=0.0.0.0\nSARD_METRICS_TOKEN=%s\n' "$(openssl rand -hex 24)" >> .env
   ```

   `0.0.0.0` здесь — адрес **внутри контейнера**; наружу порт попадёт только через
   публикацию на шаге 2.

2. Опубликуйте порт на адресе хоста из вашей внутренней сети —
   `docker-compose.override.yml` рядом с `docker-compose.yml`:

   ```yaml
   services:
     server:
       ports:
         - "<10.0.0.5>:8081:8081"
   ```

   Не публикуйте порт на `0.0.0.0` хоста без межсетевого экрана. Если вы меняли
   `SARD_METRICS_PORT`, правая часть — это он.

3. Примените:

   ```bash
   docker compose up -d
   ```

4. Проверьте с машины, где стоит Prometheus (токен — из `.env`):

   ```bash
   curl -s -o /dev/null -w '%{http_code}\n' http://<10.0.0.5>:8081/metrics
   curl -s -H "Authorization: Bearer <токен>" http://<10.0.0.5>:8081/metrics | head -n 5
   ```

   Первая команда без токена отвечает `401`, вторая — метриками. Ответ `401` не
   содержит тела и подсказок; заголовок `WWW-Authenticate: Bearer`.

## Настройка Prometheus

Положите токен в файл, который читает только Prometheus, и укажите его в
`scrape_configs` — значения токена в конфиге нет:

```bash
install -m 600 /dev/null /etc/prometheus/sard-metrics-token
printf '%s' '<токен>' | sudo tee /etc/prometheus/sard-metrics-token > /dev/null
```

```yaml
scrape_configs:
  - job_name: sard
    metrics_path: /metrics
    scrape_interval: 30s
    authorization:
      type: Bearer
      credentials_file: /etc/prometheus/sard-metrics-token
    static_configs:
      - targets: ["<10.0.0.5>:8081"]
```

Состояние установки сервер обновляет раз в `SARD_METRICS_STATE_INTERVAL`, поэтому
опрашивать чаще минуты смысла нет. Scrape читает память и в базу не ходит: он
отвечает, даже когда база недоступна.

## Что в метриках

Все метрики Sard начинаются с `sard_`; кроме них в ответе метрики JVM (`jvm_`) и
процесса (`process_`). Метрик HTTP-запросов нет, чтобы в метках не оказалось путей.
Метки — только `source_id` (UUID источника), `role`, `kind`, `status`, `outcome`,
`health`, `channel`, `reason`; имён, путей, адресов, текстов ошибок и токена в метках
нет. Серий с `source_id` не больше двух на живой источник.

| Метрика | Что показывает |
|---|---|
| `sard_source_last_backup_success_timestamp_seconds{source_id}` | Unix-время последнего успешного бэкапа источника; `0` — успехов не было |
| `sard_source_last_verification_success_timestamp_seconds{source_id}` | то же для последней успешной проверки восстановления |
| `sard_run_duration_seconds{kind,status}` | гистограмма длительности запусков; `kind` — `backup`, `restore`, `verify`; `status` — `succeeded`, `failed`, `cancelled`; корзины 60 300 900 1800 3600 7200 14400 28800 86400 секунд |
| `sard_run_partial_total{reason}` | бэкапы, которые закончились ошибкой, но оставили свой снимок (неполный); `reason` — `unreadable_files`, `snapshot_set_incomplete`, `unknown`; три серии есть с нуля |
| `sard_agents{role}`, `sard_agents_online{role}` | агенты (не отозванные) и агенты на связи по роли `source` и `verifier` |
| `sard_runs_active{kind,status}` | активные запуски: `queued`, `dispatched`, `running` |
| `sard_verification_jobs{status}` | очередь проверок: `queued`, `running` |
| `sard_agent_certificate_next_expiry_timestamp_seconds` | ближайшее истечение текущего сертификата неотозванного агента; уже истёкший входит; метрики нет, если сертификатов нет |
| `sard_ca_certificate_expiry_timestamp_seconds` | срок CA сервера |
| `sard_self_backup_health{health}` | шесть серий, у текущего состояния `1`: `not_configured`, `ok`, `agent_replaced`, `overdue`, `failing`, `schedule_disabled` |
| `sard_self_backup_last_success_timestamp_seconds` | последний успех самобэкапа (новейший из двух системных источников); метрики нет, пока успехов не было |
| `sard_notify_deliveries_undelivered{status}` | доставки `failed` и `expired`, созданные за последние 24 часа |
| `sard_metrics_state_refresh_timestamp_seconds` | Unix-время последнего успешного чтения состояния |
| `sard_metrics_state_refresh_failures_total` | сколько чтений состояния не удалось |
| `sard_retention_deleted_total{kind}` | сколько строк удалила уборка ([раздел 15](15-data-retention.md)) |
| `sard_database_role_excess{reason}` | две серии, читаются при старте: `1`, если роль сервера в базе суперпользователь (`superuser`) или владелец схемы `sard` либо член её владельца (`schema_owner`); под `sard_app` обе `0` ([раздел 16](16-postgresql.md#роли)) |
| `sard_database_settings_mismatch{reason}` | пять серий, читаются при старте: `1`, если база сервера отличается от базы новой установки по `data_checksums`, `encoding`, `locale`, `timezone` или `version` ([раздел 16](16-postgresql.md)) |

Метрики событий (`sard_run_finished_total`, `sard_run_partial_total`, `sard_run_duration_seconds`,
`sard_notify_*`, `sard_scheduler_fires_total`, `sard_retention_deleted_total`)
обнуляются при перезапуске сервера — считайте их через `rate()` и `increase()`.
Метрики состояния появляются после первого успешного чтения (при старте оно идёт
сразу, в фоне); до него в ответе их нет.

### Неполные бэкапы

Неполный бэкап — это запуск в статусе `failed`, у которого есть снимок: часть данных
в него не попала или не сохранился связанный с ним снимок. Новых статусов запуска
нет, поэтому каждый такой запуск учтён и в `sard_run_finished_total` со
`status="failed"`, и в `sard_run_partial_total` с одной из причин:

- `unreadable_files` — restic не смог прочитать часть файлов источника (число и
  первые пути видны в карточке запуска; причину чаще всего решают права
  пользователя `sard-agent`);
- `snapshot_set_incomplete` — основной снимок сохранён полностью, но упал
  связанный с ним снимок отпечатка источника или глобальных объектов PostgreSQL;
- `unknown` — агент старше этой версии не сообщил, сколько файлов не прочитано.

`sard_run_finished_total` считает все запуски, а не только бэкапы. Разность
ниже — все неуспешные запуски (включая восстановления и проверки), кроме
неполных бэкапов, а не только бэкапы без снимка:

```promql
sum(sard_run_finished_total{status="failed"}) - sum(sard_run_partial_total)
```

Для окна времени оберните обе части в `increase(...[1d])`. Поздний результат
потерянного шага и отменённый запуск счётчик неполных не меняют.

### Когда база недоступна

Если чтение состояния не удалось, значения остаются прежними,
`sard_metrics_state_refresh_timestamp_seconds` не двигается, а
`sard_metrics_state_refresh_failures_total` растёт; в журнале сервера появляется
строка `WARN` с классом исключения, без SQL. Признак устаревания для алерта —
`time() - sard_metrics_state_refresh_timestamp_seconds`.

## Правила алертов

Пример правил; пороги подберите под свои расписания. Время в секундах: сутки — 86400.

```yaml
groups:
  - name: sard
    rules:
      - alert: SardBackupTooOld
        expr: >
          time() - sard_source_last_backup_success_timestamp_seconds > 2 * 86400
          and sard_source_last_backup_success_timestamp_seconds > 0
        for: 10m
        labels: {severity: critical}
        annotations:
          summary: "Источник {{ $labels.source_id }}: бэкапа не было больше 2 суток"
      - alert: SardSourceNeverBackedUp
        expr: sard_source_last_backup_success_timestamp_seconds == 0
        for: 1d
        labels: {severity: warning}
        annotations:
          summary: "Источник {{ $labels.source_id }}: успешных бэкапов ещё не было"
      - alert: SardVerificationTooOld
        expr: >
          time() - sard_source_last_verification_success_timestamp_seconds > 7 * 86400
          and sard_source_last_verification_success_timestamp_seconds > 0
        for: 10m
        labels: {severity: warning}
        annotations:
          summary: "Источник {{ $labels.source_id }}: проверка восстановления не проходила больше 7 суток"
      - alert: SardAgentCertificateExpires
        expr: sard_agent_certificate_next_expiry_timestamp_seconds - time() < 14 * 86400
        for: 10m
        labels: {severity: warning}
        annotations:
          summary: "Сертификат агента истекает меньше чем через 14 суток"
      - alert: SardCaExpires
        expr: sard_ca_certificate_expiry_timestamp_seconds - time() < 90 * 86400
        for: 10m
        labels: {severity: critical}
        annotations:
          summary: "Сертификат CA сервера истекает меньше чем через 90 суток"
      - alert: SardSelfBackupFailing
        expr: sard_self_backup_health{health=~"failing|overdue|agent_replaced"} == 1
        for: 10m
        labels: {severity: critical}
        annotations:
          summary: "Самобэкап сервера в ошибке: {{ $labels.health }}"
      - alert: SardAgentOffline
        expr: sard_agents_online{role="source"} < sard_agents{role="source"}
        for: 10m
        labels: {severity: warning}
        annotations:
          summary: "Агентов роли source на связи меньше, чем зарегистрировано"
      - alert: SardMetricsStateStale
        expr: time() - sard_metrics_state_refresh_timestamp_seconds > 300
        for: 5m
        labels: {severity: warning}
        annotations:
          summary: "Состояние метрик Sard не обновлялось больше 5 минут (база недоступна?)"
```

Алерт «агент офлайн» считается по роли: метки `agent_id` у метрик нет. Какой именно
агент отключён, видно в консоли.

Проверить файл правил можно `promtool check rules <rules.yml>`.
