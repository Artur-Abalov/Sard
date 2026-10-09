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

СТОП: жду ответов владельца.
