# 2. Установка

Сервер ставится из опубликованного образа `ghcr.io/artur-abalov/sard-server`
(amd64 и arm64) вместе с PostgreSQL через Docker Compose. Файлы установки —
`docker-compose.yml` и `sard.env.example` — прикладываются к каждому релизу на
GitHub. Без доступа к реестру — [раздел 11](11-offline.md).

## 1. Файлы релиза

```bash
SARD_TAG=v0.0.1-rc.1        # релиз: https://github.com/Artur-Abalov/Sard/releases
mkdir -p ~/sard && cd ~/sard
curl -fsSLO "https://github.com/Artur-Abalov/Sard/releases/download/$SARD_TAG/docker-compose.yml"
curl -fsSL -o .env "https://github.com/Artur-Abalov/Sard/releases/download/$SARD_TAG/sard.env.example"
chmod 600 .env
```

В скачанном `.env` уже стоит `SARD_VERSION` этого релиза.

## 2. Секреты

```bash
sed -i -e "s/^SARD_DB_PASSWORD=.*/SARD_DB_PASSWORD=$(openssl rand -hex 24)/" \
       -e "s/^SARD_ADMIN_PASSWORD=.*/SARD_ADMIN_PASSWORD=$(openssl rand -hex 16)/" .env
grep '^SARD_ADMIN_PASSWORD=' .env       # пароль администратора: сохраните в менеджер паролей
```

Секреты живут только в `.env` (права `600`). В образе секретов нет.

## 3. Имя сервера

Имя, по которому агенты будут подключаться (DNS-имя или IP,
[раздел 4](04-tls-and-names.md)):

```bash
SARD_NAME=<sard.example.com>
printf 'SARD_PKI_SERVER_NAMES=%s\nSARD_AGENT_ENDPOINT=%s:9090\n' "$SARD_NAME" "$SARD_NAME" >> .env
```

Без этого шага сертификат сервера выпущен только на `localhost`, и агенты с
других хостов не подключатся.

## 4. Запуск

```bash
docker compose up -d --wait
curl -fsS http://localhost:8080/api/v1/status
```

`--wait` возвращается, когда PostgreSQL и сервер прошли healthcheck (замер на
amd64: 13 с после скачивания образов). Последняя команда печатает версию
сервера, например `{"version":"v0.0.1-rc.1","lastVerifiedRestoreAt":null}`.

## 5. Проверка

```bash
docker compose ps                                   # оба контейнера healthy
curl -fsS http://localhost:8080/actuator/health     # {"groups":[...],"status":"UP"}
echo | openssl s_client -connect localhost:9090 -alpn h2 2>/dev/null | grep -E '^(subject|issuer)='
```

Последняя команда показывает сертификат порта агентов: `issuer=CN = Sard CA`.

## Что дальше

- консоль — [раздел 4](04-tls-and-names.md), «Консоль»;
- агенты — [раздел 5](05-agents.md);
- первый бэкап самого сервера (ключ CA) — [раздел 6](06-data-and-backup.md),
  сразу после первого старта.

## Политика перезапуска

Оба контейнера — `restart: unless-stopped`: поднимаются после перезагрузки
машины, если не были остановлены вручную. Docker должен стартовать при
загрузке (`sudo systemctl enable docker`).
