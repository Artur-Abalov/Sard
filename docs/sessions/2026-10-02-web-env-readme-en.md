<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-02 — среда Claude Code on the web и README на английском

Ветка: `claude/setup-readme-english-wjqujq`. База — `main` @ `7038c7e`.
Конфигурация и документация, не продуктовая фича: через `/ship-feature` не шла.

## Что сделано
- **`.claude/hooks/session-start.sh`** — хук SessionStart, только при `CLAUDE_CODE_REMOTE=true`.
  Ставит `openjdk-25-jdk-headless` (в образе JDK 21), Node.js 24 из nodejs.org в
  `/opt/node-<версия>` со ссылкой `/opt/node24` (в образе Node 22 стоит первым в `PATH`,
  поэтому `/opt/node24/bin` добавляется в начало `PATH` и в `CLAUDE_ENV_FILE`), `make tools`,
  `go mod download` модулей `go.work`, `npm install` в `web/`, `./gradlew :server:testClasses`
  с `LC_ALL=C.UTF-8`. Maven Central отвечал 429 общему egress — шаг Gradle повторяется
  (10/30/60 с). В `CLAUDE_ENV_FILE` пишутся `JAVA_HOME`, `LC_ALL=C.UTF-8`, `PATH`.
  Синхронный режим, таймаут 900 с.
- **`.claude/settings.json`** — добавлен только блок `hooks.SessionStart`; гейты не тронуты.
- **`README.md`** переписан на английском; статус «каркас» заменён на актуальный (этап 1 в
  разработке), добавлены разделы про хук и документацию. Якорь в `CONTRIBUTING.md` обновлён
  на `#build-and-check`.

## Проверка
- Хук: три прогона, последний — код 0 за 11 с (кэши прогреты), первый полный — ~3,5 мин.
- `make license-check`: 589 files OK.
- `cli`: `gofmt -l`, `go vet`, `golangci-lint` — 0 issues; `go test ./...` — ok.
- `web`: `npm run lint`, `npm run typecheck` — без ошибок; `npm test` — 13 файлов, 100 тестов.
- `server`: `spotlessCheck detekt` — код 0; `:server:test --tests '*ArchitectureTest'` — код 0.
