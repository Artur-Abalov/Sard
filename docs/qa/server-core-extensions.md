<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# QA: точки расширения server-core (CORE1 фаза 3, SARD-7)

Спецификация: `docs/specs/server/server-core-extensions.feature` (сценарии с
тегом `@qa`; утверждена владельцем 2026-10-30). Процедура проходится через
HTTP (`curl`, `jq`), `docker compose` и `psql`; консоль не нужна.

## Подготовка

- Два стенда на чистых томах:
  - **Ядро** — `make up` из текущей ветки, без расширений.
  - **Образец** — сервер, собранный из `examples/extension-sample` против
    локально опубликованного `server-core` той же версии, на своей базе.
    Инструкция сборки и запуска — `examples/extension-sample/README.md`; этот
    файл создаётся вместе с образцом при реализации фичи.
- На обоих стендах мастер первого запуска пройден, пароль администратора
  `correct-horse-battery`, зарегистрирован агент с hostname `qa`, плагином
  `files` и репозиторием `qa`, есть источник `qa-files` этого агента с хотя бы
  одним снимком; на стенде образца подключён ещё агент роли verifier.
- Переменные: `S=http://localhost:8080`, `J=/tmp/qa-cookies`,
  `P=correct-horse-battery` (для образца — свой адрес и свой файл cookie).
  `P` — текущий пароль администратора; шаг 18 его меняет.
- Вход (повторяется после каждого перезапуска сервера):
  `curl -s -c $J -X POST $S/api/v1/session -H 'Content-Type: application/json' -d "{\"password\":\"$P\"}" -o /dev/null -w '%{http_code}\n'`
  → `204`.
- Идентификаторы стенда — после первого входа на каждом стенде, без ручной
  подстановки:

  ```bash
  AGENT=$(curl -s -b $J "$S/api/v1/agents" | jq -r '.items[] | select(.hostname == "qa") | .id')
  SRC=$(curl -s -b $J "$S/api/v1/sources" | jq -r '.items[] | select(.name == "qa-files") | .id')
  echo "$AGENT $SRC"
  ```

  Ожидается: два UUID через пробел. Пустое значение — стенд не подготовлен,
  дальше не идти.
- Свойства образца (`sard.sample.licence`, `sard.sample.deny`,
  `sard.sample.listener`, `sard.sample.subject`,
  `sard.sample.stray-controller`) задаются переменными окружения сервера
  образца (`SARD_SAMPLE_LICENCE`, `SARD_SAMPLE_DENY`, `SARD_SAMPLE_LISTENER`,
  `SARD_SAMPLE_SUBJECT`, `SARD_SAMPLE_STRAY_CONTROLLER`).
  **Каждое изменение применяется только перезапуском сервера образца**
  (`docker compose up -d --force-recreate server`); после перезапуска
  дождаться `GET /actuator/health` = `UP` и войти заново. `AGENT` и `SRC` при
  перезапуске не меняются.
- `psql` везде запускается так:
  `docker compose exec -T postgres psql -U sard -d sard -Atc "<запрос>"`.
- Каждый запуск сохраняет свой id из ответа в свою переменную (`RUN1`,
  `RUN2`, …) и дальше ссылается только на неё.

## A. Ядро без расширений

1. `curl -s $S/api/v1/status | jq -c '{edition, hasVersion: has("version"), hasLast: has("lastVerifiedRestoreAt"), hasExt: has("extensions"), hasLic: has("licence")}'`
   без cookie. Ожидается:
   `{"edition":{"id":"free","displayName":"Free"},"hasVersion":true,"hasLast":true,"hasExt":false,"hasLic":false}`.
2. `curl -s $S/api/v1/server -o /tmp/body -w '%{http_code}\n'` без cookie.
   Ожидается: `401`, `jq -r .code /tmp/body` = `unauthenticated`.
3. Войти. `curl -s -b $J $S/api/v1/server | jq -c '{edition, extensions, licence}'`.
   Ожидается: `{"edition":{"id":"free","displayName":"Free"},"extensions":[],"licence":null}`.
4. `psql` с запросом `select to_regclass('sard.flyway_schema_history_extensions') is null`.
   Ожидается: `t` (таблицы нет).
5. Создать временный источник и сохранить его id:

   ```bash
   TMP=$(curl -s -b $J -X POST $S/api/v1/sources -H 'Content-Type: application/json' \
     -d "{\"name\":\"qa-tmp\",\"agentId\":\"$AGENT\",\"plugin\":\"files\",\"repositoryName\":\"qa\",\"config\":{\"paths\":[\"/etc/hostname\"]}}" | jq -r .id); echo $TMP
   ```

   Ожидается: UUID. Удалить его:
   `curl -s -b $J -X DELETE $S/api/v1/sources/$TMP -o /dev/null -w '%{http_code}\n'`.
   Ожидается: успешный код `2xx` (не `403`) — политика ядра разрешает
   администратору всё.

## B. Редакция и лицензия образца

6. Стенд образца с `SARD_SAMPLE_LICENCE=valid` (перезапуск, вход, `AGENT` и
   `SRC` по подготовке).
   `curl -s $S/api/v1/status | jq -c '{edition, hasExt: has("extensions"), hasLic: has("licence")}'`
   без cookie. Ожидается:
   `{"edition":{"id":"sample","displayName":"Sample"},"hasExt":false,"hasLic":false}`.
7. `curl -s -b $J $S/api/v1/server | jq -c '{edition, extensions, licence}'`.
   Ожидается: `edition` = `{"id":"sample","displayName":"Sample"}`,
   `extensions` = `[{"id":"sample","displayName":"Sard Sample Extension"}]`,
   `licence` = `{"state":"valid","holder":"Pilot LLC","expiresAt":"2026-12-31T00:00:00Z","items":[{"label":"Agents","value":"1"}]}`.
8. Для каждого значения `expired`, `absent`, `invalid`: задать
   `SARD_SAMPLE_LICENCE`, перезапустить сервер образца, войти,
   `curl -s -b $J $S/api/v1/server -o /tmp/body -w '%{http_code}\n'; jq -r .licence.state /tmp/body`.
   Ожидается: код `200`, `licence.state` равно заданному значению.

## C. Лицензия не блокирует бэкап, восстановление и проверку

9. `SARD_SAMPLE_LICENCE=expired`, перезапуск, вход. Запустить бэкап:

   ```bash
   curl -s -b $J -X POST $S/api/v1/sources/$SRC/runs -o /tmp/body -w '%{http_code}\n'
   RUN1=$(jq -r .id /tmp/body); echo $RUN1
   ```

   Ожидается: тот же код ответа, что на стенде ядра (запуск создан), и UUID
   запуска; через некоторое время
   `curl -s -b $J $S/api/v1/runs/$RUN1 | jq -r .status` → `succeeded`.
10. Там же восстановить последний снимок (как в `docs/qa/console-restore.md`):

    ```bash
    SNAP=$(curl -s -b $J "$S/api/v1/sources/$SRC/snapshots" | jq -r '.items[0].snapshotId')
    RUN2=$(curl -s -b $J -X POST "$S/api/v1/sources/$SRC/snapshots/$SNAP/restores" -H 'Content-Type: application/json' \
      -d "{\"agentId\":\"$AGENT\",\"config\":{\"destination\":\"qa-ext\"}}" | jq -r .id); echo "$SNAP $RUN2"
    ```

    Ожидается: два непустых значения; `GET /api/v1/runs/$RUN2` доходит до
    `status` = `succeeded`.
11. Там же запросить проверку последнего целого снимка:
    `curl -s -b $J -X POST $S/api/v1/sources/$SRC/verifications -H 'Content-Type: application/json' -d '{"snapshotId":null}' -w '\n%{http_code}\n'`.
    Ожидается: успешный код, задание создано; запись проверки источника
    появляется со статусом `passed`.
12. Задать `SARD_SAMPLE_LICENCE=failing`, **перезапустить сервер образца и
    войти заново**, затем повторить шаг 9, сохранив id в `RUN3`. Ожидается:
    запуск создан и доходит до `succeeded`. Затем
    `curl -s -b $J $S/api/v1/server -o /tmp/body -w '%{http_code}\n'; jq -r .licence.state /tmp/body`
    → `200` и `unavailable`;
    `curl -s $S/api/v1/status -o /tmp/body -w '%{http_code}\n'; jq -r .edition.id /tmp/body`
    → `200` и `sample`; в `docker compose logs server` одна строка
    `WARN` о недоступной лицензии.

## D. Политика доступа расширения

13. `SARD_SAMPLE_DENY='DELETE /api/v1/sources/{sourceId}'`, перезапуск, вход.
    Создать временный источник командой шага 5 (переменная `TMP`). Затем
    `curl -s -b $J -X DELETE $S/api/v1/sources/$TMP -D - -o /tmp/body`.
    Ожидается: статус `403`, `Content-Type: application/problem+json`,
    `jq -r .code /tmp/body` = `access_denied`.
14. `curl -s -b $J $S/api/v1/sources/$TMP -o /dev/null -w '%{http_code}\n'`.
    Ожидается: `200` — источник на месте, запрещена только одна операция.

## E. События для аудита

15. `SARD_SAMPLE_LISTENER=record`, `SARD_SAMPLE_DENY` пусто, перезапуск, вход.
    `curl -s -b $J $S/api/v1/sample/events | jq '.[-1]'`.
    Ожидается: последнее событие — `type` = `signin.succeeded` с
    `clientAddress` адреса клиента и субъектом вида `administrator`, id `admin`.
16. Запустить бэкап `qa-files` в свою переменную:
    `RUN4=$(curl -s -b $J -X POST $S/api/v1/sources/$SRC/runs | jq -r .id); echo $RUN4`,
    дождаться, пока `GET /api/v1/runs/$RUN4` отдаст итоговый `status`. Затем
    `curl -s -b $J $S/api/v1/sample/events | jq -c --arg r "$RUN4" '[.[] | select(.runId == $r) | {type, kind, trigger, from, to, terminal}]'`.
    Ожидается:
    - первое событие — `run.created` с `kind` = `backup`, `trigger` = `manual`;
    - ровно одно `run.started`, и оно идёт после `run.created`;
    - события `run.status_changed` идут цепочкой: `from` каждого равен `to`
      предыдущего, первое начинается с `queued`;
    - ровно одно событие с `terminal` = `true`, оно последнее, и его `to`
      равно `status` из `GET /api/v1/runs/$RUN4` (`succeeded`); событий
      `run.finished` нет.
17. Остановить агента `qa` (`systemctl stop sard-agent` на его хосте),
    запустить бэкап: `RUN5=$(curl -s -b $J -X POST $S/api/v1/sources/$SRC/runs | jq -r .id)`,
    отменить его: `curl -s -b $J -X POST $S/api/v1/runs/$RUN5/cancel -o /dev/null -w '%{http_code}\n'`
    → `200`; снова запустить агента. Тем же `jq`, что в шаге 16, с `$RUN5`
    ожидается: `run.created`, затем одно `run.status_changed` с `from` =
    `queued`, `to` = `cancelled`, `terminal` = `true`; `run.started` нет.
18. Сменить пароль:
    `curl -s -b $J -c $J -X PUT $S/api/v1/session/password -H 'Content-Type: application/json' -d '{"currentPassword":"correct-horse-battery","newPassword":"new-horse-battery-2"}' -o /dev/null -w '%{http_code}\n'`
    → успешный код. **С этого шага `P=new-horse-battery-2`**: все
    последующие входы — с новым паролем (или верните старый тем же запросом
    с переставленными паролями перед разбором стенда).
19. Проверить событие смены пароля:
    `curl -s -b $J $S/api/v1/sample/events | jq -e '[.[] | select(.type == "settings.changed" and .setting == "password")] | length == 1'`
    → `true`;
    `curl -s -b $J $S/api/v1/sample/events | jq -e '[.[] | select(.type == "settings.changed" and .setting == "password") | tostring | select(test("correct-horse-battery|new-horse-battery-2|\\$argon2"))] | length == 0'`
    → `true` (в событии нет ни старого, ни нового пароля, ни хэша
    Argon2id).
20. Задать `SARD_SAMPLE_LISTENER=throw`, перезапустить сервер образца, войти
    паролем `$P`, создать токен регистрации:
    `curl -s -b $J -X POST $S/api/v1/enrollment-tokens -H 'Content-Type: application/json' -d '{}' -o /tmp/body -w '%{http_code}\n'; TOK=$(jq -r .id /tmp/body); echo $TOK`.
    Ожидается: тот же код, что на стенде ядра, и UUID;
    `curl -s -b $J $S/api/v1/enrollment-tokens | jq -e --arg t "$TOK" 'any(.items[]; .id == $t)'`
    → `true`; в логе сервера строка `WARN` о сбое слушателя с типом события.

## F. Эндпоинт расширения

21. `curl -s $S/api/v1/sample/events -o /tmp/body -w '%{http_code}\n'` без
    cookie. Ожидается: `401`, `jq -r .code /tmp/body` = `unauthenticated`.
22. То же с `-b $J`. Ожидается: `200`.
23. Действующая защита каждой операции образца: `security` операции, если он
    задан, иначе корневой `security` документа; пустой массив — операция
    открыта.

    ```bash
    curl -s $S/v3/api-docs | jq -c '. as $d
      | [.paths | to_entries[] | select(.key | startswith("/api/v1/sample"))
         | .key as $p | .value | to_entries[]
         | select(.key | IN("get","put","post","delete","patch"))
         | {path: $p, method: .key,
            secured: ((.value.security // $d.security // []) | length > 0),
            has401: (.value.responses | has("401"))}]'
    ```

    Ожидается:
    `[{"path":"/api/v1/sample/events","method":"get","secured":true,"has401":true},{"path":"/api/v1/sample/notes","method":"post","secured":true,"has401":true}]`.

## G. Миграции расширения

24. Новый стенд образца на чистых томах, но служба `migrate` — из образа
    ядра (без образца на classpath); затем запустить сервер образца.
    Ожидается: сервер не стартует; первая строка исключения в
    `docker compose logs server` начинается с
    `Database startup refused: EXTENSION_MIGRATIONS_NOT_APPLIED:` и называет
    `sard.flyway_schema_history_extensions`, службу `migrate` и
    `docs/operator/16-postgresql.md`.
25. Выполнить `migrate` из образа образца, снова запустить сервер.
    Ожидается: сервер стартует (`/actuator/health` = `UP`), и:
    - `psql` с `select success from sard.flyway_schema_history_extensions where version = '1' and description = 'sample events'`
      выводит `t`;
    - `psql` с `select count(*) from sard.flyway_schema_history where description = 'sample events'`
      выводит `0`.

## H. Один источник субъекта для политики и событий (Р26)

26. Стенд образца из шага 25: `SARD_SAMPLE_LISTENER=record`,
    `SARD_SAMPLE_SUBJECT=sso-user:alice`, `SARD_SAMPLE_DENY` пусто,
    перезапуск, вход паролем `$P`. Изменить сроки шагов по умолчанию:
    `curl -s -b $J -X PUT $S/api/v1/run-defaults -H 'Content-Type: application/json' -d '{"backupTimeoutMinutes":400,"restoreTimeoutMinutes":720}' -o /dev/null -w '%{http_code}\n'`
    → `200`. Затем
    `curl -s -b $J $S/api/v1/sample/events | jq -c '[.[] | select(.type == "settings.changed" and .setting == "run_defaults")][-1].actor'`.
    Ожидается: `{"kind":"sso-user","id":"alice"}`.
27. Запустить бэкап: `RUN6=$(curl -s -b $J -X POST $S/api/v1/sources/$SRC/runs | jq -r .id); echo $RUN6`,
    затем
    `curl -s -b $J $S/api/v1/sample/events | jq -c --arg r "$RUN6" '[.[] | select(.runId == $r and .type == "run.created")] | map(.actor)'`.
    Ожидается: `[{"kind":"sso-user","id":"alice"}]`.
28. Выйти: `curl -s -b $J -X DELETE $S/api/v1/session -o /dev/null -w '%{http_code}\n'`
    → `204`. Войти заново и выполнить
    `curl -s -b $J $S/api/v1/sample/events | jq -c '[.[] | select(.type == "signout")][-1].actor'`.
    Ожидается: `{"kind":"sso-user","id":"alice"}`.
29. Убрать `SARD_SAMPLE_SUBJECT`, перезапуск, вход, повторить запрос шага 26
    с `"backupTimeoutMinutes":360` и тот же `jq`.
    Ожидается: `{"kind":"administrator","id":"admin"}` — без свойства
    субъект берётся из сессии ядра.

## I. Контроллер вне /api/v1 из пакета ядра (Р27)

30. На стенде ядра (`make up`, без расширений)
    `curl -s $S/actuator/health | jq -r .status`.
    Ожидается: `UP` — ядро проходит проверку контроллеров.
31. На стенде образца задать `SARD_SAMPLE_STRAY_CONTROLLER=true` и
    перезапустить сервер образца.
    Ожидается: сервер не стартует; в `docker compose logs server` ошибка
    старта называет расширение `sample` и путь `/sample/stray`.
32. Убрать `SARD_SAMPLE_STRAY_CONTROLLER`, перезапустить.
    Ожидается: `/actuator/health` = `UP`.

## J. Событие настроек решается при записи (Р28)

33. Стенд образца, `SARD_SAMPLE_LISTENER=record`, `SARD_SAMPLE_SUBJECT`
    пусто, вход. Запомнить число событий `run_defaults`:

    ```bash
    count() { curl -s -b $J $S/api/v1/sample/events | jq '[.[] | select(.type == "settings.changed" and .setting == "run_defaults")] | length'; }
    N0=$(count); echo $N0
    ```

    Затем два одинаковых изменения на новое значение одновременно:

    ```bash
    B='{"backupTimeoutMinutes":420,"restoreTimeoutMinutes":720}'
    for i in 1 2; do curl -s -b $J -X PUT $S/api/v1/run-defaults -H 'Content-Type: application/json' -d "$B" -o /dev/null -w '%{http_code}\n' & done; wait
    echo $(( $(count) - N0 ))
    ```

    Ожидается: два кода `200` и разница `1`. Одновременность вручную не
    гарантирована; гонку закрывает автоматический сценарий «Два
    одновременных одинаковых изменения дают одно событие».
34. Тот же `PUT` с `$B` ещё раз, затем `echo $(( $(count) - N0 ))`.
    Ожидается: `200` и по-прежнему `1` — повтор без изменений события не
    даёт.

## K. Сброс пароля командой admin-reset (Р32)

35. Стенд образца, `SARD_SAMPLE_LISTENER=record`. Остановить сервер и
    выполнить сброс:
    `docker compose stop server && docker compose run --rm server admin-reset; echo $?`.
    Ожидается: `0`. Запустить сервер (`docker compose up -d server`),
    дождаться `UP`, пройти шаг admin мастера с кодом из лога сервера (как в
    `docs/qa/onboarding-setup.md`), задав пароль `$P`, войти. Затем
    `curl -s -b $J $S/api/v1/sample/events | jq -c '[.[] | select(.type == "settings.changed" and .setting == "admin_password") | {actor, objectId}]'`.
    Ожидается: `[{"actor":null,"objectId":null}]` — ровно одно событие.
36. Перезапустить сервер образца, войти, повторить `jq` шага 35.
    Ожидается: по-прежнему один элемент.
37. Повторить шаг 35, но выполнить `admin-reset` два раза подряд до
    запуска сервера. Ожидается: событий `admin_password` стало на одно
    больше, чем после шага 36 (два элемента всего).

## Итог

Все шаги дали ожидаемый результат — фича принята. Любое расхождение
записывается номером шага и фактическим выводом. В конце верните пароль
администратора стенда образца, если он нужен дальше (шаг 18).
