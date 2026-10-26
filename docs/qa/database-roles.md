<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# QA: роли PostgreSQL, bootstrap.sql и пароли базы (DB2)

Сценарии: `docs/specs/server/database-roles.feature`. Спецификация — черновик,
ждёт утверждения владельцем. Шаги, помеченные «(ОВN)», проверяют рекомендацию
specifier к открытому вопросу N из шапки feature-файла и меняются вместе с
решением владельца.

Здесь вручную проходятся сценарии с тегом `@qa`: новая установка, отказы
`sard_app`, неизменность записей проверок, обновление с 0.1.0-beta.2, отказ
переноса, восстановление по F7 и `bootstrap.sql`. Остальное проверяют тесты
`@pg`, `@startup`, `@arch`, `@ci`, `@deploy`, `@upgrade`, `@drill`, `@doc` с теми
же названиями.

Ожидаемый результат указан после «→» в каждом шаге. Любое расхождение — дефект.

## Подготовка

Запросы `$PSQL` идут через локальный сокет контейнера `postgres`, где
официальный образ пускает без пароля: это способ осмотреть каталог, а не
проверка паролей. Пароли проверяются только входом по сети compose (шаг 8).

Нужны Docker с плагином compose 2.23+, `curl`, `openssl`, GNU `grep`. Образы
сервера и агента `-pg18` этой версии собраны (`make e2e-images`) или скачаны.

1. Создать каталог стенда и переменные:

   ```bash
   QA=$(mktemp -d); cd "$QA"
   cp ~/Sard/deploy/docker-compose.yml ~/Sard/deploy/.env.example .
   cp .env.example .env
   sed -i "s/^SARD_VERSION=.*/SARD_VERSION=<версия>/" .env
   PSQL='docker compose exec -T postgres psql -U sard -d sard -Atc'
   ```

   → `.env` создан; в нём нет ни одной строки с `DB_PASSWORD` в имени
   (`grep -c DB_PASSWORD .env` печатает `0`).

## Часть 1. Новая установка

2. `docker compose up -d --wait` → команда завершается кодом 0;
   `curl -fsS http://localhost:8080/api/v1/status` отвечает 200 с версией сборки.
3. `$PSQL "select rolname, rolsuper, rolcreaterole, rolcreatedb, rolreplication, rolbypassrls, rolcanlogin from pg_roles where rolname like 'sard\_%' order by 1"`
   → пять строк: `sard_app`, `sard_backup`, `sard_migrator`, `sard_monitor`,
   `sard_owner`; во всех столбцах атрибутов `f`, кроме `rolcanlogin`: `f` только
   у `sard_owner` (ОВ8: `t` у `sard_monitor`). Строки `sard_self` нет (ОВ7).
4. `$PSQL "select count(*) from pg_tables where schemaname = 'public'"` → `0`.
   `$PSQL "select distinct tableowner from pg_tables where schemaname = 'sard'"` →
   одна строка `sard_owner`.
5. `$PSQL "select usename, count(*) from pg_stat_activity where datname = 'sard' and backend_type = 'client backend' group by 1"`
   → соединения сервера только под `sard_app` (и `sard_backup`, если сосед в этот
   момент делает дамп); ни одного `sard` (ОВ5).
6. Файлы паролей (ОВ6): `docker compose exec -T server sh -c 'ls /run/secrets 2>/dev/null; env' | grep -ci password`
   и то же для `self-agent` → сервер видит только файл пароля `sard_app`, сосед —
   только файл пароля `sard_backup`; ни в одном окружении нет значения пароля.
7. Значение пароля `sard_app` (из файла в контейнере сервера) ищется в
   `.env`, `docker-compose.yml`, `docker compose logs` и
   `docker inspect $(docker compose ps -q)` → не найдено нигде.

## Часть 2. sard_app только читает и пишет строки

8. Войти ролью `sard_app` паролем из её файла по сети compose (не через сокет и
   не через `127.0.0.1` контейнера `postgres`: там образ пускает без пароля):
   `docker run --rm -it --network sard_default -e PGPASSWORD="<пароль sard_app>" postgres:18.6-alpine psql -h postgres -U sard_app -d sard`.
   Выполнить по одной: `create table sard.x (i int);`, `alter table runs add column x int;`,
   `drop table runs;`, `truncate runs;`, `create temp table x (i int);`
   → каждая завершается `ERROR: permission denied …` или `must be owner of table runs`
   (SQLSTATE 42501); `\d runs` после них — без изменений.
9. В том же сеансе: `select id from restore_verifications limit 1;` (если пусто —
   сначала дождаться проверки восстановления или пройти шаг 12 части 4 после
   первого цикла), затем `update restore_verifications set status = 'passed' where id = '<id>';`
   и `delete from restore_verifications where id = '<id>';`
   → обе команды — `permission denied for table restore_verifications`; строка
   прежняя (`select * … where id = '<id>'` до и после совпадают).

## Часть 3. Смена пароля роли (ОВ4)

10. Выполнить процедуру «Смена пароля роли» из `docs/operator/16-postgresql.md`
    для `sard_app` → после неё `curl -fsS http://localhost:8080/api/v1/status`
    отвечает 200; вход шага 8 прежним паролем отвергается
    (`password authentication failed`), новым — проходит.

## Часть 4. Самобэкап и восстановление

11. Пройти мастер первого запуска и самобэкап по `docs/operator/06-data-and-backup.md`,
    «Ручной бэкап» → оба запуска самобэкапа `succeeded`; в журнале PostgreSQL
    или `pg_stat_activity` во время дампа пользователь соединения соседа —
    `sard_backup`.
12. Восстановить снимок ключей и конфигурации (как в шаге 2 страницы 12) во
    временный каталог и искать в нём значения паролей ролей:
    `grep -rF "<пароль sard_app>" keys/` → ничего не найдено (ОВ6).
13. Пройти `docs/operator/12-server-recovery.md` на чистой машине (или запустить
    `ServerRecoveryDrillTest`) → все проверки страницы выполнены; после шага 4
    страницы шаги 4 и 5 этой процедуры дают тот же результат на новой машине.

## Часть 5. Обновление с 0.1.0-beta.2

14. В новом каталоге поднять 0.1.0-beta.2 его собственным `docker-compose.yml`
    (`git show v0.1.0-beta.2:deploy/docker-compose.yml`), пройти мастер,
    зарегистрировать агента, создать источник, выполнить запуск. Записать:
    число агентов, источников, запусков, отпечаток CA из лога → значения записаны.
    Если образа 0.1.0-beta.2 в реестре нет — шаг выполняется с 0.1.0-beta.1, и
    это отмечается в отчёте QA.
15. Заменить `docker-compose.yml` файлом этой версии, поднять `SARD_VERSION`,
    `docker compose up -d --wait` → код 0; числа шага 14 и отпечаток CA те же;
    вход прежним паролем администратора; `docker compose logs server | grep -c 'setup code'` → `0`.
16. Повторить шаги 3–5 → те же результаты, что у новой установки.
17. `docker compose restart` и снова шаги 3–4 → вывод побайтово тот же, что в
    шаге 16; в `docker compose logs --since <время рестарта>` нет строк `WARN` или
    `ERROR` о ролях и правах.
18. Самобэкап «запустить сейчас» → оба запуска `succeeded` у тех же источников
    самобэкапа, что до обновления (ОВ7).
19. Откат без восстановления базы (ОВ11): `SARD_VERSION` обратно на 0.1.0-beta.2,
    `docker compose up -d server` → сервер не стартует, лог содержит
    `DATABASE_NEWER_THAN_SERVER` или `password authentication failed` (ОВ6);
    шаг 4 по-прежнему печатает `0` таблиц в `public`.
    Вернуть версию.

## Часть 6. Отказ переноса

20. Поднять 0.1.0-beta.2 как в шаге 14 и, не обновляя, открыть сеанс
    `docker compose exec postgres psql -U sard -d sard` с
    `begin; lock table runs in access exclusive mode;` (сеанс не закрывать).
21. Обновить как в шаге 15, но `docker compose up -d` без `--wait` и через минуту
    `docker compose ps` и `docker compose logs` → сервер не в состоянии healthy и
    не принимает соединений на 8080; в логе шага инициализации названа таблица
    `runs`, причина `lock_timeout` и ссылка на `docs/operator/16-postgresql.md`;
    `$PSQL "select count(*) from pg_tables where schemaname = 'public'"` из другого
    сеанса — прежнее число таблиц 0.1.0-beta.2.
22. Закрыть сеанс шага 20 (`rollback;`) и выполнить `docker compose up -d --wait`
    → код 0; шаги 3–5 — как у новой установки.

## Часть 7. bootstrap.sql внешнего кластера

23. `docker run -d --name qa-pg -e POSTGRES_PASSWORD=x postgres:18.6-alpine` и
    `docker exec -i qa-pg psql -U postgres -v ON_ERROR_STOP=1 < ~/Sard/deploy/db/bootstrap.sql`
    → код 0; запрос шага 3 (с `-U postgres`) печатает те же пять ролей с теми же
    атрибутами; у всех `rolpassword` — NULL.
24. Выполнить `bootstrap.sql` второй раз → код 0, вывод шага 23 тот же.
25. `docker exec qa-pg psql -U postgres -c "create role weak login"` и выполнить
    скрипт под `weak` в чистом кластере → ненулевой код, сообщение называет
    недостающее право; ролей Sard и схемы `sard` в кластере нет.
26. Задать пароли `sard_migrator` и `sard_app` (`\password`), выполнить миграции и
    запустить сервер по разделу 16, «Внешний кластер» → `GET /api/v1/status` —
    200; в логе сервера нет строки `WARN` о правах роли.
27. Запустить тот же сервер с `SARD_DB_USER=postgres` → сервер стартует, в логе
    одна строка `WARN` о правах роли со ссылкой на раздел 16; на слушателе метрик
    `sard_database_role_excess{reason="superuser"} 1` (ОВ12).
28. `docker rm -f qa-pg`; в каталогах стенда `docker compose down -v` → ресурсы
    удалены.
