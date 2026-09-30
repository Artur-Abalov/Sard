# QA: плагин `files` агента (A6b)

Сценарии: `docs/specs/agent/files-plugin.feature` (решения владельца по
OQ-040…OQ-045 приняты 2026-09-30; Ф5 изменено: RESTORE объявлен, но пустой). Здесь вручную
проходятся сценарии с тегом `@qa`; остальные проверяют тесты `@unit`,
`@restic`, `@register`, `@doc` с теми же названиями.

Ожидаемый результат указан после «→» в каждом шаге. Любое расхождение — дефект.

Части:
- **Часть 1 — Register и схема** выполнима после A6b.
- **Часть 2 — шаг BACKUP** выполнима только после S6a (запуск прогона
  `POST /api/v1/sources/{id}/runs` отправляет RunStep агенту) и S7 (приём
  результата); до этого бэкап и пустое восстановление проверяют только тесты
  (Ф18, OQ-045). Отображение снимка у FAILED-шага в REST — S7/S8 (OQ-046). Сквозной
  автоматический вариант — T2b.
- **Часть 3 — документация** выполнима после A6b.

## Подготовка

Нужны: Docker, Go, `restic` 0.19.1 (`.bin/restic` после `make tools` или из
пакета агента), `curl`, `jq`, `setfacl`. Команды — из корня репозитория, **не
от root**: от root права `0000` не мешают чтению, и шаги с `chmod` не
показательны.

1. Выполнить «Подготовку» и часть 2 из `docs/qa/agent-enroll.md` (сервер
   поднят, агент зарегистрирован; переменные `AG`, `H`, `QA`, `DC`, `PSQL`).
2. Выполнить «Подготовку» из `docs/qa/admin-login.md` (переменные `API`,
   `PW`, функция `login`); `login $PW $QA/jf >/dev/null`.
3. Локальный репозиторий и конфиг агента:

```bash
RESTIC=$PWD/.bin/restic
R=$QA/repo; head -c 32 /dev/urandom | base64 > $QA/repo.pass; chmod 0600 $QA/repo.pass
RESTIC_PASSWORD_FILE=$QA/repo.pass $RESTIC init -r $R
RID=$(RESTIC_PASSWORD_FILE=$QA/repo.pass $RESTIC -r $R cat config | jq -r .id)
cat >> "$H/agent.yaml" <<EOF
repositories:
  - name: qa
    url: $R
    password_file: $QA/repo.pass
executor:
  state_dir: $H/state
EOF
rs() { RESTIC_PASSWORD_FILE=$QA/repo.pass $RESTIC -r $R "$@"; }   # restic по репозиторию qa
S=QA-MARKER-5c1e
D=$QA/tree; mkdir -p $D/sub
head -c 1000 /dev/zero | tr '\0' x > $D/a.txt; printf '%s' "$S" >> $D/a.txt
printf '%-24s' "$S" > $D/sub/b.txt
"$AG" --config "$H/agent.yaml" > $QA/agent.out 2>&1 &
AGPID=$!
AGENT=$($PSQL "select id from agents order by created_at desc limit 1")
```

   → агент в `$QA/agent.out` печатает `connecting to localhost:9090` и не
   завершается.

## Часть 1. Register и схема

4. `$PSQL "select array_to_string(actions, ',') from agent_plugins where agent_id='$AGENT' and name='files'"`
   → ровно `backup,restore`.
5. `$PSQL "select version from agent_plugins where agent_id='$AGENT' and name='files'"`
   → совпадает с `"$AG" --version`.
6. `$PSQL "select config_schema from agent_plugins where agent_id='$AGENT' and name='files'" > $QA/schema.json; wc -c < $QA/schema.json`
   → число не больше `65536`.
7. `jq '.properties | to_entries[] | {k: .key, t: .value.title, d: .value.description, e: .value.examples}' $QA/schema.json`
   → для `paths`, `exclude`, `one_file_system` у каждого непустые `t`, `d`
   и непустой массив `e`; `d` у `exclude` содержит ссылку на документацию
   restic об исключениях.
8. `jq '.properties | to_entries[] | {k: .key, ru: .value["x-sard-i18n"].ru}' $QA/schema.json`
   (Ф3, OQ-042) → у каждого поля непустые `ru.title` и
   `ru.description`, текст на русском.

## Часть 2. Шаг BACKUP (после S6a и S7)

Вспомогательные функции:

```bash
# src <json-конфиг> — создаёт источник files и печатает его id
src() { curl -sS -b $QA/jf -X POST $API/sources -H 'Content-Type: application/json' \
  -d "{\"name\":\"qa-$RANDOM\",\"agentId\":\"$AGENT\",\"plugin\":\"files\",\"repositoryName\":\"qa\",\"config\":$1}" | jq -r .id; }
# run <source-id> — запускает прогон и ждёт его окончания, печатает шаг
run() { RUN=$(curl -sS -b $QA/jf -X POST $API/sources/$1/runs | jq -r .id)
  for i in $(seq 1 120); do st=$(curl -sS -b $QA/jf $API/runs/$RUN | jq -r .status)
    case $st in queued|running) sleep 1;; *) break;; esac; done
  curl -sS -b $QA/jf $API/runs/$RUN | jq '.steps[0] | {status, phase, message, backup}'; }
snaps() { rs snapshots --json | jq length; }
```

9. **Успешный бэкап.** `N0=$(snaps); run $(src "{\"paths\":[\"$D\"]}")`
   → `status` `succeeded`, `message` пустой или `null`; `backup.totalBytes`
   = `1024`, `backup.addedBytes` > 0; `snaps` = `N0+1`;
   `rs snapshots --json | jq -r '.[-1].id'` совпадает с `backup.snapshotId`;
   `rs ls latest | grep -c "$D/sub/b.txt"` → `1`; метки
   `rs snapshots --json | jq -r '.[-1].tags|join(",")'` содержат `run=` и
   `source=`.
10. **Пустой каталог.** `mkdir $QA/empty; run $(src "{\"paths\":[\"$QA/empty\"]}")`
    → `succeeded`; `backup.totalBytes` = `0`; снимок есть в `rs snapshots`.
11. **Исключения.** `touch $D/app.log $D/sub/c.log; run $(src "{\"paths\":[\"$D\"],\"exclude\":[\"*.log\"]}")`
    → `succeeded`; `rs ls latest | grep -c '\.log$'` → `0`;
    `rs ls latest | grep -c a.txt` → `1`.
12. **Отсутствующий путь.** `N0=$(snaps); run $(src "{\"paths\":[\"$D\",\"$QA/missing\"]}")`
    → `failed`; `message` называет `$QA/missing` и `no such file or directory`
    и не называет `$D`; `backup` — `null`; `phase` — `preparing`;
    `snaps` = `N0`; `rs list locks | wc -l` → `0`.
13. **Путь без права чтения.** `mkdir $QA/locked; chmod 0000 $QA/locked; run $(src "{\"paths\":[\"$QA/locked\"]}")`
    → `failed`; `message` называет `$QA/locked` и `permission denied`;
    `backup` — `null`. После шага `chmod 0700 $QA/locked`.
14. **Нечитаемые файлы внутри дерева.** `mkdir -p $D/bad; for i in $(seq 1 11); do printf '%s' "$S" > $D/bad/f$i; chmod 0000 $D/bad/f$i; done; N0=$(snaps); run $(src "{\"paths\":[\"$D\"]}")`
    → `failed`; `message` содержит `11`, называет ровно 10 путей
    `$D/bad/f…` и не содержит `$S`; `snaps` = `N0+1` (снимок сохранён);
    `backup.snapshotId` непустой и равен `rs snapshots --json | jq -r '.[-1].id'`
    — эта проверка выполнима только после правки REST в S7/S8 (OQ-046).
    После шага `chmod 0600 $D/bad/*`.
15. **Симлинк внутри дерева.** `printf '%s' "$S-link" > $QA/outside; ln -s $QA/outside $D/link; run $(src "{\"paths\":[\"$D\"]}")`
    → `succeeded`; `rs ls -l latest | grep "$D/link"` — строка с типом `l`
    и целью `$QA/outside`; `rs ls latest | grep -c "$QA/outside"` → `0`
    (цель вне дерева в снимок не попала).
16. **Указанный путь — симлинк** (Ф9). `ln -s $D $QA/tree-link; run $(src "{\"paths\":[\"$QA/tree-link\"]}")`
    → `failed`; `message` называет `$QA/tree-link` и `$D`; `backup` — `null`.
17. **Заблокированный репозиторий** (Ф10). Выполнять после шагов 18–19
    (нужны данные в репозитории). Удержать исключительную блокировку,
    приостановив `prune` после того, как он её взял:
    `rs forget --keep-last 1 >/dev/null; rs prune >/dev/null 2>&1 & P=$!; sleep 2; kill -STOP $P; rs list locks --no-lock | wc -l`
    → `1` (если `0` — `prune` успел закончиться: повторить с большим деревом).
    Затем `date +%s > $QA/t0; run $(src "{\"paths\":[\"$D\"]}"); echo $(( $(date +%s) - $(cat $QA/t0) ))`
    → шаг `failed` не раньше чем через 300 с после запуска; `message`
    называет репозиторий `qa` и блокировку; `backup` — `null`.
    Освободить: `kill -CONT $P; wait $P`.
    Повтор с освобождением во время ожидания: снова приостановить `prune`,
    запустить прогон, через 30 с выполнить `kill -CONT $P` → шаг `succeeded`.
18. **Отмена.** Создать дерево на 2 ГиБ: `mkdir $QA/big; for i in 1 2; do head -c 1G /dev/urandom > $QA/big/f$i; done`;
    запустить прогон `src "{\"paths\":[\"$QA/big\"]}"` и, когда шаг в фазе
    `uploading`, отменить прогон через консоль или API отмены (S8).
    → шаг `cancelled`; `snaps` не изменилось; `rs list locks | wc -l` → `0`;
    `pgrep -f "restic.*backup"` — пусто.
19. **Таймаут.** Как шаг 18, но без отмены и с таймаутом шага 2 с (настройка
    таймаута — S6a) → шаг `timed_out`; проверки как в шаге 18.
20. **Содержимое не утекает.** `curl -sS -b $QA/jf "$API/runs?limit=100" | jq -r '.items[].message' | grep -cF "$S"` → `0`;
    для каждого прогона части 2 (id шага — `curl -sS -b $QA/jf $API/runs/$RUN | jq -r '.steps[0].id'`)
    `curl -sS -b $QA/jf "$API/runs/$RUN/steps/<step-id>/logs" | grep -cF "$S"` → `0`.
20а. **Пустое восстановление** (Ф5; выполнимо, когда сервер умеет отправить
    агенту шаг RESTORE для источника files). Перед шагом
    `find $H/state | sort > $QA/state0`; отправить RESTORE снимка из шага 9
    → шаг `failed`; `message` говорит, что восстановление для плагина files
    ещё не реализовано; `pgrep -f 'restic.*restore'` во время шага — пусто;
    `find $H/state | sort | diff $QA/state0 -` показывает не больше чем файл
    сохранённого результата шага, каталога `$H/state/restore/<id шага>` нет.

## Часть 3. Документация

21. `test -f docs/plugins/files.md && echo ok` → `ok`.
22. Прочитать `docs/plugins/files.md` → называет пользователя `sard-agent` и
    то, что агент не root; даёт три способа выдать чтение: группа,
    `setfacl -R -m u:sard-agent:rX` вместе с ACL по умолчанию (`-d`),
    `AmbientCapabilities=CAP_DAC_READ_SEARCH` с предупреждением, что это
    чтение всех файлов хоста; говорит, что `PrivateTmp=true` делает `/tmp`
    агента отдельным от `/tmp` хоста; ссылается на документацию restic об
    исключениях.
23. Пройти способ с ACL из документации на `$QA/locked` (шаг 13) дословно
    по тексту документа, от имени пользователя, под которым запущен агент
    → повтор шага 13 (после S6a) даёт `succeeded`.

## Завершение

24. `kill $AGPID`; `rs unlock --remove-all`; `chmod -R u+rwX $QA`.
