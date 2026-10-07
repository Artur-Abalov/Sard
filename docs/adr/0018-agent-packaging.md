# 0018 — Упаковка агента: tar.gz, deb, rpm с restic и лицензиями

- Статус: принято
- Дата: 2026-09-27

## Контекст
ADR 0017: restic поставляется вместе с агентом. Нужны пакеты для linux/amd64
и linux/arm64, в которых вместе с бинарниками лежат все лицензии, требуемые
при распространении: AGPL-3.0 Sard, BSD-2 restic и лицензии Go-модулей,
вкомпилированных в `sard-agent` (BSD/MIT требуют воспроизводить текст).

## Решение
- `make package` → `scripts/package-agent.sh` → `dist/`:
  `sard-agent_<версия>_linux_<arch>.tar.gz`, `.deb`, `.rpm`, `SHA256SUMS`.
- Бинарник: `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=…"`,
  статический. restic — официальный релиз, полученный `fetch-restic.sh` с
  проверкой SHA-256 (ADR 0017).
- Раскладка deb/rpm: `/usr/libexec/sard/{sard-agent,restic}` (до v0.1.0 была
  `/usr/lib/sard/`; перенос — ADR 0047, причина — SELinux), ссылка
  `/usr/bin/sard-agent`, unit `/usr/lib/systemd/system/sard-agent.service`,
  пример конфига `/etc/sard/agent.example.yaml` (config, не перезаписывается),
  лицензии в `/usr/share/doc/sard-agent/`. `os.Executable()` на Linux
  разыменовывает ссылку, поэтому `restic.path` по умолчанию —
  `/usr/libexec/sard/restic`.
- Лицензии в каждом пакете: `LICENSE` (AGPL-3.0 Sard), `LICENSE.restic`,
  `THIRD_PARTY_LICENSES` (собирается из кэша модулей по
  `go list -deps` для целевой архитектуры; модуль без файла лицензии
  останавливает сборку), `NOTICE` (версия, коммит, ссылки на исходники —
  предложение исходного кода по AGPL).
- deb/rpm собирает nfpm (MIT) из `deploy/agent/nfpm.yaml` — один файл для
  обоих форматов, без `dpkg-deb`/`rpmbuild` в системе. Закреплён в
  `tools/go.mod`.
- Установка создаёт системного пользователя `sard-agent` и не включает
  службу: сначала нужен `/etc/sard/agent.yaml`. Удаление останавливает
  службу; пользователь, конфиг, состояние и кэш остаются.
- unit: `StateDirectory=sard-agent`, `CacheDirectory=sard/restic` —
  при `ProtectSystem=strict` systemd сам создаёт и открывает на запись
  `/var/lib/sard-agent` и кэш restic. Другой `restic.cache_dir` требует
  drop-in с `ReadWritePaths=` (описано в unit и примере конфига): открывать
  на запись произвольный путь из конфига unit не может (уточнено ADR 0030).
- Версия пакета: тег `vX.Y.Z` → `X.Y.Z`; иначе `0.0.0~dev.<git describe>`,
  что ниже любого релиза и в deb, и в rpm.
- Скрипт проверяет состав каждого архива и пакета (rpm — если есть `rpm`);
  CI (`go`, модуль `agent`) собирает пакеты и выкладывает их артефактом.
  В CI задан `REQUIRE_RPM=1` и ставится `rpm`: без утилиты проверка rpm
  падает, а не пропускается; локально без `rpm` проверка по-прежнему
  пропускается с сообщением.

## Отвергнуто
- `dpkg-deb` + `rpmbuild` — два описания пакета и системные зависимости.
- goreleaser — берёт на себя релизы целиком; сейчас нужна только упаковка.
- Устанавливать в `/usr/local/bin` — место администратора, не пакетов.

## Отложено
- Подпись пакетов и репозиториев apt/yum.
- Публикация пакетов в релизах GitHub.
