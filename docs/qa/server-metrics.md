# QA: метрики Prometheus сервера Sard и документация оператора (H1, фаза 3)

Сценарии: `docs/specs/server/server-metrics.feature` (утверждена
владельцем 2026-10-21, ответы ОВ1–ОВ10 в шапке). Шаги с пометкой «(ОВ…)»
проверяют соответствующий ответ владельца. Часы здесь не подменяются (ОВ8):
возраст данных задаётся сдвигом времени в базе, фейковые часы — только в
тестах `@server`.

Здесь вручную проходятся сценарии с тегом `@qa` и сквозной путь «стенд →
события → scrape → Prometheus». Кэш по тику, счётчики SQL, гонки, объём,
фейковые часы, сертификаты по всем тенантам и архитектурные правила
проверяют тесты `@server`, `@http`, `@startup`, `@arch`, `@doc` с теми же
названиями.

Ожидаемый результат указан после «→» в каждом шаге. Любое расхождение — дефект.

## Подготовка

Нужны: Docker, Go, `restic` 0.19.1, `curl`, `jq`, `openssl`. Команды — из
корня репозитория.

1. Выполнить «Подготовку» и часть 2 (до шага с первым бэкапом) из
   `docs/qa/files-plugin.md` (сервер, агент на хосте с `AGPID`, репозиторий
   `qa`, переменные `AG`, `H`, `QA`, `DC`, `PSQL`, `API`, `AGENT`, `D`,
   функции `src`, `run`; сессия в `$QA/jf`).
2. Вспомогательные функции:

```bash
# mloop [путь] — GET на слушатель метрик изнутри контейнера (в образе нет curl), без токена
mloop() { $DC exec -T server bash -c "exec 3<>/dev/tcp/127.0.0.1/8081 && printf 'GET ${1:-/metrics} HTTP/1.0\r\n\r\n' >&3 && cat <&3"; }
# m [токен] — scrape с хоста через опубликованный порт; печатает тело
m() { curl -sS -H "Authorization: Bearer ${1:-$TOK}" http://127.0.0.1:8081/metrics; }
# mv <серия> — значение одной серии, например mv 'sard_agents_online{role="source"}'
mv() { m | grep -F "$1 " | awk '{print $NF}'; }
code() { curl -sS -o /dev/null -w '%{http_code}\n' "$@"; }
TOK=$(openssl rand -hex 24)            # 48 символов
```

   → `echo ${#TOK}` печатает `48`.

## Часть 1. По умолчанию метрик снаружи нет

3. `$DC up -d --wait server` с `.env` без переменных `SARD_METRICS_*`
   → сервер `healthy`.
4. `code http://127.0.0.1:8081/metrics`
   → `curl` сообщает об отказе соединения, код `000` (порт не опубликован).
5. `mloop | head -1; mloop | grep -c '^sard_agents_connected'`
   → `HTTP/1.1 200` и `1` (на loopback внутри контейнера без токена — ОВ2).
6. **Основной порт, все варианты пути.** Для каждого пути
   `/actuator/prometheus`, `/actuator/metrics`, `/metrics`, `/metrics/`,
   `//actuator/prometheus`, `/actuator//prometheus`, `/;x/actuator/prometheus`,
   `/actuator;x/prometheus`, `/actuator/%70rometheus`, `/ACTUATOR/PROMETHEUS`:

```bash
for p in /actuator/prometheus /actuator/metrics /metrics /metrics/ //actuator/prometheus \
  /actuator//prometheus '/;x/actuator/prometheus' '/actuator;x/prometheus' \
  /actuator/%70rometheus /ACTUATOR/PROMETHEUS; do
  printf '%s ' "$p"; curl -sS --path-as-is "http://127.0.0.1:8080$p" | grep -cE '^(sard_|# TYPE)'; done
```

   → для каждого пути `0`.
7. То же с сессией администратора (`curl -b $QA/jf …`) для
   `/actuator/prometheus` и `/metrics`
   → `0` и `0`.
8. `curl -sS http://127.0.0.1:8080/actuator | jq -r '._links | keys[]' | sort | tr '\n' ' '`
   → `health health-path info self`.
9. `curl -sS http://127.0.0.1:8080/actuator/health`
   → `{"status":"UP"}`; `$DC ps server` → `healthy` (HEALTHCHECK образа не
   изменился).
10. `grep -n 8081 deploy/docker-compose.yml`
    → нет совпадений в разделе `ports`.

## Часть 2. Включение наружу и токен

11. **Без токена наружу — отказ старта.**

```bash
cat > $QA/override.yml <<'EOF'
services:
  server:
    environment:
      SARD_METRICS_BIND: 0.0.0.0
    ports:
      - "127.0.0.1:8081:8081"
EOF
DC0="$DC"; DC="$DC -f $QA/override.yml"
$DC up -d server; sleep 20; $DC ps server
$DC logs server | grep -E 'SARD_METRICS_TOKEN' | tail -1
```

    → контейнер не `healthy` (перезапускается или вышел); последняя строка
    называет `SARD_METRICS_TOKEN` и `SARD_METRICS_BIND`.
12. (ОВ7) Добавить `SARD_METRICS_TOKEN: short-token-123` под `environment`;
    `$DC up -d server; sleep 20; $DC logs server | grep -c short-token-123`
    → `0`; сервер не `healthy`; в логе строка, называющая `SARD_METRICS_TOKEN`.
13. Заменить значение на `$TOK`
    (`sed -i "s/short-token-123/$TOK/" $QA/override.yml`);
    `$DC up -d --wait server`
    → сервер `healthy`.
14. `code http://127.0.0.1:8081/metrics; code -H 'Authorization: Bearer wrong' http://127.0.0.1:8081/metrics; code -H "Authorization: Basic $(printf ':%s' $TOK | base64 -w0)" http://127.0.0.1:8081/metrics`
    → `401`, `401`, `401`.
15. `curl -sS -D - -H 'Authorization: Bearer wrong' http://127.0.0.1:8081/metrics`
    → статус `401`, заголовок `WWW-Authenticate: Bearer`, тело пустое
    (`Content-Length: 0` или его нет и тела нет).
16. `curl -sS -D - -o /dev/null -H "Authorization: Bearer $TOK" http://127.0.0.1:8081/metrics | grep -i content-type`
    → `text/plain` с `version=0.0.4`.
17. `for p in / /actuator/health /api/v1/status /index.html /metrics/x; do code -H "Authorization: Bearer $TOK" http://127.0.0.1:8081$p; done`
    → пять раз `404`.
18. `code -X POST -H "Authorization: Bearer $TOK" http://127.0.0.1:8081/metrics`
    → `405`.
19. **Токен нигде не виден.**
    `$DC logs server | grep -c "$TOK"; m | grep -c "$TOK"; for e in env configprops prometheus; do code http://127.0.0.1:8080/actuator/$e; done`
    → `0`, `0`, затем три раза `404`.
20. `curl -sS -H "Authorization: Bearer $TOK" http://127.0.0.1:8080/actuator/prometheus | grep -c '^sard_'`
    → `0` (токен не открывает метрики на основном порту).
21. **Через обратный прокси консоли.** Включить `SARD_FORWARD_HEADERS: native`
    в `$QA/override.yml`, `$DC up -d --wait server`;
    `curl -sS --path-as-is -H 'X-Forwarded-Proto: https' -H 'X-Forwarded-For: 203.0.113.7' -H 'X-Forwarded-Port: 8081' -H 'X-Forwarded-Prefix: /metrics' http://127.0.0.1:8080/actuator/prometheus | grep -cE '^(sard_|# TYPE)'`
    → `0`. Убрать `SARD_FORWARD_HEADERS` и добавить
    `SARD_METRICS_STATE_INTERVAL: 10s`; `$DC up -d --wait server`.

## Часть 3. Метрики меняются по событиям

22. Существующие метрики на месте:
    `m | grep -oE '^sard_[a-z_]+' | sort -u`
    → среди строк есть `sard_agents_connected`,
    `sard_agent_duplicate_sessions_total`, `sard_run_steps_queued`,
    `sard_notify_pending`, `sard_scheduler_lag_seconds`,
    `sard_retention_deleted_total`.
23. **Бэкап.**

```bash
B0=$(mv 'sard_run_duration_seconds_count{kind="backup",status="succeeded"}')
SRC=$(src "{\"paths\":[\"$D\"]}"); run $SRC >/dev/null
FIN=$(curl -sS -b $QA/jf $API/runs/$RUN | jq -r .finishedAt)
mv 'sard_run_duration_seconds_count{kind="backup",status="succeeded"}'
```

    → значение на 1 больше `$B0` сразу, без ожидания тика.
24. `sleep 11; mv "sard_source_last_backup_success_timestamp_seconds{source_id=\"$SRC\"}"`
    → равно `date -d "$FIN" +%s` (с точностью до секунды; запись в
    экспоненциальной форме допустима).
25. **Неуспешный бэкап.**
    `FSRC=$(src "{\"paths\":[\"$QA/missing\"]}"); run $FSRC >/dev/null; sleep 11`
    → `mv 'sard_run_duration_seconds_count{kind="backup",status="failed"}'`
    вырос на 1; `mv "sard_source_last_backup_success_timestamp_seconds{source_id=\"$FSRC\"}"`
    → `0` (ОВ4).
26. **Проверка восстановления.** Если на стенде есть агент роли verifier и
    проверка V4 запущена для `$SRC` (см. `docs/qa/` фичи V4), после её
    прохода и `sleep 11`:
    `mv "sard_source_last_verification_success_timestamp_seconds{source_id=\"$SRC\"}"`
    → равно `date -d "$($PSQL "select max(verified_at) from restore_verifications where source_id='$SRC' and status='passed'") " +%s`.
    Без верификатора шаг пропускается, это записывается в отчёт QA.
27. **Агент офлайн.** `ON=$(mv 'sard_agents_online{role="source"}'); kill $AGPID; sleep 120`
    (больше трёх интервалов heartbeat и одного тика)
    → `mv 'sard_agents_online{role="source"}'` на 1 меньше `$ON`;
    `mv 'sard_agents{role="source"}'` не изменился.
28. Запустить агента снова (команда из шага 3 `docs/qa/files-plugin.md`,
    обновить `AGPID`); `sleep 15`
    → `sard_agents_online{role="source"}` снова равен `$ON`.
29. **Сертификаты и самобэкап.**
    `mv sard_ca_certificate_expiry_timestamp_seconds; mv sard_agent_certificate_next_expiry_timestamp_seconds; m | grep '^sard_self_backup_health'`
    → срок CA — Unix-время `not_after` из
    `$DC exec -T server openssl x509 -enddate -noout -in /var/lib/sard/pki/ca/ca.crt`
    (если в образе нет `openssl` — скопировать файл `docker cp` и выполнить на
    хосте); ближайшее истечение равно
    `date -d "$($PSQL "select min(c.not_after) from agent_certificates c join agents a on a.id=c.agent_id where c.revoked_at is null and a.revoked_at is null")" +%s`
    или больше него (запрос учитывает и вытесненные продлением сертификаты,
    метрика — нет), и не больше `not_after` текущего сертификата агента
    `$AGENT`;
    ровно одна строка `sard_self_backup_health{…}` со значением `1`, всего
    шесть строк.
30. **Метки без имён и текстов.**

```bash
HOSTN=$($PSQL "select hostname from agents where id='$AGENT'")
NAME=$(curl -sS -b $QA/jf $API/sources/$SRC | jq -r .name)
MSG=$(curl -sS -b $QA/jf "$API/sources/$FSRC/runs" | jq -r '.items[0].message // empty')
m > $QA/expo.txt
for s in "$HOSTN" "$NAME" "$D" "$QA/missing" "$MSG" "$TOK"; do [ -n "$s" ] && grep -cF "$s" $QA/expo.txt; done
grep -oE '[a-z_]+="' $QA/expo.txt | grep -v '^le="\|^quantile="' | sort -u
```

    → каждое число `0`; имена меток — только из `source_id`, `role`, `kind`,
    `status`, `outcome`, `health`, `channel`, `reason`, а также меток
    `jvm_`/`process_` (`area`, `id`, `gc`, `state` и т. п.), ни одной с адресом,
    путём или текстом.
31. **Удалённый источник исчезает.**
    `curl -sS -b $QA/jf -X DELETE $API/sources/$FSRC -o /dev/null -w '%{http_code}\n'; sleep 11; m | grep -c "$FSRC"`
    → код `204` (или `200`), затем `0`.

## Часть 4. Сбой базы

32. `V=$(mv "sard_source_last_backup_success_timestamp_seconds{source_id=\"$SRC\"}"); R0=$(mv sard_metrics_state_refresh_timestamp_seconds); F0=$(mv sard_metrics_state_refresh_failures_total); $DC stop postgres; sleep 25`
33. `time m > /dev/null`
    → ответ за меньше 1 секунды, `curl` без ошибки.
34. `mv "sard_source_last_backup_success_timestamp_seconds{source_id=\"$SRC\"}"; mv sard_metrics_state_refresh_timestamp_seconds; mv sard_metrics_state_refresh_failures_total`
    → первое равно `$V`; второе не больше `$R0` плюс 10; третье больше `$F0`.
35. `$DC logs server --since 30s | grep -i metrics | grep WARN | tail -1`
    → строка WARN с классом исключения; в ней нет `select`, нет `$SRC`.
36. `$DC start postgres; sleep 25; mv sard_metrics_state_refresh_timestamp_seconds; date +%s`
    → значение отличается от текущего времени не больше чем на 10 секунд.

## Часть 5. Prometheus по документации

37. Взять из `docs/operator/14-monitoring.md` блок `scrape_configs` как есть,
    записать в `$QA/prometheus.yml`, заменив только адрес цели на
    `127.0.0.1:8081` и путь файла токена на `/etc/prometheus/token`;
    `printf '%s' "$TOK" > $QA/token`.
38. Взять блок правил алертов в `$QA/rules.yml`;
    `docker run --rm -v $QA/rules.yml:/r.yml --entrypoint promtool prom/prometheus check rules /r.yml`
    → `SUCCESS`, число правил не меньше 7.
39. Порт 9090 хоста занят агентским портом Sard, поэтому Prometheus — на 9091:
    `docker run -d --name qa-prom --network host -v $QA/prometheus.yml:/etc/prometheus/prometheus.yml -v $QA/token:/etc/prometheus/token -v $QA/rules.yml:/etc/prometheus/rules.yml prom/prometheus --config.file=/etc/prometheus/prometheus.yml --web.listen-address=:9091; sleep 30;
    curl -sS 'http://127.0.0.1:9091/api/v1/targets' | jq -r '.data.activeTargets[].health'`
    → `up`.
40. `curl -sS 'http://127.0.0.1:9091/api/v1/query?query=sard_agents_online' | jq '.data.result | length'`
    → `2` (роли `source` и `verifier`). `docker rm -f qa-prom`.

## Часть 6. Документация

41. `docs/operator/README.md` → в таблице разделов строки 14 «Мониторинг» и
    15 «Хранение данных сервера», ссылки открываются.
42. `docs/operator/14-monitoring.md` → описаны `SARD_METRICS_BIND`,
    `SARD_METRICS_PORT`, `SARD_METRICS_TOKEN`, `SARD_METRICS_STATE_INTERVAL`;
    предупреждение, что порт — HTTP без TLS; пример
    `docker-compose.override.yml`; токен в примере Prometheus — через файл,
    значение не записано; таблица метрик совпадает с шапкой спецификации.
43. `docs/operator/15-data-retention.md` → каждое свойство `sard.retention.*`
    с умолчанием и переменной `SARD_RETENTION_*`; что не удаляется никогда и
    почему; ограничения R2, R3, В9; требование к L1.
44. `docs/operator/10-security.md` → в «Что открыто наружу» строка порта
    метрик; `docs/operator/03-configuration.md` и `deploy/.env.example` →
    четыре переменные метрик, в `.env.example` закомментированы.
45. `./gradlew :server:test --tests '*DocsTest*'`
    → зелёный, включая `FirstStartDocsTest` и `OnboardingDocsTest`.

## Уборка

46. `DC="$DC0"; $DC up -d --wait server; rm -f $QA/override.yml $QA/token`
    → сервер `healthy`, порт 8081 с хоста снова недоступен (шаг 4).

## Ответы владельца

ОВ1–ОВ10 — в шапке `docs/specs/server/server-metrics.feature` (2026-10-21,
приняты все рекомендации specifier).
