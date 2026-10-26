<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# QA: роли PostgreSQL, bootstrap.sql и пароли базы (DB2)

Сценарии: `docs/specs/server/database-roles.feature`. Обновление со сборок до
DB2 не поддерживается и здесь не проверяется.

Здесь вручную проходятся сценарии с тегом `@qa`: новая установка, аварийный
доступ, отказы `sard_app`, неизменность записей проверок, смена пароля, отказы
`db-init` и `migrate`, `lock_timeout` миграций, восстановление по F7 и
`bootstrap.sql`. Остальное проверяют тесты `@pg`, `@startup`, `@arch`, `@ci`,
`@deploy`, `@drill`, `@doc` с теми же названиями.

Ожидаемый результат указан после «→» в каждом шаге. Любое расхождение — дефект.

## Подготовка

Нужны Docker с плагином compose 2.23+, `curl`, GNU `grep`. Образы сервера и
агента `-pg18` этой версии собраны (`make e2e-images`) или скачаны.

Запросы `$PSQL` идут через локальный сокет контейнера `postgres` (аварийный
доступ, где официальный образ пускает без пароля): это способ осмотреть
каталог, а не проверка паролей. Пароли проверяются только входом по сети
compose (`$NET`).

1. Создать каталог стенда и переменные:

   ```bash
   QA=$(mktemp -d); cd "$QA"
   # The files the README quickstart downloads (Р15); from a release, use the quickstart itself.
   cp ~/Sard/deploy/docker-compose.yml ~/Sard/deploy/.env.example .
   mkdir db && cp ~/Sard/deploy/db/* db/
   cp .env.example .env
   sed -i "s/^SARD_VERSION=.*/SARD_VERSION=<версия>/" .env
   PSQL='docker compose exec -T postgres psql -U sard -d sard -Atc'
   NET='docker run --rm -i --network sard_default postgres:18.6-alpine psql -h postgres -d sard'
   ```

   → `grep -c DB_PASSWORD .env` печатает `0`.

## Часть 1. Новая установка

1a. `grep -oE 'file: *\./[^ ]+' docker-compose.yml` → каждый путь существует в
    каталоге стенда и есть среди файлов релиза; `grep -c 'CREATE ROLE' docker-compose.yml`
    → `0` (копий db-init.sh и bootstrap.sql в compose-файле нет).

2. `docker compose up -d --wait` → код 0; `docker compose ps -a` показывает
   `db-init` и `migrate` в состоянии `exited (0)`, `postgres`, `server`,
   `self-agent` — `healthy`/`running`;
   `curl -fsS http://localhost:8080/api/v1/status` — 200 с версией сборки.
3. `$PSQL "select rolname, rolsuper, rolcreaterole, rolcreatedb, rolreplication, rolbypassrls, rolcanlogin, rolpassword is null from pg_roles where rolname like 'sard\_%' order by 1"`
   → пять строк `sard_app`, `sard_backup`, `sard_migrator`, `sard_monitor`,
   `sard_owner`; атрибуты 2–6 — `f`; `rolcanlogin` — `f` только у `sard_owner`;
   пароль NULL у `sard_owner` и `sard_monitor`. Строки `sard_self` нет.
4. `$PSQL "select count(*) from pg_tables where schemaname = 'public'"` → `0`;
   `$PSQL "select distinct tableowner from pg_tables where schemaname = 'sard'"` →
   одна строка `sard_owner`.
5. `$PSQL "select usename, count(*) from pg_stat_activity where datname = 'sard' and backend_type = 'client backend' group by 1"`
   → соединения сервера только под `sard_app` (и `sard_backup`, если сосед
   делает дамп); `sard` — только сам этот запрос.
6. Файлы паролей: в контейнерах `server`, `self-agent` (и в томах `migrate`
   через `docker volume ls` и `docker run --rm -v <том>:/v alpine ls -l /v`)
   → сервер видит только файл `sard_app`, сосед — только `sard_backup`, том
   `migrate` — только `sard_migrator`; файлы 64 hex-символа, только для чтения.
7. Значения трёх паролей ищутся в `.env`, `docker-compose.yml`,
   `docker compose logs` и `docker inspect $(docker compose ps -aq)` → не найдены.

## Часть 2. Аварийный доступ и суперпользователь

8. `docker compose exec postgres psql -U sard -d sard -c 'select current_user'`
   → код 0, без запроса пароля, печатает `sard`.
9. `$NET -U sard -c 'select 1'` с `-e PGPASSWORD=` каждого из трёх паролей и
   пустым → каждый раз `password authentication failed` (или отказ `pg_hba`);
   вход по сети суперпользователем невозможен.

## Часть 3. sard_app только читает и пишет строки

10. Войти по сети: `docker run --rm -it --network sard_default -e PGPASSWORD="<пароль sard_app>" postgres:18.6-alpine psql -h postgres -U sard_app -d sard`.
    По одной: `create table sard.x (i int);`, `alter table runs add column x int;`,
    `drop table runs;`, `truncate runs;`, `create temp table x (i int);`
    → каждая — `permission denied …` или `must be owner of table runs`
    (SQLSTATE 42501); `\d runs` после них без изменений.
    Затем `grant select on runs to public;` → только `WARNING: no privileges were
    granted`; `\dp runs` до и после совпадает, у PUBLIC прав нет.
11. После первой проверки восстановления (часть 5, шаг 15, или ручной запуск
    проверки): в том же сеансе `select id from restore_verifications limit 1;`,
    затем `update restore_verifications set status = 'passed' where id = '<id>';`
    и `delete from restore_verifications where id = '<id>';`
    → обе — `permission denied for table restore_verifications`; строка до и
    после совпадает.
12. Под `sard_app` вызвать служебную функцию очистки улик с границей `now()`
    (имя — из ADR DB2) → у записей моложе 180 дней (предел базы) `stages` не
    изменились. Отдельно: сервер с `SARD_RETENTION_VERIFICATIONS=179d` (или
    `sard.retention.verifications=179d`) не стартует, ошибка называет
    `sard.retention.verifications`.

## Часть 4. Смена пароля роли

13. Выполнить процедуру «Смена пароля роли» из `docs/operator/16-postgresql.md`
    для `sard_app` → `GET /api/v1/status` снова 200; вход шага 10 прежним
    паролем отвергается, новым — проходит.

## Часть 5. Самобэкап и восстановление

14. Пройти мастер и самобэкап по `docs/operator/06-data-and-backup.md`, «Ручной
    бэкап» → оба запуска `succeeded`; во время дампа соединение соседа в
    `pg_stat_activity` — `sard_backup`.
15. Восстановить снимок ключей и конфигурации (шаг 2 страницы 12) во временный
    каталог и `grep -rF "<пароль>" keys/` для каждого из трёх паролей → ничего.
16. Пройти `docs/operator/12-server-recovery.md` на чистой машине (или запустить
    `ServerRecoveryDrillTest`) → все проверки страницы выполнены; шаги 3–5 этой
    процедуры на новой машине дают тот же результат; пароли в файлах новой
    машины не равны паролям старой.

## Часть 6. Отказы db-init и migrate

17. `docker compose down`; положить в `docker-compose.override.yml` для
    `db-init` заведомо ломающую настройку (например, неверный путь сокета
    PostgreSQL) и `docker compose up -d` → `migrate` и `server` не созданы или не
    запущены; лог `db-init` называет причину и `docs/operator/16-postgresql.md`.
    Убрать override.
18. Открыть сеанс `docker compose exec postgres psql -U sard -d sard` с
    `begin; lock table runs in access exclusive mode;` (не закрывать). В другом
    терминале положить в стенд тестовую миграцию над `runs` (сборка образа
    сервера с ней — см. ADR DB2) и `docker compose up -d` → `migrate` завершается
    ошибкой `lock_timeout` не позже чем через 10 секунд, `server` не запущен,
    `$PSQL "select count(*) from flyway_schema_history"` — прежнее число.
19. `rollback;` в сеансе шага 18 и `docker compose up -d --wait` → код 0;
    шаги 3–5 — как у новой установки.

## Часть 7. bootstrap.sql внешнего кластера

20. `docker run -d --name qa-pg -e POSTGRES_PASSWORD=x postgres:18.6-alpine` и
    `docker exec -i qa-pg psql -U postgres -v ON_ERROR_STOP=1 < ~/Sard/deploy/db/bootstrap.sql`
    → код 0; запрос шага 3 (с `-U postgres`) печатает те же пять ролей с теми же
    атрибутами; у всех `rolpassword` — NULL.
21. Выполнить `bootstrap.sql` второй раз → код 0, вывод шага 20 тот же.
22. В новом чистом кластере `create role weak login` и выполнить скрипт под
    `weak` → ненулевой код, сообщение называет недостающее право; ролей Sard и
    схемы `sard` нет.
23. Задать пароли `sard_migrator` и `sard_app` (`\password`), выполнить
    `migrate` и запустить сервер по разделу 16, «Внешний кластер» →
    `GET /api/v1/status` — 200; в логе сервера нет строки `WARN` о правах роли.
24. Запустить тот же сервер с `SARD_DB_USER=postgres` → сервер стартует, в логе
    одна строка `WARN` о правах роли со ссылкой на раздел 16; на слушателе метрик
    `sard_database_role_excess{reason="superuser"} 1`.
25. `docker rm -f qa-pg`; в каталоге стенда `docker compose down -v` → ресурсы
    удалены.
