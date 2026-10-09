# QA: вход администратора по паролю (W1b)

Сценарии: `docs/specs/server/admin-login.feature`,
`docs/specs/web/admin-login.feature`. Контракт — OpenAPI S8a и ADR 0019;
решения владельца К1–К4 и Р1–Р12 — в заголовке серверной спецификации.
Пометка `[Р…]` или `[К…]` у шага называет решение, которое он проверяет.
Изменения контракта (403 `origin_rejected`, описание Set-Cookie) и моков —
часть этой фичи.

Процедура из трёх частей:

- **Часть 1 — REST API сервера** (`curl`).
- **Часть 2 — консоль в браузере**, дважды: на моках (`VITE_API_MOCKS=1`) и
  против сервера (`make up`). Результаты обоих проходов должны совпадать, кроме
  явно отмеченных отличий (пароль мока — `admin`).
- **Часть 3 — развёртывание, документация, ADR.**

Ожидаемый результат указан после «→» в каждом шаге. Любое расхождение — дефект.

> **Изменено F4a** (`docs/qa/onboarding-setup.md`; утверждено владельцем
> 2026-10-09). `SARD_ADMIN_PASSWORD` больше нет. Подготовка
> читается так: чистая установка `make up` без пароля, затем мастер по коду из
> `docker compose logs server` с паролем `qa-admin-password-2026` (часть 1
> процедуры F4a, шаги 1–20). Шаги 1–5 части 1 («Старт сервера») и шаги 1–5
> части 3 заменены частью 5 процедуры F4a; шаг 21 части 1 и вход после
> перезапуска — пароль из мастера сохраняется. Остальные шаги — без изменений.

Сроки сессии (12 ч, 7 дней) и точные границы блокировки вручную не
проверяются — это делают тесты сценариев с управляемыми часами. Здесь —
одна проверка окончания блокировки с ожиданием 15 минут (шаг 20).

## Подготовка

Нужны: Docker, `curl`, `jq`, Chromium или Firefox. Команды — из корня репозитория.

```bash
make down; rm -f deploy/.env
cp deploy/.env.example deploy/.env
sed -i 's/^#\? *SARD_ADMIN_PASSWORD=.*/SARD_ADMIN_PASSWORD=qa-admin-password-2026/' deploy/.env
grep -q '^SARD_ADMIN_PASSWORD=' deploy/.env || echo 'SARD_ADMIN_PASSWORD=qa-admin-password-2026' >> deploy/.env
make up
DC="docker compose -f deploy/docker-compose.yml --env-file deploy/.env"
API=http://localhost:8080/api/v1
PW=qa-admin-password-2026
QA=$(mktemp -d)
login() {  # login <password> <cookie-jar> [extra curl args] — печатает заголовки и тело
  curl -sS -i -X POST "$API/session" -H 'Content-Type: application/json' \
    -d "{\"password\":\"$1\"}" -c "$2" "${@:3}"
}
sid() { awk '$6=="sard_session"{print $7}' "$1"; }   # значение sard_session из jar
```

→ `make up` завершается, сервер `healthy`.

## Часть 1. REST API

### Старт сервера

1. `$DC stop server; SARD_ADMIN_PASSWORD= $DC up -d server; sleep 20; $DC ps server; $DC logs server | tail -50`
   → сервер не стартует; в логе сообщение называет `SARD_ADMIN_PASSWORD` и
   говорит, что переменная не задана.
2. `SARD_ADMIN_PASSWORD=short-pw-11 $DC up -d server; sleep 20; $DC logs server | tail -50`
   → сервер не стартует; сообщение называет `SARD_ADMIN_PASSWORD` и минимум 12
   символов; `$DC logs server | grep -c short-pw-11` → `0`.
3. `SARD_ADMIN_PASSWORD=exactly-12ch $DC up -d --wait server; login exactly-12ch $QA/j0 | head -1`
   → сервер `healthy`; `HTTP/1.1 204`.
4. `[Р6]` `SARD_ADMIN_PASSWORD=паролькирилл $DC up -d --wait server`
   → сервер `healthy` (12 символов, 24 байта).
5. `$DC up -d --wait server` (вернуть пароль из `deploy/.env`) → `healthy`.

### Вход и сессия

6. `login $PW $QA/j1`
   → `HTTP/1.1 204`, тела нет; `Set-Cookie: sard_session=…` с `HttpOnly`,
   `SameSite=Strict`, `Path=/`; без `Secure` (HTTP) `[Р4]`; без `Max-Age` и
   `Expires` `[Р7]`.
7. `curl -sS -b $QA/j1 $API/session | jq`
   → `200`, `tenantId` = `00000000-0000-0000-0000-000000000001`, `expiresAt` ≈
   сейчас + 12 ч `[Р1]`.
8. `curl -sS -i $API/session`
   → `401`, `Content-Type: application/problem+json`, тело с `type`, `title`,
   `status: 401`, `detail`, `code: "unauthenticated"`.
9. `curl -sS -i -b 'sard_session=forged-session-id' $API/session` → как в шаге 8.
10. `S1=$(sid $QA/j1); login $PW $QA/j1 -b $QA/j1 | grep -i set-cookie; S2=$(sid $QA/j1); [ "$S1" != "$S2" ] && echo rotated; curl -sS -o /dev/null -w '%{http_code}\n' -b "sard_session=$S1" $API/session`
    → `rotated`; `401` для старого идентификатора.
11. `login $PW $QA/j2 -b 'sard_session=attacker-chosen' | grep -i set-cookie`
    → значение `sard_session` не `attacker-chosen`.
12. `login $PW $QA/j3; curl -sS -o /dev/null -w '%{http_code}\n' -b $QA/j2 $API/session; curl -sS -o /dev/null -w '%{http_code}\n' -b $QA/j3 $API/session`
    → `200` и `200` (две сессии одновременно) `[Р7]`.

### Неверный пароль

13. `login wrong-password-123 $QA/jx > $QA/w1; login '' $QA/jx > $QA/w2; login 'QA-ADMIN-PASSWORD-2026' $QA/jx > $QA/w3`
    → в каждом `401`, `application/problem+json`, `code: "unauthenticated"`, нет
    `Set-Cookie: sard_session`; тела (последняя строка файлов) совпадают
    побайтно: `tail -1 $QA/w1 | cmp - <(tail -1 $QA/w2) && tail -1 $QA/w1 | cmp - <(tail -1 $QA/w3) && echo same` → `same`.
14. `[К2]` `curl -sS -i -X POST $API/session -H 'Content-Type: application/json' -d '{}'`
    → `401`, `code: "unauthenticated"` (не `400`).

Перед следующим разделом: `$DC restart server` и дождаться `healthy` (сбросить
счётчики неудач, накопленные шагами 13–14).

### Перебор

15. `for i in 1 2 3 4 5; do login wrong-password-123 $QA/jx | head -1; done`
    → пять раз `HTTP/1.1 401`.
16. `login wrong-password-123 $QA/jx | grep -iE '^HTTP|retry-after|"code"'`
    → `429`, `Retry-After` от 895 до 900 `[Р2]`, `code: "too_many_attempts"`.
17. `login $PW $QA/jx | grep -iE '^HTTP|set-cookie'`
    → `429`, нет `Set-Cookie: sard_session`.
18. `curl -sS -o /dev/null -w '%{http_code}\n' -b $QA/j3 $API/session`
    → `200`: сессия, выданная до блокировки, действует.
19. `[Р4]` `login $PW $QA/jx -H 'X-Forwarded-For: 192.0.2.99' | head -1` → `429`.
20. Подождать 15 минут от шага 15. `login $PW $QA/j4 | head -1` → `204` `[Р2]`.
21. `$DC restart server` → дождаться `healthy`; `curl -sS -o /dev/null -w '%{http_code}\n' -b $QA/j4 $API/session`
    → `401` (перезапуск завершает сессии).

### Выход

22. `login $PW $QA/j5 >/dev/null; S5=$(sid $QA/j5); curl -sS -i -X DELETE -b $QA/j5 $API/session`
    → `204`; `Set-Cookie: sard_session=` с пустым значением, `Max-Age=0`, `Path=/`.
23. `curl -sS -o /dev/null -w '%{http_code}\n' -b "sard_session=$S5" $API/session; curl -sS -o /dev/null -w '%{http_code}\n' -X DELETE -b "sard_session=$S5" $API/session`
    → `401` и `401`.
24. `curl -sS -i -X DELETE $API/session` → `401`, `code: "unauthenticated"`.

### CSRF `[К1]` `[Р5]`

25. `login $PW $QA/j6 >/dev/null; curl -sS -i -X DELETE -b $QA/j6 -H 'Origin: https://evil.example' $API/session`
    → `403`, `application/problem+json`, `code: "origin_rejected"`, нет `Set-Cookie: sard_session`;
    `curl -sS -o /dev/null -w '%{http_code}\n' -b $QA/j6 $API/session` → `200`.
26. То же с `-H 'Origin: null'` → `403` `origin_rejected`; сессия действует.
27. То же с `-H 'Origin: http://localhost:9999'` → `403` `origin_rejected`; сессия действует.
28. `curl -sS -i -X POST -b $QA/j6 -H 'Origin: https://evil.example' -H 'Content-Type: application/json' -d '{}' $API/sources`
    → `403` `origin_rejected`, не `501`.
29. `login $PW $QA/j7 -H 'Origin: https://evil.example' | grep -iE '^HTTP|set-cookie'`
    → `403` `origin_rejected`, нет `Set-Cookie: sard_session`.
30. `curl -sS -o /dev/null -w '%{http_code}\n' -b $QA/j6 -H 'Origin: https://evil.example' $API/session`
    → `200` (чтение не отклоняется).
31. `curl -sS -o /dev/null -w '%{http_code}\n' -X DELETE -b $QA/j6 -H 'Origin: http://localhost:8080' $API/session`
    → `204`.
32. `login $PW $QA/j8 >/dev/null; curl -sS -o /dev/null -w '%{http_code}\n' -X DELETE -b $QA/j8 $API/session`
    (без Origin) → `204`.
32а. `[К1]` `[К3]` `jq -r '.paths[] | to_entries[] | select(.key|test("post|put|patch|delete")) | .value.responses["403"] != null' web/src/api/openapi.json | sort -u`
    → только `true`; `jq -r '.components.schemas.ErrorCode.enum[]' web/src/api/openapi.json | grep -c origin_rejected` → `1`;
    `jq -r '.paths["/api/v1/session"].post.responses["204"].headers["Set-Cookie"].description' web/src/api/openapi.json`
    → называет `HttpOnly`, `SameSite=Strict`, `Path=/` и `Secure` при HTTPS.

### Защита API

33. Без cookie, для каждой операции: `GET /agents`, `GET /agents/{id}`,
    `GET|POST /enrollment-tokens`, `GET /enrollment-tokens/{id}`,
    `POST /enrollment-tokens/{id}/revoke`, `GET|POST /sources`,
    `GET|PUT|DELETE /sources/{id}`, `POST /sources/{id}/runs`,
    `GET /sources/{id}/snapshots`, `GET /runs`, `GET /runs/{id}`,
    `GET /runs/{id}/steps/{stepId}/logs`, `GET /no-such-resource`
    (идентификаторы — любые UUID):
    `curl -sS -o /dev/null -w '%{http_code} %{content_type}\n' -X <метод> $API/<путь>`
    → каждый раз `401 application/problem+json`.
34. `login $PW $QA/j9 >/dev/null; curl -sS -b $QA/j9 $API/agents | jq .code`
    → `"not_implemented"` (заглушка до S8b).
35. `curl -sS -o /dev/null -w '%{http_code}\n' $API/status` → `200`.
36. `[Р11]` `curl -sS -o /dev/null -w '%{http_code}\n' localhost:8080/actuator/health; curl -sS -o /dev/null -w '%{http_code}\n' localhost:8080/v3/api-docs`
    → `200` и `200`.
37. `for p in env configprops httpexchanges metrics heapdump; do curl -sS -o /dev/null -w "$p %{http_code}\n" localhost:8080/actuator/$p; done`
    → каждый `404`.
38. Регистрация агента по gRPC без cookie — шаг 1 части 1 `docs/qa/agent-enrollment.md`
    → успех.

### Журнал и утечки

39. `$DC logs server > $QA/server.log`; в `$QA/server.log` есть записи об
    успешном входе, неудачном входе, выходе и блокировке, в каждой — адрес
    клиента; запись о блокировке — одна на эпизод шагов 15–17 `[Р10]`.
40. `grep -cF "$PW" $QA/server.log; grep -cF wrong-password-123 $QA/server.log; grep -cF QA-ADMIN-PASSWORD-2026 $QA/server.log`
    → `0`, `0`, `0`.
41. Для каждого значения `sard_session` из `$QA/j*`:
    `grep -cF "<значение>" $QA/server.log` → `0` `[Р10]`.
42. `grep -rlF "$PW" $QA/w* ; curl -sS localhost:8080/actuator | jq -r '._links[].href' | xargs -n1 curl -sS | grep -cF "$PW"`
    → ничего не найдено; `0`.

## Часть 2. Консоль

Выполняется дважды:

- **Моки:** `make down; cd web && VITE_API_MOCKS=1 npm run dev`; пароль — `admin`
  (`MOCK_PASSWORD`). `[К4]` Моки стартуют без входа.
- **Сервер:** `make up; cd web && npm run dev`; пароль — `$PW`.

Консоль — `http://localhost:5173`. DevTools → Network открыт, «Preserve log» включён.

1. Открыть `/agents` → адрес `/login?redirect=%2Fagents`; в Network до
   перехода — только `GET /api/v1/session` (401).
2. Открыть `/runs?status=failed` → `/login?redirect=%2Fruns%3Fstatus%3Dfailed` `[Р9]`.
3. Страница входа → одно поле типа `password`, кнопка входа, поля имени нет;
   при пустом поле кнопка неактивна `[Р9]`.
4. Ввести неверный пароль → локализованное сообщение о неверном пароле; адрес
   не изменился, в `redirect` нет вложенного `/login`.
5. Ввести верный пароль на `/login?redirect=%2Fruns%3Fstatus%3Dfailed`
   → открыта страница запусков с `?status=failed`; адрес не содержит пароля.
6. Открыть `/login` при действующей сессии → переход на `/` `[Р9]`.
7. Открыть вручную `/login?redirect=https://evil.example/`, войти → главная
   консоли. Повторить с `//evil.example/agents`, `/\evil.example`,
   `javascript:alert(1)` → каждый раз главная.
8. Шапка на `/`, `/agents`, `/sources`, `/runs` → на каждой кнопка выхода.
9. DevTools → Application: `localStorage`, `sessionStorage`, IndexedDB
   → нет пароля и значения `sard_session`; в Console `document.cookie`
   → без `sard_session`.
10. Нажать «Выйти» на `/agents` → `DELETE /api/v1/session` 204; адрес `/login`
    без `redirect` `[Р9]`. Кнопка браузера «Назад» → `/login?redirect=%2Fagents`.
11. Войти; в другой вкладке завершить сессию (сервер: `curl -X DELETE` с
    cookie из DevTools или `$DC restart server`; моки: в консоли
    DevTools нет доступа — пропустить) и в первой вкладке перейти на другую
    страницу → переход на `/login?redirect=<текущий путь>`; после входа — назад
    на тот же путь.
12. Пять раз ввести неверный пароль, затем шестой
    → сообщение о блокировке с ожиданием 15 минут `[Р2]` `[Р9]` (на моках — те же
    15 минут `[К4]`).
13. Сервер: `$DC stop server`; ввести пароль → сообщение о недоступности
    сервера, отличное от сообщения о неверном пароле. Открыть `/agents` при
    остановленном сервере → экран ошибки с повтором, адрес не меняется `[Р9]`.
    Войти, остановить сервер, нажать «Выйти» → сообщение об ошибке, консоль
    остаётся на странице `[Р9]`. `$DC start server`.
14. Переключить язык RU/EN на странице входа, после ошибки входа, после
    блокировки и в шапке → все надписи и сообщения входа и выхода переведены,
    ключей i18n (`login.…`) на экране нет.

## Часть 3. Развёртывание, документация, ADR

1. `grep -n SARD_ADMIN_PASSWORD deploy/docker-compose.yml`
   → строка в `environment` сервиса `server` со ссылкой `${SARD_ADMIN_PASSWORD…}`
   на `deploy/.env`, без значения; рядом `SARD_AGENT_ENDPOINT`.
2. `sed -i '/^SARD_ADMIN_PASSWORD=/d' deploy/.env; $DC config >/dev/null`
   → ошибка, называющая `SARD_ADMIN_PASSWORD` и `deploy/.env`.
3. `grep -n -B2 -A1 -E 'SARD_ADMIN_PASSWORD|SARD_AGENT_ENDPOINT' deploy/.env.example`
   → обе переменные с комментариями; у пароля — минимум 12 символов.
4. `[Р3]` `make down; rm deploy/.env; make up 2>&1 | tee $QA/up.log`
   → `deploy/.env` содержит `SARD_ADMIN_PASSWORD` длиной ≥ 24;
   `grep -cF "$(sed -n 's/^SARD_ADMIN_PASSWORD=//p' deploy/.env)" $QA/up.log` → `0`;
   вывод называет `deploy/.env`; вход этим паролем → `204`.
5. README.md, раздел «Запуск» → называет `SARD_ADMIN_PASSWORD`, `deploy/.env`,
   минимум 12 символов и рядом `SARD_AGENT_ENDPOINT`.
6. `docs/adr/0021-*.md` (D2, вход администратора) → разделы «Контекст»,
   «Решение», «Отвергнуто» (без входа; несколько пользователей на этапе 1),
   «Последствия» (сессии в памяти теряются при перезапуске; отложены роли,
   SSO/LDAP, смена пароля из консоли, хранение сессий в БД и HA); есть в
   `docs/adr/README.md`.
