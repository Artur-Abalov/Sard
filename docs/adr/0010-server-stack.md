# 0010 — Стек сервера: Spring Boot 4.1, встроенный gRPC, отдельный proto/jvm

- Статус: принято
- Дата: 2026-09-27

## Решение
- Spring Boot 4.1.1, Kotlin 2.4.20 (переопределяет 2.3.21 из BOM — решение владельца), JDK 25, Gradle 9.7.1.
- gRPC — встроенный `spring-boot-starter-grpc-server` (spring-grpc-core 1.1.1). `AgentGrpcService` наследует `AgentServiceCoroutineImplBase` и помечен `@GrpcService`; все методы отвечают `UNIMPLEMENTED` (унаследовано). Порт — `spring.grpc.server.port` (9090), в тестах 0.
- JVM-код из `proto/` генерируется в отдельном Gradle-проекте `:proto-jvm` (`proto/jvm`, Apache-2.0), сервер зависит от него как от библиотеки. Причины: (1) protobuf-gradle-plugin и mutflow конфликтуют в одном проекте (второй source set `mutatedMain` → `Invalid state: INIT`); (2) сгенерированный код сам собой выпадает из покрытия, CRAP и мутаций; (3) контракт остаётся рядом с `.proto` под той же лицензией. Каталог сборки `proto-jvm` вынесен в корневой `build/`, т. к. исходники — родительский каталог.
- REST — webmvc; OpenAPI — `springdoc-openapi-starter-webmvc-api` без Swagger UI (на одну зависимость меньше). Документ выгружает интеграционный тест в `server/build/openapi/openapi.json` без поля `servers`; `make openapi` копирует его в веб.
- Хранение — Spring Data JPA + Flyway, `ddl-auto=validate`: расхождение сущности и миграции роняет старт; интеграционный тест дополнительно делает round-trip `Agent`.
- Расширения — интерфейс `SardExtension`; enterprise-стартер регистрирует бин из своей автоконфигурации (`AutoConfiguration.imports`), ядро собирает их в `ExtensionRegistry` через `ObjectProvider` в `SardExtensionsAutoConfiguration`. Ноль расширений — проверяемая конфигурация (интеграционный тест и `ApplicationContextRunner`).
- `/api/v1/status` возвращает версию из `build-info.properties` (`-PsardVersion`, по умолчанию `dev`) и `lastVerifiedRestoreAt: null` из заглушки `NoRestoreVerifications`.
- Configuration cache Gradle выключен: protobuf-gradle-plugin 0.10.0 с ним несовместим.
- detekt 2.0.0-alpha.6 собран на Kotlin 2.4.10; в конфигурации `detekt` версия Kotlin закреплена на 2.4.10, иначе BOM Spring подменяет её на 2.4.20.
- В `proto-jvm` BOM Spring импортируется с `bomProperty("kotlin.version", "2.4.20")`: без этого BOM подменял классы компилятора Kotlin на 2.3.x и компиляция падала внутри Build Tools API.

## Образ
`deploy/server/Dockerfile` — multi-stage (JDK 25 → JRE 25, непривилегированный пользователь). Для сборки за TLS-перехватывающим прокси предусмотрены `ARG JAVA_TOOL_OPTIONS` и необязательный BuildKit-секрет `build-ca`; без них сборка обычная.

## Отвергнуто
- Сторонние стартеры net.devh / LogNet — нет Boot 4.
- Генерация proto в самом `server` — конфликт с mutflow (см. выше).
- Swagger UI — не нужен каркасу.
