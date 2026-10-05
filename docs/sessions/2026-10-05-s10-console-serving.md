# Сессия 2026-10-05: S10 — сервер отдаёт консоль, консоль в образе

Ветка `claude/determined-volta-pg43zw`. Спецификация `docs/specs/server/console-serving.feature`,
QA `docs/qa/console-serving.md`, решение — [ADR 0037](../adr/0037-console-served-by-server.md).

## Решения владельца (2026-10-05)

О1 принято (`style-src 'unsafe-inline'`); О2 заголовки безопасности только на ответах консоли;
О3 без сжатия и ETag; О4 `meta sard-version` принят; О5 `sard.console.location` остаётся
внутренним; О6 405 с `Allow`; О7 не трогать (404 под `/api/v1/*` как был). Записано в QA.

## Что сделано

- `server/.../console/`: `ConsoleRoutes` (чистая функция правил Р4), `ConsoleBundle`,
  `ConsoleFilter` (Р4-Р6, Р8), `ConsoleAutoConfiguration` (регистрация фильтра, одна запись
  INFO без консоли, Р3). Фильтр сессии и Origin по-прежнему только на `/api/v1/*`.
- `server/build.gradle.kts`: `-PsardConsoleDist=<каталог>` кладёт набор в `console/` jar;
  каталог без `index.html` — ошибка с именем свойства и каталога.
- `web`: `src/buildVersion.ts` (чистые функции) и плагин Vite пишут `<meta name="sard-version">`.
- `deploy/server/Dockerfile`: стадия `web` (Node 24 на `$BUILDPLATFORM`), `.dockerignore`,
  `make build` (web до jar), `scripts/smoke-server.sh` (страница и версия консоли),
  `test/e2e/.../ConsoleImageTest.kt`, комментарии в `ci.yml` и `release.yml`.
- Тесты: `ConsoleRoutesTest`, `ConsoleFilterTest`, `ContentTypesTest` (mutflow),
  `ConsoleStartupTest` (@startup), `ConsoleServingIntegrationTest`,
  `ConsoleAbsentIntegrationTest`, `ConsoleWithoutDatabaseIntegrationTest` (@http).

## Находки

- `npm run build` (`tsc -b`) читает `agent/plugins/files/schema.json` вне `web/` (тесты
  консоли импортируют его): стадия `web` копирует этот один файл, `.dockerignore` его
  открывает.
- Среда: сборка образа — `--network host`, секрет `build-ca`, `HTTPS_PROXY` для npm,
  `JAVA_TOOL_OPTIONS` для Gradle; `E2E_SERVER_BUILD_FLAGS` с теми же флагами.
