# QA: сервер отдаёт консоль (S10)

Сценарии: `docs/specs/server/console-serving.feature`. Решения Р1–Р10 — в
заголовке спецификации; пометка `[Р…]` у шага называет решение, которое он
проверяет. Ожидаемый результат указан после «→». Любое расхождение — дефект.

## Решения и открытые вопросы для владельца

Решение владельца, не обсуждается: консоль отдаёт sard-server, она входит в
опубликованный образ, отдельного обратного прокси нет.

Предложенные решения (подробно — в заголовке спецификации):

- **Р1.** Набор консоли — внутри jar (`console/` на пути классов); Gradle
  включает его только по `-PsardConsoleDist=<каталог>`, без свойства jar без
  консоли и Node не нужен; каталог без `index.html` — ошибка сборки.
- **Р2.** Dockerfile: стадия `web` на `$BUILDPLATFORM` (Node 24) собирает
  `web/dist` из того же контекста; jar собирается с ним; в образе только jar.
  `.dockerignore` открывает `web/` без `node_modules` и `dist`; `make build`
  собирает web до jar.
- **Р3.** Без консоли пути консоли — 404, одна запись INFO при старте.
- **Р4.** SPA-fallback: только GET и HEAD; пути без точки в последнем сегменте и
  не под `/assets/` получают `index.html`; Accept не учитывается; `/api`,
  `/actuator`, `/v3` исключены по сегменту; отсутствующий файл — 404; прочие
  методы — 405 `Allow: GET, HEAD`; вне набора ничего не отдаётся.
- **Р5.** `index.html` и файлы корня — `no-cache`; `/assets/*` —
  `public, max-age=31536000, immutable`.
- **Р6.** На ответах консоли: `nosniff`, `X-Frame-Options: DENY`,
  `Referrer-Policy: same-origin`, CSP (точное значение — в спецификации). HSTS нет.
- **Р7.** `index.html` несёт `<meta name="sard-version">` из `SARD_VERSION`
  сборки; в образе равна версии `/api/v1/status`.
- **Р8.** Консоль без сессии, без `Set-Cookie`, не продлевает сессию, работает
  без базы.
- **Р9.** CI `image`, `release.yml`, `make e2e-images` — тот же Dockerfile;
  e2e и `smoke-server.sh` проверяют консоль в образе.
- **Р10.** Новый ADR; ADR 0036 ссылается на него.

Открытые вопросы (у каждого — рекомендация):

- **О1.** CSP `style-src 'unsafe-inline'`: Mantine вставляет элементы `style` и
  атрибуты `style`, nonce для статической страницы невозможен. Рекомендация:
  принять; скрипты остаются только `'self'`.
- **О2.** Заголовки безопасности только на ответах консоли или также на API
  (`nosniff`, `frame-ancestors` для JSON)? Рекомендация: только консоль в этой
  фиче, API — отдельно, если понадобится.
- **О3.** Сжатие (gzip/br) и условные запросы (ETag) не входят. Перед первой
  площадкой стоит Cloudflare, он сжимает сам. Рекомендация: не делать сейчас.
- **О4.** `meta sard-version` раскрывает версию без входа. Она и так публична в
  `GET /api/v1/status`. Рекомендация: принять — это единственный проверяемый
  признак, что консоль и сервер из одной сборки.
- **О5.** `sard.console.location` — внутреннее свойство (нужно тестам), в
  документации для операторов не описывается и подмену консоли не
  поддерживает. Рекомендация: так и оставить.
- **О6.** Методы, кроме GET и HEAD, на путях консоли — 405 (а не 404).
  Рекомендация: 405 с `Allow`.
- **О7.** Неизвестный путь под `/api/v1` с сессией сейчас отвечает 404
  `application/problem+json` от Spring, без поля `code` схемы Problem.
  Спецификация фиксирует только 404 и `application/problem+json`, а не HTML.
  Привести ответ к Problem с `code: "not_found"` — изменение контракта.
  Рекомендация: отдельная задача.

Известное следствие Р4: параметр маршрута с точкой (например, имя вместо UUID)
не получит `index.html`. Сейчас все параметры маршрутов — UUID.

## Подготовка

Нужны: Docker с buildx, `curl`, `jq`, `unzip`, JDK 25, Node 24, Chromium или
Firefox. Команды запускаются из корня репозитория.

```bash
V=0.0.0-qa
docker build -f deploy/server/Dockerfile --build-arg SARD_VERSION=$V -t sard-server:$V .
make down; [ -f deploy/.env ] || ./scripts/ensure-admin-password.sh
DC="env SARD_IMAGE=sard-server SARD_VERSION=$V docker compose -f deploy/docker-compose.yml --env-file deploy/.env"
$DC up -d --wait
H=http://localhost:8080
PW=$(sed -n 's/^SARD_ADMIN_PASSWORD=//p' deploy/.env)
QA=$(mktemp -d)
hdr() { curl -sS -o /dev/null -D - "$@"; }      # только заголовки
code() { curl -sS -o /dev/null -w '%{http_code} %{content_type}\n' "$@"; }
```

→ сборка образа успешна; `$DC ps` — сервер `healthy`.

## Часть 1. Образ

1. `[Р2]` `docker run --rm --entrypoint bash sard-server:$V -c 'ls -A /app; command -v node || echo no-node'`
   → `sard-server.jar` и `no-node`.
2. `[Р1]` `docker run --rm --entrypoint bash sard-server:$V -c 'cat /app/sard-server.jar' > $QA/s.jar; unzip -l $QA/s.jar | grep -E 'console/(index.html|assets/)' | head`
   → есть `BOOT-INF/classes/console/index.html` и файлы `console/assets/…`;
   `unzip -l $QA/s.jar | grep -c mockServiceWorker` → `0`.
3. `[Р7]` `unzip -p $QA/s.jar BOOT-INF/classes/console/index.html | grep -o '<meta name="sard-version"[^>]*>'`
   → `content="0.0.0-qa"`; `curl -sS $H/api/v1/status | jq -r .version` → `0.0.0-qa`.
4. `[Р9]` Мультиархитектура (нужен QEMU, в облачной среде Claude Code не
   проверить — тогда смотреть результат задания `image` в CI):
   `for a in amd64 arm64; do docker buildx build --platform linux/$a --build-arg SARD_VERSION=$V -f deploy/server/Dockerfile -t sard-server:$V-$a --load . && docker run --rm --platform linux/$a --entrypoint sha256sum sard-server:$V-$a /app/sard-server.jar; done`
   → обе суммы совпадают.
5. Сборка за TLS-прокси (если есть): `docker buildx build --secret id=build-ca,src=<CA.pem> --build-arg JAVA_TOOL_OPTIONS=… -f deploy/server/Dockerfile .`
   → стадия `web` (`npm ci`) и стадия `build` проходят `[Р2]`.

## Часть 2. HTTP

### Страница и глубокие ссылки `[Р4]`

6. `curl -sS $H/ | head -c 400; code $H/`
   → HTML с `<div id="root">` и `sard-version`; `200 text/html;charset=UTF-8`.
7. Для `/login`, `/login?redirect=%2Fagents`, `/agents`, `/agents/`,
   `/agents/0192f7a0-0000-7000-8000-000000000101`, `/sources/new`,
   `/sources/0192f7a0-0000-7000-8000-000000000101/edit`, `/runs?status=failed`,
   `/tokens`, `/no-such-page/deeper`, `/apiary`, `/index.html`:
   `curl -sS "$H<путь>" | cmp - <(curl -sS $H/) && echo same`
   → каждый раз `same`.
8. `curl -sS -H 'Accept: application/json' $H/agents | cmp - <(curl -sS $H/) && echo same` → `same`.
9. `curl -sS -I $H/agents/0192f7a0-0000-7000-8000-000000000101`
   → `200`, `text/html`, `Cache-Control: no-cache`; `curl -sS --head … | wc -c` —
   только заголовки.

### Файлы набора

10. `A=$(curl -sS $H/ | grep -oE '/assets/[^"]+\.js' | head -1); C=$(curl -sS $H/ | grep -oE '/assets/[^"]+\.css' | head -1); hdr $H$A; hdr $H$C; hdr $H/favicon.svg`
    → `200`; JS — `text/javascript`, CSS — `text/css`, SVG — `image/svg+xml`;
    у `$A` и `$C` `Cache-Control: public, max-age=31536000, immutable` `[Р5]`; у
    `favicon.svg` — `no-cache`.
11. Для `/assets/index-Zz00Zz00.js`, `/assets/missing.css`, `/assets/no-extension`,
    `/robots.txt`, `/agents/report.pdf`, `/mockServiceWorker.js`: `code "$H<путь>"`
    → каждый `404`, тип не `text/html`; `hdr $H/assets/index-Zz00Zz00.js | grep -ci immutable` → `0`.
12. Для `/application.yaml`, `/BOOT-INF/classes/application.yaml`,
    `/META-INF/MANIFEST.MF`, `/console/index.html`: `code "$H<путь>"` → `404`.
13. Обход каталога, без нормализации пути клиентом:
    `for p in '/assets/../application.yaml' '/assets/%2e%2e/application.yaml' '/assets/%2e%2e%2f%2e%2e%2fapplication.yaml' '/%2e%2e/application.yaml'; do curl -sS --path-as-is -w ' %{http_code}\n' "$H$p" | grep -c datasource; done`
    → для каждого `0` и статус не `200`.

### Методы `[Р4]` `[О6]`

14. Для `POST /`, `PUT /agents`, `DELETE /agents/<uuid>`, `DELETE /favicon.svg`,
    `PATCH /index.html`, `OPTIONS /login`, `POST $A`:
    `curl -sS -o /dev/null -D - -X <метод> "$H<путь>" | grep -iE '^HTTP|^allow'`
    → `405`, `Allow: GET, HEAD` (порядок не важен).

### API, Actuator, OpenAPI не перекрыты

15. `code $H/api/v1/no-such-resource` → `401 application/problem+json`.
16. `curl -sS -c $QA/j -H 'Content-Type: application/json' -d "{\"password\":\"$PW\"}" $H/api/v1/session; code -b $QA/j $H/api/v1/no-such-resource`
    → `404 application/problem+json` `[О7]`.
17. `code $H/api; code $H/api/; code $H/api/v2/agents` → каждый `404 application/problem+json`.
18. `code $H/api/v1/status; code $H/actuator/health; code $H/v3/api-docs`
    → `200 application/json` у каждого (у health — `application/vnd.spring-boot.actuator.v3+json` или `application/json`).
19. `code $H/actuator/no-such; code $H/actuator/env; code $H/v3/no-such; code $H/v3` → каждый `404`, тип не `text/html`.
20. `curl -sS $H/v3/api-docs | jq -r '.paths | keys[]' | grep -vc '^/api/v1/'` → `0`;
    `curl -sS $H/v3/api-docs | jq -S . | diff - <(jq -S . web/src/api/openapi.json) && echo same` → `same`.

### Заголовки безопасности `[Р6]`

21. Для `/`, `/agents/<uuid>`, `$A`, `/favicon.svg`:
    `hdr "$H<путь>" | grep -iE '^(x-content-type-options|x-frame-options|referrer-policy|content-security-policy):'`
    → `nosniff`, `DENY`, `same-origin`, CSP ровно
    `default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'`.
    `hdr $H/ | grep -ci strict-transport-security` → `0`.

### Вход и сессия `[Р8]`

22. `hdr $H/ | grep -ci set-cookie; hdr -b 'sard_session=forged-session-id' $H/agents | grep -ci set-cookie; hdr -b $QA/j $H$A | grep -ci set-cookie`
    → `0`, `0`, `0`; первые два ответа — `200 text/html`.
23. `code $H/api/v1/agents` → `401 application/problem+json`.
24. `$DC stop postgres; code $H/agents; $DC start postgres` → `200 text/html;charset=UTF-8`.

## Часть 3. Консоль в браузере

Открыть `http://localhost:8080/` (не `:5173`). DevTools открыты: Console и
Network, «Preserve log» включён.

25. `/` без сессии → страница входа; войти паролем `$PW` → главная.
26. Пройти главную, агенты, карточку агента, источники, новый источник,
    редактирование источника, запуски, карточку запуска, токены; на каждой
    странице нажать F5 → после перезагрузки та же страница, не 404 и не пустой экран.
27. Во всех шагах 25–26 в Console нет сообщений о нарушении Content Security
    Policy и ошибок загрузки; шрифты IBM Plex Sans и JetBrains Mono применены
    (DevTools → Computed → font-family у текста и моноширинных значений) `[Р6]` `[О1]`.
28. Network → нет запросов к `/mockServiceWorker.js` и к хостам, кроме
    `localhost:8080`; Application → Service Workers пуст.
29. Выйти, затем открыть вручную `http://localhost:8080/runs?status=failed`
    → `/login?redirect=%2Fruns%3Fstatus%3Dfailed`; после входа — страница запусков с фильтром.
30. Повторная загрузка главной (F5): в Network `index.html` — `200` с
    `Cache-Control: no-cache`, файлы `/assets/*` — из кэша (memory/disk cache) `[Р5]`.
31. `$DC restart server`, обновить страницу → консоль загружается, сессия
    завершена, открыт вход.

## Часть 4. Сборка без консоли и с консолью

32. `[Р1]` `env PATH=$(echo $PATH | tr : '\n' | grep -v -e node -e npm | paste -sd:) LC_ALL=C.UTF-8 ./gradlew :server:test`
    (убедиться, что `command -v node` в этом окружении пусто) → успешно.
33. `./gradlew -q :server:bootJar && unzip -l server/build/libs/sard-server.jar | grep -c 'console/index.html'` → `0`.
34. `[Р3]` Запустить этот jar в сети compose рядом с базой, на порту 8081:
    `docker run -d --name sard-nc --network sard_default -p 127.0.0.1:8081:8080 -v $PWD/server/build/libs:/app:ro -e SARD_ADMIN_PASSWORD=$PW -e SARD_DB_URL=jdbc:postgresql://postgres:5432/sard -e SARD_DB_PASSWORD=$(sed -n 's/^SARD_DB_PASSWORD=//p' deploy/.env) -e SARD_PKI_DIR=/tmp/pki eclipse-temurin:25-jre java -jar /app/sard-server.jar; sleep 30`
    → `code localhost:8081/` и `code localhost:8081/agents` — `404`, тип не `text/html`;
    `code localhost:8081/api/v1/status` — `200`;
    `docker logs sard-nc 2>&1 | grep -i console` → ровно одна запись INFO о том,
    что консоль не включена в сборку. `docker rm -f sard-nc`.
35. `mkdir -p $QA/empty; ./gradlew -q :server:bootJar -PsardConsoleDist=$QA/empty`
    → ошибка сборки; сообщение называет `sardConsoleDist` и `$QA/empty`.
36. `make build VERSION=$V && unzip -p server/build/libs/sard-server.jar BOOT-INF/classes/console/index.html | grep -o 'sard-version" content="[^"]*"'`
    → `sard-version" content="0.0.0-qa"`.
37. `cd web && npm run dev` → консоль на `http://localhost:5173` работает как
    прежде, против сервера `make up` и с `VITE_API_MOCKS=1` (поведение
    разработки не изменилось).

## Часть 5. CI, релиз, документация

38. `[Р9]` `make e2e` → зелёный, включая тесты `@e2e` этой фичи.
39. `scripts/smoke-server.sh deploy/.env 0.0.0-qa` (при запущенном `$DC`)
    → строки `ok:` для health, версии, входа, консоли (с версией `0.0.0-qa`) и
    порта агентов. С неверной версией (`… 9.9.9`) скрипт завершается `FAIL`.
40. `.github/workflows/ci.yml` (задание `image`) и `release.yml` собирают образ
    тем же `deploy/server/Dockerfile` без отдельного шага сборки web; после
    первого релиза с этой фичей задания `quickstart` и `offline` проходят
    `smoke-server.sh` с проверкой консоли.
41. `[Р10]` Новый ADR о раздаче консоли → разделы «Контекст», «Решение»,
    «Рассмотренные альтернативы» (отдельный прокси, каталог в образе вместо
    jar, выбор по Accept), «Последствия»; есть в `docs/adr/README.md`; в
    ADR 0036 пункт «Консоль в образ пока не входит» заменён ссылкой на него.

Завершение: `$DC down; docker image rm sard-server:$V; rm -rf $QA`.
