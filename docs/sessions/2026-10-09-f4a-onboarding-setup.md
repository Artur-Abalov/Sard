<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# Сессия 2026-10-09: F4a — первый запуск: код настройки, администратор, шаг CA

Ветка `claude/charming-gates-2om1mw` (в задаче — `feat/f4a-onboarding-setup`;
как в прошлых задачах, работа идёт в назначенной), база — `main` @ `8e4a542`.
Задача — продуктовая фича, поэтому после контрольной точки 1 она идёт через
`/ship-feature` (specifier → coder → cleaner → architect → hardener).

## Фаза 1: исследование

Проверено чтением кода (file:line) и командами; где сказано «полагаю» —
не проверено.

### Вход сейчас (W1b, ADR 0021)

- Пароль — `@Value("\${SARD_ADMIN_PASSWORD:}")`
  (`auth/AdminAuthAutoConfiguration.kt:36`). Проверка — `require` в
  конструкторе `AdminPasswordAuthenticator` (`:31-37`): пустой или короче 12
  кодовых точек — контекст не поднимается. Отдельной проверки при старте нет.
- В памяти — несолёный SHA-256 (`AdminPasswordAuthenticator.kt:27`),
  сравнение `MessageDigest.isEqual`. Для пароля из окружения это допустимо
  (ADR 0021), для хэша в БД — нет: нужен медленный солёный хэш.
- Сессии — `SessionStore` в памяти, 12 ч / 7 дней. Перебор —
  `LoginAttemptTracker`, по адресу клиента, 5 за 15 минут,
  `withAddressLock`. Часы внедряются (`ClockAutoConfiguration`), в тестах —
  `MovableClock` (`pki/PkiFixtures.kt:28`).
- Публичные операции — `PUBLIC_OPERATIONS = setOf("POST /api/v1/session",
  "GET /api/v1/status")` (`auth/SessionAuthFilter.kt:27`). Тот же список
  проверяет контракт: `ApiContractIntegrationTest.kt:173`.
- Шов enterprise: `SessionApi` + бин `sessionAuthFilterRegistration`;
  `AdminPasswordAuthenticator` создаётся, только если своего `SessionApi` нет.
  Онбординг администратора должен уступать тому же стартеру.
- Смена пароля сейчас — только переменная и перезапуск
  (`docs/operator/10-security.md:20`).

### Зависимости

- Spring Security на classpath нет; ADR 0021 его отверг.
- `bcpkix-jdk18on:1.86` (`server/build.gradle.kts:66`) тянет
  `bcprov-jdk18on:1.86` (есть в кэше Gradle). В `bcprov` есть
  `org.bouncycastle.crypto.generators.Argon2BytesGenerator` и `OpenBSDBCrypt`
  (проверено `unzip -l` на jar 1.85 из дистрибутива Gradle; полагаю, в 1.86
  то же). Argon2id не требует новой зависимости — только расширения цели BC
  в `docs/dependencies.md:59`.

### Хранение

- Миграции — `server/src/main/resources/db/migration/`, последняя
  `V202610081200__self_agent.sql`; `ddl-auto: validate`.
- Таблицы-одиночки «состояние установки» нет. Тенант по умолчанию —
  `00000000-…-0001` (`V2__tenants.sql`).

### CA (F8, ADR 0052)

- `CaOrigin { GENERATED, IMPORTED, EXISTING }` (`pki/CaDirectory.kt:58`);
  лог каждого старта — `FileCertificateAuthority.kt:77-88`.
- `GET /api/v1/ca` → `CaInfo(fingerprint)`, только с сессией (OQ-181:
  «API только с сессией администратора, не `/api/v1/status`»).
- **Импорт после первого старта невозможен.** Сервер генерирует CA на первом
  старте; повторный старт с `SARD_PKI_IMPORT_DIR` и другим CA —
  `CA_ALREADY_PRESENT` (OQ-180, `docs/specs/server/ca-import.feature:52`).
  Кроме того, `sard-self` (F5) регистрируется сгенерированным CA сразу.

### Агент-сосед (F5)

- От входа не зависит. Живой встроенный агент —
  `LIVE_BUILTIN_AGENTS` (`fleet/Agents.kt:31`); запроса «агенты, кроме
  встроенного» нет, ближайший образец — `fleet/Overviews.kt:11`.

### Консоль и контракт

- Guard — `web/src/routes/_app.tsx:12` → `auth/guard.ts:20-31`
  (`GET /api/v1/session`, 401 → `/login`). Глобальный 401 —
  `auth/queryClient.ts:13-31`, `main.tsx:23-27`.
- Страницы настроек нет (`Layout.tsx:98-107`).
- Моки — MSW + openapi-msw, тип схемы проверяет пути и статусы
  (`mocks/http.ts`); сессия — `mocks/api/session.ts`, CA — `mocks/api/tokens.ts:34`.
- Ошибки — `application/problem+json`, `ErrorCode` (16 значений).
  Новый DTO требует образца в `ApiSerializationIntegrationTest.kt:113`.

### SARD_ADMIN_PASSWORD — где менять

- Обязательна в compose: `deploy/docker-compose.yml:42`
  (`${SARD_ADMIN_PASSWORD:?…}`); `deploy/.env.example:25`.
- Документация: `README.md:33,41,149`; `docs/operator/02-install.md:24-25`,
  `03-configuration.md:28`, `04-tls-and-names.md:92`,
  `09-troubleshooting.md:29,52`, `10-security.md:20-22`;
  `docs/demo.md:85,113`; QA и спецификации W1b.
- Скрипты и CI: `scripts/ensure-admin-password.sh`, `smoke-server.sh:23`,
  `test-self-agent.sh:35` (в том числе обновление с rc1),
  `test-console-install.sh:98`, `test-agent-install.sh:97`;
  `.github/workflows/ci.yml:244`; `release.yml:422-452` (`verify-quickstart`
  исполняет блок README между `quickstart:begin/end` и логинится паролем из
  `.env` через `smoke-server.sh`), `release.yml:485` (офлайн).
- e2e: `SardEnvironment.kt:64,287`, `SardApi.kt` (вход паролем из env).

### Вопросы к контрольной точке 1

Трудные первыми; у каждого — предложение.

1. **Импорт CA из мастера при уже сгенерированном CA.** Первый старт уже
   сгенерировал CA, F8 отвергает импорт (`CA_ALREADY_PRESENT`), `sard-self`
   зарегистрирован. Предложение: правила F8 не трогать. Пока нет агентов,
   кроме встроенного, мастер показывает команды
   `docker compose down -v` и старт с `SARD_PKI_IMPORT_DIR` (до
   онбординга в томах нечего терять). Есть другие агенты — ссылка на переезд
   (`docs/operator/08`). Альтернатива: сервер заменяет сгенерированный CA
   при незавершённом онбординге — меняет Р3 F8 и требует перерегистрации
   `sard-self`.
2. **Что открывает код.** D2′ — «мастер без кода не открывается», OQ-181 —
   CA только с сессией. Предложение: первый экран мастера — код; верный код
   обменивается на сессию настройки (cookie `sard_setup`, в памяти, до
   завершения шага admin, не дольше срока кода, умирает при перезапуске).
   С ней видны шаг CA и шаг admin; завершение admin → `sard_session`,
   код и все сессии настройки недействительны. Вопрос: можно ли ввести код
   повторно (перезагрузка вкладки, другой браузер) до завершения шага admin —
   предложение «да» (D15: «действует до завершения шага»). Иначе потеря
   вкладки = перезапуск сервера.
3. **Переменная и БД одновременно.** Предложение вместо «БД главнее»:
   сервер помнит отпечаток последнего применённого значения переменной.
   Новое значение (не совпадает с запомненным) становится паролем при старте.
   То же значение — пароль в БД главнее, смена из консоли переживает
   перезапуск. Так продолжает работать ротация через `.env`
   (`10-security.md:20`), а установка без переменной живёт на БД.
4. **Восстановление доступа.** С правилом из п. 3 восстановление — задать
   новый `SARD_ADMIN_PASSWORD` и перезапустить (нужен доступ к серверу);
   после этого переменную можно убрать. Отдельный `SARD_ADMIN_RESET`
   предлагаю не вводить: флаг `true`, забытый в `.env`, сбрасывает пароль
   при каждом перезапуске. Если нужен именно возврат мастера — одноразовое
   значение, запомненное в БД, как в п. 3.
5. **Хэш.** Argon2id из `bcprov` (уже на classpath), параметры OWASP
   (m = 19 МиБ, t = 2, p = 1), строка PHC. Отвергнуто: `spring-security-crypto`
   (новая зависимость, ADR 0021), PBKDF2. Верхний предел пароля — 1024 кодовые
   точки (защита от дорогих хэшей).
6. **Вход до создания администратора.** `POST /api/v1/session` без
   администратора в БД. Предложение: 409 `setup_required`, без учёта в
   перебор (консоль всё равно ведёт на `/setup`).
7. **Контракт REST** (предложение):
   - `GET /api/v1/onboarding` — публичный: шаги и их состояние, выдан ли
     код. Данные CA — только с сессией настройки или администратора.
   - `POST /api/v1/onboarding/setup-session {code}` → 204 + `sard_setup`;
     401 неверный, 429 перебор (свой счётчик, правила W1b), 409 шаг admin
     выполнен.
   - `POST /api/v1/onboarding/admin {password}` (с `sard_setup`) → 204 +
     `sard_session`.
   - `PUT /api/v1/session/password {currentPassword, newPassword}` → 204,
     остальные сессии завершаются; неверный текущий — 422 `wrong_password`
     (не 401: 401 выкидывает консоль на вход), с учётом в перебор.
   - `CaInfo` дополняется полями `origin` и `keyPath` (аддитивно), чтобы
     шаг CA и страница токенов показывали одно и то же.
8. **Quickstart и `verify-quickstart`.** Предложение: основной путь README —
   первый запуск по коду; неинтерактивный — отдельный абзац. Job
   `verify-quickstart` дописывает `SARD_ADMIN_PASSWORD` в `.env` после блока
   README и перезапускает (`smoke-server.sh` без изменений). Compose:
   `${SARD_ADMIN_PASSWORD:-}`. `make up` для разработки по-прежнему
   генерирует пароль.

Решения исполнителя (не вопросы):

- Состояние онбординга — на установку, не на тенант (одна таблица,
  строки `ca`, `admin`, `self_backup`, `keys_confirmed`). Администратор —
  одна строка тенанта по умолчанию.
- Шаг `ca` отмечается вместе с шагом `admin` (владелец видел шаг CA в
  мастере); в неинтерактивном пути и при обновлении — оба при старте.
- Код — 140 бит: 28 знаков base32 без похожих символов (Crockford), группы по
  4; в памяти — SHA-256 кода (код случайный, медленный хэш не нужен),
  сравнение `MessageDigest.isEqual`.
- Онбординг уступает enterprise-`SessionApi` (тот же шов, что у
  `AdminPasswordAuthenticator`).

## Ответы владельца (контрольная точка 1, 2026-10-09)

1. Правило F8 меняется: для онбординга отказ `CA_ALREADY_PRESENT` после
   сгенерированного CA — дефект.
2. Согласен: код → сессия настройки → шаги CA и admin; код можно вводить
   повторно до завершения шага admin.
3. `SARD_ADMIN_PASSWORD` — дефект безопасности, удаляется. Обратная
   совместимость не нужна.
4. Нужен другой путь восстановления доступа, без пароля в `.env`.
5. Предложено вернуть Spring Security — обсуждение открыто.
6. Да: 409 `setup_required`, без учёта в перебор.
7. Да: контракт REST как предложен.
8. Без пароля в `.env`.

### Предложения по открытым пунктам (ждут ответа)

**П1. Новое правило F8.** Сгенерированный CA заменяется импортом, пока шаг
`ca` не выполнен и сервер не выдал ни одного сертификата агента (таблица
`agent_certificates` пуста, встроенный агент тоже). Шаг `ca` выполняется
явным «использовать этот CA» в мастере (с сессией настройки). До этого
сервер не выпускает встроенный токен `sard-self` (`SelfAgentCheck.pass`,
`selfagent/SelfAgentCheck.kt:50`) — иначе сосед зарегистрируется на CA,
который потом заменят. Сертификат сервера перевыпускается под новым CA.
Старый ключ CA удаляется (он ничего не подписал). Шаг `ca` выполнен или
есть сертификаты — прежнее `CA_ALREADY_PRESENT` (ручной переезд, Р3).

**П2. Без `SARD_ADMIN_PASSWORD`: восстановление и автоматизация.** Образ
сервера — `ENTRYPOINT ["java", "-jar", …]` (`deploy/server/Dockerfile:123`),
значит, `docker compose run --rm server admin-reset` запускает тот же jar
командой: она открывает шаг admin в БД (хэш удаляется) и выходит. После
`docker compose restart server` — новый код в логе, все сессии сброшены
перезапуском. Доступ к хосту Docker — доказательство владения, как и для
кода. Автоматизация (e2e, `verify-quickstart`, `smoke-server.sh`,
скрипты установки) проходит настоящим путём: строка лога с кодом имеет
постоянный вид, скрипт читает её из `docker compose logs server` и
вызывает API мастера — путь первого запуска проверяется в каждом прогоне.
`scripts/ensure-admin-password.sh` и переменная удаляются.

**П3. Spring Security.** За: стандартные `PasswordEncoder`
(`Argon2PasswordEncoder` на уже имеющемся BC, `DelegatingPasswordEncoder` —
смена алгоритма без миграции), цепочка фильтров, в которую enterprise
подключает OIDC/SAML/LDAP без своего шва, роли и проверки на методах
(RBAC, тенанты), Spring Session JDBC, если сессии уйдут в БД; проверенный
код вместо своего в месте, где ошибка — дыра. Против: крупная зависимость
и неявное поведение (CSRF-токен, редиректы на HTML-логин, формат 401/403 —
каждое надо приручить под `problem+json` и проверку Origin); переписывается
W1b (`SessionAuthFilter`, `OriginGuardFilter`, шов `SessionApi`), ADR 0021
заменяется; декларативная конфигурация плохо поддаётся мутационному
тестированию — защищать её придётся интеграционными тестами. Предложение:
если брать — то сейчас и отдельной задачей **F4a-0** до F4a: перевод W1b на
Spring Security без изменения поведения (тесты W1b — страховка), затем F4a
строит сессию настройки и смену пароля уже на нём. Иначе F4a допишет ещё
своего кода, который потом выбросят. Вариант поменьше — только
`spring-security-crypto` ради `Argon2PasswordEncoder` сейчас, остальное —
когда понадобится SSO.

### Ответы владельца на П1–П3 (2026-10-09)

- П1 и П2 приняты.
- П3: сейчас — только `spring-security-crypto` (`Argon2PasswordEncoder`);
  перевод входа на Spring Security — отдельная задача (нужна в любом случае),
  не в F4a.

Дальше — `/ship-feature`, начиная со specifier.

## Спецификация (specifier)

`docs/specs/server/onboarding-setup.feature` (161 сценарий),
`docs/specs/web/onboarding-setup.feature` (63), `docs/qa/onboarding-setup.md`;
поправки W1b, F8, F5. Владелец 2026-10-09 утвердил спецификацию и все
предложения OQ-187…OQ-197 («Утверждаю. Все принимаю»).

### Уточнение владельца 2026-10-09 (specifier)

- Толкования specifier к поправке «обратной совместимости нет» утверждены
  («Остальное утверждаю»), кроме одного: граница совместимости — 0.1.0-beta
  («Будет верно после 0.1.0-бета»). Первая бета уже содержит F4a; обновление с
  0.1.0-beta.N сохраняет данные и состояние онбординга. Сценарий F5 обновления
  снова действует (переименован в «Обновление с compose выпущенной беты
  сохраняет данные и агентов»), его e2e включается выпуском первой беты;
  сценарий «После обновления … сосед появляется …» остаётся `@заменено-f4a`.
- OQ-199: CA без записанного происхождения — отказ старта
  `CA_ORIGIN_NOT_RECORDED`; обратный случай (пустой каталог CA без источника при
  базе, где CA в деле) — `CA_MISSING`. Решение Р19, 11 новых сценариев
  (`docs/specs/server/onboarding-setup.feature`: 161 → 172), QA — часть 3,
  шаги 17–23.
- Статус «черновик, ждёт утверждения» в заметках F4a у W1b, F8, F5 заменён на
  «утверждено 2026-10-09».
- `scripts/test-self-agent.sh` не менялся (зона coder): что в нём поменять —
  в отчёте specifier.

## Фаза 2: сервер (coder)

Ветка `claude/charming-gates-2om1mw`. Исходное состояние — коммит 63912f8 (черновик
предыдущего прогона: хэшер, коды, сессии настройки, миграция); его тесты прошли (в том
числе `JdbcStoresIntegrationTest`, которого прежде не запускали: Docker теперь есть).
Рабочий цикл — тест, красный запуск, минимальный код, зелёный запуск; полный
`:server:test` — после каждого крупного куска.

### Что сделано

- **Хэш и администратор.** `auth/PasswordHasher` (Argon2id, параметры OQ-193),
  `auth/Administrators` (+`JdbcAdministrators`; хэш читается из базы при каждой
  проверке), `auth/AdminSetup` (`StoredAdminSetup` в ядре, `ExternalAdminSetup` при
  замене `SessionApi`), `auth/PasswordRules` (12–1024 кодовые точки).
  `AdminPasswordAuthenticator` и все проверки `SARD_ADMIN_PASSWORD` удалены; сервер
  переменную не читает.
- **Вход.** `SessionApiImpl`: блокировка адреса, затем чтение хэша (заблокированный
  адрес базу не трогает); нет администратора — `SignInResult.SetupRequired` (409
  `setup_required`, не засчитывается); недоступная база — исключение доступа к данным,
  то есть 503, попытка не засчитывается. `changePassword` в `SessionApi` (по умолчанию
  `NotSupported` — 501): поля, блокировка, текущий пароль; успех завершает все прочие
  сессии (`SessionStore.removeAll`) и выдаёт новый идентификатор.
- **Мастер.** `onboarding/`: `SetupCodes` (SHA-256, Crockford, 24 часа от
  внедрённых часов), `SetupSessions`, `OnboardingService` (реализует `api.OnboardingApi`),
  `SetupCodeAnnouncer` (строка Р1 при старте, SmartLifecycle), `OnboardingAutoConfiguration`.
  Контроллеры — каждый шаг свой (`OnboardingControllers.kt`, `PasswordController.kt`),
  у каждого свой обработчик `HttpMessageNotReadableException`. `SessionAuthFilter`:
  `PUBLIC_OPERATIONS` из четырёх, шаги `ca` и `admin` и `GET /onboarding` пропускаются,
  сессию настройки проверяет контроллер (иначе цикл пакетов `auth` ↔ `onboarding`).
- **CA и база (Р11, Р18, Р19).** `pki.CaLedger` (происхождение по отпечатку, «в деле ли
  CA»), `onboarding.JdbcCaLedger`; `CaDirectory.open` теперь решает по реестру:
  `CA_ORIGIN_NOT_RECORDED`, `CA_MISSING` (`CaStartRefused`, «CA startup refused»),
  замена (`CaStore.replace`: `.tmp-*` → `ca`, прежний — `.tmp-replaced-*` и удаление;
  `CaLeftovers` возвращает прежний CA после прерванной замены), запись происхождения до
  появления CA. `CaImport.reconcile(present, usage)` возвращает CA на замену или null и до
  шага `ca` проверяет источник по всей таблице. `CaReplacementListener` →
  `enrollment.TokensRevokedOnCaReplacement` (отзыв активных обычных токенов, одна строка
  WARN с числом). `CertificateAuthority` получил `provenance()` и `keyLocation()`;
  `CaInfo` — `origin` и `keyPath`.
- **Встроенный агент.** `SelfAgentCheck` не выпускает токен и не пишет файл, пока шаг `ca`
  не выполнен (после проверки «живой агент → удалить файл»).
- **admin-reset.** `onboarding/ServerCommand` + `main`: первый аргумент без `--`;
  без Spring-контекста; коды 0/1/2; таблицы нет (`42P01`) — «пароль не задан».
- **Контракт.** `make openapi` — `web/src/api/openapi.json` и `schema.d.ts`
  перегенерированы; в веб правлено только нужное для зелёного `gate.sh web fast`:
  четыре кода в `errors.ts` и локалях, `origin`/`keyPath` в моке `/api/v1/ca`.
- **Тесты (новые).** `OnboardingServiceTest`, `SessionApiImplTest`, `AdminSetupTest`,
  `PasswordRulesTest`, `ServerCommandTest`, `CaDirectoryLedgerTest`,
  `JdbcCaLedgerIntegrationTest`, `TokensRevokedOnCaReplacementIntegrationTest`;
  HTTP: `SetupCodeIntegrationTest`, `OnboardingStateIntegrationTest`,
  `PasswordChangeIntegrationTest`, `FirstStartBehindProxyIntegrationTest`,
  `OnboardingContractIntegrationTest`, `NoSecretsInBeansIntegrationTest`; настоящие
  серверы один за другим над одной установкой (`Installations.kt`):
  `FirstStartRestartIntegrationTest`, `CaStartupIntegrationTest`,
  `OnboardingDatabaseDownIntegrationTest` (TCP-реле к базе вместо остановки
  контейнера), `AdminResetCommandIntegrationTest` (процесс jar).
- **Тесты (прежние).** В `server/build.gradle.kts` убрана переменная
  `SARD_ADMIN_PASSWORD`; каждый контекст `@SpringBootTest` получает свой каталог CA
  (`FreshPkiDirectory`, `sard.test.pki-base`), потому что каталог CA идёт вместе со своей
  базой (Р19), а у каждого контекста своя база. Вход в REST-тестах — через мастер с
  фиксированным кодом (`WizardCodeConfiguration`, `ApiClient.signIn`); тесты входа
  засевают администратора настоящим хэшем (`SeededAdministrator`); тесты встроенного
  агента подтверждают шаг `ca` (`ConfirmedCaStep`).

### Отклонения от заметок предыдущего прогона и решения

1. **Слушатель отзыва токенов — в `enrollment`, не в `selfagent`.** Токены регистрации
   живут в `enrollment`; `selfagent` может быть выключен (`SARD_SELF_DIR` пуст), а отзыв
   обязан работать всегда. `pki` ничего о токенах не знает (интерфейс
   `CaReplacementListener`).
2. **Реестр CA без «закрытого по умолчанию» значения.** `CaLedger` — обязательный
   параметр `FileCertificateAuthority`; его бин объявляет `OnboardingAutoConfiguration`
   (`@DependsOn("flywayInitializer")`), корпоративная замена CA реестром не пользуется.
3. **`Administrators`, `PasswordHasher`, `AdminSetup` — в пакете `auth`**, не в
   `onboarding` (иначе `auth` зависел бы от `onboarding` и наоборот; `PackageCycleTest`).
4. **Атрибут запроса администратора** `SESSION_REQUEST_ATTRIBUTE` перенесён в `api`
   (`ArchitectureTest` запрещает `api` ссылаться на `auth`); значение —
   `dev.sard.server.session`. По нему мастер узнаёт администратора расширения.
5. **`PUT /session/password`: `wrong_password` — это `ValidationProblem` с ошибкой у
   `currentPassword`** (422 в контракте объявлен одной схемой).
6. **Тела запросов с секретами** (`SetupCodeRequest`, `AdminStepRequest`,
   `PasswordChangeRequest`, `SessionRequest`) переопределяют `toString`: Spring пишет
   прочитанное тело в лог на DEBUG, а спецификация требует, чтобы код и пароль не
   попадали в «захваченные логи» при DEBUG.
7. **Шаг `admin` при внешнем входе отвечает `setup_completed` до проверки сессии
   настройки** (Р16), а в ядре после шага `admin` запрос со старой сессией настройки —
   401 (сценарий «После шага admin все сессии настройки не действуют»): порядок проверок
   зависит от `AdminSetup.external`.
8. **Таблицы `onboarding_steps` и `ca_origins` — глобальные** в `TenancyIntegrationTest`
   (состояние установки, не тенанта; Р решения исполнителя).
9. **Файл `deploy/.env.example` не менялся.** `DeployEnvExampleTest` проверял, что
   `SARD_ADMIN_PASSWORD` в нём — пустая заготовка с комментарием о длине; эти два теста
   проверяли отменённое поведение (сценарии `@qa-only` правила 1 W1b заменены F4a), и я их
   удалил. Тест на отсутствие строки (`@doc` «Compose и пример окружения не содержат
   пароль администратора») не добавлен: он проходит только после правки compose и
   `.env.example`, то есть в следующем прогоне.
10. **Конфигурация проверок.** Пороги и тесты не менялись. Для `detekt` (длина строки 120,
    не более 11 функций в классе, `ReturnCount`) `CaDirectory` разделён на `CaDirectory`,
    `CaStore` и `CaLeftovers`, а проверки файлов источника вынесены из `CaImportSource` в
    `CaImportFiles`.
11. **Условие «файл ключа принадлежит другому пользователю»** (Р18, причина
    `IMPORT_FILE_UNREADABLE`) на уровне процесса не проверяется: тесты идут от root, и
    root читает любой файл. Покрыто модульно: `CaImportSource` принимает `isReadable`.

### Что не покрыто автоматическим тестом сервера

- Сценарии `@e2e`, `@doc` и `@qa-only` — следующий прогон (e2e, документация, скрипты,
  compose).
- Мутационный прогон (`gate.sh server`, без `fast`) не запускался; новые чистые функции
  покрыты тестами `@MutFlowTest` (`PasswordRulesTest`, `OnboardingServiceTest`,
  `SessionApiImplTest`, `AdminSetupTest`, `CaDirectoryLedgerTest`, `ServerCommandTest`,
  `SetupCodeAnnouncerTest`).

### Результат шлюзов (как напечатано)

`./scripts/gate.sh server fast` (шестой запуск; предыдущие остановились на CRAP 7–8 у
`SessionApiImpl.replace`, `CaDirectory.empty`, `SessionAuthFilter.doFilterInternal`,
`OnboardingService.completeAdmin` — функции разделены — и на двух нестабильных тестах
(`CaStartupIntegrationTest` — гонка за схему, `OnboardingDatabaseDownIntegrationTest` — пул
после возврата базы), которые теперь ждут):

```
== gate server: spotless, detekt, tests, coverage >= 80%
coverage: 96.7% (instructions)
== gate server: CRAP <= 6
gate: PASSED (server, fast)
```

Всего 1863 теста сервера; худшие функции по CRAP — ровно 6.0 (`main` — 2 ветви без
покрытия, остальные — прежний код).

`./scripts/gate.sh web fast`:

```
 Test Files  33 passed (33)
      Tests  444 passed (444)
gate: PASSED (web, fast)
```

Мутационный прогон (`./scripts/gate.sh server` без `fast`) не запускался.
