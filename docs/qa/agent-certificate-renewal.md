# QA: продление сертификата агента (C1, фаза 2)

Сценарии: `docs/specs/server/agent-certificate-renewal.feature` и
`docs/specs/agent/agent-certificate-renewal.feature`. Решения владельца Р1–Р10
— `docs/sessions/2026-10-15-c1-cert-renewal.md`; решения specifier П1–П31 — в
заголовках спецификаций.

Ожидаемый результат указан после «→» в каждом шаге. Любое расхождение — дефект.

Стенд работает с минимальным сроком сертификата агента `PT10M` (П1): порог
продления — 6 мин 40 с после выпуска, действующий интервал частоты — 1 мин
(П2). Управляемых часов на стенде нет, поэтому часть шагов ждёт реальное время;
ожидания указаны. Границы порога и пауз, гонки, сбои базы, старый сервер
(`UNIMPLEMENTED`), агент предыдущей версии и миграцию проверяют тесты
сценариев (раздел «Не проверяется вручную»).

## Подготовка

Нужны: Docker с compose v2, Go, `curl`, `jq`, `grpcurl`, `openssl` 3, `python3`.
Команды — из корня репозитория, не от root. Шаги частей 2–8 ведутся в одной
оболочке.

```bash
QA=$(mktemp -d); J=$QA/jar
cat > $QA/override.yml <<'EOF'
services:
  server:
    environment:
      SARD_PKI_AGENT_CERTIFICATE_VALIDITY: ${QA_VALIDITY:-PT10M}
      SARD_PKI_AGENT_RENEWAL_MIN_INTERVAL: ${QA_MIN_INTERVAL:-PT1H}
EOF
DC="docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.build.yml -f $QA/override.yml --env-file deploy/.env"
$DC down -v; rm -f deploy/.env; cp deploy/.env.example deploy/.env
$DC up -d --build --wait && make build
PSQL="$DC exec -T postgres psql -U sard -d sard -At -c"
API=http://localhost:8080/api/v1
source scripts/lib/setup-wizard.sh
SARD_WIZARD_COOKIES=$J sard_complete_wizard http://localhost:8080 qa-admin-password-2026 $DC logs server && echo 204
a() { curl -sS -X "$1" "$API$2" -b $J ${3:+-H 'Content-Type: application/json' -d "$3"} \
  -w '\nHTTP %{http_code} %{content_type}\n'; }
body() { sed '$d'; }
AG=$PWD/agent/bin/sard-agent
$DC exec -T server cat /var/lib/sard/pki/ca/ca.crt > $QA/ca.crt
$DC exec -T server cat /var/lib/sard/pki/ca/ca.key > $QA/ca.key; chmod 0600 $QA/ca.key
GRPC="grpcurl -cacert $QA/ca.crt -import-path proto -proto sard/agent/v1/agent.proto -format-error"
HEX64=$(printf 'a%.0s' $(seq 64))
# host <каталог> — конфиг агента без репозиториев
host() { mkdir -p -m 0700 $1/tls $1/state; cat > $1/agent.yaml <<EOF
server:
  address: localhost:9090
tls:
  ca_file: $1/tls/ca.pem
  cert_file: $1/tls/agent.pem
  key_file: $1/tls/agent.key
executor:
  state_dir: $1/state
EOF
}
enroll_as() { local cmd; cmd=$(a POST /enrollment-tokens "$2" | body | jq -r .enrollCommand)
  $AG ${cmd#sudo sard-agent } --config $1/agent.yaml; }
san() { openssl x509 -in $1 -noout -ext subjectAltName | tail -n +2 | tr ',' '\n' | sed 's/^ *//'; }
cn() { openssl x509 -in $1 -noout -subject | sed 's/.*CN *= *//'; }
serial() { openssl x509 -in $1 -noout -serial | cut -d= -f2 | tr 'A-F' 'a-f'; }
nb() { date -d "$(openssl x509 -in $1 -noout -startdate | cut -d= -f2)" +%s; }
na() { date -d "$(openssl x509 -in $1 -noout -enddate | cut -d= -f2)" +%s; }
pub() { openssl pkey -in $1 -pubout 2>/dev/null || openssl x509 -in $1 -noout -pubkey; }
# reg <сертификат> <ключ> — Register по mTLS
reg() { $GRPC -cert $1 -key $2 -d "{\"hostname\":\"qa\",\"agentVersion\":\"qa\",\"protocolVersion\":1,\"os\":\"linux\",\"arch\":\"amd64\",\"machineId\":\"$HEX64\"}" \
  localhost:9090 sard.agent.v1.AgentService/Register; }
# renew <сертификат> <ключ> <ключ CSR> <выход> — RenewCertificate; цепочка из ответа — в <выход>
renew() { local csr; csr=$(openssl req -new -key $3 -subj /CN=qa -outform DER | base64 -w0)
  $GRPC -cert $1 -key $2 -d "{\"csrDer\":\"$csr\"}" localhost:9090 sard.agent.v1.AgentService/RenewCertificate \
  | tee $QA/renew.json; jq -r .certificateChainPem $QA/renew.json > $4 2>/dev/null; }
confirm() { $GRPC -cert $1 -key $2 -d '{}' localhost:9090 sard.agent.v1.AgentService/ConfirmCertificate; }
newkey() { openssl ecparam -name prime256v1 -genkey -noout | openssl pkcs8 -topk8 -nocrypt -out $1; chmod 0600 $1; }
certrows() { $PSQL "select serial, coalesce(predecessor_serial,'-'), superseded_at is not null, revoked_at is not null from agent_certificates where agent_id = '$1' order by issued_at"; }
# Агент для части 7: регистрируется сейчас и не запускается, к части 7 сертификат истечёт
E=$QA/exp; host $E; enroll_as $E '{}'
```

→ мастер печатает `204`; `make build` завершается; регистрация `$E` — код 0.

## Часть 1. Настройки срока и интервала (П1, П2)

1. `QA_VALIDITY=PT9M59S $DC up -d server; sleep 20; $DC logs server | grep -m1 'sard.pki.agent-certificate-validity'`
   → строка называет настройку, значение `PT9M59S` и минимум `PT10M`;
   `$DC ps server` — контейнер не `healthy` (перезапускается или остановлен).
2. То же для `QA_VALIDITY=PT0S` и `QA_VALIDITY=year` → настройка и значение в
   строке ошибки, сервер не стартует.
3. `QA_MIN_INTERVAL=PT0S $DC up -d server; sleep 20; $DC logs server | grep -m1 'sard.pki.agent-renewal-min-interval'`
   → строка называет настройку и значение `PT0S`; сервер не стартует.
4. `$DC up -d --wait server` (значения по умолчанию override: `PT10M`, `PT1H`)
   → `healthy`.

## Часть 2. Срок при регистрации, status (П1, П26)

```bash
S1=$QA/s1; host $S1; enroll_as $S1 '{}'; XS=$(cn $S1/tls/agent.pem)
cp $S1/tls/agent.pem $QA/c1.pem; cp $S1/tls/agent.key $QA/k1.key
```

5. `echo $(( $(na $S1/tls/agent.pem) - $(nb $S1/tls/agent.pem) ))` → `600`.
6. `$AG status --config $S1/agent.yaml; echo "exit=$?"`
   → шесть строк: `agent_id: $XS`, `tenant_id: …`, `role: source`,
   `certificate_not_after: …`, `certificate_days_left: 0`, `certificate_state: ok`; `exit=0`.
7. `$AG --config $S1/agent.yaml > $QA/s1.out 2>&1 & SP=$!; sleep 5; head -1 $QA/s1.out`
   → `sard-agent <версия>: connecting to localhost:9090 as source`;
   `a GET /agents/$XS | body | jq -r .status` → `online`.

## Часть 3. Продление на двух третях срока (задача 1)

8. Дождаться порога с запасом в минуту:
   `sleep $(( $(nb $QA/c1.pem) + 460 - $(date +%s) )); grep 'certificate renewed' $QA/s1.out`
   → ровно одна строка, в ней `$(serial $QA/c1.pem)` и серийный номер нового сертификата.
9. `ls -l $S1/tls; ls $S1/tls | grep -c '\.new$'`
   → `agent.key` — `-rw-------`, `agent.pem` — `-rw-r--r--`, владелец — проверяющий; `0`.
10. `cmp -s $QA/c1.pem $S1/tls/agent.pem || echo changed; [ "$(pub $QA/k1.key)" != "$(pub $S1/tls/agent.key)" ] && echo newkey; diff <(san $QA/c1.pem) <(san $S1/tls/agent.pem) && echo same-san; cn $S1/tls/agent.pem`
    → `changed`, `newkey`, `same-san` (URI личности `$XS` и `URI:sard://roles/source`), `$XS`.
11. `echo $(( $(na $S1/tls/agent.pem) - $(nb $S1/tls/agent.pem) )); grep -c PRIVATE $QA/s1.out`
    → `600`; `0`.
12. `certrows $XS`
    → две строки: `$(serial $QA/c1.pem)|-|t|f` и `<новый>|$(serial $QA/c1.pem)|f|f`.
13. Поток не переоткрывался: `grep -c 'connecting to' $QA/s1.out; grep -c reconnecting $QA/s1.out`
    → `1`; `0`. `a GET /agents/$XS | body | jq -r .status` → `online`.
14. Старый сертификат вытеснен: `reg $QA/c1.pem $QA/k1.key`
    → `UNAUTHENTICATED`, ровно один `ErrorInfo` с `reason` `CERT_SUPERSEDED`, `domain` `sard.dev`.
15. `$AG status --config $S1/agent.yaml | tail -2` → `certificate_days_left: 0`, `certificate_state: ok`.
16. Переподключение с новым сертификатом:
    `kill $SP; wait $SP; N=$($PSQL "select count(*) from agents"); $AG --config $S1/agent.yaml > $QA/s1.out 2>&1 & SP=$!; sleep 5`
    → `a GET /agents/$XS | body | jq -r '"\(.status) \(.role)"'` → `online source`;
    `$PSQL "select count(*) from agents"` равно `$N`;
    `$DC logs --since 1m server | grep -c 'reason=CERT_'` → `0`.

## Часть 4. Отказы сервера (П4–П12, задача 2)

Отдельный агент `S2` проверяется вызовами `grpcurl`, без процесса агента.

```bash
S2=$QA/s2; host $S2; enroll_as $S2 '{}'; X2=$(cn $S2/tls/agent.pem)
C=$S2/tls/agent.pem; K=$S2/tls/agent.key
```

17. Сразу после регистрации: `newkey $QA/k2.key; renew $C $K $QA/k2.key $QA/c2.pem`
    → `RESOURCE_EXHAUSTED`, `reason` `RENEWAL_TOO_FREQUENT`; в деталях `RetryInfo` с
    `retryDelay` не больше `60s`; `certrows $X2` — одна строка.
18. `sleep 61; renew $C $K $K $QA/x.pem`
    → `INVALID_ARGUMENT`, `reason` `CSR_KEY_REUSED`.
19. `$GRPC -cert $C -key $K -d '{"csrDer":"cXE="}' localhost:9090 sard.agent.v1.AgentService/RenewCertificate`
    → `INVALID_ARGUMENT`, `reason` `CSR_INVALID`.
20. `renew $C $K $QA/k2.key $QA/c2.pem; [ "$(pub $QA/k2.key)" = "$(pub $QA/c2.pem)" ] && echo k2; diff <(san $C) <(san $QA/c2.pem) && echo same-san; grep -c 'BEGIN CERTIFICATE' $QA/c2.pem`
    → ответ с `certificateChainPem`; `k2`; `same-san`; `2` (лист и CA, как у Enroll).
21. Неподтверждённый сертификат: `reg $QA/c2.pem $QA/k2.key` → `UNAUTHENTICATED`, `CERT_NOT_CONFIRMED`.
22. Старый до подтверждения работает: `reg $C $K` → успех, `agentId` = `$X2`.
23. Замена неподтверждённого: `sleep 61; newkey $QA/k3.key; renew $C $K $QA/k3.key $QA/c3.pem; confirm $QA/c2.pem $QA/k2.key`
    → продление успешно; подтверждение `c2` — `UNAUTHENTICATED`, `CERT_SUPERSEDED`;
    `certrows $X2` — три строки, у `c2` вытеснение `t`, у исходного `f`.
24. `confirm $QA/c3.pem $QA/k3.key; T=$($PSQL "select superseded_at from agent_certificates where serial = '$(serial $C)'"); sleep 2; confirm $QA/c3.pem $QA/k3.key; $PSQL "select superseded_at from agent_certificates where serial = '$(serial $C)'"`
    → оба вызова успешны (`{}`); время вытеснения не изменилось и равно `$T`.
25. `reg $C $K; reg $QA/c3.pem $QA/k3.key`
    → первый — `CERT_SUPERSEDED`; второй — успех, `agentId` = `$X2`.
26. Подтверждение сертификата без предшественника: `S5=$QA/s5; host $S5; enroll_as $S5 '{}'; confirm $S5/tls/agent.pem $S5/tls/agent.key; certrows $(cn $S5/tls/agent.pem)`
    → `{}`; одна строка `…|-|f|f`.
27. Чужие сертификаты:
    ```bash
    printf 'subjectAltName=%s\nextendedKeyUsage=clientAuth\n' "$(san $QA/c3.pem | paste -sd,)" > $QA/ext.cnf
    openssl req -new -key $QA/k3.key -subj /CN=$X2 | openssl x509 -req -CA $QA/ca.crt -CAkey $QA/ca.key \
      -set_serial 0x0123456789abcdef0123456789abcdef -days 1 -extfile $QA/ext.cnf -out $QA/unk.pem 2>/dev/null
    renew $QA/unk.pem $QA/k3.key $QA/k2.key $QA/x.pem
    openssl req -x509 -new -key $QA/k3.key -subj /CN=$X2 -days 1 -addext "$(head -1 $QA/ext.cnf)" -out $QA/self.pem
    renew $QA/self.pem $QA/k3.key $QA/k2.key $QA/x.pem
    ```
    → первый — `UNAUTHENTICATED`, `CERT_UNKNOWN`; второй — ошибка рукопожатия TLS
    (`UNAVAILABLE`); `certrows $X2` — по-прежнему три строки.
28. Отзыв: `a POST /agents/$X2/revoke | tail -1; sleep 61; renew $QA/c3.pem $QA/k3.key $QA/k2.key $QA/x.pem`
    → `HTTP 200`; продление — `UNAUTHENTICATED`, `CERT_REVOKED`; `certrows $X2` — у всех строк отзыв `t`.
29. `$DC logs server | grep -E 'CERT_SUPERSEDED|CERT_NOT_CONFIRMED|renew' | tail -5`
    → строки называют agent_id и серийные номера; `$DC logs server | grep -c 'BEGIN '` → `0`.

## Часть 5. Сбой посреди продления оставляет рабочую пару (задача 3)

Перезапуск агента в нужный момент имитируется: новую пару получает `grpcurl`, а
агент находит на диске то, что оставил бы прерванный процесс. Между шагами 30,
31 и 32 должно пройти не меньше минуты (интервал частоты).

30. Сбой после записи новой пары до подтверждения:
    ```bash
    kill $SP; wait $SP; P=$S1/tls
    newkey $P/agent.key.new; renew $P/agent.pem $P/agent.key $P/agent.key.new $P/agent.pem.new; chmod 0644 $P/agent.pem.new
    NEW=$(serial $P/agent.pem.new); OLD=$(serial $P/agent.pem)
    $AG --config $S1/agent.yaml > $QA/s1.out 2>&1 & SP=$!; sleep 5
    ```
    → `serial $P/agent.pem` = `$NEW`; файлов `.new` нет; `certrows $XS` — у `$OLD` вытеснение `t`;
    агент `online`; `$DC logs --since 1m server | grep -c "reason=.*$OLD"` → `0`
    (агент не предъявлял старый сертификат).
31. Сбой между двумя переименованиями (сервер уже подтвердил):
    ```bash
    sleep 61; kill $SP; wait $SP
    newkey $P/agent.key.new; renew $P/agent.pem $P/agent.key $P/agent.key.new $P/agent.pem.new; chmod 0644 $P/agent.pem.new
    confirm $P/agent.pem.new $P/agent.key.new; NEW=$(serial $P/agent.pem.new); mv $P/agent.key.new $P/agent.key
    $AG --config $S1/agent.yaml > $QA/s1.out 2>&1 & SP=$!; sleep 5
    ```
    → `serial $P/agent.pem` = `$NEW`; файлов `.new` нет; ключ подходит к сертификату
    (`[ "$(pub $P/agent.key)" = "$(pub $P/agent.pem)" ] && echo ok` → `ok`); агент `online`.
32. Недописанная новая пара:
    `kill $SP; wait $SP; newkey $P/agent.key.new; $AG --config $S1/agent.yaml > $QA/s1.out 2>&1 & SP=$!; sleep 5`
    → файла `agent.key.new` нет; журнал агента называет путь `$P/agent.key.new`;
    агент `online`, `serial $P/agent.pem` не изменился.
33. Каталог tls без записи (П25, Р8): `chmod 0500 $P` перед очередным порогом
    (`sleep $(( $(nb $P/agent.pem) + 410 - $(date +%s) ))`), затем через 2 минуты
    `$AG status --config $S1/agent.yaml | tail -5`
    → `certificate_state: renewal-failing`, `renewal_failures: ` не меньше 1,
    `renewal_last_error:` называет `$P`, `renewal_next_attempt:` — время в будущем;
    пара в `$P` прежняя; агент `online`. `chmod 0700 $P`; дождаться
    `renewal_next_attempt` → `grep -c 'certificate renewed' $QA/s1.out` → `1`;
    `$AG status --config $S1/agent.yaml | grep state` → `certificate_state: ok`;
    `ls $S1/state/cert-renewal.json` → нет файла.
34. Сервер недоступен на пороге: дождаться следующего порога минус 30 с,
    `$DC stop server`, через 2 минуты `$AG status --config $S1/agent.yaml | grep -E 'state|last_error'`
    → `renewal-failing`, ошибка с `Unavailable`; `$DC start server`; не позже чем
    через 5 минут — новая строка `certificate renewed`, агент `online`.

## Часть 6. Продление во время шага (задача 4)

Нужен репозиторий `qa` у агента `S1` и источник `files` (как в
`docs/qa/agent-role.md`, часть 6). Данные — каталог со случайными файлами
общим объёмом, который агент бэкапит не меньше минуты (например,
`head -c 3G /dev/urandom > $QA/tree/big`).

35. Запустить бэкап источника за 20 с до порога текущего сертификата `S1`
    (`a POST /sources/$S/runs`).
    → в журнале агента строка `certificate renewed` стоит между принятием шага и его
    результатом; запуск доходит до `succeeded`; `grep -c 'connecting to' $QA/s1.out` → `1`,
    `grep -c reconnecting $QA/s1.out` → `0`.

## Часть 7. Истёкший сертификат (задача 5)

К этому моменту с регистрации `$E` в подготовке прошло больше 10 минут.

36. `$AG --config $E/agent.yaml; echo "exit=$?"`
    → одна строка stderr: `sard-agent: …`, содержит `CERT_EXPIRED`, `tls.cert_file`,
    `$E/tls/agent.pem`, `expired at <notAfter в RFC 3339 с Z>`, команду
    `sudo sard-agent enroll --force --config $E/agent.yaml --server localhost:9090 --token <TOKEN>`
    и оканчивается `not reconnecting: fix the cause, then restart the agent`;
    нет `connecting to`; `exit=78`.
37. `$AG status --config $E/agent.yaml; echo "exit=$?"`
    → `certificate_days_left: 0`, `certificate_state: expired`, `re_enroll: ` и та же
    команда; `exit=0`.
38. Команда из шага 37 с новым токеном (без `sudo`, проверяющий — владелец каталога):
    `T=$(a POST /enrollment-tokens '{}' | body | jq -r .token); $AG enroll --force --config $E/agent.yaml --server localhost:9090 --token $T; timeout 8 $AG --config $E/agent.yaml | head -1`
    → регистрация успешна; `connecting to localhost:9090 as source`.
39. Юнит: `grep -E '^(ReadWritePaths|RestartPreventExitStatus|ProtectSystem)=' deploy/agent/sard-agent.service`
    → `ReadWritePaths=/etc/sard/tls`, `RestartPreventExitStatus=78`, `ProtectSystem=strict`.
40. (ВМ с systemd, пакет по `docs/qa/agent-install.md`.) Сертификат агента дать
    истечь при остановленной службе, затем `systemctl start sard-agent; sleep 30; systemctl show sard-agent -p ExecMainStatus -p NRestarts -p ActiveState`
    → `ExecMainStatus=78`, `NRestarts=0`, `ActiveState=failed`;
    `journalctl -u sard-agent | grep -c CERT_EXPIRED` → `1`.
    На той же ВМ с действующим сертификатом и сервером `PT10M` → через 7 минут
    `journalctl -u sard-agent | grep 'certificate renewed'` — строка есть
    (служба пишет в `/etc/sard/tls`).

## Часть 8. Встроенный агент sard-self (задача 7, Р4)

```bash
SELF=$(a GET /agents | body | jq -r '.items[] | select(.builtin and .revokedAt == null) | .id')
RC() { docker inspect -f '{{.RestartCount}}' sard-self-agent-1; }; R0=$(RC)
```

41. Спустя не меньше 7 минут после регистрации sard-self:
    `certrows $SELF; $DC logs self-agent | grep -c 'certificate renewed'`
    → не меньше двух строк, у всех, кроме последней, вытеснение `t`; не меньше `1`;
    `a GET /agents/$SELF | body | jq -r .status` → `online`; `RC` равно `$R0`.
42. Отказ без перезапусков: `a POST "/agents/$SELF/revoke?confirm=sard-self" | tail -1`, через 2 минуты
    `RC; $DC ps self-agent --format '{{.State}}'`
    → `HTTP 200`; `RC` равно `$R0`; `running`;
    `$DC logs self-agent | grep -c 'revoke the built-in agent in the console'` → не меньше `1`
    или уже прошла новая регистрация (шаг 43).
43. Через 3 минуты после шага 42:
    `a GET /agents | body | jq -r '.items[] | select(.builtin and .revokedAt == null) | "\(.id) \(.status)"'`
    → новый id, отличный от `$SELF`, `online`; `RC` по-прежнему `$R0`.
44. Истёкший сертификат соседа: `$DC stop self-agent`, подождать 11 минут, `$DC start self-agent`, через минуту
    `$DC logs --since 2m self-agent | grep -c CERT_EXPIRED; RC; $DC ps self-agent --format '{{.State}}'`
    → `1`; `$R0`; `running`; в строке нет `sudo sard-agent enroll`. Отозвать
    встроенного агента, как в шаге 42 → через 3 минуты новый встроенный агент `online`.

## Часть 9. Уборка

```bash
kill $SP 2>/dev/null; $DC down -v; rm -rf $QA
```

## Не проверяется вручную (тесты сценариев)

- Порог по часам агента, паузы повторов и разброс, задержка `RetryInfo`,
  сброс счётчика (`@renewal`).
- Старый сервер без продления (`UNIMPLEMENTED`, повтор раз в 24 часа) и агент
  предыдущей версии без продления (`@renewal`, `@grpc`).
- Одновременные продления и подтверждения, сбои базы и подписи CA, обрезка по
  сроку CA, округление задержки, границы действующего интервала (`@grpc`, `@unit`).
- Миграция базы предыдущей версии и ограничения предшественника (`@migration`).
- Недоступный сервер при подтверждении на старте, окончательный отказ в
  подтверждении, `CERT_SUPERSEDED` при переподключении (`@start`, `@renewal`).
- Унарные вызовы при открытом потоке не дают отметки дубликата (`@grpc`).
- Сквозные сценарии `@e2e @phase3` — после реализации в фазе 3 в `test/e2e`.
