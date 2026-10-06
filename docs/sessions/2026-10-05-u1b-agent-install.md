<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-05 — U1b: установка агента с сервера Sard

Ветка: `ccr-a4ba4ba3-y0pruh`. Спецификации (утверждены владельцем 2026-10-05, решения В1–В9
и RPM — по рекомендации): `docs/specs/server/agent-install.feature`,
`docs/specs/web/agent-install.feature`, `docs/specs/agent/agent-install.feature`,
`docs/qa/agent-install.md`. Роль — coder: тест первым, потом код.

## Что сделано

Сервер (`dev.sard.server.install`, Kotlin):
- `AgentVersions` — правило «устарел» (В4): обе версии релизные `vX.Y.Z`, версия агента строго
  меньше раздаваемой, числовое сравнение; `outdated` в `AgentSummary` и `AgentDetails` (К3).
- `DownloadsUrl` — адрес раздачи в командах (В1): `SARD_AGENT_DOWNLOADS_URL` (`http`/`https` без
  запроса и фрагмента, без символов, ломающих оболочку) либо `http://<хост AgentEndpoint>:<HTTP-порт>`;
  сервер не стартует с другим значением, сообщение называет переменную. `AgentEndpoint.host`.
- `ReleaseKey` — ключ релизов из `deploy/release/sard-release.pub`, ресурс сборки сервера
  (`processResources`, `COPY` в `deploy/server/Dockerfile`) (В5); тест сверяет его со строкой в `README.md`.
- Манифест: `restic_version`, `arch`, `format` пакетов; `AgentPackageCatalog.resticVersion/signed/fileOf`;
  `AgentOffer` (раздаётся релиз или раздача выключена) — bean `agentOffer` вместо загрузки каталога
  внутри `agentDownloadsMapping`.
- `InstallCommands` и `AgentInstalls` — шаги и их команды (В2, В3, В6), причины пустого обновления.
- REST: `GET /api/v1/agent-install?arch&format&fetch` (К1), `GET /api/v1/agents/{id}/upgrade?format&fetch`
  (К2); `InstallStep` и `ReleaseKey` помечены обязательными в OpenAPI (`requireConstructorParameters`
  ищет классы и в пакете `install`); `openapi.json` и `schema.d.ts` перегенерированы (`make openapi`).
- `application.yaml`, `deploy/docker-compose.yml`, `deploy/.env.example` — `SARD_AGENT_DOWNLOADS_URL`.

Консоль: `install.ts` (чистые функции: текст шага, подстановка `enrollCommand` в диалоге токена),
`AgentInstallBlock` (список агентов: раскрыт в пустом состоянии, свёрнут при агентах; диалог токена),
`AgentUpgradeBlock` (карточка устаревшего агента), пометка «Доступно обновление» цветом notice,
переключатели архитектуры, формата и curl/wget, блок «раздача выключена», шаг подписи с ключом и
ссылкой на README, `ru.json`/`en.json`, моки (`mocks/api/install.ts`, `web2` отмечен `outdated`).

Пакет: `deploy/agent/postinstall.sh` печатает следующий шаг только при первой установке (В8).

Скрипты: `scripts/test-postinstall.sh` (запускает postinstall в контейнере Debian), дополнения
`scripts/test-agent-install.sh` (подсказка при первой установке, нет её при обновлении, 60 секунд без
перезапусков), новый `scripts/test-console-install.sh` (команды из ответа сервера на хостах без интернета,
подпись, обновление) и шаг в `release.yml`.

Документация: `docs/operations/agent-install.md`, ADR `0037`, строка в `docs/release.md`.
Зависимостей не добавлено (`docs/dependencies.md` без изменений).

## Расхождения со спецификацией

- Имя deb в сценариях — `sard-agent_v1.4.0_linux_amd64.deb`; `make package` называет его
  `sard-agent_1.4.0_amd64.deb`. Сервер берёт имена из манифеста, тесты используют настоящие.
- Список утилит не называет `ln` и `sard-agent`: ссылка `/usr/bin/sard-agent` делается `cp -sf`,
  `sard-agent` разрешён как сама программа.
- `enrollCommand` (S2b) без `sudo -u sard-agent`, а шаг `enroll` К1 — с ним; диалог токена по спецификации
  показывает `enrollCommand` без изменений (пояснение шага говорит, от какого пользователя запускать).
- Сочетание arch и format без пакета в релизе (К1) спецификация не описывает: отвечает 200 с пустым списком шагов.

## Проверено

- `./scripts/gate.sh server fast` → `gate: PASSED (server, fast)`; покрытие 96.5% (инструкции), CRAP ≤ 6.
  По пути: CRAP 9 у `ReleaseVersion.parse` — разбит; detekt (длина строки, число функций класса) — исправлено;
  `ApiContractIntegrationTest` и `ApiSerializationIntegrationTest` дополнены новыми операциями и схемами.
- `./scripts/gate.sh web fast` → `gate: PASSED (web, fast)`: 31 файл, 415 тестов, сборка.
- `./gradlew -Pmutflow.enabled=true :server:test` на `install.*`, `AgentPackage*`, `RequireConstructorParametersTest`,
  `InstallConfigurationTest` — без выживших (выживший мутант `plain()` в `DownloadsUrl` был эквивалентным:
  запрос и фрагмент не проходят белый список символов — проверка удалена; три выживших в `InstallCommands` —
  добавлены утверждения в `AgentInstallsTest`). Полный `mutflow` по всему серверу не запускался.
- `scripts/test-postinstall.sh` (контейнер Debian 12) → `all checks passed`: первая установка deb и rpm печатает
  команду enroll от `sard-agent` и указание на консоль, обновление — ничего о регистрации.
- Команды установки из архива и шаг `configure` выполнены вручную в контейнере Debian 12 с поддельным `sudo`
  (без systemd): раскладка как у deb (`/etc/sard` `root:sard-agent 750`, `tls` и `secrets` `sard-agent 700`,
  кэш restic 700, ссылка `/usr/bin/sard-agent`), `configure` не перезаписал изменённый `agent.yaml`.
- `make license-check` → 752 files OK.

Не выполнено в этой среде: `scripts/test-console-install.sh`, дополнения `scripts/test-agent-install.sh`
(`@package`/`@e2e`: нужны привилегированные контейнеры с systemd, собранные `make package` пакеты и образ сервера),
подпись реальным ключом, ручной QA `docs/qa/agent-install.md`, `release.yml` (`actionlint` не запускался).
Docker поднят вручную (`dockerd`) для Testcontainers; образы — через `mirror.gcr.io` из-за лимита Docker Hub.

Дальше — cleaner.
