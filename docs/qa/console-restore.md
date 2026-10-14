<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# QA: восстановление из консоли (R2)

Сценарии: `docs/specs/agent/console-restore.feature` (ВК1–ВК25),
`docs/specs/server/console-restore.feature` (КВ1–КВ6, ВК30–ВК39),
`docs/specs/web/console-restore.feature` (ВК40–ВК43).

**ПРОЕКТ, не утверждён владельцем (2026-10-14).** Шаги, зависящие от ответа
на вопрос, помечены «(ВВ<номер>)». Части 1–4 и 6–7 — срез R2a, часть 5 — R2b.
R2c (перезапись) не проверяется: в R2 его нет (ВВ1).

Выполнима после реализации R2a (части 1–4, 6, 7) и R2b (часть 5). Ожидаемый
результат — после «→». Любое расхождение — дефект.

## Стенды

- **Сервер**: `make up` (PostgreSQL + sard-server), владелец прошёл мастер
  (F4a), консоль на `https://<сервер>`; уведомления в Telegram настроены
  (`docs/qa/run-notifications.md`, часть 0), `SARD_NOTIFY_LANGUAGE=ru`,
  `SARD_CONSOLE_PUBLIC_URL` задан.
- **ВМ-1 Debian 12** (`db1`): агент R2 зарегистрирован и онлайн, репозиторий
  `main` (локальный `/srv/sard/main`, `ReadWritePaths` в drop-in) и `s1` (S3);
  PostgreSQL 16 с базой `app` (таблицы `t1`, `t2` по 1000 строк, владелец
  `app_rw`), секрет `pg` (роль `backup`, `pg_read_all_data`) и секрет
  `pg-restore` (роль `restorer` с `CREATEDB`), секрет `pg-super` (роль
  `postgres`).
- **ВМ-2 Debian 12** (`db2`): агент R2 зарегистрирован, подключён `s1` той же
  командой `repo add` с паролем из копии (`docs/qa/host-setup.md`, шаг 50).
- **ВМ-3** (`db3`): агент предыдущей версии (до R2) с подключённым `s1`.
- **PG-14**: отдельный PostgreSQL 14 (`pg14:5432`), доступный с ВМ-1, роль
  `restorer` с `CREATEDB`.

Источники в консоли (бэкап каждого — `succeeded` перед частью 2):

| источник | плагин | агент | репозиторий | что |
|---|---|---|---|---|
| `qa files` | files | db1 | `s1` | `/srv/qa-data` (как `docs/qa/repo-restore.md`, стенды) |
| `qa-big` | files | db1 | `s1` | `/srv/big`, 4 ГБ |
| `qa-pg` | postgresql | db1 | `s1` | база `app`, `include_globals: true` |

Переменные:

```bash
API=https://<сервер>/api/v1
C="curl -sS -b $HOME/sard.cookies -H content-type:application/json"   # cookie sard_session после входа
SF=<id источника qa files>; SB=<id qa-big>; SQ=<id qa-pg>
A=<agentId db1>; B=<agentId db2>; OLD=<agentId db3>
SNAP=$($C "$API/sources/$SF/snapshots" | jq -r '.items[0].snapshotId')
REPO_PASS=$(sudo cat /etc/sard/secrets/restic-s1.pass)   # на ВМ-1, только для grep утечек
```

## Часть 1. Каталог восстановления на хосте

1. ВМ-1: в `/etc/sard/agent.yaml` и `agent.d` нет `restore:`;
   `$C "$API/agents/$A" | jq -r .restoreDir` → `/var/lib/sard-agent/restore`.
2. ВМ-3: `$C "$API/agents/$OLD" | jq .restoreDir` → `null`.
3. ВМ-1: добавить `restore: {dir: relative}` (`sudoedit /etc/sard/agent.d/restore.yaml`),
   `sudo systemctl restart sard-agent` → служба не `active`;
   `journalctl -u sard-agent -n 20` называет `restore.dir`. То же с `dir: /`
   и с `dir: /var/cache/sard/restic/x`. Удалить фрагмент, перезапустить →
   `active`.

## Часть 2. REST: снимки, цели, предпросмотр, отказы

4. `$C "$API/sources/$SF/snapshots" | jq '.items[0] | {restorable, restoreBlockedReason}'`
   → `true`, `null`.
5. `$C "$API/sources/$SF/snapshots/$SNAP/restore-options" | jq '[.targets[].hostname], [.ineligible[] | {hostname, reason}], .attachHint'`
   → `["db1","db2"]` (db1 с `sourceAgent: true`); ineligible
   `[{"hostname":"db3","reason":"agent_outdated"}]`; `attachHint.command` —
   `sudo sard-agent repo add s1 <адрес хранилища> --password-from-file <файл с копией пароля>`
   без адреса S3 и ключей.
6. `… | jq '.targets[0].defaults'` → `{"destination":"qa%20files/<время снимка YYYYMMDDTHHMMSSZ>"}`.
7. Предпросмотр:
   `$C -X POST "$API/sources/$SF/snapshots/$SNAP/restores/preview" -d "{\"agentId\":\"$A\",\"config\":{\"destination\":\"qa1\"}}"`
   → `200`, `target` `/var/lib/sard-agent/restore/qa1`, `warnings`
   `["owners_not_restored"]`; `$C "$API/runs?kind=restore" | jq '.items | length'` → `0`.
8. Отказы (каждый — запусков восстановления по-прежнему `0`):
   `config` `{"destination":"../etc"}` → `422`, ошибка у `config/destination`;
   `{"destination":"x","overwrite":true}` → `422` у `config`;
   `agentId` `$OLD` → `409 unknown_plugin`;
   `agentId` случайный UUID → `422` у `agentId`;
   без cookie → `401`; с `-H 'Origin: https://evil.example'` → `403 origin_rejected`.
9. Неполный снимок: на ВМ-1 `sudo chmod 000 /srv/qa-data/sub/b.txt`, бэкап
   `qa files` из консоли → `failed`, снимок `partial`; `restorable false`,
   `restoreBlockedReason partial`; POST restores этого снимка → `422 snapshot_not_restorable`
   (ВВ8). Вернуть права.

## Часть 3. Консоль (дважды: на моках `VITE_API_MOCKS=1` и против сервера)

10. Карточка `qa files` → у снимка кнопка «Восстановить»; у неполного снимка
    шага 9 — причина «Снимок неполный…», кнопки нет.
11. Страница запуска бэкапа `qa files` (succeeded) → кнопка «Восстановить».
12. Страница восстановления: имя `qa files`, id 8 символов моноширинным, время,
    объём; агенты `db1 (исходный)`, `db2`; `db3` отдельно с текстом
    «Агент не умеет восстанавливать из консоли: обновите sard-agent.» и не
    выбирается; рядом с `destination` — `/var/lib/sard-agent/restore`.
13. Выбрать `db2` → `destination` снова из defaults `db2`. Вернуть `db1`.
14. «Проверить» → итоговый путь и предупреждение о владельцах; «Запустить
    восстановление» видна. Изменить `paths` → кнопка запуска исчезла до новой
    проверки.
15. В `destination` ввести `../etc`, «Проверить» → ошибка под полем.
16. Переключить язык на EN → на странице нет русского текста, кроме данных.
17. Без агентов: отозвать db1 и db2 на копии стенда (или мок с пустым
    `targets`) → объяснение и команда `attachHint` с кнопкой копирования,
    кнопок «Проверить» и «Запустить» нет.

## Часть 4. Восстановление files

18. ВМ-1: `sudo rm -rf /srv/qa-data/sub`. Консоль: восстановить последний
    снимок `qa files` на db1 с назначением по умолчанию → страница запуска,
    тип «Восстановление», «Запустил admin», фаза `restoring` с файлами и байтами,
    затем `succeeded`; итог — путь, «N файлов», объём, пояснение о владельцах.
19. ВМ-1: `P=<путь из итога>`; `sudo diff -r /srv/qa-data "$P/srv/qa-data"` →
    различие только в `sub` (удалён на месте); `sudo cmp <копия b.txt> "$P/srv/qa-data/sub/b.txt"`
    → совпадают (копию b.txt снять до шага 18);
    `sudo stat -c '%U %a %Y' "$P/srv/qa-data/a.txt"` → `sard-agent 640 <то же время>` (ВВ3);
    `sudo stat -c '%U %a' /var/lib/sard-agent/restore` → `sard-agent 700`.
20. Telegram → одно сообщение «✅ Восстановление выполнено: qa files», строки
    `Агент: db1`, `Снимок: <8> от …`, `Восстановлено файлов: …, объём: …. Куда: <P>`, ссылка.
21. Повторить шаг 18 с тем же назначением → запуск `failed`, сообщение
    `TARGET_NOT_EMPTY: …` с путём `P`; в `P` ничего не изменилось
    (`sudo find "$P" -newer <метка до шага> | wc -l` → `0`).
22. Часть путей: назначение `qa-part`, `paths` `/srv/qa-data/sub` →
    `succeeded`; `sudo find /var/lib/sard-agent/restore/qa-part -type f` →
    только `…/srv/qa-data/sub/b.txt`. `paths` `/srv/nothing` → `failed`,
    `PATH_NOT_IN_SNAPSHOT`; каталога `qa-nothing` нет.
23. Ссылка: `sudo -u sard-agent ln -s /etc /var/lib/sard-agent/restore/evil`; <!-- allow-sudo-u: ссылка от пользователя службы для проверки отказа -->
    назначение `evil/x` → `failed`, `TARGET_INVALID`, называет `…/evil`;
    `ls /etc/x` → нет. Удалить ссылку.
24. Другой агент: восстановить тот же снимок на `db2` → `succeeded`; на ВМ-2
    содержимое совпадает с ВМ-1 (`sha256sum`).
25. Данные мимо сервера: на машине сервера запомнить `docker stats --no-stream`
    (NET I/O контейнера сервера), восстановить `qa-big` (4 ГБ) на db2 →
    `succeeded`; прирост NET I/O сервера меньше 10 МБ.
26. Офлайн: `sudo systemctl stop sard-agent` на ВМ-2; восстановить на db2 →
    предпросмотр предупреждает «Агент не в сети…»; запуск `queued` с пояснением
    «начнётся при подключении агента»; `sudo systemctl start sard-agent` →
    запуск уходит в `running` и `succeeded` (ВВ10).
27. Потеря: во время восстановления `qa-big` на db2 `sudo systemctl kill -s KILL sard-agent`
    на ВМ-2 и не запускать дольше окна потери → шаг `lost`, запуск `failed`,
    «agent lost the step»; Telegram — «❌ Восстановление потеряно…» со строкой о
    повторе. На ВМ-2 запустить службу. В консоли «Повторить» → страница
    восстановления с тем же агентом и назначением; «Проверить», «Запустить» →
    `failed`, `TARGET_NOT_EMPTY` (назначение с частичными данными); сменить
    назначение → `succeeded`.
28. Одновременно с бэкапом: запустить бэкап `qa-big`, сразу восстановление
    `qa files` → оба запроса `201`; оба запуска завершаются (на агенте с
    `max_parallel: 1` — по очереди). Пока идёт восстановление, кнопка бэкапа
    источника доступна, `POST runs` → `201`.
29. Нет места: том 64 МБ (`truncate`, `mkfs.ext4`, `mount -o loop` на
    `/var/lib/sard-agent/restore/small`, `chown sard-agent`), восстановить
    `qa-big` в `small/x` → `failed`, `TARGET_WRITE`; страница запуска говорит,
    что частично восстановленные данные не удалены, с путём; `sudo ls /var/lib/sard-agent/restore/small/x`
    → файлы есть. Telegram — строка «Частично восстановленные данные не
    удалены: …». Отмонтировать.
30. Хранилище пропало: на ВМ-2 `sudo iptables -I OUTPUT -p tcp -d <адрес S3> -j REJECT`,
    восстановить `qa files` на db2 → `failed`, `BACKEND_UNAVAILABLE`; сервер
    при этом онлайн. Убрать правило.
31. Удалённый источник (ВВ9): создать и забэкапить источник `qa-del`, удалить
    его; в списке запусков открыть его успешный бэкап → «Восстановить» →
    `succeeded`.
32. Отмена: на странице идущего восстановления кнопки отмены нет.

## Часть 5. PostgreSQL (R2b)

33. Восстановить последний снимок `qa-pg` на db1: форма заполнена `host`,
    `user backup`, `password_ref pg`, `database app_restored_<дата>`,
    `restore_globals` выключен. Запуск → `failed`, `CREATEDB_REQUIRED`, роль
    `backup` (у неё нет CREATEDB); базы `app_restored_…` нет
    (`psql -lqt | cut -d'|' -f1 | grep -c app_restored` → `0`).
34. Сменить `user restorer`, `password_ref pg-restore` → `succeeded`; итог —
    имя базы `app_restored_<дата>` и число таблиц `2`; `psql -d app_restored_<дата> -tAc 'select count(*) from t1'`
    → `1000`; содержимое совпадает: `psql -d app -c '\copy t1 to stdout' | sha256sum`
    и то же для новой базы → равны; владелец `t1` — `restorer` (`--no-owner`).
35. Повторить шаг 34 → `failed`, `DATABASE_EXISTS`, называет базу; база не
    изменилась (число строк то же).
36. Версии: целевой `host pg14` → `failed`, `PG_SERVER_TOO_OLD`, называет 14 и
    16 (ВВ5); на PG-14 базы нет.
37. Глобальные объекты: `restore_globals` включить при роли `restorer` →
    предпросмотр предупреждает об изменении кластера; запуск → `failed`,
    `SUPERUSER_REQUIRED`. Со второй, пустой копией кластера (`pg_createcluster 16 qa --start`,
    порт 5433, секрет `pg-super`) → `succeeded`; роль `app_rw` есть и владеет
    `t1` в новой базе; лог шага — строки WARN «already exists», итог называет
    их число (ВВ6).
38. Сбой посреди: большая база (`pgbench -i -s 200 app`, бэкап), восстановление,
    через 5 с остановить целевой кластер → `failed`; страница говорит, что база
    `…` создана и не удалена; после запуска кластера база есть.
39. Дамп не на диске: во время шага 34 `sudo inotifywait -m -r /var/lib/sard-agent /tmp -e create`
    → ни одного файла дампа.
40. Нет клиента: на ВМ-2 без пакета `postgresql-client` восстановить `qa-pg` на
    db2 → `failed`, `PG_CLIENT_MISSING`, называет пакет.
41. Telegram (en на копии стенда) → «Database: app_restored_…, tables: 2».

## Часть 6. Секреты и утечки

42. `grep -cF "$REPO_PASS"` по: логам шагов восстановления
    (`$C "$API/runs/<id>/steps/<stepId>/logs"` для всех запусков частей 4–5),
    `docker compose logs sard-server`, `journalctl -u sard-agent` на ВМ-1 и
    ВМ-2 → `0`. То же для паролей ролей `restorer` и `postgres`.
43. Во время шага 34 `ps -eo args | grep -cF '<пароль restorer>'` (несколько
    раз) → `0`.
44. DevTools браузера, вкладка Network, все запросы страниц восстановления →
    ни в одном теле нет пароля репозитория, адреса и ключей S3, паролей баз.

## Часть 7. Документация

45. `docs/operations/agent-host-setup.md` — раздел о восстановлении из консоли:
    `restore.dir`, `/var/lib/sard-agent/restore`, `ReadWritePaths` для другого
    каталога, владельцы не переносятся, перенос `sudo cp -a` / `rsync -a` и
    `restorecon -R`. `docs/plugins/postgresql.md` — роль для восстановления
    (`CREATEDB`, суперпользователь для глобальных объектов), клиент
    PostgreSQL на целевом хосте не старше версии `pg_dump` снимка.
46. Пройти шаги 18 и 34 по документации, не заглядывая в эту процедуру →
    результат тот же.

Вручную не воспроизводятся и проверяются тестами с теми же названиями:
`TARGET_BUSY` при двух командах в одно назначение, база, созданная другим между
проверкой и `CREATE DATABASE`, имя базы с кавычкой, таймаут шага,
`RESTORE_INCOMPLETE`, повторно присланный `command_id`, укорачивание длинного
имени базы по умолчанию, вывод бэкапа в шаге восстановления.
