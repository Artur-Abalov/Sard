<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# QA: точки расширения server-core (CORE1 фаза 3, SARD-7)

Спецификация: `docs/specs/server/server-core-extensions.feature` (сценарии с
тегом `@qa`). Процедура проходится через HTTP (`curl`), `docker compose` и
`psql`; консоль не нужна.

## Подготовка

- Два стенда на чистых томах:
  - **Ядро** — `make up` из текущей ветки, без расширений.
  - **Образец** — сервер, собранный из `examples/extension-sample` против
    локально опубликованного `server-core` той же версии (как описано в
    `examples/extension-sample/README.md`), на своей базе.
- На обоих стендах мастер первого запуска пройден, пароль администратора
  `correct-horse-battery`, зарегистрирован агент `qa` с плагином `files` и
  репозиторием `qa`, есть источник `qa-files` этого агента.
- Переменные: `S=http://localhost:8080`, `J=/tmp/qa-cookies` (для образца —
  свой адрес и свой файл cookie).
- Вход: `curl -s -c $J -X POST $S/api/v1/session -H 'Content-Type: application/json' -d '{"password":"correct-horse-battery"}' -o /dev/null -w '%{http_code}\n'`
  → `204`.
- Свойства образца (`sard.sample.licence`, `sard.sample.deny`,
  `sard.sample.listener`) задаются переменными окружения сервера образца
  (`SARD_SAMPLE_LICENCE`, `SARD_SAMPLE_DENY`, `SARD_SAMPLE_LISTENER`) и
  применяются перезапуском сервера; после перезапуска войти заново.

## A. Ядро без расширений

1. `curl -s $S/api/v1/status | jq '{edition, extensions, licence}'` без cookie.
   Ожидается: `{"edition":{"id":"free","displayName":"Free"},"extensions":[],"licence":null}`;
   поля `version` и `lastVerifiedRestoreAt` присутствуют, как раньше.
2. `docker compose exec postgres psql -U sard -d sard -Atc "select to_regclass('sard.flyway_schema_history_extensions')"`.
   Ожидается: пустая строка (таблицы нет).
3. Удалить источник с сессией: `curl -s -b $J -X DELETE $S/api/v1/sources/<id> -o /dev/null -w '%{http_code}\n'`
   на временном источнике. Ожидается: успешный код `2xx` (не `403`):
   политика ядра разрешает администратору всё.

## B. Редакция и лицензия образца

4. Стенд образца с `SARD_SAMPLE_LICENCE=valid`.
   `curl -s $S/api/v1/status | jq '{edition, extensions, licence}'`.
   Ожидается: `edition` = `{"id":"sample","displayName":"Sample"}`,
   `extensions` = `[{"id":"sample","displayName":"Sard Sample Extension"}]`,
   `licence.state` = `valid`, `licence.holder` = `Pilot LLC`,
   `licence.expiresAt` = `2026-12-31T00:00:00Z`,
   `licence.items` = `[{"label":"Agents","value":"1"}]`.
5. Повторить шаг 4 для `expired`, `absent`, `invalid`.
   Ожидается: каждый раз ответ `200`, `licence.state` равно заданному
   значению, `edition.id` = `sample`.

## C. Лицензия не блокирует бэкап, восстановление и проверку

6. `SARD_SAMPLE_LICENCE=expired`. Войти, запустить бэкап:
   `curl -s -b $J -X POST $S/api/v1/sources/<qa-files>/runs -w '\n%{http_code}\n'`.
   Ожидается: тот же код ответа, что на стенде ядра (запуск создан), через
   некоторое время `GET /api/v1/runs/<runId>` отдаёт `status` = `succeeded`.
7. Там же запустить восстановление последнего снимка
   (`POST /api/v1/sources/<qa-files>/snapshots/<snapshotId>/restores` с телом,
   как в `docs/qa/console-restore.md`). Ожидается: запуск создан и доходит до
   `succeeded`.
8. Там же запросить проверку (`POST /api/v1/sources/<qa-files>/verifications`
   с телом по контракту OpenAPI, без snapshotId — последний целый снимок) при
   подключённом верификаторе. Ожидается: задание создано, запись
   проверки появляется со статусом `passed`.
9. Повторить шаг 6 с `SARD_SAMPLE_LICENCE=failing`. Ожидается: запуск создан и
   доходит до `succeeded`; `GET /api/v1/status` отвечает `200` с
   `licence.state` = `unavailable`, в логе сервера одна строка `WARN` о
   недоступной лицензии.

## D. Политика доступа расширения

10. `SARD_SAMPLE_DENY='DELETE /api/v1/sources/{sourceId}'`. Войти, создать
    временный источник и удалить его:
    `curl -s -b $J -X DELETE $S/api/v1/sources/<id> -D - -o /tmp/body`.
    Ожидается: статус `403`, `Content-Type: application/problem+json`,
    `jq .code /tmp/body` = `"access_denied"`; `GET /api/v1/sources/<id>`
    отвечает `200` (источник на месте).
11. Там же `GET /api/v1/sources/<id>`. Ожидается: `200` — запрещена только
    одна операция.

## E. События для аудита

12. `SARD_SAMPLE_LISTENER=record`, `SARD_SAMPLE_DENY` пусто. Войти с адреса
    стенда. `curl -s -b $J $S/api/v1/sample/events | jq '.[-1]'`.
    Ожидается: последнее событие — «вход удался» с `clientAddress` адреса
    клиента и субъектом сессии.
13. Запустить бэкап `qa-files` (как в шаге 6) и дождаться завершения.
    Ожидается: в `GET /api/v1/sample/events` ровно одно событие «запуск
    начат» и ровно одно «запуск завершён» с этим `runId`, `kind` = `backup`,
    `trigger` = `manual`; `status` завершения равен `status` из
    `GET /api/v1/runs/<runId>`; «начат» стоит раньше «завершён».
14. Сменить пароль (`PUT /api/v1/session/password` с текущим
    `correct-horse-battery` и новым `new-horse-battery-2`), затем
    `curl -s -b $J $S/api/v1/sample/events | grep -c -e correct-horse -e new-horse`.
    Ожидается: событие «настройки изменены» с `setting` = `password` есть;
    `grep` выводит `0`.
15. Перезапустить сервер с `SARD_SAMPLE_LISTENER=throw`, войти, запустить
    бэкап `qa-files`. Ожидается: ответ такой же, как в шаге 6, запуск
    создан; в логе сервера строка `WARN` о сбое слушателя с типом события.

## F. Эндпоинт расширения

16. `curl -s $S/api/v1/sample/events -o /tmp/body -w '%{http_code}\n'` без
    cookie. Ожидается: `401`, `jq .code /tmp/body` = `"unauthenticated"`.
17. То же с `-b $J`. Ожидается: `200`.
18. `curl -s $S/v3/api-docs | jq '.paths | keys | map(select(startswith("/api/v1/sample")))'`.
    Ожидается: `["/api/v1/sample/events","/api/v1/sample/notes"]`; у обеих
    операций непустое `security`.

## G. Миграции расширения

19. Новый стенд образца на чистых томах, но служба `migrate` — из образа
    ядра (без образца на classpath); затем запустить сервер образца.
    Ожидается: сервер не стартует; первая строка исключения в
    `docker compose logs sard-server` начинается с
    `Database startup refused: EXTENSION_MIGRATIONS_NOT_APPLIED:` и называет
    `sard.flyway_schema_history_extensions`, службу `migrate` и
    `docs/operator/16-postgresql.md`.
20. Выполнить `migrate` из образа образца, снова запустить сервер.
    Ожидается: сервер стартует;
    `psql -Atc "select version, success from sard.flyway_schema_history_extensions where version = '1'"`
    выводит `1|t`, а в `sard.flyway_schema_history` строки версии `1` образца нет.

## Итог

Все шаги дали ожидаемый результат — фича принята. Любое расхождение
записывается номером шага и фактическим выводом.
