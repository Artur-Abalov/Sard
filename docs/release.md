<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# Релиз агента и проверка подписи

Решение — `docs/adr/0043-agent-release.md`. Релиз выпускает тег
`vX.Y.Z`, `vX.Y.Z-beta.N` или `vX.Y.Z-rc.N` (схема версий ниже):
`.github/workflows/release.yml` проверяет тег, собирает пакеты агента дважды,
сверяет суммы, собирает jar сервера один раз, проверяет подделки, установку и
обновление deb (Debian, Ubuntu) и rpm (Rocky 9) и e2e на образе
из этого jar и этих пакетов, подписывает `SHA256SUMS` ключом релизов и
публикует GitHub Release и образ из того же jar (ADR 0045). С SC1 (ADR
00XX-draft-supply-chain) к каждому релизу прилагаются SBOM (CycloneDX)
пакетов и образов и происхождение сборки, образы подписываются cosign, а
файлы установки сервера — minisign через `SHA256SUMS.server`; окружение
`release` подтверждается дважды: `sign` (пакеты) и `sign-server` (образы и
файлы сервера).

## Схема версий

Решение — `docs/adr/0048-release-versions.md`, правило —
`scripts/release-version.sh`. Путь к выпуску:

```text
v0.1.0-beta.1 → v0.1.0-beta.2 → … → v0.1.0-rc.1 → v0.1.0-rc.2 → … → v0.1.0
```

- **beta** — функции выпуска ещё добавляются и меняются; для пробы на своих
  машинах, данные и настройки могут потребовать ручных шагов при обновлении.
  Обновление установки сервера поддерживается начиная с первой
  `0.1.0-beta.N`: данные, агенты и пройденный мастер первого запуска
  сохраняются (e2e обновления — `scripts/test-self-agent.sh` с
  `UPGRADE_FROM`, исходная версия — выпущенная бета, запускается вручную;
  без `UPGRADE_FROM` скрипт идёт в CI, джоба `self-agent`). Установку `0.0.1-rc1` не
  обновляют, а переустанавливают с новыми томами
  (`docs/operator/07-upgrade.md`).
- **rc** — кандидат в выпуск: новых функций нет, только исправления; если
  ошибок не найдено, тот же код выходит как `vX.Y.Z`.
- Номер предрелиза — после точки (`rc.2`, не `rc2`): так `rc.10` новее `rc.2`
  и в SemVer, и в deb/rpm. Тег другого вида (`v0.1.0-rc1`, `v0.1.0-alpha.1`)
  `release.yml` отклоняет первой задачей.

| Тег | Образ | deb, rpm | Файлы пакетов |
|---|---|---|---|
| `v0.1.0-beta.1` | `0.1.0-beta.1` | `0.1.0~beta.1` | `sard-agent_v0.1.0-beta.1_amd64.deb`, `sard-agent-v0.1.0-beta.1.x86_64.rpm` |
| `v0.1.0` | `0.1.0`, `0.1`, `latest` | `0.1.0` | `sard-agent_v0.1.0_amd64.deb`, `sard-agent-v0.1.0.x86_64.rpm` |

Тильда в deb и rpm ставит предрелиз ниже выпуска (`0.1.0~rc.1 < 0.1.0`), поэтому
`apt`/`rpm -U` обновляют beta → rc → выпуск. В именах файлов тильды нет: GitHub
заменяет её точкой, и подписанный `SHA256SUMS` перестал бы совпадать с файлами
релиза. Так случилось с `v0.0.1-rc1`: его deb и rpm (версия
`0.0.0~dev.v0.0.1.rc1`, ниже любой `0.1.0~…`) проверяйте по `SHA256SUMS`,
переименовав скачанные файлы обратно (`.dev.` → `~dev.`).

## Ключи релизов

Имена образов и пути ключей — в одном месте, `deploy/release/identity.env`
(D24); `scripts/check-identity.sh` сверяет с ним копии в compose-файле и
`.env.example`, а `release.yml` до сборки проверяет, что оба ключа на месте.

minisign — `SHA256SUMS` пакетов агента и `SHA256SUMS.server`:

| | |
|---|---|
| Файл | `deploy/release/sard-release.pub` |
| ID ключа | `DF5D5B6DB257DBFA` |
| Публичный ключ | `RWT621eybVtd38CL7B33xZrcc8ArYiPt3GlKXyJuk9ZQzDnoUV+kLi2d` |
| С | 2026-10-04 |

cosign — образы `sard-server`, `sard-agent` и аттестации их SBOM:

| | |
|---|---|
| Файл | `deploy/release/cosign.pub` |
| С | дата первого релиза с ним (заполняет владелец вместе с ключом) |

## Проверить релиз

Нужны `minisign` и `sha256sum`; ключ берите из репозитория, а не с сервера,
с которого скачан пакет.

```bash
minisign -Vm SHA256SUMS -p sard-release.pub      # подпись и строка «sard-agent vX.Y.Z»
sha256sum -c --ignore-missing SHA256SUMS         # скачанные файлы
```

Полная проверка каталога релиза (все файлы, версия в подписи и манифесте):
`scripts/release-verify.sh <каталог> vX.Y.Z`; файлов сервера —
`scripts/release-verify-server.sh <каталог> vX.Y.Z`; образов —
`scripts/release-cosign.sh verify images.json`. Что запускает оператор —
`docs/operator/10-security.md`, «Проверка релиза», и
`docs/operator/11-offline.md`; `release.yml` выполняет эти блоки дословно
(`verify-quickstart`, `verify-offline`, без сети).

Сборка воспроизводима: `git checkout vX.Y.Z && make package VERSION=vX.Y.Z`
даёт тот же `SHA256SUMS` при тех же версиях Go и restic.

## Раздача сервером

Сервер версии N раздаёт пакеты агента версии N из своего образа, без
сессии и без обращения к внешней сети:

```bash
curl -fsSO http://sard.example.com:8080/downloads/agent/manifest.json        # версия, артефакты, суммы
curl -fsSO http://sard.example.com:8080/downloads/agent/SHA256SUMS
curl -fsSO http://sard.example.com:8080/downloads/agent/SHA256SUMS.minisig
curl -fsSO http://sard.example.com:8080/downloads/agent/sard-agent_vX.Y.Z_amd64.deb
```

Проверка — как выше, ключом из репозитория: сервер раздаёт подпись, но не
подписывает. `SARD_AGENT_DOWNLOADS=false` выключает раздачу (404). Адрес, с которого консоль
предлагает скачивать пакеты, — `SARD_AGENT_DOWNLOADS_URL` (`docs/operations/agent-install.md`).

## Настройка (один раз, владелец)

Закрытый ключ создаётся на машине владельца и попадает только в секреты GitHub.

1. Ключ: `minisign -G -p sard-release.pub -s sard-release.key` — с паролем.
   Копию `sard-release.key` и пароль — в офлайн-хранилище.
2. `sard-release.pub` — в `deploy/release/`, ID и строку ключа — в таблицу
   выше и в `README.md`; коммит через PR.
3. GitHub → Settings → Environments → `release`:
   - Deployment branches and tags: только теги `v*`;
   - Required reviewers: владелец; запрет самоутверждения не нужен — владелец один;
   - Environment secrets: `MINISIGN_SECRET_KEY` — содержимое
     `sard-release.key` целиком, `MINISIGN_PASSWORD` — пароль.
4. Settings → Rules → Rulesets:
   - ветка `main`: запрет прямых пушей, обязательный PR, запрет force-push и
     удаления; обход — только владелец;
   - теги `v*`: создавать, менять и удалять — только владелец.
5. Settings → Actions → General: «Require approval for all outside
   collaborators» для PR из форков; права `GITHUB_TOKEN` по умолчанию —
   только чтение.
6. Удалить `sard-release.key` с машины, если копия в хранилище проверена.
7. Ключ cosign (SC1), так же на своей машине:
   `cosign generate-key-pair` — с паролем; получаются `cosign.key` и
   `cosign.pub`. Копию `cosign.key` и пароль — в офлайн-хранилище.
   `cosign.pub` — в `deploy/release/`, дата — в таблицу выше, коммит через
   PR (`release.yml` без него не начнёт сборку). В окружение `release`:
   `COSIGN_PRIVATE_KEY` — содержимое `cosign.key` целиком,
   `COSIGN_PASSWORD` — пароль. Затем удалить `cosign.key` с машины.
8. Renovate: установить приложение GitHub Renovate на репозиторий
   (конфигурация — `.github/renovate.json5`); первый PR — dependency
   dashboard. Права workflow `renovate-pins.yml` не требуют секретов.
9. Подтверждение релиза — дважды: задача `sign` (пакеты агента) и
   `sign-server` (образы и файлы сервера, после сборки образов). Обе видят
   ключи; остальные задачи — нет (`scripts/check-workflow-secrets.sh`).

## Смена ключа

Утечка или потеря ключа: новый ключ по шагам 1–3 (minisign) или 7 (cosign),
новый `deploy/release/sard-release.pub` или `cosign.pub`, запись в `README.md`
и здесь с датой смены. До автообновления (этап 2) агенты ключ не знают,
поэтому смена их не ломает. Старые релизы остаются подписаны старым ключом:
храните его публичную часть рядом с датой смены, чтобы их можно было
проверить. Подписи cosign прежних образов после утечки ключа не отзываются
сами — перевыпустите релиз и сообщите в `SECURITY.md`-порядке.
