<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# QA: шаг FORGET и нижний предел хранения на хосте (L1a)

Сценарии: `docs/specs/agent/retention-forget.feature` и
`docs/specs/proto/retention-forget.feature` (черновик, ждёт утверждения
владельцем). Здесь вручную проходятся сценарии с тегом `@qa`; остальные
проверяют тесты `@start`, `@register`, `@unit`, `@restic`, `@wire`, `@buf` с теми
же названиями.

Ожидаемый результат указан после «→» в каждом шаге. Любое расхождение — дефект.

Части:
- **Часть 1 — предел в конфиге агента** выполнима после L1a.
- **Часть 2 — бэкап ждёт исключительную блокировку (OQ-156)** выполнима после
  L1a: бэкап запускается существующим `POST /api/v1/sources/{id}/runs`.
- **Часть 3 — шаг FORGET** выполнима только после L1b: шаг FORGET агенту
  отправляет сервер, в L1a его не отправляет никто. До L1b эти сценарии
  проверяют только тесты. Способ запуска forget (кнопка или запрос REST)
  дописывает спецификация L1b; проверки результата по `restic` ниже от него не
  зависят.

## Подготовка

Нужны: Docker, Go, `restic` 0.19.1 (`.bin/restic` после `make tools`), `curl`,
`jq`. Команды — из корня репозитория.

1. Выполнить «Подготовку» и часть 2 из `docs/qa/agent-enroll.md` (сервер
   поднят, агент зарегистрирован; переменные `AG`, `H`, `QA`, `DC`).
2. Выполнить «Подготовку» из `docs/qa/admin-login.md` (переменные `API`,
   `PW`, функция `login`); `login $PW $QA/jf >/dev/null`.
3. Локальный репозиторий `qa` и функция `rs` — как шаг 3 «Подготовки»
   `docs/qa/files-plugin.md`. → `rs snapshots` печатает пустой список.
4. Функция засева снимков источника с заданным временем:

```bash
seed() { # seed <source> <run> <YYYY-MM-DD HH:MM:SS> [доп. метки через запятую]
  d=$(mktemp -d); echo "$3" > "$d/f"
  rs backup --time "$3" --tag "sard.source=$1,sard.run=$2,sard.step=st-$2${4:+,$4}" "$d" --json \
    | jq -r 'select(.message_type=="summary").snapshot_id'
  rm -rf "$d"
}
count() { rs snapshots --tag "sard.source=$1" --json | jq length; }
```

## Часть 1 — предел в конфиге агента

5. Остановить агента. В `$H/agent.yaml` нет раздела `retention_floor`.
   Запустить агента. → В журнале агента строка
   `retention floor repository=qa keep_last=7 keep_within_days=14 origin=built_in`.
6. Остановить агента. Дописать в `$H/agent.yaml`:

```yaml
retention_floor:
  repositories:
    qa:
      disabled: true
```

   Запустить агента. → В журнале
   `retention floor repository=qa disabled origin=config_repository`; агент
   подключается к серверу.
7. Заменить `disabled: true` на `keep_last: 0` и `keep_within_days: 0`.
   Запустить агента. → Код выхода 1; stderr называет
   `retention_floor.repositories.qa` и содержит `disabled: true`; к серверу
   агент не подключается.
8. Удалить раздел `retention_floor` из `$H/agent.yaml`. Создать
   `$H/agent.d/zz-floor.yaml` (права и владелец — как у фрагментов `repo add`)
   с содержимым:

```yaml
retention_floor:
  default:
    keep_last: 1
```

   Запустить агента. → Код выхода 1; stderr называет файл
   `agent.d/zz-floor.yaml` и ключ `retention_floor`. Удалить файл.
9. Запустить агента без раздела `retention_floor`. Открыть агента в консоли
   или `GET $API/agents/<id>` (сессия из шага 2). → Агент онлайн; список
   репозиториев агента содержит `qa` (поле предела REST появится в L1b, здесь
   не проверяется).

## Часть 2 — бэкап ждёт исключительную блокировку (OQ-156)

10. Создать источник `files` на этом агенте с репозиторием `qa` и каталогом
    `$QA/data` (как часть 2 `docs/qa/files-plugin.md`); `S=<id источника>`.
11. Взять исключительную блокировку и приостановить держащий её процесс, как в
    шаге 123 `docs/qa/files-plugin.md`:
    `seed X x1 "2026-11-01 10:00:00" >/dev/null; rs forget --keep-last 1 --tag sard.source=X >/dev/null; rs prune >/dev/null 2>&1 & P=$!; sleep 2; kill -STOP $P`.
    → `rs list locks --no-lock | wc -l` печатает `1`.
12. Запустить бэкап `POST $API/sources/$S/runs`; через 20 секунд
    `kill -CONT $P; wait $P`.
    → Запуск завершается `succeeded`, а не `failed` с
    `repository is locked`; время шага бэкапа не меньше 20 секунд.

## Часть 3 — шаг FORGET (после L1b)

Перед каждым шагом этой части: `rs snapshots --json | jq -r '.[].id' | xargs -r rs forget >/dev/null` → `rs snapshots --json | jq length` печатает `0`.
Источник `S1` — источник на этом агенте с репозиторием `qa`; `S1` в функциях
ниже — его id.

13. Предел встроенный (раздела `retention_floor` нет). Засеять 20 недельных
    снимков: `for i in $(seq 1 20); do seed S1 r$i "$(date -d "2026-11-30 10:00 -$(( (20-i)*7 )) days" '+%F %T')"; done >/dev/null`.
    Отправить FORGET источника `S1` с политикой «keep_last 1».
    → Запуск forget `succeeded`; `count S1` печатает `7`; в логе шага у шести
    снимков стоят слова `kept by host retention floor`.
14. Предел выключен (шаг 6), репозиторий пуст. Засеять 3 основных снимка
    `S1` по дням и к каждому отпечаток:
    `m=$(seed S1 rA "2026-11-28 10:00:00"); seed S1 rA "2026-11-28 10:00:01" "files.part=fingerprint,files.main_snapshot=$m" >/dev/null`
    (`seed` печатает полный id снимка)
    (то же для `rB` 29-го и `rC` 30-го). Отправить FORGET `S1` с «keep_last 1».
    → `count S1` печатает `2`: основной снимок `rC` и его отпечаток.
15. Предел выключен. Засеять 5 снимков `S1` и 5 снимков `S2`, плюс снимок без
    меток `rs backup $QA/data`. Отправить FORGET `S1` с «keep_last 1».
    → `count S1` печатает `1`; `count S2` печатает `5`; снимок без меток на
    месте (`rs snapshots --json | jq '[.[]|select(.tags==null)]|length'` → `1`).
16. Предел встроенный. Засеять 30 ежедневных снимков `S1`. Отправить FORGET
    `S1` с политикой, в которой все поля нулевые (как это позволит L1b; если
    консоль и REST такую политику не принимают — шаг пропускается, его
    проверяет тест). → Итог `rejected`, `retention policy is empty`;
    `count S1` печатает `30`.
17. Предел встроенный, 30 ежедневных снимков `S1` с отпечатками. Отправить
    FORGET `S1` с «keep_last 1». → Остались основные снимки за последние 14
    дней и их отпечатки; ни одного отпечатка без основного снимка.
18. Устаревшая блокировка: `rs backup $QA/data & P=$!; sleep 1; kill -9 $P`
    (блокировка остаётся). Отправить FORGET `S1` с «keep_last 1».
    → `succeeded`; в логе шага — снятие устаревшей блокировки; среди строк
    лога нет `--remove-all`.
19. Учётка без права удаления (только для стенда S3 из ADR 0047): подключить
    репозиторий ключом без `DeleteObject` вне `locks/`, засеять 30 снимков,
    отправить FORGET. → `failed`, в итоге
    `storage account has no delete permission`; `count S1` печатает `30`.
20. Отказ поддельного шага: отправить агенту шаг FORGET с непустым
    `snapshot_id` (стенд e2e с подменённым сервером, T3; вручную через консоль
    невозможно — это и есть свойство). → `rejected`,
    `forget takes no snapshot id`; список снимков не изменился.
