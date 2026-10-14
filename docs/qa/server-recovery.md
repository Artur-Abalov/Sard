<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# QA: восстановление сервера Sard без Sard (F7)

Сценарии: `docs/specs/server/server-recovery.feature`. Решения specifier
Р31–Р42 и состояние кода С1–С14 — в её заголовке. Шаг с пометкой `[OQ-NNN]`
написан по предложению на этот вопрос (`docs/open-questions.md`) и меняется
вместе с решением.

СТАТУС: утверждено владельцем 2026-10-14 вместе со спецификацией и всеми
предложениями OQ-211…OQ-222 (OQ-220 — по Р43: вход после восстановления прежним
паролем администратора). Процедура предполагает, что страница
`docs/operator/12-server-recovery.md` уже написана (И3): части 3–4 проходят её
блоки по порядку, эта процедура лишь говорит, что должно получиться после
каждого.

Процедура из шести частей на одной машине с Docker. «Старая ВМ» и «новая ВМ» —
два прохода в каталоге `~/sard` с проектом compose `sard`; гибель старой ВМ —
`docker compose down -v` и удаление каталога. Имя сервера для агентов —
`sard.qa.test`, его «DNS» — `--add-host` у контейнера агента; сервер в обоих
проходах публикует 9090 на хосте.

- **Часть 1 — старая ВМ**: установка, самобэкап в MinIO, агенты, история,
  карточка.
- **Часть 2 — гибель старой ВМ.**
- **Часть 3 — восстановление по странице**, шаги 0–4.
- **Часть 4 — сосед, имя, агенты, история.**
- **Часть 5 — отказы.**
- **Часть 6 — секреты, отказ новой базе, документация, время.**

Ожидаемый результат — после «→» в каждом шаге. Любое расхождение — дефект.
Точные границы (окно потери от старта, проход планировщика, гонка двух
серверов за роль `sard_self`) вручную не проверяются — это делают тесты
`@startup` с управляемыми часами.

## Подготовка

Нужны: Docker с compose, `curl`, `jq`, браузер. Команды — из корня
репозитория, если не сказано иное. Образы текущей сборки: `make e2e-images
e2e-agent-images` (`sard-server:e2e`, `sard-agent:e2e`, `sard-agent:e2e-pg18`).

```bash
docker volume rm sard_postgres-data sard_sard-pki sard_sard-self-channel sard_sard-self-config sard_sard-self-state 2>/dev/null
QA=$(mktemp -d) && chmod 700 "$QA"           # «сейф» владельца: вне ВМ
GW=$(docker network inspect bridge -f '{{(index .IPAM.Config 0).Gateway}}')
docker run -d --name sard-qa-minio -p 9000:9000 \
  -e MINIO_ROOT_USER=sardqa -e MINIO_ROOT_PASSWORD=sardqa-secret-key-0001 minio/minio server /data
docker run --rm --network host --entrypoint sh minio/mc -c \
  'mc alias set m http://127.0.0.1:9000 sardqa sardqa-secret-key-0001 && mc mb m/sard-self'
API=http://localhost:8080/api/v1
J=$QA/cookie
get()  { curl -sS -b $J "$API/$1"; }
send() { curl -sS -o /dev/stderr -w '%{http_code}\n' -b $J -X "$1" "$API/$2" -H 'Content-Type: application/json' "${@:3}"; }
```

→ MinIO запущен, бакет `sard-self` создан; `$GW` — адрес шлюза моста Docker
(обычно `172.17.0.1`).

## Часть 1. Старая ВМ

1. Установка по `docs/operator/02-install.md` в `~/sard` с образами сборки:

   ```bash
   mkdir -p ~/sard && cp deploy/docker-compose.yml ~/sard/ && cd ~/sard
   cat > .env <<EOF
   SARD_VERSION=e2e
   SARD_IMAGE=sard-server
   SARD_AGENT_IMAGE=sard-agent
   SARD_DB_PASSWORD=$(openssl rand -hex 24)
   SARD_PKI_SERVER_NAMES=localhost,127.0.0.1,sard.qa.test
   SARD_AGENT_ENDPOINT=sard.qa.test:9090
   EOF
   sudo chgrp 10001 .env && chmod 640 .env
   cp .env $QA/env.old
   docker compose up -d --wait
   ```

   → все сервисы `healthy` или `running`.
2. Мастер F4a в консоли (`docs/qa/onboarding-setup.md`, часть 1) с паролем
   `correct-horse-battery`; войти, cookie — в `$J`.
   → шаги `ca` и `admin` done.
3. Шаги самобэкапа F4b в консоли: S3, `custom`, endpoint `http://$GW:9000`,
   бакет `sard-self`; команда мастера выполнена в `~/sard`, ключ `sardqa`,
   секрет `sardqa-secret-key-0001`; первые бэкапы прошли; копия ключей
   подтверждена.
   → `get onboarding | jq -r .restriction` → `none`.
4. Пароль репозитория — в сейф:

   ```bash
   (umask 077; docker compose exec self-agent sard-agent repo password self --reveal \
      --config /etc/sard/self/agent.yaml > $QA/restic.pass)
   ```

   → файл `$QA/restic.pass` непустой, права `600`.
5. Обычный агент A1: токен в консоли, контейнер `sard-agent:e2e` с
   `--add-host sard.qa.test:$GW`, `server.address: sard.qa.test:9090`, тома
   `/etc/sard` и `/var/lib/sard-agent` — именованные `qa-a1-etc`, `qa-a1-state`
   (переживают «гибель ВМ»), `sard-agent enroll`, локальный репозиторий
   `main` (`repo add`). Источник files A1 с расписанием `* * * * *`.
   → A1 online; через 3 минуты у источника A1 не меньше трёх запусков
   `succeeded`.
6. Агент A2 так же (`qa-a2-*`), источник files без расписания, три запуска
   «запустить сейчас».
   → три `succeeded`.
7. Снимок учений: `send POST self-backup/runs`, дождаться обоих `succeeded`.
   Сохранить:

   ```bash
   get self-backup/recovery-card > $QA/card.json
   get agents | jq -S '[.items[] | {id, revokedAt}]' > $QA/agents.json
   get sources | jq -S '[.items[] | {id, name}]' > $QA/sources.json
   get 'runs?limit=500' | jq -S '[.items[] | select(.finishedAt != null) | {id, status, finishedAt}]' > $QA/runs.json
   ```

   → `jq -r '.repositoryId, .caFingerprint, .sardVersion, .location' $QA/card.json`
   печатает 64 hex, 64 hex, `e2e`, `s3:http://…:9000/sard-self`.
8. После снимка: агент A3 (как A1, тома `qa-a3-*`), и ещё один запуск
   «запустить сейчас» источника A2; записать id этого запуска в `$QA/after.txt`.
   → A3 online; запуск `succeeded`.

## Часть 2. Гибель старой ВМ

9. ```bash
   cd ~/sard && docker compose down -v --remove-orphans && cd ~ && rm -rf ~/sard
   docker volume ls --format '{{.Name}}' | grep '^sard_'
   ```

   → `grep` ничего не находит; агенты A1, A2, A3 работают и пишут в журнал
   попытки переподключения (`docker logs qa-a1 2>&1 | tail`).

## Часть 3. Восстановление по странице: шаги 0–4

Блоки `docs/operator/12-server-recovery.md` выполняются как есть; значения в
угловых скобках — из `$QA/card.json`; пароль и секрет вводятся в `read -rs` с
клавиатуры (пароль — `cat $QA/restic.pass` в другом окне).

10. Шаг 0 страницы.
    → `stat -c '%a' ~/sard-recovery` → `700`; файлы в нём — `600`; `history |
    tail -20` не содержит ни пароля, ни `sardqa-secret-key-0001`.
11. Шаг 1: список снимков.
    → видны снимки с тегом `postgresql.part=database` и снимки путей
    `/var/lib/sard/pki`, `/etc/sard/install`; выбраны D и K по правилу страницы
    `[OQ-212]`; `restic cat config` печатает `id`, равный `repositoryId`
    карточки.
12. Шаг 1, проверка полноты K.
    → проверка перечисляет `ca.key`, `ca.crt`, `.env`, `docker-compose.yml`.
13. Шаг 2: ключи и конфигурация `[OQ-215]`.
    → `ls -l ~/sard/.env` — группа `10001`, `-rw-r-----`; `diff ~/sard/.env
    $QA/env.old` пуст; `docker run --rm -u 0 -v sard_sard-pki:/p alpine stat -c
    '%u %a %n' /p/ca /p/ca/ca.key /p/ca/ca.crt` → `10001 700`, `10001 600`,
    `10001 600`.
14. Шаг 3: PostgreSQL и база.
    → `docker compose ps` — только `postgres`, `healthy`;
    `docker compose exec -T postgres psql -U sard -d sard -Atc 'select count(*) from agents'`
    → на 1 больше, чем строк A1, A2 (`sard-self` X) — то есть 3; агента A3 нет;
    `select count(*) from pg_roles where rolname = 'sard_self'` → `0`.
15. Шаг 4: сервер.
    → `docker compose logs server | grep 'CA fingerprint='` содержит
    `caFingerprint` карточки и `origin=existing`; `curl -fsS $API/status` —
    версия `e2e`; `curl -fsS $API/onboarding | jq -r .restriction` → `none`; лог
    без `SARD SETUP CODE`.
16. Роль дампа `[OQ-214]`:

    ```bash
    pw=$(docker compose exec -T server cat /var/lib/sard/self/db-password)
    docker compose exec -T -e PGPASSWORD="$pw" postgres psql -h 127.0.0.1 -U sard_self -d sard -Atc 'select count(*) from agents'
    docker compose exec -T -e PGPASSWORD="$pw" postgres psql -h 127.0.0.1 -U sard_self -d sard -c 'set default_transaction_read_only = off; create temp table x (i int)'
    ```

    → первая команда печатает `3`; вторая — ошибка прав (`permission denied`).

## Часть 4. Сосед, имя, агенты, история

17. Шаг 5 страницы, до отзыва: `docker compose up -d self-agent`.
    → через 30 с `docker compose logs self-agent` — одна строка ожидания файла
    токена; `docker compose exec -T server ls /var/lib/sard/self` — без
    `enroll-token`.
18. Шаг 5: вход паролем `correct-horse-battery` и отзыв прежнего встроенного
    агента X `[OQ-216]`.
    → отзыв `200`; через 2 минуты в `get agents` живой встроенный агент Y ≠ X,
    `online`; `get self-backup | jq -c .warnings` содержит `agent_replaced`.
19. Шаг 5: `repo add` существующего репозитория.
    → вывод говорит, что подключён существующий репозиторий, и содержит
    `repositoryId` карточки; через 60 с `get self-backup | jq -c .repositories`
    содержит `self` с тем же `repositoryId`.
20. Шаг 5: перепривязка с `location` карточки и «запустить сейчас».
    → оба запуска `succeeded`; `get self-backup/recovery-card | jq -r .location`
    равно `location` из `$QA/card.json`; `get self-backup | jq -c .warnings` →
    `["keys_unconfirmed"]` `[OQ-217]`; в консоли баннер самобэкапа виден на
    каждой странице до этого шага.
21. Шаг 5: подтверждение копии ключей.
    → `204`; `warnings` → `[]`, `status` → `ok`; баннера нет.
22. Шаг 6: имя. В этой процедуре имя не меняется: сервер новой ВМ уже
    публикует 9090 там же.
    → через 3 минуты A1 и A2 `online` с id из `$QA/agents.json`;
    `docker inspect -f '{{.RestartCount}} {{.State.StartedAt}}' qa-a1` — тот же
    старт, что до части 2.
23. Агент A3.
    → контейнер `qa-a3` остановлен, код выхода `78`; `docker logs qa-a3 2>&1 |
    grep 'reason CERT_UNKNOWN'` находит строку с `not reconnecting`; в логе
    сервера — `reason=CERT_UNKNOWN` с id A3.
24. Шаг 7: повторная регистрация A3 по странице (новый токен, `enroll
    --force`, запуск).
    → A3 `online` с новым id.
25. История.
    → `get agents`, `get sources` и завершённые запуски `get 'runs?limit=500'`
    содержат всё из `$QA/agents.json`, `$QA/sources.json`, `$QA/runs.json` с
    теми же статусами и временем завершения — кроме двух запусков самобэкапа
    шага 7 (их дамп снимался, пока они шли) и запусков, завершившихся после
    начала шага 7; запуска из `$QA/after.txt` нет.
26. Потерянные и догоняющие запуски.
    → оба запуска самобэкапа шага 7 `failed` (в `$QA/runs.json` они
    `succeeded`): шаг базы `lost` по окну или с сообщением `agent revoked`,
    шаг ключей — так же; у источника A1 ровно один запуск с `trigger`
    `catch_up` и `catchUp.missedCount` ≥ 2 (если запуск A1 шёл в момент дампа,
    его догоняющий пропущен как активный и запуск идёт по расписанию следующей
    минуты `[OQ-222]`).
27. Новый бэкап: «запустить сейчас» источника A2.
    → `succeeded`.
28. Уборка по шагу 7 страницы.
    → `~/sard-recovery` удалён.

## Часть 5. Отказы

Каждый пункт начинается с повтора частей 1–2 или с состояния, указанного в
пункте; после пункта — снова часть 2.

29. Неверный пароль: в шаге 0 ввести `wrong`, выполнить шаг 1.
    → restic: `wrong password` (или `no key found`); строка «Если не вышло»
    шага 1 называет эту причину; `docker volume ls | grep sard_` пусто.
30. Хранилище недоступно: `docker stop sard-qa-minio`, шаг 1.
    → restic завершается ошибкой соединения; томов `sard_*` нет;
    `docker start sard-qa-minio`.
31. Чужой репозиторий: в MinIO бакет `other`, в нём `restic init` с тем же
    паролем; шаг 1 с адресом `…/other`.
    → `restic cat config` печатает `id` ≠ `repositoryId` карточки; страница
    велит остановиться.
32. Неполный снимок ключей: на старой ВМ `chmod 600 ~/sard/.env` (группа не
    читает) и «запустить сейчас».
    → запуск ключей `failed`; на новой ВМ проверка полноты последнего K
    называет `.env`; страница велит взять предыдущий K.
33. Непустая база: после шага 3 выполнить блок `pg_restore` второй раз.
    → `pg_restore` сообщает ошибки (`already exists`); строка «Если не вышло»
    шага 3 даёт `DROP DATABASE` / `CREATE DATABASE`; после них и повтора —
    код `0`.
34. Отпечаток не совпал: сравнить строку `CA fingerprint=` с изменённым в
    `$QA/card.json` отпечатком.
    → страница велит `docker compose stop server` и не переводить имя.
35. Неверный пароль в `repo add`: в файл пароля шага 0 — `wrong`, блок `repo
    add`.
    → `WRONG_PASSWORD`; `repositories` без `self`; в томе соседа нет
    `restic-self.pass` (`docker compose run --rm --no-deps -u 0 --entrypoint ls
    self-agent /etc/sard/self/secrets`).
36. Сервер раньше базы: после шага 2, пропустив шаг 3, выполнить блок шага 4
    — так делать нельзя, проверяется, что страница это объясняет.
    → сервер отказывается `CA startup refused` с `CA_ORIGIN_NOT_RECORDED` (CA
    в томе, база пуста), агентам подключаться не к чему; в базе `sard` теперь
    таблицы миграций; «Если не вышло» шага 4 называет эту причину и велит
    пересоздать базу (`DROP DATABASE` / `CREATE DATABASE`) и повторить шаг 3.
    После этого шаг 4 — `origin=existing`.

## Часть 6. Секреты, новая база, документация, время

37. Секреты в логах:

    ```bash
    cd ~/sard
    for s in $(docker compose ps --services); do
      docker compose logs --no-color "$s" 2>&1 | grep -F -q -e "$(cat $QA/restic.pass)" -e sardqa-secret-key-0001 && echo "LEAK in $s"
    done
    for a in qa-a1 qa-a2 qa-a3; do docker logs $a 2>&1 | grep -F -q "$(cat $QA/restic.pass)" && echo "LEAK in $a"; done
    ```

    → ничего не напечатано.
38. Отказ новой базе `[Р31]` `[Р32]`:

    ```bash
    docker compose stop server
    docker compose exec -T postgres psql -U sard -d sard -c "insert into flyway_schema_history
      (installed_rank, version, description, type, script, checksum, installed_by, execution_time, success)
      select max(installed_rank) + 1, '299901010000', 'from the future', 'SQL', 'V299901010000__from_the_future.sql', 0, 'qa', 0, true
      from flyway_schema_history"
    docker compose up -d --wait server; echo "exit $?"
    docker compose logs --no-color server | grep 'Database startup refused'
    ```

    → `up --wait` завершается с ненулевым кодом; строка содержит
    `DATABASE_NEWER_THAN_SERVER`, `299901010000`, `202610121300` (или более
    позднюю последнюю миграцию сборки), версию `e2e` и
    `docs/operator/12-server-recovery.md`. Затем удалить строку
    (`delete from flyway_schema_history where version = '299901010000'`) и
    `docker compose up -d --wait server` → сервер `healthy`.
39. Документация.
    → раздел «Что нужно» страницы говорит, что вход после восстановления —
    тем же паролем администратора, хранить его вместе с карточкой, а
    `admin-reset` — запасной путь `[OQ-220]`; `docs/operator/README.md`
    содержит строку 12; раздел
    `06-data-and-backup.md` «Восстановление из самобэкапа (F7)» ведёт на
    страницу; на странице у шагов 1–6 есть «Проверка» и «Если не вышло»;
    `09-troubleshooting.md` называет `DATABASE_NEWER_THAN_SERVER`; таблица
    `11-offline.md` называет образ `sard-agent`; английская страница — если
    утверждено `[OQ-219]`.
40. Время `[OQ-221]`: `make e2e-test` с фильтром `ServerRecoveryDrillTest`.
    → тест зелёный; `test/e2e/build/recovery-drill/result.md` содержит строку
    в формате таблицы «Сколько это займёт»; в таблице страницы есть строка с
    результатом не старше этого изменения.
