# 11. Установка без доступа в интернет

К каждому релизу на GitHub приложены:

| Файл | Что |
|---|---|
| `sard-<версия>-images-linux-amd64.tar.gz` | `docker save` образов сервера и PostgreSQL для amd64 |
| `sard-<версия>-images-linux-arm64.tar.gz` | то же для arm64 |
| `docker-compose.yml`, `sard.env.example` | файлы установки этой версии |
| `SHA256SUMS.server` | суммы файлов выше |

Пакеты агента уже внутри образа сервера: хосты агентов в закрытой сети берут
их с сервера ([раздел 5](05-agents.md)), доступ в интернет им не нужен.

## На машине с интернетом

```bash
SARD_TAG=<vX.Y.Z>
ARCH=<amd64 или arm64>      # архитектура сервера
mkdir sard-offline && cd sard-offline
for f in "sard-${SARD_TAG#v}-images-linux-$ARCH.tar.gz" docker-compose.yml sard.env.example SHA256SUMS.server; do
  curl -fsSLO "https://github.com/Artur-Abalov/Sard/releases/download/$SARD_TAG/$f"
done
sha256sum --check --ignore-missing SHA256SUMS.server
```

Перенесите каталог `sard-offline` на сервер (носитель, `scp` через бастион).

## На сервере

```bash
cd ~/sard-offline
sha256sum --check --ignore-missing SHA256SUMS.server
gunzip -c sard-*-images-linux-*.tar.gz | docker load
mkdir -p ~/sard && cp docker-compose.yml ~/sard/ && cp sard.env.example ~/sard/.env
cd ~/sard && chmod 600 .env
```

Дальше — [раздел 2](02-install.md), шаги 2–3 (секреты и имя), и запуск с
запретом скачивания образов:

```bash
docker compose up -d --wait --pull never
```

`--pull never` гарантирует, что ни один образ не идёт в реестр; если образа
нет локально, команда сразу падает. Обновление — так же: новый архив,
`docker load`, новая `SARD_VERSION` в `.env`, `docker compose up -d --wait
--pull never` ([раздел 7](07-upgrade.md)).
