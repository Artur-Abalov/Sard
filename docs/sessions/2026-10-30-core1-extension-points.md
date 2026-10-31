<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# CORE1 (SARD-7) — server-core: фаза 3, точки расширения (coder)

Дата: 2026-10-30. Спецификация `docs/specs/server/server-core-extensions.feature`, ADR 0070.

## Сделано (срезы)
1. Шов редакции и лицензии, `edition` в статусе, `GET /api/v1/server`, отказ старта при двух реализациях.
2. Шов политики доступа, субъект сессии, 403 `access_denied`, отказ закрыт, контроллер вне `/api/v1` — отказ.
3. События ядра (запуски, вход/выход, окончание сессии, настройки), доставка, сроки сессии 1 ч / 8 ч.
4. Проверка миграций расширений при старте.
5. Публикации Maven, образец `examples/extension-sample`, CI-задача `extension-sample`, README.
6. ABI-выгрузка пакетов `extension` (встроенная проверка Kotlin Gradle plugin).

## Что узнали
- Тестовые `@Configuration` в пакетах `dev.sard.server.*` попадают в сканирование всех `@SpringBootTest`
  (`@EnableWebMvc` ломал разбор запросов); тестовые конфигурации — `@TestConfiguration`.
- Зависимость `WebMvcConfigurer` от бинов (ObjectMapper, политика) создаёт их рано; провайдеры (`ObjectProvider`).
- Сессии с 1 часом не переживают сдвиги часов тестов на час и больше: такие тесты входят заново.
- `./gradlew :server:cleanTest` перед прогоном: после убитого воркера отчёт Gradle падает с `EOFException`.
