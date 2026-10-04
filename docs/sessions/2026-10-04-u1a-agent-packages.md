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
  суммы; повтор с `GOFLAGS=-a` в другой каталог → `diff SHA256SUMS` пусто (тот же
  каталог исходников; сборка на другом пути — в CI-задаче `reproduce`, локально не проверял).
- `scripts/test-release-signing.sh <dist> v0.0.1` → exit 0: правильный релиз принят;
  отказ с ожидаемой причиной для: байт в deb, подмена `SHA256SUMS` без ключа, чужой
  ключ, подпись другой версии, нет файла, лишний файл, нет подписи.
- `scripts/test-agent-install.sh ubuntu:24.04` (N=v0.0.1 → N+1=v0.0.2) → exit 0;
  `ubuntu:22.04` → exit 0 (отдельным запуском). Пользователь, каталоги и права, юнит
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
