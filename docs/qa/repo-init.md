# QA: `sard-agent repo init`, `sard-agent repo list` и проверка restic при старте (A5b)

Сценарии: `docs/specs/agent/repo-init.feature`. Ответы владельца на вопросы
В1–В9 — 2026-09-30; спецификация в целом, решения С1–С11 и Л1–Л10 ждут
утверждения владельцем. Поправка 2026-09-30 (решения владельца В8а и В8б,
ADR 0028): блокировка init — в `restic.cache_dir`, причина `LOCK_WRITE`;
пакеты deb/rpm создают `/var/cache/sard/restic` при установке. Поправка и
решения С12–С14 утверждены владельцем 2026-09-30 (передано координатором).
Классы и номера кодов выхода — A2b
(`docs/specs/agent/agent-enroll.feature`, В3; ADR 0025).

Выполнима после реализации A5b. Ожидаемый результат указан после «→» в
каждом шаге. Любое расхождение — дефект.

Коды выхода repo init: `0` успех, `1` ошибка агента (restic не найден, старый
или непригоден; несетевой отказ бэкенда), `2` использование (в том числе
неверный пароль существующего репозитория), `4` репозиторий уже
инициализирован, `6` временная (сеть, таймаут, прерывание, идёт другой init),
`7` запись (файл пароля не создать — `PASSWORD_FILE_WRITE`; файл блокировки в
`restic.cache_dir` не создать — `LOCK_WRITE`).

Коды выхода repo list: `0` состояние всех репозиториев известно; иначе код
класса первой по порядку конфига постоянной проблемы строки (`1` или `2`);
`6`, если все проблемы временные; `2` / `1` — ошибка флагов или конфига /
непригодный restic (таблица не печатается). repo list блокировку не берёт.

## Подготовка

Нужны: Go, Docker (только для частей 6 и 9), `curl`, `sha256sum`, `jq`.
Команды выполняются из корня репозитория **не от root** (иначе права файлов не
показательны).

```bash
make build
scripts/fetch-restic.sh
AG=$PWD/agent/bin/sard-agent
RV=$(sed -n 's/^version=//p' agent/internal/restic/restic-version)      # 0.19.1
RMIN=$(sed -n 's/^min_version=//p' agent/internal/restic/restic-version) # 0.19.0
RB=$PWD/.bin/restic/$RV/linux_$(go env GOARCH)/restic
QA=$(mktemp -d); H=$QA/host; mkdir -m 0700 "$H" "$H/sec" "$QA/cache"
printf 'PASS-MARKER-%s\n' "$RANDOM$RANDOM" > "$H/sec/main.pass"; PASS=$(cat "$H/sec/main.pass")
printf 'AWS_SECRET_ACCESS_KEY=ENV-MARKER-%s\n' "$RANDOM" > "$H/sec/main.env"; ENVV=$(cut -d= -f2 "$H/sec/main.env")
printf 'offsite-pass\n' > "$H/sec/offsite.pass"
chmod 0600 "$H"/sec/*
URLM=URL-MARKER-$RANDOM
# mkcfg <restic.path или пусто> — пишет $H/agent.yaml
mkcfg() { cat > "$H/agent.yaml" <<EOF
server:
  address: 127.0.0.1:9
tls:
  ca_file: $H/tls/ca.pem
  cert_file: $H/tls/agent.pem
  key_file: $H/tls/agent.key
repositories:
  - name: main
    url: $H/repo
    password_file: $H/sec/main.pass
    env_file: $H/sec/main.env
  - name: offsite
    url: rest:http://qa:$URLM@127.0.0.1:9/offsite
    password_file: $H/sec/offsite.pass
restic:
  path: $1
  cache_dir: $QA/cache
EOF
[ -n "$1" ] || sed -i '/^  path: $/d' "$H/agent.yaml"; }
mkcfg "$RB"
# ri / rl [аргументы…] — repo init / repo list, печатают код и вывод, копят вывод в $QA/all
ri() { "$AG" repo init "$@" >"$QA/out" 2>"$QA/err"; echo "exit=$?"; cat "$QA/out" "$QA/err"; cat "$QA/out" "$QA/err" >> "$QA/all"; }
init() { ri --config "$H/agent.yaml" "$@"; }
rl() { "$AG" repo list --config "$H/agent.yaml" "$@" >"$QA/out" 2>"$QA/err"; echo "exit=$?"; cat "$QA/out" "$QA/err"; cat "$QA/out" "$QA/err" >> "$QA/all"; }
snap() { find "$H/sec" "$H/agent.yaml" -printf '%m %u %p\n' | sort; sha256sum "$H"/sec/* "$H/agent.yaml"; }
repo() { [ -d "$H/repo" ] && (cd "$H/repo" && find . -type f -exec sha256sum {} + | sort) || echo "no repo"; }
fake() { printf '#!/bin/sh\necho "restic %s compiled with go1.22.1 on linux/amd64"\n' "$1" > "$QA/restic-$1"; chmod 0755 "$QA/restic-$1"; echo "$QA/restic-$1"; }
# locks — число файлов блокировки init в кэше и рядом с файлами паролей (С14)
locks() { find "$QA/cache" "$H/sec" -maxdepth 1 -name '.sard-init-*' 2>/dev/null | wc -l; }
```

1. `"$AG" repo init --help; echo "exit=$?"`
   → `exit=0`; справка говорит, что команда создаёт репозиторий по имени из
   конфига агента, называет `--config` (по умолчанию `/etc/sard/agent.yaml`),
   `--generate-password`, `--timeout` (по умолчанию `2m`), перечисляет коды
   `0`, `1`, `2`, `4`, `6`, `7` со смыслом для repo init; флагов пароля или
   ключа нет.
2. `"$AG" repo list --help; echo "exit=$?"`
   → `exit=0`; справка называет колонки `NAME BACKEND STATUS REPOSITORY_ID`,
   значения `initialized` и `not-initialized`, флаги `--config` и
   `--timeout`, коды `0`, `1`, `2`, `6` и правило выбора кода при проблемах
   строк.

## Часть 1. Отказы repo init до обращения к бэкенду

Перед частью: `snap > "$QA/s0"`. В каждом шаге этой части дополнительно:
`repo` → `no repo`; `snap | diff - "$QA/s0"` → пусто; `locks` → `0`.

3. `"$AG" repo --config "$H/agent.yaml"`; `init` (без имени);
   `init main extra`; `init --insecure main`; `init --timeout 0s main`;
   `init --timeout abc main` → каждый раз `exit=2`.
4. `init --password "$PASS" main` → `exit=2`; `grep -cF "$PASS" "$QA/err"` → `0`.
   То же `init --password-file "$H/sec/main.pass" main` → `exit=2`.
5. `init backup` → `exit=2`; stderr содержит `REPOSITORY_UNKNOWN`, `backup`,
   `main`, `offsite`. `init Main` → `exit=2`, `REPOSITORY_UNKNOWN`.
6. `ri --config "$QA/nope.yaml" main` → `exit=2`; сообщение называет путь.
7. Неподдерживаемый провайдер:
   `sed -i '/name: main/a\    crypto_provider: gost' "$H/agent.yaml"; init main`
   → `exit=2`; `CRYPTO_PROVIDER_UNSUPPORTED`, `gost`, `restic-aes`.
   Вернуть: `mkcfg "$RB"`.
8. `mv "$H/sec/main.pass" "$QA/p.bak"; init main; mv "$QA/p.bak" "$H/sec/main.pass"`
   → `exit=2`; `PASSWORD_FILE_MISSING`, `password_file`, путь файла,
   `--generate-password`.
9. `cp "$H/sec/main.pass" "$QA/p.bak"; : > "$H/sec/main.pass"; init main; cp "$QA/p.bak" "$H/sec/main.pass"`
   → `exit=2`; `PASSWORD_FILE_EMPTY` и путь.
10. Права (A1): для каждой пары выставить права, `init main`, вернуть `0600`:
    `main.pass` — `0640`, `0604`; `main.env` — `0640`, `0644`.
    → каждый раз `exit=2`; сообщение — текст A1 (ключ
    `repositories[0].password_file` или `repositories[0].env_file`, путь,
    текущие права, «owner bits only») — дословно как у
    `"$AG" --config "$H/agent.yaml"` с теми же правами (сравнить строки без
    префикса команды).
11. `mv "$H/sec/main.env" "$QA/e.bak"; init main; mv "$QA/e.bak" "$H/sec/main.env"`
    → `exit=2`; `ENV_FILE_MISSING`, `env_file`, путь.
12. `cp "$H/sec/main.env" "$QA/e.bak"; echo "RESTIC_PASSWORD=$ENVV" >> "$H/sec/main.env"; init main; cp "$QA/e.bak" "$H/sec/main.env"`
    → `exit=2`; `ENV_FILE_INVALID`, путь и номер строки; `grep -cF "$ENVV" "$QA/err"` → `0`.
13. Команда не ждёт ввода:
    `sleep 600 | "$AG" repo init --config "$H/agent.yaml" backup; echo "exit=$?"`
    → завершается сразу, `exit=2`.
14. Права чужого репозитория не проверяются (исключение из инварианта части —
    репозиторий создаётся):
    `chmod 0644 "$H/sec/offsite.pass"; init main; chmod 0600 "$H/sec/offsite.pass"`
    → `exit=0`; `locks` → `0`. Затем `rm -rf "$H/repo"`.

## Часть 2. restic не найден, устарел, непригоден

После каждого шага `repo` → `no repo`, `locks` → `0`; в конце части `mkcfg "$RB"`.

15. `mkcfg "$QA/nope-restic"; init main`
    → `exit=1`; `RESTIC_NOT_FOUND`, `restic.path`, `$QA/nope-restic`, `$RMIN`.
    `rl` → `exit=1`, `RESTIC_NOT_FOUND`, stdout пуст.
16. `mkcfg ""; init main` (в `agent/bin/` нет `restic`)
    → `exit=1`; `RESTIC_NOT_FOUND`, путь `$PWD/agent/bin/restic`, сказано,
    что `restic.path` не задан и его можно задать.
17. Для версий `0.18.1`, `0.9.6`: `mkcfg "$(fake <версия>)"; init main`
    → `exit=1`; `RESTIC_TOO_OLD`, найденная версия и `$RMIN`.
18. `mkcfg "$QA"; init main` (каталог);
    `cp "$RB" "$QA/noexec"; chmod 0644 "$QA/noexec"; mkcfg "$QA/noexec"; init main`;
    `printf '#!/bin/sh\necho hello\n' > "$QA/hello"; chmod 0755 "$QA/hello"; mkcfg "$QA/hello"; init main`
    → каждый раз `exit=1`; `RESTIC_UNUSABLE` и путь.
19. `mkcfg "$QA/nope-restic"; mv "$H/sec/main.pass" "$QA/p.bak"; init --generate-password main`
    → `exit=1`, `RESTIC_NOT_FOUND`; `ls "$H/sec/main.pass"` → нет файла.
    Вернуть: `mv "$QA/p.bak" "$H/sec/main.pass"; mkcfg "$RB"`.

## Часть 3. Успех и повтор

20. `init main`
    → `exit=0`; stdout содержит `main`, `local`, repository_id из 64
    шестнадцатеричных символов (далее `X`), путь `$H/sec/main.pass`, фразы
    `only on this host` и `unrecoverable`, совет перезапустить службу
    `sard-agent`, чтобы сервер получил repository_id; `locks` → `0`.
21. `RESTIC_PASSWORD_FILE="$H/sec/main.pass" "$RB" -r "$H/repo" cat config | jq -r .id`
    → `X`.
22. `snap | diff - "$QA/s0"` → пусто (конфиг и файлы паролей не изменились,
    рядом с ними нет новых файлов).
23. `repo > "$QA/r1"; init main`
    → `exit=4`; `REPOSITORY_EXISTS` и `X`; `repo | diff - "$QA/r1"` → пусто;
    `locks` → `0`.
24. `cp "$H/sec/main.pass" "$QA/p.bak"; printf 'other\n' > "$H/sec/main.pass"; init main; cp "$QA/p.bak" "$H/sec/main.pass"`
    → `exit=2`; `WRONG_PASSWORD`; сказано, что по адресу уже есть репозиторий,
    а файл пароля его не открывает; `repo | diff - "$QA/r1"` → пусто.
25. Вывод в файл: `rm -rf "$H/repo"; "$AG" repo init --config "$H/agent.yaml" main > "$QA/o.txt"; grep -c 'unrecoverable' "$QA/o.txt"`
    → `1` или больше.
26. Окружение оператора не влияет: `rm -rf "$H/repo"; RESTIC_REPOSITORY="$QA/elsewhere" RESTIC_PASSWORD=zzz init main`
    → `exit=0`; `ls "$QA/elsewhere"` → нет такого каталога; шаг 21 даёт id из
    вывода.
27. Флаги после имени: `rm -rf "$H/repo"; ri main --config "$H/agent.yaml"` → `exit=0`.
28. Файл пароля из одного перевода строки:
    `rm -rf "$H/repo"; cp "$H/sec/main.pass" "$QA/p.bak"; printf '\n' > "$H/sec/main.pass"; init main; cp "$QA/p.bak" "$H/sec/main.pass"`
    → код не `0`; `repo` → `no repo` или в каталоге нет файла `config`.

## Часть 4. Генерация пароля и каталог файла пароля

29. `rm -rf "$H/repo"; mv "$H/sec/main.pass" "$QA/p.bak"; (umask 000; init --generate-password main)`
    → `exit=0`; stdout говорит, что файл пароля создан этой командой;
    `stat -c '%a %U' "$H/sec/main.pass"` → `600 $(id -un)`;
    `grep -cE '^[A-Za-z0-9_-]{43}$' "$H/sec/main.pass"` → `1`, в файле одна строка;
    `grep -cF "$(cat "$H/sec/main.pass")" "$QA/all"` → `0`.
30. `cp "$H/sec/main.pass" "$QA/gen1"; rm -rf "$H/repo"; init --generate-password main`
    → `exit=0`; stdout говорит, что использован существующий файл;
    `cmp "$H/sec/main.pass" "$QA/gen1"` → совпадают.
31. `chmod 0644 "$H/sec/main.pass"; init --generate-password main; chmod 0600 "$H/sec/main.pass"`
    → `exit=2` (A1); файл не изменился.
32. Каталог недоступен на запись: `rm "$H/sec/main.pass"; chmod 0500 "$H/sec"; init --generate-password main; chmod 0700 "$H/sec"`
    → `exit=7`; `PASSWORD_FILE_WRITE` и каталог; файла нет; `locks` → `0`.
    Каталога нет: в конфиге `password_file: $QA/nodir/main.pass`, `init --generate-password main`
    → `exit=7`; `PASSWORD_FILE_WRITE`; `ls -d "$QA/nodir"` → нет каталога.
    Вернуть `mkcfg "$RB"`.
33. Неудача после генерации: `rm -rf "$H/repo" "$H/sec/main.pass"; sed -i "s|url: $H/repo|url: rest:http://127.0.0.1:9/main|" "$H/agent.yaml"; init --generate-password main`
    → `exit=6`; `BACKEND_UNAVAILABLE`; `stat -c %a "$H/sec/main.pass"` → `600`;
    сообщение говорит, что созданный файл пароля сохранён; `locks` → `0`.
    Вернуть: `mkcfg "$RB"; mv "$QA/p.bak" "$H/sec/main.pass"`.
34. Существующий файл пароля в каталоге только для чтения (В8а):
    `rm -rf "$H/repo"; snap > "$QA/s1"; chmod 0500 "$H/sec"; init main`
    → `exit=0`; в выводе нет `PASSWORD_FILE_WRITE` и `LOCK_WRITE`.
    `rm -rf "$H/repo"; init --generate-password main`
    → `exit=0`; stdout говорит, что использован существующий файл.
    Затем `chmod 0700 "$H/sec"; snap | diff - "$QA/s1"` → пусто; `locks` → `0`.
    Затем `rm -rf "$H/repo"`.

## Часть 5. Таймаут, прерывание, одновременный запуск, блокировка

Неотвечающий адрес: `10.255.255.1` (пакеты теряются). Перед частью:
`mkcfg "$RB"; rm -rf "$H/repo"; sed -i "s|url: $H/repo|url: rest:http://10.255.255.1:8000/main|" "$H/agent.yaml"`.

35. `time init --timeout 5s main`
    → завершается примерно через 5 с, `exit=6`, `TIMEOUT`; сказано, что
    репозиторий мог быть создан частично; `pgrep -f "$RB"` → пусто;
    `locks` → `0`.
36. `time init main` (без `--timeout`)
    → завершается примерно через 2 мин, не раньше, `exit=6`, `TIMEOUT`.
37. `"$AG" repo init --config "$H/agent.yaml" main & P=$!; sleep 3; kill -INT $P; wait $P; echo "exit=$?"`
    → `exit=6`; `INTERRUPTED`; сказано, что репозиторий мог быть создан
    частично и команду нужно повторить; `pgrep -f "$RB"` → пусто;
    `locks` → `0`.
38. Одновременный запуск и место блокировки:
    `"$AG" repo init --config "$H/agent.yaml" main & P=$!; sleep 1`;
    `ls -A "$QA/cache" | grep -c '^\.sard-init-main\.lock$'` → `1`;
    `ls -A "$H/sec" | grep -c '^\.sard-init-'` → `0`;
    `init main` → сразу `exit=6`, `INIT_IN_PROGRESS`;
    `kill -INT $P; wait $P`. Затем `init --timeout 2s main`
    → нет `INIT_IN_PROGRESS` (блокировка снята), `exit=6`, `TIMEOUT`;
    `locks` → `0`.
39. Разные репозитории: `"$AG" repo init --config "$H/agent.yaml" main & P=$!; sleep 1; init offsite; kill -INT $P; wait $P`
    → вторая команда без `INIT_IN_PROGRESS` (`exit=6`, `BACKEND_UNAVAILABLE`).
40. Оставшийся файл блокировки, который никто не удерживает:
    `printf '999999\n' > "$QA/cache/.sard-init-main.lock"; init --timeout 2s main`
    → нет `INIT_IN_PROGRESS`, `exit=6`, `TIMEOUT`; `locks` → `0`.
41. Каталога кэша нет (`LOCK_WRITE`, С13):
    `sed -i "s|cache_dir: .*|cache_dir: $QA/nocache|" "$H/agent.yaml"; snap > "$QA/s2"; init main`
    → сразу (без ожидания бэкенда) `exit=7`; `LOCK_WRITE`, `restic.cache_dir`,
    `$QA/nocache`; `ls -d "$QA/nocache"` → нет каталога;
    `snap | diff - "$QA/s2"` → пусто. Вернуть `mkcfg "$RB"` и снова
    `sed` адреса из преамбулы части.
42. Каталог кэша недоступен на запись, пароль не генерируется:
    `mv "$H/sec/main.pass" "$QA/p.bak"; chmod 0500 "$QA/cache"; init --generate-password main; chmod 0700 "$QA/cache"`
    → сразу `exit=7`; `LOCK_WRITE`, `restic.cache_dir`, `$QA/cache`;
    `ls "$H/sec/main.pass"` → нет файла; `locks` → `0`.
    Вернуть: `mv "$QA/p.bak" "$H/sec/main.pass"`.
43. Путь кэша — обычный файл: `: > "$QA/cfile"; sed -i "s|cache_dir: .*|cache_dir: $QA/cfile|" "$H/agent.yaml"; init main`
    → сразу `exit=7`; `LOCK_WRITE` и `$QA/cfile`.
    Вернуть: `mkcfg "$RB"`.

## Часть 6. Недоступный и отказывающий бэкенд

44. `init offsite` (REST на закрытом порту 9, пароль `$URLM` в адресе)
    → `exit=6`; `BACKEND_UNAVAILABLE`, тип `rest`, причина от restic
    (`connection refused`), совет повторить; `grep -cF "$URLM" "$QA/all"` → `0`.
45. Неверные учётные данные S3 (MinIO):
    `docker run -d --rm --name qa-minio -p 19000:9000 -e MINIO_ROOT_USER=qaadmin -e MINIO_ROOT_PASSWORD=qaadmin-secret minio/minio server /data`;
    дождаться порта; в конфиге у `main`: `url: s3:http://127.0.0.1:19000/qa-bucket`,
    в `main.env`: `AWS_ACCESS_KEY_ID=wrong` и `AWS_SECRET_ACCESS_KEY=$ENVV`;
    `init main`
    → `exit=1`; `BACKEND_REFUSED`, тип `s3`, причина от restic;
    `grep -cF "$ENVV" "$QA/all"` → `0`; `locks` → `0`.
46. Верные учётные данные (`qaadmin` / `qaadmin-secret` в `main.env`): `init main`
    → `exit=0`, тип `s3`. `docker stop qa-minio`; `init main`
    → `exit=6`, `BACKEND_UNAVAILABLE`. Вернуть конфиг (`mkcfg "$RB"`) и `main.env`.

## Часть 7. Список репозиториев

Перед частью: `mkcfg "$RB"; rm -rf "$H/repo"; init main` (запомнить `X`).

47. `rl`
    → `exit=6`; первая строка stdout — `NAME BACKEND STATUS REPOSITORY_ID`;
    строка `main local initialized X`; строка `offsite rest BACKEND_UNAVAILABLE -`;
    в stderr сообщение, начинающееся с `offsite`; `grep -cF "$URLM" "$QA/all"` → `0`;
    stdout не содержит `$H/repo`; `locks` → `0`.
48. Убрать `offsite` из конфига (`sed -i '/name: offsite/,/offsite.pass/d' "$H/agent.yaml"`),
    добавить репозиторий `spare` с `url: $QA/spare`, `password_file: $H/sec/offsite.pass`; `rl`
    → `exit=0`; `main … initialized X`, `spare local not-initialized -`;
    `ls "$QA/spare"` → нет каталога (список ничего не создаёт).
49. `chmod 0644 "$H/sec/offsite.pass"; rl; chmod 0600 "$H/sec/offsite.pass"`
    → `exit=2`; `spare … SECRET_FILE_REJECTED -`; `main` по-прежнему
    `initialized X`; stderr содержит текст A1 о файле.
50. `printf 'other\n' > "$QA/o.pass"; chmod 0600 "$QA/o.pass"`; у `main`
    `password_file: $QA/o.pass`; `rl`
    → `exit=2`; `main local WRONG_PASSWORD -`.
    Вернуть `password_file` main.
51. Порядок кодов: у `main` — `url: rest:http://127.0.0.1:9/main`, у `spare` —
    `password_file` на несуществующий файл; `rl`
    → `exit=2` (постоянная проблема важнее временной); `main … BACKEND_UNAVAILABLE`,
    `spare … PASSWORD_FILE_MISSING`.
52. Таймаут списка: у `main` — `url: rest:http://10.255.255.1:8000/main`,
    `spare` — исправный; `time rl --timeout 3s`
    → примерно 3 с, `exit=6`; `main … TIMEOUT -`, `spare … not-initialized -`.
53. `rl extra` → `exit=2`; `rl --json` → `exit=2`; `rl --timeout 0s` → `exit=2`.
54. Конфиг без `repositories`: `rl` → `exit=0`; stdout говорит, что
    репозиториев не задано.
55. `sleep 600 | "$AG" repo list --config "$H/agent.yaml"; echo "exit=$?"`
    → завершается, не дожидаясь ввода.
56. Список не берёт блокировку: `mkcfg "$RB"; sed -i "s|url: $H/repo|url: rest:http://10.255.255.1:8000/main|" "$H/agent.yaml"`;
    `"$AG" repo init --config "$H/agent.yaml" main & P=$!; sleep 1; time rl --timeout 3s`
    → список завершается примерно через 3 с, пока `repo init` ещё работает
    (`kill -0 $P` → успех); в выводе списка нет `INIT_IN_PROGRESS` и
    `LOCK_WRITE`. Затем `kill -INT $P; wait $P`.
57. Список без каталога кэша: `mkcfg "$RB"; sed -i "s|cache_dir: .*|cache_dir: $QA/nocache|" "$H/agent.yaml"; rl`
    → в выводе нет `LOCK_WRITE`; строка `main` — `initialized X`.
    Вернуть: `mkcfg "$RB"`.

## Часть 8. Старт агента

Сервер не нужен для отказов: агент должен завершиться до сети.
`start` — `timeout 10 "$AG" --config "$H/agent.yaml" >"$QA/sout" 2>"$QA/serr"; echo "exit=$?"; cat "$QA/sout" "$QA/serr"`.

58. `mkcfg "$QA/nope-restic"; start`
    → сразу `exit=1`; stderr начинается с `sard-agent: `, содержит
    `RESTIC_NOT_FOUND`, `restic.path`, путь, `$RMIN`; в stdout нет `connecting to`.
59. `mkcfg ""; start` → `exit=1`; путь `$PWD/agent/bin/restic`, «restic.path не задан».
60. `mkcfg "$(fake 0.18.1)"; start` → `exit=1`; `RESTIC_TOO_OLD`, `0.18.1`, `$RMIN`.
61. `mkcfg "$QA/hello"; start` → `exit=1`; `RESTIC_UNUSABLE`, путь.
62. Порядок: `mkcfg "$QA/nope-restic"; chmod 0644 "$H/sec/main.pass"; start; chmod 0600 "$H/sec/main.pass"`
    → `exit=1`; сообщение A1 о `password_file`; `RESTIC_NOT_FOUND` нет.
63. Для `mkcfg "$(fake 0.19.0)"`, `mkcfg "$(fake 0.20.0)"` и `mkcfg "$RB"`:
    выполнить части «Подготовка» и 2 `docs/qa/agent-enroll.md` для этого
    `$H` (сервер поднят, агент зарегистрирован), `start`
    → в stdout `connecting to`, `exit=124`, `RESTIC_TOO_OLD` нет.
64. enroll без restic: `mkcfg "$QA/nope-restic"`, новый токен, `rm "$H"/tls/*`;
    `"$AG" enroll --config "$H/agent.yaml" --token <токен>` (адрес сервера в
    конфиге) → `exit=0`.

## Часть 9. Пакет deb/rpm: каталог кэша и repository_id на сервере

Пакет — из `make package` (`dist/`). Шаги 65а–65д — в чистом контейнере
дистрибутива (Debian 12 для deb, Rocky 9 для rpm) с systemd, служба не
включена и не запускалась; выполнить для каждого формата.

65а. `ls -d /var/cache/sard/restic` → нет каталога. Установить пакет
     (`apt install ./dist/sard-agent_*.deb` или `dnf install ./dist/sard-agent-*.rpm`)
     → `stat -c '%U %G %a' /var/cache/sard/restic` → `sard-agent sard-agent 700`;
     `systemctl is-active sard-agent` → `inactive`.
65б. До первого старта службы: `/etc/sard/agent.yaml` с `server.address`,
     `tls.*` и репозиторием `main` (`url: /srv/qa-repo`, каталог `/srv`
     доступен на запись `sard-agent`, `password_file: /etc/sard/restic/main.pass`,
     `sudo install -d -o sard-agent -g sard-agent -m 0700 /etc/sard/restic`),
     без `restic.cache_dir`;
     `sudo -u sard-agent sard-agent repo init --generate-password main; echo "exit=$?"`
     → `exit=0`; в выводе нет `LOCK_WRITE`;
     `sudo ls -A /var/cache/sard/restic | grep -c '^\.sard-init-'` → `0`;
     `systemctl is-active sard-agent` → `inactive`.
65в. `sudo touch /var/cache/sard/restic/F; sudo chown root:root /var/cache/sard/restic; sudo chmod 0755 /var/cache/sard/restic`;
     переустановить пакет (`apt install --reinstall …` / `dnf reinstall …`)
     → `ls /var/cache/sard/restic/F` → есть;
     `stat -c '%U %G %a' /var/cache/sard/restic` → `sard-agent sard-agent 700`.
65г. После регистрации агента (`docs/qa/agent-enroll.md`)
     `sudo systemctl start sard-agent`
     → `stat -c '%a' /var/cache/sard/restic` → `700` (старт службы права не
     меняет).
65д. Удалить пакет (`apt remove sard-agent` / `dnf remove sard-agent`)
     → `ls /var/cache/sard/restic/F` → есть.
     Архив: `tar tzf dist/sard-agent_*_linux_amd64.tar.gz | grep -c 'var/cache'` → `0`.

65. На хосте с установленным пакетом агент зарегистрирован, служба запущена;
    в `/etc/sard/agent.yaml` репозиторий `main` с локальным `url`, каталог для
    файла пароля создан для пользователя службы:
    `sudo install -d -o sard-agent -g sard-agent -m 0700 /etc/sard/restic`;
    `password_file: /etc/sard/restic/main.pass`. Перезапустить службу.
    `$PSQL "select name, backend, repository_id from agent_repositories where agent_id = '<id>'"`
    → `main | local | ` (id пуст).
66. `sudo -u sard-agent sard-agent repo init --generate-password main`
    → `exit=0`; владелец файла пароля — `sard-agent`, права `600`; вывод
    называет repository_id `X`; `sudo ls -A /var/cache/sard/restic | grep -c '^\.sard-init-'`
    → `0`. `sudo -u sard-agent sard-agent repo list`
    → `main local initialized X`.
67. До перезапуска: запрос шага 65 → id по-прежнему пуст (конфиг не
    перечитывается; при обрыве связи id может прийти раньше — это не дефект).
68. `sudo systemctl restart sard-agent`; запрос шага 65 → `main | local | X`.
69. Файл пароля в каталоге root (В8а): второй репозиторий `third` в конфиге с
    локальным `url` и `password_file: /etc/sard/third.pass`;
    `stat -c '%U %a' /etc/sard` → `root 755`;
    `head -c 32 /dev/urandom | base64 | sudo install -o sard-agent -g sard-agent -m 0600 /dev/stdin /etc/sard/third.pass`;
    `sudo -u sard-agent sard-agent repo init third`
    → `exit=0`; в выводе нет `PASSWORD_FILE_WRITE` и `LOCK_WRITE`;
    `sudo ls -A /etc/sard | grep -c '^\.sard-init-'` → `0`.
70. От root без `sudo -u`: `sudo sard-agent repo init --generate-password other`
    (ещё один репозиторий в конфиге) → файл пароля принадлежит `root`;
    `sudo ls -A /var/cache/sard/restic | grep -c '^\.sard-init-'` → `0`;
    `sudo systemctl restart sard-agent` → служба не стартует, в журнале
    сообщение A1 о владельце файла. Это ожидаемо и описано в
    `docs/operations/repo-init.md`.
71. `docs/operations/repo-init.md` существует и говорит: выполнять команды от
    имени пользователя службы (`sudo -u sard-agent`); сохранить копию файла
    пароля вне хоста; перезапустить службу после инициализации;
    `restic.cache_dir` должен существовать и быть доступен на запись
    пользователю команды (пакет deb/rpm создаёт каталог по умолчанию при
    установке; архив tar.gz или другой путь — создать самому, владелец —
    пользователь службы, права 0700), иначе `LOCK_WRITE`; таблицы кодов выхода repo init и
    repo list совпадают со справками.

## Часть 10. Секреты не утекают

72. `grep -cF "$PASS" "$QA/all"` → `0`; `grep -cF "$ENVV" "$QA/all"` → `0`;
    `grep -cF "$URLM" "$QA/all"` → `0`; то же для `$QA/serr`.

Вручную не воспроизводятся и проверяются тестами `@local` с теми же
названиями: гонка «репозиторий создан между проверкой и init», непредусмотренный
вывод restic, restic, печатающий значения из `env_file`, `BACKEND_REFUSED` в
строке списка, прерывание списка, агент через символьную ссылку с restic в
каталоге настоящего файла, блокировка в каталоге кэша по умолчанию при
незаданном `restic.cache_dir`.
