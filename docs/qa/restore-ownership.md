<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# QA: владение восстановлением (R2c) — дополнение к `console-restore.md`

Сценарии: `docs/specs/agent/restore-ownership.feature`; ADR 0066.

**ПРЕДЛОЖЕНО, ждёт одобрения владельца** вместе со спецификацией. R2c — рефакторинг:
наблюдаемое поведение R2b не меняется. Поэтому основа процедуры — повтор шагов
`docs/qa/console-restore.md` и `docs/qa/repo-restore.md` на новом агенте со
сравнением текстов с эталоном, снятым на агенте R2b; новое — каталог журнала
восстановления, обновление с прерванным восстановлением и доля таблицы блокировок.
Ожидаемый результат — после «→». Любое расхождение — дефект.

Части 1 и 2 выполнимы после R2c-1, части 3–5 — после R2c-2, часть 6 — после R2c-3.

## Стенды

Как в `docs/qa/console-restore.md` (сервер, ВМ-1 `db1` с PostgreSQL 16, кластеры
5433 и 5434, источники `qa files` и `qa-pg`), плюс:

- пакет агента R2b (`sard-agent_<версия R2b>`) и пакет агента текущей сборки;
- **копия ВМ-1** для части 4 (там агента убивают посреди восстановления).

Переменные — как в `console-restore.md`, плюс:

```bash
J=/var/lib/sard-agent/executor/restore-journal
OLDJ=/var/lib/sard-agent/executor/pg-restore
msg() { $C "$API/runs/$1" | jq -r '.steps[] | .message'; }   # сообщение шага запуска
```

## Часть 0. Эталон на агенте R2b

0. На ВМ-1 с агентом R2b выполнить шаги `console-restore.md` 18, 21, 22, 23, 33,
   34, 35, 36, 37, 37а, 37б, 40 и для каждого запуска сохранить `msg <id>` в
   `~/qa-r2b/<номер шага>.txt` → файлы есть. Удалить созданные базы и назначения.
1. Там же: `sudo sard-agent repo restore s1 <снимок qa-pg> --target /srv/qa-pgout-r2b`
   (как `docs/qa/repo-restore.md`, часть PostgreSQL), сохранить stdout в
   `~/qa-r2b/repo-restore-pg.txt` и `ls -l /srv/qa-pgout-r2b` в
   `~/qa-r2b/repo-restore-pg.ls`.

## Часть 1. Тексты не изменились (после каждого этапа)

2. Установить агента текущей сборки (`sudo apt install ./sard-agent_….deb`),
   `systemctl is-active sard-agent` → `active`.
3. Повторить шаги части 0 и сравнить: `diff <(sed -E 's/[0-9a-f]{8,64}/ID/g; s/[0-9]{8}T[0-9]{6}Z/TIME/g' ~/qa-r2b/<шаг>.txt) <(msg <id> | sed -E '…то же…')`
   → пусто для каждого шага (те же причина, стадия, текст и `leftover`).
4. Шаг 40 (нет клиента PostgreSQL): последняя строка сообщения — «run `sudo
   sard-agent check` on the host» → как в эталоне.

## Часть 2. files выкладывает агент (R2c-1)

5. Шаг 27а `console-restore.md` (inotifywait при восстановлении `qa files`) →
   тот же порядок: `.sard-staging/<id шага>`, `root`, метка, один `moved_to`
   подкаталога `root`; после шага `.sard-staging` пуст.
6. `sudo ls -A $J 2>/dev/null` после шага 5 → пусто или «нет такого каталога»
   (files журнал не пишет).

## Часть 3. Журнал восстановления PostgreSQL (R2c-2)

7. Большая база (как шаг 38 `console-restore.md`: `pgbench -i -s 200 app`, бэкап),
   восстановление с `restore_globals` в пустой кластер 5434; во время `pg_restore`:
   `sudo ls -ln $J` → один файл `<id шага>.json`, `-rw-------`, владелец —
   пользователь службы; `sudo stat -c '%a %U' $J` → `700 sard-agent`.
8. Там же: `sudo jq -r '.plugin, .command_id' $J/<id шага>.json` → `postgresql` и
   id шага; `sudo jq -r .entry $J/<id шага>.json | base64 -d | jq .` → имя базы с
   OID, роли с OID, `password_ref` `pg-super`; `sudo grep -cF '<пароль postgres>' $J/<id шага>.json`
   → `0`, и то же после `base64 -d` → `0`.
9. После `succeeded`: `sudo ls -A $J` → пусто; `sudo ls -A $OLDJ 2>/dev/null` →
   пусто или «нет такого каталога».
10. Доля таблицы блокировок (РВ-7): шаг 36а `console-restore.md` целиком → те же
    строки `pg_restore mode: …` с `share 0.5`, затем `share 0`; `postgresql_lock_share: 2`
    → служба не стартует, журнал называет `restore.postgresql_lock_share`.
    Дополнительно `postgresql_lock_share: 0.25` → строка с `share 0.25`. Убрать настройку.
11. Перезапуск посреди (шаг 38а `console-restore.md`) на агенте текущей сборки →
    тот же исход; до первого шага журнал агента пишет об удалении базы и ролей;
    `sudo ls -A $J` → пусто.

## Часть 4. Обновление с прерванным восстановлением (R2c-2, копия ВМ-1)

12. Установить агента R2b. Начать восстановление большой базы (шаг 7) и во время
    `pg_restore` `sudo systemctl kill -s KILL sard-agent`, службу не запускать
    → `sudo ls $OLDJ` → `<id шага>.json`; база `app_restored_…` есть на 5434.
13. Установить пакет текущей сборки, `sudo systemctl start sard-agent` → до
    приёма первого шага `journalctl -u sard-agent` пишет об удалении базы и ролей
    по журналу отката; запуск в консоли `failed` с «interrupted: agent restarted
    before the step finished» и «nothing was restored»; на 5434 нет новой базы и
    ролей из снимка; `sudo ls -A $OLDJ` → пусто; `sudo ls -A $J` → пусто.
14. Вариант «завершено»: на агенте R2b повторить шаг 38в `console-restore.md`
    (убить сразу после конца `pg_restore`), установить текущую сборку и
    запустить → запуск `succeeded`, «agent restarted after the restore completed»,
    база на месте с теми же строками; `$OLDJ` пуст. Если шаг R2b успел
    завершиться сам — отметить вариант как проверенный только тестом `@upgrade`.
15. Вариант «неудачный откат»: на агенте R2b восстановить большую базу без
    `restore_globals` в основной кластер от роли `restorer` (`password_ref
    pg-restore`, как шаг 34) и во время `pg_restore` убить агента, как в шаге 12;
    до запуска новой сборки выполнить `ALTER DATABASE <новая база> OWNER TO postgres`
    (роль `restorer` её теперь не удалит); установить текущую сборку и запустить →
    запуск `failed`, `CLEANUP_FAILED`, `leftover` называет базу;
    `sudo ls $OLDJ` → запись на месте. Удалить базу вручную, перезапустить службу
    → запись удалена, строк ERROR о журнале нет.

## Часть 5. Секреты (R2c-2)

16. Шаги 42–43 `console-restore.md` для запусков частей 3 и 4 → `0` совпадений;
    дополнительно `sudo grep -rcF '<пароль restorer>' $J $OLDJ 2>/dev/null` → `0`.

## Часть 6. repo restore и архитектура (R2c-3)

17. `sudo sard-agent repo restore s1 <снимок qa-pg> --target /srv/qa-pgout` →
    `diff <(sed -E 's/qa-pgout-r2b/qa-pgout/g' ~/qa-r2b/repo-restore-pg.txt) <(stdout)`
    → пусто; набор файлов, права и владельцы `ls -l /srv/qa-pgout` — как в
    `~/qa-r2b/repo-restore-pg.ls`; напечатанные команды восстанавливают базу
    (шаг «Напечатанные команды…» `repo-restore.md`).
18. `sudo sard-agent repo restore s1 latest --source <id qa-pg> --target /srv/qa-latest`
    → восстановлен снимок базы, а не более новый снимок глобальных объектов (как
    в эталоне R2b).
19. `sudo sard-agent check` на ВМ-1 и на ВМ-2 без клиента PostgreSQL → строка
    `plugin.programs` плагина postgresql та же, что у агента R2b (В1).
20. В корне репозитория: `cd agent && go test ./internal/ ./plugins/...` → `ok`;
    `grep -rnE '"(postgresql\.|PG_|pg_restore)' internal --include='*.go' --exclude='*_test.go'`
    → пусто.

Вручную не воспроизводятся и проверяются тестами с теми же названиями: пределы
размера и числа записей журнала, сбои fsync и rename при сохранении записи,
одновременные записи двух команд, код причины вне образца, остановка потока
собственным контекстом плагина, испорченный конверт, запись плагина, которого нет
в агенте, зависший разбор при старте, запись одной команды в двух журналах,
архитектурный тест на подложенных нарушениях.
