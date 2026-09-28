# Сессия 2026-09-28: W1b — вход администратора по паролю

Ветка: `claude/load-repository-7tynh5`. Спецификации: `docs/specs/server/admin-login.feature`,
`docs/specs/web/admin-login.feature`, `docs/qa/admin-login.md`. ADR 0020.

## Сервер (`dev.sard.server.auth`)

- `AdminPasswordAuthenticator`: валидирует `SARD_ADMIN_PASSWORD` (>= 12
  кодовых точек Unicode) в конструкторе — Spring создаёт синглтоны при
  старте контекста, поэтому невалидный пароль роняет старт естественно, без
  отдельного механизма. Сырое значение — параметр конструктора, не поле:
  тест обходит поля бинов рефлексией и не находит пароль. `matches` сравнивает
  SHA-256 через `MessageDigest.isEqual` (JDK документирует его как постоянное
  по времени, без раннего выхода).
- `SessionStore`: сессии — `ConcurrentHashMap`, идентификатор — 32 случайных
  байта (256 бит) в hex. `touch` — единственная точка чтения и продления;
  граница «12 ч бездействия» проверена на миллисекунду в обе стороны.
- `LoginAttemptTracker`: неудачи — `ArrayDeque<Instant>` на адрес, окно 15 мин.
  `withAddressLock` — отдельный метод: без него параллельные запросы читали
  бы «не заблокировано» до того, как первый из них запишет пятую неудачу
  (тест на 20 одновременных запросов ловил это самим первым прогоном).
- `OriginGuardFilter` и `SessionAuthFilter` — обычные `Filter`, зарегистрированы
  через `FilterRegistrationBean` с `urlPatterns=["/api/v1/*"]` и явным порядком
  (Origin раньше сессии). Оба пишут `Problem` напрямую в `HttpServletResponse`
  (`ProblemResponses.writeProblem`, через `outputStream`, не `writer` — иначе
  Tomcat дописывает `;charset=UTF-8` к `Content-Type`, а контракт требует
  точное значение).
- `SessionController`/`SessionApi`: методы принимают `HttpServletRequest`/
  `HttpServletResponse` и возвращают `ResponseEntity`/`Unit`, потому что статус
  (204/401/429) не определяется типом запроса. Тело без пароля — валидный JSON,
  `null` или вовсе не JSON — обрабатывается одним `@ExceptionHandler` в самом
  контроллере (`HttpMessageNotReadableException`), который вызывает тот же путь
  с паролем `""` (пароли короче 12 символов никогда не совпадают, поэтому
  пустая строка — безопасный признак «нечего проверять»).
- OpenAPI: `describeOriginRejected` — customizer по образцу
  `describeUnauthorized` (ADR 0019), добавляет 403 `origin_rejected` каждой
  операции POST/PUT/PATCH/DELETE, а не аннотацией на каждом контроллере.
  `ErrorCode.origin_rejected` добавлен в перечисление.
- `ApiContractIntegrationTest` («заглушки отвечают 501») обновлён: сессия
  реализована, поэтому её три операции исключены из списка заглушек, а
  остальные вызовы теперь идут с cookie (сервер защищает `/api/v1/*`).

### Тесты (все выполнялись, `./gradlew :server:test`)
- Юнит: `AdminPasswordAuthenticatorTest`, `SessionStoreTest`,
  `LoginAttemptTrackerTest`, `OriginGuardFilterTest`, `SessionAuthFilterTest`,
  `SessionCookiesTest`, `AdminPasswordStartupTest` (`ApplicationContextRunner`,
  без БД — только сама валидация и реальное разрешение свойств Spring),
  `DescribeOriginRejectedTest`.
- Интеграционные (`@SpringBootTest`, Testcontainers Postgres, `MovableClock`):
  `AdminLoginIntegrationTest` (52 сценария: вход, сессия, перебор, CSRF, защита
  API), `AdminLoginLoggingIntegrationTest` (журнал и отсутствие пароля/id
  сессии в нём), `NonAsciiPasswordIntegrationTest`, `PasswordRotationIntegrationTest`
  (эквивалент перезапуска — свой контекст с другим паролем),
  `DatabaseUnavailableIntegrationTest` (Р8: останавливает свой контейнер
  Postgres, `@DirtiesContext`, чтобы не портить закешированный контекст других
  классов).
- Тест на 20 параллельных неверных попыток нашёл настоящую гонку до
  `withAddressLock` (все 20 получали 401) — оставлен как регрессионный.

### Не сделано / расхождения со спецификацией
- **HTTPS для `Secure` на cookie** проверено не через настоящий TLS на HTTP-порту
  (его никто не поднимает — TLS есть только на gRPC-порту, ADR 0014), а через
  прямой юнит-тест `sessionCookie(id, secure=true)`. Реальной интеграционной
  проверки «сервер за HTTPS выдаёт `Secure`» нет.
- **Восстановление после сбоя базы** (`DatabaseUnavailableIntegrationTest`)
  проверяет вход и чтение сессии; полный сценарий QA (часть 1, шаги 1–5,
  перезапуск контейнера через `docker compose`) не прогонялся — это ручная
  процедура, не автоматизация.
- Адрес клиента в тестах — `127.0.0.1` (реальное TCP-соединение локального
  HTTP-клиента), а не `203.0.113.10` из истории спецификации: сама механика
  (адрес — из соединения, не из заголовков) проверена, конкретное значение —
  нет и не может быть без прокси перед тестом.

## Консоль (`web/src`)

- `auth/redirect.ts` (`resolveRedirectTarget`) и `auth/lockout.ts`
  (`formatLockoutMinutes`) — чистые функции с прямыми тестами.
- `auth/session.ts` (`checkSession`, `UnauthenticatedError`), `auth/guard.ts`
  (переписан: асинхронный, кидает `redirect()` на 401, пробрасывает прочие
  ошибки дальше для `errorComponent`), `auth/loginGuard.ts`
  (`redirectIfSignedIn` — вынесен из `routes/login.tsx`, потому что реальная
  навигация через `router.navigate()`/`router.load()` в тестах Vitest не
  доводила редирект до конца по неясной причине; тестируется сама функция,
  как уже тестировался `guard`).
- `auth/queryClient.ts` (`createAppQueryClient`) — общий `QueryClient` с
  `QueryCache.onError`: `UnauthenticatedError` чистит кэш и вызывает
  обработчик один раз даже при нескольких параллельных отказах (микрозадача
  гасит дубликаты). Реальных страниц с запросами, кроме дашборда (публичный
  статус), ещё нет — это инфраструктура для W2.
- `routes/login.tsx`, `pages/Login.tsx`, `pages/RouteError.tsx` — новые;
  `routes/_app.tsx` получил `errorComponent` (Р9в: сбой проверки — не вход).
  `routeTree.gen.ts` перегенерирован (`npm run gen:routes`).
- `Layout.tsx`: кнопка выхода в шапке (`DELETE /api/v1/session`,
  затем `/login` без `redirect`; сбой сети — сообщение об ошибке на месте).
- `router.test.ts` обновлён: маршруты `/_app/*` теперь требуют сессию, тесты
  подставляют мок `GET /api/v1/session` → 200 в `beforeEach`.
- Моки (К4): `mocks/state.ts` — `failedSignIns` стал массивом меток времени
  (скользящее окно 15 мин вместо счётчика); `mocks/api/session.ts` —
  `Retry-After: 900`, `expiresAt` = +12 ч, `Set-Cookie` с `Path=/`;
  `mocks/browser.ts` стартует без входа (пересматривает решение владельца 8
  в S8a); `mocks/origin.ts` — новый набор обработчиков (`POST`/`PUT`/`PATCH`/
  `DELETE`, не `http.all`: `http.all` перехватывал и `GET` без обработчика,
  ломая тест «необработанный запрос — ошибка, не сеть», потому что резолвер,
  вернувший `undefined`, у MSW уходит в реальную сеть, а не к следующему
  обработчику, когда handler вообще совпал по методу).
- `locales/{ru,en}.json` — ключи `login.*`, `app.logout`, `routeError.*`;
  `locales/locales.test.ts` сверяет множества путей ключей.
- `api/client.ts` — `client` экспортирован (раньше был приватным), нужен
  `Login`/`Layout`/`session.ts`.

### Тесты (`npm test`, `npm run lint`, `npm run build` — все зелёные)
86 тестов, 13 файлов. `formatLockoutMinutes`, `resolveRedirectTarget`,
`checkSession`, `guard`, `redirectIfSignedIn`, `createAppQueryClient`,
`locales`, обновлённые `router.test.ts` и `handlers.test.ts` (лок-окно на
фейковых таймерах, `Path=/`, `expiresAt` +12 ч, `origin_rejected`).

### Не сделано (@qa-only, только `docs/qa/admin-login.md`)
Внешний вид формы входа, переключение языка на странице входа, хранилища
браузера, `document.cookie`, поведение при остановленном сервере (часть 2 QA)
— по определению не автоматизированы (спецификация помечает их `@qa-only`).

## Контракт и деплой

- `make openapi` перегенерировал `web/src/api/{openapi.json,schema.d.ts}`:
  403 `origin_rejected` у каждой изменяющей операции, значение в `ErrorCode`,
  описание `Set-Cookie` с `Path=/` и `Secure` при HTTPS. `proto/` не тронут,
  `make breaking-proto` не требуется.
- `deploy/docker-compose.yml`, `deploy/.env.example`: `SARD_ADMIN_PASSWORD`
  рядом с `SARD_AGENT_ENDPOINT`, без значения в compose (`:?` требует его из
  `.env`). `scripts/ensure-admin-password.sh` — новый скрипt, `make up`
  вызывает его вместо голого `cp`: при первом запуске генерирует пароль
  (`openssl rand -hex 16`, 32 символа) и печатает путь к `deploy/.env`, не сам
  пароль. `README.md`, раздел «Запуск», и ADR 0020.

## Гейты
- `./scripts/gate.sh server fast` и `./scripts/gate.sh web fast` — оба
  зелёные (детали в отчёте задачи). Мутационное тестирование (не `fast`)
  не запускалось в этой сессии.
