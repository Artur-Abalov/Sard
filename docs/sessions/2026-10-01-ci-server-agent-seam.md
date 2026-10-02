<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-01 — CI: красный job `server` на `main` после A5b

Ветка: `claude/laughing-brown-ok3snp`. База — `main` @ `993456b` (слит PR #30, A5b).

## Симптом
CI run 82 (`main` @ `993456b`), job `server`: `./scripts/gate.sh server` → `770 tests completed, 1 failed`.
Тот же job красный и на PR #30 (run 81). Локально (JDK 25, Docker для Testcontainers,
`LC_ALL=C.UTF-8`) воспроизведено так же: 770 тестов, 1 падение.

## Причина
`AgentSeamIntegrationTest.the real agent registers its snapshot with a certificate from Enroll`:

```
sard-agent: RESTIC_NOT_FOUND: restic was not found at …/no-restic (restic.path in …/agent.yaml)
```

Тест запускает настоящий `sard-agent` и намеренно указывал `restic.path` на несуществующий
файл (комментарий: «restic is deliberately absent: the agent then announces the repository
with an empty id»). С A5b (`59775f4`, ADR 0017) агент при старте выполняет `restic version`
(`agent/internal/restic/check.go`, `CLI.Check`) и без подходящего restic не стартует. Это
задуманное поведение, поэтому устарела предпосылка теста, а не код агента.

## Исправление
- `AgentSeamIntegrationTest`: вместо отсутствующего файла — заглушка `restic` (sh, `rwx------`).
  На `restic version` она печатает `restic <min_version> compiled with go`, на всё остальное
  отвечает `exit 1`. Старт агента проходит; `restic cat config` падает, поэтому репозиторий
  по-прежнему объявляется с пустым id. Ожидания теста (`repository_id = null`) не меняются.
- Версию заглушки тест берёт из `min_version=` в `agent/internal/restic/restic-version`
  (единственный источник, ADR 0017). Путь к файлу передаёт системное свойство
  `sard.test.restic-version-file` (`server/build.gradle.kts`), так же как это сделано в `test/e2e`.

## Проверка
`./scripts/gate.sh server` (full): spotless, detekt, тесты + покрытие, CRAP, мутационное
тестирование → `gate: PASSED (server, full)`.

## Заметки о среде
- В контейнере сессии нужны `openjdk-25-jdk-headless` (apt; Adoptium закрыт прокси) и
  запущенный `dockerd`.
- Maven Central через прокси периодически отвечает 429; Gradle докачивает при повторах.
- Отдельно: в run 79 (PR #29) красный `go (agent)`:
  `TestDialTOFUClassifiesAContextDeadlineAsTemporary` (`agent/internal/enroll/trust_test.go:341`,
  `class = "trust", want temporary`). К server отношения не имеет, в этой сессии не разбиралось.
