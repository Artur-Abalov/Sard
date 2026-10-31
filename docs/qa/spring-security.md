<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# QA: HTTP-безопасность сервера на Spring Security (CORE1, редакция 5)

Спецификация: `docs/specs/server/spring-security.feature` (сценарии с тегом
`@qa`; решения Р34–Р53, ответы владельца на В-1–В-8 от 2026-10-31).
Процедура проходится через HTTP (`curl`, `jq`), `grpcurl` и `docker compose`;
консоль не нужна.

Главное, что видит тестировщик: снаружи почти ничего не изменилось. Видимых
изменений два: ответы `/api/v1` несут `X-Content-Type-Options: nosniff` и
`Cache-Control: no-store` (Р47), а пути, которые нельзя однозначно
сопоставить, отклоняются ответом `400` (Р51). Остальные шаги проверяют места,
где переход на Spring Security мог бы что-то изменить.

## Подготовка

- Два стенда на чистых томах, как в `docs/qa/server-core-extensions.md`,
  раздел «Подготовка»: **Ядро** (`make up` из текущей ветки, с консолью) и
  **Образец** (`examples/extension-sample`).
- На стенде ядра мастер первого запуска пройден, пароль администратора
  `correct-horse-battery`, зарегистрирован агент с hostname `qa`.
- Переменные: `S=http://localhost:8080`, `J=/tmp/qa-cookies`,
  `P=correct-horse-battery`, `H=/tmp/qa-headers`.
- Вход: `curl -s -c $J -D $H -X POST $S/api/v1/session -H 'Content-Type: application/json' -d "{\"password\":\"$P\"}" -o /dev/null -w '%{http_code}\n'`
  → `204`.
- `AGENT=$(curl -s -b $J "$S/api/v1/agents" | jq -r '.items[] | select(.hostname == "qa") | .id'); echo $AGENT`
  → UUID. Пустое значение — стенд не подготовлен, дальше не идти.
- Проверка двух заголовков API — функция оболочки, ей передаются аргументы
  `curl` без `-D` и `-o`:

  ```bash
  apihdr() {
    curl -s -D - -o /dev/null "$@" | tr -d '\r' \
      | grep -iE '^(HTTP|x-content-type-options|cache-control):' \
      | sed -E 's/^(HTTP\/[0-9.]+ [0-9]+).*/\1/' | tr 'A-Z' 'a-z'
  }
  ```

  «Заголовки API» в ожиданиях ниже — после строки статуса ровно две строки,
  `x-content-type-options: nosniff` и `cache-control: no-store`, каждая один
  раз.
- Новые свойства образца задаются переменными окружения сервера образца:
  `SARD_SAMPLE_PUBLIC_PING`, `SARD_SAMPLE_OWN_CHAIN`,
  `SARD_SAMPLE_PUBLIC_OUTSIDE`. Каждое изменение — перезапуск сервера образца
  (`docker compose up -d --force-recreate server`), затем дождаться
  `GET /actuator/health` = `UP` и войти заново.

## A. Нет чужих cookie, сессий контейнера и пароля по умолчанию (Р34)

1. `grep -ci 'set-cookie' $H; grep -i 'set-cookie' $H | cut -d= -f1`
   после входа из подготовки. Ожидается: `1` и `Set-Cookie: sard_session`.
2. Пройти по путям и собрать все cookie:

   ```bash
   for p in / /agents /actuator/health /v3/api-docs /api/v1/status /api/v1/sources /api/v1/no-such-resource; do
     curl -s -o /dev/null -D - "$S$p"; curl -s -o /dev/null -D - -b $J "$S$p"
   done | grep -i '^set-cookie' | sort -u
   ```

   Ожидается: пустой вывод (ни `JSESSIONID`, ни `XSRF-TOKEN`, ни иных).
3. `docker compose logs server | grep -c 'Using generated security password'`.
   Ожидается: `0`.
4. `docker compose logs server | grep -E 'WARN|ERROR' | grep -c 'org.springframework.security'`.
   Ожидается: `0`.
5. `curl -s -u "admin:$P" $S/api/v1/sources -D - -o /tmp/body | grep -ci '^www-authenticate'; jq -r .code /tmp/body`.
   Ожидается: `0` и `unauthenticated` (HTTP Basic с верным паролем не входит).
6. `curl -s -H 'Authorization: Bearer forged-token' $S/api/v1/sources -o /tmp/body -w '%{http_code}\n'; jq -r .code /tmp/body`.
   Ожидается: `401` и `unauthenticated`.
7. Пути Spring Security — это пути консоли:
   - `curl -s -b $J $S/login | grep -c 'name="sard-version"'` → `1`
     (страница консоли, а не форма входа Spring);
   - `curl -s -b $J $S/logout | grep -c 'name="sard-version"'` → `1`;
   - `curl -s -b $J -X POST $S/logout -D - -o /dev/null | grep -iE '^(HTTP|allow)'`
     → `405` и `Allow: GET, HEAD` (в любом порядке);
   - `curl -s -b $J $S/api/v1/session -o /dev/null -w '%{http_code}\n'` → `200`
     (сессия после этого действует).

## B. Заголовки ответов (Р47)

8. Ответы `/api/v1` несут заголовки API. Для каждой строки ожидается указанный
   статус и заголовки API:

   | Команда | Статус |
   |---|---|
   | `apihdr $S/api/v1/status` | `200` |
   | `apihdr -b $J $S/api/v1/session` | `200` |
   | `apihdr $S/api/v1/sources` | `401` |
   | `apihdr -X DELETE $S/api/v1/session` | `401` |
   | `apihdr -b $J -X POST $S/api/v1/sources -H 'Origin: https://evil.example' -H 'Content-Type: application/json' -d '{}'` | `403` |
   | `apihdr -b $J $S/api/v1/no-such-resource` | `404` |
   | `apihdr -X POST $S/api/v1/session -H 'Content-Type: application/json' -d "{\"password\":\"$P\"}"` | `204` |

9. Блокировка входа: с одного адреса шесть раз
   `apihdr -X POST $S/api/v1/session -H 'Content-Type: application/json' -d '{"password":"wrong-password-123"}'`.
   Ожидается: в шестом ответе `429` и заголовки API. После шага подождать
   15 минут или перезапустить сервер и войти заново.
10. Прочих заголовков Spring Security у ответов API нет:
    `curl -s -D - -o /dev/null $S/api/v1/status | grep -iEc '^(pragma|expires|x-frame-options|x-xss-protection|strict-transport-security):'`
    и то же для `$S/api/v1/sources`. Ожидается: `0` в обоих случаях.
11. Ответы вне `/api/v1` заголовков API не получают:
    `for p in /actuator/health /v3/api-docs /api/v2/agents /api; do curl -s -D - -o /dev/null $S$p | grep -iEc '^(cache-control|x-content-type-options):'; done`.
    Ожидается: четыре строки `0`.
12. `curl -s -D - -o /dev/null $S/ | grep -iE '^(cache-control|x-frame-options|x-content-type-options|referrer-policy|content-security-policy|pragma|expires|x-xss-protection):'`.
    Ожидается ровно пять строк: `Cache-Control: no-cache`,
    `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`,
    `Referrer-Policy: same-origin` и `Content-Security-Policy` со значением
    из `console-serving.feature` Р6; ни одного заголовка дважды; `Pragma`,
    `Expires`, `X-XSS-Protection` нет.
13. Взять имя скрипта из страницы:
    `A=$(curl -s $S/ | grep -o '/assets/[^"]*\.js' | head -1); curl -s -D - -o /dev/null $S$A | grep -i '^cache-control'`.
    Ожидается одна строка `Cache-Control: public, max-age=31536000, immutable`.
14. `curl -s -D - -o /dev/null $S/downloads/agent/manifest.json | grep -iE '^(cache-control|x-content-type-options|x-frame-options|pragma|expires|content-security-policy):'`.
    Ожидается: по одной строке `Cache-Control` (не `no-store`) и
    `X-Content-Type-Options: nosniff`, других нет.

## C. Порядок проверок, открытые пути и продление сессии (Р35, Р37, Р52, Р53)

15. `curl -s -X POST $S/api/v1/sources -H 'Origin: https://evil.example' -H 'Content-Type: application/json' -d '{}' -o /tmp/body -w '%{http_code}\n'; jq -r .code /tmp/body`
    без cookie. Ожидается: `403` и `origin_rejected` (не `401`).
16. `curl -s -X POST $S/agents -H 'Origin: https://evil.example' -o /dev/null -w '%{http_code}\n'`.
    Ожидается: `405` (на путь консоли проверка Origin не действует).
17. Для каждого из `GET /api/v1/status/`, `GET /api/v1/STATUS`,
    `POST /api/v1/status`: `curl -s -X <метод> $S<путь> -o /dev/null -w '%{http_code}\n'`
    без cookie, и `curl -s -I $S/api/v1/status -o /dev/null -w '%{http_code}\n'`.
    Ожидается: `401` каждый раз; и
    `curl -s $S/api/v1/status -o /dev/null -w '%{http_code}\n'` → `200`.
18. `curl -s -b 'sard_session=forged-session-id' -D - -o /dev/null $S/actuator/health | grep -iE '^(HTTP|set-cookie)'`.
    Ожидается: `200` и ни одной строки `Set-Cookie`.
19. Две cookie сессии: `SID=$(awk '$6=="sard_session"{print $7}' $J)`;
    `curl -s -H "Cookie: sard_session=$SID; sard_session=forged-session-id" $S/api/v1/sources -o /dev/null -w '%{http_code}\n'`
    → `200`; `curl -s -H "Cookie: sard_session=forged-session-id; sard_session=$SID" $S/api/v1/sources -o /dev/null -w '%{http_code}\n'`
    → `401`.
20. Продление (сроки — час бездействия; шаг занимает больше часа, его можно
    пройти параллельно с остальными на отдельной сессии). Войти вторым файлом
    cookie `J2`; в течение часа каждые 10 минут выполнять только
    `curl -s -b $J2 $S/api/v1/status -o /dev/null`; через 60 минут после входа
    `curl -s -b $J2 $S/api/v1/session -o /dev/null -w '%{http_code}\n'` → `401`
    (статус сессию не продлевает). То же с `GET /api/v1/onboarding` вместо
    статуса → `200`.

## D. Политика спрашивается только об операциях (Р40)

Стенд образца, `SARD_SAMPLE_DENY='GET /api/v1/sources'`, перезапуск, вход.

21. `apihdr -b $J $S/api/v1/sources; curl -s -b $J $S/api/v1/sources -D - -o /tmp/body | grep -i '^content-type'; jq -r .code /tmp/body`.
    Ожидается: `403` с заголовками API, `application/problem+json`,
    `access_denied`.
22. `curl -s -b $J $S/api/v1/no-such-resource -o /tmp/body -w '%{http_code}\n'; jq -r .code /tmp/body`.
    Ожидается: `404` и не `access_denied` (политику о неизвестном пути не
    спрашивают).

## E. Шов настройщика цепочки на образце (Р41, Р44)

23. Стенд образца, `SARD_SAMPLE_PUBLIC_PING=true`, перезапуск.
    `apihdr $S/api/v1/sample/ping` без cookie → `204` и заголовки API.
    `curl -s $S/api/v1/sample/ping/deeper -o /dev/null -w '%{http_code}\n'` → `401`.
24. `SARD_SAMPLE_PUBLIC_PING=false`, `SARD_SAMPLE_OWN_CHAIN=true`, перезапуск.
    `docker compose ps server` — сервер не работает;
    `docker compose logs server | grep -c SardSecurityCustomizer` → не меньше `1`,
    и в той же записи об отказе есть `sample`.
25. `SARD_SAMPLE_OWN_CHAIN=false`, `SARD_SAMPLE_PUBLIC_OUTSIDE=true`, перезапуск.
    Сервер не работает; `docker compose logs server | grep -c 'GET /sample/open'`
    → не меньше `1`.
26. `SARD_SAMPLE_PUBLIC_OUTSIDE=false`, перезапуск. `GET /actuator/health` → `UP`.

## F. Агенты по gRPC (Р45)

Стенд ядра.

27. `curl -s -b $J $S/api/v1/agents/$AGENT | jq .` — агент `qa` показан
    подключённым (как до перехода); на хосте агента
    `journalctl -u sard-agent --since -10min | grep -ci 'unauthenticated'` → `0`.
28. Вызов любого метода `AgentService`, кроме `Register`, через `grpcurl -v`
    по TLS без клиентского сертификата. Ожидается: статус `Unauthenticated` с
    текстом `agent certificate rejected` и `ErrorInfo` домена `sard.dev`,
    причина `CERT_MISSING`.

## G. Брандмауэр путей (Р51, Р47)

29. Для каждого пути `/api;x/v1/status`, `/api/v1;x/status`,
    `/api/v1/%2e%2e/v1/status`, `/assets/%2e%2e/application.yaml`,
    `/;x/actuator/health`:
    `apihdr --path-as-is -b $J "$S<путь>"` и
    `curl -s --path-as-is -b $J "$S<путь>" | wc -c`.
    Ожидается: `400`, заголовки API и `0` (тело пустое).

## Итог

Все шаги дали ожидаемое — переход на Spring Security снаружи виден только
двумя заголовками API и отказом `400` для неоднозначных путей, а шов расширений
работает. Любое расхождение — дефект с номером шага и выводом команды.
