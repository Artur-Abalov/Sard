<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# QA: HTTP-безопасность сервера на Spring Security (CORE1, редакция 5)

Спецификация: `docs/specs/server/spring-security.feature` (сценарии с тегом
`@qa`; решения Р34–Р50). Процедура проходится через HTTP (`curl`, `jq`),
`grpcurl` и `docker compose`; консоль не нужна. Шаги, помеченные **[В-n]**,
проверяют рекомендацию по вопросу владельца В-n: если владелец ответил
иначе, шаг правится вместе со спецификацией.

Главное, что видит тестировщик: снаружи ничего не изменилось. Каждый шаг
ниже — место, где переход на Spring Security мог бы что-то изменить.

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

## B. Заголовки ответов прежние (Р47)

8. `curl -s -D - -o /dev/null $S/api/v1/status | grep -iEc '^(cache-control|pragma|expires|x-content-type-options|x-frame-options|x-xss-protection|strict-transport-security):'`.
   Ожидается: `0`.
9. То же для `curl -s -D - -o /dev/null $S/api/v1/sources` (без cookie, 401)
   и `curl -s -D - -o /dev/null -b $J $S/api/v1/session` (200).
   Ожидается: `0` в обоих случаях.
10. `curl -s -D - -o /dev/null $S/ | grep -iE '^(cache-control|x-frame-options|x-content-type-options|referrer-policy|content-security-policy|pragma|expires|x-xss-protection):'`.
    Ожидается ровно пять строк: `Cache-Control: no-cache`,
    `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`,
    `Referrer-Policy: same-origin` и `Content-Security-Policy` со значением
    из `console-serving.feature` Р6; ни одного заголовка дважды; `Pragma`,
    `Expires`, `X-XSS-Protection` нет.
11. Взять имя скрипта из страницы:
    `A=$(curl -s $S/ | grep -o '/assets/[^"]*\.js' | head -1); curl -s -D - -o /dev/null $S$A | grep -i '^cache-control'`.
    Ожидается одна строка `Cache-Control: public, max-age=31536000, immutable`.
12. `curl -s -D - -o /dev/null $S/downloads/agent/manifest.json | grep -iE '^(cache-control|x-content-type-options|x-frame-options|pragma|expires|content-security-policy):'`.
    Ожидается: по одной строке `Cache-Control` и `X-Content-Type-Options: nosniff`,
    других нет.

## C. Порядок проверок и открытые пути (Р35, Р37)

13. `curl -s -X POST $S/api/v1/sources -H 'Origin: https://evil.example' -H 'Content-Type: application/json' -d '{}' -o /tmp/body -w '%{http_code}\n'; jq -r .code /tmp/body`
    без cookie. Ожидается: `403` и `origin_rejected` (не `401`).
14. `curl -s -X POST $S/agents -H 'Origin: https://evil.example' -o /dev/null -w '%{http_code}\n'`.
    Ожидается: `405` (на путь консоли проверка Origin не действует).
15. Для каждого из `GET /api/v1/status/`, `GET /api/v1/STATUS`,
    `HEAD /api/v1/status`, `POST /api/v1/status`:
    `curl -s -X <метод> $S<путь> -o /dev/null -w '%{http_code}\n'` без cookie
    (`HEAD` — через `curl -I`). Ожидается: `401` каждый раз; и
    `curl -s $S/api/v1/status -o /dev/null -w '%{http_code}\n'` → `200`.
16. `curl -s -b 'sard_session=forged-session-id' -D - -o /dev/null $S/actuator/health | grep -iE '^(HTTP|set-cookie)'`.
    Ожидается: `200` и ни одной строки `Set-Cookie`.

## D. Политика спрашивается только об операциях (Р40)

Стенд образца, `SARD_SAMPLE_DENY='GET /api/v1/sources'`, перезапуск, вход.

17. `curl -s -b $J $S/api/v1/sources -D - -o /tmp/body | grep -iE '^(HTTP|content-type)'; jq -r .code /tmp/body`.
    Ожидается: `403`, `application/problem+json`, `access_denied`.
18. `curl -s -b $J $S/api/v1/no-such-resource -o /tmp/body -w '%{http_code}\n'; jq -r .code /tmp/body`.
    Ожидается: `404` и не `access_denied` (политику о неизвестном пути не
    спрашивают).

## E. Шов настройщика цепочки на образце (Р41, Р44) [В-2, В-4]

19. Стенд образца, `SARD_SAMPLE_PUBLIC_PING=true`, перезапуск.
    `curl -s $S/api/v1/sample/ping -o /dev/null -w '%{http_code}\n'` без cookie.
    Ожидается: `204`. `curl -s $S/api/v1/sample/ping/deeper -o /dev/null -w '%{http_code}\n'`
    → `401`.
20. `SARD_SAMPLE_PUBLIC_PING=false`, `SARD_SAMPLE_OWN_CHAIN=true`, перезапуск.
    `docker compose ps server` — сервер не работает;
    `docker compose logs server | grep -c SardSecurityCustomizer` → не меньше `1`,
    и в той же записи об отказе есть `sample`.
21. `SARD_SAMPLE_OWN_CHAIN=false`, `SARD_SAMPLE_PUBLIC_OUTSIDE=true`, перезапуск.
    Сервер не работает; `docker compose logs server | grep -c 'GET /sample/open'`
    → не меньше `1`.
22. `SARD_SAMPLE_PUBLIC_OUTSIDE=false`, перезапуск. `GET /actuator/health` → `UP`.

## F. Агенты по gRPC (Р45)

Стенд ядра.

23. `curl -s -b $J $S/api/v1/agents/$AGENT | jq .` — агент `qa` показан
    подключённым (как до перехода); на хосте агента
    `journalctl -u sard-agent --since -10min | grep -ci 'unauthenticated'` → `0`.
24. `grpcurl -insecure localhost:9090 list` (TLS без клиентского сертификата)
    или вызов любого метода `AgentService`, кроме `Register`, без сертификата.
    Ожидается: статус `Unauthenticated` с текстом
    `agent certificate rejected` и `ErrorInfo` домена `sard.dev`, причина
    `CERT_MISSING` (`grpcurl -v` показывает детали).

## G. Брандмауэр путей [В-5]

25. `curl -s --path-as-is -b $J "$S/api;x/v1/status" -o /dev/null -w '%{http_code}\n'`
    и `curl -s --path-as-is -b $J "$S/api/v1/%2e%2e/v1/status" -o /dev/null -w '%{http_code}\n'`.
    Ожидается: `400` в обоих; тело не содержит `sard-version`.

## Итог

Все шаги дали ожидаемое — переход на Spring Security снаружи не виден, а шов
расширений работает. Любое расхождение — дефект с номером шага и выводом
команды.
