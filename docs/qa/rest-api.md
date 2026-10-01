# QA: REST API этапа 1 (S8b)

Сценарии: `docs/specs/server/rest-api.feature` (утверждена владельцем
2026-10-01). Контракт — OpenAPI S8a (ADR 0019), экспорт `web/src/api/openapi.json`.

Ожидаемый результат указан после «→» в каждом шаге. Любое расхождение — дефект.

Управляемые часы, гонки и точные границы окон (три heartbeat-интервала,
окно дубликата) вручную не проверяются — это делают тесты сценариев. Здесь —
сквозной путь через настоящие сервер, базу и агента.

## Подготовка

Нужны: Docker, Go, `curl`, `jq`, `restic` (`.bin/restic` после `make tools`).
Команды — из корня репозитория, не от root.

```bash
make down; rm -f deploy/.env
cp deploy/.env.example deploy/.env
sed -i 's/^#\? *SARD_ADMIN_PASSWORD=.*/SARD_ADMIN_PASSWORD=qa-admin-password-2026/' deploy/.env
grep -q '^SARD_ADMIN_PASSWORD=' deploy/.env || echo 'SARD_ADMIN_PASSWORD=qa-admin-password-2026' >> deploy/.env
make up && make build
DC="docker compose -f deploy/docker-compose.yml --env-file deploy/.env"
PSQL="$DC exec -T postgres psql -U sard -d sard -At -c"
API=http://localhost:8080/api/v1
QA=$(mktemp -d); J=$QA/jar
curl -sS -o /dev/null -w '%{http_code}\n' -X POST "$API/session" -H 'Content-Type: application/json' \
  -d '{"password":"qa-admin-password-2026"}' -c $J
# a <метод> <путь> [тело] — запрос с сессией; печатает тело, затем строку "HTTP <код> <Content-Type>"
a() { curl -sS -X "$1" "$API$2" -b $J ${3:+-H 'Content-Type: application/json' -d "$3"} \
  -w '\nHTTP %{http_code} %{content_type}\n'; }
# n <метод> <путь> — то же без сессии
n() { curl -sS -X "$1" "$API$2" -w '\nHTTP %{http_code} %{content_type}\n'; }
body() { sed '$d'; }   # тело ответа a/n без последней строки
AG=$PWD/agent/bin/sard-agent
RESTIC=$PWD/.bin/restic
```

→ вход печатает `204`, `make build` завершается.

## Часть 1. Без сессии и Origin

1. `for p in agents enrollment-tokens sources runs; do n GET /$p | tail -1; done`
   → четыре строки `HTTP 401 application/problem+json`; тело каждого — `code` = `unauthenticated`.
2. `n POST /enrollment-tokens | tail -1; $PSQL "select count(*) from enrollment_tokens"`
   → `HTTP 401`; число токенов не изменилось.
3. `curl -sS -X POST "$API/enrollment-tokens" -b $J -H 'Origin: https://evil.example' -H 'Content-Type: application/json' -d '{}' -w '\n%{http_code}\n'`
   → `403`, `code` = `origin_rejected`; число токенов не изменилось.
4. `a GET /agents | tail -1` → `HTTP 200 application/json` (не 501).

## Часть 2. Токены

5. `a POST /enrollment-tokens '{}' | tee $QA/t1`
   → `HTTP 201`; `token` вида `sard_<43>.<64>`; `enrollCommand` =
   `sard-agent enroll --server localhost:9090 --token <token>`;
   `expiresAt` ≈ сейчас + 24 ч; `agentEndpointConfigured` = `false`.
6. `T1=$(body < $QA/t1 | jq -r .token); TID=$(body < $QA/t1 | jq -r .id)`;
   `a GET /enrollment-tokens | body | grep -cF "$T1"; a GET /enrollment-tokens/$TID | body | grep -cF "$T1"`
   → `0` и `0`; карточка — `status` `active`.
7. `a POST /enrollment-tokens '{"ttlSeconds":300}' | tail -1; a POST /enrollment-tokens '{"ttlSeconds":604800}' | tail -1`
   → оба `HTTP 201`.
8. `a POST /enrollment-tokens '{"ttlSeconds":299}'; a POST /enrollment-tokens '{"ttlSeconds":604801}'`
   → оба `HTTP 422`, `code` `validation_failed`, `errors[0].field` = `ttlSeconds`;
   новых токенов нет.
9. `a POST /enrollment-tokens "{\"label\":\"$(printf 'x%.0s' $(seq 200))\"}" | tail -1`
   → `HTTP 201`, в списке `label` из 200 символов; с 201 символом → `HTTP 422`, поле `label`;
   с `""` → `201`, `label` = `null`.
10. Отозвать: `a POST /enrollment-tokens/$TID/revoke` → `HTTP 200`, `status` `revoked`, `revokedAt` задан.
11. Повторно `a POST /enrollment-tokens/$TID/revoke` → `HTTP 200`, `revokedAt` тот же, что в шаге 10.
12. `a POST /enrollment-tokens/$(cat /proc/sys/kernel/random/uuid)/revoke` → `HTTP 404`, `code` `not_found`.
13. Истёкший: `T5=$(a POST /enrollment-tokens '{"ttlSeconds":300}' | body | jq -r .id)`; подождать 5 мин;
    `a POST /enrollment-tokens/$T5/revoke` → `HTTP 409`, `code` `token_expired`, `agentId` `null`.
14. `a GET '/enrollment-tokens?status=revoked' | body | jq '[.items[].status] | unique'` → `["revoked"]`.
15. `$DC logs server | grep -cF "$T1"` → `0`; то же для секрета (часть между `sard_` и `.`) → `0`.

## Часть 3. Агент: регистрация по токену из API, онлайн-статус

```bash
H=$QA/host; mkdir -p -m 0700 $H/tls $H/secrets
R=$QA/repo; head -c 32 /dev/urandom | base64 > $QA/repo.pass; chmod 0600 $QA/repo.pass
RESTIC_PASSWORD_FILE=$QA/repo.pass $RESTIC init -r $R
printf 'secret-value-QA-9d2e' > $H/secrets/db-password; chmod 0600 $H/secrets/db-password
cat > $H/agent.yaml <<EOF
server:
  address: localhost:9090
tls:
  ca_file: $H/tls/ca.pem
  cert_file: $H/tls/agent.pem
  key_file: $H/tls/agent.key
repositories:
  - name: qa
    url: $R
    password_file: $QA/repo.pass
secrets:
  db-password: $H/secrets/db-password
executor:
  state_dir: $H/state
EOF
CMD=$(a POST /enrollment-tokens '{}' | body | jq -r .enrollCommand)
$AG ${CMD#sard-agent } --config $H/agent.yaml
$AG --config $H/agent.yaml > $QA/agent.out 2>&1 & AGPID=$!
sleep 5; X=$(a GET /agents | body | jq -r '.items[0].id')
```

16. `a GET /agents/$X | body | jq '{status, agentVersion, os, arch, plugins: [.plugins[].name], repositories: [.repositories[].name], secretNames, revokedAt, duplicateSessionAt}'`
    → `status` `online`; версия, `linux`, архитектура хоста; `plugins` содержит `files`;
    `repositories` = `["qa"]`; `revokedAt` `null`; `duplicateSessionAt` `null`.
17. `a GET /agents/$X | body | grep -c secret-value-QA-9d2e` → `0`.
18. `a GET '/agents?status=online' | body | jq -r '.items[].id'` → содержит `$X`;
    `?status=offline` → не содержит.
19. `kill $AGPID; sleep 100; a GET /agents/$X | body | jq -r .status` → `offline`; `lastSeenAt` не `null`.
    Снова запустить агента (`$AG --config $H/agent.yaml > $QA/agent.out 2>&1 & AGPID=$!`), через 5 с → `online`.

## Часть 4. Источники

20. `a POST /sources "{\"name\":\"etc\",\"agentId\":\"$X\",\"plugin\":\"files\",\"repositoryName\":\"qa\",\"config\":{\"paths\":[\"$QA/tree\"]}}" | tee $QA/s1`
    (сначала `mkdir -p $QA/tree; echo hi > $QA/tree/a.txt`) → `HTTP 201`, тело повторяет вход и содержит `id`, `createdAt`, `updatedAt`.
    `S=$(body < $QA/s1 | jq -r .id)`.
21. То же с `"agentId":"<случайный uuid>"` → `HTTP 422`, `unknown_agent`, поле `agentId`;
    с `"plugin":"postgres"` → `unknown_plugin`, поле `plugin`;
    с `"repositoryName":"offsite"` → `unknown_repository`, поле `repositoryName`.
22. С `"config":{"paths":["etc"],"one_file_system":"yes"}`
    → `HTTP 422`, `invalid_config`, `errors[].field` = `config/one_file_system`, `config/paths/0`.
23. С `"config":{}` → `HTTP 422`, `invalid_config`; с `"config":{"paths":["/etc"],"compression":"max"}` → `HTTP 422`, `invalid_config`.
24. Повторить шаг 20 с тем же `name` → `HTTP 422`, `validation_failed`, поле `name`.
25. `a GET "/sources?agentId=$X" | body | jq -r '.items[].id'` → содержит `$S`.
26. `a PUT /sources/$S "{\"name\":\"etc\",\"agentId\":\"$X\",\"plugin\":\"files\",\"repositoryName\":\"qa\",\"config\":{\"paths\":[\"$QA/tree\"],\"exclude\":[\"*.log\"]}}"`
    → `HTTP 200`, `updatedAt` позже `createdAt`, `exclude` сохранён.
27. Источник с вложенными путями: `paths` = `["$QA/tree", "$QA/tree/sub"]` → `HTTP 201` (сервер это не проверяет); его запуск в части 5 даёт шаг `rejected` с сообщением агента.
28. `$DC logs server | grep -cF "$QA/tree"` → `0` (значения конфигов не в логах).

## Часть 5. Запуски, шаги, логи, снимки

29. `a POST /sources/$S/runs | tee $QA/r1` → `HTTP 201`, `status` `queued`, `trigger` `manual`, один шаг `ordinal` 0, `action` `backup`.
    `RUN=$(body < $QA/r1 | jq -r .id); STEP=$(body < $QA/r1 | jq -r '.steps[0].id')`.
30. Сразу же `a POST /sources/$S/runs` → `HTTP 409`, `run_active`, `activeRunId` = `$RUN`
    (если запуск уже успел закончиться — повторить с остановленным агентом, шаг 33).
31. Дождаться: `until a GET /runs/$RUN | body | jq -e '.status|IN("succeeded","failed")'; do sleep 2; done`
    → `succeeded`; у шага `backup.snapshotId` = последний снимок `restic -r $R snapshots --json`,
    `backup.repositoryId` = `id` из `restic -r $R cat config`.
32. `a GET "/runs/$RUN/steps/$STEP/logs?limit=1" | body | jq '{n: (.items|length), nextAfterSeq, hasMore, truncated}'`
    → `n` 1, `nextAfterSeq` = `seq` строки, `hasMore` `true`; `truncated` `false`;
    `?afterSeq=<последний seq>` → `items` пуст, `hasMore` `false`.
33. Остановить агента (`kill $AGPID`), `a POST /sources/$S/runs` → `HTTP 201`, `queued`;
    через 60 с всё ещё `queued`; запустить агента → запуск доходит до `succeeded`.
34. `a GET /sources/$S/snapshots | body | jq '.items[0] | {snapshotId, repositoryName, repositoryId, totalBytes, addedBytes, createdAt}'`
    → снимок из шага 33 первым; `repositoryName` `qa`, `repositoryId` как в шаге 31.
35. `a GET "/runs?sourceId=$S&status=succeeded" | body | jq '.items|length'` → `2`;
    `a GET '/runs?status=failed&status=succeeded'` → только эти два статуса.
36. `a GET "/runs?queuedFrom=$(date -u -d '-1 min' +%FT%TZ)"` → только запуски последней минуты.
37. Неуспешный бэкап со снимком: `chmod 000 $QA/tree/a.txt; echo b > $QA/tree/b.txt`; запустить `$S`
    → шаг `failed`, `message` называет нечитаемый файл; `backup.snapshotId` задан;
    снимок с этим `snapshotId` есть в `GET /sources/$S/snapshots`, `partial` `true`. Вернуть `chmod 644`.
38. Запуск источника из шага 27 → шаг `rejected`, `message` — текст агента; запуск `failed`.
39. Во время долгого бэкапа (дерево из 50 000 файлов) `a GET /runs/<id>` несколько раз
    → `phase` `uploading`, растущие `bytesProcessed`, `filesProcessed`; `filesTotal` не `null` к концу.
40. Удаление: во время идущего запуска `a DELETE /sources/$S` → `HTTP 409`, `run_active`; после
    завершения → `HTTP 204`; `a GET /sources/$S` → `404`; `a GET "/runs?sourceId=$S"` — запуски на месте;
    `a GET /sources/$S/snapshots` → `HTTP 200`, снимки на месте.
41. Новый источник `etc` с тем же именем → `HTTP 201`.

## Часть 6. Пометка дубликата

42. Клон: `cp -a $H $QA/clone; sed -i "s|$H|$QA/clone|g" $QA/clone/agent.yaml; $AG --config $QA/clone/agent.yaml > $QA/clone.out 2>&1 & CLPID=$!`;
    подождать 90 с → в `$QA/clone.out` отказ `AGENT_DUPLICATE_SESSION`;
    `a GET /agents/$X | body | jq -r .duplicateSessionAt` — время, не `null`; то же в `GET /agents`.
43. `kill $CLPID; $DC restart server`; дождаться `healthy`; войти снова (Подготовка, `curl … /session`)
    → `duplicateSessionAt` агента `$X` прежний.

## Часть 7. Отзыв агента

44. Поставить долгий запуск источника агента `$X` (большое дерево) и, пока он `running`:
    `a POST /agents/$X/revoke` → `HTTP 200`, `revokedAt` задан, `status` `offline`.
    В `$QA/agent.out` в течение 2 с — стрим закрыт `UNAUTHENTICATED`, причина `AGENT_REVOKED`
    (не через минуту периодической проверки).
45. `$PSQL "select count(*) from agent_certificates where agent_id='$X' and revoked_at is null"` → `0`.
46. Агент пытается переподключиться → в логе агента `UNAUTHENTICATED`, `CERT_REVOKED`; `status` остаётся `offline`.
47. Шаг запуска из шага 44 → `lost`, `message` `agent revoked`; запуск `failed`.
48. Повторно `a POST /agents/$X/revoke` → `HTTP 200`, `revokedAt` прежний.
49. `a POST /sources/<источник агента X>/runs` → `HTTP 409`, `code` `agent_revoked`;
    создание источника с `agentId` `$X` → `HTTP 422`, `agent_revoked`.
50. `a GET /agents` → `$X` в списке с `revokedAt`; `GET /runs?agentId=$X`, карточки и логи,
    `GET /sources/<id>/snapshots` → `200` с прежними данными.

## Часть 8. Пагинация, неверные параметры, отказ базы

51. Создать 60 токенов (`for i in $(seq 60); do a POST /enrollment-tokens '{}' >/dev/null; done`);
    `a GET '/enrollment-tokens?limit=50'` → 50 элементов и `nextCursor`; следующая страница с этим `cursor`
    → остаток, `nextCursor` `null`; объединение страниц без повторов, число = всего токенов.
52. `a GET '/agents?limit=0'`, `?limit=201`, `?cursor=garbage`, `/runs?status=done`
    → каждый `HTTP 422`, `validation_failed`, поле — имя параметра. `a GET /sources/not-a-uuid` → `HTTP 404`.
53. `$DC stop postgres; a GET /agents` → `HTTP 503`, `code` `unavailable`, тело без
    текста исключения; `$DC start postgres`, дождаться `healthy` → `HTTP 200`.

## Часть 9. Консоль и e2e

54. `cd web && npm run dev`, войти, пройти страницы агентов, токенов, источников и запусков
    против сервера → те же данные, что в ответах API выше; ошибок типов в консоли браузера нет.
55. `make openapi && git diff --exit-code web/src/api` → без изменений (экспорт совпадает с контрактом).
56. `make e2e` → зелёный; `grep -rn "INSERT INTO enrollment_tokens" test/e2e/src` → пусто.
