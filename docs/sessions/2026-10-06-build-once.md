<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-06 — Собрать один раз, тестировать собранное (ADR 0045)

Ветка `claude/sweet-brown-d9a0v0`, база — `main` @ `901dad8`. Это задача
инструментов (CI, Makefile, Dockerfile), а не функция продукта, поэтому она
шла не через `/ship-feature`. Решение — `docs/adr/0045-build-once.md`.

## Исследование

Вопрос владельца: собираем ли мы каждый компонент в CI один раз и потом
тестируем, или e2e собирает свои версии? Ответ — нет, не один раз.

- В `ci.yml` не было ни `needs:`, ни `download-artifact`.
- Агент собирался 4 раза: `go (agent)`, `image`, `e2e` с `GO_TAGS=e2e`,
  `server` (`buildTestAgent`). Артефакт `sard-agent-packages` никто не читал.
- Jar сервера собирался 2 раза: в `image` и в `e2e`.
- e2e тестировал сборку стенда, а не поставляемого агента.
- В релизе jar собирался заново и для `install`, и для `image`; e2e в релизе
  не было.

Владелец: «это всё дефекты сборки» — исправить всё.

## Что сделано

- **Тест первым.** `RunStepSeamTest`: агент набора отклоняет `e2e-slow` как
  неизвестный плагин. Со старым `make e2e-images` (агент с тегом `e2e`) тест
  падает.
- **e2e.** Новое свойство `e2e.standAgentImage`. `AgentHost` и
  `AgentEnroller.enroll` принимают `image`. Образ стенда берут только
  `T3Agent` и `SlowStreamTest`.
- **Dockerfile.** Новая стадия `server-jar` (`FROM scratch`). Финальная стадия
  копирует jar из неё. `--build-context server-jar=…` подменяет стадию, и
  `web` и `build` не выполняются. Это проверено отдельным минимальным
  Dockerfile и на настоящем образе: в логе сборки нет шагов `[web]` и
  `[build]`.
- **Makefile.**
  - Сборка: `package` (`DIST`, `ARCHES`), `package-stand`, `server-jar`.
  - Только сборка образов: `server-image`, `e2e-agent-images`, `e2e-assemble`.
  - Тесты: `e2e-test`.
  - `e2e-images` и `e2e` — обёртки.
  - `E2E_SERVER_BUILD_FLAGS` → `SERVER_BUILD_FLAGS`.
- **Сервер.** `-PsardTestAgentBinary` и `-PsardTestAgentVersion`: seam-тест
  запускает переданного агента и сверяет версию из
  `sard.test.agent-version`. Без свойств `buildTestAgent` собирает агента,
  как раньше. Если задан бинарник без версии, сборка падает с понятной
  ошибкой.
- **`ci.yml`:**
  - `packages` — единственная сборка агента и пакетов стенда;
  - `server-image` — единственная сборка jar; из него образ для amd64 и arm64
    и `docker save`;
  - `server` и `e2e` скачивают готовое.

  Задача `image` вошла в `server-image`. Сборка агента и пакетов ушла из
  `go (agent)`.
- **`release.yml`:**
  - `server-jar` — единственная сборка jar;
  - `install` и новая `e2e` тестируют образ из этого jar и пакетов `build a`;
  - `sign` ждёт `e2e`;
  - `image` публикует образ из того же jar (`build-contexts`). Новые действия
    закреплены на коммитах: `actions/setup-java` v6.0.1 и
    `gradle/actions/setup-gradle` v6.4.0, коммиты взяты через `git ls-remote`.
- Документы: ADR 0045, пометки в ADR 0020 и 0036, `test/e2e/README.md`,
  `docs/release.md`, комментарии в `.dockerignore`, `test/e2e/agent/Dockerfile`,
  `stand_e2e.go`, `package-agent.sh`, `gate.sh`.

## Проверка

- `:server:spotlessCheck` и `:server:detekt` — зелёные. `:e2e:compileTestKotlin`
  — без ошибок.
- `make license-check`: 782 файла OK. `actionlint` на обоих workflow — без
  замечаний (инструмент поставлен во временный каталог, в репозиторий не
  добавлен).
- `AgentSeamIntegrationTest`:
  - со своей сборкой (`buildTestAgent`) — 1/1;
  - с бинарником из `make package` (`VERSION=localci1`) — 1/1.

  `--dry-run`: со свойствами `buildTestAgent` не запускается, без них —
  запускается.
- `make package DIST=test/e2e/build/dist ARCHES=amd64` и `make package-stand` —
  OK. Первая попытка с относительным `DIST` упала в nfpm. Исправлено:
  Makefile передаёт скрипту `$(abspath …)`.
- `make server-jar` в облаке упирается в OQ-132: Gradle внутри Docker не
  получает плагин mutflow из Maven Central, а стадия `web` проходит. Поэтому
  jar для локальной проверки собран на хосте (`:server:bootJar` с консолью) и
  подан через `SERVER_JAR_DIR`. Это тот же путь, по которому CI подаёт
  готовый jar.
- `make e2e-assemble`:
  - jar в образе `sard-server:e2e` совпадает с собранным побайтно (sha256);
  - `sard-agent` в `sard-agent:e2e` совпадает с бинарником из tar.gz
    `make package`;
  - стадии `web` и `build` не выполнялись.
- `make e2e-test`: см. ниже.
