<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# Образец расширения server-core

Образец показывает, как enterprise-сборка встаёт в точки расширения открытого ядра
(CORE1 фаза 3, `docs/specs/server/server-core-extensions.feature`). Он нужен для тестов и
документации, не для продукта: лицензия AGPL-3.0-only, в релиз он не входит.

Это отдельная Gradle-сборка со своим `settings.gradle.kts` и своими файлами блокировки
зависимостей (строгими, как в корне). Она собирается против `sard-server-core`,
опубликованного в локальный репозиторий Maven, а не против исходников соседнего модуля, —
так же, как будет собираться enterprise.

## Что в образце

| Что | Где |
|---|---|
| Расширение `sample`, «Sard Sample Extension» | `SampleAutoConfiguration` (`META-INF/spring/…AutoConfiguration.imports`) |
| Шов редакции и лицензии: редакция `sample` | `SampleEdition`, свойство `sard.sample.licence` |
| Шов политики доступа | `SamplePolicy`, свойства `sard.sample.deny`, `sard.sample.policy-failing` |
| Слушатель событий ядра, пишет строку в `sample_events` | `SampleEventListener`, свойство `sard.sample.listener` |
| Своя миграция `V1__sample_events.sql` | `db/migration-extensions` |
| Свои эндпоинты `GET /api/v1/sample/events`, `POST /api/v1/sample/notes` | `SampleController` |

Свойства (их же задают переменные окружения `SARD_SAMPLE_LICENCE` и т. д.):

- `sard.sample.licence` — `valid` (по умолчанию), `expired`, `absent`, `invalid` или `failing`
  (шов лицензии бросает исключение). `valid` и `expired` сообщают держателя «Pilot LLC», срок
  `2026-12-31T00:00:00Z` или `2026-10-01T00:00:00Z` и строку `Agents: 1`.
- `sard.sample.deny` — операции, которые политика запрещает: `METHOD /шаблон/пути` через
  запятую, например `DELETE /api/v1/sources/{sourceId}`.
- `sard.sample.policy-failing` — `true`: политика бросает исключение (доступ закрывается).
- `sard.sample.listener` — `record` (по умолчанию), `throw` или `slow` (три секунды, затем запись).

## Сборка и тесты

Из корня репозитория, нужен Docker (Testcontainers) и JDK 25:

```bash
# 1. server-core, протокол и консоль — в ~/.m2 (версия dev, если не задан -PsardVersion)
./gradlew :server:publishToMavenLocal :proto-jvm:publishToMavenLocal :server-console:publishToMavenLocal

# 2. сборка и тесты образца, jar для стенда
./gradlew -p examples/extension-sample build bootJar
```

Тесты образца поднимают PostgreSQL (образ из `deploy/postgres.env`), выполняют
`deploy/db/bootstrap.sql` и миграции, как db-init и migrate, и запускают сервер ядра с
образцом на classpath. Тот же порядок выполняет CI (задача `extension-sample`).

Файлы блокировки обновляются так (после публикации ядра):

```bash
./gradlew -p examples/extension-sample --write-locks resolveAndLockAll
```

## Стенд для ручной проверки

Стенд — `deploy/docker-compose.yml` с сервером и шагом `migrate` из образа образца:

```bash
./gradlew :server:publishToMavenLocal :proto-jvm:publishToMavenLocal :server-console:publishToMavenLocal
./gradlew -p examples/extension-sample bootJar
cp deploy/.env.example deploy/.env      # задать SARD_VERSION (любое значение) и пароли, как в docs/operator/02-install.md
export COMPOSE_FILE=deploy/docker-compose.yml:examples/extension-sample/docker-compose.sample.yml
docker compose build migrate server
docker compose up -d --wait
```

Миграция образца применяется шагом `migrate` после миграций ядра, в таблицу
`sard.flyway_schema_history_extensions`. Если запустить сервер образца на базе, где `migrate`
не выполнялся с образцом, сервер откажется стартовать:
`Database startup refused: EXTENSION_MIGRATIONS_NOT_APPLIED:`.

Свойства образца меняются переменными окружения и применяются **только перезапуском** сервера:

```bash
SARD_SAMPLE_LICENCE=expired docker compose up -d --force-recreate server
```

Дальше — процедура `docs/qa/server-core-extensions.md`.

## Что образец не показывает

Лицензия не блокирует бэкап, восстановление и проверку, и ядро не применяет лимиты лицензии
(`Agents: 1` — строка для показа). Это свойство ядра: в образце нет кода, который
пытался бы иначе, а тесты ядра закрепляют его с любым состоянием лицензии.
