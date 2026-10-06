# QA: команда `sard-agent enroll` (A2b)

Сценарии: `docs/specs/agent/agent-enroll.feature` (утверждены владельцем
2026-09-28). Серверный контракт Enroll — `docs/specs/server/agent-enrollment.feature`,
формат токена — `docs/specs/enrollment-token.md`. Решения владельца и классы
кодов выхода — в заголовке спецификации.

Выполнима после реализации A2a (вместе с проверкой прав секретных файлов при
старте агента, A1, @a1) и A2b.

Ожидаемый результат указан после «→» в каждом шаге. Любое расхождение — дефект.
«Хост не изменён» — вывод `snap` совпадает с сохранённым до шага.

Коды выхода (решение В3): `0` успех, `1` ошибка агента, `2` использование,
`3` отказ по токену, `4` идентичность есть, `5` доверие, `6` временная,
`7` запись.

## Подготовка

Нужны: Docker, Go, `grpcurl`, `openssl`, `python3`, `jq`, `nc`. Команды
выполняются из корня репозитория.

1. Выполнить блок «Подготовка» из `docs/qa/agent-enrollment.md` (`make up`,
   переменные `DC`, `PSQL`, `DEFAULT`, `QA`, `F`, функции `new_token`,
   `hash_of`, `token_row`). Сервер поднят с именами `localhost,127.0.0.1,::1`,
   `SARD_AGENT_ENDPOINT` не задан — команда консоли:
   `sudo -u sard-agent sard-agent enroll --server localhost:9090 --token <строка>`.
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
# cfg <адрес> — копия конфига с другим server.address, печатает путь
cfg() { f="$QA/cfg-$RANDOM.yaml"; sed "s|address: localhost:9090|address: $1|" "$H/agent.yaml" > "$f"; echo "$f"; }
# run [флаги…] — sard-agent enroll, печатает код выхода и вывод, копит вывод в $QA/all
run() { "$AG" enroll "$@" >"$QA/out" 2>"$QA/err"; echo "exit=$?"; cat "$QA/out" "$QA/err"; cat "$QA/out" "$QA/err" >> "$QA/all"; }
enroll() { run --config "$H/agent.yaml" --server localhost:9090 "$@"; }
snap() { find "$H/tls" -mindepth 1 -printf '%m %u %p\n' | sort; sha256sum "$H"/tls/* "$H/agent.yaml" 2>/dev/null; }
agents() { $PSQL "select count(*) from agents"; }
spki() { openssl x509 -in "$1" -pubkey -noout | openssl pkey -pubin -outform DER | sha256sum | cut -c1-64; }
```

3. `"$AG" enroll --help; echo "exit=$?"`
   → `exit=0`; справка называет `--server`, `--token`, `--token-file`,
   `--force`, `--config`, `--timeout`, переменную `SARD_ENROLL_TOKEN` и коды
   `0`–`7` со смыслом каждого, как в таблице выше; флага отключения проверки
   сервера нет.

## Часть 1. Отказы до обращения к серверу

До шага 21 токен `T` не должен расходоваться. Перед частью:
`T=$(new_token $DEFAULT "now() + interval '1 hour'"); A0=$(agents); snap > "$QA/empty"`.
В каждом шаге этой части дополнительно: `token_row "$T"` → `-|-`,
`agents` → `$A0`, `snap | diff - "$QA/empty"` → пусто.

4. `enroll` (без токена) → `exit=2`; сообщение перечисляет `--token`,
   `--token-file`, `SARD_ENROLL_TOKEN`.
5. `SARD_ENROLL_TOKEN="$T" enroll --token "$T"` → `exit=2`; сообщение называет
   оба источника.
6. `printf '%s' "$T" > "$QA/tok"; enroll --token "$T" --token-file "$QA/tok"` → `exit=2`.
7. `enroll --token-file "$QA/nope"` → `exit=2`; сообщение называет `$QA/nope`.
8. `: > "$QA/empty.tok"; enroll --token-file "$QA/empty.tok"` → `exit=2`,
   «файл токена пуст».
9. `enroll --token nonsense` → `exit=2`; в сообщении `TOKEN_MALFORMED` и
   «повтор этой строкой не поможет».
10. `enroll --token ""` → `exit=2`, `TOKEN_MALFORMED`.
11. `enroll --token "${T%?}"` (без последнего символа) → `exit=2`, `TOKEN_MALFORMED`.
12. `printf ' %s' "$T" > "$QA/sp.tok"; enroll --token-file "$QA/sp.tok"` → `exit=2`, `TOKEN_MALFORMED`.
13. `enroll --token "$T" --insecure` → `exit=2`.
14. `run --config "$H/agent.yaml" --server localhost --token "$T"` (без порта)
    → `exit=2`; сообщение называет `localhost`.
15. `run --config "$QA/nope.yaml" --server localhost:9090 --token "$T"`
    → `exit=2`; сообщение называет путь.
16. Конфиг без адреса: `grep -v -e '^server:' -e 'address:' "$H/agent.yaml" > "$QA/noaddr.yaml"`;
    `run --config "$QA/noaddr.yaml" --server localhost:9090 --token "$T"`
    → `exit=2`; сообщение называет `server.address` и говорит задать его в
    конфиге; `sha256sum "$QA/noaddr.yaml"` не изменился. То же без `--server`
    → тот же результат.
17. `run --config "$H/agent.yaml" --server 127.0.0.1:9090 --token "$T"`
    (в конфиге `localhost:9090`) → `exit=2`; сообщение называет
    `localhost:9090` и `127.0.0.1:9090`. То же с `--force` → тот же результат.
18. `enroll --token "$T" --timeout 0s` → `exit=2`.
19. `chmod 0500 "$H/tls"; enroll --token "$T"; chmod 0700 "$H/tls"`
    → `exit=7`; сообщение называет `$H/tls`.
20. Команда не ждёт ввода: `sleep 600 | "$AG" enroll --config "$H/agent.yaml" --token nonsense; echo "exit=$?"`
    → завершается сразу, `exit=2`.

### Доверие к серверу

21. `enroll --token "${T%.*}.8544e2352a80a3d403eed68f8bb4ff271d0ce02c09c1d0faf6f090cea423be9f"`
    (секрет `T`, отпечаток тестового вектора) → `exit=5`; сообщение называет
    обе причины — токен от другого сервера или подмена соединения — и не
    предлагает обход; `token_row "$T"` → `-|-`.
22. Несовпадение имени (порт сервера опубликован на всех адресах; `127.0.0.2`
    нет в сертификате): `run --config "$(cfg 127.0.0.2:9090)" --token "$T"`
    → `exit=5`; сообщение называет `127.0.0.2`, говорит, что адрес должен
    совпадать с `SARD_AGENT_ENDPOINT` или именами сертификата, и перечисляет
    `localhost`, `127.0.0.1`, `::1`; `token_row "$T"` → `-|-`.

### Сервер недоступен, таймаут

23. `run --config "$(cfg localhost:9)" --token "$T"`
    → `exit=6`; сообщение называет `localhost:9` и «токен цел, можно повторить».
24. `nc -lk 127.0.0.1 19999 & NC=$!`;
    `time run --config "$(cfg 127.0.0.1:19999)" --token "$T" --timeout 3s; kill $NC`
    → завершается примерно через 3 с, `exit=6`.

## Часть 2. Успех

25. `printf '%s\n' "$T" > "$QA/tok"; enroll --token-file "$QA/tok"`
    → `exit=0`; stdout содержит `agent_id` (далее `X`), `localhost:9090`, пути
    `$H/tls/agent.key`, `$H/tls/agent.pem`, `$H/tls/ca.pem` и совет запустить
    или перезапустить службу агента; предложения записать адрес в конфиг нет;
    `sha256sum "$H/agent.yaml"` не изменился.
26. `stat -c '%a %U' "$H"/tls/*` → ключ `600`, сертификат и бандл `644`;
    владелец — `$(id -un)`.
27. `openssl x509 -in "$H/tls/agent.pem" -pubkey -noout | sha256sum` и
    `openssl pkey -in "$H/tls/agent.key" -pubout | sha256sum` → совпадают.
28. `spki "$H/tls/ca.pem"` → `$F`;
    `openssl verify -CAfile "$H/tls/ca.pem" "$H/tls/agent.pem"` → `OK`.
29. `openssl x509 -in "$H/tls/agent.pem" -noout -ext subjectAltName`
    → `URI:sard://tenants/00000000-0000-0000-0000-000000000001/agents/X`.
30. `token_row "$T"` → `used_at` заполнено, `agent_id` = `X`;
    `$PSQL "select hostname from agents where id = 'X'"` → `$(hostname)`.
31. `grpcurl -cacert "$H/tls/ca.pem" -cert "$H/tls/agent.pem" -key "$H/tls/agent.key" -import-path proto -proto sard/agent/v1/agent.proto -d '{}' localhost:9090 sard.agent.v1.AgentService/Register`
    → не `UNAUTHENTICATED` (на момент написания — `UNIMPLEMENTED` до S4/S5).
32. `ls -A "$H/tls"` → ровно `agent.key agent.pem ca.pem` (файла блокировки
    нет). `snap > "$QA/id1"`.

## Часть 3. Существующая идентичность и `--force`

33. `T2=$(new_token $DEFAULT "now() + interval '1 hour'"); enroll --token "$T2"`
    → `exit=4`; сообщение называет `X` и `localhost:9090`, `--force` (новая
    личность) и что перерегистрация с сохранением личности пока недоступна;
    `snap | diff - "$QA/id1"` → пусто; `token_row "$T2"` → `-|-`.
34. `enroll --force --token "$T"` (использованный токен) → `exit=3`;
    `TOKEN_USED`, «нужен новый токен»; `snap | diff - "$QA/id1"` → пусто.
35. Без `--server`: `run --config "$H/agent.yaml" --force --token "$T2"`
    → `exit=0`; stdout называет `localhost:9090`, новый `agent_id` `Y` ≠ `X` и
    `X` как прежнюю личность; ключ изменился (`sha256sum "$H/tls/agent.key"`
    ≠ из `$QA/id1`), права ключа `600`; `ls -A "$H/tls"` → ровно три файла,
    резервных копий нет; `$PSQL "select count(*) from agents where id in ('X','Y')"` → `2`.
    `snap > "$QA/id2"`.

## Часть 4. Отказы настоящего сервера

В каждом шаге — `--force`, чтобы дойти до сервера; после шага
`snap | diff - "$QA/id2"` → пусто.

36. `U=$(python3 -c "import os,base64; print('sard_'+base64.urlsafe_b64encode(os.urandom(32)).rstrip(b'=').decode()+'.$F')"); enroll --force --token "$U"`
    → `exit=3`, `TOKEN_UNKNOWN`, «повтор не поможет».
37. `T3=$(new_token $DEFAULT "now() - interval '1 second'" "now() - interval '1 hour'"); enroll --force --token "$T3"`
    → `exit=3`, `TOKEN_EXPIRED`.
38. `T4=$(new_token $DEFAULT "now() + interval '1 hour'"); $PSQL "update enrollment_tokens set revoked_at = now() where token_hash = decode('$(hash_of "$T4")','hex')"; enroll --force --token "$T4"`
    → `exit=3`, `TOKEN_REVOKED`.
39. `T5=$(new_token $DEFAULT "now() + interval '1 hour'"); $DC stop postgres; enroll --force --token "$T5"`
    → `exit=6`, `INTERNAL_RETRYABLE`, «временная проблема, токен цел, можно
    повторить»; команда завершается сразу после ответа, без пауз на повторы
    (точное число вызовов проверяет тест «Команда не повторяет регистрацию
    после временной ошибки»).
40. `$DC start postgres`, дождаться `healthy`; `token_row "$T5"` → `-|-`;
    `enroll --force --token "$T5"` → `exit=0`.

`CSR_INVALID`, `HOSTNAME_INVALID`, непредусмотренные ответы сервера, сбой
записи после успешной регистрации, таймаут и SIGINT после отправки запроса и
одновременные запуски вручную не воспроизводятся — их проверяют тесты
сценариев `@fake` с этими названиями.

## Часть 5. Права секретных файлов при старте агента (A1, @a1)

Агент из части 4 зарегистрирован, файлы в `$H/tls`. Секретные файлы — пути из
ключей `tls.key_file`, `repositories[].password_file`,
`repositories[].env_file`, `secrets.<имя>`, `scripts.<имя>`. Требование: ни
одного бита прав группы и остальных, владелец — пользователь агента.
`tls.cert_file` и `tls.ca_file` не проверяются. `timeout 10` ограничивает
успешный запуск: агент работает до сигнала.

```bash
S="$H/sec"; mkdir -m 0700 "$S"
printf 'repo-pass\n' > "$S/repo.pass"; printf 'AWS_ACCESS_KEY_ID=x\n' > "$S/repo.env"
printf 'db-pass\n' > "$S/pg"; printf '#!/bin/sh\nexit 0\n' > "$S/maint"
chmod 0600 "$S/repo.pass" "$S/repo.env" "$S/pg"; chmod 0700 "$S/maint"
cp "$H/agent.yaml" "$H/a1.yaml"; cat >> "$H/a1.yaml" <<EOF
repositories:
  - name: main
    url: $H/repo
    password_file: $S/repo.pass
    env_file: $S/repo.env
secrets:
  pg: $S/pg
scripts:
  maint: $S/maint
EOF
# start — запуск агента с a1.yaml; печатает код выхода и вывод
start() { timeout 10 "$AG" --config "$H/a1.yaml" 2>&1; echo "exit=$?"; }
```

41. `start` (все файлы закрыты: ключ `600`, `repo.pass`/`repo.env`/`pg` `600`,
    `maint` `700`, `agent.pem` и `ca.pem` `644`)
    → агент подключается (в выводе `connecting to localhost:9090`, отказа по
    правам нет), завершается по `timeout` (`exit=124`).
42. Для каждой пары «файл — права» из списка: выставить права, `start`,
    вернуть исходные права (`600`, у `maint` — `700`):
    - `$H/tls/agent.key` — `0640`, `0604`, `0644`;
    - `$S/repo.pass` — `0640`, `0604`;
    - `$S/repo.env` — `0640`;
    - `$S/pg` — `0604`;
    - `$S/maint` — `0750`, `0705`, `0755`.

    → каждый раз агент сразу завершается с ошибкой (не `124`); сообщение
    называет ключ конфига (`tls.key_file`, `password_file`, `env_file`,
    `secrets` / `pg`, `scripts` / `maint`), путь файла, текущие права и
    требование «только владелец».
43. `chmod 0400 "$H/tls/agent.key" "$S/repo.pass"; chmod 0500 "$S/maint"; start`
    → агент подключается, `exit=124`. Вернуть `600` / `700`.
44. Конфиг без `env_file`: `sed -i '/env_file:/d' "$H/a1.yaml"; rm "$S/repo.env"; start`
    → агент подключается, `exit=124`.
45. Другой владелец (нужен root): для каждого из `$H/tls/agent.key`,
    `$S/repo.pass`, `$S/pg`, `$S/maint`:
    `sudo chown nobody <файл>; start; sudo chown "$(id -un)" <файл>`
    → агент сразу завершается с ошибкой; сообщение называет ключ конфига,
    путь, владельца `nobody` и пользователя агента.

## Часть 6. Секреты не утекают

46. Для каждой строки токена из этой процедуры (`$T`, `$T2` … `$T5`, `$U`) и её
    секрета (`s=${tok#sard_}; s=${s%%.*}`): `grep -cF "<строка или секрет>" "$QA/all"` → `0`.
47. `grep -c 'PRIVATE KEY' "$QA/all"` → `0`.

## Часть 7. От имени пользователя службы (хост с пакетом deb/rpm)

Каталог для файлов `tls.*`, доступный на запись пользователю `sard-agent`,
пакет не создаёт — это упаковка, вне A2b; здесь он создаётся вручную.

48. `sudo install -d -o sard-agent -g sard-agent -m 0700 /etc/sard/tls`; в
    `/etc/sard/agent.yaml` — `server.address` из команды консоли и пути `tls.*`
    в `/etc/sard/tls`;
    `sudo -u sard-agent sard-agent enroll --server <адрес> --token <строка>`
    (команда из консоли как есть, без `--config`) → `exit=0`; владелец файлов —
    `sard-agent`, ключ `600`.
49. `sudo systemctl restart sard-agent` → служба активна, в журнале нет
    отказа по правам секретных файлов или по сертификату.
