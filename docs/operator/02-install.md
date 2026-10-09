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
sed -i -e "s/^SARD_DB_PASSWORD=.*/SARD_DB_PASSWORD=$(openssl rand -hex 24)/" .env
```

Секреты живут только в `.env` (права `600`). В образе секретов нет. Пароля
администратора в `.env` нет: его задают при первом запуске в консоли
([шаг 5](#5-первый-запуск-код-настройки-и-мастер)).

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

## 5. Первый запуск: код настройки и мастер

Пока пароля администратора нет, консоль открывается мастером `/setup`, а сервер
при каждом старте печатает в свой лог одноразовый **код настройки** (28 знаков,
группы по 4 через дефис, действует 24 часа). Код — доказательство того, что у вас
есть доступ к хосту сервера:

```bash
docker compose logs server | grep "SARD SETUP CODE"
# SARD SETUP CODE: XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX valid until 2026-10-10T12:00:00Z
```

1. Откройте консоль, `http://localhost:8080` (с другой машины — через
   SSH-туннель, [раздел 4](04-tls-and-names.md)): она сама ведёт на `/setup`.
2. Введите код (регистр, пробелы и дефисы не важны). Неверный код пять раз за
   15 минут блокирует ввод на 15 минут с этого адреса.
3. **CA сервера.** Мастер показывает отпечаток CA, его происхождение (создан
   сервером или импортирован) и путь ключа в томе. Сохраните копию ключа CA
   сразу ([раздел 6](06-data-and-backup.md)): без неё при потере тома агентов
   придётся регистрировать заново. Свой CA (переезд на новую машину) можно
   подключить импортом, пока вы не нажали «Использовать этот CA»
   ([раздел 8](08-migrate-and-remove.md)); после — нельзя.
4. **Пароль администратора** — от 12 до 1024 символов, дважды. Мастер сразу
   открывает консоль; сервер хранит только хэш (Argon2id).

Код истёк (или консоль пишет, что кода нет) — `docker compose restart server`
печатает новый; прежние сессии настройки с рестартом заканчиваются. Встроенный
агент `sard-self` регистрируется только после шага CA. Потерянный пароль
восстанавливают командой на хосте сервера:
[раздел 10](10-security.md), «Восстановление доступа».

## 6. Проверка

```bash
docker compose ps                                   # оба контейнера healthy
curl -fsS http://localhost:8080/actuator/health     # {"groups":[...],"status":"UP"}
echo | openssl s_client -connect localhost:9090 -alpn h2 2>/dev/null | grep -E '^(subject|issuer)='
```

Последняя команда показывает сертификат порта агентов: `issuer=CN = Sard CA`.

## Что дальше

- смена пароля — страница «Настройки» консоли;
- консоль — [раздел 4](04-tls-and-names.md), «Консоль»;
- агенты — [раздел 5](05-agents.md);
- первый бэкап самого сервера (ключ CA) — [раздел 6](06-data-and-backup.md),
  сразу после первого старта.

## Политика перезапуска

Оба контейнера — `restart: unless-stopped`: поднимаются после перезагрузки
машины, если не были остановлены вручную. Docker должен стартовать при
загрузке (`sudo systemctl enable docker`).
