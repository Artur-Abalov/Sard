<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# Установка и обновление агента

Агент ставится из пакетов, которые раздаёт сам сервер Sard (U1a), командами,
которые показывает консоль (U1b). Хосту нужен доступ только к серверу Sard: ни
GitHub, ни реестров пакетов. Спецификации — `docs/specs/server/agent-install.feature`,
`docs/specs/web/agent-install.feature`, `docs/specs/agent/agent-install.feature`;
решения — `docs/adr/0037-agent-install-from-server.md`.

## Из консоли

Блок «Установка агента» есть на странице «Агенты» (раскрыт, пока агентов нет,
иначе свёрнут) и в окне создания токена (там в шаге регистрации стоит команда с
настоящим токеном). Выберите архитектуру, формат и утилиту скачивания (curl или
wget), выполните шаги по порядку — у каждого своя кнопка копирования. Шаг
проверки подписи необязателен. Агент, чья версия ниже раздаваемой, помечен в
списке «Доступно обновление»; в его карточке — команды обновления.

Команды строит сервер. Адрес, с которого хост скачивает пакеты, — настройка
`SARD_AGENT_DOWNLOADS_URL`:

- пусто (по умолчанию) — `http://<хост из SARD_AGENT_ENDPOINT или первое имя из
  SARD_PKI_SERVER_NAMES>:<SARD_HTTP_PORT>`; пакеты — по обычному HTTP, поэтому за
  пределами доверенной сети лучше следующий вариант;
- абсолютный адрес `http` или `https` без запроса и фрагмента, например
  `https://sard.example.com` — адрес обратного прокси с TLS, за которым стоит
  сервер. С любым другим значением сервер не стартует. Адрес из заголовков запроса
  не используется никогда.

Адрес в команде регистрации — тот же `SARD_AGENT_ENDPOINT`, что и в
`docs/operations/agent-enroll.md`.

## Вручную (раздача выключена или консоль недоступна)

`SARD_AGENT_DOWNLOADS=false` выключает раздачу: консоль тогда объясняет это и
ссылается на этот раздел. Возьмите пакеты релиза из GitHub Releases
(`sard-agent_<версия>_linux_<архитектура>.tar.gz`, `.deb`, `.rpm`, `SHA256SUMS`,
`SHA256SUMS.minisig`) или соберите их `make package`, и выполните на хосте.

### 1. Проверка

```bash
grep '  <файл пакета>$' SHA256SUMS | sha256sum -c -      # OK или код выхода не 0
minisign -Vm SHA256SUMS -P <строка ключа>                # необязательно, см. ниже
```

Ключ релизов — `deploy/release/sard-release.pub`; его ID и строку берите из
`README.md` («Verifying releases») репозитория, а не с того сервера, с которого
скачали пакет: ключ с сервера не доказывает ничего о самом сервере. После
проверки подписи `minisign` печатает доверенный комментарий `sard-agent <версия>`.

### 2. Установка

deb (Debian, Ubuntu, Astra):

```bash
sudo dpkg -i sard-agent_<версия>_<архитектура>.deb
```

Пакет создаёт пользователя `sard-agent`, каталоги `/etc/sard` (`root:sard-agent`,
`0750`), `/etc/sard/tls` и `/etc/sard/secrets` (`sard-agent`, `0700`) и кэш restic;
служба не включена и не запущена. При первой установке пакет печатает следующий
шаг; обновление ничего о регистрации не пишет.

tar.gz (другие Linux с systemd) — та же раскладка:

```bash
tar -xzf sard-agent_<версия>_linux_<архитектура>.tar.gz
cd sard-agent_<версия>_linux_<архитектура>
sudo useradd --system --no-create-home --home-dir /var/lib/sard-agent \
  --shell /usr/sbin/nologin --user-group sard-agent
sudo install -d -m 0755 /usr/lib/sard
sudo install -m 0755 sard-agent restic /usr/lib/sard/
sudo ln -sf /usr/lib/sard/sard-agent /usr/bin/sard-agent
sudo install -m 0644 sard-agent.service /usr/lib/systemd/system/sard-agent.service
sudo install -d -o root -g sard-agent -m 0750 /etc/sard
sudo install -d -o sard-agent -g sard-agent -m 0700 /etc/sard/tls /etc/sard/secrets /var/cache/sard/restic
sudo install -m 0644 agent.example.yaml /etc/sard/agent.example.yaml
sudo systemctl daemon-reload
```

### 3. Настройка, регистрация, запуск

```bash
sudo cp -n /etc/sard/agent.example.yaml /etc/sard/agent.yaml    # не перезаписывает ваш файл
sudoedit /etc/sard/agent.yaml     # server.address и ваш репозиторий (адрес, файлы пароля и ключей)
sudo -u sard-agent sard-agent enroll --server <адрес сервера> --token <токен из консоли>
sudo -u sard-agent sard-agent repo init --generate-password <имя репозитория>
sudo systemctl enable --now sard-agent.service
```

Регистрацию и создание репозитория выполняйте от имени `sard-agent`: файлы
идентичности и пароль должен читать пользователь службы
(`docs/operations/agent-enroll.md`, `docs/operations/repo-init.md`). Репозитории,
пароли и ключи остаются на хосте; сервер знает только их имена (ADR 0008).

## Обновление

deb: `sudo dpkg -i <новый .deb>` — ставит поверх, не трогает `/etc/sard` и ключи,
перезапускает службу, если она работает. tar.gz: распаковать, заменить
`/usr/lib/sard/sard-agent` и `/usr/lib/sard/restic`
(`sudo install -m 0755 sard-agent restic /usr/lib/sard/`) и
`sudo systemctl try-restart sard-agent.service`. Обновляйте, когда у агента нет
активных запусков: перезапуск их прерывает.
