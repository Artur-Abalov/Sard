# QA: роль агента и идентификатор машины (V1)

Сценарии: `docs/specs/server/agent-role.feature` и
`docs/specs/agent/agent-role.feature`. Решения владельца и specifier (Р1–Р12,
С1–С11) — в заголовках этих файлов; исследование —
`docs/sessions/2026-10-12-v1-agent-role.md`.

Роль в сертификате — второй URI в subjectAltName (`sard://roles/source` или
`sard://roles/verifier`) рядом с URI личности `sard://tenants/<t>/agents/<a>`;
поправка владельца 2026-10-12 к решению 3 заменила прежнее собственное
расширение X.509 (OID с дугой >= 2^31 не разбирает `x509.ParseCertificate` в Go).

Ожидаемый результат указан после «→» в каждом шаге. Любое расхождение — дефект.

Вручную проходятся сквозной путь и сценарии с тегом `@qa`. Управляемые часы,
гонки, порядок проверок S3, StepDispatcher при шаге, вставленном в базу, и
подставной сервер, присылающий бэкап верификатору, проверяют тесты сценариев с
теми же названиями: агент сам дозванивается до сервера, и вручную подменить
сервер на этом стенде нельзя. Консоль (выбор и показ роли) — часть 10 по
`docs/specs/web/agent-role.feature`; её проход против сервера выполняется до
уборки (часть 9).

## Подготовка

Нужны: Docker, Go, `curl`, `jq`, `grpcurl`, `openssl` 3, `python3`. Команды —
из корня репозитория, не от root. Хост, на котором идёт проверка, должен иметь
непустой `/etc/machine-id`.

```bash
make down; rm -f deploy/.env
cp deploy/.env.example deploy/.env
make up && make build
DC="docker compose -f deploy/docker-compose.yml --env-file deploy/.env"
PSQL="$DC exec -T postgres psql -U sard -d sard -At -c"
API=http://localhost:8080/api/v1
QA=$(mktemp -d); J=$QA/jar
source scripts/lib/setup-wizard.sh
SARD_WIZARD_COOKIES=$J sard_complete_wizard http://localhost:8080 qa-admin-password-2026 $DC logs server && echo 204
a() { curl -sS -X "$1" "$API$2" -b $J ${3:+-H 'Content-Type: application/json' -d "$3"} \
  -w '\nHTTP %{http_code} %{content_type}\n'; }
body() { sed '$d'; }
AG=$PWD/agent/bin/sard-agent
$DC exec -T server cat /var/lib/sard/pki/ca/ca.crt > $QA/ca.crt
$DC exec -T server cat /var/lib/sard/pki/ca/ca.key > $QA/ca.key; chmod 0600 $QA/ca.key
GRPC="grpcurl -cacert $QA/ca.crt -import-path proto -proto sard/agent/v1/agent.proto -format-error"
TEN=00000000-0000-0000-0000-000000000001
# EXP — ожидаемый machine_id этого хоста в тенанте по умолчанию (С9)
EXP=$(python3 -c "import hmac,hashlib,sys; print(hmac.new(b'sard-machine-id/v1'+sys.argv[1].encode(), sys.argv[2].encode(), hashlib.sha256).hexdigest())" $TEN "$(tr -d '[:space:]' < /etc/machine-id)")
# host <каталог> — конфиг агента без репозиториев в каталоге
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
# enroll_as <каталог> <тело POST токена> — регистрирует агента в каталоге
enroll_as() { local cmd; cmd=$(a POST /enrollment-tokens "$2" | body | jq -r .enrollCommand)
  $AG ${cmd#sudo sard-agent } --config $1/agent.yaml; }
# san <сертификат> — имена subjectAltName по одному в строке, в порядке сертификата
san() { openssl x509 -in $1 -noout -ext subjectAltName | tail -n +2 | tr ',' '\n' | sed 's/^ *//'; }
# idu <сертификат> — URI личности агента из SAN, где бы он ни стоял
idu() { san $1 | sed -n 's|^URI:\(sard://tenants/.*\)$|\1|p'; }
# forge <каталог агента> <значение subjectAltName> <выход> — поддельный сертификат
# CA сервера с серийным номером настоящего и заданным SAN (порядок имён сохраняется)
forge() { local ser id; ser=$(openssl x509 -in $1/tls/agent.pem -noout -serial | cut -d= -f2)
  id=$(idu $1/tls/agent.pem)
  printf 'subjectAltName=%s\nextendedKeyUsage=clientAuth\n' "$2" > $QA/ext.cnf
  openssl req -new -key $1/tls/agent.key -subj "/CN=${id##*/}" \
  | openssl x509 -req -CA $QA/ca.crt -CAkey $QA/ca.key -set_serial 0x$ser -days 30 \
      -extfile $QA/ext.cnf -out $3 2>/dev/null; }
# reg <сертификат> <ключ> <machine_id> — Register по mTLS
reg() { $GRPC -cert $1 -key $2 -d "{\"hostname\":\"qa\",\"agentVersion\":\"qa\",\"protocolVersion\":1,\"os\":\"linux\",\"arch\":\"amd64\",\"machineId\":\"$3\"}" \
  localhost:9090 sard.agent.v1.AgentService/Register; }
```

→ мастер печатает `204`; `make build` завершается; `echo $EXP` печатает 64
строчных шестнадцатеричных символа.

## Часть 1. Роль токена в REST

1. `a POST /enrollment-tokens '{}' | body | jq -r .role`
   → `source`.
2. `a POST /enrollment-tokens '{"role":"verifier"}' | tee $QA/tv | tail -1; VT=$(body < $QA/tv | jq -r .id); body < $QA/tv | jq -r .role`
   → `HTTP 201`, `verifier`.
3. `a GET /enrollment-tokens/$VT | body | jq -r .role; a GET /enrollment-tokens | body | jq -r ".items[] | select(.id==\"$VT\") | .role"`
   → `verifier` и `verifier`.
4. `body < $QA/tv | jq -r .enrollCommand`
   → `sudo sard-agent enroll --server localhost:9090 --token <token>`, как у токена из шага 1.
5. `N=$($PSQL "select count(*) from enrollment_tokens"); for r in '""' '"SOURCE"' '"Verifier"' '"admin"' 1; do a POST /enrollment-tokens "{\"role\":$r}" | tr '\n' ' ' | grep -o 'HTTP 422\|"field":"role"' | sort -u | tr '\n' ' '; echo; done; $PSQL "select count(*) from enrollment_tokens"`
   → шесть строк `"field":"role" HTTP 422`; `code` каждого — `validation_failed`; число токенов равно `$N`.
6. `$PSQL "select role from enrollment_tokens where builtin"`
   → только `source` (встроенный токен sard-self, если ещё не израсходован, или израсходованный).
7. `$PSQL "insert into enrollment_tokens (id, tenant_id, token_hash, expires_at, created_at, builtin, role) values (gen_random_uuid(), '00000000-0000-0000-0000-000000000001', decode(md5('qa'),'hex'), now() + interval '1 hour', now(), true, 'verifier')"`
   → ошибка нарушения ограничения CHECK; строка не вставлена.

## Часть 2. Регистрация: роль в сертификате, в базе и у агента

```bash
S1=$QA/src; V1=$QA/ver; host $S1; host $V1
enroll_as $S1 '{}'; enroll_as $V1 '{"role":"verifier"}'
XS=$(openssl x509 -in $S1/tls/agent.pem -noout -subject | sed 's/.*CN *= *//')
XV=$(openssl x509 -in $V1/tls/agent.pem -noout -subject | sed 's/.*CN *= *//')
IS=$(idu $S1/tls/agent.pem); IV=$(idu $V1/tls/agent.pem)
```

8. `san $S1/tls/agent.pem; echo; san $V1/tls/agent.pem`
   → первый блок — ровно две строки `URI:sard://tenants/$TEN/agents/$XS` и
   `URI:sard://roles/source` (в любом порядке); второй — `URI:sard://tenants/$TEN/agents/$XV`
   и `URI:sard://roles/verifier`.
9. `san $V1/tls/agent.pem | grep -c '^URI:sard://roles/'; openssl x509 -in $V1/tls/agent.pem -noout -text | grep -c '2\.25\.'`
   → `1` (ровно один URI роли); `0` (собственного расширения роли нет).
10. `$PSQL "select id, role from agents where id in ('$XS','$XV') order by role"`
    → `$XS|source`, `$XV|verifier`.
11. `$AG status --config $S1/agent.yaml; echo "exit=$?"`
    → ровно четыре строки: `agent_id: $XS`, `tenant_id: 00000000-0000-0000-0000-000000000001`,
    `role: source`, `certificate_not_after: <notAfter сертификата в RFC 3339 UTC с Z>`; `exit=0`.
    `openssl x509 -in $S1/tls/agent.pem -noout -enddate` → та же дата.
12. `$AG status --config $V1/agent.yaml | grep role` → `role: verifier`.
13. `host $QA/none; $AG status --config $QA/none/agent.yaml; echo "exit=$?"`
    → stderr `sard-agent: not enrolled: run sard-agent enroll`; stdout пуст; `exit=1`.
14. `$AG status; echo "exit=$?"` → `exit=2`.
15. Запуск обоих агентов:
    `$AG --config $S1/agent.yaml > $QA/s.out 2>&1 & SP=$!; $AG --config $V1/agent.yaml > $QA/v.out 2>&1 & VP=$!; sleep 5; head -1 $QA/s.out; head -1 $QA/v.out`
    → `sard-agent <версия>: connecting to localhost:9090 as source` и `… as verifier`.
16. `a GET /agents | body | jq -r ".items[] | select(.id==\"$XS\" or .id==\"$XV\") | \"\(.id) \(.role) \(.status)\""`
    → `$XS source online`, `$XV verifier online`; `a GET /agents/$XV | body | jq -r .role` → `verifier`.
17. `a GET /agents | body | jq -r '.items[] | select(.builtin) | .role'`
    → `source` (встроенный агент sard-self).

## Часть 3. Роль не берётся из сообщений агента

18. CSR, в SAN которого `sard://roles/verifier` и URI личности верификатора, по токену source:

    ```bash
    openssl ecparam -name prime256v1 -genkey -noout -out $QA/k
    printf '[req]\ndistinguished_name=dn\nreq_extensions=ext\n[dn]\n[ext]\nsubjectAltName=URI:sard://roles/verifier,URI:%s\n' "$IV" > $QA/csr.cnf
    CSR=$(openssl req -new -key $QA/k -subj "/CN=verifier/OU=verifier" -config $QA/csr.cnf -outform DER | base64 -w0)
    T=$(a POST /enrollment-tokens '{}' | body | jq -r .token)
    $GRPC -d "{\"enrollment_token\":\"$T\",\"csr_der\":\"$CSR\",\"hostname\":\"qa-forge\"}" localhost:9090 sard.agent.v1.EnrollmentService/Enroll | tee $QA/f.json
    jq -r .certificateChainPem $QA/f.json > $QA/f.pem; san $QA/f.pem
    ```

    → регистрация успешна; ровно две строки: `URI:sard://tenants/$TEN/agents/<agentId из $QA/f.json>`
    и `URI:sard://roles/source`; `san $QA/f.pem | grep -c "verifier\|$XV"` → `0`;
    `$PSQL "select role from agents where id = '$(jq -r .agentId $QA/f.json)'"` → `source`.
19. В API нет операции изменения роли:
    `curl -s http://localhost:8080/v3/api-docs | jq -r '.components.schemas | to_entries[] | select(.value.properties.role) | .key'`
    → ровно четыре схемы: `AgentDetails`, `AgentSummary`, `CreateEnrollmentTokenRequest`,
    `EnrollmentToken`; из них тело запроса — только `CreateEnrollmentTokenRequest`.

## Часть 4. Проверка S3: сертификат без роли или с чужой ролью

В каждом шаге: ответ `UNAUTHENTICATED`, ровно один `ErrorInfo` с указанным
`reason` и `domain` = `sard.dev`.

20. `forge $S1 "URI:$IS" $QA/nr.pem; reg $QA/nr.pem $S1/tls/agent.key $(printf 'a%.0s' $(seq 64))`
    → `CERT_ROLE_MISSING`.
21. `forge $S1 "URI:$IS,URI:sard://roles/marker-ROLE-7f" $QA/bad.pem; reg $QA/bad.pem $S1/tls/agent.key $(printf 'a%.0s' $(seq 64))`
    → `CERT_ROLE_INVALID`; `$DC logs server | grep reason=CERT_ROLE_INVALID | tail -1`
    → строка называет `$XS` и тенант по умолчанию; `$DC logs server | grep -c marker-ROLE-7f` → `0`.
22. Недопустимые URI роли:

    ```bash
    for s in "URI:$IS,URI:sard://roles/source,URI:sard://roles/source" \
             "URI:$IS,URI:sard://roles/source,URI:sard://roles/verifier" \
             "URI:$IS,URI:sard://roles/source/" "URI:$IS,URI:sard://roles/Source" \
             "URI:$IS,URI:sard://roles/source?x=1" "URI:$IS,URI:sard://roles:443/source"; do
      forge $S1 "$s" $QA/x.pem; reg $QA/x.pem $S1/tls/agent.key $(printf 'a%.0s' $(seq 64)) 2>&1 | grep -o 'CERT_[A-Z_]*' | sort -u
    done
    ```

    → шесть строк `CERT_ROLE_INVALID`.
23. `forge $S1 "URI:$IS,URI:sard://roles/verifier" $QA/sv.pem; reg $QA/sv.pem $S1/tls/agent.key $(printf 'a%.0s' $(seq 64))`
    → `CERT_IDENTITY_MISMATCH` (агент-источник с сертификатом роли verifier).
24. `forge $V1 "URI:$IV,URI:sard://roles/source" $QA/vs.pem; reg $QA/vs.pem $V1/tls/agent.key $(printf 'a%.0s' $(seq 64))`
    → `CERT_IDENTITY_MISMATCH`.
25. Порядок имён в SAN не значим — URI роли первым:
    `forge $V1 "URI:sard://roles/verifier,URI:$IV" $QA/rf.pem; san $QA/rf.pem; reg $QA/rf.pem $V1/tls/agent.key $EXP`
    → `san` печатает сначала `URI:sard://roles/verifier`, затем `URI:$IV`; Register успешен;
    `$PSQL "select hostname, machine_id from agents where id = '$XV'"` → `qa|$EXP`.
26. После шагов 20–25: `$PSQL "select role from agents where id in ('$XS','$XV') order by role"`
    → `source`, `verifier` — не изменились.

## Часть 5. Агент с сертификатом без допустимой роли не стартует

```bash
kill $SP; wait $SP 2>/dev/null
cp $S1/tls/agent.pem $QA/good.pem
```

27. `cp $QA/nr.pem $S1/tls/agent.pem; timeout 10 $AG --config $S1/agent.yaml; echo "exit=$?"`
    → одна строка stderr `sard-agent: …`, содержит `CERT_ROLE_MISSING`, `tls.cert_file`,
    путь `$S1/tls/agent.pem` и `sard-agent enroll --force`; нет `connecting to`; `exit=1`.
28. `cp $QA/bad.pem $S1/tls/agent.pem; timeout 10 $AG --config $S1/agent.yaml 2>$QA/e; echo "exit=$?"; cat $QA/e`
    → `CERT_ROLE_INVALID`; `exit=1`; `grep -c marker-ROLE-7f $QA/e` → `0`.
29. `$AG status --config $S1/agent.yaml; echo "exit=$?"`
    → одна строка stderr с `CERT_ROLE_INVALID` и `sard-agent enroll --force`; stdout пуст; `exit=1`.
    То же с двумя URI роли: `forge $S1 "URI:$IS,URI:sard://roles/source,URI:sard://roles/source" $S1/tls/agent.pem; $AG status --config $S1/agent.yaml; echo "exit=$?"`
    → `CERT_ROLE_INVALID`; stdout пуст; `exit=1`.
30. Статус при URI роли перед URI личности (шаг 25), в отдельном каталоге:
    `host $QA/rf; cp $V1/tls/ca.pem $QA/rf/tls/; cp $QA/rf.pem $QA/rf/tls/agent.pem; $AG status --config $QA/rf/agent.yaml; echo "exit=$?"`
    → `agent_id: $XV`, `tenant_id: $TEN`, `role: verifier`, `certificate_not_after: …`; `exit=0`.
31. `cp $QA/good.pem $S1/tls/agent.pem; $AG --config $S1/agent.yaml > $QA/s.out 2>&1 & SP=$!; sleep 5; head -1 $QA/s.out`
    → `… as source`; агент `online`.

## Часть 6. Источник привязывается только к агенту-источнику

```bash
mkdir -p $QA/tree; echo hi > $QA/tree/a.txt
SRC="{\"name\":\"etc\",\"agentId\":\"$XS\",\"plugin\":\"files\",\"repositoryName\":\"qa\",\"config\":{\"paths\":[\"$QA/tree\"]}}"
```

Для источника нужен репозиторий `qa` у обоих агентов: добавить в оба конфига
раздел `repositories` (как в `docs/qa/rest-api.md`, часть 3, общий `password_file`
с правами 0600), инициализировать репозиторий и перезапустить оба агента.

32. `a POST /sources "${SRC/$XS/$XV}"`
    → `HTTP 422`, `code` `agent_role_mismatch`, `errors[0].field` = `agentId`;
    `$PSQL "select count(*) from sources"` не изменилось.
33. `a POST /sources "{\"name\":\"x\",\"agentId\":\"$XV\",\"plugin\":\"postgres\",\"repositoryName\":\"offsite\",\"config\":{}}"`
    → `HTTP 422`, `agent_role_mismatch`, поле `agentId` (роль проверяется раньше плагина и репозитория).
34. `a POST /sources "$SRC" | tee $QA/s1 | tail -1; S=$(body < $QA/s1 | jq -r .id)`
    → `HTTP 201`.
35. `a PUT /sources/$S "${SRC/$XS/$XV}"`
    → `HTTP 422`, `agent_role_mismatch`, поле `agentId`; `a GET /sources/$S | body | jq -r .agentId` → `$XS`.
36. Обход API: `$PSQL "update sources set agent_id = '$XV' where id = '$S'"; a POST /sources/$S/runs`
    → `HTTP 409`, `code` `agent_role_mismatch`; `a GET "/runs?sourceId=$S" | body | jq '.items|length'` → `0`;
    `grep -c 'step accepted' $QA/v.out` → `0`.
37. `$PSQL "update sources set agent_id = '$XS' where id = '$S'"; a POST /sources/$S/runs`
    → `HTTP 201`; запуск доходит до `succeeded`; `grep -c 'step accepted' $QA/v.out` → `0`.

## Часть 7. Идентификатор машины

`TEN` и `EXP` заданы в подготовке.

38. `$PSQL "select machine_id from agents where id = '$XS'"; $PSQL "select machine_id from agents where id = '$XV'"; echo $EXP`
    → три одинаковые строки из 64 строчных шестнадцатеричных символов
    (два агента разных ролей одной машины — сигнал «один хост» для V4).
39. `[ "$EXP" != "$(tr -d '[:space:]' < /etc/machine-id)" ] && echo differs; $PSQL "select count(*) from agents where machine_id like '%$(tr -d '[:space:]' < /etc/machine-id)%'"`
    → `differs`; `0`.
40. `kill $SP; wait $SP; $AG --config $S1/agent.yaml > $QA/s.out 2>&1 & SP=$!; sleep 5; $PSQL "select machine_id from agents where id = '$XS'"`
    → по-прежнему `$EXP`.
41. `a GET /agents/$XS | body | grep -c "$EXP"; a GET /agents | body | grep -c machineId; grep -c "$EXP" $QA/s.out; $DC logs server | grep -c "$EXP"`
    → `0`, `0`, `0`, `0`.
42. Пустой machine_id: `reg $QA/good.pem $S1/tls/agent.key ""`
    → `INVALID_ARGUMENT`, `reason` `FIELD_INVALID`, metadata `field=machine_id`;
    `$PSQL "select machine_id, hostname from agents where id = '$XS'"` не изменились.
    То же с `MARKER-MID-0001` → `FIELD_INVALID`; `$DC logs server | grep -c MARKER-MID-0001` → `0`.
43. Хост без machine-id — агент в контейнере:

    ```bash
    C=$QA/ctr; host $C; enroll_as $C '{}'; sed -i "s|$C|/h|g" $C/agent.yaml
    # контейнер запускается от root, чтобы удалить machine-id образа, затем агент
    # работает от uid проверяющего (права файлов $C проходят проверку A1)
    scripts/fetch-restic.sh; RV=$(sed -n 's/^version=//p' agent/internal/restic/restic-version)
    RB=$PWD/.bin/restic/$RV/linux_$(go env GOARCH)/restic   # restic рядом с агентом, ADR 0017
    run_ctr() { docker run --rm --network host -v $AG:/sard-agent:ro -v $RB:/restic:ro -v $C:/h ubuntu:24.04 \
      sh -c "rm -f /etc/machine-id /var/lib/dbus/machine-id; exec setpriv --reuid=$(id -u) --regid=$(id -g) --clear-groups timeout 8 /sard-agent --config /h/agent.yaml"; }
    run_ctr; ls -l $C/state/machine-id; cat $C/state/machine-id; echo
    ```

    Каталог `$C` смонтирован в `/h`, поэтому пути `tls.*` и `state_dir` в конфиге
    заменены на `/h/…`.
    → `-rw-------` (0600); 64 строчных шестнадцатеричных символа без перевода строки;
    `$PSQL "select machine_id from agents where id = '$(openssl x509 -in $C/tls/agent.pem -noout -subject | sed 's/.*CN *= *//')'"`
    → 64 hex, отличается от `$EXP` и от содержимого `$C/state/machine-id`.
44. `M1=$(cat $C/state/machine-id); run_ctr; [ "$(cat $C/state/machine-id)" = "$M1" ] && echo same`
    → `same`; `machine_id` агента в базе не изменился.
45. `printf not-hex > $C/state/machine-id; run_ctr; echo "exit=$?"`
    → одна строка stderr с путём `/h/state/machine-id`; `exit=1`; файл по-прежнему `not-hex`.

## Часть 8. Встроенный агент после повторной регистрации

46. Отозвать sard-self с подтверждением (`docs/qa/self-agent.md`), дождаться новой
    регистрации соседа → `a GET /agents | body | jq -r '.items[] | select(.builtin and .revokedAt == null) | .role'`
    → `source`; `$DC logs self-agent | grep 'connecting to' | tail -1` → `… as source`.

## Часть 9. Уборка

```bash
kill $SP $VP 2>/dev/null; rm -rf $QA; make down
```

## Не проверяется вручную (тесты сценариев)

- Шаг не своей роли, присланный подставным сервером (`@transport`, «Подставной
  сервер не может заставить верификатор сделать бэкап»), и все сценарии `@executor`.
- StepDispatcher при шаге, вставленном в базу правкой (`@grpc`, «StepDispatcher
  не отправляет шаг, который не допускает роль агента»).
- Миграция базы предыдущей версии (`@migration`) и отказ `CERT_ROLE_MISSING`
  агенту, зарегистрированному до V1.
- Сквозной сценарий стенда `@e2e @phase3` — после реализации в фазе 3
  запускается в `test/e2e`.

## Часть 10. Консоль

Сценарии: `docs/specs/web/agent-role.feature` (утверждена владельцем
2026-10-14). Пометка `[РК…]` называет решение из её заголовка. Проход против
сервера идёт на стенде частей 2–8 **до уборки** (часть 9): нужны `$XS`
(источник, онлайн), `$XV` (верификатор, онлайн, с плагином `files` и
репозиторием `qa` из части 6), источник `$S` агента `$XS`. Оба агента на одном
хосте, поэтому имя хоста у них может совпадать: в списках токенов и агентов они
различаются колонкой роли, а в выборе агента формы источника `$XV` нет вовсе.
Проход на моках: `cd web && VITE_API_MOCKS=1 npm run dev`; против сервера:
`cd web && npm run dev`. Консоль — `http://localhost:5173`, DevTools → Network,
фильтр `api/v1`. Отличия проходов отмечены «(моки: …)»; на моках `$XV` —
агент-верификатор из фикстур, `$XS` — любой неотозванный агент-источник фикстур.

47. `cd web && npm test -- --reporter=verbose 2>&1 | tee $QA/vt; npx tsc --noEmit`
    → всё зелёное; `grep -c 'AgentRole' $QA/vt` ≥ 1; в выводе найдены названия
    всех сценариев `@vitest` спецификации: двух правила «Подписи роли», «Ошибка
    сервера у поля role относится к выбору роли токена», «Выбор агента в форме
    источника предлагает только неотозванных агентов-источников», «Выбор агента
    сохраняет порядок списка агентов», «Если все неотозванные агенты —
    верификаторы, выбор агента пуст», «Кнопка создания источника на карточке
    агента есть только у агента-источника»; тесты `@mocks` из той же
    спецификации — в `src/mocks/handlers.test.ts`, зелёные.
    `grep -c 'agentOption' src/locales/ru.json src/locales/en.json` → `0` в обоих.
48. Токены → «Создать токен» → выбор «Роль агента» показывает «источник», в
    списке ровно «источник» и «верификатор», под ним подсказка о том, что роль
    записывается в сертификат и не меняется `[РК2]`.
49. Создать токен, роль не меняя → тело `POST enrollment-tokens` содержит
    `"role":"source"`; окно после первого `GET` токена показывает «Роль агента:
    источник» `[РК4]`. Закрыть окно.
50. Создать токен с ролью «верификатор» → тело содержит `"role":"verifier"`; окно —
    «Роль агента: верификатор»; после закрытия первая строка списка — новый
    токен, колонка «Роль агента» — «верификатор» `[РК3]`.
    Против сервера: `a GET /enrollment-tokens | body | jq -r '.items[0].role'` → `verifier`.
51. DevTools → Network → Offline; выбрать «верификатор», «Создать» → сообщение о
    недоступности сервера, выбор по-прежнему «верификатор»; Online, «Создать»
    ещё раз → тело содержит `"role":"verifier"`.
52. Агенты → колонка «Роль»: `$XS` — «источник», `$XV` — «верификатор»,
    встроенный sard-self — «источник» `[РК5]`. (моки: верификатор — агент из
    фикстур; отозванный верификатор задаётся правкой состояния мока.)
53. Карточка `$XV` → в таблице фактов «Роль» — «верификатор». Переключить язык на
    EN → «Role» — «verifier» и в списке, и в карточке; вернуть RU.
54. На странице агентов и карточке нет поля идентификатора машины;
    в Network → поиск `machine` по ответам и запросам → ничего.
55. Источники → «Создать источник» → открыть выбор агента `[РК6]` → число
    вариантов равно выводу
    `a GET /agents | body | jq '[.items[] | select(.role=="source" and .revokedAt==null)] | length'`;
    текст каждого варианта — только имя хоста, без «— источник», «— верификатор»
    и без слов «источник»/«верификатор». Выбрать по очереди каждый вариант →
    в Network запросы `GET /agents/<id>` идут только с id агентов-источников,
    ни разу с `$XV`. (моки: имени хоста верификатора из фикстур среди вариантов
    нет; число вариантов — неотозванные агенты-источники фикстур.)
56. Открыть адресом `http://localhost:5173/sources/new?agentId=$XV` (подставить
    значение `$XV`) → форма открыта, в выборе агента варианта `$XV`
    по-прежнему нет. Заполнить имя `qa-role`, плагин `files`, репозиторий `qa`,
    путь `$QA/tree`; `N=$($PSQL "select count(*) from sources")`; «Сохранить» →
    тело `POST sources` содержит `"agentId":"$XV"`; ответ 422
    `agent_role_mismatch`, `errors[0].field` = `agentId`; под выбором агента
    «Агент — верификатор и не выполняет бэкап», над формой этого текста нет;
    значения в форме, адрес не сменился. Против сервера:
    `$PSQL "select count(*) from sources"` → `$N`.
57. Карточка `$XV` → в блоке источников нет кнопки «Создать источник» `[РК7]`.
    Карточка `$XS` → «Создать источник» есть; нажать → форма с уже выбранным
    `$XS`. Агенты → отозванный агент-верификатор, если он есть на стенде
    (моки: задаётся правкой состояния мока, как в шаге 52), → кнопки тоже нет.
58. (Только против сервера.) `$PSQL "update sources set agent_id = '$XV' where id = '$S'"`;
    карточка `$S` → «Изменить» → в выборе агента варианта `$XV` нет; «Сохранить»
    → тело `PUT sources/$S` содержит `"agentId":"$XV"`; ответ 422
    `agent_role_mismatch`, `errors[0].field` = `agentId`; сообщение «Агент —
    верификатор и не выполняет бэкап» показано один раз — под выбором агента;
    адрес не сменился; `a GET /sources/$S | body | jq -r .agentId` → `$XV`
    (не изменился). PUT на моках проверяет сценарий `@mocks`.
59. (Только против сервера, привязка из шага 58 ещё действует.) Карточка `$S` →
    «Запустить бэкап» → ответ 409 `agent_role_mismatch`; консоль показывает
    «Агент — верификатор и не выполняет бэкап», перехода в карточку запуска нет.
    Вернуть: `$PSQL "update sources set agent_id = '$XS' where id = '$S'"`;
    `a GET /sources/$S | body | jq -r .agentId` → `$XS`.
