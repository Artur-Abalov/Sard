<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# CORE1 (SARD-7) — server-core: фаза 1, исследование

Дата: 2026-10-29. Только чтение, код не менялся. База: main `b608141` (J1 уже влит, PR #111).

## Что проверено (прочитано в коде)
- Один модуль `:server`: код, `@SpringBootApplication` (`SardServerApplication.kt:11`, без `scanBasePackages`),
  `main` с `ServerCommand`, `springBoot { buildInfo() }`, bootJar `sard-server.jar`, консоль и ключ релиза
  копируются в `processResources` этого же модуля (`server/build.gradle.kts:88-115, 299-306`).
- Автоконфигураций семь (`AutoConfiguration.imports`). Остальное — 40+ `@Configuration`, сканируемых по пакету
  `dev.sard.server`, 16 `@Component` (14 `*ApiImpl`, `SourceWarnings`, `RestoreVerifications`), 21 REST-контроллер в `api/`.
  `@Service`/`@Repository` нет; Spring Data — один `AgentRepository`. `@EntityScan`/`@EnableJpaRepositories` нет.
- `@ConditionalOnMissingBean` в сканируемой конфигурации — `OnboardingPorts.kt:33` (`GuardTargets`): стартер его заменить
  не может (сканируемые конфигурации обрабатываются раньше автоконфигураций).
- Заменяемо уже сейчас: `TenantResolver` (ADR 0013), `SessionApi` + `sessionAuthFilterRegistration` (ADR 0021), `Clock`,
  `CertificateAuthority`, `SetupCodeGenerator`.
- Не заменяемо / нет шва: роли и пользователи (один администратор, `AdminSession` без субъекта и роли; `role/` — роли
  агентов), лицензия и редакция, события входа/настроек/запуска. Есть свой механизм слушателей через `ObjectProvider`:
  `RunFinishedListener` (`runs/RunFinished.kt:39`, после коммита, синхронно, ошибки в лог), `AgentSessionListener`,
  `CaReplacementListener`. `ApplicationEventPublisher` нигде нет.
- `ExtensionRegistry` никто в production не читает (только лог). Эндпоинта редакции/расширений нет; ближайшее —
  `GET /api/v1/status` (`{version, lastVerifiedRestoreAt}`).
- OpenAPI: один документ, без групп — контроллеры расширения попадут в него; `required` выводится только для пакета
  `dev.sard.server.api` (`OpenApiConfiguration.kt:146`). `ApiContractIntegrationTest` сверяет документ с
  `web/src/api/openapi.json` байт в байт.
- Миграции: порядок «расширения после ядра» уже проверен (`MigratorIntegrationTest.kt:247`); старт сервера применённость
  миграций расширений не проверяет (ADR 0069 п. 5, оставлено открытым).
- `internal` уже в 85 файлах main — после разбиения он станет границей модуля.
- Сборка: Gradle 9.7.1, Kotlin 2.4.20, Spring Boot 4.1.2; блокировка зависимостей STRICT для всех конфигураций
  (`build.gradle.kts:29-35`) — новый модуль и `maven-publish` требуют lock-состояния; `verification-metadata.xml`.
  `maven-publish` нигде нет. Пути `server/` зашиты в: `deploy/server/Dockerfile:70,92,97`, `.dockerignore`, Makefile
  (`build`, `test`, `lint-server`, `openapi`), `ci.yml` (test-results, openapi), `renovate-pins.yml`, защищённые
  `scripts/gate.sh`, `scripts/crap.sh`, `scripts/claude/*` (guard-edit), `.claude/agents/hardener.md`, ~32 теста с
  путями `../` (работают, только если модуль на глубине 1). mutflow завязан на `bootBuildInfo`.
- Релиз: SBOM — syft по образу и каталогам пакетов (не по jar), cosign/minisign; deb/rpm — только агент.

## Полагаю (не проверено)
- Встроенная ABI-валидация KGP (`abiValidation`, с Kotlin 2.2) есть в 2.4.20 и заменяет binary-compatibility-validator —
  проверить сборкой в фазе 3.
- Внутренний состав bootJar изменится (классы ядра уедут из `BOOT-INF/classes` в `BOOT-INF/lib/…server-core….jar`) —
  сравнение «до/после» придётся делать по развёрнутому составу классов и ресурсов, а не по списку записей jar.

## Контрольная точка 1 — решения владельца
1. Enterprise собирает свой bootJar из `server-core`, своих стартеров и консоли, опубликованной отдельным артефактом.
   Открытое ядро собирается так же: приложение — только упаковка (`server-core` + консоль), `main` и
   `@SpringBootApplication` остаются в библиотеке.
2. «Тот же результат» — развёрнутый состав классов и ресурсов jar, файлы образа вне jar, e2e и SBOM образа.
3. Лимиты ключа Pro (доступные функции и их лимиты, число агентов, число пользователей с тонкими ролями, число
   OIDC-провайдеров) — дело enterprise. Ядро их не разбирает и ничего по ним не ограничивает. Пользователи ядра —
   администраторы с полными правами и наблюдатели.
4. Шов ролей: субъект сессии и интерфейс доступа, реализация ядра — «администратор может всё». Отдельно — исследовать
   переход на Spring Security (ниже).
5. Библиотека остаётся в `server/` (Gradle `:server`, артефакт `sard-server-core`), приложение — `server-app/`.
6. Публичный API — `dev.sard.server.extension.**`; ABI-дамп этих пакетов в CI; архитектурный тест образца.
7. Снапшотов из main нет. Enterprise `X.Y.Z-*` совместим с ядром `X.Y.Z-*`.
8. Аудит — слушатели событий ядра, не отдельный механизм.
9. Редакция, расширения и сводка лицензии — в ответе API, консоль ядра показывает общий блок; без npm-пакета.
10. Старт сервера проверяет применённость миграций расширений (отдельная история, отдельные таблицы) — закрыть
    открытый пункт ADR 0069 п. 5.
11. `GuardTargets` — до тенантов, в CORE1 не трогать.
12. Образец — отдельная Gradle-сборка `examples/extension-sample` против `publishToMavenLocal`, AGPL, только для тестов и
    документации; настоящий enterprise — в другом репозитории (LIC1).

## Spring Security — исследование (вопрос 4)
- Сейчас: `auth/` — 12 файлов, 800 строк; два фильтра (`OriginGuardFilter`, `SessionAuthFilter`) и `SessionApiImpl`;
  из Spring Security в classpath только `spring-security-crypto` (Argon2/BCrypt). ADR 0021 отверг Spring Security как
  ненужную зависимость для одного пароля.
- Для enterprise выгода прямая: OIDC (`spring-security-oauth2-client`, `ClientRegistrationRepository` — естественное
  место лимита числа провайдеров), LDAP (`spring-security-ldap`), роли через `authorizeHttpRequests`/method security.
  Писать OIDC руками рискованнее, чем подключить готовое.
- Если Spring Security есть только в enterprise: появление `spring-security-web` в classpath включает автоконфигурацию
  Boot с цепочкой по умолчанию на все пути (консоль, `/api/v1/status`, actuator, загрузки агентов). Стартеру пришлось бы
  описать всю цепочку ядра сам, а список публичных операций ядра (`PUBLIC_OPERATIONS`) — `internal`. Ядровые фильтры
  при этом продолжают работать рядом. Это хрупко: безопасность ядра описана дважды.
- Если ядро переходит на Spring Security: одна `SecurityFilterChain` ядра (публичные операции, проверка сессии,
  Origin-фильтр внутри цепочки), своя `SecurityContextRepository` поверх `SessionStore`, 401 в виде problem+json;
  enterprise добавляет `oauth2Login`/LDAP и правила ролей через бины-настройщики. Сохранить надо Р1 (продление сессии),
  Р12 (очистка cookie), блокировку перебора, формат cookie и ошибок; CSRF Spring выключить в пользу Origin-проверки.
- Полагаю (не проверено): у spring-grpc есть автоконфигурация безопасности gRPC, включающаяся при Spring Security в
  classpath; её нужно явно выключить или проверить, что mTLS агентов не меняется.
- Оценка: переход полезен, но LIC1 и пилоту не нужен (SSO — после пилота). Предложение: в CORE1 не переходить, но
  описать шов входа в терминах `Subject` ядра и экспортировать список публичных операций в публичный API, а переход —
  отдельной задачей перед SSO с ADR, заменяющим часть 0021. При lockstep-совместимости X.Y.Z ломающая смена API шва в
  следующей минорной версии допустима.

## Окружение
- В контейнере не было запущенного Docker; `dockerd` поднят вручную, после этого Testcontainers, образ и e2e доступны.

## Фаза 2 — разбиение модулей без изменения кода
- `server/` — библиотека (Gradle `:server`, артефакт `sard-server-core`): bootJar выключен, обычный jar без
  классификатора, в манифесте `Implementation-Title/Version` (по нему Spring пишет версию в строке старта); свой
  `build-info.properties` только для тестов (`testBuildInfo` → test resources). Консоль и `bootBuildInfo` из модуля
  убраны. Файлы не переносились: правило 5(б), пути тестов и ворот прежние.
- `server-app/` — без кода: `server` + `server-console` → `sard-server.jar`, `mainClass` из библиотеки, `buildInfo`
  с прежними `artifact`/`name` = `server`, `Implementation-Title: server`.
- `server-console/` — jar с `console/` из `-PsardConsoleDist`, та же проверка `index.html`.
- Lock-файлы новых модулей; `server/gradle.lockfile` не изменился. Dockerfile, `.dockerignore`, Makefile (`build`),
  `renovate-pins.yml`, README, ADR 0040, спецификация и QA консоли, QA self-agent — на `:server-app:bootJar`.

### Проверено
- Развёрнутый состав jar: 1963 записи, sha256 совпадают с одномодульной сборкой (вложенные `sard-server-core` и
  `sard-server-console` развёрнуты в `BOOT-INF/classes`; `build.time` не сравнивается). Отличаются только место
  записей `META-INF/spring*` (внутри jar ядра), порядок строк манифеста и `layers.idx`.
- Образ из `make server-image` (тот же поддельный каталог пакетов агента у обоих): файловая система совпадает до записи,
  кроме `/app/sard-server.jar`; конфигурация образа идентична.
- Запуск jar без базы: та же строка старта (с версией) и та же причина отказа, что у прежнего jar.
- Целевые тесты сервера зелёные: `SardServerIntegrationTest`, загрузки агентов, консоль, `StatusController`,
  `CaStartupIntegrationTest`, `OnboardingDatabaseDownIntegrationTest`; `license-check` зелёный.

### Не проверено в этом окружении
- Полные ворота `gate.sh server fast`: четыре прогона, каждый раз падает 1 тест (разные: `BindException` в
  `OnboardingDatabaseDownIntegrationTest`, `CaStartupIntegrationTest`), а отчёт Gradle затем ломается `EOFException`.
  Чистый main (`b608141`) в этом же контейнере тоже падает: воркер тестов убит с кодом 137 (SIGKILL). Оба класса по
  отдельности зелёные. Вывод: окружение (память, порты), не изменение; подтвердит CI.
- `make server-jar` (стадия build Dockerfile): Gradle внутри Docker не скачивает плагины (429 Maven Central и Docker
  Hub через прокси). Путь `:server-app:bootJar` и `COPY` новых модулей проверит CI.
- e2e и мутационный прогон (`processMutatedMainResources` больше не зависит от `bootBuildInfo`, build-info приходит из
  test resources).

### Окружение (локально, не в репозитории)
- `~/.gradle/init.d/mirror.init.gradle.kts` — Maven Central через зеркало Google (429); `org.gradle.workers.max=1`;
  `dockerd` с `registry-mirrors: mirror.gcr.io`.

## Контрольная точка 2 — ответы владельца
- `CLAUDE.md:92` — путь jar `server-app/build/libs/sard-server.jar`: владелец разрешил, но файл защищён
  `guard-edit.sh`; правку вносит владелец.
- Версии: LIC1 получает ядро через пре-релизные теги (`X.Y.Z-alpha.N` и т. п.), публикуемые в GitHub Packages;
  публичный API заморожен внутри одного `X.Y.Z` — все `X.Y.Z-*` ABI-совместимы между собой.
- Фаза 2 уходит в PR, полные ворота, образ и e2e проверяет CI.
