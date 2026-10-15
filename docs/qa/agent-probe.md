<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# QA: вызов проверки агента `AgentService/Probe` (A8d-3, сервер)

Сценарии: `docs/specs/server/agent-probe.feature` (ПР1–ПР10). СТАТУС:
ОЖИДАЕТ ОДОБРЕНИЯ вместе со спецификацией; ожидания шагов 11, 17 и 19 зависят от
ответов на ПРВ3, ПРВ2 и ПРВ4. Агентская сторона (`sard-agent check`, строки
`server.accepted` и `clock.skew`) — `docs/qa/host-check.md`, часть 10.

Ожидаемый результат — после «→». Любое расхождение — дефект.

Управляемые часы, точные границы `not_after`, одновременные вызовы и окно
потери шага вручную не проверяются — это делают тесты сценариев. Здесь —
сквозной путь через настоящие сервер, базу, `grpcurl` и агента.

## Подготовка

Нужны: Docker, Go, `grpcurl`, `curl`, `jq`, `restic` (`.bin/restic` после
`make tools`). Команды — из корня репозитория, не от root.

Выполнить блок «Подготовка» из `docs/qa/rest-api.md` (сервер, мастер первого
запуска, `$DC`, `$PSQL`, `$QA`, функции `a`, `body`, `$AG`, `$RESTIC`) и блок
части 3 оттуда же (агент `$X` с конфигом `$H/agent.yaml` запущен, `$AGPID`).
Затем:

```bash
GRPC="grpcurl -cacert $H/tls/ca.pem -import-path proto -proto sard/agent/v1/agent.proto -format-error"
# probe [флаги grpcurl] — Probe с сертификатом агента $X
probe() { $GRPC -cert $H/tls/agent.pem -key $H/tls/agent.key "$@" -d '{}' localhost:9090 sard.agent.v1.AgentService/Probe; }
# dump <имя> — данные всех таблиц, построчно и отсортированно
dump() { $DC exec -T postgres pg_dump -U sard -d sard --data-only --inserts | grep -v -e '^--' -e '^$' | sort > "$QA/$1.sql"; }
SERIAL=$(openssl x509 -in $H/tls/agent.pem -noout -serial | cut -d= -f2 | tr 'A-F' 'a-f' | sed 's/^0*//')
```

→ `a GET /agents/$X | body | jq -r .status` → `online`; `$SERIAL` не пуст.

## Часть 1. Контракт proto

1. `make breaking-proto` → код выхода `0`.
2. `make proto && git status --porcelain proto/gen/go` → пусто.
3. `$GRPC describe sard.agent.v1.ProbeResponse` → ровно одно поле
   `.google.protobuf.Timestamp server_time = 1;`.
   `$GRPC describe sard.agent.v1.ProbeRequest` → полей нет.

## Часть 2. Принятый агент

4. `probe | tee $QA/p.json; date -u +%s` →
   ответ `{"serverTime": "..."}` и ничего больше;
   `date -d "$(jq -r .serverTime $QA/p.json)" +%s` отличается от
   `date -u +%s` не больше чем на 2 с.
5. `probe | jq -c 'keys'` → `["serverTime"]`. `probe | grep -c -e "$X" -e qa -e db-password`
   → `0`.
6. Агент без Register: выпустить второй токен и зарегистрировать второй
   каталог без запуска службы
   (`H2=$QA/host2; cp -a $H $H2; rm -f $H2/tls/*; sed -i "s|$H|$H2|g" $H2/agent.yaml;
   CMD=$(a POST /enrollment-tokens '{}' | body | jq -r .enrollCommand);
   $AG ${CMD#sudo sard-agent } --config $H2/agent.yaml`), затем
   `$GRPC -cert $H2/tls/agent.pem -key $H2/tls/agent.key -d '{}' localhost:9090 sard.agent.v1.AgentService/Probe`
   → ответ с `serverTime`. Новый агент в `a GET /agents` — `offline`, без
   `agentVersion` и плагинов.

## Часть 3. Без побочных действий

7. Агент остановлен, чтобы его поток не писал `last_seen_at`:
   `kill $AGPID; sleep 5; dump before; for i in $(seq 20); do probe >/dev/null; done; dump after`
   → `diff $QA/before.sql $QA/after.sql` — пусто. Запустить агента снова
   (`$AG --config $H/agent.yaml > $QA/agent.out 2>&1 & AGPID=$!`), через 5 с —
   `online`.
8. При работающем агенте:
   `a GET /agents/$X | body > $QA/card1.json; for i in $(seq 20); do probe >/dev/null; done; a GET /agents/$X | body > $QA/card2.json`
   → `diff <(jq 'del(.lastSeenAt)' $QA/card1.json) <(jq 'del(.lastSeenAt)' $QA/card2.json)`
   → пусто (плагины, репозитории, секреты, статус `online` прежние);
   `grep -c -e SESSION_REPLACED -e AGENT_DUPLICATE_SESSION -e 'stream closed' $QA/agent.out`
   → не выросло после шага 7; `a GET /agents/$X | body | jq -r .duplicateSessionAt` → `null`.
9. Агент остановлен (`kill $AGPID; sleep 100`), `a GET /agents/$X | body | jq -r '.status, .lastSeenAt'`
   → `offline` и время T; `probe` → ответ с `serverTime`; повторный запрос
   карточки → снова `offline` и то же T. Запустить агента снова.
10. Во время бэкапа: запустить бэкап источника агента `$X` из консоли или
    `a POST /sources/<источник>/runs`, пока шаг в работе — `probe` 5 раз →
    запуск завершается `succeeded`; ни один шаг не `lost`;
    `$DC logs server | grep -c "agent lost the step"` не выросло.
11. `$DC logs server --since 1m | grep -c -e "$X" -e "$SERIAL" -e Probe` сразу
    после `probe` (агент остановлен, поток не пишет) → `0` строк уровня INFO и
    выше (ПРВ3).

## Часть 4. Отказы

Шаги 14–18 меняют записи агента `$X`; его служба после этого может
остановиться (отказ Register — ADR 0026). Для шагов части это не важно: они
вызывают Probe напрямую.

12. Без сертификата: `$GRPC -d '{}' localhost:9090 sard.agent.v1.AgentService/Probe`
    → `Code: Unauthenticated`, `Message: agent certificate rejected`, деталь
    `ErrorInfo` с `reason` `CERT_MISSING`, `domain` `sard.dev`; `serverTime` нет.
13. Сертификат чужого CA (самоподписанный: `openssl req -x509 -newkey ec
    -pkeyopt ec_paramgen_curve:P-256 -nodes -subj /CN=x -keyout $QA/x.key -out $QA/x.pem -days 1`)
    → `$GRPC -cert $QA/x.pem -key $QA/x.key -d '{}' localhost:9090 sard.agent.v1.AgentService/Probe`
    → `Code: Unavailable` (рукопожатие), ответа нет.
14. `dump b; $PSQL "update agent_certificates set not_after = now() - interval '1 second' where serial = '$SERIAL'"; dump b2`;
    `probe` → `Unauthenticated`, `CERT_EXPIRED`; `dump a2; diff $QA/b2.sql $QA/a2.sql` → пусто.
    Вернуть `not_after` (`... set not_after = now() + interval '1 year' ...`), `probe` → `serverTime`.
15. Отозвать агента в базе без сертификата:
    `$PSQL "update agents set revoked_at = now() where id = '$X'"` → `probe` →
    `AGENT_REVOKED`. Вернуть: `... set revoked_at = null ...`.
16. Отозвать сертификат: `$PSQL "update agent_certificates set revoked_at = now() where serial = '$SERIAL'"`
    → `probe` → `CERT_REVOKED`; `$DC logs server | grep 'reason=CERT_REVOKED' | tail -1`
    → строка с `serial=$SERIAL` и `method=sard.agent.v1.AgentService/Probe`;
    `$DC logs server | grep -c 'BEGIN CERTIFICATE'` → `0`. Вернуть `revoked_at = null`.
17. Отзыв в консоли: `a POST /agents/$X/revoke` → `HTTP 200`; `probe` →
    `CERT_REVOKED` (ПР7; по ответу на ПРВ2 — иначе). Ни текст, ни деталь не
    содержат `$X` и `$SERIAL`: `probe 2>&1 | grep -c -e "$X" -e "$SERIAL"` → `0`.
18. Неизвестный сертификат: `$DC exec -T postgres psql -U sard -d sard -c "delete from agent_certificates where serial = '$SERIAL'"`
    (агент `$X` уже отозван в шаге 17) → `probe` → `CERT_UNKNOWN`.

## Часть 5. Отказ базы

19. Второй агент из шага 6 жив. `$DC stop postgres`;
    `$GRPC -cert $H2/tls/agent.pem -key $H2/tls/agent.key -d '{}' localhost:9090 sard.agent.v1.AgentService/Probe`
    → код не `OK`, не `Unauthenticated` и не `Unimplemented` (точный код — по
    ПРВ4); текст не содержит имени хоста базы и `Exception`.
20. `$DC start postgres`, подождать готовности → тот же вызов → `serverTime`.

## Часть 6. Совместимость

21. Старый сервер (предыдущий выпуск без Probe; `make down`, образ
    предыдущего тега в `deploy/.env`, `make up`, агент зарегистрирован
    заново): `probe` с действующим сертификатом → `Code: Unimplemented`; с
    отозванным (`$PSQL` как в шаге 16) → `Unauthenticated`, `CERT_REVOKED`, а не
    `Unimplemented`.
22. Старый агент (предыдущий выпуск) с новым сервером: регистрируется,
    `online`, бэкап `succeeded`; в логе сервера нет вызовов Probe.
