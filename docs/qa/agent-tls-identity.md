# QA: проверка ключа и сертификата агента при старте (OQ-027)

Сценарии: `docs/specs/agent/agent-tls-identity.feature` (черновик, ждёт
утверждения владельцем). Здесь вручную проходятся сценарии с тегом `@qa`;
остальные проверяют тесты `@start` с теми же названиями.

Ожидаемый результат указан после «→» в каждом шаге. Любое расхождение — дефект.
Сообщение о незавершённой регистрации (далее `MSG`) — дословно:

```text
tls.key_file не соответствует tls.cert_file: регистрация не завершена — повторите `sard-agent enroll --force`
```

## Подготовка

Нужны: Docker, Go, `openssl`. Команды выполняются из корня репозитория, **не от
root** (иначе права `0000` не мешают чтению и пункты `chmod 0000` в шагах 6 и 7 не показательны).

1. Выполнить части «Подготовка» и 2 из `docs/qa/agent-enroll.md` (сервер поднят,
   агент зарегистрирован, файлы в `$H/tls`, переменные `AG`, `H`, `QA`).
2. Подготовить вспомогательные функции и сохранить исходную пару:

```bash
cp "$H/tls/agent.key" "$QA/good.key"; cp "$H/tls/agent.pem" "$QA/good.pem"
MSG='tls.key_file не соответствует tls.cert_file: регистрация не завершена — повторите `sard-agent enroll --force`'
S=QA-MARKER-7f3a
# start — запуск агента; печатает код выхода, stdout и stderr, копит вывод в $QA/start-all
start() { timeout 10 "$AG" --config "$H/agent.yaml" >"$QA/sout" 2>"$QA/serr"; echo "exit=$?"; cat "$QA/sout" "$QA/serr"; cat "$QA/sout" "$QA/serr" >> "$QA/start-all"; }
restore() { install -m 0600 "$QA/good.key" "$H/tls/agent.key"; install -m 0644 "$QA/good.pem" "$H/tls/agent.pem"; }
```

## Шаги

3. `restore; start`
   → агент подключается: в stdout `connecting to localhost:9090`, завершается
   по `timeout` (`exit=124`); `grep -cF "$MSG" "$QA/serr"` → `0`.
4. Ключ другой пары (имитация прерванного `enroll --force`):
   `openssl ecparam -name prime256v1 -genkey -noout | openssl pkcs8 -topk8 -nocrypt > "$QA/other.key"; install -m 0600 "$QA/other.key" "$H/tls/agent.key"; start`
   → завершается сразу, `exit=1`; stderr — ровно одна строка
   `sard-agent: ` + `MSG` (`[ "$(cat "$QA/serr")" = "sard-agent: $MSG" ] && echo ok` → `ok`);
   в stdout нет `connecting to`; в журнале сервера
   (`$DC logs sard-server --since 30s`) нет подключения агента.
5. То же с ключом RSA:
   `openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 > "$QA/rsa.key"; install -m 0600 "$QA/rsa.key" "$H/tls/agent.key"; start`
   → как в шаге 4.
6. Проблемы с файлами. Перед каждым пунктом `restore`, затем подменить один
   файл и выполнить `start`:
   - `rm "$H/tls/agent.pem"`;
   - `chmod 0000 "$H/tls/agent.pem"`;
   - `: > "$H/tls/agent.pem"`;
   - `echo "$S" > "$H/tls/agent.pem"`;
   - `cp "$QA/good.key" "$H/tls/agent.pem"` (в файле сертификата только ключ).

   → каждый раз `exit=1` сразу; stderr называет `tls.cert_file` и
   `$H/tls/agent.pem`; `grep -cF "$MSG" "$QA/serr"` → `0`; в stdout нет
   `connecting to`.
7. То же для ключа (`restore` перед каждым пунктом):
   - `rm "$H/tls/agent.key"`;
   - `chmod 0000 "$H/tls/agent.key"`;
   - `: > "$H/tls/agent.key"`;
   - `echo "$S" > "$H/tls/agent.key"`;
   - `install -m 0600 "$QA/good.pem" "$H/tls/agent.key"` (в файле ключа только сертификат).

   → каждый раз `exit=1` сразу; stderr называет `tls.key_file` и
   `$H/tls/agent.key`; `grep -cF "$MSG" "$QA/serr"` → `0`.
8. Восстановление по совету сообщения: `install -m 0600 "$QA/other.key" "$H/tls/agent.key"`;
   `T6=$(new_token $DEFAULT "now() + interval '1 hour'"); "$AG" enroll --config "$H/agent.yaml" --force --token "$T6"; echo "exit=$?"`
   → `exit=0`; затем `start` → `connecting to localhost:9090`, `exit=124`.

## Материал ключей не утекает

9. `grep -c 'PRIVATE KEY' "$QA/start-all"` → `0`;
   `grep -c 'CERTIFICATE' "$QA/start-all"` → `0`;
   `grep -cF "$S" "$QA/start-all"` → `0`;
   для каждого из `$QA/good.key`, `$QA/other.key`, `$QA/rsa.key`,
   `$QA/good.pem`: `grep -cF "$(sed -n 2p <файл>)" "$QA/start-all"` → `0`
   (вторая строка PEM — начало base64-тела).
