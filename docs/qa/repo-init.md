# QA: `sard-agent repo init` и проверка restic при старте (A5b)

Сценарии: `docs/specs/agent/repo-init.feature` (ЧЕРНОВИК, ждёт утверждения
владельцем). Классы и номера кодов выхода — A2b
(`docs/specs/agent/agent-enroll.feature`, В3; ADR 0025). Шаги, помеченные
**(В1)** … **(В8)**, зависят от открытых вопросов спецификации и проходятся
только после решения владельца; ожидаемый результат в них — по рекомендации
specifier.

Выполнима после реализации A5b. Ожидаемый результат указан после «→» в
каждом шаге. Любое расхождение — дефект.

Коды выхода repo init: `0` успех, `1` ошибка агента (restic не найден, старый
или непригоден; несетевой отказ бэкенда — В5), `2` использование, `4`
репозиторий уже инициализирован (В3), `6` временная, `7` запись (В1).

## Подготовка

Нужны: Go, Docker (только для частей 5 и 7), `curl`, `sha256sum`, `jq`.
Команды выполняются из корня репозитория **не от root** (иначе права файлов не
показательны).

```bash
make build
scripts/fetch-restic.sh
AG=$PWD/agent/bin/sard-agent
RV=$(sed -n 's/^version=//p' agent/internal/restic/restic-version)      # 0.19.1
RMIN=$(sed -n 's/^min_version=//p' agent/internal/restic/restic-version) # 0.19.0
RB=$PWD/.bin/restic/$RV/linux_$(go env GOARCH)/restic
QA=$(mktemp -d); H=$QA/host; mkdir -m 0700 "$H" "$H/sec"
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
# ri [аргументы…] — sard-agent repo init, печатает код и вывод, копит вывод в $QA/all
ri() { "$AG" repo init "$@" >"$QA/out" 2>"$QA/err"; echo "exit=$?"; cat "$QA/out" "$QA/err"; cat "$QA/out" "$QA/err" >> "$QA/all"; }
init() { ri --config "$H/agent.yaml" "$@"; }
snap() { find "$H/sec" "$H/agent.yaml" -printf '%m %u %p\n' | sort; sha256sum "$H"/sec/* "$H/agent.yaml"; }
repo() { [ -d "$H/repo" ] && (cd "$H/repo" && find . -type f -exec sha256sum {} + | sort) || echo "no repo"; }
```

1. `"$AG" repo init --help; echo "exit=$?"`
   → `exit=0`; справка говорит, что команда создаёт репозиторий по имени из
   конфига агента, называет `--config` (по умолчанию `/etc/sard/agent.yaml`),
   перечисляет коды `0`, `1`, `2`, `4`, `6`, `7` со смыслом для repo init;
   флагов пароля или ключа нет. **(В1)** называет `--generate-password`.

## Часть 1. Отказы до обращения к бэкенду

Перед частью: `snap > "$QA/s0"`. В каждом шаге этой части дополнительно:
`repo` → `no repo`; `snap | diff - "$QA/s0"` → пусто.

2. `"$AG" repo --config "$H/agent.yaml"` ; `init` (без имени);
   `init main extra`; `init --insecure main` → каждый раз `exit=2`.
3. `init --password "$PASS" main` → `exit=2`; `grep -cF "$PASS" "$QA/err"` → `0`.
   То же `init --password-file "$H/sec/main.pass" main` → `exit=2`.
4. `init backup` → `exit=2`; stderr содержит `REPOSITORY_UNKNOWN`, `backup`,
   `main`, `offsite`. `init Main` → `exit=2`, `REPOSITORY_UNKNOWN`.
5. `ri --config "$QA/nope.yaml" main` → `exit=2`; сообщение называет путь.
6. Неподдерживаемый провайдер:
   `sed -i '/name: main/a\    crypto_provider: gost' "$H/agent.yaml"; init main`
   → `exit=2`; `CRYPTO_PROVIDER_UNSUPPORTED`, `gost`, `restic-aes`.
   Вернуть: `mkcfg "$RB"`.
7. `mv "$H/sec/main.pass" "$QA/p.bak"; init main; mv "$QA/p.bak" "$H/sec/main.pass"`
   → `exit=2`; `PASSWORD_FILE_MISSING`, `password_file`, путь файла.
   **(В1)** сообщение называет `--generate-password`.
8. `cp "$H/sec/main.pass" "$QA/p.bak"; : > "$H/sec/main.pass"; init main; cp "$QA/p.bak" "$H/sec/main.pass"`
   → `exit=2`; `PASSWORD_FILE_EMPTY` и путь.
9. Права (A1): для каждой пары выставить права, `init main`, вернуть `0600`:
   `main.pass` — `0640`, `0604`; `main.env` — `0640`, `0644`.
   → каждый раз `exit=2`; сообщение — текст A1: ключ
   `repositories[0].password_file` или `repositories[0].env_file`, путь,
   текущие права и требование «owner bits only» — дословно как у
   `"$AG" --config "$H/agent.yaml"` с теми же правами (сравнить обе строки
   без префикса команды).
10. `chmod 0644 "$H/sec/offsite.pass"; init main; chmod 0600 "$H/sec/offsite.pass"`
    → `exit=0`: права файла другого репозитория не проверяются (исключение из
    инварианта части: репозиторий создан). Затем `rm -rf "$H/repo"`.
11. `mv "$H/sec/main.env" "$QA/e.bak"; init main; mv "$QA/e.bak" "$H/sec/main.env"`
    → `exit=2`; `ENV_FILE_MISSING`, `env_file`, путь.
12. `cp "$H/sec/main.env" "$QA/e.bak"; echo "RESTIC_PASSWORD=$ENVV" >> "$H/sec/main.env"; init main; cp "$QA/e.bak" "$H/sec/main.env"`
    → `exit=2`; `ENV_FILE_INVALID`, путь и номер строки; `grep -cF "$ENVV" "$QA/err"` → `0`.
13. Команда не ждёт ввода:
    `sleep 600 | "$AG" repo init --config "$H/agent.yaml" backup; echo "exit=$?"`
    → завершается сразу, `exit=2`.

## Часть 2. restic не найден, устарел, непригоден

Снова `repo` → `no repo` после каждого шага.

```bash
fake() { printf '#!/bin/sh\necho "restic %s compiled with go1.22.1 on linux/amd64"\n' "$1" > "$QA/restic-$1"; chmod 0755 "$QA/restic-$1"; echo "$QA/restic-$1"; }
```

14. `mkcfg "$QA/nope-restic"; init main`
    → `exit=1`; `RESTIC_NOT_FOUND`, `restic.path`, `$QA/nope-restic`,
    минимальная версия `$RMIN`.
15. `mkcfg ""; init main` (в `agent/bin/` нет `restic`)
    → `exit=1`; `RESTIC_NOT_FOUND`, путь `$PWD/agent/bin/restic`, сказано,
    что `restic.path` не задан и его можно задать.
16. Для каждой версии `0.18.1`, `0.9.6`: `mkcfg "$(fake <версия>)"; init main`
    → `exit=1`; `RESTIC_TOO_OLD`, найденная версия и `$RMIN`.
17. `mkcfg "$QA"; init main` (каталог); `cp "$RB" "$QA/noexec"; chmod 0644 "$QA/noexec"; mkcfg "$QA/noexec"; init main`;
    `printf '#!/bin/sh\necho hello\n' > "$QA/hello"; chmod 0755 "$QA/hello"; mkcfg "$QA/hello"; init main`
    → каждый раз `exit=1`; `RESTIC_UNUSABLE` и путь.
18. **(В1)** `mkcfg "$QA/nope-restic"; mv "$H/sec/main.pass" "$QA/p.bak"; init --generate-password main`
    → `exit=1`, `RESTIC_NOT_FOUND`; `ls "$H/sec/main.pass"` → нет файла.
    Вернуть: `mv "$QA/p.bak" "$H/sec/main.pass"; mkcfg "$RB"`.

## Часть 3. Успех и повтор

19. `init main`
    → `exit=0`; stdout содержит `main`, `local`, repository_id из 64
    шестнадцатеричных символов (далее `X`), путь `$H/sec/main.pass`, фразы
    `only on this host` и `unrecoverable`, совет перезапустить службу
    `sard-agent`, чтобы сервер получил repository_id.
20. `RESTIC_PASSWORD_FILE="$H/sec/main.pass" "$RB" -r "$H/repo" cat config | jq -r .id`
    → `X`.
21. `snap | diff - "$QA/s0"` → пусто (конфиг и файлы паролей не изменились).
22. **(В3)** `repo > "$QA/r1"; init main`
    → `exit=4`; `REPOSITORY_EXISTS` и `X`; `repo | diff - "$QA/r1"` → пусто.
23. **(В4)** `cp "$H/sec/main.pass" "$QA/p.bak"; printf 'other\n' > "$H/sec/main.pass"; init main; cp "$QA/p.bak" "$H/sec/main.pass"`
    → `exit=2`; `WRONG_PASSWORD`; сказано, что по адресу уже есть репозиторий,
    а файл пароля его не открывает; `repo | diff - "$QA/r1"` → пусто.
24. Вывод в файл, не в терминал: `rm -rf "$H/repo"; "$AG" repo init --config "$H/agent.yaml" main > "$QA/o.txt"; grep -c 'unrecoverable' "$QA/o.txt"`
    → `1` или больше.
25. Окружение оператора не влияет: `rm -rf "$H/repo"; RESTIC_REPOSITORY="$QA/elsewhere" RESTIC_PASSWORD=zzz init main`
    → `exit=0`; `ls "$QA/elsewhere"` → нет такого каталога; шаг 20 даёт id из
    вывода.
26. Флаги после имени: `rm -rf "$H/repo"; ri main --config "$H/agent.yaml"` → `exit=0`.
27. Файл пароля из одного перевода строки:
    `rm -rf "$H/repo"; cp "$H/sec/main.pass" "$QA/p.bak"; printf '\n' > "$H/sec/main.pass"; init main; cp "$QA/p.bak" "$H/sec/main.pass"`
    → код не `0`; `repo` → `no repo` или в каталоге нет файла `config`.

## Часть 4. Генерация пароля (В1)

28. `rm -rf "$H/repo"; mv "$H/sec/main.pass" "$QA/p.bak"; (umask 000; init --generate-password main)`
    → `exit=0`; stdout говорит, что файл пароля создан этой командой;
    `stat -c '%a %U' "$H/sec/main.pass"` → `600 $(id -un)`;
    `grep -cE '^[A-Za-z0-9_-]{43}$' "$H/sec/main.pass"` → `1`, в файле одна строка;
    `grep -cF "$(cat "$H/sec/main.pass")" "$QA/all"` → `0`.
29. `cp "$H/sec/main.pass" "$QA/gen1"; rm -rf "$H/repo"; init --generate-password main`
    → `exit=0`; stdout говорит, что использован существующий файл;
    `cmp "$H/sec/main.pass" "$QA/gen1"` → совпадают.
30. `chmod 0644 "$H/sec/main.pass"; init --generate-password main; chmod 0600 "$H/sec/main.pass"`
    → `exit=2` (A1); файл не изменился.
31. Каталог недоступен на запись: `rm "$H/sec/main.pass"; chmod 0500 "$H/sec"; init --generate-password main; chmod 0700 "$H/sec"`
    → `exit=7`; `PASSWORD_FILE_WRITE` и каталог; файла нет.
32. Неудача после генерации: `rm -rf "$H/repo" "$H/sec/main.pass"; sed -i "s|url: $H/repo|url: rest:http://127.0.0.1:9/main|" "$H/agent.yaml"; init --generate-password main`
    → `exit=6`; `BACKEND_UNAVAILABLE`; `stat -c %a "$H/sec/main.pass"` → `600`;
    сообщение говорит, что созданный файл пароля сохранён.
    Вернуть: `mkcfg "$RB"; mv "$QA/p.bak" "$H/sec/main.pass"`.

## Часть 5. Недоступный и отказывающий бэкенд

33. `init offsite` (REST на закрытом порту 9, пароль `$URLM` в адресе)
    → `exit=6`; `BACKEND_UNAVAILABLE`, тип `rest`, причина от restic
    (`connection refused`), совет повторить; `grep -cF "$URLM" "$QA/all"` → `0`.
    Записать время выполнения: если больше минуты — отметить для В7.
34. **(В5)** Неверные учётные данные S3 (MinIO):
    `docker run -d --rm --name qa-minio -p 19000:9000 -e MINIO_ROOT_USER=qaadmin -e MINIO_ROOT_PASSWORD=qaadmin-secret minio/minio server /data`;
    дождаться порта; в конфиге у `main`: `url: s3:http://127.0.0.1:19000/qa-bucket`,
    в `main.env`: `AWS_ACCESS_KEY_ID=wrong` и `AWS_SECRET_ACCESS_KEY=$ENVV`;
    `init main`
    → `exit=1`; `BACKEND_REFUSED`, тип `s3`, причина от restic;
    `grep -cF "$ENVV" "$QA/all"` → `0`.
35. Верные учётные данные (`qaadmin` / `qaadmin-secret` в `main.env`): `init main`
    → `exit=0`, тип `s3`. `docker stop qa-minio`; `init main`
    → `exit=6`, `BACKEND_UNAVAILABLE`. Вернуть конфиг и `main.env`.
36. Прерывание: `mkcfg "$RB"; rm -rf "$H/repo"; sed -i "s|url: $H/repo|url: rest:http://10.255.255.1:8000/main|" "$H/agent.yaml"`
    (адрес, который не отвечает); `"$AG" repo init --config "$H/agent.yaml" main & P=$!; sleep 3; kill -INT $P; wait $P; echo "exit=$?"`
    → `exit=6`; `INTERRUPTED`; сказано, что репозиторий мог быть создан
    частично и команду нужно повторить; `pgrep -f "$RB"` → пусто.
    Вернуть: `mkcfg "$RB"`.

## Часть 6. Старт агента

Сервер не нужен для отказов: агент должен завершиться до сети.
`start` — `timeout 10 "$AG" --config "$H/agent.yaml" >"$QA/sout" 2>"$QA/serr"; echo "exit=$?"; cat "$QA/sout" "$QA/serr"`.

37. `mkcfg "$QA/nope-restic"; start`
    → сразу `exit=1`; stderr начинается с `sard-agent: `, содержит
    `RESTIC_NOT_FOUND`, `restic.path`, путь, `$RMIN`; в stdout нет `connecting to`.
38. `mkcfg ""; start` → `exit=1`; путь `$PWD/agent/bin/restic`, «restic.path не задан».
39. `mkcfg "$(fake 0.18.1)"; start` → `exit=1`; `RESTIC_TOO_OLD`, `0.18.1`, `$RMIN`.
40. `mkcfg "$QA/hello"; start` → `exit=1`; `RESTIC_UNUSABLE`, путь.
41. Порядок: `mkcfg "$QA/nope-restic"; chmod 0644 "$H/sec/main.pass"; start; chmod 0600 "$H/sec/main.pass"`
    → `exit=1`; сообщение A1 о `password_file`; `RESTIC_NOT_FOUND` нет.
42. Для версий `0.19.0`, `0.20.0` (`mkcfg "$(fake <версия>)"`) и `mkcfg "$RB"`:
    выполнить части «Подготовка» и 2 `docs/qa/agent-enroll.md` для этого
    `$H` (сервер поднят, агент зарегистрирован), `start`
    → в stdout `connecting to`, `exit=124`, `RESTIC_TOO_OLD` нет.
43. enroll без restic: `mkcfg "$QA/nope-restic"`, новый токен, `rm "$H"/tls/*`;
    `"$AG" enroll --config "$H/agent.yaml" --token <токен>` (адрес сервера в
    конфиге) → `exit=0`.

## Часть 7. Сервер получает repository_id (хост с пакетом deb/rpm)

44. На хосте с установленным пакетом, агент зарегистрирован и служба запущена;
    в `/etc/sard/agent.yaml` репозиторий `main` с локальным `url`, каталог для
    файла пароля создан для пользователя службы:
    `sudo install -d -o sard-agent -g sard-agent -m 0700 /etc/sard/restic`;
    `password_file: /etc/sard/restic/main.pass`. Перезапустить службу.
    `$PSQL "select name, backend, repository_id from agent_repositories where agent_id = '<id>'"`
    → `main | local | ` (id пуст).
45. **(В1)** `sudo -u sard-agent sard-agent repo init --generate-password main`
    → `exit=0`; владелец файла пароля — `sard-agent`, права `600`; вывод
    называет repository_id `X`.
    Без В1: создать файл вручную (`sudo -u sard-agent sh -c 'umask 077; head -c 32 /dev/urandom | base64 > /etc/sard/restic/main.pass'`),
    затем `sudo -u sard-agent sard-agent repo init main`.
46. До перезапуска: запрос шага 44 → id по-прежнему пуст (конфиг не
    перечитывается; при обрыве связи id может прийти раньше — это не дефект).
47. `sudo systemctl restart sard-agent`; запрос шага 44 → `main | local | X`.
48. От root без `sudo -u`: `sudo sard-agent repo init --generate-password other`
    (второй репозиторий в конфиге) → файл пароля принадлежит `root`;
    `sudo systemctl restart sard-agent` → служба не стартует, в журнале
    сообщение A1 о владельце файла. Это ожидаемо и описано в
    `docs/operations/repo-init.md`.
49. `docs/operations/repo-init.md` существует и говорит: выполнять команду от
    имени пользователя службы (`sudo -u sard-agent`); сохранить копию файла
    пароля вне хоста; перезапустить службу после инициализации; таблица кодов
    выхода совпадает со справкой.

## Часть 8. Секреты не утекают

50. `grep -cF "$PASS" "$QA/all"` → `0`; `grep -cF "$ENVV" "$QA/all"` → `0`;
    `grep -cF "$URLM" "$QA/all"` → `0`; то же для `$QA/serr`.

Вручную не воспроизводятся и проверяются тестами `@local` с теми же
названиями: гонка «репозиторий создан между проверкой и init», непредусмотренный
вывод restic, restic, печатающий значения из `env_file`, одновременные
запуски (В8), агент через символьную ссылку с restic в каталоге настоящего файла.
