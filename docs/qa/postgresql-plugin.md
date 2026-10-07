# QA: плагин `postgresql` агента (F1)

Сценарии: `docs/specs/agent/postgresql-plugin.feature` (ЧЕРНОВИК: решения
ПГ1–ПГ22 приняты владельцем 2026-10-07, ПГ17 изменено, подрешения
ПГ17a–ПГ17j ждут ответа). Здесь вручную проходятся сценарии с тегом
`@qa`; остальные проверяют тесты `@unit`, `@restic`, `@register`, `@doc`,
`@e2e` с теми же названиями.

Ожидаемый результат указан после «→» в каждом шаге. Любое расхождение — дефект.

## Подготовка

Нужны: Docker, Go, `restic` 0.19.1 (`.bin/restic`), `curl`, `jq`, клиент
PostgreSQL 18 на хосте (`pg_dump`, `psql`, `pg_restore`, `createdb`; пакет
`postgresql-client-18` из PGDG или аналог). Команды — из корня репозитория,
не от root.

1. Выполнить «Подготовку» (шаги 1–3) из `docs/qa/files-plugin.md` и
   определить вспомогательные функции из начала её части 2 (`src`, `run`,
   `snaps`): сервер поднят, агент зарегистрирован, репозиторий `qa`
   настроен; есть переменные `AG`, `H`, `QA`, `PSQL`, `API`, `AGENT`,
   функция `rs`. Функция `src` там создаёт источник плагина `files` —
   заменить в ней `\"plugin\":\"files\"` на `\"plugin\":\"postgresql\"`.
   Остановить агента: `kill $AGPID`.
2. Сервер PostgreSQL 18 для QA с TLS выключенным (как в официальном образе):

```bash
docker run -d --name qa-pg -e POSTGRES_PASSWORD=admin -p 55432:5432 postgres:18
until docker exec qa-pg pg_isready -U postgres >/dev/null; do sleep 1; done
A() { docker exec -i qa-pg psql -U postgres -v ON_ERROR_STOP=1 "$@"; }   # psql суперпользователем
P="p'a:s\\s w0rd-Ж"                                                       # пароль роли backup
A -c "create role backup login password \$\$$P\$\$" -c "grant pg_read_all_data to backup" -c "create database app"
A -d app -c "create table t(id int primary key, v text); insert into t select g, md5(g::text) from generate_series(1,100000) g"
A -d app -c "create schema audit; create table audit.a(x int); create table public.log_x(x int); create table secret_t(x int); revoke all on secret_t from public"
A -d app -c "create sequence s; select setval('s', 42)"
A -c "create role app_owner nologin" -c "create role app_rw login password 'qa-rw-pass' in role app_owner" \
  -c "alter role app_rw set search_path = public" -c "create role app_ro login createdb"
A -d app -c "alter table t owner to app_owner"
```

   → каждая команда печатает `CREATE …`/`INSERT …`/`setval 42` без `ERROR`.
   Роль `backup` — член `pg_read_all_data`, поэтому `secret_t` она читает;
   для шага 13 права у неё отнимаются.
3. Секрет агента вручную (до A8):

```bash
mkdir -p $H/secrets; printf '%s\n' "$P" > $H/secrets/pg-app; chmod 0600 $H/secrets/pg-app
printf 'secrets:\n  pg-app: %s\n' "$H/secrets/pg-app" >> "$H/agent.yaml"
"$AG" --config "$H/agent.yaml" > $QA/agent.out 2>&1 & AGPID=$!
# cfg [<json-объект>] — конфиг источника: базовый, поля аргумента заменяют базовые
cfg() { jq -cn --argjson o "${1:-null}" \
  '{host:"127.0.0.1",port:55432,database:"app",user:"backup",password_ref:"pg-app",tls_mode:"disable"} + ($o // {})'; }
```

   → агент работает; `$PSQL "select secret_names from agents where id='$AGENT'"`
   содержит `pg-app`.

## Часть 1. Register и схема

4. `$PSQL "select array_to_string(actions, ',') from agent_plugins where agent_id='$AGENT' and name='postgresql'"`
   → ровно `backup,restore`; `version` той же строки совпадает с `"$AG" --version`.
5. `$PSQL "select config_schema from agent_plugins where agent_id='$AGENT' and name='postgresql'" > $QA/pg-schema.json; wc -c < $QA/pg-schema.json`
   → не больше `65536`.
6. `jq '.properties.password_ref, .required, .additionalProperties' $QA/pg-schema.json`
   → у `password_ref` `"type": "string"` и `"format": "sard-secret"`;
   `required` содержит `host`, `database`, `user`, `password_ref`;
   `additionalProperties` — `false`.
7. `jq '.properties | to_entries[] | {k: .key, t: .value.title, d: .value.description, e: .value.examples, ru: .value["x-sard-i18n"].ru}' $QA/pg-schema.json`
   → ровно поля `host`, `port`, `database`, `user`, `password_ref`,
   `tls_mode`, `tls_root_cert`, `exclude_schemas`, `exclude_tables`,
   `pg_dump_path`, `include_globals`, `globals_role_passwords`; у каждого непустые `t`, `d`, `e`, `ru.title`,
   `ru.description` (на русском).
8. В консоли: «Источники» → «Создать» → агент, плагин `postgresql`
   → поле «пароль» — выбор из имён секретов агента, в списке `pg-app`;
   поля для ввода значения пароля нет.
9. Неизвестный секрет на сервере:
   `curl -sS -b $QA/jf -X POST $API/sources -H 'Content-Type: application/json' -d "{\"name\":\"qa-bad\",\"agentId\":\"$AGENT\",\"plugin\":\"postgresql\",\"repositoryName\":\"qa\",\"config\":$(cfg '{"password_ref":"nope"}')}" -w '\n%{http_code}\n'`
   → код `422`, ошибка у поля `config/password_ref` называет `nope`.

## Часть 2. Успешный бэкап и восстановление

10. **Бэкап.** `N0=$(snaps); SRC=$(src "$(cfg)"); run $SRC`
    → `status` `succeeded`, `message` пустой или `null`; `snaps` = `N0+2`
    (снимок базы и снимок глобальных объектов);
    `SN=$(curl -sS -b $QA/jf $API/runs/$RUN | jq -r '.steps[0].backup.snapshotId')`.
11. **Файлы и метки.** `rs ls $SN` → ровно один файл `/app.dump`;
    `rs snapshots --json | jq -r ".[] | select(.id==\"$SN\") | .tags | sort | join(\" \")"`
    → содержит `postgresql.database=app`, `postgresql.format=custom`,
    `postgresql.part=database`, `postgresql.pg_dump_version=18.<n>`,
    `postgresql.server_version=18.<n>` и метки `sard.run=`, `sard.source=`.
    `GS=$(rs snapshots --json --tag postgresql.main_snapshot=$SN | jq -r '.[0].id'); rs ls $GS`
    → ровно один файл `/app.globals.sql`; метки `GS` содержат
    `postgresql.part=globals`, `postgresql.format=plain`,
    `postgresql.role_passwords=false`.
    `rs dump $GS /app.globals.sql | grep -c 'SCRAM-SHA-256'` → `0`;
    `rs dump $GS /app.globals.sql | grep -c 'CREATE ROLE app_rw'` → `1`.
12. **Восстановление руками по документации** (сначала глобальные объекты, затем база).

```bash
docker run -d --name qa-pg2 -e POSTGRES_PASSWORD=admin -p 55433:5432 postgres:18
until docker exec qa-pg2 pg_isready -U postgres >/dev/null; do sleep 1; done
rs dump $GS /app.globals.sql | PGPASSWORD=admin psql -X -h 127.0.0.1 -p 55433 -U postgres -d postgres >/dev/null
rs dump $SN /app.dump > $QA/app.dump; head -c 5 $QA/app.dump; echo
PGPASSWORD=admin createdb -h 127.0.0.1 -p 55433 -U postgres restored
PGPASSWORD=admin pg_restore -h 127.0.0.1 -p 55433 -U postgres -d restored $QA/app.dump; echo rc=$?
q() { PGPASSWORD=admin psql -h 127.0.0.1 -p $1 -U postgres -d $2 -At -c "$3"; }
for p in "55432 app" "55433 restored"; do
  q $p "select count(*), md5(string_agg(v, '' order by id)) from t"; q $p "select last_value from s"
  q $p "select tableowner from pg_tables where tablename='t'"
  q $p "select string_agg(rolname||':'||rolcanlogin||rolcreatedb, ',' order by rolname) from pg_roles where rolname like 'app_%'"
  q $p "select string_agg(r.rolname||'>'||m.rolname, ',') from pg_auth_members a join pg_roles r on r.oid=a.member join pg_roles m on m.oid=a.roleid where r.rolname like 'app_%'"
done
```

    → psql с глобальными объектами печатает лишь ошибки «role "postgres"
    already exists» (если есть), остальное без `ERROR`; `head` печатает
    `PGDMP`; `rc=0`; обе базы дают одинаковые `100000|<md5>`, `42`,
    владелец `app_owner`, одинаковый список ролей и членство `app_rw>app_owner`.
    `PGPASSWORD=qa-rw-pass psql -h 127.0.0.1 -p 55433 -U app_rw -d restored -c 'select 1'`
    → вход отклонён (роль без пароля, ПГ17c); после
    `q 55433 restored "alter role app_rw password 'qa-rw-pass'"` — вход проходит.
12а. **Пароли ролей без суперпользователя.** `N0=$(snaps); run $(src "$(cfg '{"globals_role_passwords":true}')")`
    → `failed`; `phase` `preparing`; `message` называет роль `backup`,
    `globals_role_passwords` и суперпользователя; `snaps` = `N0`.
12б. **Без глобальных объектов.** `N0=$(snaps); run $(src "$(cfg '{"include_globals":false}')")`
    → `succeeded`; `snaps` = `N0+1`; `rs ls latest` — только `/app.dump`.
13. **Таблица без права чтения.** `A -c "revoke pg_read_all_data from backup"; N0=$(snaps); run $SRC`
    → `failed`; `message` называет `pg_dump` и `secret_t`
    (`permission denied`); `backup` — `null`; `snaps` = `N0`;
    `rs list locks | wc -l` → `0`. В логе шага
    (`curl -sS -b $QA/jf "$API/runs/$RUN/steps/<step-id>/logs"`) есть строка
    WARN, называющая `backup` и `pg_read_all_data`.
    Затем `run $(src "$(cfg '{"exclude_tables":["secret_t"]}')")` при тех же
    правах — только если остальные таблицы роль читает: сначала
    `A -d app -c "grant usage on schema audit to backup; grant select on t, audit.a, public.log_x, s to backup"`
    → `succeeded`. Вернуть: `A -c "grant pg_read_all_data to backup"`.

## Часть 3. Отказы подготовки

Каждый шаг: `N0=$(snaps)` до запуска; после — `snaps` = `N0`, `backup` —
`null`, `phase` — `preparing`, `message` не содержит `$P`.

14. **Неверный пароль.** `printf 'wrong\n' > $H/secrets/pg-app; run $SRC`
    → `failed`; `message` содержит `password authentication failed for user "backup"`.
    Вернуть: `printf '%s\n' "$P" > $H/secrets/pg-app`.
15. **Нет базы.** `run $(src "$(cfg '{"database":"nope"}')")`
    → `failed`; `message` содержит `database "nope" does not exist`.
16. **TLS require против сервера без TLS.** `run $(src "$(cfg '{"tls_mode":"require"}')")`
    → `failed`; `message` содержит `server does not support SSL`.
17. **Нет pg_dump.** `run $(src "$(cfg '{"pg_dump_path":"/nonexistent/pg_dump"}')")`
    → `failed`; `message` называет `/nonexistent/pg_dump` и
    `no such file or directory`. Затем остановить агента, запустить его с
    `PATH=/usr/sbin:/sbin` (без каталога клиента PostgreSQL) и `run $SRC`
    → `failed`; `message` говорит, что `pg_dump` не найден, и называет
    `postgresql-client`, `postgresql` и `pg_dump_path`. Перезапустить агента
    с обычным `PATH`.
18. **Старый pg_dump.** Подставной клиент:

```bash
mkdir -p $QA/oldpg; printf '#!/bin/sh\necho "pg_dump (PostgreSQL) 16.4"\n' > $QA/oldpg/pg_dump
ln -sf "$(command -v psql)" $QA/oldpg/psql; chmod +x $QA/oldpg/pg_dump
```

    `run $(src "$(cfg "{\"pg_dump_path\":\"$QA/oldpg/pg_dump\"}")")`
    → `failed`; `message` содержит `16.4` и `18.`, говорит поставить
    `pg_dump 18` или новее или задать `pg_dump_path`.

## Часть 4. Сбой и остановка во время дампа

19. Увеличить базу так, чтобы дамп шёл дольше 10 с:
    `A -d app -c "create table big as select g, repeat(md5(g::text), 30) v from generate_series(1, 3000000) g"`.
20. **Сервер упал посреди дампа.** `N0=$(snaps); run $SRC & sleep 4; docker stop qa-pg; wait`
    (до остановки убедиться, что `phase` шага `uploading`)
    → `failed`; `message` называет `pg_dump` и потерю соединения;
    `snaps` = `N0`; `rs list locks | wc -l` → `0`;
    `pgrep -f 'pg_dump|restic.*backup'` → пусто. `docker start qa-pg`, дождаться `pg_isready`.
21. **Отмена.** Запустить прогон `$SRC` и в фазе `uploading` отменить его
    через консоль или API отмены
    → `cancelled`; `snaps` не изменилось; блокировок нет;
    `pgrep -f 'pg_dump|restic.*backup'` → пусто; через 10 с
    `A -At -c "select count(*) from pg_stat_activity where application_name='sard-agent'"` → `0`.
22. **Таймаут.** Как шаг 21, но без отмены и с таймаутом шага 3 с
    → `timed_out`; проверки как в шаге 21.

## Часть 5. Пароль не утекает

23. **Список процессов.** Во время прогона из шага 21 (до отмены):
    `for p in $(pgrep -f 'pg_dump|psql'); do tr '\0' ' ' < /proc/$p/cmdline; echo; done | grep -cF "$P"`
    → `0`; в строках видно `--dbname=` с `host=`, `dbname=`, без `password`.
    От другого пользователя хоста (`sudo -u nobody`):
    `sudo -u nobody cat /proc/$(pgrep -n pg_dump)/environ` → `Permission denied`.
24. **Логи и результаты.**
    `curl -sS -b $QA/jf "$API/runs?limit=100" | jq -r '.items[].message' | grep -cF "$P"` → `0`;
    для каждого прогона частей 2–4 логи шага `… | grep -cF "$P"` → `0`;
    `grep -cF "$P" $QA/agent.out` → `0`.
25. **Нет файлов с паролем и дампом на диске.** `grep -rlF "$P" $H/state $QA/state 2>/dev/null | wc -l` → `0`;
    `find $H/state \( -name '*.dump' -o -name '*.globals.sql' -o -name '.pgpass' \) | wc -l` → `0`.

## Часть 6. Документация

26. Прочитать `docs/plugins/postgresql.md` → есть создание роли (`LOGIN`,
    `GRANT pg_read_all_data`, `BYPASSRLS` при RLS); секрет в `secrets:` файлом
    `0600` владельца агента и перезапуск; пароль в конфиге не пишется, только
    `password_ref`; пакеты `postgresql-client`/`postgresql` и `pg_dump_path`;
    поиск снимка глобальных объектов по `postgresql.main_snapshot`; порядок
    восстановления — глобальные объекты через `psql` суперпользователем
    (ошибки «already exists» ожидаемы), затем `restic dump` → `createdb` →
    `pg_restore`; требование `pg_restore`/`psql` не старше
    `postgresql.pg_dump_version`; явная фраза, что дамп базы ролей и
    глобальных объектов не содержит, и `pg_restore --no-owner --no-acl` для
    `include_globals: false`; роли без паролей и `ALTER ROLE … PASSWORD`;
    предупреждение о хэшах паролей при `globals_role_passwords`; правило
    имён файлов с примерами `my%20db.dump` и `my%20db.globals.sql`.
27. Шаг 12 выполнен дословно по тексту документа (кроме адресов) → тот же результат.

## Завершение

28. `kill $AGPID; docker rm -f qa-pg qa-pg2; rs unlock --remove-all`.
