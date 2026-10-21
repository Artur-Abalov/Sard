# QA: сроки хранения и фоновая уборка сервера (H1, фаза 2)

Сценарии: `docs/specs/server/server-retention.feature` (утверждена
владельцем 2026-10-21, ответы В1–В11 в шапке). Шаги с пометкой «(В…)»
проверяют соответствующий ответ владельца.

Здесь вручную проходятся сценарии с тегом `@qa` и сквозной путь «старые строки
в базе → проход уборки → REST и консоль». Пачки, гонки, два экземпляра,
тенанты, фейковые часы и точные границы проверяют тесты `@server`, `@startup`,
`@arch` с теми же названиями. Возраст строк здесь задаётся сдвигом отметок
времени в базе (`$PSQL`), а не ожиданием.

Ожидаемый результат указан после «→» в каждом шаге. Любое расхождение — дефект.

## Подготовка

Нужны: Docker, Go, `restic` 0.19.1, `curl`, `jq`. Команды — из корня
репозитория.

1. Выполнить «Подготовку» из `docs/qa/files-plugin.md` (сервер, агент,
   репозиторий `qa`, переменные `AG`, `H`, `QA`, `DC`, `PSQL`, `API`, `AGENT`,
   `D`, функции `src`, `run`, `rs`; сессия в `$QA/jf`).
2. Короткий интервал уборки (В6: свойство `sard.retention.interval`) и
   файл переопределения для сервера. Первый проход наступает через один
   интервал после старта (В10):

```bash
cat > $QA/override.yml <<'EOF'
services:
  server:
    environment:
      SARD_RETENTION_INTERVAL: 1m
EOF
DC="$DC -f $QA/override.yml"
$DC up -d --wait server
# waitpass — ждёт строку итога следующего прохода (до 3 мин), печатает её
waitpass() { n=$($DC logs server | grep -c 'Retention pass deleted')
  for i in $(seq 1 180); do m=$($DC logs server | grep -c 'Retention pass deleted'); [ "$m" -gt "$n" ] && break; sleep 1; done
  $DC logs server | grep 'Retention pass deleted' | tail -1; }
# old <run-id> <days> — сдвигает запуск и его шаги на <days> дней в прошлое
old() { $PSQL "update runs set queued_at=queued_at-interval '$2 days', started_at=started_at-interval '$2 days', finished_at=finished_at-interval '$2 days' where id='$1'; update run_steps set queued_at=queued_at-interval '$2 days', dispatched_at=dispatched_at-interval '$2 days', started_at=started_at-interval '$2 days', finished_at=finished_at-interval '$2 days' where run_id='$1'"; }
code() { curl -sS -b $QA/jf -o /dev/null -w '%{http_code}\n' "$1"; }
```

   → сервер `healthy`; `$DC logs server | grep -ci 'retention'` не содержит
   строк уровня `ERROR`.

## Часть 1. Проверка свойств при старте

3. **Срок доставок равен ttl.** Добавить в `$QA/override.yml` под
   `environment` строки `SARD_NOTIFY_TTL: 24h` и
   `SARD_RETENTION_NOTIFICATION_DELIVERIES: 24h`; `$DC up -d server; sleep 20;
   $DC ps server`
   → контейнер не `healthy` (перезапускается или вышел);
   `$DC logs server | grep -c 'sard.retention.notification-deliveries'` → не
   меньше `1`, и та же строка называет `sard.notify.ttl`.
4. Заменить значение на `25h`; `$DC up -d --wait server`
   → сервер `healthy`.
5. **Нулевой срок.** Удалить строку доставок, добавить
   `SARD_RETENTION_RUNS: 0s`; `$DC up -d server; sleep 20; $DC logs server |
   grep -c 'sard.retention.runs'`
   → не меньше `1`; сервер не `healthy`.
6. Убрать из `$QA/override.yml` всё, кроме `SARD_RETENTION_INTERVAL: 1m`;
   `$DC up -d --wait server`
   → сервер `healthy`.

## Часть 2. Запуски: удаляются старые без снимка, остаются снимки и последние успехи

7. Источник и запуски:

```bash
SRC=$(src "{\"paths\":[\"$D\"]}")
run $SRC >/dev/null; B1=$RUN; run $SRC >/dev/null; B2=$RUN; run $SRC >/dev/null; B3=$RUN
FSRC=$(src "{\"paths\":[\"$QA/missing\"]}")
run $FSRC >/dev/null; F1=$RUN; run $FSRC >/dev/null; F2=$RUN
$PSQL "select count(*) from snapshots s join run_steps t on t.id=s.step_id where t.run_id in ('$F1','$F2')"
```

   → последняя команда печатает `0` (у F1 и F2 снимков нет);
   `curl -sS -b $QA/jf $API/runs/$B1 | jq -r .status` → `succeeded`.
8. Запомнить список снимков:
   `curl -sS -b $QA/jf "$API/sources/$SRC/snapshots" | jq -S '[.items[].snapshotId]' > $QA/snaps.before`
9. Состарить все пять запусков: `for r in $B1 $B2 $B3 $F1 $F2; do old $r 200; done`
10. `waitpass`
    → строка `Retention pass deleted … run=2 …` и `run_step=2` (по шагу у
    F1 и F2); уровень `INFO`.
11. `code $API/runs/$F1; code $API/runs/$F2`
    → `404` и `404`.
12. `code $API/runs/$B1; code $API/runs/$B2; code $API/runs/$B3`
    → три раза `200` (у каждого есть снимок).
13. **Снимки после уборки.**
    `curl -sS -b $QA/jf "$API/sources/$SRC/snapshots" | jq -S '[.items[].snapshotId]' | diff - $QA/snaps.before && echo same`
    → `same`.
14. Для первого снимка из `$QA/snaps.before`:
    `code "$API/sources/$SRC/snapshots/$(jq -r '.[0]' $QA/snaps.before)/restore-options"`
    → `200`.
15. В консоли (`cd web && npm run dev`) открыть страницу источника `$SRC`
    → все снимки на месте, кнопка восстановления у них есть; в списке
    запусков нет F1 и F2.
Правила «последний успешный restore и verify» и «последняя проверка
источника» здесь не проходятся: их проверяют тесты `@server`.

## Часть 3. Логи шагов: секция удаляется целиком, чтение без ошибки

16. Перенести строки лога шага B1 в старую секцию:

```bash
STEP=$(curl -sS -b $QA/jf $API/runs/$B1 | jq -r '.steps[0].id')
$PSQL "select count(*) from step_logs where step_id='$STEP'"            # запомнить N
$PSQL "create table if not exists step_logs_2026_08 partition of step_logs for values from ('2026-08-01') to ('2026-09-01')"
$PSQL "update step_logs set received_at='2026-08-15T00:00:00Z' where step_id='$STEP'"
$PSQL "select count(*) from step_logs_2026_08"                           # = N
STEP2=$(curl -sS -b $QA/jf $API/runs/$B2 | jq -r '.steps[0].id')
$PSQL "select count(*) from step_logs where step_id='$STEP2'"           # запомнить N2
```

    → `N` больше `0`; последняя выборка из `step_logs_2026_08` равна `N`.
17. `waitpass`
    → строка итога с `step_log_partition=1` (или больше, если в базе были
    другие секции старше срока).
18. `$PSQL "select to_regclass('step_logs_2026_08') is null"`
    → `t`.
19. `curl -sS -b $QA/jf -w '\n%{http_code}\n' "$API/runs/$B1/steps/$STEP/logs?afterSeq=0"`
    → тело `{"items":[],"nextAfterSeq":0,"hasMore":false,"truncated":false}`
    (порядок полей не важен), код `200`.
20. `$PSQL "select count(*) from step_logs where step_id='$STEP2'"`
    → `N2` (секция текущего месяца не тронута).
21. Открыть в консоли страницу запуска B1
    → страница открывается без ошибки, статус и снимок шага показаны; лог
    шага пуст, текст «Строк пока нет» (В2: пометки об удалении по сроку нет,
    консоль не меняется).

## Часть 4. Доставки

22. Вставить старую финальную и старую ожидающую доставку о запуске B3:

```bash
$PSQL "insert into notification_deliveries (id, tenant_id, run_id, channel, status, attempts, next_attempt_at, created_at, finished_at)
  select gen_random_uuid(), tenant_id, id, 'qa-done', 'delivered', 1, now()-interval '40 days', now()-interval '40 days', now()-interval '40 days' from runs where id='$B3'"
$PSQL "insert into notification_deliveries (id, tenant_id, run_id, channel, status, attempts, next_attempt_at, created_at)
  select gen_random_uuid(), tenant_id, id, 'qa-wait', 'pending', 0, now()+interval '1 day', now()-interval '40 days' from runs where id='$B3'"
```

23. `waitpass`
    → в строке итога `notification_delivery=1` (или больше при других
    старых доставках).
24. `$PSQL "select channel from notification_deliveries where run_id='$B3' order by channel"`
    → только `qa-wait` (В7: pending не удаляется, удаляются только финальные
    статусы); `qa-done` нет.
25. `$PSQL "delete from notification_deliveries where channel='qa-wait'"` —
    уборка за собой.

## Часть 5. Ошибка прохода

26. `$DC stop postgres`; подождать 90 с.
    → `$DC logs server | grep -i retention | grep -c ERROR` не меньше `1`;
    строка ERROR называет класс исключения и не содержит текста строк лога;
    процесс сервера жив (`$DC ps server` — контейнер запущен).
27. Вернуть базу и состарить новый запуск: `$DC start postgres; $DC up -d --wait server`;
    `run $FSRC >/dev/null; F3=$RUN; old $F3 200; waitpass`
    → строка итога с `run=1`; `code $API/runs/$F3` → `404`.

## Завершение

28. `$DC down`; удалить `$QA` и `$H`; вернуть `deploy/.env`, если он
    менялся.
