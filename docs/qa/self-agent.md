# QA: встроенный агент sard-self — самостоятельная регистрация (F5, фаза 3)

Сценарии: `docs/specs/server/self-agent.feature`,
`docs/specs/agent/self-agent.feature`, `docs/specs/web/self-agent.feature`
(решения владельца, включая О1–О4, переданы координатором 2026-10-08). Решения
владельца и specifier — в заголовке серверной спецификации. Контекст — `docs/adr/00XX-draft-self-agent.md`,
`docs/sessions/2026-10-07-f5-sidecar-agent.md`.

Ожидаемый результат указан после «→» в каждом шаге. Любое расхождение — дефект.

Управляемые часы, границы срока и запаса встроенного токена, гонки и сбои базы
внутри проверки вручную не воспроизводятся — это делают тесты сценариев
`@service`, `@startup`, `@grpc`, `@fake`, `@local`. Здесь — сквозной путь через
настоящие compose, сервер, базу и соседа: проверки задачи 1, 2, 4, 5 (проверка 3
сделана в фазе 2) и консоль. Проверка 5 — обновление с выпущенной 0.1.0-beta.N,
не с 0.0.1-rc1 (изменено F4a, часть 5).

## Подготовка

Нужны: Docker с compose v2, Go, `curl`, `jq`, `openssl`, `python3`. Команды — из
корня репозитория, не от root. Образы `sard-server` и `sard-agent` текущей
сборки — `make up` (собирает их через `deploy/docker-compose.build.yml`).

```bash
DC="docker compose -f deploy/docker-compose.yml --env-file deploy/.env"
$DC down -v                                   # тома установки удаляются: чистый старт
rm -f deploy/.env; cp deploy/.env.example deploy/.env
PSQL="$DC exec -T postgres psql -U sard -d sard -At -c"
API=http://localhost:8080/api/v1
QA=$(mktemp -d); J=$QA/jar
login() { curl -sS -o /dev/null -w '%{http_code}\n' -X POST "$API/session" \
  -H 'Content-Type: application/json' -d '{"password":"qa-admin-password-2026"}' -c $J; }
# a <метод> <путь> [тело] — запрос с сессией; печатает тело, затем "HTTP <код> <Content-Type>"
a() { curl -sS -X "$1" "$API$2" -b $J ${3:+-H 'Content-Type: application/json' -d "$3"} \
  -w '\nHTTP %{http_code} %{content_type}\n'; }
body() { sed '$d'; }
builtin() { a GET /agents | body | jq -c '[.items[] | select(.builtin) | {id, hostname, status, revokedAt}]'; }
btokens() { $PSQL "select count(*) from enrollment_tokens where builtin"; }
chan() { $DC exec -T server ls -la /var/lib/sard/self; }
```

## Часть 1. Чистая установка (проверка 1)

1. `time make up` (или `$DC up --wait` после сборки образов)
   → завершается с кодом 0 не позднее чем через 3 минуты; `$DC ps` — `postgres`,
   `server`, `self-agent` в состоянии `running`, у `postgres` и `server` —
   `healthy`.
2. До мастера в канале нет токена (`chan` — только `db-password`) и сосед ждёт. Мастер
   по коду из лога (F4a; шаг CA выдаёт встроенный токен): `source scripts/lib/setup-wizard.sh;
   sard_complete_wizard http://localhost:8080 qa-admin-password-2026 $DC logs server`
   → код возврата 0; `login` → `204`.
3. `builtin` (повторять до 3 минут после шага 2)
   → ровно один элемент: `hostname` = `sard-self`, `status` = `online`,
   `revokedAt` = `null`. Запомнить `X=<id>`.
4. `a GET /agents | body | jq '[.items[] | {hostname, builtin}]'`
   → у каждого агента есть поле `builtin`; `true` только у `sard-self`.
5. `a GET /agents/$X | body | jq '{builtin, secretNames}'`
   → `builtin: true`, `secretNames` содержит `sard-db`.
6. `$DC logs self-agent | grep -c 'waiting for the enrollment token in /run/sard-self/enroll-token'`
   → `0` или `1` (не больше одной строки за запуск контейнера).
7. `chan`
   → в каталоге только `db-password` (`-rw-------`, владелец uid 10001);
   файла `enroll-token` нет; каталог `drwx------`, владелец uid 10001.
8. `$DC exec -T server sh -c 'wc -c < /var/lib/sard/self/db-password; grep -Ec "^[0-9a-f]{64}$" /var/lib/sard/self/db-password'`
   → `64` и `1`.
9. `P=$($DC exec -T server cat /var/lib/sard/self/db-password); $DC exec -T -e PGPASSWORD="$P" postgres psql -h localhost -U sard_self -d sard -At -c 'select 1'`
   → `1`.
10. `$DC exec -T -e PGPASSWORD=wrong postgres psql -h localhost -U sard_self -d sard -At -c 'select 1'`
    → `password authentication failed for user "sard_self"`.
11. `$PSQL "select left(rolpassword, 14) from pg_authid where rolname = 'sard_self'"`
    → `SCRAM-SHA-256$`.
12. `$DC logs | grep -cF "$P"` → `0`.
13. `$PSQL "select count(*) from agents where builtin and revoked_at is null"` → `1`;
    `btokens` → `1`; `$PSQL "select count(*) from enrollment_tokens where builtin and used_at is not null"` → `1`.

13а. (О4) `a GET /overview | body | jq '{agentsOnline, agentsTotal, t: .firstSteps.tokenIssued, a: .firstSteps.agentConnected}'`
     → `agentsOnline: 1`, `agentsTotal: 1`, `t: false` (обычных токенов ещё нет),
     `a: true`.

## Часть 2. Пересоздание контейнера соседа (проверка 2)

14. `B=$(btokens); $DC up -d --force-recreate self-agent`; повторять `builtin` до 1 минуты
    → тот же единственный элемент `id` = `$X`, `status` = `online`.
15. `btokens` → равно `$B`; `chan` → `enroll-token` нет.
16. `$DC rm -sf self-agent && $DC up -d self-agent`; повторять `builtin` до 1 минуты
    → `id` = `$X`, `status` = `online`; `btokens` → `$B`.
17. `$DC restart server`; дождаться `healthy`; `login`; через 30 с `builtin`
    → `id` = `$X`; `btokens` → `$B`; `chan` → `enroll-token` нет;
    `$DC exec -T server cat /var/lib/sard/self/db-password` → равно `$P` (пароль
    переживает перезапуск); шаг 9 снова → `1`.

## Часть 3. Встроенный токен и канал (проверка 4)

Перехват токена до регистрации: на чистой установке остановить соседа до
первого старта и прочитать файл со стороны сервера (так видит его только тот,
у кого есть доступ к тому канала).

18. `$DC down -v; $DC up -d --wait postgres server; login`
    → сервер `healthy`; `chan` → есть `enroll-token`, `-rw-------`, uid 10001.
19. `S=$($DC exec -T server cat /var/lib/sard/self/enroll-token); echo "${#S}"; printf %s "$S" | grep -Ec '^sard_[A-Za-z0-9_-]{43}\.[0-9a-f]{64}$'`
    → `113` и `1`.
20. `$DC logs server | grep -F 'built-in agent enrollment token written to /var/lib/sard/self/enroll-token' | wc -l`
    → не меньше `1`; `$DC logs server | grep -cF "$S"` → `0`; то же для секрета
    (`${S:5:43}`) → `0`.
21. `a GET /enrollment-tokens | body | jq '.items | length'` → `0` (встроенного токена в списке нет);
    `a GET /enrollment-tokens | body | grep -cF "${S:5:43}"` → `0`.
22. `TID=$($PSQL "select id from enrollment_tokens where builtin and revoked_at is null and used_at is null")`;
    `a GET /enrollment-tokens/$TID | tail -1` → `HTTP 404 application/problem+json`;
    `a POST /enrollment-tokens/$TID/revoke | tail -1` → `HTTP 404 application/problem+json`;
    `$PSQL "select revoked_at is null from enrollment_tokens where id = '$TID'"` → `t`.
23. `a POST /enrollment-tokens '{"builtin":true}'; btokens` → встроенных токенов по-прежнему `1`.

23а. (О2) Файл токена пропал: `docker run --rm -u 10001 -v sard_sard-self-channel:/c alpine rm /c/enroll-token`;
     подождать 20 с (больше интервала проверки 15 с)
     → `chan` — `enroll-token` снова есть; `S2=$($DC exec -T server cat /var/lib/sard/self/enroll-token)` отличается от `$S`;
     `$PSQL "select revoked_at is not null from enrollment_tokens where id = '$TID'"` → `t`;
     `btokens` → на 1 больше, чем в шаге 23; пригодный встроенный токен один:
     `$PSQL "select count(*) from enrollment_tokens where builtin and used_at is null and revoked_at is null"` → `1`.
23б. (О2) Файл с чужой строкой: `docker run --rm -u 10001 -v sard_sard-self-channel:/c alpine sh -c 'printf garbage > /c/enroll-token'`;
     подождать 20 с → файл снова содержит строку токена формата шага 19, отличную от `$S2`;
     прежний токен отозван (как в 23а). Далее `S=$($DC exec -T server cat /var/lib/sard/self/enroll-token)` —
     строка, по которой сосед зарегистрируется в шаге 24.
24. `$DC up -d --wait self-agent`; повторять `builtin` до 2 минут → один элемент,
    `online`. `chan` → `enroll-token` нет (удалён не позднее 15 с после регистрации).
25. Попытка с «другого хоста»: собрать агента (`make build`), конфиг во временном
    каталоге с `server.address: localhost:9090` (как в `docs/qa/agent-enroll.md`,
    «Подготовка», шаг 2), затем
    `agent/bin/sard-agent enroll --config "$H/agent.yaml" --token "$S"; echo "exit=$?"`
    → `exit=3`, сообщение называет `TOKEN_USED`; `a GET /agents | body | jq '.items | length'` не изменилось.
26. `$DC exec -T self-agent sh -c 'touch /run/sard-self/x'` → `Read-only file system`;
    `$DC exec -T self-agent sh -c 'echo >> /run/sard-self/db-password'` → `Read-only file system`;
    `$DC exec -T self-agent sh -c 'rm /run/sard-self/db-password'` → `Read-only file system`;
    `$DC exec -T self-agent sh -c 'touch /var/lib/sard/pki/ca/x'` → `Read-only file system`.
27. `$DC config --format json | jq -c '[.services | to_entries[] | {svc: .key, m: [.value.volumes[]? | select(.source == "sard-self-channel") | {target, read_only}]} | select(.m | length > 0)]'`
    → ровно `server` (target `/var/lib/sard/self`, без `read_only`) и
    `self-agent` (target `/run/sard-self`, `read_only: true`); у `postgres` тома нет.
28. `$DC config --format json | jq -r '.services["self-agent"].command | join(" ")'`
    → `--config /etc/sard/self/agent.yaml --enroll-token-file /run/sard-self/enroll-token`.
29. `$DC logs | grep -cF "$S"` → `0`; `$DC logs | grep -cF "$($DC exec -T server cat /var/lib/sard/self/db-password)"` → `0`.

## Часть 4. Отзыв встроенного агента и повторная регистрация

30. `X=$(builtin | jq -r '.[0].id'); a POST /agents/$X/revoke | tail -2`
    → тело Problem с `code` = `self_agent_confirmation_required`, `HTTP 409 application/problem+json`;
    `builtin` → `X` по-прежнему `online`, `revokedAt` = `null`.
31. `a POST "/agents/$X/revoke?confirm=SARD-SELF" | tail -1` и `a POST "/agents/$X/revoke?confirm=yes" | tail -1`
    → оба `HTTP 409 application/problem+json`; `X` не отозван.
32. `a POST "/agents/$X/revoke?confirm=sard-self" | body | jq '{revokedAt, status, builtin}'`
    → `revokedAt` не `null`, `status` = `offline`, `builtin` = `true`.
33. Повторять `a GET /agents | body | jq -c '[.items[] | select(.builtin) | {id, status, revokedAt}]'` до 2 минут
    → два элемента: `X` с `revokedAt` и `offline`; новый `Y` ≠ `X`, `online`, `revokedAt` = `null`.

33а. (О1) `R=$(a GET /agents/$X | body | jq -r .revokedAt); a POST /agents/$X/revoke | tail -1; a GET /agents/$X | body | jq -r .revokedAt`
     → `HTTP 200 application/json`; `revokedAt` равно `$R` (повторный отзыв без
     `confirm` ничего не меняет).
34. `$DC logs self-agent | grep -E 'CERT_REVOKED|AGENT_REVOKED'` → строки отказа прежней
    личности есть; `$DC logs self-agent | grep -cE 'sard_[A-Za-z0-9_-]{43}\.'` → `0` (строки токена нет).
35. `chan` → `enroll-token` нет; `$PSQL "select count(*) from agents where builtin and revoked_at is null"` → `1`.
36. Обычный агент отзывается без подтверждения: зарегистрировать агента по
    обычному токену (`docs/qa/agent-enroll.md`, шаги успеха), `Z=<его id>`;
    `a POST /agents/$Z/revoke | tail -1` → `HTTP 200 application/json`.
37. `a DELETE /agents/$Y | tail -1` → не `2xx`; `builtin` → `Y` по-прежнему `online`.

## Часть 5. Обновление с выпущенной 0.1.0-beta.N (проверка 5)

Изменено F4a (уточнение владельца 2026-10-09: «Будет верно после
0.1.0-бета»). Исходная версия — первая выпущенная `0.1.0-beta.N` (она уже
содержит F4a и соседа). `0.0.1-rc1` — не поддерживаемый источник обновления:
такую установку переустанавливают с новыми томами (`docs/operator/07-upgrade.md`).
**Предусловие:** тег `v0.1.0-beta.N` существует (`git tag -l 'v0.1.0-beta.*'`
не пусто). Пока его нет (на 2026-10-09 есть только `v0.0.1-rc1`), часть 5 не
выполняется — это не дефект. Образы беты — из ghcr; если их там нет, собрать
из её тега (решение 12 контрольной точки 1). `BETA` — номер версии без `v`,
например `0.1.0-beta.1`.

38. `$DC down -v`; `git show v$BETA:deploy/docker-compose.yml > $QA/beta.yml`;
    `.env` для беты — по `deploy/.env.example` её тега, `SARD_VERSION=$BETA`;
    `docker compose -f $QA/beta.yml --env-file $QA/beta.env up -d --wait`
    → `postgres`, `server` `healthy`, `self-agent` `running`; тома
    `sard_postgres-data`, `sard_sard-pki`, `sard_sard-self-*` созданы (`docker volume ls`).
39. Пройти мастер беты по коду из `docker compose logs server` (как в
    `docs/qa/onboarding-setup.md`, часть 1, шаги 10, 17, 20) с паролем
    `qa-admin-password-2026`; `login` → `204`. Дождаться `builtin` → один
    `sard-self`, `online`; запомнить `S=<id>`. Выпустить два токена,
    зарегистрировать по одному обычного агента A (`docs/qa/agent-enroll.md`); сохранить
    `a GET /agents | body | jq -S '[.items[] | {id, hostname, builtin, revokedAt}]' > $QA/agents-before`,
    `a GET /enrollment-tokens | body | jq -S '[.items[] | {id, status}]' > $QA/tokens-before`,
    `$DC exec -T server sha256sum /var/lib/sard/pki/ca/ca.crt > $QA/ca-before`.
40. `docker compose -f $QA/beta.yml --env-file $QA/beta.env down` (без `-v`), затем
    `$DC up --wait` с текущими compose и образами
    → код 0; `server` `healthy`; `self-agent` `running`.
41. `$DC logs server 2>&1 | grep -cE 'SARD SETUP CODE'` → `0` (мастер не
    открывается); `curl -sS $API/onboarding | jq -c '[.steps[0].state, .steps[1].state]'`
    → `["done","done"]`; `login` (пароль из мастера беты) → `204`.
42. `diff <(a GET /agents | body | jq -S '[.items[] | {id, hostname, builtin, revokedAt}]') $QA/agents-before`
    → нет различий (агент A и `sard-self` `$S` на месте); то же для токенов → нет различий;
    `$DC exec -T server sha256sum /var/lib/sard/pki/ca/ca.crt | diff - $QA/ca-before` → нет различий.
43. `$PSQL "select count(*) from flyway_schema_history where not success"` → `0`;
    версия последней миграции равна последнему файлу
    `server/src/main/resources/db/migration` текущей сборки.
44. Повторять `builtin` до 3 минут → один элемент, `id` = `$S`, `online`;
    встроенных токенов не прибавилось. Агент A (если запущен на хосте) снова
    `online`, его `id` не изменился.

## Часть 6. Консоль

45. Открыть консоль, войти. Страница агентов → `sard-self` в списке, рядом метка
    «встроенный» (в английской локали — её перевод из `src/locales/en.json`);
    у обычного агента метки нет. Карточка `sard-self` → та же метка.
46. Ни в списке, ни в карточке нет действия удаления агента.
47. Карточка `sard-self` → «Отозвать»: кнопка подтверждения неактивна; ввести
    `sard-sel` → неактивна; `SARD-SELF` → неактивна; ` sard-self` (с пробелом) →
    неактивна; `sard-self` → активна.
48. Закрыть диалог без подтверждения → DevTools → Network: запроса `revoke` не было.
49. Открыть снова, ввести `sard-self`, подтвердить → в Network запрос
    `POST /api/v1/agents/<id>/revoke?confirm=sard-self`, ответ `200`; агент показан
    отозванным. В течение 2 минут в списке появляется новый `sard-self` с меткой
    «встроенный», онлайн (шаг 33).
50. Отзыв обычного агента → диалог без поля ввода, запрос без `confirm`, ответ `200`.
51. `grep -c self_agent_confirmation_required web/src/locales/ru.json web/src/locales/en.json`
    → в каждом файле не меньше `1`; ключи метки «встроенный» и поля
    подтверждения есть в обоих словарях.

Сценарий консоли «Отказ сервера в отзыве без подтверждения показывается
локализованным сообщением» на настоящем сервере вручную не воспроизводится:
консоль всегда отправляет `confirm=sard-self`. Его проверяют в режиме моков
(`web/src/mocks` отвечает на отзыв встроенного агента без верного `confirm`
409, как сервер, К2) — это открытый пункт к реализации, см. отчёт specifier.

## Часть 7. Без канала (установка не из compose)

52. `$DC down`; запустить сервер вне compose без `SARD_SELF_DIR`
    (`java -jar server/build/libs/sard-server.jar` с переменными базы из
    `deploy/.env` и `SARD_DB_URL=jdbc:postgresql://localhost:5432/sard`, база
    `$DC up -d postgres` с опубликованным портом) → сервер стартует; в логе нет
    строк `WARN` со ссылкой на `docs/operations/self-agent.md` и строки
    `built-in agent enrollment token written`.
53. Через 1 минуту `$PSQL "select count(*) from enrollment_tokens where builtin and created_at > now() - interval '2 minutes'"`
    → `0`.

### Непригодный канал (О3)

Тот же запуск сервера вне compose, что в шаге 52, но с `SARD_SELF_DIR`:

54. `SARD_SELF_DIR=$QA/nope` (каталога нет) → сервер не стартует, процесс
    завершается с ненулевым кодом; сообщение называет `$QA/nope` и
    `docs/operations/self-agent.md`.
55. `touch $QA/file; SARD_SELF_DIR=$QA/file` → то же, сообщение называет `$QA/file`.
56. `mkdir -m 0500 $QA/ro; SARD_SELF_DIR=$QA/ro` → то же, сообщение называет `$QA/ro`;
    `ls -A $QA/ro` → пусто.
57. После шагов 54–56 `$PSQL "select count(*) from enrollment_tokens where builtin and created_at > now() - interval '5 minutes'"`
    → `0`.
