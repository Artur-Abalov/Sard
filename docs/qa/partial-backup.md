<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# QA: неполный бэкап виден и понятен (SARD-15 / PL7, часть 2)

Сценарии: `docs/specs/proto/partial-backup.feature` (сводка решений НБ1–НБ16 и
вопросы ВН1–ВН11) и поправки PL7-2 в `docs/specs/agent/files-plugin.feature`
(Ф19, Ф20), `docs/specs/agent/postgresql-plugin.feature` (ПГ17f),
`docs/specs/agent/repo-restore.feature` (Р89–Р93),
`docs/specs/server/rest-api-w2.feature` (К18–К22),
`docs/specs/server/run-notifications.feature` (Р11–Р13),
`docs/specs/server/server-metrics.feature` (`sard_run_partial_total`),
`docs/specs/server/schedules.feature` (К8), `docs/specs/web/console-pages.feature`
(Г10–Г13), `docs/specs/web/schedules.feature`. **ЧЕРНОВИК** — проходится после
утверждения спецификации владельцем и реализации срезов PL7-2a…PL7-2d. Шаги,
зависящие от ответа владельца, помечены «(ВН…)».

Здесь вручную проходятся сквозные пути: нечитаемый файл на хосте → вывод
агента → REST → консоль → Telegram → метрика, и хост без сервера. Обрезка
путей по бюджету, нормализация сервера, причины `snapshot_set_incomplete` и
`unknown` в уведомлении, гонки и разбор proto проверяются тестами `@unit`,
`@server`, `@http`, `@wire`, `@buf` с теми же названиями; здесь эти причины
видны только на моках консоли (часть 5).

Ожидаемый результат указан после «→» в каждом шаге. Любое расхождение — дефект.

## Подготовка (стенд разработчика)

Нужны: Docker, Go, `restic` 0.19.1, `curl`, `jq`; Telegram-клиент с ботом и
чатом, как в `docs/qa/run-notifications.md`. Команды — из корня репозитория,
**не от root**: от root права `0000` не мешают чтению.

1. Выполнить «Подготовку» и часть 2 (до первого бэкапа) из
   `docs/qa/files-plugin.md` (сервер, агент на хосте, репозиторий `qa`,
   переменные `AG`, `H`, `QA`, `DC`, `PSQL`, `API`, `AGENT`, `D`, функции
   `src`, `run`, `rs`; сессия в `$QA/jf`).
2. Выполнить шаг 2 «Подготовки» `docs/qa/run-notifications.md` (бот, публичный
   адрес консоли `http://localhost:5173/`, `SARD_NOTIFY_LANGUAGE=ru`, функции
   `dlv`, `waitdlv`).
3. Вспомогательные функции:

```bash
# mloop — scrape слушателя метрик изнутри контейнера, без токена (loopback)
mloop() { $DC exec -T server bash -c "exec 3<>/dev/tcp/127.0.0.1/8081 && printf 'GET /metrics HTTP/1.0\r\n\r\n' >&3 && cat <&3"; }
# mpart — три серии счётчика неполных бэкапов и счётчик провалов
mpart() { mloop | grep -E '^sard_run_(partial|finished)_total' | sort; }
# runj <run-id> — карточка запуска; runs — первая страница списка запусков
runj() { curl -sS -b $QA/jf $API/runs/$1; }
runs() { curl -sS -b $QA/jf "$API/runs?limit=20"; }
```

   → `mpart` печатает `sard_run_partial_total{reason="snapshot_set_incomplete"} 0.0`,
   `…{reason="unknown"} 0.0`, `…{reason="unreadable_files"} 0.0` (или
   текущие значения после прошлых прогонов — записать их как базу).

4. Дерево с нечитаемыми файлами (11 штук, чтобы проверить «и ещё»):

```bash
P=$QA/ptree; mkdir -p $P/ok $P/locked
printf 'readable\n' > $P/ok/a.txt
for i in $(seq -w 1 11); do printf 'QA-SECRET-CONTENT-%s\n' $i > $P/ok/f$i.txt; chmod 0000 $P/ok/f$i.txt; done
chmod 0000 $P/locked
SP=$(src "{\"paths\":[\"$P\"]}")
```

   → `ls -l $P/ok | grep -c '^----------'` → `11`.

## Часть 1. Агент и REST (PL7-2a, PL7-2b)

5. `run $SP` → `status` `failed`; `backup.snapshotId` не пуст;
   `backup.partial` `true`; `backup.incompleteReason` `unreadable_files`;
   `backup.unreadableCount` `12` (11 файлов и каталог `locked`);
   `backup.unreadablePaths` — ровно 10 абсолютных путей под `$P`, в порядке, в
   котором их называет `message`; `message` — английская строка агента с
   числом 12, как до PL7-2 (НБ3). Запомнить `RUNP=$RUN`.
6. `runj $RUNP | grep -c QA-SECRET-CONTENT` → `0` (содержимого нет, только
   пути).
7. `runs | jq -r ".items[] | select(.id==\"$RUNP\") | [.status, .partial] | @tsv"`
   → `failed	true`. Для успешного запуска из `docs/qa/files-plugin.md` шаг 9
   (если он был) → `succeeded	false`.
8. `curl -sS -b $QA/jf "$API/sources/$SP/snapshots" | jq '.items[0] | {partial, incompleteReason, restorable, restoreBlockedReason}'`
   → `partial` `true`, `incompleteReason` `unreadable_files`, `restorable`
   `false`, `restoreBlockedReason` `partial` (R2 ВВ8 не меняется).
9. Вернуть права одному файлу и запустить снова:
   `chmod 0644 $P/ok/f01.txt; run $SP` → `unreadableCount` `11`, путь
   `…/ok/f01.txt` в `unreadablePaths` отсутствует.
10. Полный бэкап: `chmod -R u+rwX $P; run $SP` → `status` `succeeded`;
    `backup.partial` `false`; `unreadableCount` `null`; `unreadablePaths`
    `[]`; `incompleteReason` `null`. Затем вернуть нечитаемость:
    `chmod 0000 $P/locked $P/ok/f*.txt`.

## Часть 2. Telegram (PL7-2b)

11. Сообщение о запуске `$RUNP` из шага 5 (`waitdlv $RUNP` → `delivered|…`).
    Прочитать в чате → строки по порядку:
    - `❌ Бэкап завершился ошибкой: <имя источника>` (жирным);
    - `Агент: …`, `Длительность: …`;
    - `Причина: файлов, которые агент не смог прочитать: 12`;
    - 10 строк путей моноширинным шрифтом, те же и в том же порядке, что
      `unreadablePaths` шага 5;
    - `и ещё 2`;
    - строки `Полный текст — в консоли.` нет (пути короче бюджета 500,
      ВН7) — если сумма длин путей `$P` на стенде больше 500, строка есть, а
      перечислены первые пути, сумма которых не больше 500;
    - `Проверьте, что пользователь sard-agent может читать эти файлы: https://github.com/Artur-Abalov/sard/blob/main/docs/plugins/files.md#от-чьего-имени-читаются-файлы`;
    - `Снимок создан, но неполный: часть данных в него не попала. Восстановить его можно только на хосте командой repo restore.`;
    - `Всего: …, добавлено: …`, `Запуск: $RUNP`, ссылка на запуск.
    В тексте нет `at least one source file could not be read` и нет
    `QA-SECRET-CONTENT`.
12. Нажать ссылку подсказки (ВН9) → открывается `docs/plugins/files.md` на
    GitHub на разделе «От чьего имени читаются файлы».
13. Английский: в `deploy/.env` заменить `SARD_NOTIFY_LANGUAGE=ru` на `en`,
    `$DC up -d --wait server`, `run $SP; waitdlv $RUN` → в сообщении
    `Reason: files the agent could not read: 12`, `and 2 more`,
    `Check that the user sard-agent can read these files: <та же ссылка>`,
    `The snapshot was created but is incomplete: …`. Вернуть `ru`.
14. Серия по расписанию (ADR 0024): задать источнику `$SP` в консоли
    расписание `*/2 * * * *`, дождаться двух запусков по расписанию → в чат
    пришло **одно** сообщение (о первом, с причиной шага 11);
    `dlv <id второго>` → `skipped|…`. Удалить расписание.

## Часть 3. Метрика (PL7-2b)

15. `mpart` после шагов 5, 9, 13, 14 → `sard_run_partial_total{reason="unreadable_files"}`
    выросла на число неполных запусков с базы шага 3; две другие серии не
    изменились; `sard_run_finished_total{status="failed"}` выросла не меньше,
    чем `unreadable_files`.
16. Провал без снимка: `run $(src '{"paths":["/no/such/path"]}')` → `failed`,
    `backup` `null`; `mpart` → `sard_run_partial_total` не изменился,
    `sard_run_finished_total{status="failed"}` +1.
17. `mloop | grep '^sard_run_partial_total' | grep -c -E 'ptree|QA-SECRET|/'`
    → `0` (в метках нет путей).
18. `grep -n 'sard_run_partial_total' docs/operator/14-monitoring.md` → есть
    описание, три значения `reason` и выражение «провалы без снимка» с
    вычитанием из `sard_run_finished_total{status="failed"}`.

## Часть 4. Консоль против сервера (PL7-2c)

`cd web && npm run dev`, <http://localhost:5173>, язык RU.

19. Страница запусков → у `$RUNP` метка «ошибка» и рядом текст «неполный»;
    наведение даёт подсказку «Бэкап не завершён, но снимок создан —
    подробности в карточке запуска». У запуска шага 16 — «ошибка» без
    «неполный». У успешного — без пометки.
20. Обзор → в блоке последних запусков у `$RUNP` та же пометка «неполный».
21. Карточка `$RUNP` → причина «Файлов, которые агент не смог прочитать: 12»;
    10 путей моноширинным шрифтом по одному на строке; «и ещё 2»; подсказка о
    правах пользователя sard-agent со ссылкой (ведёт туда же, что шаг 12);
    пояснение о снимке «…часть данных в него не попала… repo restore»; текста
    `at least one source file could not be read` на странице нет.
22. Переключить язык на EN → те же блоки по-английски, пути не изменились.
23. Карточка источника `$SP`: блок расписания (задать расписание и дождаться
    одного запуска, как шаг 14) → у последнего запуска по расписанию пометка
    «неполный»; список снимков → у неполных снимков «неполный» с пояснением
    «часть данных в него не попала»; кнопки «Восстановить» у них нет.
24. Пометка без цвета: в инструментах разработчика включить эмуляцию
    «achromatopsia» → «неполный» читается как текст.

## Часть 5. Консоль на моках — все причины неполноты (PL7-2c)

`cd web && VITE_API_MOCKS=1 npm run dev`.

25. Открыть запуск фикстуры с `incompleteReason` `snapshot_set_incomplete` →
    сообщение агента показано как есть; пояснение: «Основной снимок сохранён
    полностью, но связанный с ним дополнительный снимок (отпечаток источника
    или глобальные объекты PostgreSQL) не сохранился, поэтому бэкап не
    завершён. Восстановить снимок можно только на хосте командой repo
    restore.»; подсказки о правах нет (ВН1).
26. Запуск фикстуры с `incompleteReason` `unknown` → сообщение агента как есть;
    пояснение «Снимок создан, но бэкап не завершён, поэтому снимок считается
    неполным. …» (ВН6).
27. Запуск фикстуры с `unreadable_files`, где число больше числа путей и один
    путь содержит `[REDACTED]` → «и ещё N» с верным N; путь с `[REDACTED]`
    показан как есть.
28. Список снимков источника этих фикстур → у каждого неполного снимка
    пояснение своей причины (как шаги 23, 25, 26).

## Часть 6. Агент N−1 (НБ1, НБ6)

29. Остановить агент (`kill $AGPID`), запустить агент предыдущего релиза
    (бинарник из GitHub Releases той же платформы) с тем же
    `--config "$H/agent.yaml"`; `run $SP` → `failed`, `partial` `true`,
    `unreadableCount` `null`, `unreadablePaths` `[]`, `incompleteReason`
    `unknown`; `message` — строка агента с числом и путями.
30. Сообщение в Telegram → `Причина: <message агента>` моноширинным, строки
    подсказки о правах нет, строка о снимке «Снимок создан, но бэкап не
    завершён, поэтому снимок считается неполным. …» (ВН6); `mpart` →
    `reason="unknown"` +1.
31. Вернуть текущий агент: остановить старый, запустить `"$AG" --config "$H/agent.yaml"`.

## Часть 7. Хост без сервера (PL7-2d)

Стенд ВМ-1 из `docs/qa/repo-restore.md` (переменные `AG`, `OUT`, функции
`run`, `audit`; источник `qa-files` с id `$SF`, два успешных бэкапа после
V2). Дополнительно на ВМ-1:

```bash
sudo install -d -m 0755 /srv/qa-partial; printf 'ok\n' | sudo tee /srv/qa-partial/ok.txt >/dev/null
printf 'secret\n' | sudo tee /srv/qa-partial/locked.txt >/dev/null; sudo chmod 0600 /srv/qa-partial/locked.txt
```

В консоли создать источник `qa-partial` (files, `main`, путь
`/srv/qa-partial`) и запустить бэкап → `failed` с неполным снимком;
`SQP=<id источника>`.

32. `run sudo $AG repo snapshots main` → `exit=0`; первая строка
    `ID TIME HOST SOURCE RUN COMPLETE PATHS` (ВН4); у снимков `qa-files`,
    сделанных после V2 и успешно, COMPLETE `confirmed`; у снимка `qa-partial`
    — `unconfirmed`; у снимков `qa-pg` (базы и глобальных объектов), у снимка
    отпечатка `/sard-fingerprint.json` и у ручного снимка `/etc/hostname` —
    `-`; в stderr ровно одна строка `completeness of <N> snapshot(s) is not
    confirmed …` с `<N>` = числу строк `unconfirmed`.
33. `sudo $AG repo snapshots main --json | jq -r '.snapshots[] | [.short_id, (.completeness // "null")] | @tsv'`
    → те же значения, что в шаге 32 (`-` ↔ `null`); stderr пуст.
34. `sudo $AG repo snapshots main --source $SQP` → снимок `qa-partial` с
    `unconfirmed`; `sudo $AG repo snapshots main --source $SF` → снимки
    `qa-files` с `confirmed`, снимки отпечатка с `-`.
35. Ничего не пишется (V2): `sudo /usr/libexec/sard/restic --repo /srv/sard/main --password-file /etc/sard/secrets/restic-main.pass --no-lock snapshots --json | sha256sum`
    до и после шагов 32–34 → одинаковые суммы.
36. Восстановление неподтверждённого (ВН2):
    `T=$(mktemp -u /tmp/qa-r.XXXX); run sudo $AG repo restore main latest --source $SQP --target $T`
    → `exit=0`; в stderr до строк восстановления —
    `warning: completeness of snapshot <короткий id> is not confirmed: …`;
    в итоге строка `completeness: not confirmed` и `restored`;
    `sudo ls $T/srv/qa-partial` → `ok.txt` есть, `locked.txt` нет.
37. Восстановление подтверждённого: `T2=$(mktemp -u /tmp/qa-r.XXXX); run sudo $AG repo restore main latest --source $SF --target $T2`
    → `exit=0`; ни в stdout, ни в stderr нет `completeness`.
38. Аудит: `audit '-5 min'` → строки `repository main snapshot <id> restored to $T`
    и `… restored to $T2` — того же вида, что до PL7-2 (Р65).
39. Справка: `sudo $AG repo snapshots --help` → объясняет колонку COMPLETE,
    значения `confirmed`, `unconfirmed`, `-`, что `unconfirmed` получают и
    полные снимки с упавшим отпечатком и снимки до отпечатков, поле
    `completeness` в `--json`. `sudo $AG repo restore --help` → говорит о
    предупреждении и строке `completeness` в итоге.
40. Документация: `docs/operations/agent-host-setup.md`, раздел «Снимки и
    восстановление на хосте», и `docs/plugins/files.md` → объясняют
    `unconfirmed`, предупреждение repo restore и что делать (права
    sard-agent, ссылка на раздел «От чьего имени читаются файлы»).

## Уборка

41. Стенд разработчика: `chmod -R u+rwX $QA/ptree`; удалить источник `$SP` и
    источник шага 16 в консоли. ВМ-1: `sudo rm -rf /srv/qa-partial $T $T2`,
    удалить источник `qa-partial`.
