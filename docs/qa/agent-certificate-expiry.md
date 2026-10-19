# QA: срок сертификата агента — консоль, уведомления, документация (C1, фаза 3)

Сценарии: `docs/specs/server/agent-certificate-expiry.feature` (П32–П44),
`docs/specs/web/agent-certificate-renewal.feature` (КС1–КС6) и сценарии `@t8`
правила «Продление на стенде» в `docs/specs/server/agent-certificate-renewal.feature`.
Продление (фаза 2) проверяет `docs/qa/agent-certificate-renewal.md`; его
подготовка здесь повторена, чтобы процедура шла сама по себе.

Ожидаемый результат указан после «→» в каждом шаге. Любое расхождение — дефект.

Стенд работает со сроком сертификата `PT10M`. Тогда (П33, П36): `renewal_due` —
с 6 мин 40 с после выпуска, `expiring` и порог 30 — когда до конца осталось не
больше 49,3 с (с 9 мин 11 с), порог 7 — не больше 11,5 с (с 9 мин 49 с), порог 1 —
не больше 1,6 с (с 9 мин 59 с), `expired` — с 10 мин. Проверка срока идёт раз в
5 с (`SARD_PKI_AGENT_CERTIFICATE_CHECK_INTERVAL=PT5S`), поэтому каждый порог
ловится отдельно; ожидания в шагах — с запасом на проверку и тик доставки.

## Подготовка

Нужны: Docker с compose v2, Go, `curl`, `jq`, `grpcurl`, `openssl` 3; Telegram-
клиент, бот от @BotFather и чат, в который он добавлен, id чата. Команды — из
корня репозитория, не от root, в одной оболочке.

```bash
QA=$(mktemp -d); J=$QA/jar
TG_TOKEN='<токен бота>'; TG_CHAT='<id чата>'
cat > $QA/override.yml <<'EOF'
services:
  server:
    environment:
      SARD_PKI_AGENT_CERTIFICATE_VALIDITY: PT10M
      SARD_PKI_AGENT_RENEWAL_MIN_INTERVAL: PT1H
      SARD_PKI_AGENT_CERTIFICATE_CHECK_INTERVAL: ${QA_CHECK:-PT5S}
      SARD_TELEGRAM_BOT_TOKEN: ${QA_TG_TOKEN}
      SARD_TELEGRAM_CHAT_ID: ${QA_TG_CHAT}
      SARD_CONSOLE_PUBLIC_URL: https://sard.example.com
      SARD_NOTIFY_LANGUAGE: ${QA_LANG:-ru}
EOF
export QA_TG_TOKEN=$TG_TOKEN QA_TG_CHAT=$TG_CHAT
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
GRPC="grpcurl -cacert $QA/ca.crt -import-path proto -proto sard/agent/v1/agent.proto -format-error"
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
cn() { openssl x509 -in $1 -noout -subject | sed 's/.*CN *= *//'; }
nb() { date -d "$(openssl x509 -in $1 -noout -startdate | cut -d= -f2)" +%s; }
cert() { a GET /agents/$1 | body | jq -c .certificate; }
# wait_to <файл сертификата> <секунд от notBefore>
wait_to() { local d=$(( $(nb $1) + $2 - $(date +%s) )); [ $d -gt 0 ] && sleep $d; }
renew() { local csr; csr=$(openssl req -new -key $3 -subj /CN=qa -outform DER | base64 -w0)
  $GRPC -cert $1 -key $2 -d "{\"csrDer\":\"$csr\"}" localhost:9090 sard.agent.v1.AgentService/RenewCertificate \
  | jq -r .certificateChainPem > $4; }
confirm() { $GRPC -cert $1 -key $2 -d '{}' localhost:9090 sard.agent.v1.AgentService/ConfirmCertificate; }
newkey() { openssl ecparam -name prime256v1 -genkey -noout | openssl pkcs8 -topk8 -nocrypt -out $1; chmod 0600 $1; }
# Строки доставки нового предмета: ни run_id, ни fire_id, ни episode_id не заданы.
certdel() { $PSQL "select status, count(*) from notification_deliveries where run_id is null and fire_id is null and episode_id is null group by status order by status"; }
```

→ мастер печатает `204`; `make build` завершается.

## Часть 1. Настройка интервала проверки (П35)

1. `QA_CHECK=PT0S $DC up -d server; sleep 20; $DC logs server | grep -m1 'sard.pki.agent-certificate-check-interval'`
   → строка называет настройку и значение `PT0S`; `$DC ps server` — не `healthy`.
2. То же для `QA_CHECK=-PT1M` и `QA_CHECK=hourly` → настройка и значение в
   строке ошибки, сервер не стартует.
3. `$DC up -d --wait server` → `healthy`.

## Часть 2. Поля сертификата в REST (П32–П34, задача 6)

Агент `E` регистрируется и не запускается: он не продлевает сертификат.
Агент `S1` запускается и продлевает.

```bash
E=$QA/e; host $E; enroll_as $E '{}'; XE=$(cn $E/tls/agent.pem)
S1=$QA/s1; host $S1; enroll_as $S1 '{}'; XS=$(cn $S1/tls/agent.pem)
$AG --config $S1/agent.yaml > $QA/s1.out 2>&1 & SP=$!
```

4. `cert $XE`
   → объект ровно из пяти ключей `notBefore`, `notAfter`, `daysLeft`, `state`,
   `renewalIssuedAt`; `notAfter − notBefore` = 600 с; `daysLeft` `0`; `state`
   `"ok"`; `renewalIssuedAt` `null`.
5. `a GET /agents | body | jq -c --arg id $XE '.items[] | select(.id == $id) | .certificate'`
   → то же, что в шаге 4.
6. `wait_to $E/tls/agent.pem 401; cert $XE | jq -r .state` → `renewal_due`.
7. Выданное, но не подтверждённое продление: `newkey $QA/k2.key; renew $E/tls/agent.pem $E/tls/agent.key $QA/k2.key $QA/c2.pem; cert $XE`
   → `state` `"renewal_due"`, `notAfter` прежний, `renewalIssuedAt` — время
   этого вызова (с точностью до секунды).
8. `wait_to $E/tls/agent.pem 552; cert $XE | jq -r .state` → `expiring`.
9. `wait_to $E/tls/agent.pem 601; cert $XE | jq -r '"\(.state) \(.daysLeft)"'` → `expired 0`.
10. Агент `S1` продлевается сам: `cert $XS | jq -r .state` в любой момент
    шагов 4–9 и `grep -c 'certificate renewed' $QA/s1.out` после шага 9
    → состояние `ok` или `renewal_due`, никогда `expiring`; не меньше `1`;
    `notAfter` после продления — на 600 с позже его `notBefore`.
11. Отозванный агент: `R=$QA/r; host $R; enroll_as $R '{}'; XR=$(cn $R/tls/agent.pem); a POST /agents/$XR/revoke | tail -1; cert $XR`
    → `HTTP 200`; `null`.

## Часть 3. Уведомления в Telegram (П36–П39, задача 6)

Шаги смотрят в чат и в базу. Агент `E` из части 2 к этому моменту уже прошёл
все пороги; его уведомления пришли во время шагов 8–9.

12. Чат → о `E` ровно три сообщения, по одному у каждого порога, первое — около
    9 мин 11 с после регистрации `E`, второе — около 9 мин 49 с, третье — около
    9 мин 59 с (каждое не позже чем через 10 с после своей отметки). Текст
    первого:

    ```text
    ⚠️ Сертификат агента скоро истечёт: <hostname хоста проверяющего>
    Истекает: <notAfter E как ГГГГ-ММ-ДД ЧЧ:ММ> UTC
    Осталось: <от 40 до 49> с
    ID агента: <$XE>
    Новый сертификат выдан <время шага 7> UTC, но агент его не подтвердил
    На хосте агента: sudo sard-agent status — строка renewal_last_error называет причину
    Если сертификат истечёт, агент не подключится, пока его не зарегистрируют заново
    https://sard.example.com/agents/<$XE>
    ```

    Первая строка жирная, id агента и `sudo sard-agent status` — моноширинные.
    В тексте нет «30 дней», серийного номера и `BEGIN`.
13. `certdel` → `delivered|3` (агенты `S1` и `R` строк не дали).
14. `$DC logs server | grep -E "reached (30|7|1)/365" | grep -c $XE` → `3`.
15. Повторов нет: подождать 2 минуты → в чате о `E` по-прежнему три сообщения;
    `certdel` → `delivered|3`.
16. Подтверждённое продление прекращает уведомления. Агент `F` регистрируется
    и не запускается; после первого уведомления (порог 30) его продлевают
    вручную:

    ```bash
    F=$QA/f; host $F; enroll_as $F '{}'; XF=$(cn $F/tls/agent.pem)
    wait_to $F/tls/agent.pem 560
    newkey $QA/kf.key; renew $F/tls/agent.pem $F/tls/agent.key $QA/kf.key $QA/cf.pem; confirm $QA/cf.pem $QA/kf.key
    ```

    → к 560 с в чате одно сообщение о `F`, в его пятой строке «Агент ни разу не
    подключался»; подтверждение `{}`; через 2 минуты о `F` по-прежнему одно
    сообщение; `cert $XF | jq -r .state` → `ok`, `notBefore` — время продления.
17. Английский текст: `QA_LANG=en $DC up -d --wait server`, агент `G`
    регистрируется и не запускается, через 10 мин 10 с → три сообщения о `G`,
    первая строка `⚠️ Agent certificate expires soon: …`, третья `Time left: … s`,
    пятая `The agent has never connected`. `$DC up -d --wait server` (снова `ru`).
18. Встроенный агент (если стенд — установка compose с sard-self): остановить
    соседа на время срока, `$DC stop self-agent`, дождаться 10 мин 10 с с
    последнего продления sard-self
    → в чате три сообщения о хосте `sard-self`: шестая строка «Встроенный агент:
    docker compose logs self-agent», седьмая — «Если сертификат истечёт,
    отзовите встроенного агента в консоли — он зарегистрируется заново сам»,
    `sudo sard-agent` в тексте нет. `$DC start self-agent`; восстановление — шаг
    44 `docs/qa/agent-certificate-renewal.md`.

## Часть 4. Сбой доставки и режим без бота

19. **Неверный чат.** `QA_TG_CHAT=-1 $DC up -d --wait server`; агент `H`
    регистрируется и не запускается; через 10 мин 10 с
    `$DC logs server | grep 'Notification of agent certificate'`
    → строки WARN `… (30/365) through telegram failed: …chat not found…`
    (и для 7/365, 1/365); в чате о `H` ничего.
20. **Без бота.** `QA_TG_TOKEN= QA_TG_CHAT= $DC up -d --wait server`; агент `I`
    регистрируется и не запускается; через 10 мин 10 с
    → `certdel` не изменился с шага 19; `$DC logs server | grep -E 'WARN|ERROR' | grep dev.sard.server.notify`
    — только одна стартовая строка `Notifications are off`; `cert <id I> | jq -r .state`
    → `expired` (консоль работает без бота). Вернуть бота: `$DC up -d --wait server`.
21. **База недоступна во время проверки.** Агент `K` регистрируется и не
    запускается; на 9 мин 5 с `$DC pause postgres`, на 9 мин 20 с
    `$DC unpause postgres`
    → `$DC logs server | grep 'Agent certificate check failed; retrying at the next one'` — не меньше одной строки WARN;
    в чате о `K` три сообщения, без дублей.

## Часть 5. Консоль (КС1–КС6)

Дважды: на моках (`cd web && VITE_API_MOCKS=1 npm run dev`, адрес
`http://localhost:5173`) и против сервера (`http://localhost:8080`, агенты
частей 2–4).

22. Страница агентов → колонка «Сертификат» после «последнего контакта»; у
    агента `ok` и `renewal_due` — только дата; у `expiring` — дата и отметка
    «скоро истечёт» (цвет notice), у `expired` — «истёк» (цвет error); у
    отозванного — прочерк.
23. Карточка агента `expired` → строки «Сертификат действует до», «Осталось
    суток» `0`, «Состояние сертификата» с отметкой «истёк»; подсказка о
    перерегистрации со ссылкой на раздел «Сертификаты агентов» руководства
    оператора; в подсказке нет `sard-agent`.
24. Карточка агента с `renewalIssuedAt` (на моках — фикстура, против сервера —
    `E` после шага 7, до шага 9) → строка «Новый сертификат выдан <время>,
    агент его ещё не подтвердил».
25. Карточка отозванного агента → строк о сертификате нет.
26. Язык EN → колонка `Certificate`, отметки `expires soon` / `expired`, строка
    карточки `Certificate valid until`.
27. Против сервера: перевести часы браузера на год вперёд (или открыть консоль
    с машины с другим временем) → отметки те же, что до этого: консоль
    состояние не вычисляет.
28. Остановить сервер (`$DC stop server`), открыть карточку → сообщение о
    недоступности сервера, строк о сертификате нет. `$DC start server`.

## Часть 6. Хост: status и check

29. `$AG status --config $E/agent.yaml | tail -3`
    → `certificate_days_left: 0`, `certificate_state: expired`, `re_enroll: sudo sard-agent enroll --force --config $E/agent.yaml --server localhost:9090 --token <TOKEN>`.
30. `$AG check --config $S1/agent.yaml --offline | grep tls.expiry`
    → одна строка `tls.expiry`. Сообщение и порог предупреждения — открытый
    вопрос 1 серверной спецификации: записать фактическую строку в отчёт QA,
    дефектом не считать до ответа владельца.

## Часть 7. Документация и ADR (П40, П41)

31. `ls docs/operator/13-agent-certificates.md && grep -c '13-agent-certificates.md' docs/operator/README.md docs/operator/05-agents.md docs/operator/09-troubleshooting.md`
    → файл есть; в каждом из трёх файлов не меньше `1`.
32. Пройти страницу 13 глазами: как работает продление (две трети срока, пара
    `.new`, подтверждение), как смотреть срок (`sudo sard-agent status`,
    `sard-agent check`, консоль), что делать с истёкшим сертификатом (команда
    перерегистрации, новый id, перевод источников, отзыв прежнего), sard-self
    (`/var/lib/sard-self/tls`, отзыв с `confirm=sard-self`), свой каталог `tls`
    (`sudo systemctl edit sard-agent`, `ReadWritePaths=`), настройки сервера
    → каждая команда страницы выполняется как написана (для перерегистрации —
    на агенте `E`, новым токеном): `E` после неё `online` с новым id.
33. `grep -n 'Автоматического продления нет' docs/operator/09-troubleshooting.md` → пусто.
34. `grep -E 'SARD_PKI_AGENT_(CERTIFICATE_VALIDITY|RENEWAL_MIN_INTERVAL|CERTIFICATE_CHECK_INTERVAL)' docs/operator/03-configuration.md | wc -l` → `3`.
35. `ls docs/adr/00XX-draft-agent-certificate-renewal.md; grep -n 'RenewCertificate' docs/open-questions.md | head`
    → ADR есть; в строке OQ-022 раздела «Открыто» `RenewCertificate` нет, есть
    запись о закрытии со ссылкой на ADR; есть новые OQ о сроке CA и о
    `ServerRecoveryDrillTest`.

## Часть 8. Долгая жизнь на стенде (задача 8, по желанию)

Автоматически — сценарии `@e2e @t8`. Вручную, если стенд свободен 35 минут:
агент `S1` с репозиторием и источником `files` с расписанием `*/2 * * * *`
(как в `docs/qa/agent-role.md`, часть 6).

36. Через 32 минуты после регистрации `S1`
    → `$PSQL "select count(*), count(*) filter (where superseded_at is not null) from agent_certificates where agent_id = '$XS'"`
    — не меньше 4 и на одну меньше; все запуски источника `succeeded`, их не
    меньше 12; процесс `$SP` жив (`kill -0 $SP && echo alive` → `alive`);
    `grep -c 'not reconnecting' $QA/s1.out` → `0`; в чате о `S1` ни одного
    сообщения.

## Часть 9. Уборка

```bash
kill $SP 2>/dev/null; $DC down -v; rm -rf $QA
```

Удалить бота из чата или отозвать его токен в @BotFather.

## Не проверяется вручную (тесты сценариев)

- Границы долей срока при `P365D` (сутки) и точное сравнение без округления
  (`@http`).
- Наименьший порог после простоя, пройденный порог не уведомляется позже,
  обновление с сертификатом за порогом (`@server`).
- Отмена неотправленного уведомления после подтверждения продления, отзыва
  или истечения (`skipped`), одновременные проверки двух серверов, срок жизни
  доставки 24 ч (`@server`).
- Формат остатка (сутки вверх, длительность S9b), части Bold и Code (`@unit`).
- Схема OpenAPI `AgentCertificate` (`@contract`), моки (`@mocks`), словари и
  тема (`@vitest`), строки документации и ADR (`@doc`).
