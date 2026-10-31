<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# QA: политика хранения, забывание и ручная очистка хранилища (L1, выпуск 1)

Сценарии: `docs/specs/server/retention-policy.feature`,
`docs/specs/web/retention-policy.feature`,
`docs/specs/agent/retention-prune.feature`,
`docs/specs/proto/retention-prune.feature`. Здесь вручную проходятся сценарии с
тегами `@qa` и `@qa-only`; остальные проверяют тесты с теми же названиями.
Проверки самого шага FORGET на агенте (предел, части, блокировки) — часть 3
`docs/qa/retention-forget.md`; её шаги «отправить FORGET» выполняются шагом 14
этой процедуры.

Ожидаемый результат указан после «→» в каждом шаге. Любое расхождение — дефект.

## Подготовка

Нужны: Docker, Go, `restic` 0.19.1 (`.bin/restic` после `make tools`), `curl`,
`jq`, браузер. Команды — из корня репозитория.

1. Выполнить «Подготовку» `docs/qa/retention-forget.md` (шаги 1–4: сервер,
   агент `AG` на хосте `H`, сессия `login`, репозиторий `qa`, функции `rs`,
   `seed`, `count`). В `$H/agent.yaml` нет раздела `retention_floor`.
2. Создать источник `files` на этом агенте с репозиторием `qa` и каталогом
   `$QA/data` (как часть 2 `docs/qa/files-plugin.md`); `S=<id источника>`.
   Создать второй такой же источник `T` (каталог `$QA/data2`).
3. Вспомогательные функции:

```bash
ret()  { curl -s -b $QA/jf "$API/sources/$1/retention"; }
setp() { curl -s -b $QA/jf -X PUT -H 'Content-Type: application/json' -d "$2" "$API/sources/$1/retention" -w '\n%{http_code}\n'; }
fgt()  { curl -s -b $QA/jf -X POST "$API/sources/$1/forget" -w '\n%{http_code}\n'; }
prn()  { curl -s -b $QA/jf -X POST "$API/repositories/$1/prune" -w '\n%{http_code}\n'; }
run()  { curl -s -b $QA/jf "$API/runs/$1"; }
wait_run() { for i in $(seq 1 120); do s=$(run $1 | jq -r .status); case $s in succeeded|failed|cancelled|agent_unavailable) echo $s; return;; esac; sleep 2; done; echo timeout; }
snaps() { curl -s -b $QA/jf "$API/sources/$1/snapshots"; }
```

4. `R=$(ret $S | jq -r .repositoryId)`. → Непустой id; `ret $S | jq .maintenanceAgent.hostname`
   печатает hostname агента.

## Часть 1 — политика источника и политика по умолчанию (REST)

5. `curl -s -b $QA/jf $API/retention-defaults | jq -c .policy`.
   → `{"preset":"standard","keepLast":0,"keepDaily":7,"keepWeekly":4,"keepMonthly":6,"keepYearly":0,"keepWithinDays":0}`.
6. `ret $S | jq -c '{policy, hostFloor, floorExtendsPolicy, forgetBlockedReason, pruneBlockedReason}'`.
   → policy «стандарт» (источник создан после обновления); hostFloor
   `{"disabled":false,"keepLast":7,"keepWithinDays":14,"origin":"built_in"}`;
   floorExtendsPolicy `true`; обе причины `null`.
7. `setp $S '{"policy":{"preset":"minimum"}}'` → код `200`, policy keepDaily 7,
   остальные поля 0, preset `minimum`.
8. `setp $S '{"policy":{"preset":"custom"}}'` → код `422`, `code`
   `validation_failed`, ошибка у поля `policy` с текстом `retention policy is empty`;
   `ret $S | jq -r .policy.preset` по-прежнему `minimum`.
9. `setp $S '{"policy":{"preset":"standard","keepLast":3}}'` → `422`, ошибка у
   поля `policy.keepLast`.
10. `setp $S '{"policy":null}'` → `200`, `policy` null, `forgetBlockedReason`
    `retention_not_configured`. `fgt $S` → `409`, `retention_not_configured`.
11. Запустить бэкап `S` (`POST $API/sources/$S/runs`), дождаться `succeeded`.
    → `curl -s -b $QA/jf "$API/runs?kind=forget&sourceId=$S" | jq '.items|length'`
    печатает `0`.

## Часть 2 — забывание после бэкапа и вручную, сверка

Перед частью: `rs snapshots --json | jq -r '.[].id' | xargs -r rs forget >/dev/null`;
остановить агента, дописать в `$H/agent.yaml` выключенный предел для `qa`
(шаг 6 `docs/qa/retention-forget.md`), запустить агента.

12. `setp $S '{"policy":{"preset":"custom","keepLast":2}}'`. Запустить три
    бэкапа `S` подряд, каждый дождаться `succeeded`.
    → `curl -s -b $QA/jf "$API/runs?kind=forget&sourceId=$S" | jq '.items|length'`
    печатает число от 1 до 3 (forget, ещё не завершённый к следующему бэкапу,
    новый не порождает); у каждого trigger `after_backup`, `requestedBy`
    null, итог `succeeded`; `count $S` печатает `2`.
13. `snaps $S | jq -c '.items[] | {snapshotId, forgottenAt, forgottenReason, restorable, restoreBlockedReason}'`.
    → Ровно один снимок с непустым `forgottenAt`, `forgottenReason` `policy`,
    `restorable` false, `restoreBlockedReason` `forgotten`; у двух других
    `forgottenAt` null.
14. Ручной forget: `F=$(fgt $S | head -1 | jq -r .id)`; `wait_run $F`.
    → Код ответа `201`, kind `forget`, trigger `manual`, requestedBy `admin`;
    итог `succeeded`; `run $F | jq '.steps[0].forget'` несёт `policy`,
    `forgottenCount`, `keptByFloorCount`, `remainingCount`, `listedAt`.
    Этим шагом отправляется FORGET в части 3 `docs/qa/retention-forget.md`
    (политика задаётся заранее через `setp`).
15. Сверка снимка, удалённого руками: взять id самого старого незабытого
    снимка из шага 13 (`X`), выполнить `rs forget $X`, затем `fgt $S` и
    дождаться итога. → У снимка `X` `forgottenReason` `missing`; в консоли
    подпись «не найден в репозитории», а не «удалён по политике».
16. Повторный ручной forget, пока первый в очереди: остановить агента,
    `fgt $S` дважды. → Первый `201`, второй `409` `maintenance_active` с
    `activeRunId` первого. Запустить агента. → Первый запуск завершается.
17. Восстановление забытого снимка: `POST $API/sources/$S/snapshots/<id из шага 13>/restores`
    с телом, как в `docs/qa/console-restore.md`. → `422`
    `snapshot_not_restorable`.

## Часть 3 — очистка хранилища (prune)

18. Засеять снимки с уникальным содержимым и забыть их без prune:
    `for i in 1 2 3; do head -c 20M /dev/urandom > $QA/data/big$i; done`,
    бэкап `S`, затем `rm $QA/data/big*`, ещё два бэкапа `S` (политика
    keepLast 2 забудет снимок с большими файлами). `RD` — каталог
    репозитория `qa`, на который указывает функция `rs` из «Подготовки».
    Запомнить `B=$(du -sb $RD/data | cut -f1)`.
19. `P=$(prn $R | head -1 | jq -r .id)`; `wait_run $P`.
    → Код `201`, kind `prune`, `sourceId` null, `repository.repositoryId`
    равен `$R`; итог `succeeded`; `run $P | jq .steps[0].prune.freedBytes`
    больше 50000000; `du -sb $RD/data` меньше `$B`; список снимков
    (`rs snapshots --json | jq length`) тот же, что до шага.
20. Лог шага prune (`GET $API/runs/$P/steps/<stepId>/logs`). → Есть строки
    сводки restic prune и строка `prune freed … bytes, repository size … bytes`;
    нет `--max-unused`, `--unsafe`, `forget`.
21. Prune при занятом репозитории: взять блокировку, как шаг 11
    `docs/qa/retention-forget.md`, но сохранить PID в `L`
    (`rs prune >/dev/null 2>&1 & L=$!; sleep 2; kill -STOP $L`), затем
    `prn $R` и дождаться итога (до 6 минут). → `failed`, шаг prune `failure`
    `repository_locked`, `lockHolder` называет PID `$L`;
    затем `kill -CONT $L; wait $L`.
22. Повторный prune в очереди: остановить агента, `prn $R` дважды. → `201`,
    затем `409` `maintenance_active`. Запустить агента.
23. Учётка без права удаления (стенд S3 ADR 0047, ключ без `DeleteObject` вне
    `locks/`): `prn` этого репозитория. → `failed`, сообщение
    `storage account has no delete permission: assign repository maintenance to a host whose account can delete`.

## Часть 4 — обслуживающий агент и старые агенты

24. Зарегистрировать второй агент роли source (часть 2
    `docs/qa/agent-enroll.md`, второй каталог `H2`) и подключить на нём тот же
    репозиторий `qa` (`repo add` с тем же адресом и паролем). Дождаться его
    Register. → `ret $S | jq -c '{maintenanceAgent, forgetBlockedReason, pruneBlockedReason}'`
    печатает `null` и `maintenance_owner_required` дважды; `fgt $S` и `prn $R`
    → `409` `maintenance_owner_required`. Бэкап `S` → `succeeded`, нового
    запуска forget нет.
25. Отозвать второй агент (`POST $API/agents/<id>/revoke`). → Причины снова
    `null`.
26. Агент предыдущего выпуска (пакет прошлой версии из раздачи или сборка из
    тега до L1a): заменить бинарь агента, перезапустить. → `ret $S` даёт обе
    причины `agent_outdated`, `hostFloor` null; `fgt $S` → `409`
    `agent_outdated`; бэкап `S` и восстановление его снимка → `succeeded`;
    запусков forget не появляется.
27. Агент L1a (сборка с FORGET, но без PRUNE, если доступна): → причина
    forget `null`, причина prune `agent_outdated`. Вернуть текущий агент.

## Часть 5 — консоль (дважды: на моках `VITE_API_MOCKS=1` и против сервера)

28. Открыть карточку источника без политики. → Блок «Хранение»: «Хранение не
    настроено — снимки этого источника не удаляются»; «Забыть по политике»
    недоступна; в списке источников у него пометка «хранение не настроено».
29. Выбрать «Стандарт», сохранить. → Блок показывает «Стандарт» и
    «7 ежедневных, 4 еженедельных, 6 ежемесячных»; строку «Хост … хранит не
    меньше: последние 7 снимков и всё за 14 дней (встроенный предел агента)» и
    «Снимки под этим пределом не удаляются, даже если политика их отпускает».
30. С выключенным пределом (против сервера — шаг 6 `docs/qa/retention-forget.md`;
    на моках — источник фикстуры с disabled). → Предупреждение «Нижний предел
    на хосте … выключен…».
31. Источник с каждой причиной блокировки (на моках — фикстура; против
    сервера — шаги 24 и 26). → Кнопки недоступны, причина видна текстом;
    для `agent_outdated` — «Обновите агента …».
32. Нажать «Забыть по политике». → Открывается страница запуска «Забывание
    по политике, вручную»; на ней политика словами и после итога «забыто N,
    сохранено пределом хоста M, осталось K».
33. Нажать «Очистить хранилище (prune)». → Диалог говорит, что очистка
    касается всех источников хранилища, ничего не забывает и что бэкапы ждут
    до 5 минут и затем завершаются ошибкой. «Отмена» — запроса нет;
    «Очистить» — страница запуска «Очистка хранилища», вместо источника —
    хранилище `qa`.
34. После шага 33 карточка источника. → «Последняя очистка: освобождено …,
    в хранилище …» (или «размер неизвестен» у мока без сводки);
    «Последнее забывание» с итогом.
35. Список снимков источника. → Забытые снимки подписаны «удалён по политике
    <дата>» или «не найден в репозитории <дата>», без кнопки «Восстановить».
36. Настройки → «Политика хранения для новых источников». → Показана
    «Стандарт» с пояснением, что смена не меняет созданные источники;
    выбрать «Не удалять снимки», сохранить; создать источник. → У нового
    источника «Хранение не настроено».
37. Переключить язык на EN и пройти шаги 28–35 глазами. → Ни одного ключа
    i18n и русского текста, кроме данных.

## Часть 6 — документация

38. Открыть `docs/operator/18-snapshot-retention.md`. → Есть пример
    `retention_floor` с `default` и `repositories`, встроенные 7 снимков и 14
    дней, запрет `disabled` в `default` и в `agent.d`, фраза «задайте нижний
    предел на каждом хосте, учётка которого может удалять в репозитории»,
    ограничение «один агент на репозиторий», раздел о ручной очистке с
    предупреждением о блокировке бэкапов, абзац о пределе `sard-self` на
    машине сервера. Ссылки на раздел есть в `docs/operator/README.md` и
    `docs/operator/03-configuration.md`.
39. Пройти пример из раздела о ручной очистке буквально на стенде из
    «Подготовки». → Каждая команда и каждый ожидаемый вывод совпадают.
