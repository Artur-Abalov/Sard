# 6. Данные и бэкап сервера

Sard бэкапит сам себя (самобэкап): агент-сосед `sard-self` по расписанию кладёт
базу, ключ CA и конфигурацию установки в репозиторий restic, который вы ему
дали. Ручная процедура ниже остаётся в силе: для установки без соседа, перед
обновлением и для отдельной копии ключа CA.

## Самобэкап

### Что входит

| Источник | Что в снимке | Путь в снимке |
|---|---|---|
| «Sard: database» (плагин `postgresql`) | база `sard`: `pg_dump --format=custom` ролью `sard_self` (только чтение) | `/sard.dump` |
| «Sard: keys and configuration» (плагин `files`) | каталог CA: ключ и сертификат | `/var/lib/sard/pki/ca/ca.key`, `/var/lib/sard/pki/ca/ca.crt` |
| | каталог установки: `.env`, `docker-compose.yml`, `docker-compose.override.yml`; без `*.dump`, `*.tgz`, `*.tar*`, `*.zst` (ручные бэкапы и офлайн-архивы) | `/etc/sard/install/…` |

Глобальных объектов (`pg_dumpall`) в снимке нет. Роль `sard` создаёт
инициализация образа postgres из `.env`, роль `sard_self` — миграция сервера.

Каждый источник бэкапится своим запуском. Снимок находят по тегам:

| Тег | Значение |
|---|---|
| `sard.source` | id системного источника (в консоли — «Источники», метка «системный») |
| `sard.run`, `sard.step` | запуск и шаг, которые сделали снимок |
| `postgresql.part=database`, `postgresql.database=sard`, `postgresql.server_version` | только у снимка базы |

```bash
docker compose exec -u 10001 self-agent /usr/libexec/sard/restic snapshots \
  --repo /var/lib/sard-self/<имя> --password-file /etc/sard/self/secrets/restic-<имя>.pass --no-cache
```

### Как включить

1. `.env` должен читаться группой 10001 (сосед работает под uid сервера):
   `sudo chgrp 10001 .env && chmod 640 .env`. Больше его никто не прочитает.
2. Добавьте соседу репозиторий. Хранилище по умолчанию — S3 или SFTP (D10).
   Локальный путь — только сознательно: копия погибнет вместе с машиной.

   ```bash
   docker compose run --rm --no-deps -u 0 \
     --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER --cap-add SETUID --cap-add SETGID \
     self-agent repo add self /var/lib/sard-self/self --no-restart --config /etc/sard/self/agent.yaml
   docker compose restart self-agent
   ```

   Разовый контейнер с теми же томами нужен потому, что у работающего соседа
   нет capabilities: даже root в нём не войдёт в его каталоги `0700`.
   `repo add` нужен root (ADR 0050). Пять capabilities нужны, чтобы создать
   каталоги владельцу 10001 и запустить restic от его имени. Сосед после
   перезапуска снова работает без прав. Для S3 и SFTP — `repo add` с адресом
   хранилища (A8b).
   Пароль репозитория создаётся на соседе (`/etc/sard/self/secrets/restic-self.pass`,
   том `sard_sard-self-config`). **Сохраните его копию вне этой машины.** Без
   пароля снимки не прочитать, а сервер его не знает (ADR 0008).
3. Привяжите репозиторий: мастер онбординга (F4b: он же собирает команду шага 2
   и ждёт репозиторий) или
   `PUT /api/v1/self-backup/repository {"repositoryName":"self"}`. Для локального
   пути добавьте `"confirmLocalStorage": true`. Сервер создаст два источника и
   расписание `0 3 * * *` в своём часовом поясе
   (`SARD_SELF_BACKUP_CRON`, `SARD_SELF_BACKUP_TIMEZONE`). Расписание каждого
   источника меняется как у любого другого.
4. Проверьте: `POST /api/v1/self-backup/runs` запускает оба источника сразу.

### Когда он плох

`GET /api/v1/self-backup` показывает состояние, консоль — баннер на каждой
странице. Работа при этом не блокируется.

| Предупреждение | Что значит |
|---|---|
| `agent_replaced` | встроенный агент отозван и зарегистрирован заново — привяжите репозиторий ещё раз |
| `overdue` | нет успеха дольше 1,5 интервала расписания |
| `failing` | последний запуск источника упал |
| `schedule_disabled` | расписание источника выключено |
| `local_storage` | репозиторий на диске самого сервера |
| `keys_unconfirmed` | после онбординга хранилище сменили, а копию пароля и доступа не подтвердили заново (настройки → «Самобэкап») |

Уведомления (Telegram) приходят о первой ошибке, о восстановлении и о
просрочке. Порог «давно не было успеха» общий для всех источников с
расписанием: полтора интервала cron от последнего успеха.

### Ключ CA и база в одном репозитории

Самобэкап кладёт ключ CA рядом с базой. Раньше правило было «храните их
раздельно». Теперь репозиторий зашифрован, а пароль от него только у соседа и
у вас вне ВМ. Тот, кто получил хранилище без пароля, не получил ни ключа, ни
`.env`. Тот, у кого есть и пароль, и хранилище, держит корень доверия
установки. Если нужно разделение, храните ещё одну копию CA отдельно, по
ручной процедуре ниже.

### Восстановление из самобэкапа (F7)

Что нужно в руках: пароль репозитория (команда `repo password <имя> --reveal`
на соседе), доступ к хранилищу и карточка восстановления
(`GET /api/v1/self-backup/recovery-card`, в консоли — страница «Карточка
восстановления», её можно распечатать или скачать как текст): имя и тип
репозитория, адрес хранилища, id репозитория, отпечаток CA, версия сервера.
Пароля и ключей в карточке нет.

Процедура и учения — F7. Уже известно одно: база и ключи бэкапятся разными
запусками. Агенты, зарегистрированные между последним бэкапом базы и аварией,
после восстановления получат `CERT_UNKNOWN`: их сертификатов нет в базе. Такие
агенты регистрируются заново.

## Ручной бэкап

Без соседа, перед обновлением ([раздел 7](07-upgrade.md)) и для отдельной копии ключа CA.

### Что где лежит

| Том | Что | Критичность |
|---|---|---|
| `sard_sard-pki` | ключ и сертификат CA (`ca/ca.key`, `ca/ca.crt`) | **потеря = повторная регистрация всех агентов**; утечка = возможность выпустить сертификат любого агента |
| `sard_postgres-data` | база: агенты, источники, запуски, снимки, журналы шагов, токены | потеря = потеря истории и настроек; бэкапы restic на хранилищах целы |
| `~/sard/.env` | секреты и настройки | без него — новый пароль базы (пароль администратора в базе, не в `.env`) |

Ключей restic и паролей репозиториев на сервере нет: они на хостах агентов
(ADR 0008). Бэкап сервера их не заменяет — копия пароля каждого репозитория
хранится отдельно ([раздел 5](05-agents.md), шаг 5).

В базе, кроме данных, хэш пароля администратора (Argon2id) и запись о том, откуда
взялся CA и в деле ли он. Поэтому том базы и каталог CA — **пара**: восстанавливайте
их из бэкапа одного момента или переустанавливайте вместе. Сервер не стартует,
если база говорит, что CA в деле, а каталог пуст (`CA_MISSING`), или каталог
хранит CA, которого база не знает (`CA_ORIGIN_NOT_RECORDED`):
[раздел 9](09-troubleshooting.md). Ключ CA стоит сохранить на шаге CA мастера
первого запуска ([раздел 2](02-install.md)).

Ключ CA и база бэкапятся **раздельно**: базу можно отдать в более широкий
доступ, ключ CA — нет ([ключ CA](../operations/pki.md)).

### Бэкап

Из `~/sard`, сервер можно не останавливать:

```bash
cd ~/sard
IMAGE=$(docker compose config --images | grep sard-server)
STAMP=$(date +%F)
# база
docker compose exec -T postgres pg_dump -U sard -d sard -Fc > "sard-db-$STAMP.dump"
# ключ CA (файлы пишутся один раз, при первом старте)
docker run --rm --user 0 --entrypoint tar -v sard_sard-pki:/pki:ro -v "$PWD":/backup \
  "$IMAGE" -C /pki -czf "/backup/sard-pki-$STAMP.tgz" ca
chmod 600 "sard-db-$STAMP.dump" "sard-pki-$STAMP.tgz"
```

Унесите оба файла и копию `.env` с машины: архив CA — в хранилище секретов
(сейф, офлайн-носитель), дамп базы — в обычное хранилище бэкапов.

Ключ CA достаточно сохранить один раз, после первого старта; базу — по
расписанию (cron) и обязательно перед обновлением ([раздел 7](07-upgrade.md)).

### Проверка бэкапа

Дамп базы разворачивается во временный PostgreSQL и сверяется с живой базой:

```bash
docker run -d --name sard-verify -e POSTGRES_PASSWORD=verify postgres:18-alpine
until docker exec sard-verify pg_isready -U postgres -q; do sleep 1; done
docker exec -i sard-verify pg_restore -U postgres -d postgres --no-owner < "sard-db-$STAMP.dump"
docker exec sard-verify psql -U postgres -tAc 'select count(*) from agents'
docker compose exec -T postgres psql -U sard -d sard -tAc 'select count(*) from agents'
docker rm -f sard-verify
```

Два последних числа совпадают. Архив CA — по отпечатку:

```bash
fp() { openssl x509 -in "$1" -pubkey -noout | openssl pkey -pubin -outform DER | sha256sum | cut -c1-64; }
tar -xzOf "sard-pki-$STAMP.tgz" ca/ca.crt > /tmp/backup-ca.crt
fp /tmp/backup-ca.crt
docker run --rm --user 0 --entrypoint cat -v sard_sard-pki:/pki:ro "$IMAGE" /pki/ca/ca.crt > /tmp/live-ca.crt
fp /tmp/live-ca.crt
```

Отпечатки совпадают. Это тот же отпечаток, что в конце строки токена
регистрации, в логе сервера при старте и на странице «Токены» консоли
(встроенный отпечаток openssl считает хэш всего сертификата — другое число, с
отпечатком Sard он не сверяется).

### Восстановление

На работающей установке (тот же `~/sard`, тома существуют). Файлы бэкапа —
в `~/sard`:

```bash
cd ~/sard
IMAGE=$(docker compose config --images | grep sard-server)
DUMP=<sard-db-ГГГГ-ММ-ДД.dump>
PKI=<sard-pki-ГГГГ-ММ-ДД.tgz>
docker compose stop server
# база
docker compose exec -T postgres psql -U sard -d postgres \
  -c 'DROP DATABASE sard WITH (FORCE)' -c 'CREATE DATABASE sard OWNER sard'
docker compose exec -T postgres pg_restore -U sard -d sard --no-owner < "$DUMP"
# ключ CA
docker run --rm --user 0 --entrypoint sh -v sard_sard-pki:/pki -v "$PWD":/backup "$IMAGE" -c \
  "rm -rf /pki/ca && tar -C /pki -xzf /backup/$PKI && chown -R 10001:10001 /pki/ca && chmod 700 /pki/ca && chmod 600 /pki/ca/*"
docker compose up -d --wait
```

Проверка: `curl -fsS http://localhost:8080/api/v1/status` отвечает; в консоли
агенты те же и через минуту «в сети» — CA тот же, повторная регистрация не
нужна. Сессии консоли сбрасываются (они в памяти).

Перенос на другую машину — не распаковкой в том, а импортом CA с проверками:
[раздел 8](08-migrate-and-remove.md).

Права на каталог CA важны: с правами шире `0700`/`0600` сервер не стартует.
