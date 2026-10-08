# Прямые зависимости

Каждая сторонняя библиотека должна быть нужна каркасу сейчас. Лицензии сверены по файлам LICENSE в кэше модулей / POM на Maven Central / package.json в npm.
Совместимость: всё, что попадает в бинарники ядра, совместимо с AGPL-3.0; в Apache-2.0-модулях (`proto/gen/go`, `agent/plugins/sdk`) — только пермиссивные лицензии.

## Go

| Модуль | Зависимость | Версия | Лицензия | Где | Зачем |
|---|---|---|---|---|---|
| proto/gen/go (Apache-2.0) | google.golang.org/protobuf | v1.36.12 | BSD-3-Clause | runtime | сообщения protobuf |
| proto/gen/go (Apache-2.0) | google.golang.org/grpc | v1.84.0 | Apache-2.0 | runtime | gRPC-стабы `AgentService` |
| agent/plugins/sdk (Apache-2.0) | — | — | — | — | только stdlib |
| agent | google.golang.org/grpc, google.golang.org/protobuf | см. выше | Apache-2.0, BSD-3-Clause | runtime (через proto/gen/go) | транспорт к серверу |
| agent | go.yaml.in/yaml/v3 | v3.0.5 | MIT + Apache-2.0 | runtime | чтение YAML-конфига (поддерживаемый преемник архивного gopkg.in/yaml.v3) |
| agent | google.golang.org/genproto/googleapis/rpc (пакет errdetails) | v0.0.0-20260706201446-f0a921348800 | Apache-2.0 | runtime | разбор `google.rpc.ErrorInfo` в отказах `Enroll` (A2a, домен `sard.dev`, ADR 0025) — ранее приходила транзитивно через grpc, объявлена явно, так как код использует её типы напрямую |
| agent | github.com/santhosh-tekuri/jsonschema/v6 | v6.0.3 | Apache-2.0 | runtime | валидация `config_json` по `ConfigSchema()` плагина (draft 2020-12), формат `sard-secret`; ADR 0027 |
| agent | pgregory.net/rapid | v1.3.0 | MPL-2.0 (без приложения B «Incompatible With Secondary Licenses», совместима с AGPL-3.0) | только тесты, в бинарник не попадает | тесты свойств `internal/redact` (A7a): случайные тексты и разбиения на порции с уменьшением контрпримера; своих зависимостей нет |
| agent (плагин postgresql, F1) | — | — | — | — | только stdlib; драйвера PostgreSQL нет: `psql`, `pg_dump`, `pg_dumpall` — программы хоста (пакет `postgresql-client`), в сборку и в пакеты агента не входят, deb и rpm их только предлагают (`Suggests`); ADR 0049 |
| cli | github.com/spf13/cobra | v1.10.2 | Apache-2.0 | runtime | дерево подкоманд `sardctl` |
| cli | github.com/spf13/pflag (транзитивно) | v1.0.9 | BSD-3-Clause | runtime | флаги для cobra |

## Инструменты разработки (`tools/go.mod`, в бинарники не попадают)

| Инструмент | Версия | Лицензия | Зачем |
|---|---|---|---|
| github.com/bufbuild/buf | v1.73.0 | Apache-2.0 | lint и генерация proto |
| google.golang.org/protobuf/cmd/protoc-gen-go | v1.36.12 | BSD-3-Clause | генерация Go-сообщений |
| google.golang.org/grpc/cmd/protoc-gen-go-grpc | v1.6.2 | Apache-2.0 | генерация gRPC-стабов |
| github.com/golangci/golangci-lint/v2 | v2.14.0 | GPL-3.0 | линтер; запускается, не линкуется и не распространяется — на лицензию Sard не влияет |
| github.com/avito-tech/go-mutesting | v0.0.0-20251226130216-48d0401f00fb | MIT | мутационное тестирование Go |
| github.com/goreleaser/nfpm/v2 | v2.47.0 | MIT | сборка deb и rpm агента (`make package`); запускается, в пакеты не попадает |

## Инструменты релиза (из apt раннера, не в `tools/go.mod`)

| Инструмент | Версия | Лицензия | Зачем |
|---|---|---|---|
| minisign | из Ubuntu 24.04 (0.11) | ISC | подпись и проверка `SHA256SUMS` релиза агента; запускается, в пакеты не попадает (`docs/adr/0043-agent-release.md`) |

## Поставляется вместе с агентом (не линкуется)

| Программа | Версия | Лицензия | Зачем |
|---|---|---|---|
| restic (официальный бинарник релиза) | 0.19.1 (`agent/internal/restic/restic-version`) | BSD-2-Clause | хранение, дедупликация и шифрование бэкапов; ADR 0017, 0018. Текст лицензии — `third_party/restic/LICENSE`, в пакете — `LICENSE.restic` |

Зависимость пакета (ставит менеджер пакетов хоста, не поставляется): `openssh-client` (deb) / `openssh-clients` (rpm), OpenSSH, BSD — `ssh` для бэкенда `sftp:` restic; ADR 0047.

## Сервер (Kotlin, Gradle)

Версии без явного номера управляются BOM Spring Boot 4.1.1. Лицензии — из POM на Maven Central.

| Зависимость | Версия | Лицензия | Где | Зачем |
|---|---|---|---|---|
| Kotlin stdlib, kotlin-reflect | 2.4.20 | Apache-2.0 | runtime | язык; reflect нужен Spring/Jackson |
| spring-boot-starter-webmvc (Spring Framework 7, Tomcat 11) | 4.1.1 | Apache-2.0 | runtime | REST `/api/v1/status` |
| spring-boot-starter-actuator | 4.1.1 | Apache-2.0 | runtime | `/actuator/health` |
| spring-boot-starter-data-jpa (Hibernate ORM 7.4, jakarta.persistence-api 3.2) | 4.1.1 | Apache-2.0; persistence-api — EPL-2.0 **или** EDL-1.0 (BSD-3), используем EDL | runtime | таблица `agents` |
| spring-boot-starter-flyway + flyway-database-postgresql | 4.1.1 / 12.4.0 | Apache-2.0 | runtime | миграции |
| org.postgresql:postgresql | 42.7.13 | BSD-2-Clause | runtime | JDBC-драйвер |
| org.bouncycastle:bcpkix-jdk18on (+ bcprov-jdk18on, bcutil-jdk18on транзитивно) | 1.86 | Bouncy Castle Licence (MIT) | runtime | локальный CA: разбор CSR агента, выпуск X.509 (у JDK нет публичного API для этого) — ADR 0014 |
| spring-boot-starter-grpc-server (spring-grpc-core 1.1.1, grpc-netty 1.83.1) | 4.1.1 | Apache-2.0 | runtime | gRPC-сервер `AgentService`; `grpc-services` из него — health-check (`HealthGrpc` в списке открытых сервисов S3) |
| io.grpc:grpc-protobuf (+ com.google.api.grpc:proto-google-common-protos транзитивно) | 1.83.1 / 2.64.1 | Apache-2.0 | runtime | `google.rpc.ErrorInfo` в деталях статуса Enroll (S2b, контракт отказов) — уже транзитивная зависимость spring-boot-starter-grpc-server, объявлена явно, так как код использует её классы напрямую |
| com.google.api.grpc:proto-google-common-protos | 2.64.1 (та же, что приходит транзитивно через grpc-protobuf 1.83.1) | Apache-2.0 | runtime | `google.rpc.ErrorInfo` в отказах перехватчика агентов (S3, ADR 0009) и `Enroll` (S2b); объявлена явно, потому что используется напрямую |
| springdoc-openapi-starter-webmvc-api (без Swagger UI) | 3.1.1 | Apache-2.0 | runtime | `/v3/api-docs`, из него генерируется клиент веба |
| com.networknt:json-schema-validator (+ com.ethlo.time:itu 1.14.0, tools.jackson.dataformat:jackson-dataformat-yaml 3.1.5 и org.snakeyaml:snakeyaml-engine 3.0.1 транзитивно) | 3.0.8 (собрана с Jackson 3.2.1; BOM сводит к 3.1.5) | Apache-2.0 (транзитивные — Apache-2.0) | runtime | проверка `config` источника по `config_schema` плагина — JSON Schema 2020-12, формат `sard-secret`, без загрузки внешних `$ref` (S8b, ADR 0031) |
| tools.jackson.module:jackson-module-kotlin | 3.x (BOM) | Apache-2.0 | runtime | JSON для Kotlin-классов |
| proto-jvm: grpc-protobuf, grpc-stub, grpc-kotlin-stub, protobuf-java, kotlinx-coroutines-core | 1.83.1 / 1.5.0 / 4.35.1 / 1.10.2 | Apache-2.0; protobuf-java — BSD-3-Clause | runtime | JVM-стабы контракта |
| spring-boot-starter-webmvc-test, spring-boot-starter-grpc-server-test, spring-boot-testcontainers | 4.1.1 | Apache-2.0 | только тесты | тестовая инфраструктура |
| testcontainers-postgresql, testcontainers-junit-jupiter | 2.0.5 | MIT | только тесты | настоящий PostgreSQL в тестах |
| kotlin-test-junit5 | 2.4.20 | Apache-2.0 | только тесты | assert-функции |
| mutflow (плагин, core, runtime, junit6) | 1.5.0 | Apache-2.0 | тесты; `mutflow-core` (аннотации) в runtime-classpath, мутированный код в jar не попадает | мутационное тестирование (ADR 0006) |

## Сквозные тесты (`test/e2e`, Gradle-модуль `:e2e`, только тесты)

Новых библиотек нет — те же версии, что у сервера, через BOM Spring Boot 4.1.1.

| Зависимость | Версия | Лицензия | Зачем |
|---|---|---|---|
| proto-jvm (см. выше) | — | Apache-2.0 | клиент `EnrollmentService` и `AgentService` |
| io.grpc:grpc-netty | 1.83.1 | Apache-2.0 | транспорт gRPC-клиента (у сервера — тот же артефакт в runtime) |
| org.postgresql:postgresql | 42.7.13 | BSD-2-Clause | чтение записей сервера (агенты, шаги, снимки, логи); строки запуска швов S6a/S7a; истечение токена |
| testcontainers-junit-jupiter, testcontainers-postgresql | 2.0.5 | MIT | контейнеры PostgreSQL, sard-server, sard-agent |
| kotlin-test-junit5 | 2.4.20 | Apache-2.0 | assert-функции |

Образы: `postgres:18-alpine` (как в `deploy/`), `ubuntu:24.04` с `openssh-client`/`openssh-server` из архива Ubuntu — база образов агента и SFTP-сервера стенда (ADR 0047).
`dxflrs/garage:v2.1.0` (AGPL-3.0) — S3-хранилище стенда (ADR 0047), только запускается.
`nicolaka/netshoot:v0.14` (Apache-2.0) — только `iptables` в сетевом пространстве контейнера агента: односторонний обрыв связи в T3 (`Interruptions.block`).

## Инструменты сборки сервера (в поставку не входят)

| Инструмент | Версия | Лицензия | Зачем |
|---|---|---|---|
| Gradle (wrapper) | 9.7.1 | Apache-2.0 | сборка |
| com.google.protobuf (Gradle-плагин) + protoc, protoc-gen-grpc-java, protoc-gen-grpc-kotlin | 0.10.0 / 4.35.1 / 1.83.1 / 1.5.0 | BSD-3-Clause / Apache-2.0 | генерация JVM-кода из proto |
| io.spring.dependency-management | 1.1.7 | Apache-2.0 | BOM Spring Boot |
| detekt | 2.0.0-alpha.6 | Apache-2.0 | статический анализ, сложность ≤ 8 |
| Spotless + ktlint | 8.10.3 / 1.8.0 | Apache-2.0 / MIT | форматирование Kotlin |
| JaCoCo | 0.8.15 | EPL-2.0 | покрытие; агент только в тестовой JVM |

## Веб (`web/package.json`)

Версии зафиксированы в `web/package-lock.json`. Лицензии — из `package.json` пакетов.

| Пакет | Лицензия | Где | Зачем |
|---|---|---|---|
| @mantine/core 9.6.3 | MIT | runtime | UI-кит (ADR 0003) |
| @mantine/hooks 9.6.3 | MIT | runtime | хуки Mantine (требование @mantine/core) |
| @fontsource/ibm-plex-sans 5.3.0 | OFL-1.1 | runtime | шрифт интерфейса по docs/design/DESIGN.md; самохостинг без обращений к CDN (агент и сервер работают в закрытых контурах); подключены только начертания 400 и 600, кириллица и латиница (ADR 0035) |
| @fontsource/jetbrains-mono 5.3.0 | OFL-1.1 | runtime | моноширинный шрифт для хостов, путей, команд, хэшей, размеров и времени; начертание 400, кириллица и латиница (ADR 0035) |
| @tanstack/react-query 5.104.0 | MIT | runtime | загрузка и кеширование данных API |
| @tanstack/react-router 1.170.39 | MIT | runtime | типизированная маршрутизация (ADR 0015) |
| i18next 26.4.2 | MIT | runtime | локализация ru/en |
| openapi-fetch 0.17.0 | MIT | runtime | типизированный клиент API |
| react 19.3.0 | MIT | runtime | UI |
| react-dom 19.3.0 | MIT | runtime | рендеринг в DOM |
| react-i18next 17.0.15 | MIT | runtime | i18next для React |
| @redocly/openapi-core 1.34.20 | MIT | сборка/тесты | линтер спецификации `npm run lint:api` (ADR 0019); та же версия, что у openapi-typescript |
| @tanstack/react-query-devtools 5.104.0 | MIT | сборка/тесты | devtools Query, только в dev (ADR 0015) |
| @tanstack/react-router-devtools 1.167.2 | MIT | сборка/тесты | devtools роутера, только в dev (ADR 0015) |
| @tanstack/router-generator 1.167.38 | MIT | сборка/тесты | `npm run gen:routes` и проверка дерева маршрутов в CI (ADR 0015) |
| @tanstack/router-plugin 1.168.40 | MIT | сборка/тесты | генерация дерева маршрутов из src/routes (ADR 0015) |
| @types/node 24.19.0 | MIT | сборка/тесты | типы Node для конфигов |
| @types/react 19.3.0 | MIT | сборка/тесты | типы React |
| @types/react-dom 19.3.0 | MIT | сборка/тесты | типы React DOM |
| @vitejs/plugin-react 6.1.1 | MIT | сборка/тесты | React в Vite |
| msw 2.15.0 | MIT | сборка/тесты | моки API в dev и Vitest (ADR 0016) |
| openapi-msw 2.0.0 | MIT | сборка/тесты | типизация обработчиков MSW из OpenAPI (ADR 0016) |
| openapi-typescript 7.13.0 | MIT | сборка/тесты | генерация типов из OpenAPI |
| oxlint 1.85.0 | MIT | сборка/тесты | линтер (ADR 0011) |
| postcss 8.5.28 | MIT | сборка/тесты | PostCSS для Mantine |
| postcss-preset-mantine 1.18.0 | MIT | сборка/тесты | официальная настройка Mantine |
| postcss-simple-vars 7.0.1 | MIT | сборка/тесты | брейкпоинты Mantine |
| prettier 3.9.9 | MIT | сборка/тесты | форматирование |
| typescript 6.0.3 | Apache-2.0 | сборка/тесты | компилятор |
| vite 8.3.1 | MIT | сборка/тесты | сборка и dev-сервер |
| vitest 5.0.2 | MIT | сборка/тесты | юнит-тесты |
