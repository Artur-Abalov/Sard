<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 12. Восстановление сервера

Сервер сгорел вместе с ВМ. У вас есть доступ к хранилищу самобэкапа, пароль
репозитория и карточка восстановления. По этой странице вы поднимаете Sard на
новой машине: агенты подключаются сами, без перерегистрации, история на месте,
самобэкап продолжается. Каждую команду страницы выполняют учения в CI
(`ServerRecoveryDrillTest` в [`test/e2e`](../../test/e2e/README.md)) на чистой
машине без доступа в интернет: блоки, помеченные в исходнике страницы строкой
`<!-- drill: … -->`, проходят как написаны.

Как читать страницу:

- шаги идут по порядку, у каждого из шагов 1–6 есть блок команд, строка
  «Проверка» (что должно получиться) и строка «Если не вышло»;
- блок рассчитан на запуск отдельным процессом `bash` (вставьте его в `bash` или
  сохраните в файл и запустите `bash файл`); состояние между блоками лежит в
  каталоге `~/sard-recovery` (шаг 0);
- значения в угловых скобках — из карточки восстановления или ваши (таблица
  ниже); в блоках нет ни паролей, ни ключей: секреты вводятся без эха
  (`read -rs`) и дальше идут только файлами с правами `600`;
- если ВМ без доступа в интернет, образы берутся из офлайн-архива
  ([раздел 11](11-offline.md)), а каждый `docker compose up` в блоках идёт с
  `--pull never`.

| Подстановка | Откуда |
|---|---|
| `<repositoryName>` | карточка: имя репозитория |
| `<location>` | карточка: адрес хранилища, как его собрал мастер |
| `<repositoryId>` | карточка: id репозитория (64 hex) |
| `<caFingerprint>` | карточка: отпечаток CA (64 hex) |
| `<sardVersion>` | карточка: версия сервера, на которой сделана карточка |
| `<адрес-restic>` | `<location>` в виде, который понимает restic (таблица в шаге 0) |
| `<образ-агента>` | образ `sard-agent:<sardVersion>-pg18` (шаг 0) |
| `<имя-сервера>` | DNS-имя, по которому агенты находят сервер |

## Что нужно

Три вещи. Без любой из них восстановление невозможно, и сервер Sard не хранит их
за вас (ADR 0008):

1. **Пароль репозитория.** Его знает только сосед `sard-self`; вы сохранили его,
   выполнив `repo password <имя> --reveal`
   ([раздел 6](06-data-and-backup.md)). Без пароля снимки не прочитать.
2. **Доступ к хранилищу:** для S3 — идентификатор ключа, секретный ключ и
   регион; для SFTP — ваш ключ SSH и отпечаток ключа сервера. Локальное
   хранилище на диске старой ВМ погибло вместе с ней: восстановиться из него
   нельзя, и страница ничем не поможет.
3. **Карточка восстановления.** Консоль → «Самобэкап» → «Карточка
   восстановления» (печать или `.txt`) или `GET /api/v1/self-backup/recovery-card`
   до аварии. В ней имя и тип репозитория, адрес хранилища, `repositoryId`,
   `caFingerprint`, `sardVersion`. Пароля и ключей в карточке нет.

**Пароль администратора** — не четвёртая вещь и не новый механизм. Его хэш лежит
в базе, а база возвращается из снимка вместе со всем остальным: после
восстановления вход — тот же пароль, что был на момент снимка базы. Храните
пароль администратора вместе с карточкой: он понадобится на шаге 5. Забыли —
запасной путь `admin-reset` ([раздел 10](10-security.md#восстановление-доступа));
завершённый онбординг он не сбрасывает, данные не трогает.

Новая ВМ: Docker с плагином compose
([раздел 1](01-requirements.md)), пользователь в группе `docker`, доступ по
сети к хранилищу, свободные порты 8080 (только `127.0.0.1`) и 9090. Имя сервера
для агентов пока указывает на старую ВМ или никуда: переводить его рано.

## Сколько это займёт

Время измеряют учения: от начала первого блока на новой ВМ до health сервера,
до состояния «в сети» всех агентов после перевода имени и до успешных запусков
самобэкапа. База учений мала, поэтому цифры показывают порядок, а не гарантию:
на вашей базе минуты растут вместе с размером дампа и скоростью хранилища.
Длительность учения CI не утверждает (общий раннер); строку в таблицу переносит
человек из `test/e2e/build/recovery-drill/result.md`.

Строка снята учениями 2026-10-14 на машине разработчика (не на CI) с локальной
сборкой `0.0.0-e2e`; «до сервера» — от начала загрузки образов до health,
«до агентов» — до состояния «в сети» двух старых агентов после перевода имени,
«до самобэкапа» — до успеха обоих запусков самобэкапа в шаге 5.

| Дата | Версия | Размер дампа | Размер базы | Минут до сервера | Минут до агентов | Минут до самобэкапа |
|---|---|---|---|---|---|---|
| 2026-10-14 | 0.0.0-e2e | 0.1 MiB | 9.5 MiB | 0.9 | 2.2 | 1.5 |

## Шаг 0. Подготовка новой ВМ

Откуда берётся адрес restic:

| Хранилище (из карточки) | `<location>` | `<адрес-restic>` |
|---|---|---|
| S3, свой (`custom`) | `s3:https://s3.example.com/<бакет>[/<путь>]` | то же |
| AWS S3 | `s3:<бакет>[/<путь>]` | `s3:https://s3.<регион>.amazonaws.com/<бакет>[/<путь>]` |
| Backblaze B2 | `s3:<бакет>[/<путь>]` | `s3:https://s3.<регион>.backblazeb2.com/<бакет>[/<путь>]` |
| SFTP | `sftp:<пользователь>@<хост>:<путь>` | то же |

Регион карточка не хранит: он у вас. Образ агента нужен той же версии, что в
карточке: в нём лежит restic (`/usr/libexec/sard/restic`) и `repo add`. С доступом
в интернет:

```bash
docker pull ghcr.io/artur-abalov/sard-agent:<sardVersion>-pg18
docker pull ghcr.io/artur-abalov/sard-server:<sardVersion>
docker pull postgres:18.6-alpine@sha256:898c60cb390d1085bc97bdd6a1ae968757421df394b071848b52b1c09c89d6a5
# the tag an offline installation's .env names (раздел 11)
docker tag postgres:18.6-alpine@sha256:898c60cb390d1085bc97bdd6a1ae968757421df394b071848b52b1c09c89d6a5 postgres:18.6-alpine
```

Без доступа в интернет архив с образами (`sard-<версия>-images-linux-<арх>.tar.gz`,
[раздел 11](11-offline.md)) лежит в `~/sard-offline`:

<!-- drill: images -->
```bash
set -euo pipefail
gunzip -c ~/sard-offline/sard-*-images-linux-*.tar.gz | docker load
docker images --format '{{.Repository}}:{{.Tag}}' | grep -c 'sard-'
```

Каталог `~/sard-recovery` с правами `700` держит пароль, ключи хранилища и
рабочие файлы; секреты вводятся без эха и ложатся в файлы с правами `600`.
Образ агента (`<образ-агента>`) — это `sard-agent:<sardVersion>-pg18` с тем
реестром, из которого вы его взяли. Для SFTP цикл ввода ключей S3 не нужен:
уберите его и файлы `s3.*`.

<!-- drill: prepare -->
```bash
set -euo pipefail
umask 077
mkdir -p ~/sard-recovery
chmod 700 ~/sard-recovery
cd ~/sard-recovery
cat > state.env <<'EOF'
REPO='<адрес-restic>'
AGENT_IMAGE='<образ-агента>'
EOF
cat > restic <<'EOF'
#!/bin/bash
cd "$(dirname "$0")" && . ./state.env
env_file() {
  printf '%s=%s\n' AWS_ACCESS_KEY_ID "$(cat s3.id)" AWS_SECRET_ACCESS_KEY "$(cat s3.secret)" AWS_DEFAULT_REGION "$(cat s3.region)"
}
exec docker run --rm --user "$(id -u):$(id -g)" --env-file <(env_file) -v "$PWD:$PWD" -w "$PWD" \
  --entrypoint /usr/libexec/sard/restic "$AGENT_IMAGE" --no-cache -r "$REPO" --password-file restic.pass "$@"
EOF
read -rs -p 'restic repository password: ' P; echo
printf '%s' "$P" > restic.pass
for F in s3.id s3.secret s3.region; do
  read -rs -p "$F: " V; echo
  printf '%s' "$V" > "$F"
done
unset P V
chmod 700 restic
chmod 600 state.env restic.pass s3.id s3.secret s3.region
test "$(stat -c '%a' . restic restic.pass s3.id s3.secret s3.region | tr '\n' ' ')" = '700 700 600 600 600 600 '
ls -l
```

Файл `restic` — обёртка: она запускает restic из образа агента от вашего
пользователя (ключи хранилища идут через `--env-file`, не в командной строке).
Если на ВМ уже стоит свой restic, замените обёртку этим вариантом, остальные
шаги не меняются:

```bash
cat > ~/sard-recovery/restic <<'EOF'
#!/bin/bash
cd "$(dirname "$0")" && . ./state.env
while read -r N F; do export "$N=$(cat "$F")"; done <<'KEYS'
AWS_ACCESS_KEY_ID s3.id
AWS_SECRET_ACCESS_KEY s3.secret
AWS_DEFAULT_REGION s3.region
KEYS
exec restic -r "$REPO" --password-file restic.pass --no-cache "$@"
EOF
chmod 700 ~/sard-recovery/restic
```

SFTP: в обёртке из образа вместо `--env-file` смонтируйте свой каталог SSH
(`-v "$HOME/.ssh:/ssh:ro"`) и добавьте
`-o sftp.args='-i /ssh/id_ed25519 -o UserKnownHostsFile=/ssh/known_hosts'`;
в обёртке с отдельным restic достаточно своего `~/.ssh`. Учения проходят S3, SFTP
они не проверяют.

**Проверка.** Блок `images` печатает число загруженных образов Sard (не меньше
двух: сервер и агент `-pg18`). Последняя команда блока `prepare` печатает пять
файлов; права каталога и обёртки — `700`, остальных файлов — `600`. Команды не
оставляют секретов в истории оболочки: вводы идут через `read -rs`.

**Если не вышло.** `docker load` не находит файл — архив не перенесён или версия
другая: [раздел 11](11-offline.md). `read` пропустил ввод — повторите блок
целиком, он перезаписывает файлы.

## Шаг 1. Снимки: выбор и сверка с карточкой

В хранилище лежат снимки двух источников:

- **D** — снимок базы: последний снимок с тегом `postgresql.part=database`
  (снимок базы появляется только после успешного дампа);
- **K** — снимок ключей и конфигурации: последний снимок путей
  `--path /var/lib/sard/pki --path /etc/sard/install`, в котором есть `ca.key`,
  `ca.crt`, `.env` и `docker-compose.yml`. Упавший шаг мог оставить неполный
  снимок, поэтому блок проверяет полноту сам и берёт первый полный, считая от
  новых.

Блок сначала сверяет `restic cat config` с карточкой: `id` репозитория обязан
равняться `repositoryId`. Другой репозиторий с тем же паролем даёт другой `id`.

<!-- drill: snapshots -->
```bash
set -euo pipefail
cd ~/sard-recovery
. ./state.env
ID=$(./restic cat config | grep -oE '"id": *"[0-9a-f]+"' | cut -d'"' -f4)
echo "repository id: $ID"
if [ "$ID" != '<repositoryId>' ]; then echo 'WRONG REPOSITORY: stop here' >&2; exit 1; fi
list() { ./restic snapshots --json "$@" | grep -oE '"(time|id)":"[^"]*"' | cut -d'"' -f4 | paste - -; }
complete() {
  local L
  L=$(./restic ls "$1")
  for F in /var/lib/sard/pki/ca/ca.key /var/lib/sard/pki/ca/ca.crt /etc/sard/install/.env /etc/sard/install/docker-compose.yml; do
    if ! grep -qxF "$F" <<<"$L"; then echo "snapshot $1 lacks $F" >&2; return 1; fi
  done
}
read -r DT DID <<<"$(list --latest 1 --tag postgresql.part=database | tail -1)"
if [ -z "$DID" ]; then echo 'no snapshot of the database' >&2; exit 1; fi
K=
while read -r KT KID; do
  if complete "$KID"; then K=$KID; break; fi
done < <(list --path /var/lib/sard/pki --path /etc/sard/install | tac)
if [ -z "$K" ]; then echo 'no complete snapshot of the keys' >&2; exit 1; fi
printf 'D=%s\nK=%s\n' "$DID" "$K" >> state.env
echo "database snapshot: $DID ($DT)"
echo "keys snapshot:     $K ($KT)"
if [[ "$KT" < "$DT" ]]; then
  echo 'WARNING: the keys snapshot is older than the database one; if the server refuses with DATABASE_NEWER_THAN_SERVER, raise SARD_VERSION in ~/sard/.env'
fi
```

**Проверка.** Первая строка — `repository id:` с числом, равным `repositoryId`
карточки; затем два снимка с временем. Если ключи старше базы, блок
предупреждает: тогда на шаге 4 сервер может отказать с
`DATABASE_NEWER_THAN_SERVER` — `SARD_VERSION` в `.env` из снимка K меньше
версии, чьи миграции уже в базе.

**Если не вышло.** `wrong password` или `no key found` — неверный пароль в
`restic.pass`. Ошибка соединения — адрес хранилища, регион и ключи; ничего на ВМ
не создано, повторите шаг 0. `WRONG REPOSITORY` — адрес указывает на другой
репозиторий: остановитесь и найдите правильный адрес. `no complete snapshot of
the keys` — полных снимков ключей нет: карточка и старый `.env`
(`.env` с именами и настройками) нужны вручную, обратитесь к
[разделу 8](08-migrate-and-remove.md) и к бэкапу CA, сделанному вами отдельно.

## Шаг 2. Ключи и конфигурация

Снимок K восстанавливается в `~/sard-recovery/keys`, оттуда `.env`, файлы
compose и каталог `db/` (скрипты и настройки базы, DB2) идут в `~/sard`, а ключ и сертификат CA — прямо в том `sard_sard-pki`
с владельцем 10001 и правами `700` (каталоги) и `600` (файлы). `.env` читает
группа 10001 (сосед работает под uid сервера): группа 10001, права `640`.

<!-- drill: keys -->
```bash
set -euo pipefail
cd ~/sard-recovery
. ./state.env
./restic restore "$K" --target keys
mkdir -p ~/sard
cp keys/etc/sard/install/.env keys/etc/sard/install/docker-compose.yml ~/sard/
cp -R keys/etc/sard/install/db ~/sard/
if [ -f keys/etc/sard/install/docker-compose.override.yml ]; then
  cp keys/etc/sard/install/docker-compose.override.yml ~/sard/
fi
chmod 640 ~/sard/.env
docker run --rm --user 0 -v "$HOME/sard:/sard" --entrypoint chgrp "$AGENT_IMAGE" 10001 /sard/.env
cd ~/sard
docker compose create --pull never
docker run --rm --user 0 -v sard_sard-pki:/pki -v "$HOME/sard-recovery/keys/var/lib/sard/pki:/from:ro" \
  --entrypoint sh "$AGENT_IMAGE" -c 'mkdir -p /pki/ca && cp /from/ca/ca.key /from/ca/ca.crt /pki/ca/ && chown -R 10001:10001 /pki && chmod 700 /pki /pki/ca && chmod 600 /pki/ca/ca.key /pki/ca/ca.crt'
test "$(stat -c '%a %g' ~/sard/.env)" = '640 10001'
docker run --rm --user 0 -v sard_sard-pki:/pki:ro --entrypoint stat "$AGENT_IMAGE" -c '%u:%g %a %n' /pki/ca /pki/ca/ca.key /pki/ca/ca.crt
grep '^SARD_VERSION=' ~/sard/.env
```

**Проверка.** Три строки `10001:10001 700 /pki/ca`, `10001:10001 600 …/ca.key`,
`10001:10001 600 …/ca.crt`, затем `SARD_VERSION=` версии установки. `.env` в
`~/sard` — тот, что был на старой ВМ.

**Если не вышло.** Права на каталог CA шире `0700`/`0600` — сервер не
стартует: повторите блок. `docker compose create` ругается на переменную —
`.env` восстановлен не полностью (шаг 1 выбрал неполный снимок K). Образа нет
локально — выполните шаг 0.

## Шаг 3. База

Сначала поднимается только PostgreSQL: `docker compose up -d --wait postgres`
(офлайн — с `--pull never`, как в блоке). Пустую базу `sard` создаёт
инициализация образа. Дамп из снимка D разворачивается в неё
`pg_restore --no-owner --no-acl`: владельцем всего становится суперпользователь
`sard`, прав у ролей Sard нет. Ролей Sard и их паролей в дампе нет: на шаге 4
служба `db-init` создаёт роли и **новые пароли**, возвращает владельцем всего
`sard_owner` и выдаёт права, как у новой установки.

<!-- drill: database -->
```bash
set -euo pipefail
cd ~/sard-recovery
. ./state.env
./restic restore "$D" --target dump
cd ~/sard
docker compose up -d --wait --pull never postgres
docker compose exec -T postgres pg_restore -U sard -d sard --no-owner --no-acl < ~/sard-recovery/dump/sard.dump
docker compose exec -T postgres psql -U sard -d sard -Atc 'select count(*) from agents'
docker compose exec -T postgres psql -U sard -d sard -Atc 'select version from flyway_schema_history order by installed_rank desc limit 1'
```

**Проверка.** `pg_restore` ничего не печатает и завершается кодом 0; затем два
числа: сколько агентов в базе и версия последней миграции (она же покажет, какую
версию сервера нужно запускать).

**Если не вышло.** `already exists` — в базе `sard` уже есть таблицы (блок
запускали дважды или сервер успел применить миграции). Пересоздайте базу и
повторите шаг: `docker compose exec -T postgres psql -U sard -d postgres -c 'DROP
DATABASE sard WITH (FORCE)' -c 'CREATE DATABASE sard OWNER sard'`. Файла
`dump/sard.dump` нет — в снимке D не база: проверьте тег
`postgresql.part=database`.

## Шаг 4. Сервер

`docker compose up -d --wait --pull never server` сначала запускает `db-init`
(роли, владение, новые пароли) и `migrate` (миграции, которых нет в дампе),
потом сервер на восстановленной базе. Сервер делает две вещи, важные для
восстановления: проверяет, что база не новее него (иначе отказ
`DATABASE_NEWER_THAN_SERVER`, ничего не меняя), и сверяет каталог CA с базой.

<!-- drill: server -->
```bash
set -euo pipefail
cd ~/sard
docker compose up -d --wait --pull never server
docker compose logs server | grep -F 'CA fingerprint=<caFingerprint> origin=existing'
curl -fsS http://localhost:8080/api/v1/status | grep -F '"version":"<sardVersion>"'
curl -fsS http://localhost:8080/api/v1/onboarding | grep -o '"restriction":"none"'
docker compose exec -T postgres psql -U sard -d sard -Atc "select count(*) from pg_roles where rolname = 'sard_app'"
docker compose exec -T postgres psql -U sard -d sard -Atc "select distinct tableowner from pg_tables where schemaname = 'sard'"
```

**Проверка.** Лог содержит строку `CA fingerprint=<отпечаток карточки>
origin=existing` — отпечаток обязан совпасть с `caFingerprint` карточки;
версия в `/api/v1/status` равна `sardVersion`; `restriction` — `none` (онбординг
пройден, код настройки не печатается); предпоследняя команда печатает `1`: роль
`sard_app` создана `db-init`; последняя — `sard_owner`, единственного владельца
таблиц схемы `sard`. Не совпал отпечаток — это другой CA: остановите сервер
(`docker compose stop server`), имя не переводите и вернитесь к шагу 1, взяв
более старый снимок K.

**Если не вышло.** Причина — первая строка исключения в
`docker compose logs server`:

- `DATABASE_NEWER_THAN_SERVER` — в базе применена миграция новее, чем знает
  сервер версии из `.env`: снимок K старше снимка D. Поднимите `SARD_VERSION` в
  `~/sard/.env` до версии из сообщения (или `sardVersion` карточки) и повторите
  блок; ничего не изменено, повтор безопасен.
- `CA startup refused: … CA_ORIGIN_NOT_RECORDED` — в каталоге CA лежит CA,
  которого база не знает: ключи и база из разных установок. Вернитесь к шагу 1,
  возьмите пару из одного самобэкапа; если базу вы восстановили не в пустую
  `sard`, пересоздайте её (шаг 3).
- `CA startup refused: … CA_MISSING` — база говорит, что CA в деле, а каталог
  пуст: повторите шаг 2.
- `MIGRATIONS_NOT_APPLIED` или лог `db-init`/`migrate` с ошибкой — подготовка
  базы не прошла, сервер не запущен: первая строка лога службы
  (`docker compose logs db-init migrate`) называет причину
  ([раздел 16](16-postgresql.md)). Для базы, залитой другим способом, чем в
  шаге 3 (в `sard` уже лежали таблицы), пересоздайте её (шаг 3).

Прочие отказы старта — [раздел 9](09-troubleshooting.md).

## Шаг 5. Сосед и самобэкап

В восстановленной базе прежний встроенный агент `sard-self` (назовём его X) ещё
жив: сервер не выпускает токен второму, пока X не отозван. Сосед новой ВМ
идентичности не имеет (она лежала в томе старой ВМ) и ждёт файл токена. План:
войти, запустить соседа, отозвать X, дать соседу стать новым встроенным агентом
(Y), подключить к нему существующий репозиторий, привязать самобэкап,
запустить его и подтвердить копию ключей.

Вход — тот же пароль администратора, что до аварии; пароль вводится без эха и
идёт в `curl` стандартным вводом.

<!-- drill: login -->
```bash
set -euo pipefail
umask 077
cd ~/sard-recovery
read -rs -p 'administrator password: ' A; echo
A=$(printf '%s' "$A" | sed 's/\\/\\\\/g; s/"/\\"/g')
CODE=$(printf '{"password":"%s"}' "$A" | curl -sS -o /dev/null -w '%{http_code}' -c cookie -H 'Content-Type: application/json' --data-binary @- http://localhost:8080/api/v1/session)
unset A
echo "$CODE"
test "$CODE" = 204
```

Сосед запускается до отзыва и ждёт токен:

<!-- drill: self-agent-wait -->
```bash
set -euo pipefail
cd ~/sard
docker compose up -d --wait --pull never self-agent
sleep 15
docker compose logs self-agent | grep -c 'waiting for the enrollment token'
docker compose exec -T server ls /var/lib/sard/self
```

Одна строка `waiting for the enrollment token` и в канале только пустой `db-password` (заглушка `db-init`), без
`enroll-token` — так и должно быть. Теперь отзыв X
(`POST /api/v1/agents/<id>/revoke?confirm=sard-self`, в консоли — «Отозвать»
с вводом `sard-self`). Отзыв переводит шаги X в `lost` с сообщением `agent
revoked`; сервер выпускает токен, сосед регистрируется сам:

<!-- drill: revoke-x -->
```bash
set -euo pipefail
cd ~/sard-recovery
API=http://localhost:8080/api/v1
live() { curl -fsS -b cookie "$API/agents?limit=100" | tr '{' '\n' | grep '"builtin":true' | grep '"revokedAt":null' | grep -o '"id":"[0-9a-f-]*"' | head -1 | cut -d'"' -f4 || true; }
X=$(live)
test -n "$X"
echo "previous built-in agent: $X"
curl -fsS -b cookie -X POST "$API/agents/$X/revoke?confirm=sard-self" | grep -o '"revokedAt":"[^"]*"'
Y=
for I in $(seq 60); do
  Y=$(live)
  if [ -n "$Y" ] && [ "$Y" != "$X" ]; then break; fi
  sleep 3
done
test -n "$Y" -a "$Y" != "$X"
echo "new built-in agent: $Y"
for I in $(seq 60); do
  if curl -fsS -b cookie "$API/agents?limit=100" | tr '{' '\n' | grep "\"id\":\"$Y\"" | grep -F '"status":"online"' > /dev/null; then break; fi
  sleep 3
done
curl -fsS -b cookie "$API/agents?limit=100" | tr '{' '\n' | grep "\"id\":\"$Y\"" | grep -o '"status":"[a-z]*"'
curl -fsS -b cookie "$API/self-backup" | grep -o '"warnings":\[[^]]*\]'
```

**Проверка.** Печатается `revokedAt` отозванного X, затем id нового Y, не равный
X, его статус `online` и предупреждения самобэкапа: `agent_replaced` (источники
ещё привязаны к X) и `failing`: запуски самобэкапа, шедшие в момент снимка,
стали `failed` (шаг 7), пока не пройдёт новый.

Подключение существующего репозитория соседом — `repo add` с паролем и ключами
из файлов шага 0 (`--password-from-file`, `--secret-key-from-file`; значениями в
командной строке они не идут). Команда — та же, что показывает мастер, плюс
каталог `~/sard-recovery` только для чтения. Для AWS и B2 вместо адреса —
`s3:<бакет>[/<путь>] --provider aws` (или `b2`) `--region <регион>`:

<!-- drill: repo-add -->
```bash
set -euo pipefail
cd ~/sard
docker compose run --rm --no-deps --pull never -u 0 -v "$HOME/sard-recovery:/recovery:ro" \
  --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER --cap-add SETUID --cap-add SETGID \
  self-agent repo add '<repositoryName>' '<адрес-restic>' \
  --access-key-id "$(cat ~/sard-recovery/s3.id)" --region "$(cat ~/sard-recovery/s3.region)" \
  --secret-key-from-file /recovery/s3.secret --password-from-file /recovery/restic.pass \
  --no-restart --config /etc/sard/self/agent.yaml
docker compose restart self-agent
API=http://localhost:8080/api/v1
S=
for I in $(seq 60); do
  S=$(curl -fsS -b ~/sard-recovery/cookie "$API/self-backup" | grep -o '"repositories":\[[^]]*\]' || true)
  case "$S" in *'"repositoryId":"<repositoryId>"'*) break ;; esac
  sleep 3
done
case "$S" in *'"repositoryId":"<repositoryId>"'*) ;; *) echo 'the neighbour does not report the repository' >&2; exit 1 ;; esac
echo "$S"
```

**Проверка.** Вывод `repo add` говорит, что репозиторий подключён (`attached`),
а `repositories` состояния самобэкапа содержит репозиторий с именем и
`repositoryId` из карточки.

Перепривязка (`PUT /api/v1/self-backup/repository` с `location` карточки)
переносит прежние источники (id и история сохраняются) на Y и записывает
`location` в новую карточку восстановления. Затем «запустить сейчас»
(`POST /api/v1/self-backup/runs`) и ожидание обоих запусков:

<!-- drill: rebind -->
```bash
set -euo pipefail
cd ~/sard-recovery
API=http://localhost:8080/api/v1
printf '{"repositoryName":"%s","location":"%s"}' '<repositoryName>' '<location>' | curl -fsS -b cookie -X PUT -H 'Content-Type: application/json' --data-binary @- "$API/self-backup/repository" > /dev/null
curl -fsS -b cookie "$API/self-backup/recovery-card" | grep -o '"location":"[^"]*"'
```

<!-- drill: backup-now -->
```bash
set -euo pipefail
cd ~/sard-recovery
API=http://localhost:8080/api/v1
curl -fsS -b cookie -X POST "$API/self-backup/runs" | grep -o '"role":"[a-z_]*"'
S=
for I in $(seq 100); do
  S=$(curl -fsS -b cookie "$API/self-backup")
  case "$S" in *'"firstBackupDone":true'*) break ;; esac
  sleep 3
done
case "$S" in *'"firstBackupDone":true'*) ;; *) echo 'the first backups did not finish' >&2; exit 1 ;; esac
echo "$S" | grep -o '"warnings":\[[^]]*\]'
```

**Проверка.** Печатается `location`, равный `location` карточки; затем роли двух
запущенных источников (`self_database`, `self_keys`) и предупреждения:
остаётся `keys_unconfirmed` (после перепривязки копию пароля и доступа просят
подтвердить заново). Подтвердите её (`keys-confirmation`):

<!-- drill: confirm-keys -->
```bash
set -euo pipefail
cd ~/sard-recovery
API=http://localhost:8080/api/v1
CODE=$(curl -sS -o /dev/null -w '%{http_code}' -b cookie -X POST -H 'Content-Type: application/json' -d '{"copiedOutsideServer":true}' "$API/self-backup/keys-confirmation")
echo "$CODE"
test "$CODE" = 204
S=$(curl -fsS -b cookie "$API/self-backup")
echo "$S" | grep -o '"status":"[a-z]*"' | head -1
echo "$S" | grep -o '"warnings":\[[^]]*\]'
```

**Проверка.** Код `204`, `"status":"ok"` и `"warnings":[]`.

**Если не вышло.** `WRONG_PASSWORD` в выводе `repo add` — неверный пароль в
`restic.pass`, следов не осталось: поправьте файл и повторите блок. Отзыв
отвечает `409` — нет `confirm=sard-self`. Y не появляется — сервер выпускает
токен раз в 15 секунд (`SARD_SELF_CHECK_INTERVAL`), смотрите
`docker compose logs self-agent server`. `422 repository_not_initialized` при
перепривязке — сосед ещё не прислал репозиторий: подождите минуту.
`first_backup_pending` при подтверждении — оба запуска ещё не завершены.
Запуск `failed` — сообщение шага в консоли, чаще всего ключи хранилища или права
`sard_backup` ([агент-сосед](../operations/self-agent.md)).

## Шаг 6. Имя сервера и агенты

Переведите DNS-имя сервера на новую ВМ (запись A или перенос IP). Имя то же, что
было: сертификат сервера выпускается на имена `SARD_PKI_SERVER_NAMES` из `.env`,
порт 9090 открыт, прокси, завершающего TLS, между агентом и сервером нет
([раздел 4](04-tls-and-names.md)). Агенты, зарегистрированные до снимка базы,
подключаются сами, без перерегистрации и перезапуска: CA тот же, их
сертификаты в базе. Они переподключаются с нарастающей паузой, поэтому на это
уходит до нескольких минут.

Имя меняется — добавьте новое в `SARD_PKI_SERVER_NAMES` (старое оставьте),
поправьте `SARD_AGENT_ENDPOINT`, на каждом агенте поправьте `server.address` в
`/etc/sard/agent.yaml` и перезапустите службу — порядок и подробности в
[разделе 8](08-migrate-and-remove.md). На будущее используйте DNS-имя, а не IP.

<!-- drill: agents-online -->
```bash
set -euo pipefail
cd ~/sard-recovery
curl -fsS -b cookie 'http://localhost:8080/api/v1/agents?limit=100' | tr '{' '\n' | grep '"status":"online"' | grep -o '"hostname":"[^"]*"'
```

**Проверка.** Блок печатает имена хостов агентов в состоянии `online`: все
агенты, зарегистрированные до снимка базы, и `sard-self`.

**Если не вышло.** Агент не появляется — на его хосте
`journalctl -u sard-agent --since '-10min'`: ошибка TLS про имя хоста значит,
что имени нет в `SARD_PKI_SERVER_NAMES`, закрытый порт 9090 — фаервол.
`CERT_UNKNOWN` — агент зарегистрирован после снимка базы, см. шаг 7.

## Шаг 7. После восстановления

Страница не знает, что случилось между снимком базы и аварией, но последствия
предсказуемы. Агенты, зарегистрированные после снимка базы, получают
`CERT_UNKNOWN` и останавливаются с кодом 78 (systemd их не перезапускает). На
хосте агента `journalctl -u sard-agent` показывает `reason CERT_UNKNOWN … not
reconnecting`, в логе сервера — `agent call refused: reason=CERT_UNKNOWN`:

```bash
journalctl -u sard-agent --no-pager | grep CERT_UNKNOWN
```

Такой агент регистрируется заново новым токеном (консоль → «Токены
регистрации»). На его хосте токен вводится без эха и не попадает в командную
строку:

<!-- drill: reenroll -->
```bash
set -euo pipefail
umask 077
read -rs -p 'enrollment token: ' T; echo
printf '%s' "$T" > /tmp/sard-enroll-token
unset T
sudo sard-agent enroll --force --server <имя-сервера>:9090 --token-file /tmp/sard-enroll-token
rm -f /tmp/sard-enroll-token
sudo systemctl restart sard-agent
```

Старая запись такого агента остаётся в консоли офлайн: отзовите её. Остальные
последствия:

- **Токены регистрации, выпущенные после снимка,** сервер не знает
  (`TOKEN_UNKNOWN`): выпустите новые.
- **Агенты, отозванные после снимка,** снова живы: отзыв был после снимка, а в
  базе его нет. Отзовите их ещё раз (`POST /api/v1/agents/<id>/revoke` или
  «Отозвать» в консоли), иначе они продолжат подключаться.
- **Пароль администратора** — тот, что был на момент снимка базы. Сменили после
  снимка — действует прежний; смените снова в настройках.
- **Снимки, сделанные после снимка базы,** есть в хранилищах, но не в истории
  сервера: история обрывается на снимке D. Данные при этом целы.
- **Запуски, шедшие в момент снимка,** станут `failed` с шагом `lost`: сервер
  не получил результат. Это касается и запуска самобэкапа базы, который и делал
  снимок D, и запуска ключей, поставленного вместе с ним.
- **Расписания.** У источника с расписанием, пропустившим срабатывания за время
  простоя, будет один догоняющий запуск (`trigger` `catch_up`, период простоя —
  в `catchUp`), если пропусков два и больше; один пропуск — обычный поздний
  запуск. Источник, запуск которого шёл в момент снимка, догоняющего не
  получает: срабатывание записывается как `skipped_active`.
- **Пропуск в истории** виден по уже существующему: `failed` с шагом `lost`,
  `catchUp` и строка `CA fingerprint=… origin=existing` в логе первого старта
  новой ВМ. Отдельной отметки «восстановлено из бэкапа» нет.

Уборка: в `~/sard-recovery` лежат пароль репозитория, ключи хранилища, cookie и
копия ключа CA. Они нужны только на время восстановления:

<!-- drill: cleanup -->
```bash
set -euo pipefail
rm -rf ~/sard-recovery
test ! -e ~/sard-recovery
```

Храните пароль репозитория и карточку вне ВМ, как и раньше
([раздел 6](06-data-and-backup.md)). Карточку после восстановления сервер уже
выдаёт новую (`GET /api/v1/self-backup/recovery-card`): распечатайте её заново.
