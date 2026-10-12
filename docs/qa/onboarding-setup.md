<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# QA: безопасный первый запуск — код настройки, шаг CA, пароль администратора (F4a)

Сценарии: `docs/specs/server/onboarding-setup.feature`,
`docs/specs/web/onboarding-setup.feature`. Контракт К1–К9, решения владельца
и решения specifier Р1–Р19 — в заголовке серверной спецификации; решения
консоли Рк1–Рк8 — в заголовке веб-спецификации. Шаг с пометкой `[OQ-NNN]`
меняется вместе с решением по этому вопросу (`docs/open-questions.md`).

Поправка 2026-10-09: обратной совместимости с версиями до 0.1.0-beta нет;
OQ-198, OQ-199. Установка 0.0.1-rc1 не обновляется, а переустанавливается с
новыми томами. Первая бета 0.1.0-beta.N уже содержит F4a; обновление с неё
сохраняет данные и состояние онбординга — его проверяет часть 5 процедуры
`docs/qa/self-agent.md`, когда первая бета выпущена. Здесь проверки обновления
нет.

Процедура из пяти частей:

- **Часть 1 — REST API и лог сервера** (`curl`) на чистой установке compose.
- **Часть 2 — консоль**, дважды: на моках и против сервера.
- **Часть 3 — замена CA импортом, нечитаемый источник до шага ca, тома CA и
  базы вместе** (правило F8, изменённое F4a; OQ-198, OQ-199).
- **Часть 4 — восстановление доступа** (`admin-reset`).
- **Часть 5 — установка, автоматизация, документация.**

Ожидаемый результат — после «→» в каждом шаге. Любое расхождение — дефект.

Срок кода (24 часа), срок сессии настройки и точные границы блокировки вручную
не проверяются — это делают тесты сценариев с управляемыми часами.

## Подготовка

Нужны: Docker, `curl`, `jq`, `openssl`, Chromium или Firefox. Команды — из
корня репозитория.

```bash
make down; docker volume rm sard_postgres-data sard_sard-pki sard_sard-self-channel sard_sard-self-config sard_sard-self-state 2>/dev/null
rm -f deploy/.env; cp deploy/.env.example deploy/.env    # пароль базы и версию — по комментариям файла
make up
DC="docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.build.yml --env-file deploy/.env"
API=http://localhost:8080/api/v1
QA=$(mktemp -d)
RE='SARD SETUP CODE: ([0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4}){6}) valid until ([^ ]+)'
code() { $DC logs server 2>&1 | grep -oE "$RE" | tail -1 | cut -d' ' -f4; }   # код последнего старта
post() { curl -sS -i -X POST "$API/$1" -H 'Content-Type: application/json' "${@:2}"; }
enter() { post onboarding/setup-session -d "{\"code\":\"$1\"}" -c "$2" "${@:3}"; }   # enter <код> <jar>
fp() { openssl x509 -in "$1" -pubkey -noout | openssl pkey -pubin -outform DER | sha256sum | cut -c1-64; }
IMAGE=$($DC config --images | grep sard-server)
pkicrt() { docker run --rm --user 0 --entrypoint cat -v sard_sard-pki:/pki:ro "$IMAGE" /pki/ca/ca.crt > "$1"; }
```

→ `make up` завершается, сервер `healthy`; `make up` не создаёт и не меняет
пароль администратора (в выводе нет `SARD_ADMIN_PASSWORD`).

## Часть 1. REST API и лог сервера

### Код в логе `[Р1]`

1. `$DC logs server 2>&1 | grep -cE "$RE"`
   → `1`. `C1=$(code); echo "$C1"` → 7 групп по 4 знака через дефис, без `I`,
   `L`, `O`, `U`; срок в строке — время старта плюс 24 часа (UTC).
2. `$DC logs server 2>&1 | grep -B3 -A3 -E "$RE"`
   → рядом строки с путём `/setup` и командой `docker compose logs server`; код
   только в одной строке.

### Состояние до мастера

3. `curl -sS $API/onboarding | jq -c .`
   → `{"steps":[{"id":"ca","state":"pending"},{"id":"admin","state":"pending"},{"id":"self_backup","state":"upcoming"},{"id":"keys_confirmed","state":"upcoming"}],"setupCode":"active","access":"none","ca":null,"caReplaceable":null}`.
   `pkicrt $QA/g.crt; G=$(fp $QA/g.crt); curl -sS $API/onboarding | grep -c "$G"` → `0`.
4. `post session -d '{"password":"correct-horse-battery"}' | grep -E '^HTTP|"code"'`
   → `409`, `"code":"setup_required"`; нет `Set-Cookie: sard_session` `[В6]`.
5. Без cookie: `for p in session ca agents overview enrollment-tokens sources runs; do curl -sS -o /dev/null -w "$p %{http_code}\n" $API/$p; done`
   → каждый `401`. `curl -sS -o /dev/null -w '%{http_code}\n' $API/status` → `200`.

### Ввод кода и перебор `[Р4]` `[Р5]`

6. `for i in 1 2 3 4 5; do enter 0000-0000-0000-0000-0000-0000-0000 $QA/jx | head -1; done`
   → пять раз `401`; тела одинаковы, `code: "unauthenticated"`, нет `Set-Cookie: sard_setup`.
7. `enter 0000-0000-0000-0000-0000-0000-0000 $QA/jx | grep -iE '^HTTP|retry-after|"code"'`
   → `429`, `Retry-After` от 895 до 900, `too_many_attempts`.
   `enter "$C1" $QA/jx | grep -iE '^HTTP|set-cookie'` → `429`, без `sard_setup`.
8. `enter "$C1" $QA/jx -H 'X-Forwarded-For: 192.0.2.99' | head -1` → `429`.
9. `$DC restart server` → `healthy`. `C2=$(code); [ "$C1" != "$C2" ] && echo new`
   → `new`. `enter "$C1" $QA/jx | head -1` → `401` (прежний код не действует,
   блокировка снята перезапуском) `[D15]`.
10. `[OQ-194]` `enter "$(echo "$C2" | tr -d - | tr A-Z a-z)" $QA/j1 | grep -iE '^HTTP|set-cookie'`
    → `204`; `Set-Cookie: sard_setup=…; Path=/api/v1/onboarding; HttpOnly; SameSite=Strict`,
    без `Secure`, `Max-Age`, `Expires`; нет `sard_session` `[Р3]`.
11. `enter "$C2" $QA/j2 >/dev/null; curl -sS -b $QA/j1 $API/onboarding | jq -r .access; curl -sS -b $QA/j2 $API/onboarding | jq -r .access`
    → `setup` и `setup` (код вводится повторно) `[В2]`.
12. `enter "$C2" $QA/j3 -H 'Origin: https://evil.example' | grep -E '^HTTP|"code"'`
    → `403` `origin_rejected`.

### Сессия настройки и шаг CA

13. `curl -sS -b $QA/j1 $API/onboarding | jq -c '{ca,caReplaceable}'`
    → `ca.fingerprint` равен `$G`, `ca.origin` `generated`, `ca.keyPath`
    `/var/lib/sard/pki/ca/ca.key`, `caReplaceable` `true` `[Р12]`.
14. С сессией настройки: `for p in session ca agents; do curl -sS -o /dev/null -w "$p %{http_code}\n" -b $QA/j1 $API/$p; done`
    → каждый `401`. То же с cookie на корне:
    `curl -sS -o /dev/null -w '%{http_code}\n' -H "Cookie: sard_setup=$(awk '$6=="sard_setup"{print $7}' $QA/j1)" $API/agents` → `401`.
15. `[OQ-187]` `post onboarding/admin -b $QA/j1 -d '{"password":"correct-horse-battery"}' | grep -E '^HTTP|"code"'`
    → `409` `ca_step_pending`; `curl -sS $API/onboarding | jq -c '.steps[1]'` → `admin` `pending`.
16. `sleep 60; docker run --rm --user 0 --entrypoint ls -v sard_sard-self-channel:/c:ro "$IMAGE" /c`
    → нет `enroll-token` (до шага ca встроенный токен не выпускается) `[Р13]`.
17. `post onboarding/ca -b $QA/j1 | head -1` → `204`. Повтор → `204`.
    `curl -sS -b $QA/j1 $API/onboarding | jq -c '[.steps[0].state, .caReplaceable]'` → `["done",false]`.
18. Не позднее чем через 30 секунд `docker run --rm --user 0 --entrypoint ls -v sard_sard-self-channel:/c:ro "$IMAGE" /c`
    → `enroll-token` появился или уже удалён после регистрации соседа.

### Шаг admin

19. `post onboarding/admin -b $QA/j1 -d '{"password":"short-pw-11"}' | grep -E '^HTTP|"code"'` → `422` `validation_failed`;
    `post onboarding/admin -b $QA/j1 -d "{\"password\":\"$(printf 'a%.0s' $(seq 1025))\"}" | head -1` → `422`;
    `curl -sS -b $QA/j1 $API/onboarding | jq -r .access` → `setup` (отказ не завершил сессию).
20. `PW=qa-admin-password-2026; post onboarding/admin -b $QA/j1 -c $QA/a1 -d "{\"password\":\"$PW\"}" | grep -iE '^HTTP|set-cookie'`
    → `204`; `Set-Cookie: sard_session=…; Path=/; HttpOnly; SameSite=Strict`;
    `Set-Cookie: sard_setup=; Max-Age=0; Path=/api/v1/onboarding`.
21. `curl -sS -b $QA/a1 $API/session | jq -c .` → `200`, `tenantId` по умолчанию,
    `expiresAt` ≈ сейчас + 12 ч. `curl -sS -b $QA/j2 $API/onboarding | jq -r .access`
    → `none`. `enter "$C2" $QA/jx | grep -E '^HTTP|"code"'` → `409` `setup_completed`.
22. `curl -sS -b $QA/a1 $API/onboarding | jq -c '[.steps[].state, .setupCode, .access]'`
    → `["done","done","upcoming","upcoming","not_issued","admin"]`.
    `curl -sS -b $QA/a1 $API/ca | jq -c .` → `fingerprint` = `$G`, `origin`
    `generated`, `keyPath` как в шаге 13.
23. `post session -c $QA/a2 -d "{\"password\":\"$PW\"}" | head -1` → `204`.
    `post session -d '{"password":"QA-ADMIN-PASSWORD-2026"}' | head -1` → `401`.
24. `[OQ-193]` `$DC exec -T postgres pg_dump -U sard sard > $QA/db.sql; grep -cF "$PW" $QA/db.sql; grep -oE '\$argon2id\$v=19\$m=[0-9]+,t=[0-9]+,p=[0-9]+\$' $QA/db.sql`
    → `0`; одна строка `$argon2id$v=19$m=19456,t=2,p=1$`.
25. Через 3 минуты: `curl -sS -b $QA/a1 $API/agents | jq -r '.items[] | [.hostname,.builtin,.status] | @tsv'`
    → `sard-self true online`.
26. `$DC restart server` → `healthy`; `$DC logs --since 1m server 2>&1 | grep -cE "$RE"` → `0`;
    вход паролем `$PW` → `204`.

### Смена пароля `[Р9]`

27. `post session -c $QA/b1 -d "{\"password\":\"$PW\"}" >/dev/null; post session -c $QA/b2 -d "{\"password\":\"$PW\"}" >/dev/null`
28. `curl -sS -i -X PUT $API/session/password -b $QA/b1 -H 'Content-Type: application/json' -d '{"currentPassword":"wrong-password-123","newPassword":"new-password-2026"}' | grep -E '^HTTP|"code"'`
    → `422` `wrong_password` (не `401`); `curl -sS -o /dev/null -w '%{http_code}\n' -b $QA/b2 $API/session` → `200`.
29. То же с `"newPassword":"short-pw-11"` и верным текущим → `422` `validation_failed`.
30. `curl -sS -i -X PUT $API/session/password -b $QA/b1 -c $QA/b1 -H 'Content-Type: application/json' -d "{\"currentPassword\":\"$PW\",\"newPassword\":\"new-password-2026\"}" | grep -iE '^HTTP|set-cookie'`
    → `204`; `[OQ-195]` новый `Set-Cookie: sard_session`.
31. `for j in b1 b2 a2; do curl -sS -o /dev/null -w "$j %{http_code}\n" -b $QA/$j $API/session; done`
    → `b1 200`, `b2 401`, `a2 401`. Вход прежним паролем → `401`, новым → `204`.
    `PW=new-password-2026`.
32. Чужой Origin: тот же `PUT` с `-H 'Origin: https://evil.example'` → `403`, пароль не сменился.
33. `$DC restart server`; затем 4 неверных входа, затем `PUT` с неверным текущим
    (`422`), затем вход верным паролем → `429` (общий счётчик с входом).
    `$DC restart server` (снять блокировку).

### Журнал и утечки `[Р14]`

34. `$DC logs server > $QA/server.log 2>&1`
    → записи о неудачном и успешном вводе кода, блокировке ввода кода (одна),
    шаге ca с отпечатком, шаге admin, неудачной и успешной смене пароля — каждая
    с адресом клиента.
35. `for c in "$C1" "$C2"; do grep -cF "$c" $QA/server.log; grep -cF "$(echo $c | tr -d -)" $QA/server.log; done`
    → `1`, `0`, `1`, `0` (каждый код — только в своей строке старта).
36. `grep -cF qa-admin-password-2026 $QA/server.log; grep -cF new-password-2026 $QA/server.log; grep -cF wrong-password-123 $QA/server.log; grep -cF 0000-0000-0000 $QA/server.log`
    → `0`, `0`, `0`, `0`. Для каждого значения `sard_setup` и `sard_session` из
    `$QA/*` → `grep -cF` → `0`.
37. `curl -sS localhost:8080/actuator | jq -r '._links[].href' | xargs -n1 curl -sS | grep -cE "$C2|$(echo $C2 | tr -d -)"`
    → `0`. `for p in env metrics heapdump; do curl -sS -o /dev/null -w "$p %{http_code}\n" localhost:8080/actuator/$p; done` → каждый `404`.

## Часть 2. Консоль

Дважды:

- **Моки:** `make down; cd web && VITE_API_MOCKS=1 VITE_MOCK_ONBOARDING=1 npm run dev`;
  код — `ABCD-EFGH-JKMN-PQRS-TVWX-YZ01-2345`, отпечаток —
  `8544e2352a80a3d403eed68f8bb4ff271d0ce02c09c1d0faf6f090cea423be9f` `[Рк8]`.
- **Сервер:** подготовка из начала процедуры (чистая установка), затем
  `cd web && npm run dev`; код — `code`, отпечаток — `$G`.

Консоль — `http://localhost:5173`. DevTools → Network открыт, «Preserve log» включён.

1. Открыть `/agents` → адрес `/setup` без `redirect`; до перехода — только
   `GET /api/v1/session` (401) и `GET /api/v1/onboarding` `[Рк1]`.
2. Открыть `/login` → переход на `/setup` `[Рк2]`.
3. Экран кода → одно поле, кнопка неактивна при пустом поле; подсказка с
   `docker compose logs server` и `SARD SETUP CODE`; список шагов: CA,
   администратор, самобэкап и копия ключей с меткой «скоро» `[Рк4]` `[Рк5]`.
4. Ввести неверный код → сообщение о неверном коде, экран кода, перехода на
   `/login` нет. Ввести неверный код ещё 4 раза, затем шестой → сообщение о
   блокировке с ожиданием 15 минут. Против сервера: `$DC restart server`, взять
   новый `code`.
5. Ввести код строчными буквами с пробелами вместо дефисов → в Network тело
   запроса — ровно введённая строка; ответ `204`; показан шаг CA `[Р2]`.
6. Шаг CA → отпечаток (моноширинный, в одну строку, кнопка копирования —
   в буфере ровно 64 символа), подпись «создан сервером», путь
   `/var/lib/sard/pki/ca/ca.key`, объяснение про копию ключа со ссылкой на
   `docs/operator/06`; блок импорта с `SARD_PKI_IMPORT_DIR`, override-файлом,
   командой перезапуска, ссылкой на `docs/operator/08` и предупреждением о
   необратимости.
7. Обновить страницу → снова шаг CA, без кода `[Рк3]`.
8. Открыть `/setup` во втором браузере → экран кода; ввести тот же код → шаг CA `[В2]`.
9. Против сервера: в первом браузере `$DC restart server`, затем «Использовать
   этот CA» → экран кода с сообщением о закончившейся сессии настройки, перехода
   на `/login` нет `[Рк7]`. Ввести новый код.
10. «Использовать этот CA» → шаг администратора; CA в списке отмечен выполненным.
11. Шаг администратора → два поля `password`, требования «от 12 до 1024»;
    кнопка неактивна при пустых или несовпадающих полях, подсказка о
    несовпадении. Ввести `short`/`short` → сообщение с требованиями от сервера.
12. Ввести `qa-admin-password-2026` дважды → главная страница консоли без
    страницы входа; `GET /api/v1/session` → 200.
13. Во втором браузере (шаг 8) нажать любую кнопку мастера → экран кода; ввести
    код → сообщение, что администратор уже задан, переход на `/login`.
14. Открыть `/setup` → `/login` → (сессия действует) `/` `[Рк2]`.
15. «Токены» → отпечаток CA на странице равен отпечатку из шага 6.
16. DevTools → Application: `localStorage`, `sessionStorage`, IndexedDB, адрес и
    история → нет ни кода, ни пароля; `document.cookie` → нет `sard_setup` и
    `sard_session`.
17. Навигация → пункт «Настройки» → `/settings`: три поля `password`, требования.
    Кнопка неактивна, пока не заполнены все поля и подтверждение не совпадает.
18. Неверный текущий пароль → сообщение о неверном текущем пароле, остаёмся на
    `/settings`, перехода на `/login` нет.
19. Новый пароль `short` → сообщение с требованиями.
20. Во втором браузере войти. В первом сменить пароль на `new-password-2026` →
    сообщение об успехе и о завершении прочих сессий; поля очищены; переход на
    `/agents` работает. Во втором браузере открыть `/runs` →
    `/login?redirect=%2Fruns`; вход новым паролем → `/runs`.
21. Переключить RU/EN на экране кода, шаге CA, шаге администратора, после
    ошибок, на `/settings` → всё переведено, ключей i18n на экране нет.
22. Против сервера: `$DC stop server`, открыть `/agents` → экран ошибки с
    повтором, адрес не меняется. `$DC start server`.

## Часть 3. Замена CA импортом до шага ca

Нужен второй CA — как в `docs/qa/ca-import.md`, часть 1 («Подготовка»):
функции `seal`, `src`, `newkey`, `mkcert`, `CAEXT`, файл `$QA/override.yml`.

1. Чистая установка (подготовка из начала процедуры), мастер **не** проходить.
   `pkicrt $QA/g.crt; G=$(fp $QA/g.crt)`.
   `newkey $QA/f.key; mkcert $QA/f.key $QA/f.crt -days 3650 "${CAEXT[@]}"; src imp $QA/f.crt $QA/f.key; F=$(fp $QA/f.crt)`.
2. `QA_SRC="$QA/imp" $DC -f "$QA/override.yml" up -d --force-recreate --wait server`
   → `healthy`; `$DC logs server | grep 'CA imported from'` → строка с
   `fingerprint=$F`, `origin=imported`, `replaced=$G` `[Р11]`.
3. `pkicrt $QA/n.crt; fp $QA/n.crt` → `$F`. Каталог CA: `docker run --rm --user 0 --entrypoint sh -v sard_sard-pki:/pki:ro "$IMAGE" -c 'ls -A /pki /pki/ca'`
   → только `ca`, в нём `ca.crt` и `ca.key`, нет `.tmp-*`;
   `docker run --rm --user 0 --entrypoint cat -v sard_sard-pki:/pki:ro "$IMAGE" /pki/ca/ca.key | cmp - <(sudo cat $QA/imp/ca/ca.key) && echo same` → `same`.
4. `echo | openssl s_client -connect localhost:9090 -alpn h2 -showcerts 2>/dev/null | awk '/BEGIN/{n++} n==2' | sed '/END/q' > $QA/root.pem; fp $QA/root.pem`
   → `$F`.
5. Ввести новый код; `curl -sS -b <jar> $API/onboarding | jq -c .ca` → `fingerprint` `$F`, `origin` `imported`.
6. `[OQ-190]` Повторить шаг 2 с третьим CA `H` → заменён (`replaced=$F`;
   OQ-190 утверждён как предложено).
7. Подтвердить CA в мастере (`POST /onboarding/ca`). Повторить шаг 2 с другим
   источником → сервер не стартует; `CA import refused`, `CA_ALREADY_PRESENT`,
   оба отпечатка, `onboarding step ca is complete`,
   `docs/operator/08-migrate-and-remove.md`; каталог CA не изменился.
8. Причина `agent certificates issued` вручную не проверяется: без
   enterprise-замены `SessionApi` сертификатов агентов до шага ca быть не может
   (обновление с версий до F4a не поддерживается). Её проверяет тест «Выданный сертификат агента
   запрещает замену CA».
9. Источник с ключом от другого сертификата (`src mism $QA/f.crt <другой ключ>`)
   на чистой установке → `CA_KEY_MISMATCH`, каталог CA — прежний `$G`.
10. `$DC stop postgres; QA_SRC="$QA/imp" $DC -f "$QA/override.yml" up -d --force-recreate server; sleep 30`
    → сервер не стартует; после `$DC start postgres` каталог CA прежний. Вернуть
    стенд без override.

### Нечитаемый источник до шага ca `[OQ-198]` `[Р18]`

11. Чистая установка без пересоздания `$QA`:
    `make down; docker volume rm sard_postgres-data sard_sard-pki sard_sard-self-channel sard_sard-self-config sard_sard-self-state; make up`;
    мастер **не** проходить. `pkicrt $QA/g.crt; G=$(fp $QA/g.crt)`.
    `printf 'services:\n  server:\n    environment:\n      SARD_PKI_IMPORT_DIR: /var/lib/sard/pki-missing\n' > $QA/missing.yml`
12. `$DC -f $QA/missing.yml up -d --force-recreate server; sleep 30; $DC ps -a server; $DC logs --since 1m server 2>&1 | grep 'CA import refused'`
    → сервер не работает (не `healthy`, перезапускается или вышел); строка
    `CA import refused` с `IMPORT_SOURCE_MISSING`, `SARD_PKI_IMPORT_DIR` и
    `/var/lib/sard/pki-missing`; предупреждения «импорт пропущен» нет.
    `pkicrt $QA/n.crt; fp $QA/n.crt` → `$G`.
13. Источник без сертификата:
    `sudo mkdir -p $QA/nocrt/ca; sudo cp $QA/f.key $QA/nocrt/ca/ca.key; seal $QA/nocrt; QA_SRC="$QA/nocrt" $DC -f "$QA/override.yml" up -d --force-recreate server; sleep 30`
    → сервер не работает; `CA import refused`, `IMPORT_FILE_MISSING`, путь
    `…/ca/ca.crt`; каталог CA — прежний `$G`.
14. Источник с тем же CA `G`, но ключом с правами `0640`:
    `sudo mkdir -p $QA/gsrc; docker run --rm --user 0 --entrypoint tar -v sard_sard-pki:/pki:ro "$IMAGE" -C /pki -cf - ca | sudo tar -C $QA/gsrc -xf -; seal $QA/gsrc; sudo chmod 640 $QA/gsrc/ca/ca.key`;
    `QA_SRC="$QA/gsrc" $DC -f "$QA/override.yml" up -d --force-recreate server; sleep 30`
    → сервер не работает; `CA import refused`, `IMPORT_PERMISSIONS_TOO_OPEN`,
    путь ключа и `rw-r-----`; каталог CA — прежний `$G`.
15. `$DC up -d --force-recreate --wait server` (без override) → `healthy`, отпечаток
    `$G`. Ввести новый код и подтвердить CA (`POST /onboarding/ca` → `204`).
16. После шага ca: `$DC -f $QA/missing.yml up -d --force-recreate --wait server`
    → `healthy`; в логе этого старта WARN: импорт пропущен, CA уже есть,
    называет `SARD_PKI_IMPORT_DIR`; `pkicrt $QA/n.crt; fp $QA/n.crt` → `$G`.
    Вернуть стенд без override.

### Тома CA и базы — вместе `[OQ-199]` `[Р19]`

Стенд после шага 16: шаг ca выполнен, CA `$G`, мастер дальше не проходить.

17. Копия CA на случай возврата:
    `sudo mkdir -p $QA/gbak; docker run --rm --user 0 --entrypoint tar -v sard_sard-pki:/pki:ro "$IMAGE" -C /pki -cf - ca | sudo tar -C $QA/gbak -xf -; seal $QA/gbak; fp $QA/gbak/ca/ca.crt`
    → `$G`.
18. Пересоздать только том базы:
    `$DC rm -sf server self-agent postgres; docker volume rm sard_postgres-data; $DC up -d --wait postgres; $DC up -d server; sleep 30; $DC ps -a server; $DC logs --since 1m server 2>&1 | grep 'CA startup refused'`
    → сервер не работает (не `healthy`, перезапускается или вышел); строка
    `CA startup refused` с `CA_ORIGIN_NOT_RECORDED`, отпечатком `$G`, путём
    `/var/lib/sard/pki/ca`, `CA origin is not recorded in the database`,
    `server volumes are reinstalled together` и
    `docs/operator/09-troubleshooting.md`.
    `$DC logs --since 1m server 2>&1 | grep -cE "$RE"` → `0`;
    `pkicrt $QA/n.crt; fp $QA/n.crt` → `$G`;
    `$DC exec -T postgres psql -U sard -d sard -Atc "select count(*) from ca_origins"` → `0`.
19. То же с источником, где тот же CA: `QA_SRC="$QA/gbak" $DC -f "$QA/override.yml" up -d --force-recreate server; sleep 30`
    → сервер не работает; `CA startup refused`, `CA_ORIGIN_NOT_RECORDED`; замены и
    `CA imported from` в логе этого старта нет; каталог CA — прежний `$G`.
20. Переустановить тома вместе: `make down; docker volume rm sard_postgres-data sard_sard-pki sard_sard-self-channel sard_sard-self-config sard_sard-self-state; make up`
    → `healthy`, новая строка кода. Ввести код и подтвердить CA (`POST /onboarding/ca` → `204`).
    `pkicrt $QA/g2.crt; G2=$(fp $QA/g2.crt); sudo rm -rf $QA/gbak; sudo mkdir -p $QA/gbak; docker run --rm --user 0 --entrypoint tar -v sard_sard-pki:/pki:ro "$IMAGE" -C /pki -cf - ca | sudo tar -C $QA/gbak -xf -; seal $QA/gbak`.
21. Пересоздать только том CA, без источника:
    `$DC rm -sf server self-agent; docker volume rm sard_sard-pki; $DC up -d server; sleep 30; $DC logs --since 1m server 2>&1 | grep 'CA startup refused'`
    → сервер не работает; `CA startup refused` с `CA_MISSING`,
    `CA directory is empty`, `onboarding step ca is complete`,
    `SARD_PKI_IMPORT_DIR`, `server volumes are reinstalled together`;
    `docker run --rm --user 0 --entrypoint ls -v sard_sard-pki:/pki:ro "$IMAGE" -A /pki` → нет `ca`.
22. Вернуть CA из копии: `QA_SRC="$QA/gbak" $DC -f "$QA/override.yml" up -d --force-recreate --wait server`
    → `healthy`; `CA imported from` с `fingerprint=$G2`; `pkicrt $QA/n.crt; fp $QA/n.crt` → `$G2`;
    `$DC up -d --wait` → `self-agent` снова `online` (повторять до 3 минут).
23. Вернуть стенд без override: `$DC up -d --force-recreate --wait server` → `healthy`, отпечаток `$G2`.

## Часть 4. Восстановление доступа `[Р10]`

Стенд после части 1 (мастер пройден, пароль `$PW`).

1. `post session -c $QA/r1 -d "{\"password\":\"$PW\"}" >/dev/null`.
2. `$DC run --rm server admin-reset; echo "exit=$?"`
   → `exit=0`; вывод говорит, что пароль администратора удалён, и называет
   `docker compose restart server`; хэша в выводе нет; порты не публикуются.
3. До перезапуска: вход паролем `$PW` → `409` `setup_required`;
   `curl -sS -o /dev/null -w '%{http_code}\n' -b $QA/r1 $API/session` → `200`;
   `curl -sS $API/onboarding | jq -c '[.steps[1].state,.setupCode]'` → `["pending","not_issued"]`.
4. `$DC restart server` → в логе новая строка кода; `r1` → `401`;
   `curl -sS $API/onboarding | jq -c '[.steps[].state]'` → `["done","pending","upcoming","upcoming"]`.
5. Ввести код, шаг admin с `recovered-password-1` (шаг CA не нужен) → `204`;
   вход прежним паролем → `401`, новым → `204`; агенты и источники на месте.
6. `$DC run --rm server admin-reset` два раза подряд → оба `exit=0`; первый
   вывод — пароль удалён, второй — пароль администратора не задан. Пройти
   мастер снова (`$DC restart server`, код, шаг admin).
7. `$DC stop postgres; $DC run --rm --no-deps server admin-reset; echo "exit=$?"`
   → `exit=1`, вывод называет базу и говорит, что ничего не изменено.
   `$DC start postgres`; вход паролем → `204`.
8. `$DC run --rm server admin-rest; echo "exit=$?"` → `exit=2`, вывод называет `admin-reset`.
9. Доступ без хоста: с другой машины сети, у которой есть только порт 8080, нет
   ни одной операции, открывающей шаг admin: `jq -r '.paths | keys[]' web/src/api/openapi.json`
   → среди путей с паролем только `/api/v1/onboarding/admin` и
   `/api/v1/session/password`.

## Часть 5. Установка, автоматизация, документация

1. `grep -c SARD_ADMIN_PASSWORD deploy/docker-compose.yml deploy/.env.example` → `0` и `0`.
   `$DC config >/dev/null; echo $?` → `0`.
2. `ls scripts/ensure-admin-password.sh` → нет файла; `grep -n ADMIN_PASSWORD Makefile` → пусто.
3. `git grep -l SARD_ADMIN_PASSWORD -- ':!docs/sessions' ':!docs/adr' ':!docs/specs' ':!docs/qa' ':!docs/open-questions.md'`
   → только тест сценария «Переменная SARD_ADMIN_PASSWORD в окружении не
   становится паролем» и тест поиска этой строки; кода сервера среди них нет.
4. `[OQ-189]` На стенде с пройденным мастером (`PW` — пароль, заданный в
   мастере последним): override с
   `environment: {SARD_ADMIN_PASSWORD: old-env-password-1}`,
   `up -d --force-recreate --wait server` → `healthy`;
   `$DC logs --since 1m server 2>&1 | grep -cE 'SARD_ADMIN_PASSWORD|old-env-password-1'` → `0`;
   вход паролем `old-env-password-1` → `401`, паролем `$PW` → `204`. Вернуть стенд
   без override.
5. README: блок между `quickstart:begin` и `quickstart:end` без
   `SARD_ADMIN_PASSWORD`; после него — как найти код (`docker compose logs server`,
   `SARD SETUP CODE`) и открыть `/setup`.
   Чистая ВМ: выполнить блок дословно, затем найти код и пройти мастер в браузере
   → вход в консоль.
6. `docs/operator/02-install.md` — первый запуск по коду; `03-configuration.md` —
   нет `SARD_ADMIN_PASSWORD`; `10-security.md` — Argon2id, смена пароля в
   консоли, раздел «Восстановление доступа» с `admin-reset` и `restart`;
   `07-upgrade.md` — установка версии до 0.1.0-beta (0.0.1-rc1) не
   обновляется, а переустанавливается с новыми томами (`docker compose down -v`),
   а обновление с 0.1.0-beta.N и новее сохраняет данные, пароль администратора и
   пройденный мастер;
   `09-troubleshooting.md` — `setup_required`, `setup_completed`, ссылка на
   восстановление доступа; `CA_ORIGIN_NOT_RECORDED` и `CA_MISSING` `[OQ-199]` —
   восстановить тома базы и CA из бэкапа одного момента или переустановить их
   вместе (`docker compose down -v`), у `CA_MISSING` — вернуть CA через
   `SARD_PKI_IMPORT_DIR`; `docs/demo.md` — первый запуск по коду.
7. `scripts/smoke-server.sh` на чистом стенде → находит код в
   `docker compose logs server`, проходит мастер, входит; код выхода `0`.
8. `make e2e` → тесты сценариев `@e2e` серверной спецификации проходят
   (чистая установка, восстановление доступа, sard-self после шага ca).
9. ADR F4a есть в `docs/adr/` и `docs/adr/README.md`; разделы «Контекст»,
   «Решение», «Отвергнуто», «Последствия»; «Решение» называет отказы
   `CA_ORIGIN_NOT_RECORDED` и `CA_MISSING`; «Последствия» говорят, что установка
   версии до 0.1.0-beta переустанавливается с новыми томами, а с 0.1.0-beta.N
   обновление сохраняет состояние онбординга; ADR 0021 помечен как
   частично заменённый, ADR 0052 ссылается на F4a.
