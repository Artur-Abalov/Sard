# 11. Установка без доступа в интернет

К каждому релизу на GitHub приложены:

| Файл | Что |
|---|---|
| `sard-<версия>-images-linux-amd64.tar.gz` | `docker save` образов `sard-server`, `sard-agent` (вариант `<версия>-pg18`: агент-сосед и restic для [восстановления](12-server-recovery.md)) и PostgreSQL для amd64 |
| `sard-<версия>-images-linux-arm64.tar.gz` | то же для arm64 |
| `docker-compose.yml`, `sard.env.example` | файлы установки этой версии |
| `images.json` | образы релиза по digest реестра |
| `*.cdx.json` | SBOM образов (CycloneDX), [раздел 10](10-security.md#sbom) |
| `SHA256SUMS.server`, `SHA256SUMS.server.minisig` | суммы файлов выше и их подпись ключом релиза (minisign) |

Пакеты агента уже внутри образа сервера: хосты агентов в закрытой сети берут
их с сервера ([раздел 5](05-agents.md)), доступ в интернет им не нужен. Образ
`sard-agent` из архива нужен и самому серверу: из него запускается агент-сосед
и restic при [восстановлении сервера](12-server-recovery.md).

## На машине с интернетом

```bash
SARD_TAG=<vX.Y.Z>
ARCH=<amd64 или arm64>      # архитектура сервера
mkdir sard-offline && cd sard-offline
for f in "sard-${SARD_TAG#v}-images-linux-$ARCH.tar.gz" docker-compose.yml sard.env.example \
  SHA256SUMS.server SHA256SUMS.server.minisig; do
  curl -fsSLO "https://github.com/Artur-Abalov/Sard/releases/download/$SARD_TAG/$f"
done
# The public key from the repository at that tag; compare it with README.md.
curl -fsSLO "https://raw.githubusercontent.com/Artur-Abalov/Sard/$SARD_TAG/deploy/release/sard-release.pub"
```

Перенесите каталог `sard-offline` на сервер (носитель, `scp` через бастион).

## На сервере

Сначала подпись и суммы — без сети, нужны только `minisign` (пакет
`minisign` в Debian, Ubuntu, EPEL) и файлы из каталога:

<!-- offline-verify:begin -->
```bash
minisign -Vm SHA256SUMS.server -p sard-release.pub
sha256sum --check --ignore-missing SHA256SUMS.server
```
<!-- offline-verify:end -->

`minisign` должен напечатать `Signature and comment signature verified` и
`Trusted comment: sard-server <vX.Y.Z>` той версии, которую вы ставите;
`sha256sum` — `OK` у архива и файлов установки. Иначе не продолжайте: файлы
подменены или повреждены. Архив собран в релизе из образов, подписанных
cosign в реестре; их digest — в `images.json`.

```bash
cd ~/sard-offline
gunzip -c sard-*-images-linux-*.tar.gz | docker load
mkdir -p ~/sard && cp docker-compose.yml ~/sard/ && cp sard.env.example ~/sard/.env
# PostgreSQL по тегу из архива: у загруженного образа нет digest реестра (раздел 16)
sed -n 's/^# SARD_POSTGRES_IMAGE=\(.*\)@.*/SARD_POSTGRES_IMAGE=\1/p' sard.env.example >> ~/sard/.env
cd ~/sard && sudo chgrp 10001 .env && chmod 640 .env
```

Последняя строка `.env` должна стать `SARD_POSTGRES_IMAGE=postgres:<версия>-alpine`.
Образ в compose-файле закреплён по digest реестра. `docker load` такого digest
образу не даёт, поэтому без этой строки `--pull never` ответит `No such image`.

Дальше — [раздел 2](02-install.md), шаги 2–3 (секреты и имя), и запуск с
запретом скачивания образов:

```bash
docker compose up -d --wait --pull never
```

`--pull never` гарантирует, что ни один образ не идёт в реестр; если образа
нет локально, команда сразу падает. Обновление — так же: новый архив,
`docker load`, новая `SARD_VERSION` в `.env`, `docker compose up -d --wait
--pull never` ([раздел 7](07-upgrade.md)).
