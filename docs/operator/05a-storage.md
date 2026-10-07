<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 5a. Хранилища репозиториев: S3 и SFTP

Репозиторий restic агента может лежать на диске хоста, в S3-совместимом
хранилище или на SFTP-сервере. Данные бэкапа идут от агента прямо в
хранилище, через сервер Sard они не проходят. Адрес, пароль и ключи доступа
лежат только на хосте агента, в `/etc/sard/agent.yaml` и файлах рядом с ним.
Сервер знает только имя репозитория (ADR 0008).

Этот раздел описывает, как подключить хранилище сейчас: руками, в конфиге
агента. Команды консоли для этого появятся позже. Общий порядок подключения
агента — в [разделе 5](05-agents.md); команды `repo init` и `repo list`, их коды
выхода и права файлов — на странице
[«Репозиторий агента»](../operations/repo-init.md).

Всё ниже проверено сквозными тестами на Garage (S3) и OpenSSH (SFTP): классы
`StorageChainTest`, `StorageFailureTest`, `StorageOutageTest` и
`StorageLockTest` в [`test/e2e`](../../test/e2e/README.md), ADR 0047.

## S3

### Конфиг

```yaml
repositories:
  - name: main
    url: s3:https://<s3.example.com>/<бакет>/<путь>
    password_file: /etc/sard/secrets/restic-main.pass
    env_file: /etc/sard/secrets/restic-main.env
```

Формат `url` — формат restic: `s3:<схема>://<хост>[:<порт>]/<бакет>/<путь>`.
`<путь>` — каталог репозитория внутри бакета. В одном бакете можно держать
несколько репозиториев с разными путями.

### Ключи доступа: `env_file`

Ключи передаются restic только через `env_file` репозитория. Это строки
`ИМЯ=значение` без кавычек и `export`:

```bash
sudo install -o sard-agent -g sard-agent -m 0600 /dev/null /etc/sard/secrets/restic-main.env
sudo -u sard-agent tee /etc/sard/secrets/restic-main.env >/dev/null <<'EOF'
AWS_ACCESS_KEY_ID=<идентификатор ключа>
AWS_SECRET_ACCESS_KEY=<секрет>
AWS_DEFAULT_REGION=<регион хранилища>
EOF
```

- Владелец файла — `sard-agent`, права `0600`. Иначе агент файл не примет, и
  шаг и `repo init` откажут (проверка A1).
- Переменные `RESTIC_*`, `PATH`, `HOME`, `TMPDIR`, `LD_*`, `XDG_*`,
  `SSH_AUTH_SOCK` и `GO*` в `env_file` запрещены: они меняют репозиторий,
  ключ или исполняемый код. Агент называет строку и переменную, значение не
  печатает.
- `AWS_DEFAULT_REGION` нужен, если хранилище отвечает только в своём регионе.
- Агент читает `env_file` заново на каждом шаге. После замены ключа
  перезапуск службы не нужен.

Ключу нужны права на чтение, запись и удаление объектов в бакете. Удаление
restic использует для своих блокировок (`locks/`) и для `forget --prune`. Ключ
только на чтение даёт отказ на первом же шаге, см. «Отказы» ниже.

### Создание репозитория

Бакет создаётся заранее средствами хранилища. Затем:

```bash
sudo -u sard-agent sard-agent repo init --generate-password main
sudo systemctl restart sard-agent
```

## SFTP

### Что нужно на хосте

restic подключается к SFTP-серверу через системный `ssh`. Пакет `sard-agent`
зависит от `openssh-client`, а для rpm — от `openssh-clients`.

- `apt-get install ./sard-agent_<версия>_<арх>.deb` поставит зависимость сам.
- `dpkg -i` её не ставит. Если `ssh` на хосте нет, сначала выполните
  `sudo apt-get install openssh-client`.
- Для установки из tar.gz клиент OpenSSH ставится отдельно.

### Где `ssh` ищет ключ и `known_hosts`

`ssh` берёт домашний каталог пользователя из системной базы (passwd). У
пользователя службы это `/var/lib/sard-agent`, поэтому ключ, `known_hosts` и
`config` лежат в **`/var/lib/sard-agent/.ssh/`**. Переменная `HOME`, которую
агент передаёт restic (`restic.cache_dir`), на `ssh` не влияет. Каталог
`/var/lib/sard-agent` служба может изменять (`StateDirectory`), и
`ProtectHome` его не касается.

### Ключ пользователя службы

```bash
sudo -u sard-agent install -d -m 0700 /var/lib/sard-agent/.ssh
sudo -u sard-agent ssh-keygen -t ed25519 -N '' -C "sard-agent@$(hostname)" -f /var/lib/sard-agent/.ssh/id_ed25519
sudo cat /var/lib/sard-agent/.ssh/id_ed25519.pub
```

Пароль на ключ не ставится: служба работает без терминала и агента ключей.
Открытую часть (`.pub`) администратор SFTP-сервера добавляет в
`authorized_keys` пользователя хранилища. Лучше выделить для бэкапов
отдельного пользователя только с SFTP (`ForceCommand internal-sftp`) и своим
каталогом.

### Ключ хоста: `known_hosts`

Неизвестный ключ хоста служба **не принимает**. У неё нет терминала, поэтому
`ssh` не спросит «Are you sure you want to continue connecting» и откажет:
`Host key verification failed`. Ключ хоста сервера оператор кладёт в
`known_hosts` сам и сверяет его отпечаток по каналу, которому доверяет:

```bash
# на SFTP-сервере (или у его администратора): отпечаток ключа хоста
ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub

# на хосте агента: получить ключ и сверить отпечаток с полученным выше
ssh-keyscan -t ed25519 <sftp.example.com> > /tmp/sftp.hostkey
ssh-keygen -lf /tmp/sftp.hostkey

# отпечатки совпали — записать
sudo -u sard-agent tee -a /var/lib/sard-agent/.ssh/known_hosts < /tmp/sftp.hostkey >/dev/null
```

`ssh-keyscan` без сверки отпечатка — это доверие первому ответу. Подменённый
сервер получил бы ваши бэкапы. Если ключ хоста сменился, `ssh` откажет до
исправления `known_hosts`: шаги будут FAILED, см. «Отказы».

### Обрыв связи без ответа: `~/.ssh/config`

`ssh`, которым restic подключается к серверу, по умолчанию не проверяет, жив
ли сервер. Если сервер пропал из сети молча (без закрытия соединения),
бэкап ждёт его возвращения сколько угодно (OQ-154). Ограничьте ожидание:

```bash
sudo -u sard-agent tee /var/lib/sard-agent/.ssh/config >/dev/null <<'EOF'
Host <sftp.example.com>
    ServerAliveInterval 15
    ServerAliveCountMax 4
    ConnectTimeout 30
EOF
```

С такими значениями `ssh` разрывает соединение через минуту молчания, и шаг
становится FAILED: `ssh command exited: exit status 255`. В том же файле
задаются нестандартный порт (`Port`) и другой ключ (`IdentityFile`).

### Конфиг и проверка

```yaml
repositories:
  - name: main
    url: sftp:<пользователь>@<sftp.example.com>:/<каталог>/main
    password_file: /etc/sard/secrets/restic-main.pass
```

`env_file` для SFTP не нужен. Порт, отличный от 22, задаётся в
`~/.ssh/config` или в адресе: `sftp://<пользователь>@<хост>:<порт>//<каталог>`.

Проверить вход от имени службы без restic:

```bash
sudo -u sard-agent sftp -b /dev/null <пользователь>@<sftp.example.com>
```

Код 0 означает, что ключ принят и ключ хоста известен. Затем:

```bash
sudo -u sard-agent sard-agent repo init --generate-password main
sudo systemctl restart sard-agent
```

`repo init` создаёт каталог репозитория. Каталог над ним должен быть доступен
пользователю хранилища на запись.

## Отказы: что видно в консоли

Шаг бэкапа показывает сообщение и журнал. Журнал — это строки restic, которые
агент передал серверу; секреты в нём замаскированы. В таблице — что тесты
видят сейчас, за сколько и что делать. Строки с пометкой «проба restic»
измерены запуском restic 0.19.1 напрямую (`docs/sessions/2026-10-07-f2-s3-sftp.md`),
остальные — сквозными тестами через агента.

| Что случилось | Шаг | Где причина | Что делать |
|---|---|---|---|
| S3: неверный секрет ключа | FAILED, < 1 с | сообщение: `Stat: Access Denied.` | исправить `env_file` |
| S3: у ключа нет прав на запись | FAILED, < 1 с | только журнал: `Forbidden: Operation is not allowed for this key`; сообщение — `restic cat: exit code 1` | дать ключу запись в бакет |
| S3: бакета нет | FAILED, < 1 с | сообщение: `repository does not exist` | создать бакет, `repo init` |
| S3: хранилище недоступно до шага (имя не разрешается, порт закрыт) | FAILED через **13–15 мин**: restic повторяет запросы (проба restic, не e2e) | сообщение: `connection refused` / `no such host` | вернуть хранилище; следующий запуск успешен |
| S3: хранилище пропало посреди бэкапа и вернулось | restic ждёт, бэкап **успешен** (проверено на 30 с) | — | — |
| S3: хранилище пропало посреди бэкапа и не вернулось | `running` **дольше 20 мин** (проба restic, OQ-154) | — | вернуть хранилище; предел — таймаут шага |
| SFTP: ключ не принят сервером | FAILED, < 1 с | сообщение: `unable to start the sftp session`; журнал: `Permission denied (publickey)` — не всегда (OQ-155) | `authorized_keys` на сервере |
| SFTP: ключ хоста неизвестен или сменился | FAILED, < 1 с | сообщение: `unable to start the sftp session`; журнал: `Host key verification failed` | сверить и записать `known_hosts` |
| SFTP: каталог только на чтение | FAILED, < 2 с | только журнал: `permission denied`; сообщение — `restic cat: exit code 1` | права на каталог репозитория |
| SFTP: каталога нет | FAILED, < 1 с | сообщение: `repository does not exist` | проверить путь, `repo init` |
| SFTP: сервер выключен (имя не разрешается; порт закрыт — проба restic) | FAILED, < 1 с | сообщение: `unable to start the sftp session`; журнал: `Could not resolve hostname` / `Connection refused` | вернуть сервер |
| SFTP: сервер остановлен посреди бэкапа | FAILED, когда кончатся данные источника | сообщение: `ssh command exited: exit status 255` | вернуть сервер; следующий запуск успешен |
| SFTP: сервер пропал из сети молча | с `ServerAliveInterval` — FAILED (`ssh command exited`); без него — `running`, пока сеть не вернётся (OQ-154) | — | настроить `~/.ssh/config` |

Предел для любого зависания — таймаут шага. Сервер его пока не задаёт, а
максимум агента — 24 часа (OQ-154). Сообщения без причины перечислены в
OQ-155: причину ищите в журнале шага.

## Блокировки restic в удалённом репозитории

restic берёт в репозитории блокировку (`locks/`). Процесс, убитый посреди
работы (сбой агента, `kill -9`, перезагрузка хоста), оставляет её в
хранилище.

- **Блокировка прерванного бэкапа** — общая. Следующий бэкап через неё
  проходит, шаг успешен. Но `restic check` и `restic prune` берут
  исключительную блокировку и откажут, пока старая лежит:
  `repository is already locked by PID … on <хост>`.
- **Исключительная блокировка прерванной команды оператора** (`check`,
  `prune`) останавливает бэкапы. Шаг сразу FAILED:
  `restic cat: repository is locked by another process`. Ожидание до 5 минут
  (`--retry-lock 5m`, ADR 0029) сейчас не срабатывает: проверка `restic cat`
  перед бэкапом ждать не умеет (OQ-156).

Снять оставшиеся блокировки — от имени службы, когда ни один restic с этим
репозиторием точно не работает (ни на этом хосте, ни на других):

```bash
sudo -u sard-agent sh -c 'set -a; . /etc/sard/secrets/restic-main.env; exec /usr/lib/sard/restic \
  --repo <url> --password-file /etc/sard/secrets/restic-main.pass unlock --remove-all'
```

Ключи берутся из `env_file` и не попадают в список процессов. Для SFTP строка
`set -a; . …;` не нужна. Обычный `unlock` без `--remove-all` удаляет
только устаревшие блокировки: старше 30 минут или оставленные процессом этого
же хоста, которого уже нет.
