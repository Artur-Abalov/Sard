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

## Итог фазы
Список вопросов владельцу и предложения по контрольной точке 1 — в ответе сессии; код — после ответов (фаза 2).
