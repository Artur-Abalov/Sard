<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# QA: точки расширения server-core (CORE1 фаза 3, SARD-7)

Спецификация: `docs/specs/server/server-core-extensions.feature` (сценарии с
тегом `@qa`). Процедура проходится через HTTP (`curl`, `jq`), `docker compose`
и `psql`; консоль не нужна.

## Подготовка

- Два стенда на чистых томах:
  - **Ядро** — `make up` из текущей ветки, без расширений.
  - **Образец** — сервер, собранный из `examples/extension-sample` против
    локально опубликованного `server-core` той же версии, на своей базе.
    Инструкция сборки и запуска — `examples/extension-sample/README.md`; этот
    файл создаётся вместе с образцом при реализации фичи.
- На обоих стендах мастер первого запуска пройден, пароль администратора
  `correct-horse-battery`, зарегистрирован агент `qa` с плагином `files` и
  репозиторием `qa`, есть источник `qa-files` этого агента.
- Переменные: `S=http://localhost:8080`, `J=/tmp/qa-cookies`, `P=correct-horse-battery`
  (для образца — свой адрес и свой файл cookie). `P` — текущий пароль
  администратора; шаг 15 его меняет.
- Вход (повторяется после каждого перезапуска сервера):
  `curl -s -c $J -X POST $S/api/v1/session -H 'Content-Type: application/json' -d "{\"password\":\"$P\"}" -o /dev/null -w '%{http_code}\n'`
  → `204`.
- Свойства образца (`sard.sample.licence`, `sard.sample.deny`,
  `sard.sample.listener`) задаются переменными окружения сервера образца
  (`SARD_SAMPLE_LICENCE`, `SARD_SAMPLE_DENY`, `SARD_SAMPLE_LISTENER`).
  **Каждое изменение применяется только перезапуском сервера образца**
  (`docker compose up -d --force-recreate sard-server`); после перезапуска
  дождаться `GET /actuator/health` = `UP` и войти заново.
- `psql` везде запускается так:
  `docker compose exec -T postgres psql -U sard -d sard -Atc "<запрос>"`.

## A. Ядро без расширений

1. `curl -s $S/api/v1/status | jq '{edition, hasVersion: has("version"), hasLast: has("lastVerifiedRestoreAt"), hasExt: has("extensions"), hasLic: has("licence")}'`
   без cookie. Ожидается:
   `{"edition":{"id":"free","displayName":"Free"},"hasVersion":true,"hasLast":true,"hasExt":false,"hasLic":false}`.
2. `curl -s $S/api/v1/server -o /tmp/body -w '%{http_code}\n'` без cookie.
   Ожидается: `401`, `jq -r .code /tmp/body` = `unauthenticated`.
3. Войти. `curl -s -b $J $S/api/v1/server | jq -c '{edition, extensions, licence}'`.
   Ожидается: `{"edition":{"id":"free","displayName":"Free"},"extensions":[],"licence":null}`.
4. `psql` с запросом `select to_regclass('sard.flyway_schema_history_extensions') is null`.
   Ожидается: `t` (таблицы нет).
5. Создать временный источник и запомнить его id:
   `ID=$(curl -s -b $J -X POST $S/api/v1/sources -H 'Content-Type: application/json' -d '{"name":"qa-tmp","agentId":"<id агента qa>","plugin":"files","repositoryName":"qa","config":{"paths":["/etc/hostname"]}}' | jq -r .id); echo $ID`.
   Ожидается: непустой UUID. Удалить его:
   `curl -s -b $J -X DELETE $S/api/v1/sources/$ID -o /dev/null -w '%{http_code}\n'`.
   Ожидается: успешный код `2xx` (не `403`) — политика ядра разрешает
   администратору всё.

## B. Редакция и лицензия образца

6. Стенд образца с `SARD_SAMPLE_LICENCE=valid` (перезапуск, вход).
   `curl -s $S/api/v1/status | jq -c '{edition, hasExt: has("extensions"), hasLic: has("licence")}'`
   без cookie. Ожидается:
   `{"edition":{"id":"sample","displayName":"Sample"},"hasExt":false,"hasLic":false}`.
7. `curl -s -b $J $S/api/v1/server | jq -c '{edition, extensions, licence}'`.
   Ожидается: `edition` = `{"id":"sample","displayName":"Sample"}`, `extensions` = `[{"id":"sample","displayName":"Sard Sample Extension"}]`,
   `licence` = `{"state":"valid","holder":"Pilot LLC","expiresAt":"2026-12-31T00:00:00Z","items":[{"label":"Agents","value":"1"}]}`.
8. Для каждого значения `expired`, `absent`, `invalid`: задать
   `SARD_SAMPLE_LICENCE`, перезапустить сервер образца, войти,
   `curl -s -b $J $S/api/v1/server -o /tmp/body -w '%{http_code}\n'; jq -r .licence.state /tmp/body`.
   Ожидается: код `200`, `licence.state` равно заданному значению.

## C. Лицензия не блокирует бэкап, восстановление и проверку

9. `SARD_SAMPLE_LICENCE=expired`, перезапуск, вход. Запустить бэкап:
   `curl -s -b $J -X POST $S/api/v1/sources/<qa-files>/runs -w '\n%{http_code}\n'`.
   Ожидается: тот же код ответа, что на стенде ядра (запуск создан); через
   некоторое время `GET /api/v1/runs/<runId>` отдаёт `status` = `succeeded`.
10. Там же запустить восстановление последнего снимка
    (`POST /api/v1/sources/<qa-files>/snapshots/<snapshotId>/restores` с телом,
    как в `docs/qa/console-restore.md`). Ожидается: запуск создан и доходит до
    `succeeded`.
11. Там же запросить проверку (`POST /api/v1/sources/<qa-files>/verifications`
    с телом по контракту OpenAPI, без snapshotId — последний целый снимок) при
    подключённом верификаторе. Ожидается: задание создано, запись проверки
    появляется со статусом `passed`.
12. Задать `SARD_SAMPLE_LICENCE=failing`, **перезапустить сервер образца и
    войти заново**, затем повторить шаг 9. Ожидается: запуск создан и доходит
    до `succeeded`. Затем
    `curl -s -b $J $S/api/v1/server -w '\n%{http_code}\n'` → код `200`,
    `licence.state` = `unavailable`; `curl -s $S/api/v1/status -w '\n%{http_code}\n'` →
    `200` с `edition.id` = `sample`; в `docker compose logs sard-server` одна
    строка `WARN` о недоступной лицензии.

## D. Политика доступа расширения

13. `SARD_SAMPLE_DENY='DELETE /api/v1/sources/{sourceId}'`, перезапуск, вход.
    Создать временный источник, как в шаге 5, и сохранить `ID`. Затем
    `curl -s -b $J -X DELETE $S/api/v1/sources/$ID -D - -o /tmp/body`.
    Ожидается: статус `403`, `Content-Type: application/problem+json`,
    `jq -r .code /tmp/body` = `access_denied`.
14. `curl -s -b $J $S/api/v1/sources/$ID -o /dev/null -w '%{http_code}\n'`.
    Ожидается: `200` — источник на месте, запрещена только одна операция.

## E. События для аудита

15. `SARD_SAMPLE_LISTENER=record`, `SARD_SAMPLE_DENY` пусто, перезапуск, вход.
    `curl -s -b $J $S/api/v1/sample/events | jq '.[-1]'`.
    Ожидается: последнее событие — `type` = `signin.succeeded` с
    `clientAddress` адреса клиента и субъектом вида `administrator`, id `admin`.
16. Запустить бэкап `qa-files` (как в шаге 9), запомнить `RUN` — `id` из
    ответа, дождаться завершения. Затем
    `curl -s -b $J $S/api/v1/sample/events | jq -c --arg r "$RUN" '[.[] | select(.runId == $r) | {type, kind, trigger, from, to, terminal}]'`.
    Ожидается:
    - первое событие — `run.created` с `kind` = `backup`, `trigger` = `manual`;
    - ровно одно `run.started`, и оно идёт после `run.created`;
    - события `run.status_changed` идут цепочкой: `from` каждого равен `to`
      предыдущего, первое начинается с `queued`;
    - ровно одно событие с `terminal` = `true`, оно последнее, и его `to`
      равно `status` из `GET /api/v1/runs/$RUN` (`succeeded`); событий
      `run.finished` нет.
16а. Остановить агента `qa` (`systemctl stop sard-agent` на его хосте),
    запустить бэкап `qa-files`, запомнить `RUN2`, отменить его
    (`POST /api/v1/runs/$RUN2/cancel`), снова запустить агента. Тем же `jq` с
    `$RUN2` ожидается: `run.created`, затем одно `run.status_changed` с
    `from` = `queued`, `to` = `cancelled`, `terminal` = `true`; `run.started`
    нет.
17. Сменить пароль:
    `curl -s -b $J -X PUT $S/api/v1/session/password -H 'Content-Type: application/json' -d '{"currentPassword":"correct-horse-battery","newPassword":"new-horse-battery-2"}' -c $J -o /dev/null -w '%{http_code}\n'`
    → успешный код. **С этого шага `P=new-horse-battery-2`**: все
    последующие входы — с новым паролем (или верните старый тем же запросом
    с переставленными паролями перед разбором стенда).
18. Проверить событие смены пароля:
    `curl -s -b $J $S/api/v1/sample/events | jq -e '[.[] | select(.type == "settings.changed" and .setting == "password")] | length == 1'`
    → `true`;
    `curl -s -b $J $S/api/v1/sample/events | jq -e '[.[] | select(.setting == "password") | tostring | select(test("correct-horse-battery|new-horse-battery-2|\\$argon2"))] | length == 0'`
    → `true` (в событии нет ни старого, ни нового пароля, ни хэша
    Argon2id).
19. Задать `SARD_SAMPLE_LISTENER=throw`, перезапустить сервер образца, войти
    паролем `$P`, создать токен регистрации
    (`POST /api/v1/enrollment-tokens` с `{}`). Ожидается: ответ такой же, как
    на стенде ядра, токен есть в `GET /api/v1/enrollment-tokens`; в логе
    сервера строка `WARN` о сбое слушателя с типом события.

## F. Эндпоинт расширения

20. `curl -s $S/api/v1/sample/events -o /tmp/body -w '%{http_code}\n'` без
    cookie. Ожидается: `401`, `jq -r .code /tmp/body` = `unauthenticated`.
21. То же с `-b $J`. Ожидается: `200`.
22. `curl -s $S/v3/api-docs | jq -c '[.paths | to_entries[] | select(.key | startswith("/api/v1/sample")) | {path: .key, secured: ([.value[] | .security // null | select(. != [])] | length > 0)}]'`.
    Ожидается: `[{"path":"/api/v1/sample/events","secured":true},{"path":"/api/v1/sample/notes","secured":true}]`.

## G. Миграции расширения

23. Новый стенд образца на чистых томах, но служба `migrate` — из образа
    ядра (без образца на classpath); затем запустить сервер образца.
    Ожидается: сервер не стартует; первая строка исключения в
    `docker compose logs sard-server` начинается с
    `Database startup refused: EXTENSION_MIGRATIONS_NOT_APPLIED:` и называет
    `sard.flyway_schema_history_extensions`, службу `migrate` и
    `docs/operator/16-postgresql.md`.
24. Выполнить `migrate` из образа образца, снова запустить сервер.
    Ожидается: сервер стартует (`/actuator/health` = `UP`), и:
    - `psql` с `select success from sard.flyway_schema_history_extensions where version = '1' and description = 'sample events'`
      выводит `t`;
    - `psql` с `select count(*) from sard.flyway_schema_history where description = 'sample events'`
      выводит `0`.

## Итог

Все шаги дали ожидаемый результат — фича принята. Любое расхождение
записывается номером шага и фактическим выводом. В конце верните пароль
администратора стенда образца, если он нужен дальше (шаг 17).
