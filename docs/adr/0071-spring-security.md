<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 0071 — HTTP-безопасность сервера на Spring Security

- Статус: принято (CORE1, SARD-7; спецификация `docs/specs/server/spring-security.feature`, решения Р34–Р53,
  ответы владельца В-1–В-8 2026-10-31)
- Дата: 2026-10-31
- Заменяет частично [0021](0021-admin-password-login.md): пункт «Spring Security» раздела «Отвергнуто»;
  описание `SessionAuthFilter` и `OriginGuardFilter` как сервлет-фильтров; «Полный контракт замены — оба
  бина», бин `sessionAuthFilterRegistration` и обязанность выставлять `SESSION_REQUEST_ATTRIBUTE`.
  Дополняет [0070](0070-server-core-extension-points.md) (пункты 3 и 4 уточнены).

## Контекст
ADR 0021 отверг Spring Security: функциональность W1b (cookie, Origin, перебор) умещалась в два фильтра, а
весь Spring Security давал лишнее. С тех пор в ядро добавлены шов политики доступа (0070), и расширению
(SSO, OIDC, LDAP) нужен публичный способ встроить свой вход. Свои фильтры и атрибуты запроса для этого не
годятся: их нельзя держать в замороженном API. Продукт ещё не выпущен, после пилота переход обойдётся
дороже (решение владельца 2026-10-31).

## Решения
1. **Одна цепочка `SecurityFilterChain`** (`CoreSecurityAutoConfiguration.sardSecurityFilterChain`) на
   HTTP-порту. Spring Security не включает в ней ничего, что видно снаружи: нет пользователя по умолчанию и
   пароля в логе (автоконфигурацию `UserDetailsServiceAutoConfiguration` исключает `EnvironmentPostProcessor` `NoDefaultUser`, поэтому
   и любое приложение на ядре, не только со своим `main`),
   формы входа, Basic, выхода, remember-me, request cache, CORS, CSRF, `HttpSession` (cookie `JSESSIONID`
   нет). Порядок: заголовки API → Origin → контекст безопасности → исключения → доступ. gRPC (mTLS и
   перехватчик ядра, порт 9090) и слушатель метрик вне Spring Security.
2. **Сессия ядра — `SecurityContextRepository` поверх `SessionStore`** (`SessionSecurityContextRepository`).
   Действующая cookie `sard_session` (первая в заголовке, Р53) даёт `SubjectAuthentication(Subject.ADMINISTRATOR)`
   и продлевает сессию — для запросов под `/api/v1`, кроме `POST /api/v1/session`, `GET /api/v1/status` и
   `POST /api/v1/onboarding/setup-session` (Р52). Контекст читается один раз при входе запроса в цепочку,
   после проверки Origin, и не сохраняется: он живёт один запрос. Сама сессия остаётся атрибутом запроса
   для кода ядра, которому нужен её арендатор; от расширения этого атрибута не требуется.
3. **Проверка Origin — фильтр цепочки** (`OriginGuardFilter`, правила 0021 и 0046 прежние), CSRF Spring
   Security выключен. Отказы цепочки — прежние problem+json: 401 `unauthenticated` (точка входа; для
   `DELETE /api/v1/session` она очищает cookie) без `WWW-Authenticate`, 403 `origin_rejected`, 403
   `access_denied`.
4. **Политика доступа — `AuthorizationManager` цепочки** (`AccessPolicyAuthorizationManager`) вместо
   перехватчика MVC. Операция — метод и шаблон пути обработчика, которого выберет MVC (`MvcOperationResolver`
   спрашивает `RequestMappingHandlerMapping` на обёртке запроса, не оставляя на запросе следов); нет
   обработчика — политику не спрашивают. Публичные операции, операции мастера (`WIZARD_OPERATIONS`) и выход
   политике не подчинены; совпадение операции — по методу и точному пути (`OperationMatcher`).
5. **Субъект — `SubjectAuthentication`** (публичный класс в `dev.sard.server.extension`) вместо атрибута
   `SUBJECT_REQUEST_ATTRIBUTE`, который удалён: субъект под атрибутом без аутентификации ничего не
   открывает. `SubjectSource` по-прежнему единственный публичный шов «кто действует»; реализация ядра читает
   аутентификацию из контекста безопасности текущего запроса. Без аутентификации — 401 что бы ни ответил
   шов; аутентификация есть, а шов вернул `null` — 401; шов бросил исключение — 403 и WARN.
6. **Шов входа расширения — `SardSecurityCustomizer`** (публичный интерфейс, `HttpSecurity` входит в ABI):
   `publicOperations` и `customize(http)`. Ядро применяет настройщики после своих правил доступа, в порядке
   `@Order`, затем имени бина (расширение называет бины по своему id). Правила доступа к путям настройщик
   задать не может: цепочка уже закрыта `anyRequest()`, и попытка роняет сборку. Публичные операции
   расширения — только под `/api/v1`, в OpenAPI у них пустое требование безопасности.
7. **Вход паролем остаётся контроллером** `POST /api/v1/session` (`SessionApiImpl`): фильтр входа Spring
   Security не воспроизводит 204 без тела, 409, 429 с `Retry-After` и события `signin.*`. Пароль проверяет
   `AuthenticationManager` ядра с провайдером над `Argon2PasswordEncoder`; это не бин приложения (иначе
   Spring Boot вернул бы пользователя по умолчанию). `SessionApi` остаётся внутренним и заменяемым
   (`@ConditionalOnMissingBean`); бин `sessionAuthFilterRegistration` и обязанность выставлять
   `SESSION_REQUEST_ATTRIBUTE` уходят с фильтром.
8. **Проверки старта** (отказ закрыт; защита от ошибки, а не граница безопасности, 0070 п. 8): бин
   `SecurityFilterChain` или `WebSecurityCustomizer` не из `server-core` (по источнику кода, как контроллеры);
   публичная операция расширения вне `/api/v1`; настройщик бросил исключение или задал правила доступа.
   Текст называет бин, его конфигурацию и `SardSecurityCustomizer`.
9. **Заголовки.** Каждый ответ под `/api/v1` несёт ровно по одному `X-Content-Type-Options: nosniff` и
   `Cache-Control: no-store` (`ApiHeadersFilter` — первый фильтр цепочки, поэтому их несут и отказы
   Origin, 401 и 403). Прочие заголовки Spring Security выключены (`headers().disable()`); ответы вне
   `/api/v1` не меняются.
10. **Брандмауэр.** `StrictHttpFirewall` с настройками по умолчанию отклоняет пути с `;`, закодированными
    `%2e`, `%2f`, `%5c` и ненормализованные ответом 400 без тела; так как он стоит перед цепочкой, заголовки
    API пишет сам обработчик отказа (`ApiRequestRejectedHandler`). Tomcat по умолчанию отвечает на
    закодированные `/` и `\` своей HTML-страницей ошибки; соединению задано `passthrough`, чтобы эти пути
    дошли до брандмауэра.
11. **Зависимость** `spring-boot-starter-security` (Apache-2.0), новая строка в `docs/dependencies.md`.

## Отвергнуто
- **Фильтр входа `UsernamePasswordAuthenticationFilter`** — см. пункт 7.
- **Хранить сессию в `HttpSession`** — сессии ядра в памяти `SessionStore` со своими сроками, событиями и
  обходом (Р23); `HttpSession` дал бы второе хранилище и cookie `JSESSIONID`.
- **Политика в `HandlerInterceptor`** — две точки проверки в двух механизмах; расширению пришлось бы знать оба.
- **Публичный `SessionApi`** — отдельным решением спецификации SSO.

## Последствия
- Расширение встраивает вход через `SardSecurityCustomizer` и `SubjectAuthentication`, не трогая внутренние
  типы; образец `examples/extension-sample` показывает оба.
- Ответы `/api/v1` получили два заголовка (OpenAPI их не описывает); пути, которые нельзя сопоставить
  однозначно, отвечают 400 вместо 401, 404 или 405.
- ABI ядра выросло на `SubjectAuthentication` и `SardSecurityCustomizer` и уменьшилось на
  `SUBJECT_REQUEST_ATTRIBUTE`.
