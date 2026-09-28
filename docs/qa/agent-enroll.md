# QA: команда `sard-agent enroll` (A2b)

Сценарии: `docs/specs/agent/agent-enroll.feature`. Серверный контракт Enroll —
`docs/specs/server/agent-enrollment.feature`, формат токена —
`docs/specs/enrollment-token.md`. Решения владельца, классы кодов выхода и
открытые вопросы — в заголовке спецификации.

**Выполнима только после слияния A2a и A2b.** Шаги с пометкой `[qN]` зависят от
открытого вопроса N и уточняются после ответа владельца.

Ожидаемый результат указан после «→» в каждом шаге. Любое расхождение — дефект.
«Хост не изменён» — вывод `snap` совпадает с сохранённым до шага.

## Подготовка

Нужны: Docker, Go, `grpcurl`, `openssl`, `python3`, `jq`, `nc`. Команды
выполняются из корня репозитория.

1. Выполнить блок «Подготовка» из `docs/qa/agent-enrollment.md` (`make up`,
   переменные `DC`, `PSQL`, `DEFAULT`, `QA`, `F`, функции `new_token`,
   `hash_of`, `token_row`). Сервер поднят с именами `localhost,127.0.0.1,::1`,
   `SARD_AGENT_ENDPOINT` не задан — команда консоли была бы
   `sard-agent enroll --server localhost:9090 --token <строка>`.
2. Собрать агента и подготовить «хост»:

```bash
make build
AG=$PWD/agent/bin/sard-agent
H=$(mktemp -d); mkdir -m 0700 "$H/tls"
cat > "$H/agent.yaml" <<EOF
server:
  address: localhost:9090
tls:
  ca_file: $H/tls/ca.pem
  cert_file: $H/tls/agent.pem
  key_file: $H/tls/agent.key
EOF
# enroll [флаги…] — печатает stdout, stderr и код выхода, сохраняет вывод в $QA/out
enroll() { "$AG" enroll --config "$H/agent.yaml" --server localhost:9090 "$@" >"$QA/out" 2>"$QA/err"; echo "exit=$?"; cat "$QA/out" "$QA/err"; cat "$QA/out" "$QA/err" >> "$QA/all"; }
snap() { find "$H/tls" -mindepth 1 -printf '%m %u %p\n' | sort; sha256sum "$H"/tls/* "$H/agent.yaml" 2>/dev/null; }
agents() { $PSQL "select count(*) from agents"; }
spki() { openssl x509 -in "$1" -pubkey -noout | openssl pkey -pubin -outform DER | sha256sum | cut -c1-64; }
```

3. `"$AG" enroll --help; echo "exit=$?"`
   → `exit=0`; справка называет `--server`, `--token`, `--token-file`,
   `--force`, `--config`, флаг таймаута, переменную `SARD_ENROLL_TOKEN` и
   номер и смысл кода выхода каждого класса; флага отключения проверки
   сервера нет. Записать номера классов в переменные `E_USAGE`, `E_TOKEN`,
   `E_IDENTITY`, `E_TRUST`, `E_TEMP`, `E_WRITE`, `E_AGENT` → все номера
   различны, успех — `0` `[q3]`.

## Часть 1. Отказы до обращения к серверу

До шага 18 токен `T` не должен расходоваться. Перед частью:
`T=$(new_token $DEFAULT "now() + interval '1 hour'"); A0=$(agents); snap > "$QA/empty"`.
В каждом шаге этой части дополнительно: `token_row "$T"` → `-|-`,
`agents` → `$A0`, `snap | diff - "$QA/empty"` → пусто.

4. `enroll` (без токена) → `exit=$E_USAGE`; сообщение перечисляет `--token`,
   `--token-file`, `SARD_ENROLL_TOKEN`.
5. `SARD_ENROLL_TOKEN="$T" enroll --token "$T"` → `exit=$E_USAGE`; сообщение
   называет оба источника.
6. `printf '%s' "$T" > "$QA/tok"; enroll --token "$T" --token-file "$QA/tok"` → `exit=$E_USAGE`.
7. `enroll --token-file "$QA/nope"` → `exit=$E_USAGE`; сообщение называет `$QA/nope`.
8. `: > "$QA/empty.tok"; enroll --token-file "$QA/empty.tok"` → `exit=$E_USAGE`,
   «файл токена пуст» `[q11]`.
9. `enroll --token nonsense` → `exit=$E_USAGE`; в сообщении `TOKEN_MALFORMED`
   и «повтор этой строкой не поможет».
10. `enroll --token "${T%?}"` (без последнего символа) → `exit=$E_USAGE`, `TOKEN_MALFORMED`.
11. `printf ' %s' "$T" > "$QA/sp.tok"; enroll --token-file "$QA/sp.tok"`
    → `exit=$E_USAGE`, `TOKEN_MALFORMED` `[q11]`.
12. `enroll --token "$T" --insecure` → `exit=$E_USAGE`.
13. `"$AG" enroll --config "$H/agent.yaml" --server localhost --token "$T"; echo "exit=$?"`
    (без порта) → `exit=$E_USAGE`; сообщение называет `localhost` `[q10]`.
14. `"$AG" enroll --config "$QA/nope.yaml" --server localhost:9090 --token "$T"; echo "exit=$?"`
    → `exit=$E_USAGE`; сообщение называет путь `[q7]`.
15. `"$AG" enroll --config "$H/agent.yaml" --server 127.0.0.1:9090 --token "$T"; echo "exit=$?"`
    (в конфиге `localhost:9090`) → `exit=$E_USAGE`; сообщение называет
    `localhost:9090` и `127.0.0.1:9090`. То же с `--force` → тот же результат.
16. `enroll --token "$T" --timeout 0s` (имя флага — из справки) → `exit=$E_USAGE`.
17. `chmod 0500 "$H/tls"; enroll --token "$T"; chmod 0700 "$H/tls"`
    → `exit=$E_WRITE`; сообщение называет `$H/tls` `[q12]`.

### Доверие к серверу

18. `enroll --token "${T%.*}.8544e2352a80a3d403eed68f8bb4ff271d0ce02c09c1d0faf6f090cea423be9f"`
    (секрет `T`, отпечаток тестового вектора) → `exit=$E_TRUST`; сообщение
    называет обе причины — токен от другого сервера или подмена соединения —
    и не предлагает обход; `token_row "$T"` → `-|-`.
19. Несовпадение имени: `sed 's/localhost:9090/127.0.0.2:9090/' "$H/agent.yaml" > "$QA/ip2.yaml"`;
    `"$AG" enroll --config "$QA/ip2.yaml" --server 127.0.0.2:9090 --token "$T"; echo "exit=$?"`
    (порт сервера опубликован на всех адресах; `127.0.0.2` нет в сертификате)
    → `exit=$E_TRUST`; сообщение называет `127.0.0.2` и говорит, что адрес
    должен совпадать с `SARD_AGENT_ENDPOINT` или именами сертификата
    (перечисляет `localhost`, `127.0.0.1`, `::1` `[q17]`); `token_row "$T"` → `-|-`.

### Сервер недоступен, таймаут

20. `sed 's/localhost:9090/localhost:9/' "$H/agent.yaml" > "$QA/p9.yaml"`;
    `"$AG" enroll --config "$QA/p9.yaml" --server localhost:9 --token "$T"; echo "exit=$?"`
    → `exit=$E_TEMP`; сообщение называет `localhost:9` и «токен цел, можно повторить».
21. `nc -lk 127.0.0.1 19999 & NC=$!`;
    `sed 's/localhost:9090/127.0.0.1:19999/' "$H/agent.yaml" > "$QA/nc.yaml"`;
    `time "$AG" enroll --config "$QA/nc.yaml" --server 127.0.0.1:19999 --token "$T" --timeout 3s; echo "exit=$?"; kill $NC`
    → завершается примерно через 3 с, `exit=$E_TEMP`.
22. Команда не ждёт ввода: `sleep 600 | "$AG" enroll --config "$H/agent.yaml" --server localhost:9090 --token nonsense; echo "exit=$?"`
    → завершается сразу, `exit=$E_USAGE`.

## Часть 2. Успех

23. `printf '%s\n' "$T" > "$QA/tok"; enroll --token-file "$QA/tok"` `[q11]`
    → `exit=0`; stdout содержит `agent_id` (далее `X`), `localhost:9090`, пути
    `$H/tls/agent.key`, `$H/tls/agent.pem`, `$H/tls/ca.pem` и совет запустить
    или перезапустить службу агента.
24. `stat -c '%a %U' "$H"/tls/*` → ключ `600`, сертификат и бандл `644` `[q19]`;
    владелец — `$(id -un)`.
25. `openssl x509 -in "$H/tls/agent.pem" -pubkey -noout | sha256sum` и
    `openssl pkey -in "$H/tls/agent.key" -pubout | sha256sum` → совпадают.
26. `spki "$H/tls/ca.pem"` → `$F`;
    `openssl verify -CAfile "$H/tls/ca.pem" "$H/tls/agent.pem"` → `OK`.
27. `openssl x509 -in "$H/tls/agent.pem" -noout -ext subjectAltName`
    → `URI:sard://tenants/00000000-0000-0000-0000-000000000001/agents/X`.
28. `token_row "$T"` → `used_at` заполнено, `agent_id` = `X`;
    `$PSQL "select hostname from agents where id = 'X'"` → `$(hostname)`.
29. `grpcurl -cacert "$H/tls/ca.pem" -cert "$H/tls/agent.pem" -key "$H/tls/agent.key" -import-path proto -proto sard/agent/v1/agent.proto -d '{}' localhost:9090 sard.agent.v1.AgentService/Register`
    → не `UNAUTHENTICATED` (на момент написания — `UNIMPLEMENTED` до S4/S5).
30. `ls -A "$H/tls"` → ровно `agent.key agent.pem ca.pem`. `snap > "$QA/id1"`.

## Часть 3. Существующая идентичность и `--force`

31. `T2=$(new_token $DEFAULT "now() + interval '1 hour'"); enroll --token "$T2"`
    → `exit=$E_IDENTITY`; сообщение называет `X` и `localhost:9090`, `--force`
    (новая личность) и что перерегистрация с сохранением личности пока
    недоступна; `snap | diff - "$QA/id1"` → пусто; `token_row "$T2"` → `-|-`.
32. `enroll --force --token "$T"` (использованный токен) → `exit=$E_TOKEN`;
    `TOKEN_USED`, «нужен новый токен»; `snap | diff - "$QA/id1"` → пусто.
33. `enroll --force --token "$T2"` → `exit=0`; новый `agent_id` `Y` ≠ `X`
    (stdout называет `X` как прежнюю личность `[q18]`); ключ изменился
    (`sha256sum "$H/tls/agent.key"` ≠ из `$QA/id1`), права ключа `600`;
    `ls -A "$H/tls"` → ровно три файла, резервных копий нет;
    `$PSQL "select count(*) from agents where id in ('X','Y')"` → `2`.
    `snap > "$QA/id2"`.

## Часть 4. Отказы настоящего сервера

В каждом шаге — `--force`, чтобы дойти до сервера; после шага
`snap | diff - "$QA/id2"` → пусто.

34. `U=$(python3 -c "import os,base64; print('sard_'+base64.urlsafe_b64encode(os.urandom(32)).rstrip(b'=').decode()+'.$F')"); enroll --force --token "$U"`
    → `exit=$E_TOKEN`, `TOKEN_UNKNOWN`, «повтор не поможет».
35. `T3=$(new_token $DEFAULT "now() - interval '1 second'" "now() - interval '1 hour'"); enroll --force --token "$T3"`
    → `exit=$E_TOKEN`, `TOKEN_EXPIRED`.
36. `T4=$(new_token $DEFAULT "now() + interval '1 hour'"); $PSQL "update enrollment_tokens set revoked_at = now() where token_hash = decode('$(hash_of "$T4")','hex')"; enroll --force --token "$T4"`
    → `exit=$E_TOKEN`, `TOKEN_REVOKED`.
37. `T5=$(new_token $DEFAULT "now() + interval '1 hour'"); $DC stop postgres; enroll --force --token "$T5"`
    → `exit=$E_TEMP`, `INTERNAL_RETRYABLE`, «временная проблема, токен цел,
    можно повторить»; команда завершается сразу после ответа, без пауз на
    повторы (точное число вызовов проверяет тест «Команда не повторяет
    регистрацию после временной ошибки»).
38. `$DC start postgres`, дождаться `healthy`; `token_row "$T5"` → `-|-`;
    `enroll --force --token "$T5"` → `exit=0`.

`CSR_INVALID`, `HOSTNAME_INVALID`, непредусмотренные ответы сервера, сбой
записи после успешной регистрации, таймаут после отправки запроса и
одновременные запуски вручную не воспроизводятся — их проверяют тесты
сценариев `@fake` с этими названиями.

## Часть 5. Секреты не утекают

39. Для каждой строки токена из этой процедуры (`$T`, `$T2` … `$T5`, `$U`) и её
    секрета (`s=${tok#sard_}; s=${s%%.*}`): `grep -cF "<строка или секрет>" "$QA/all"` → `0`.
40. `grep -c 'PRIVATE KEY' "$QA/all"` → `0`.

## Часть 6. От имени пользователя службы (хост с пакетом deb/rpm)

41. `sudo install -d -o sard-agent -g sard-agent -m 0700 /etc/sard/tls`, в
    `/etc/sard/agent.yaml` пути `tls.*` — в `/etc/sard/tls` `[q20]`;
    `sudo -u sard-agent sard-agent enroll --server <адрес> --token <строка>`
    (команда из консоли, без `--config` `[q7]`) → `exit=0`; владелец файлов —
    `sard-agent`, ключ `600`.
42. `sudo systemctl restart sard-agent` → служба активна, в журнале нет
    отказа по сертификату.
