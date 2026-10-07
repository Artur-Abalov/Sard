<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# Релиз агента и проверка подписи

Решение — `docs/adr/0043-agent-release.md`. Релиз выпускает тег
`vX.Y.Z`, `vX.Y.Z-beta.N` или `vX.Y.Z-rc.N` (схема версий ниже):
`.github/workflows/release.yml` проверяет тег, собирает пакеты агента дважды,
сверяет суммы, собирает jar сервера один раз, проверяет подделки, установку и
обновление deb (Debian, Ubuntu) и rpm (Oracle Linux 9, Rocky 9) и e2e на образе
из этого jar и этих пакетов, подписывает `SHA256SUMS` ключом релизов и
публикует GitHub Release и образ из того же jar (ADR 0045).

## Схема версий

Решение — `docs/adr/0047-release-versions.md`, правило —
`scripts/release-version.sh`. Путь к выпуску:

```text
v0.1.0-beta.1 → v0.1.0-beta.2 → … → v0.1.0-rc.1 → v0.1.0-rc.2 → … → v0.1.0
```

- **beta** — функции выпуска ещё добавляются и меняются; для пробы на своих
  машинах, данные и настройки могут потребовать ручных шагов при обновлении.
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

## Ключ релизов

| | |
|---|---|
| Файл | `deploy/release/sard-release.pub` |
| ID ключа | `DF5D5B6DB257DBFA` |
| Публичный ключ | `RWT621eybVtd38CL7B33xZrcc8ArYiPt3GlKXyJuk9ZQzDnoUV+kLi2d` |
| С | 2026-10-04 |

## Проверить релиз

Нужны `minisign` и `sha256sum`; ключ берите из репозитория, а не с сервера,
с которого скачан пакет.

```bash
minisign -Vm SHA256SUMS -p sard-release.pub      # подпись и строка «sard-agent vX.Y.Z»
sha256sum -c --ignore-missing SHA256SUMS         # скачанные файлы
```

Полная проверка каталога релиза (все файлы, версия в подписи и манифесте):
`scripts/release-verify.sh <каталог> vX.Y.Z`.

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

## Смена ключа

Утечка или потеря ключа: новый ключ по шагам 1–3, новый
`deploy/release/sard-release.pub`, запись в `README.md` и здесь с датой смены.
До автообновления (этап 2) агенты ключ не знают, поэтому смена их не ломает.
