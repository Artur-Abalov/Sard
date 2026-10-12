# Contributing to Sard · Участие в разработке Sard

[English](#english) · [Русский](#русский)

## English

### Before you open a pull request

1. **Sign the CLA.** Contributions are accepted only under the individual
   contributor license agreement, [docs/legal/CLA.md](docs/legal/CLA.md) (in
   Russian). You keep authorship; the copyright holder gets the right to
   distribute your contribution, including under other licenses. There is no
   automated check yet: leave the comment "I have read and agree to the Sard
   CLA" on your first pull request and send a signed copy to the address given
   in the CLA.
2. **Run the quality gate:** `make gate`, or `make gate M=<module>` for one
   module (`proto gen sdk tools agent cli server web`). A pull request with a
   red gate is not accepted. Thresholds are defined once, in
   `scripts/gate.sh`: coverage ≥ 80%, CRAP ≤ 6, cyclomatic complexity ≤ 8,
   mutation score ≥ 0.80 for Go and no surviving mutflow mutants for Kotlin.
   Do not disable tests, lower thresholds or suppress linters: fix the cause.
3. **Markdown changes** are checked by the docs workflow
   (`.github/workflows/docs.yml`); run the same checks locally:

   ```bash
   npx --yes markdownlint-cli2@0.18.1 "**/*.md"
   lychee --offline --include-fragments --exclude-path node_modules --exclude-path web/dist './**/*.md'
   ```

Security issues are not reported through pull requests or issues; see
[SECURITY.md](SECURITY.md).

### Language

- Commit messages, pull request titles, code comments and identifiers: English.
- Pull request descriptions and discussion: English or Russian.
- `README.md` is in English, `README.ru.md` in Russian; keep both in step.
  ADRs, specifications, the operator guide and session logs are in Russian.

### Build

Requirements and commands are in the [README](README.md#build-and-check). In
short: `make proto build test lint`; a local stand is `make up` / `make down`.

Kotlin test class names contain Cyrillic, so Gradle needs a UTF-8 locale.
`make` (including `make gate` and `make gate-fast`) sets `LC_ALL=C.UTF-8` for
its own Gradle targets. When you run `scripts/gate.sh` or `./gradlew`
directly, `export LC_ALL=C.UTF-8` first, and stop a Gradle daemon or Kotlin
compile daemon started without it (`./gradlew --stop`,
`pkill -f KotlinCompileDaemon`).

Two Gradle builds in one `server/build` corrupt each other's test results.
`make` takes the lock `.gradle/sard-build.lock` (`flock`, not on macOS) for
`gate`, `gate-fast` and the Gradle targets, so run the gate through `make`
when another build may be running.

### Style

- **Go:** `gofmt`, `go vet`, `golangci-lint` (`.golangci.yml`). `main` is a
  thin wrapper over a testable `run(...)`. Dependencies go into constructors;
  tests use fakes, never the network or the host's file system.
- **Kotlin:** ktlint via Spotless (`./gradlew :server:spotlessApply`), detekt.
  Coroutine dispatchers are injected.
- **Web:** oxlint and Prettier (`npm run format`); the web UI has no business
  logic.
- **Every source file** starts with an SPDX header and a copyright line
  (`make license-check`):

  ```text
  // SPDX-License-Identifier: AGPL-3.0-only      (Apache-2.0 under proto/ and agent/plugins/sdk/)
  // Copyright 2026 Artur Abalov
  ```

- **Minimal dependencies.** Each new library goes into
  [docs/dependencies.md](docs/dependencies.md) with its license and the reason.
  The license must be compatible with AGPL-3.0 (core) or Apache-2.0 (SDK,
  proto).
- **Non-trivial decisions** get an ADR in [docs/adr/](docs/adr/).

### Tests

Test first, code second. The project constitution, [CLAUDE.md](CLAUDE.md),
is the full definition of done.

## Русский

### Прежде чем отправить PR

1. **Подпишите CLA.** Вклад принимается только после подписания индивидуального лицензионного соглашения контрибьютора — [docs/legal/CLA.md](docs/legal/CLA.md). Вы сохраняете авторство; правообладатель получает право распространять ваш вклад, в том числе под другими лицензиями. Пока автоматической проверки нет: приложите к первому PR комментарий «I have read and agree to the Sard CLA» и отправьте подписанную копию по адресу, указанному в CLA.
2. **Прогоните шлюз качества:** `make gate` (или `make gate M=<модуль>` для одного модуля). PR с красным шлюзом не принимается.
3. **Изменения в Markdown** проверяет workflow docs (`.github/workflows/docs.yml`); локально — те же команды:

   ```bash
   npx --yes markdownlint-cli2@0.18.1 "**/*.md"
   lychee --offline --include-fragments --exclude-path node_modules --exclude-path web/dist './**/*.md'
   ```

Об уязвимостях не сообщают через PR и issue — см. [SECURITY.md](SECURITY.md).

### Язык

- Сообщения коммитов, заголовки PR, комментарии и идентификаторы в коде — на английском.
- Описание PR и обсуждение — на русском или английском.
- `README.md` — на английском, `README.ru.md` — на русском; меняйте оба вместе.
  ADR, спецификации, руководство по эксплуатации и журналы сессий — на русском.

### Сборка

Требования и команды — в [README](README.md#build-and-check). Коротко: `make proto build test lint`, локальный стенд — `make up` / `make down`.

Тесты сервера цитируют русские названия сценариев из спецификаций в именах
тестов Kotlin, поэтому имена файлов классов содержат кириллицу. Компилятор
Kotlin берёт кодировку имён файлов (`sun.jnu.encoding`) только из локали
процесса — JVM-флаги на это не влияют. `make` (включая `make gate` и
`make gate-fast`) задаёт `LC_ALL=C.UTF-8` сам для своих gradle-целей. При
запуске `scripts/gate.sh` напрямую (не через `make`) или `./gradlew`
экспортируйте локаль вручную: `export LC_ALL=C.UTF-8` (и `./gradlew --stop`,
если демон Gradle уже был запущен без неё — устаревший демон компиляции
Kotlin хранит своё старое окружение, поэтому его тоже нужно остановить,
например `./gradlew --stop` и `pkill -f KotlinCompileDaemon`; проверка в
`server/build.gradle.kts` — best-effort и не гарантированно поймает
устаревший демон).

Две сборки Gradle в одном `server/build` портят друг другу результаты тестов
(`EOFException` при чтении `test-results/binary`). Хук SubagentStop запускает
шлюз после каждого субагента, поэтому `make` берёт блокировку
`.gradle/sard-build.lock` (`flock`, нет на macOS — там без неё) для `gate`,
`gate-fast` и gradle-целей. Запускайте шлюз через `make`, а не `scripts/gate.sh`
напрямую, если рядом может идти другая сборка.

### Стиль

- **Go:** `gofmt`, `go vet`, `golangci-lint` (конфиг `.golangci.yml`). `main` — тонкая обёртка над тестируемой `run(...)`. Зависимости передаются в конструкторы; в тестах — фейки, без сети и файловой системы хоста.
- **Kotlin:** ktlint через Spotless (`./gradlew :server:spotlessApply`), detekt. Диспетчеры корутин инжектируются.
- **Веб:** oxlint + Prettier (`npm run format`), в веб-интерфейсе нет бизнес-логики.
- **Каждый исходный файл** начинается со SPDX-заголовка и строки copyright:

  ```text
  // SPDX-License-Identifier: AGPL-3.0-only      (Apache-2.0 в proto/ и agent/plugins/sdk/)
  // Copyright 2026 Artur Abalov
  ```

  Проверка — `make license-check`.
- **Зависимости минимальны.** Каждая новая библиотека добавляется в [docs/dependencies.md](docs/dependencies.md) с лицензией и причиной. Лицензия должна быть совместима с AGPL-3.0 (ядро) или с Apache-2.0 (SDK, proto).
- **Нетривиальные решения** оформляются ADR в [docs/adr/](docs/adr/).

### Тесты

Тест сначала, код потом. Пороги (покрытие ≥ 80%, CRAP ≤ 6, цикломатическая сложность ≤ 8, мутационный score ≥ 0.80 для Go и ни одного выжившего мутанта mutflow для Kotlin) заданы в `scripts/gate.sh`. Отключать тесты, понижать пороги и подавлять линтеры нельзя — исправляйте причину.
