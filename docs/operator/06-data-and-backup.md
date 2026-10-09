# 6. Данные и бэкап сервера

Самобэкапа Sard пока нет (запланирован): до него сервер бэкапят вручную, по
этой процедуре.

## Что где лежит

| Том | Что | Критичность |
|---|---|---|
| `sard_sard-pki` | ключ и сертификат CA (`ca/ca.key`, `ca/ca.crt`) | **потеря = повторная регистрация всех агентов**; утечка = возможность выпустить сертификат любого агента |
| `sard_postgres-data` | база: агенты, источники, запуски, снимки, журналы шагов, токены | потеря = потеря истории и настроек; бэкапы restic на хранилищах целы |
| `~/sard/.env` | секреты и настройки | без него — новый пароль администратора и базы |

Ключей restic и паролей репозиториев на сервере нет: они на хостах агентов
(ADR 0008). Бэкап сервера их не заменяет — копия пароля каждого репозитория
хранится отдельно ([раздел 5](05-agents.md), шаг 5).

Ключ CA и база бэкапятся **раздельно**: базу можно отдать в более широкий
доступ, ключ CA — нет ([ключ CA](../operations/pki.md)).

## Бэкап

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

## Проверка бэкапа

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

## Восстановление

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
