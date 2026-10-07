<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-07 — R1: версии предрелизов и RPM в релизе

База — `main` @ `4f84861` (после PR #49). Первая задача M3: схема версий
0.1.0-beta.N → 0.1.0-rc.N → 0.1.0, порядок предрелизов в deb и rpm, RPM в
подписанном и раздаваемом наборе, установка и обновление RPM на Oracle
Linux / Rocky с SELinux. Закрывает OQ-143.

## Фаза 1 — исследование (код не менялся, ждёт ответов владельца)

### Проверенные факты (file:line, команды)

- **Версия пакета.** `scripts/package-agent.sh`, `pkg_version`: `vX.Y.Z` →
  `X.Y.Z`, всё остальное → `0.0.0~dev.<описание>` (знаки кроме `[A-Za-z0-9.]`
  заменены точкой). Предрелизного правила нет.
- **Выпущенный `v0.0.1-rc1`** (GitHub Release, предрелиз, 2026-10-06; коммит
  `0860a05`) несёт пакеты версии `0.0.0~dev.v0.0.1.rc1`, а не `0.0.1~rc1`
  (`manifest.json` релиза: `"package_version": "0.0.0~dev.v0.0.1.rc1"`).
  Для обновления это безопасно: версия ниже любой `0.1.0~…`.
- **Дефект: имена файлов с `~` в релизе GitHub.** `SHA256SUMS` и
  `manifest.json` релиза называют файлы `sard-agent_0.0.0~dev.v0.0.1.rc1_amd64.deb`
  и `sard-agent-0.0.0~dev.v0.0.1.rc1-1.x86_64.rpm`, а GitHub при загрузке
  заменил `~` на `.`: ассеты называются `sard-agent_0.0.0.dev.v0.0.1.rc1_amd64.deb`
  и т. д. (проверено: `get_release_by_tag`, загрузка `SHA256SUMS` и
  `manifest.json` релиза). Значит, проверка из заметок релиза
  (`minisign -Vm SHA256SUMS … && sha256sum -c SHA256SUMS`) для deb и rpm,
  скачанных с GitHub, не находит файлы. Раздача сервером не затронута: образ
  берёт файлы из `dist/` с исходными именами. Схема `0.1.0~beta.1` даст тот же
  дефект в каждом предрелизе.
- **nfpm сохраняет `~`** в версии и имени файла для deb и rpm
  (`version_schema: none`; проверено пробной сборкой: `t_0.1.0~beta.1_amd64.deb`,
  `t-0.1.0~beta.1-1.x86_64.rpm`, `dpkg-deb -f … Version` = `0.1.0~beta.1`).
- **Порядок в dpkg** (`dpkg --compare-versions`, проверено): цепочка
  `0.0.1~rc1 < 0.1.0~beta.1 < 0.1.0~beta.2 < 0.1.0~rc.1 < 0.1.0~rc.2 <
  0.1.0~rc.10 < 0.1.0` — каждая следующая новее. Числа в dpkg сравниваются
  как числа, поэтому ловушка `rc10 < rc2` — только у SemVer (сервер), не у
  dpkg. Фикстура обновления в `release.yml` («package to upgrade from»,
  `0.0.0~dev.upgrade-base`) старше выпущенного `0.0.0~dev.v0.0.1.rc1`
  (`dpkg --compare-versions 0.0.0~dev.v0.0.1.rc1 lt 0.0.0~dev.upgrade-base`
  — ложь), то есть обновление именно с выпущенного rc1 она не моделирует.
  rpm (`rpmdev-vercmp`) в этой среде не проверялся: `rpm` не установлен.
- **RPM в наборе.** `make package` собирает rpm (amd64, arm64); они входят в
  `manifest.json`, `SHA256SUMS` (подписывается), `dist-a` → `sign` → релиз и
  в образ сервера (`deploy/server/Dockerfile` копирует файлы из `SHA256SUMS`),
  значит раздаются `/downloads/agent/<файл>`. Проверка состава rpm — в
  `verify` при `REQUIRE_RPM=1`. **Не сделано:** консоль rpm не предлагает —
  `InstallFormat` (`server/.../install/InstallTypes.kt:15`: «RPM stays in the
  release but is not offered»), `format=rpm` → 422 по спецификации
  (`docs/specs/server/agent-install.feature:134-135, 175`). Установка и
  обновление rpm нигде не проверяются: `install` в `release.yml` — только
  Debian 12/13 и Ubuntu 22.04/24.04 (`test-agent-install.sh`,
  `test-console-install.sh`, `test/packages/host.Dockerfile` на apt).
- **Сервер, «доступно обновление».** `server/.../install/AgentVersions.kt`:
  сравниваются только релизы `vX.Y.Z`; предрелиз (`v0.1.0-beta.1`) не
  разбирается, поэтому агент на предрелизе **никогда** не помечается
  устаревшим, и сервер на предрелизе никого не помечает.
- **Теги.** `release.yml` принимает `v[0-9]+.[0-9]+.[0-9]+` и
  `v[0-9]+.[0-9]+.[0-9]+-*` — `v0.0.1-rc1` прошёл; проверки формата нет.
  Версия образа — тег без `v`, пакетов — `pkg_version`, бинарника —
  `-X main.version=$VERSION` (тег как есть).
- **SELinux.** В этой среде SELinux нет (`/sys/fs/selinux` отсутствует), нет
  `/dev/kvm`, Docker не запущен. SELinux — свойство ядра хоста: в контейнере
  на Ubuntu-раннере GitHub (AppArmor) режим enforcing недостижим, `getenforce`
  в контейнере Rocky покажет `Disabled`. Проверить enforcing можно только на
  ядре с SELinux: ВМ (qemu/KVM на раннере `ubuntu-24.04`, образ Rocky 9 /
  Oracle Linux 9 cloud с cloud-init — там enforcing по умолчанию) или
  настоящая машина.

### Предложение: правило версий (в одном месте)

`scripts/release-version.sh` — единственный источник правила; `package-agent.sh`,
`release.yml`, Makefile берут из него.

| Тег | Образ | deb/rpm | Бинарник, манифест `version` |
|---|---|---|---|
| `v0.1.0-beta.1` | `0.1.0-beta.1` | `0.1.0~beta.1` | `v0.1.0-beta.1` |
| `v0.1.0-rc.10` | `0.1.0-rc.10` | `0.1.0~rc.10` | `v0.1.0-rc.10` |
| `v0.1.0` | `0.1.0` (+ `0.1`, `latest`) | `0.1.0` | `v0.1.0` |
| не тег (`git describe`, `upgrade-base`) | — | `0.0.0~dev.<…>` (как сейчас) | как есть |

- Допустимый тег релиза: `^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-(beta|rc)\.[1-9]\d*)?$`.
  Иное (`rc1`, `alpha.1`, `rc.01`, `beta`) — первый шаг `release.yml` падает
  до сборки.
- Имена файлов deb/rpm — **без `~`** (форма тега: `sard-agent_v0.1.0-beta.1_amd64.deb`,
  `sard-agent-v0.1.0-beta.1.x86_64.rpm`; внутри пакета версия с `~`), чтобы
  имена ассетов GitHub совпадали с `SHA256SUMS` и манифестом.
- Тест порядка: общий файл-цепочка (`0.0.0~dev.v0.0.1.rc1` → `0.1.0~beta.1` →
  `0.1.0~beta.2` → `0.1.0~rc.1` → `0.1.0~rc.10` → `0.1.0`) проверяют
  `dpkg --compare-versions`, `rpmdev-vercmp` (в контейнере Rocky) и тест
  Kotlin сервера (SemVer-форма тех же версий).
- Сервер: `AgentVersions` сравнивает по SemVer 2.0 с предрелизами (числовые
  идентификаторы — как числа); git-сборки (`…-5-gabc`) и `dev` по-прежнему не
  помечаются.

### Где проверяем SELinux (предложение)

- Контейнер Rocky 9 / Oracle Linux 9 с systemd (как `host.Dockerfile`) — в
  `install` релиза и в CI: установка, enroll и repo init от `sard-agent`,
  служба, обновление N → N+1. SELinux там выключен — записывается как «без
  SELinux».
- SELinux enforcing — отдельная задача `release.yml` на `ubuntu-24.04` с KVM:
  qemu + облачный образ Rocky 9 (или OL9), cloud-init, тот же сценарий плюс
  `getenforce` = `Enforcing` и пустой `ausearch -m avc`. Локально не
  проверяемо (нет KVM); проверит первый прогон CI.

### Вопросы владельцу — см. сообщение в чате; ответы записываются сюда

### Ответы владельца (2026-10-07)

1. Ветка — та, что есть (`ccr-225f6a16-ag3uew`).
2. Имена файлов deb/rpm без `~` — согласен.
3. Версии вне правила — `0.0.0~dev.<…>`, как сейчас.
4. Обновление с выпущенного rc1 rpm не проверяем: демо шло на Ubuntu, не на
   Oracle Linux.
5. rpm в консоли — через `/ship-feature` (specifier первым). Команды выбирает
   specifier; предложение — `rpm -Uvh` (работает без репозиториев, как
   требует U1b для хостов без интернета; `dnf install ./…` читает метаданные
   репозиториев).
6. SELinux — ВМ в CI (qemu/KVM). Не заработает — откатить и не делать.
7. Проверка 2 — тест правила, без настоящего тега.
8. Тег `v0.1.0-beta.1` ставит владелец, релиз создаёт CI.
9. Схема версий — новый ADR (0047).

## Фаза 2 — реализация

### `/ship-feature`: rpm в консоли и порядок предрелизов

- **specifier** — поправки U1b: `docs/specs/{server,web,agent}/agent-install.feature`,
  `docs/qa/agent-install.md` (части 6–9). Владелец утвердил («Поезжай»,
  2026-10-07) вместе с решениями specifier а–ж: порядок deb, rpm, tar.gz; формат,
  которого нет в релизе, при обновлении — пустые шаги и `arch_unavailable` (без
  нового кода причины); без шага restart; пример 422 — `zip`; число больше
  Long — не релиз; подпись rpm «RHEL, Oracle Linux, Rocky»; пояснение про конфиг
  — для deb и rpm. Не решено: что консоль показывает при пустом списке шагов
  (формата нет в релизе) — в реестр.
