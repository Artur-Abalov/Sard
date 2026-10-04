<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# Релиз агента и проверка подписи

Решение — `docs/adr/00XX-draft-agent-release.md`. Релиз выпускает тег
`vX.Y.Z`: `.github/workflows/release.yml` собирает пакеты агента дважды,
сверяет суммы, проверяет подделки и установку deb, подписывает `SHA256SUMS`
ключом релизов и публикует GitHub Release.

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
curl -fsSO http://sard.example.com:8080/downloads/agent/sard-agent_X.Y.Z_amd64.deb
```

Проверка — как выше, ключом из репозитория: сервер раздаёт подпись, но не
подписывает. `SARD_AGENT_DOWNLOADS=false` выключает раздачу (404).

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
