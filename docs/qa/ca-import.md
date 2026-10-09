# QA: импорт CA при первом запуске (F8)

Сценарии: `docs/specs/server/ca-import.feature`, `docs/specs/web/ca-import.feature`.
Решения Р1–Р11, таблица причин отказа и соглашения — в заголовке серверной
спецификации. Открытые вопросы — OQ-177…OQ-185 в `docs/open-questions.md`;
шаги, помеченные `[OQ-NNN]`, меняются вместе с решением по нему.

Процедура из трёх частей:

- **Часть 1 — проверки импорта на одной машине** (локальный стенд `make up`).
- **Часть 2 — переезд A → B** на двух ВМ и хосте агента: то, ради чего фича.
- **Часть 3 — консоль.**

Ожидаемый результат указан после «→» в каждом шаге. Любое расхождение — дефект.

> **Изменено F4a** (`docs/specs/server/onboarding-setup.feature`, Р11; черновик
> 2026-10-09). Пока шаг онбординга ca не выполнен и сертификатов агентов нет,
> источник с другим CA **заменяет** сгенерированный CA, а не даёт
> `CA_ALREADY_PRESENT`. Перед шагом 9 части 1 выполнить шаг ca мастера (код из
> `docker compose logs server`, `POST /api/v1/onboarding/ca`); тогда шаг 9
> ожидает `CA_ALREADY_PRESENT` с причиной `onboarding step ca is complete`. Вход
> в консоль (часть 3) — паролем, заданным в мастере. Замена — часть 3
> `docs/qa/onboarding-setup.md`.

Отпечаток CA везде считается так же, как в токене (SPKI, ADR 0014), а не
`openssl x509 -fingerprint`:

```bash
fp() { openssl x509 -in "$1" -pubkey -noout | openssl pkey -pubin -outform DER | sha256sum | cut -c1-64; }
```

## Часть 1. Проверки импорта (локальный стенд)

### Подготовка

Нужны: Docker, `openssl` 3.4 или новее (`openssl version`; нужны `-not_before`
и `-not_after` у `openssl req`), `sudo`. Команды — из корня репозитория.

```bash
make up                                     # первый старт: сервер создаёт свой CA
DC="docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.build.yml"
IMAGE=$($DC config --images | grep sard-server)
QA=$(mktemp -d)
# исходный CA — CA, который только что создал сервер (как на сервере A)
mkdir -p "$QA/good"
docker run --rm --user 0 --entrypoint tar -v sard_sard-pki:/pki:ro "$IMAGE" -C /pki -cf - ca | tar -C "$QA/good" -xf -
F=$(fp "$QA/good/ca/ca.crt")
cat > "$QA/override.yml" <<'EOF'
services:
  server:
    environment:
      SARD_PKI_IMPORT_DIR: /var/lib/sard/pki-import
    volumes:
      - ${QA_SRC}:/var/lib/sard/pki-import:ro
EOF
printf '[req]\ndistinguished_name=dn\n[dn]\n' > "$QA/min.cnf"

# seal <каталог> — владелец сервера (10001) и права, которых требует сервер
seal() { sudo chown -R 10001:10001 "$1"; sudo chmod 700 "$1" "$1/ca"; sudo chmod 600 "$1"/ca/*; }
# src <имя> <ca.crt> <ca.key> — источник из двух файлов
src() { sudo mkdir -p "$QA/$1/ca"; sudo cp "$2" "$QA/$1/ca/ca.crt"; sudo cp "$3" "$QA/$1/ca/ca.key"; seal "$QA/$1"; }
# variant <имя> — копия исходного CA с правами и владельцем, для правки одного свойства
variant() { sudo cp -a "$QA/good" "$QA/$1"; }
# fresh — пустой каталог CA сервера (база остаётся)
fresh() { $DC rm -sf server >/dev/null; docker volume rm -f sard_sard-pki >/dev/null; }
# try <имя> — первый старт с источником $QA/<имя>; печатает код up и строки про CA
try() { fresh; QA_SRC="$QA/$1" $DC -f "$QA/override.yml" up -d --wait server; echo "up=$?";
        $DC logs server 2>&1 | grep -E 'CA import|fingerprint|CA expires' ; }
# pkils — содержимое каталога CA сервера с правами и владельцем
pkils() { docker run --rm --user 0 --entrypoint sh -v sard_sard-pki:/pki:ro "$IMAGE" -c \
          'stat -c "%a %u %n" /pki /pki/ca /pki/ca/* 2>&1; ls -A /pki'; }
# snap <каталог> — отпечаток состояния источника: список, права, время, хэши
snap() { sudo find "$1" -printf '%p %m %u %T@\n' | sort; sudo find "$1" -type f -exec sha256sum {} + | sort; }
# key_leaks <ca.key> — сколько строк base64 ключа встречается в логах сервера
key_leaks() { sudo grep -v -- '-----' "$1" | while read -r l; do $DC logs server 2>&1 | grep -cF "$l"; done | awk '{s+=$1} END {print s+0}'; }
newkey() { openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$1"; }
# mkcert <ключ> <выход> [аргументы openssl req] — самоподписанный сертификат без расширений по умолчанию
mkcert() { k=$1; o=$2; shift 2; openssl req -x509 -new -config "$QA/min.cnf" -key "$k" -subj /CN=qa-ca -out "$o" "$@"; }
CAEXT=(-addext basicConstraints=critical,CA:TRUE -addext keyUsage=critical,keyCertSign,cRLSign)
```

→ `make up` завершается, `$F` — 64 символа hex.

### Успешный импорт

1. `seal "$QA/good"; snap "$QA/good" > "$QA/good.before"; try good`
   → `up=0`; в логе строка INFO, что CA импортирован из `/var/lib/sard/pki-import`,
   с отпечатком `$F`.
2. `pkils` → `/pki` и `/pki/ca` — `700 10001`, `ca.crt` и `ca.key` — `600 10001`;
   в `/pki` только `ca`, в `/pki/ca` только два файла.
3. `docker run --rm --user 0 --entrypoint cat -v sard_sard-pki:/pki:ro "$IMAGE" /pki/ca/ca.crt | openssl x509 -outform DER | sha256sum`
   и `sudo openssl x509 -in "$QA/good/ca/ca.crt" -outform DER | sha256sum` → совпадают.
4. `echo | openssl s_client -connect localhost:9090 -alpn h2 -showcerts 2>/dev/null | awk '/BEGIN/{n++} n==2' | sed '/END/q' > "$QA/root.pem"; fp "$QA/root.pem"`
   → `$F`; `echo | openssl s_client -connect localhost:9090 -alpn h2 2>/dev/null | openssl x509 -noout -ext subjectAltName`
   → имена `SARD_PKI_SERVER_NAMES` этого стенда (`localhost`, `127.0.0.1`, `::1`).
5. `snap "$QA/good" | diff - "$QA/good.before"` → пусто (источник не изменён).
6. `key_leaks "$QA/good/ca/ca.key"` → `0`.
7. Повторный старт с той же настройкой: `QA_SRC="$QA/good" $DC -f "$QA/override.yml" up -d --force-recreate --wait server`
   → `up` успешен; в логе INFO, что импорт не нужен, CA с отпечатком `$F` уже есть [OQ-180].
8. Старт без настройки: `$DC up -d --force-recreate --wait server`
   → успешен; в логе отпечаток `$F` с происхождением `existing` [OQ-181].
9. Каталог CA уже занят другим CA [OQ-180]:
   `newkey "$QA/o.key"; mkcert "$QA/o.key" "$QA/o.crt" -days 3650 "${CAEXT[@]}"; src other "$QA/o.crt" "$QA/o.key"; QA_SRC="$QA/other" $DC -f "$QA/override.yml" up -d --force-recreate --wait server`
   → не стартует; `CA import refused`, `CA_ALREADY_PRESENT`, оба отпечатка (`$F` и `fp "$QA/o.crt"`);
   `pkils` и шаг 3 → каталог CA не изменился.
10. Источник пропал при занятом каталоге [OQ-180]: `QA_SRC="$QA/nonexistent" $DC -f "$QA/override.yml" up -d --force-recreate --wait server`
    → стартует с `$F`; в логе WARN: импорт пропущен, CA уже есть, убрать `SARD_PKI_IMPORT_DIR`.
11. Источник только для чтения уже проверен шагами 1–6 (монтирование `:ro`).

### Отказы: каждая причина

В каждом шаге: `try <имя>` → `up` не `0`; строка `CA import refused` с указанной
причиной и названным путём; затем `pkils` → в `/pki` нет `ca` и нет `.tmp-*`
(частичного импорта нет); `snap` источника до и после совпадает. Ключ берётся
настоящий (`$QA/good/ca/ca.key`), если не сказано иного; после шага —
`key_leaks <ключ шага>` → `0`.

12. `try nonexistent` → `IMPORT_SOURCE_MISSING`, путь источника.
13. `touch "$QA/file"; try file` → `IMPORT_SOURCE_MISSING`.
14. `variant nocrt; sudo rm "$QA/nocrt/ca/ca.crt"; try nocrt` → `IMPORT_FILE_MISSING`, путь `ca/ca.crt`.
15. `variant nokey; sudo rm "$QA/nokey/ca/ca.key"; try nokey` → `IMPORT_FILE_MISSING`, путь `ca/ca.key`.
16. `sudo mkdir -p "$QA/flat"; sudo cp "$QA"/good/ca/* "$QA/flat/"; sudo chown -R 10001:10001 "$QA/flat"; sudo chmod 700 "$QA/flat"; try flat`
    → `IMPORT_FILE_MISSING`, ожидаемый путь `…/ca/ca.crt`.
17. `variant unread; sudo chown 0:0 "$QA/unread/ca/ca.key"; try unread` → `IMPORT_FILE_UNREADABLE`, путь ключа.
18. Права шире владельца, по одному на шаг (`variant pN`, затем в `$QA/pN`):
    `sudo chmod 640 ca/ca.key` → `IMPORT_PERMISSIONS_TOO_OPEN`, `rw-r-----`;
    `sudo chmod 604 ca/ca.key` → то же, `rw----r--`;
    `sudo chmod 644 ca/ca.crt` → то же, путь сертификата;
    `sudo chmod 750 ca` → то же, путь `ca`;
    `sudo chmod 755 .` (корень источника) → то же, путь источника.
19. Права и порядок: `variant junk`, в `$QA/junk/ca/ca.key` записан мусор, `sudo chmod 644` на него
    → `IMPORT_PERMISSIONS_TOO_OPEN`, а не `CA_KEY_INVALID`.
20. `: > "$QA/empty"; src emptycrt "$QA/empty" "$QA/good/ca/ca.key"; try emptycrt` → `CA_CERT_INVALID`.
21. Два сертификата: `sudo cat "$QA/good/ca/ca.crt" "$QA/o.crt" > "$QA/two.crt"; src two "$QA/two.crt" "$QA/good/ca/ca.key"; try two` → `CA_CERT_INVALID`.
22. Файлы перепутаны: `src swap "$QA/good/ca/ca.key" "$QA/good/ca/ca.crt"; try swap`
    → `CA_CERT_INVALID`; `key_leaks "$QA/good/ca/ca.key"` → `0`; в тексте ошибки нет `PRIVATE KEY`.
23. Ключ — пустой, зашифрованный, SEC1 (по одному на шаг):
    `src k0 "$QA/good/ca/ca.crt" "$QA/empty"`;
    `sudo openssl pkcs8 -topk8 -v2 aes-256-cbc -passout pass:qa -in "$QA/good/ca/ca.key" -out "$QA/enc.key"; src kenc "$QA/good/ca/ca.crt" "$QA/enc.key"`;
    `sudo openssl pkey -in "$QA/good/ca/ca.key" -traditional -out "$QA/sec1.key"; src ksec1 "$QA/good/ca/ca.crt" "$QA/sec1.key"`
    → каждый `CA_KEY_INVALID`, сообщение называет формат PKCS#8 PEM.
24. RSA: `openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "$QA/r.key"; mkcert "$QA/r.key" "$QA/r.crt" -days 3650 "${CAEXT[@]}"; src rsa "$QA/r.crt" "$QA/r.key"; try rsa`
    → `CA_KEY_UNSUPPORTED`, алгоритм RSA.
25. P-384: то же с `openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-384` → `CA_KEY_UNSUPPORTED`, P-384.
26. Чужой ключ: `newkey "$QA/x.key"; src mism "$QA/good/ca/ca.crt" "$QA/x.key"; try mism` → `CA_KEY_MISMATCH`.
27. Промежуточный CA:
    `newkey "$QA/i.key"; openssl req -new -config "$QA/min.cnf" -key "$QA/i.key" -subj /CN=qa-inter -out "$QA/i.csr"; printf 'basicConstraints=critical,CA:TRUE\nkeyUsage=critical,keyCertSign,cRLSign\n' > "$QA/i.ext"; openssl x509 -req -in "$QA/i.csr" -CA "$QA/o.crt" -CAkey "$QA/o.key" -days 3650 -extfile "$QA/i.ext" -out "$QA/i.crt"; src inter "$QA/i.crt" "$QA/i.key"; try inter`
    → `CA_NOT_SELF_SIGNED`, субъект `CN=qa-inter`, издатель `CN=qa-ca`.
28. Не CA: `newkey "$QA/n.key"; mkcert "$QA/n.key" "$QA/n.crt" -days 3650 -addext basicConstraints=critical,CA:FALSE; src notca "$QA/n.crt" "$QA/n.key"; try notca` → `CA_NOT_A_CA`.
    Без basicConstraints: `mkcert "$QA/n.key" "$QA/n2.crt" -days 3650; src nobc "$QA/n2.crt" "$QA/n.key"; try nobc` → `CA_NOT_A_CA`.
29. keyUsage без keyCertSign: `mkcert "$QA/n.key" "$QA/ku.crt" -days 3650 -addext basicConstraints=critical,CA:TRUE -addext keyUsage=critical,digitalSignature; src ku "$QA/ku.crt" "$QA/n.key"; try ku`
    → `CA_KEY_USAGE`, называет `keyCertSign`.
30. Без keyUsage принимается: `mkcert "$QA/n.key" "$QA/noku.crt" -days 3650 -addext basicConstraints=critical,CA:TRUE; src noku "$QA/noku.crt" "$QA/n.key"; try noku`
    → `up=0`, отпечаток `fp "$QA/noku.crt"`.
31. Ещё не действует: `mkcert "$QA/n.key" "$QA/nb.crt" "${CAEXT[@]}" -not_before "$(date -u -d '+1 hour' +%Y%m%d%H%M%SZ)" -not_after 20360101000000Z; src nyv "$QA/nb.crt" "$QA/n.key"; try nyv`
    → `CA_NOT_YET_VALID`, называет `notBefore`.
32. Истёк: `mkcert "$QA/n.key" "$QA/ex.crt" "${CAEXT[@]}" -not_before 20200101000000Z -not_after 20250101000000Z; src exp "$QA/ex.crt" "$QA/n.key"; try exp`
    → `CA_EXPIRED`, называет `notAfter` 2025-01-01.
33. Порядок проверок: ключ `x.key` к сертификату `ex.crt` → `src order "$QA/ex.crt" "$QA/x.key"; try order` → `CA_KEY_MISMATCH`, в тексте нет `CA_EXPIRED`.
34. Каталог CA только для чтения:
    `printf 'services:\n  server:\n    volumes:\n      - sard-pki:/var/lib/sard/pki:ro\n' > "$QA/ro.yml"; fresh; QA_SRC="$QA/good" $DC -f "$QA/override.yml" -f "$QA/ro.yml" up -d --wait server`
    → не стартует; `CA import refused`, `IMPORT_WRITE_FAILED`, называет `/var/lib/sard/pki`; источник не изменён.
35. После исправления: `variant fix; sudo chmod 640 "$QA/fix/ca/ca.key"; try fix` → отказ, как в шаге 18;
    затем `sudo chmod 600 "$QA/fix/ca/ca.key"; try fix` → `up=0`, отпечаток `$F`.

### Срок CA [OQ-177]

36. Истекает через 30 дней: `mkcert "$QA/n.key" "$QA/soon.crt" -days 30 "${CAEXT[@]}"; src soon "$QA/soon.crt" "$QA/n.key"; try soon`
    → `up=0`; WARN, что CA истекает `<дата>`, осталось 30 дней.
37. Перезапуск без источника: `$DC up -d --force-recreate --wait server` → предупреждения о сроке в логе этого старта нет.
38. Истекает через 120 дней: то же с `-days 120` → `up=0`, предупреждения о сроке нет.

### Отпечаток при каждом старте [OQ-181]

39. `fresh; $DC up -d --wait server` (без источника) → в логе отпечаток нового CA с происхождением `generated`, он не равен `$F`.

Вернуть стенд: `fresh; $DC up -d --wait server` (новый CA) или `try good` (CA `$F`).

## Часть 2. Переезд A → B

### Стенд

- ВМ **A** и **B** — установка по `docs/operator/02-install.md`; имя для агентов
  `sard.example.com` (DNS-запись, которую можно перевести с A на B; без DNS —
  запись в `/etc/hosts` на хостах агентов).
- Хосты агентов **H1**, **H2**, **H3** (Debian 12), пакет агента установлен
  (`docs/operations/agent-install.md`).
- На A: H1 и H2 зарегистрированы и «в сети»; на H1 есть источник `files` с
  успешным запуском. Создан токен **T** (не использован), его строка сохранена.
  Отпечаток из токена: `FA=${T##*.}`.

### Шаги

1. На A: в консоли на странице токенов отпечаток CA → равен `$FA` [OQ-181].
2. На A: бэкап по `docs/operator/06-data-and-backup.md` (дамп базы и архив
   `sard-pki-*.tgz`), затем `docker compose stop server`.
   `tar -xzOf sard-pki-*.tgz ca/ca.crt > /tmp/a.crt; fp /tmp/a.crt` → `$FA`.
3. На H1: `sudo sha256sum /etc/sard/tls/* /etc/sard/agent.yaml > /tmp/id.before`
   (файлы личности агента — `docs/operations/agent-enroll.md`); то же на H2.
4. На B: шаги 1–3 раздела 2 **без запуска**, старый `.env` вместо нового.
   Подготовить источник по `docs/operator/08`:

   ```bash
   cd ~/sard && mkdir pki-import && tar -C pki-import -xzf sard-pki-*.tgz
   sudo chown -R 10001:10001 pki-import && sudo chmod 700 pki-import pki-import/ca && sudo chmod 600 pki-import/ca/*
   ```

   и `docker-compose.override.yml` из раздела 8 (монтирование `./pki-import`
   только для чтения, `SARD_PKI_IMPORT_DIR`).
   `sudo find pki-import -printf '%p %m %u %T@\n' > /tmp/src.before; sudo sha256sum pki-import/ca/* >> /tmp/src.before`.
5. На B: база — `docker compose up -d --wait postgres`, затем `pg_restore` по
   разделу 6 («Восстановление», только часть «база»). Сервер ещё не запускался.
6. На B: `docker compose up -d --wait`
   → сервер `healthy`; `docker compose logs server | grep -E 'CA import|fingerprint'`
   → строка об импорте из `/var/lib/sard/pki-import` с отпечатком `$FA`.
7. На B: `echo | openssl s_client -connect localhost:9090 -alpn h2 2>/dev/null | openssl x509 -noout -ext subjectAltName`
   → имена из `SARD_PKI_SERVER_NAMES` B, среди них `sard.example.com`.
8. На B: `stat -c '%a %u %n'` каталога CA в томе (команда шага 2 части 1 с
   томом `sard_sard-pki` B) → `700`/`600`, владелец `10001`; источник —
   `diff` с `/tmp/src.before` пуст.
9. Перевести `sard.example.com` на B. Подождать до 2 минут.
   → в консоли B агенты H1 и H2 «в сети», их идентификаторы те же, что были на
   A; число агентов то же; на H1 и H2 `sudo sha256sum /etc/sard/tls/* /etc/sard/agent.yaml | diff - /tmp/id.before` пуст;
   `journalctl -u sard-agent` на H1 — переподключение без ошибок TLS.
10. В консоли B запустить бэкап источника H1 → запуск завершается успешно.
11. На H3: команда регистрации с токеном **T** и `--server sard.example.com:9090`
    → регистрация успешна; H3 «в сети» на B; токен T в консоли B — «использован»,
    называет агента H3.
12. На B: страница токенов → отпечаток `$FA`; создать новый токен → его строка
    оканчивается на `$FA`.
13. На B: `docker compose logs server | grep -cF "$(sudo sed -n 2p pki-import/ca/ca.key)"` → `0`
    (повторить для каждой строки base64 ключа).
14. Убрать `SARD_PKI_IMPORT_DIR` и override, `docker compose up -d --force-recreate --wait server`
    → стартует с `$FA`, происхождение `existing` [OQ-181]; агенты в сети.

### Без базы: понятная причина [OQ-183, OQ-185]

15. На B: `docker compose down -v`; снова шаги 4 (источник уже есть) и 6 **без**
    шага 5 (база пустая).
16. На H1: `sudo systemctl restart sard-agent; sleep 10; journalctl -u sard-agent -n 5`
    → строка отказа с `reason CERT_UNKNOWN`; служба остановлена (код 78, не
    перезапускается).
17. На B: `docker compose logs server | grep CERT_UNKNOWN`
    → строка с serial, идентификатором агента H1 и тенантом, и пояснением, что
    сертификат выпущен CA этого сервера, но в базе записи нет (база не
    восстановлена или старше регистрации).
18. На B: остановить сервер, восстановить базу (шаг 5), запустить сервер; на H1
    — команда из `docs/operator/08` для агентов, подключившихся до
    восстановления базы (`sudo systemctl restart sard-agent`)
    → H1 «в сети».

## Часть 3. Консоль

Дважды: на моках (`cd web && VITE_API_MOCKS=1 npm run dev`) и против сервера
(`make up`). На моках отпечаток —
`8544e2352a80a3d403eed68f8bb4ff271d0ce02c09c1d0faf6f090cea423be9f`, против
сервера — `fp` от `ca.crt` из тома.

1. Войти, открыть «Токены» → подпись «Отпечаток CA сервера», значение — 64
   символа hex, моноширинное, в одну строку, совпадает с ожидаемым.
2. Кнопка копирования отпечатка → в буфере ровно отпечаток (вставить в
   терминал: `echo -n '<вставка>' | wc -c` → `64`).
3. Подсказка у отпечатка → локализована (RU и EN), говорит о совпадении с
   окончанием строки токена и с логом старта сервера.
4. Создать токен → часть строки токена после последней точки равна отпечатку
   на странице.
5. Против сервера: DevTools → Network: запрос сведений о CA идёт с сессией;
   `curl -s http://localhost:8080/api/v1/status` не содержит отпечаток; запрос
   сведений о CA без cookie → 401.
6. Против сервера: остановить сервер при открытой странице и обновить её
   (или на моках вернуть `unavailable`) → список токенов (или его ошибка)
   показан отдельно, на месте отпечатка — локализованное сообщение об ошибке.
