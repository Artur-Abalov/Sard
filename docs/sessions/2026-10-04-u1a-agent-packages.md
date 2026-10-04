<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-04 — U1a: подписанные пакеты агента, раздаваемые сервером

Ветка: `claude/amazing-lovelace-sl6i68` (ответ В14: работать в текущей, не в
`feat/u1a-agent-packages`). База — `main @ 2a21a0f`.
ADR: `docs/adr/00XX-draft-agent-release.md` (D12). Спецификации `features/U1b-agent-download.md`
и `features/` в репозитории нет.

## Фаза 1 — исследование и контрольная точка 1

Расхождения промпта с репозиторием:
- `make package` уже собирает tar.gz, deb и rpm (nfpm, ADR 0018) с `SHA256SUMS`; в deb
  уже были пользователь, юнит без автозапуска, `noreplace`-пример конфига, `LICENSE.restic`.
- Релизов не было: ни тегов, ни задачи CI для них, ни публикации образа.
- `deploy/agent/preremove.sh` делал `systemctl disable --now` и при обновлении — служба
  после обновления deb/rpm оставалась выключенной (подтверждено тестом, см. ниже).
- Пример конфига клал ключи в `/etc/sard/`, документация — в `/etc/sard/tls`; пакет не
  создавал ни того, ни другого.
- Spring Security нет; сессия проверяется фильтром только на `/api/v1/*`.

Ответы владельца:
1. Подпись — в CI, не локально; нужно решение для публичного репозитория.
2. Воспроизводимая сборка — сейчас.
3. Ключ — в секретах GitHub; в `main` пушит только владелец.
4. Официальный `minisign` — по возможности.
5. Тест установки в systemd-контейнерах — только для релизов.
6. RPM — оставить.
7. `/etc/sard` `root:sard-agent 0750`, `/etc/sard/{tls,secrets}` `sard-agent 0700` — да.
8. Остановка службы только при удалении, `try-restart` при обновлении — да.
9. Раздача `/downloads/agent/`, при включённой раздаче без пакетов сервер не стартует — да.
10. Образы без релиза несут неподписанные пакеты — да.
11. `protocol_version` в манифесте — версия, на которой говорит агент — да.
12. Раздача сервером — отдельно (не через `/ship-feature` в этой задаче).
13. `REQUIRE_SIGNATURE=1` для restic в релизе — да.
14. Ветка — текущая.

## Фаза 2 — подпись, deb, артефакты релиза

Сделано:
- `scripts/package-agent.sh`: воспроизводимость (`SOURCE_DATE_EPOCH` = время коммита,
  `touch` стейджа, `tar --mtime`, `gzip -n`, у rpm `buildhost: sard-release`),
  `manifest.json` (схема, версия, версия пакета, коммит, restic, `protocol_version`
  из `ProtocolVersion`, артефакты с размером и SHA-256), `SHA256SUMS` включает манифест.
- `deploy/agent/postinstall.sh`, `preremove.sh`: аргументы deb и rpm; первая установка —
  `/etc/sard` `root:sard-agent 0750`, `/etc/sard/{tls,secrets}` `sard-agent 0700`;
  обновление их не трогает и делает `try-restart`; остановка — только при удалении.
- Пример конфига и `docs/operations/{agent-enroll,repo-init}.md` — пути `tls/` и `secrets/`;
  предупреждение о локальном репозитории и `ReadWritePaths` (см. «Найдено»).
- `scripts/release-sign.sh` (ключ из окружения во временный файл `0600`, удаляется при
  выходе; доверенный комментарий `sard-agent <версия>`), `scripts/release-verify.sh`
  (подпись публичным ключом, версия в подписи, `sha256sum --check --strict`, полнота
  списка, версия в манифесте), `scripts/test-release-signing.sh` (подделки, одноразовый
  ключ), `scripts/test-agent-install.sh` + `test/packages/host.Dockerfile`.
- `.github/workflows/release.yml`: две сборки → сверка `SHA256SUMS` и тест подделок,
  установка на Debian 12/13 и Ubuntu 22.04/24.04 → подпись в окружении `release` →
  GitHub Release. Действия закреплены по коммитам, `permissions: {}` по умолчанию.
- `ci.yml`: после `make package` — тест подделок, `manifest.json` в артефакте.
- `docs/release.md` (настройка ключа, окружения, правил репозитория), раздел в `README.md`,
  `minisign` в `docs/dependencies.md`, ADR-черновик.

Проверено (команда → результат):
- `REQUIRE_RPM=1 VERSION=v0.0.1 scripts/package-agent.sh` → exit 0, 6 пакетов, манифест,
  суммы; повтор с `GOFLAGS=-a` в другой каталог → `diff SHA256SUMS` пусто; сборка
  коммита `837fd1a` из `git worktree` по другому пути и из основного дерева → `diff` пусто.
  Разные машины — CI-задача `reproduce`, локально не проверял.
- `scripts/test-release-signing.sh <dist> v0.0.1` → exit 0: правильный релиз принят;
  отказ с ожидаемой причиной для: байт в deb, подмена `SHA256SUMS` без ключа, чужой
  ключ, подпись другой версии, нет файла, лишний файл, нет подписи.
- `scripts/test-agent-install.sh ubuntu:22.04 ubuntu:24.04` (N=v0.0.1 → N+1=v0.0.2) → exit 0. Пользователь, каталоги и права, юнит
  `disabled`/`inactive`, `enroll` и `repo init` от `sard-agent`, служба, агент online с
  v0.0.1; после обновления — юнит `enabled`, тот же агент online с v0.0.2, конфиг, ключи,
  пароль, состояние и каталоги не изменились.
- Контроль: N со старым `preremove.sh` из `main` → тест падает
  `unit enabled after upgrade: got "disabled"` — тест ловит исходный дефект.
- `make license-check` → 672 files OK; `shellcheck -S warning` по новым и изменённым
  скриптам — чисто (предупреждения SC2155 в `scripts/gate.sh` — до этой задачи).

Не проверено локально (полагаю, проверит первый релизный прогон):
- Debian 12/13: зеркала `deb.debian.org` закрыты политикой сети этой среды (403).
- Загрузка ключа restic с keys.openpgp.org и `REQUIRE_SIGNATURE=1` (тоже 403 здесь).
- Сам `release.yml` и `--cgroupns=host` на cgroup v2 раннеров (здесь cgroup v1);
  `actionlint` не запускался.
- Docker Hub отвечал 429 при повторных прогонах — тест установки перезапускался.

Найдено:
- Локальный репозиторий под юнитом (`ProtectSystem=strict`) доступен только на чтение;
  `restic cat config` берёт блокировку и повторяет попытку бесконечно — агент при старте
  молча висит, не выходя в онлайн. Обход — `ReadWritePaths` (документация, тест).
  Молчаливое зависание — поведение агента, вне U1a: OQ-133.

Для владельца перед первым релизом: сгенерировать ключ и настроить репозиторий по
`docs/release.md`; без `deploy/release/sard-release.pub` задача `sign` падает.

## Фаза 3 — образ сервера, раздача, ADR, реестр

Ответы владельца: публичный ключ прислан (`DF5D5B6DB257DBFA`); раздачу делать сейчас,
TDD, без `/ship-feature` (уточнение ответа В12).

Сделано:
- `deploy/release/sard-release.pub`, ID и ключ — в `README.md` и `docs/release.md`.
- `deploy/server/Dockerfile`: стадия `agent-packages` — `AGENT_PACKAGES` (по умолчанию `dist/`),
  проверка версии манифеста против `SARD_VERSION` и `sha256sum --check --strict`, в образ — только
  файлы из `SHA256SUMS` и подпись, `/usr/share/sard/agent-packages/`.
- `make image`, `make up` зависит от `make package` и передаёт `SARD_VERSION` в compose;
  `make e2e-images` собирает пакеты до образа сервера (`AGENT_PACKAGES=test/e2e/build/dist`).
- Сервер, пакет `downloads`: `AgentPackageCatalog` (версия, схема, имена файлов, ETag),
  `AgentPackageHeaders` (Cache-Control, Content-Type), `AgentPackageDirectory` (загрузка и
  проверка каталога при старте), `AgentPackagesHandler` (`ResourceHttpRequestHandler`: Range,
  HEAD, ETag, Last-Modified; только файлы релиза), `AgentDownloadsConfiguration`
  (`sard.agent-packages.enabled|dir`, `SARD_AGENT_DOWNLOADS`, `SARD_AGENT_PACKAGES_DIR`).
  Тесты сервера идут с `SARD_AGENT_DOWNLOADS=false`, кроме `AgentDownloads*IntegrationTest`.
- `scripts/test-image-packages.sh` (тест 6), задача CI `image` собирает пакеты, гоняет тест 6
  и собирает образ; `release.yml` — задача `image`: образ из подписанных файлов, проверка
  подписи в образе, публикация в GHCR (`:vX.Y.Z` и `:X.Y.Z`).
- ADR дополнен (образ, раздача, последствия), OQ-024 закрыт, `deploy/.env.example` и compose —
  `SARD_AGENT_DOWNLOADS`.

Проверено:
- `./gradlew :server:test --tests 'dev.sard.server.downloads.*'` → 28 тестов, 0 упавших
  (тест 5: манифест без сессии, `application/json`, `no-cache`, ETag = SHA-256; каждый пакет
  совпадает с суммой манифеста и строкой `SHA256SUMS`, `immutable`; Range `bytes=10-19` → 206;
  `If-None-Match` → 304; HEAD; файл вне релиза, корень, обход пути → 4xx; выключено → 404).
- `./scripts/gate.sh server` → `gate: PASSED (server, full)`: покрытие 96.3% (инструкции),
  CRAP ≤ 6, mutflow без выживших. По пути: CRAP 7 у `AgentPackageCatalog.of` и
  `AgentPackageHeaders.mediaType` — разнесены; выживший мутант «удалена проверка формы манифеста»
  в `AgentPackageDirectoryTest` — добавлен тест имени файла вне каталога.
- `make package VERSION=v0.0.1 && scripts/test-image-packages.sh v0.0.1` → exit 0: своя версия
  собирается; другая версия — отказ с обеими версиями; изменённый пакет — `FAILED`; пустой
  каталог — «run make package».
- `make license-check` → 683 files OK; shellcheck — чисто; `git diff origin/main -- agent/` пусто.
- `make e2e` (с прокси через `E2E_SERVER_BUILD_FLAGS`, как в прошлых сессиях) на `19d35f7` →
  exit 0, 17 тестов, 0 упавших. Образ сервера собран с пакетами агента той же версии
  (`/usr/share/sard/agent-packages/`: tar.gz, deb, rpm amd64, `manifest.json`, `SHA256SUMS`) и
  стартовал с включённой по умолчанию раздачей. Первый прогон упал на 3 проверках версии:
  коммит посреди прогона сменил `VERSION` между сборкой образов и тестами — не дефект.

Не проверено локально: `release.yml` целиком (подпись реальным ключом, задачи `install` на
Debian, `image` с публикацией в GHCR) — выполнится на первом теге `v*`; `actionlint` не запускался.
