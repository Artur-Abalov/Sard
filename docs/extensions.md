<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# Расширения server-core

Для тех, кто пишет расширение открытого ядра Sard: enterprise-модуль (лицензия, SSO, RBAC,
аудит) или свой стартер. Что можно, чем это закреплено и как проверить. Решения —
[ADR 0070](adr/0070-server-core-extension-points.md), поведение —
[спецификация](specs/server/server-core-extensions.feature) (Р1–Р33), рабочий пример —
[`examples/extension-sample`](../examples/extension-sample/README.md).

## Как устроено

- Ядро — библиотека `dev.sard:sard-server-core`; приложение ядра `sard-server.jar` — это она,
  протокол (`sard-proto-jvm`) и консоль (`sard-server-console`) без единой строки своего кода.
- Расширение — Spring Boot стартер: бин `SardExtension` (`id`, `displayName`) из своей
  auto-configuration (`META-INF/spring/org.springframework.boot.autoconfigure.AutoConfiguration.imports`).
- Enterprise-сборка — ядро плюс стартеры, собранные вместе; что из них включено, решает
  лицензия, а не сборка. Без расширений ядро стартует и работает — это проверенная конфигурация.
- Лицензия **никогда** не блокирует бэкап, восстановление и проверку: код запусков, отправки,
  планировщика и проверки от шва лицензии не зависит, это закрепляет тест архитектуры.

## Публичное API

Публично только `dev.sard.server.extension.**`; всё остальное внутреннее и может меняться в
любом выпуске. ABI пакета выгружено в [`server/api/server.api`](../server/api/server.api);
`./gradlew :server:check` падает, если API изменилось без отметки в выгрузке. Члены с
`@InternalSardApi` (методы `@Bean` и проверки швов в классах `*AutoConfiguration`) в выгрузку не
входят: на имена классов `*AutoConfiguration` расширение ссылается в `before = […]`, а их методы
не вызывает.

**Совместимость.** Расширение собирается против ядра `X.Y.Z` и совместимо со всеми
`X.Y.Z-*` (пре-релизы и релиз одного `X.Y.Z` ABI-совместимы). Между разными `X.Y.Z`
совместимости нет: расширения пересобираются под каждый `X.Y.Z`. Проверки версии при старте нет.

## Где взять ядро

Релизный workflow публикует `sard-server-core`, `sard-proto-jvm` и `sard-server-console` в
GitHub Packages репозитория (`https://maven.pkg.github.com/artur-abalov/sard`) для каждого
тега релиза и пре-релиза; версия Maven — тег без `v` (`v1.2.0-beta.3` → `1.2.0-beta.3`).
Файлы те же, что собраны вместе с `sard-server.jar` релиза (ADR 0045), ничего не
пересобирается. Для локальной работы:

```bash
./gradlew :server:publishToMavenLocal :proto-jvm:publishToMavenLocal :server-console:publishToMavenLocal
```

```kotlin
dependencies {
    implementation("dev.sard:sard-server-core:1.2.0-beta.3")
    runtimeOnly("dev.sard:sard-server-console:1.2.0-beta.3")
}
```

Сборку расширения стоит держать со строгой блокировкой зависимостей и
`verification-metadata.xml`, как образец.

## Швы

Шов — интерфейс, реализацию которого ядро даёт по умолчанию (`@ConditionalOnMissingBean`), а
расширение заменяет своей auto-configuration с `@AutoConfiguration(before = [<Шов>AutoConfiguration::class])`.
Две реализации одного шва от разных расширений — отказ старта, текст называет бины и
конфигурации.

| Шов | Ядро по умолчанию | Что делает расширение |
| --- | --- | --- |
| `EditionProvider` (`EditionAutoConfiguration`) | редакция `free`, лицензии нет | сообщает редакцию и `LicenceSummary` (`valid`, `expired`, `absent`, `invalid`, `unavailable`, держатель, срок, строки). Ядро показывает, но не сверяет срок и не применяет лимиты |
| `AccessPolicy` (`AccessPolicyAutoConfiguration`) | администратору можно всё | запрещает `Operation` (метод и шаблон пути OpenAPI) для `Subject` |
| `SubjectSource` (`SubjectSourceAutoConfiguration`) | субъект сессии ядра или тот, что под `SUBJECT_REQUEST_ATTRIBUTE` | называет, кто действует: его видят политика и `actor` событий |
| `TenantResolver` (`TenancyAutoConfiguration`) | один арендатор | определяет арендатора запроса |
| `SessionApi` и фильтр сессии (ADR 0021) | вход паролем администратора | заменяет вход (SSO); заменённый фильтр кладёт `Subject` под `SUBJECT_REQUEST_ATTRIBUTE` |

Правила, которые стоит знать до первой строки кода:

- **Редакция** читается у шва один раз при старте; исключение в `edition()` — отказ старта
  `Edition unavailable: …`. Лицензия читается на каждый `GET /api/v1/server`; исключение в
  `licence()` — состояние `unavailable` и одна строка WARN.
- **Порядок проверок запроса**: Origin (403 `origin_rejected`) → сессия (401) → политика
  (403 `access_denied`). Публичные операции, шаги мастера первого запуска и выход политике не
  подчинены, работа самого сервера (планировщик, цикл проверки) тоже.
- **Отказ закрыт.** Политика бросила исключение — 403 и одна строка WARN. Операция без субъекта —
  401. `SubjectSource` только называет субъекта и не аутентифицирует: без сессии ядра 401, что бы
  он ни ответил; бросил исключение — 403 и WARN, событие при этом доставляется с `actor` = null.
- События входа (`signin.*`) `SubjectSource` не спрашивают: у успешного входа `actor` — субъект
  новой сессии, у неудачи и блокировки — null.

## События ядра

События — `SardEvent` (sealed), обычные события приложения Spring: слушатель — `@EventListener`.
Аудит строится как слушатель, отдельного механизма нет.

| Тип | Когда |
| --- | --- |
| `run.created`, `run.started`, `run.status_changed` | запуск создан; у запуска появился момент начала (один раз за запуск); каждая смена статуса (`from`, `to`, `terminal`) |
| `signin.succeeded`, `signin.failed`, `signin.locked`, `signout` | вход, неудача, блокировка адреса, выход |
| `session.expired` | сессия истекла (`reason`: `idle` — час без запросов, `absolute` — восемь часов с входа) |
| `settings.changed` | изменилась настройка (`setting`, `objectId`); повтор без изменений события не даёт; сброс пароля командой `admin-reset` — один `admin_password` при следующем старте |

Доставка синхронная, **после фиксации** транзакции и не более одного раза. Исключение
слушателя не меняет исход операции и не лишает событие остальных слушателей — оно
логируется. Медленный слушатель задерживает ответ: тяжёлую работу уносите в свой поток.
Общие поля: `type`, `occurredAt`, `tenantId`, `actor` (`Subject` или null для работы сервера).

## REST расширения

Контроллеры расширения — только под `/api/v1`: они под сессией ядра, проверкой Origin и
политикой и попадают в OpenAPI сервера. Контроллер не из ядра вне `/api/v1` (или с путём,
который не определить) — отказ старта; «из ядра» определяется по jar, а не по пакету.

## Миграции расширения

Свои миграции Flyway расширение кладёт в `classpath:db/migration-extensions` (версии — метками
времени, как в ядре, иначе два расширения с `V1__` столкнутся). Их применяет `sard-server migrate`
после миграций ядра, в отдельную таблицу истории `sard.flyway_schema_history_extensions`;
другие места задаёт `SARD_MIGRATE_EXTENSION_LOCATIONS`, и сервер при старте читает тот же
параметр. Сервер, у которого на classpath есть неприменённые миграции расширений, не стартует:
`Database startup refused: EXTENSION_MIGRATIONS_NOT_APPLIED: …`.

## Проверка

- Образец: `./gradlew -p examples/extension-sample build bootJar` после публикации ядра в
  `~/.m2` (так же — задача CI `extension-sample`). Его тест `PublicApiOnlyTest` держит код
  стартера в публичном API.
- Ручная процедура на стенде образца: [docs/qa/server-core-extensions.md](qa/server-core-extensions.md).
