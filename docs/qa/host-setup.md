# QA: настройка хоста агента командами `sard-agent` (A8a, A8b)

Сценарии: `docs/specs/agent/host-setup.feature`. Срез A8a утверждён владельцем
2026-10-07; ответы на О1–О5, Н1–Н13 внесены в спецификацию. Срез A8b (части
8–11 ниже) утверждён владельцем 2026-10-08 (Н14–Н25); шаги, которые проверяют
решение по ответу, помечены «(Н<номер>)». Шаблоны адресов провайдеров A8b-3
(часть 11) подтверждены владельцем 2026-10-09. Поправки П12–П17 (2026-10-09)
учтены в шагах 51, 54а, 54б; поправки A8b-2 П18–П28 — в шагах 62а–62в,
66, 66а, 70. A8b-1 реализован; его e2e на стенде ещё не
прогнаны. Классы и номера кодов выхода —
A2b (ADR 0025); A8b добавляет к `repo add` код `5` (доверие: ключ хоста
SFTP).

Выполнима после реализации A8a. Ожидаемый результат — после «→». Любое
расхождение — дефект.

Коды: `0` успех (изменение применено или не требовалось), `1` ошибка агента
(restic, `BACKEND_REFUSED`, `SERVICE_RESTART_FAILED`), `2` использование, `4`
`REPOSITORY_CONFLICT`, `6` временная (`TIMEOUT`, `INTERRUPTED`,
`CONFIG_LOCKED`, `INIT_IN_PROGRESS`, `BACKEND_UNAVAILABLE`), `7` запись
(`CONFIG_WRITE`, `LOCK_WRITE`).

## Стенды

Прогнать целиком на двух ВМ:

- **Debian 12** (или Ubuntu 24.04), без SELinux;
- **Rocky 9** (или Oracle Linux 9), `getenforce` → `Enforcing`.

На каждой: пакет агента из `make package` установлен (`apt install ./dist/…deb`
или `dnf install ./dist/…rpm`) на чистую ВМ, `sard-server` доступен
(`make up` на другой машине или `docs/operator/02-install.md`); агент
регистрируется в части 0 этой процедуры. В `/etc/sard/agent.yaml` есть
`server.address` и `tls.*` в `/etc/sard/tls`, нет ключей `repositories` и
`secrets` (ответ О1). Оператор — обычный пользователь с `sudo`.

```bash
AG=sard-agent
OUT=$(mktemp -d)                         # вне /etc и /srv
run()  { "$@" >"$OUT/o" 2>"$OUT/e"; echo "exit=$?"; cat "$OUT/o" "$OUT/e"; cat "$OUT/o" "$OUT/e" >> "$OUT/all"; }
S=$(sudo sed -n 's/^ *state_dir: *//p' /etc/sard/agent.yaml); S=${S:-/var/lib/sard-agent/executor}
SECRET=SECRET-MARKER-$RANDOM$RANDOM
snap() { sudo find /etc/sard /etc/systemd/system/sard-agent.service.d -printf '%u %g %m %p\n' 2>/dev/null | sort; }
audit() { sudo journalctl -t sard-agent --since "$1" --no-pager -o cat; }
pid() { systemctl show -p MainPID --value sard-agent; }
```

## Часть 0. Установка и регистрация под sudo (Р25)

0а. Вывод первой установки пакета (`apt install …` / `dnf install …`,
    сохранить в `$OUT/install.txt`) → содержит
    `sudo sard-agent enroll --server`; `grep -c 'sudo -u' "$OUT/install.txt"` → `0`.
0б. Взять токен в консоли. Посторонний пользователь:
    `sard-agent enroll --server <адрес> --token <строка>; echo "exit=$?"`
    (без sudo) → `exit=2`; `PRIVILEGES_REQUIRED`; подсказка
    `sudo sard-agent enroll`, без `sudo -u`; в консоли токен активен, агентов
    не прибавилось; строка токена в выводе не встречается.
0в. Каталог `tls.*` (Р25а, поправка В12). Перед шагом
    `sudo mv /etc/sard/tls /etc/sard/tls.bak`.
    1) Промежуточного каталога нет: `sudo sed 's|/etc/sard/tls/|/etc/sard/qa-missing/tls/|' /etc/sard/agent.yaml | sudo tee /etc/sard/qa.yaml >/dev/null`;
       `sudo sard-agent enroll --config /etc/sard/qa.yaml --server <адрес> --token <строка>; echo "exit=$?"`
       → `exit=7`; сообщение называет `tls.key_file` и `/etc/sard/qa-missing`;
       `ls -d /etc/sard/qa-missing` → нет каталога; токен в консоли активен,
       агентов не прибавилось. `sudo rm /etc/sard/qa.yaml`.
    2) Неудачная регистрация не оставляет каталога: создать в консоли токен и
       отозвать его;
       `sudo sard-agent enroll --server <адрес> --token <отозванный>; echo "exit=$?"`
       → `exit=3`; `ls -d /etc/sard/tls` → нет каталога.
    3) От пользователя службы каталог не создаётся (В12):
       `sudo -u sard-agent sard-agent enroll --server <адрес> --token <строка>; echo "exit=$?"`
       → `exit=7`, сообщение называет `/etc/sard/tls`; каталога нет; токен
       активен.
    Каталог `/etc/sard/tls.bak` не возвращать: шаг 0г создаёт `/etc/sard/tls`
    сам. В конце части `sudo rm -r /etc/sard/tls.bak`.
0г. `T=$(date '+%F %T'); sudo sard-agent enroll --server <адрес> --token <строка>; echo "exit=$?"`
    (каталога `/etc/sard/tls` нет после шага 0в)
    → `exit=0`; `sudo stat -c '%U %G %a' /etc/sard/tls` → `sard-agent sard-agent 700`;
    `sudo stat -c '%U %G %a %n' /etc/sard/tls/*` →
    `sard-agent sard-agent 600 …agent.key`, `sard-agent sard-agent 644 …agent.pem`,
    `sard-agent sard-agent 644 …ca.pem`; других файлов в `/etc/sard/tls` нет;
    `audit "$T"` → пусто (enroll строк аудита не пишет).
0д. `sudo systemctl enable --now sard-agent; sleep 3; systemctl is-active sard-agent`
    → `active`; агент в консоли в сети.
0е. `--force` под sudo: `sudo chown root:root /etc/sard/tls/agent.key; sudo chmod 0644 /etc/sard/tls/agent.key`;
    новый токен; `sudo sard-agent enroll --force --server <адрес> --token <строка>`
    → `exit=0`; `sudo stat -c '%U %a' /etc/sard/tls/agent.key` → `sard-agent 600`;
    `sudo systemctl restart sard-agent` → `active`. (Старый агент в консоли
    офлайн — отозвать.)
0ж. `sard-agent enroll --help` → содержит `sudo sard-agent enroll`, говорит,
    что файлы `tls.*` получают владельцем пользователя службы; `sudo -u` нет.
0з. `docs/operations/agent-enroll.md` и `docs/operations/agent-install.md` не
    содержат `sudo -u` и `install -d` для `/etc/sard/tls` (ни для пакета, ни
    для tar.gz); регистрация — `sudo sard-agent enroll`; сказано, что под sudo
    команда создаёт отсутствующий последний каталог `tls.*`, но не
    промежуточные.

## Часть 1. Права запуска

1. `sudo -u sard-agent $AG secret set db --stdin </dev/null; echo "exit=$?"`
   → `exit=2`; `PRIVILEGES_REQUIRED`; сообщение содержит
   `sudo sard-agent secret set db --stdin`.
2. `$AG secret list; echo "exit=$?"` (обычный пользователь, без sudo)
   → `exit=2`; `PRIVILEGES_REQUIRED`, `sudo`.
3. `sudo -u sard-agent $AG secret list; echo "exit=$?"` → `exit=0`.
4. `snap > "$OUT/s0"`; повторить шаги 1–3; `snap | diff - "$OUT/s0"` → пусто.
4а. Посторонний с отсутствующим конфигом (поправка П1):
    `$AG secret list --config /nonexistent.yaml; echo "exit=$?"` → `exit=2`,
    `PRIVILEGES_REQUIRED`, ошибки конфига нет.

## Часть 2. Секреты

5. Без терминала и без флага: `T=$(date '+%F %T'); sleep 600 | sudo $AG secret set db; echo "exit=$?"`
   → завершается сразу, `exit=2`; `SECRET_SOURCE_MISSING`, `--stdin`,
   `--from-file`; `snap | diff - "$OUT/s0"` → пусто.
6. `run sudo $AG secret set db "$SECRET"` и `run sudo $AG secret set db --value "$SECRET"`
   → каждый раз `exit=2`; `grep -cF "$SECRET" "$OUT/all"` → `0`.
7. `P0=$(pid); T=$(date '+%F %T'); printf '%s' "$SECRET" | sudo $AG secret set db --stdin; echo "exit=$?"`
   → `exit=0`; stdout называет `db`, `/etc/sard/secrets/db` и «restarted»;
   `pid` ≠ `$P0`;
   `sudo stat -c '%U %a' /etc/sard/secrets/db` → `sard-agent 600`;
   `sudo stat -c '%U %G %a' /etc/sard/agent.d /etc/sard/agent.d/secret-db.yaml`
   → `root sard-agent 750` и `root sard-agent 640`;
   `sudo cmp <(printf '%s' "$SECRET") /etc/sard/secrets/db` → совпадают;
   `audit "$T"` → одна строка с `secret db`, `added`, вашим именем и uid, без
   `$SECRET`; `sudo journalctl -t sard-agent --since "$T" -o json | jq -r '[.SYSLOG_FACILITY, .PRIORITY] | @tsv'`
   → `10	5` (authpriv, notice; поправка П8); основной конфиг не изменён (`sudo sha256sum /etc/sard/agent.yaml`
   до и после).
8. С терминала: `sudo $AG secret set db2` → дважды запрос без эха (символы не
   видны); ввести разные значения → `exit=2`, `SECRET_MISMATCH`, файла
   `/etc/sard/secrets/db2` нет. Повторить с одинаковыми → `exit=0`;
   `sudo wc -c /etc/sard/secrets/db2` → длина введённого, без перевода строки.
9. Из файла: `printf '%s\n' "$SECRET-f" > "$OUT/f"; chmod 0644 "$OUT/f"; sudo $AG secret set db3 --from-file "$OUT/f"`
   → `exit=0`; `sudo cmp "$OUT/f" /etc/sard/secrets/db3` → совпадают;
   `stat -c '%U %a' "$OUT/f"` → прежние.
10. Повтор: `P0=$(pid); T=$(date '+%F %T'); printf '%s' "$SECRET" | sudo $AG secret set db --stdin`
    → `exit=0`, `unchanged`; `pid` = `$P0`; `audit "$T"` → пусто.
11. Новое значение: `printf '%s' "$SECRET-2" | sudo $AG secret set db --stdin`
    → `exit=0`, «restarted»; `audit` → строка `updated`.
12. Пустое и большое: `sudo $AG secret set e --stdin </dev/null` → `exit=2`,
    `SECRET_EMPTY`; `head -c 65537 /dev/zero | tr '\0' a | sudo $AG secret set big --stdin`
    → `exit=2`, `SECRET_TOO_LARGE`; то же с 65536 байтами → `exit=0`
    (затем `sudo $AG secret remove big`).
13. Имена: `printf x | sudo $AG secret set ../x --stdin`, `… set db.pass …`,
    `printf x | sudo $AG secret set --stdin -- -db` → `exit=2`, `NAME_INVALID`.
    `printf x | sudo $AG secret set -db --stdin` → `exit=2` без `NAME_INVALID`
    (разобрано как флаг, поправка П3).
14. `sudo $AG secret list` → `exit=0`; заголовок `NAME DEFINED_IN`, строки
    `db`, `db2`, `db3` с `/etc/sard/agent.d/secret-….yaml`; значений нет.
    `sudo $AG secret list --json | jq -r '.secrets[].name'` → `db db2 db3`.
15. Конфликт с основным конфигом: в `/etc/sard/agent.yaml` добавить
    `secrets: {pg: /etc/sard/secrets/pg}` (`sudoedit`, это часть QA, не
    процедура оператора), перезапустить службу; `printf x | sudo $AG secret set pg --stdin`
    → `exit=2`, `DEFINED_IN_CONFIG`, `/etc/sard/agent.yaml`.
    Затем во фрагмент: `sudo cp /etc/sard/agent.d/secret-db.yaml /etc/sard/agent.d/secret-dup.yaml`;
    `sudo $AG secret list` → `exit=2`, `DUPLICATE_NAME`, оба файла;
    `sudo systemctl restart sard-agent; sleep 2; systemctl is-active sard-agent`
    → не `active`, `journalctl -u sard-agent -n 5` называет `DUPLICATE_NAME` и
    оба файла. Убрать `secret-dup.yaml` и `pg` из основного конфига,
    перезапустить → `active`.
16. Удаление: `sudo $AG secret remove db3` → `exit=0`, «restarted»; файлов
    `/etc/sard/agent.d/secret-db3.yaml` и `/etc/sard/secrets/db3` нет.
    Повтор → `exit=0`, `nothing to remove`, служба не перезапущена.
17. Секрет в консоли: карточка агента в консоли перечисляет секреты `db`,
    `db2` (имена из Register), значений нет.

## Часть 3. Применение изменений

18. `--no-restart`: `P0=$(pid); printf y | sudo $AG secret set n1 --stdin --no-restart`
    → `exit=0`; stdout содержит `sudo systemctl restart sard-agent`; `pid` = `$P0`.
19. Идущий шаг: создать источник `files` на большом каталоге
    (`sudo mkdir /srv/big && sudo head -c 4G /dev/urandom | sudo tee /srv/big/f >/dev/null`)
    с репозиторием из части 4 и запустить бэкап из консоли; пока шаг
    `running`: `sudo ls "$S/journal"` → есть `*.json`;
    `P0=$(pid); printf y | sudo $AG secret set n2 --stdin`
    → `exit=0`; stdout говорит, что шаги выполняются (число), служба не
    перезапущена, и печатает `sudo systemctl restart sard-agent`; `pid` = `$P0`;
    шаг в консоли завершается `succeeded`.
20. Остановленная служба: `sudo systemctl stop sard-agent; printf y | sudo $AG secret set n3 --stdin`
    → `exit=0`; stdout говорит, что изменение вступит при старте;
    `systemctl is-active sard-agent` → `inactive`. `sudo systemctl start sard-agent`.
21. Одновременно: `(printf y; sleep 5) | sudo $AG secret set n4 --stdin & sleep 1; printf y | sudo $AG secret set n5 --stdin; echo "exit=$?"; wait`
    → вторая `exit=6`, `CONFIG_LOCKED`; первая `exit=0`.
22. Убрать `n1`…`n5`: `sudo $AG secret remove n1` и т. д.

## Часть 4. Локальный репозиторий

23. `T=$(date '+%F %T'); run sudo $AG repo add main /srv/sard/main`
    → `exit=0`; stdout: `main`, `local`, `/srv/sard/main`, repository_id из 64
    шестнадцатеричных символов (далее `X`), «created», `only on this host`,
    `unrecoverable`, `sudo sard-agent repo password main --reveal`,
    предупреждение о бэкапе на этом же хосте, «restarted».
    `sudo stat -c '%U %a' /srv/sard/main /etc/sard/secrets/restic-main.pass` →
    `sard-agent 700`, `sard-agent 600`; `stat -c '%U %a' /srv/sard` → `root 755`;
    `sudo cat /etc/sard/agent.d/repo-main.yaml` → `name: main`,
    `url: /srv/sard/main`, `password_file: /etc/sard/secrets/restic-main.pass`;
    `cat /etc/systemd/system/sard-agent.service.d/sard-repo-main.conf` →
    `ReadWritePaths=/srv/sard/main`; `sudo find /var/cache/sard/restic -not -user sard-agent` → пусто;
    `audit "$T"` → `repository main added`.
24. Сервер получил id: `$PSQL "select name, backend, repository_id from agent_repositories where agent_id = '<id>'"`
    → `main | local | X`.
25. Бэкап работает: источник `files` на `/etc/hostname` с репозиторием `main`,
    запуск из консоли → `succeeded` (служба не висит на блокировке — проверка
    drop-in).
26. Повтор: `sleep 600 | sudo $AG repo add main /srv/sard/main; echo "exit=$?"`
    → сразу `exit=0`, `unchanged`, `X`, `only on this host`; служба не
    перезапущена.
27. Конфликт: `sudo $AG repo add main /srv/sard/other` → `exit=4`,
    `REPOSITORY_CONFLICT`, `/srv/sard/main`, совет `repo remove main`.
28. Заглушки (после A8b — Р27: s3 и sftp больше не заглушка, их проверяют
    части 8 и 9): `run sudo $AG repo add restx rest:https://u:URL-MARKER@h/x`,
    `run sudo $AG repo add b2x b2:bucket:x`
    → каждый раз `exit=2`, `BACKEND_NOT_SUPPORTED`, вид адреса и
    `a local path, s3: or sftp:`; `grep -c URL-MARKER "$OUT/all"` → `0`.
29. Путь: `sudo $AG repo add r1 relative` → `exit=2`, `LOCAL_PATH_INVALID`;
    `sudo $AG repo add r2 /etc/hostname` → `exit=2`, `LOCAL_PATH_INVALID`.
29а. (П10.) Символьная ссылка:
    `sudo ln -s /etc /srv/sard/link; snap > "$OUT/s1"; sudo find /etc -printf '%u %p\n' | sort > "$OUT/etc0"`;
    `sudo $AG repo add r4 /srv/sard/link; echo "exit=$?"`
    → `exit=2`; `LOCAL_PATH_INVALID`; сообщение содержит
    `/srv/sard/link is a symbolic link; give the directory it points to`;
    `snap | diff - "$OUT/s1"` → пусто (нет `repo-r4.yaml`, нет `sard-repo-r4.conf`);
    `sudo find /etc -printf '%u %p\n' | sort | diff - "$OUT/etc0"` → пусто
    (владельцы в `/etc` не менялись). `sudo rm /srv/sard/link`.
29б. (П11.) Ссылка в родительском компоненте:
    `sudo rm -rf /var/lib/sard-agent/repos; sudo ln -s /var /var/lib/sard-agent/repos`;
    `snap > "$OUT/s2"; sudo find /var -xdev -printf '%u %g %p\n' 2>/dev/null | sort > "$OUT/var0"`;
    `sudo $AG repo add r5 /var/lib/sard-agent/repos/backups; echo "exit=$?"`
    → `exit=2`; `LOCAL_PATH_INVALID`; сообщение содержит
    `/var/lib/sard-agent/repos is a symbolic link; give the resolved path`;
    `sudo find /var -xdev -printf '%u %g %p\n' 2>/dev/null | sort | diff - "$OUT/var0"`
    → пусто (никаких смен владельца и новых каталогов в `/var`);
    `snap | diff - "$OUT/s2"` → пусто. `sudo rm /var/lib/sard-agent/repos`.
29в. (П11.) Адрес через системную ссылку: `ls -ld /var/run` → ссылка на `/run`;
    `sudo $AG repo add r6 /var/run/sard-qa; echo "exit=$?"` → `exit=2`,
    `LOCAL_PATH_INVALID`, называет `/var/run`; `ls -d /run/sard-qa` → нет.
30. `sudo $AG repo show main` → `exit=0`; `local`, `/srv/sard/main`,
    `initialized`, `X`, `/etc/sard/secrets/restic-main.pass`,
    `/etc/sard/agent.d/repo-main.yaml`. `sudo $AG repo show main --json | jq -r .repository_id` → `X`.
    `sudo -u sard-agent $AG repo list --json | jq -r '.repositories[].name'` → `main`.
31. `T=$(date '+%F %T'); sudo $AG repo password main --reveal > "$OUT/p"`
    → `exit=0`; stderr — предупреждение; `sudo cmp "$OUT/p" /etc/sard/secrets/restic-main.pass`
    → совпадают; `audit "$T"` → `repository main password revealed`.
    `sudo $AG repo password main` → `exit=2`, `REVEAL_REQUIRED`, stdout пуст.
32. Подключение существующего репозитория на другом имени — имитация нового
    хоста: `sudo $AG repo remove main` → `exit=0`; stdout предупреждает об
    источниках на сервере, называет оставшийся `/etc/sard/secrets/restic-main.pass`;
    файлов `repo-main.yaml` и `sard-repo-main.conf` нет; `/srv/sard/main` на
    месте. Затем `sudo mv /etc/sard/secrets/restic-main.pass "$OUT/keep.pass"`
    (как будто пароль есть только в копии из шага 31) и
    `sleep 600 | sudo $AG repo add main /srv/sard/main` → сразу `exit=2`,
    `SECRET_SOURCE_MISSING`, `--password-stdin`, `--password-from-file`.
    `printf 'wrong\n' | sudo $AG repo add main /srv/sard/main --password-stdin`
    → `exit=2`, `WRONG_PASSWORD`; `sudo ls -A /etc/sard/secrets | grep -c restic-main` → `0`.
    `sudo $AG repo add main /srv/sard/main --password-from-file "$OUT/p"`
    → `exit=0`; «attached» (подключён существующий), `X`; init не выполнялся
    (`X` тот же).
33. Каталог другого владельца: `sudo $AG repo remove main; sudo chown -R root:root /srv/sard/main;`
    `sudo $AG repo add main /srv/sard/main` (оставшийся пароль подходит)
    → `exit=0`; `sudo find /srv/sard/main -not -user sard-agent` → пусто.
34. Отказ при создании: `sudo chattr +i /srv/sard; sudo $AG repo add r3 /srv/sard/r3; echo "exit=$?"; sudo chattr -i /srv/sard`
    → `exit=7`, `CONFIG_WRITE`, путь `/srv/sard/r3`; файла
    `/etc/sard/agent.d/repo-r3.yaml` нет; служба не перезапущена.
35. Временные файлы: во время шагов 7 и 23 в другом терминале
    `sudo inotifywait -m -r /tmp -e create` → ни одного события от `sard-agent`.

## Часть 5. SELinux (только Rocky/Oracle Linux)

36. `sudo ls -Z /etc/sard/tls /etc/sard/agent.d /etc/sard/secrets` → те же метки, что даёт
    `sudo restorecon -nv -R /etc/sard` (вывод `restorecon -nv` пуст — менять
    нечего).
37. `sudo systemctl restart sard-agent; sleep 3; systemctl is-active sard-agent`
    → `active`; `sudo ausearch -m avc -ts recent | grep -c sard` → `0`.
38. Шаг 25 на этой ВМ → `succeeded`.

## Часть 6. repo init под sudo (поправка A5b)

39. В `/etc/sard/agent.yaml` репозиторий `legacy` (`url: /srv/sard/legacy`,
    `password_file: /etc/sard/secrets/legacy.pass`), каталог создать
    `sudo $AG repo add` нельзя (он в основном конфиге) — создать
    `sudo install -d -o sard-agent -m 0700 /srv/sard/legacy` (часть QA).
    `sudo $AG repo init --generate-password legacy`
    → `exit=0`; `sudo stat -c '%U %a' /etc/sard/secrets/legacy.pass` →
    `sard-agent 600`; `sudo find /srv/sard/legacy /var/cache/sard/restic -not -user sard-agent` → пусто;
    после `sudo systemctl restart sard-agent` → `active`.

## Часть 7. Справка и утечки

40. Для `enroll`, `secret set`, `secret list`, `secret remove`, `repo add`, `repo show`,
    `repo remove`, `repo password`, `repo list`: `$AG <команда> --help` →
    `exit=0`; флаги из сценария «Справка команды…», сказано, нужен ли sudo,
    коды выхода.
41. `grep -cF "$SECRET" "$OUT/all"` → `0`; `grep -c URL-MARKER "$OUT/all"` → `0`;
    `sudo journalctl -t sard-agent --no-pager | grep -cF "$SECRET"` → `0`.

## Часть 8. Хранилище S3 (A8b-1)

Нужно S3-совместимое хранилище, доступное с обеих ВМ: Garage на третьей
машине (как стенд F2, `test/e2e/README.md`) или облачное хранилище. В нём:
бакет `$B`, ключ `$KID`/`$KSEC` с чтением, записью и удалением в `$B`, ключ
`$ROID`/`$ROSEC` только на чтение в `$B`, бакета `$NOB` нет, и ключ не может
создавать бакеты. Значения ключей — в файлах `$OUT/ksec`, `$OUT/rosec`
(`chmod 0600`), в командную строку не попадают.

```bash
S3=https://<адрес хранилища>; B=<бакет>; NOB=<имя бакета, которого нет>
KID=<id ключа>; ROID=<id ключа только на чтение>
SEC_MARK=$(cat "$OUT/ksec")              # только для grep утечек
```

42. Без идентификатора ключа: `run sudo $AG repo add s1 s3:$S3/$B/s1 --secret-key-from-file "$OUT/ksec"`
    → `exit=2`, сообщение называет `--access-key-id`.
43. Значение флагом: `run sudo $AG repo add s1 s3:$S3/$B/s1 --access-key-id $KID --secret-key "$SEC_MARK"`
    → `exit=2`; `grep -cF "$SEC_MARK" "$OUT/all"` → `0`.
44. Учётные данные в адресе: `run sudo $AG repo add s1 "s3:https://u:URL-MARKER@${S3#https://}/$B/s1" --access-key-id $KID --secret-key-from-file "$OUT/ksec"`
    → `exit=2`, `ADDRESS_INVALID`; `grep -c URL-MARKER "$OUT/all"` → `0`.
45. Подключение пустого пути: `T=$(date '+%F %T'); P0=$(pid); run sudo $AG repo add s1 s3:$S3/$B/s1 --access-key-id $KID --secret-key-stdin < "$OUT/ksec"`
    → `exit=0`; stdout: `s1`, `s3`, адрес, repository_id из 64 шестнадцатеричных
    символов (далее `Y`), «created», `only on this host`, `unrecoverable`,
    `/etc/sard/secrets/restic-s1.env`; предупреждения «на этом же хосте» нет;
    «restarted», `pid` ≠ `$P0`.
    `sudo stat -c '%U %G %a' /etc/sard/secrets/restic-s1.env /etc/sard/secrets/restic-s1.pass`
    → `sard-agent sard-agent 600` дважды;
    `sudo sed 's/=.*/=…/' /etc/sard/secrets/restic-s1.env` → ровно
    `AWS_ACCESS_KEY_ID=…`, `AWS_SECRET_ACCESS_KEY=…` (без региона);
    `sudo grep -c $'\r' /etc/sard/secrets/restic-s1.env` → `0`;
    `sudo cat /etc/sard/agent.d/repo-s1.yaml` → `url: s3:…/s1`,
    `password_file`, `env_file: /etc/sard/secrets/restic-s1.env`;
    `ls /etc/systemd/system/sard-agent.service.d/sard-repo-s1.conf` → нет файла;
    `audit "$T"` → одна строка `repository s1 added`.
46. Сервер и бэкап: в консоли агент перечисляет `s1` с backend `s3`; источник
    `files` на `/etc/hostname` с репозиторием `s1` → `succeeded`.
47. Повтор без секрета: `P0=$(pid); T=$(date '+%F %T'); sudo $AG repo add s1 s3:$S3/$B/s1 --access-key-id $KID`
    (в терминале) → сразу `exit=0`, `unchanged`, `Y`; ничего не спрошено;
    `pid` = `$P0`; `audit "$T"` → пусто.
48. Секрет с терминала: `sudo $AG repo add s2 s3:$S3/$B/s2 --access-key-id $KID`
    → дважды запрос без эха; ввести разные значения → `exit=2`,
    `SECRET_MISMATCH`, файлов `restic-s2.*` нет. Повторить, ввести `$KSEC`
    дважды → `exit=0`. Затем `sudo $AG repo remove s2` → `exit=0`;
    `/etc/sard/secrets/restic-s2.env` нет, `restic-s2.pass` на месте.
49. Неверный секрет: `printf 'wrong' | run sudo $AG repo add s3 s3:$S3/$B/s3 --access-key-id $KID --secret-key-stdin`
    → `exit=2`, `STORAGE_ACCESS_DENIED` (или `S3_KEY_REJECTED` у хранилищ,
    различающих неверную подпись); сообщение говорит проверить ключ и права
    на бакет `$B`; `sudo ls -A /etc/sard/secrets | grep -c 'restic-s3\.'` → `0`;
    `/etc/sard/agent.d/repo-s3.yaml` нет; меньше 5 с.
50. Ключ только на чтение к существующему репозиторию: `sudo $AG repo password s1 --reveal > "$OUT/s1.pass"`;
    `run sudo $AG repo add s1ro s3:$S3/$B/s1 --access-key-id $ROID --secret-key-from-file "$OUT/rosec" --password-from-file "$OUT/s1.pass"`
    → `exit=2`, `STORAGE_ACCESS_DENIED`, в сообщении строка restic о блокировке
    (`unable to create lock`), не только `exit code 1`.
51. Нет бакета: `run sudo $AG repo add s4 s3:$S3/$NOB/s4 --access-key-id $KID --secret-key-from-file "$OUT/ksec"`
    → `exit=2`, `BUCKET_NOT_FOUND`, имя `$NOB` (Н19); сообщение говорит, что
    файлы сохранены для повтора; `sudo stat -c '%U %a' /etc/sard/secrets/restic-s4.env /etc/sard/secrets/restic-s4.pass`
    → `sard-agent 600` дважды; `/etc/sard/agent.d/repo-s4.yaml` нет (П12).
    Убрать: `sudo rm /etc/sard/secrets/restic-s4.env /etc/sard/secrets/restic-s4.pass`.
52. Недоступное хранилище: `time run sudo $AG repo add s5 s3:https://192.0.2.1/$B/s5 --access-key-id $KID --secret-key-from-file "$OUT/ksec" --connect-timeout 10s`
    → `exit=6`, `BACKEND_UNAVAILABLE`, `did not answer within 10s`,
    `--connect-timeout`; `real` меньше 25 с; файлов `restic-s5.*` и
    `repo-s5.yaml` нет. Без флага (`--connect-timeout` по умолчанию) → то же
    меньше 45 с.
53. Существующий репозиторий на втором хосте (вторая ВМ):
    `sudo $AG repo add s1 s3:$S3/$B/s1 --access-key-id $KID --secret-key-from-file "$OUT/ksec"`
    без `--password-*`, ввод — не терминал (`</dev/null`) → сразу `exit=2`,
    `SECRET_SOURCE_MISSING`, `--password-stdin`, `--password-from-file`;
    с `--password-from-file` (копия `$OUT/s1.pass`) → `exit=0`, «attached», `Y`.
54. Смена ключа (Н17): создать в хранилище второй ключ `$KID2` с теми же
    правами; `sudo $AG repo add s1 s3:$S3/$B/s1 --access-key-id $KID2 --secret-key-from-file "$OUT/ksec2"`
    → `exit=0`, `credentials updated`, служба не перезапущена;
    `audit` → `repository s1 credentials updated`; следующий бэкап `s1` →
    `succeeded`.
54а. (П14–П17) `sudo $AG repo add s1 s3:$S3/$B/s1` → `exit=2`, называет
    `--access-key-id`. `sudo $AG repo add s1 s3:$S3/$B/s1 --access-key-id $KID2 --region <другой регион> </dev/null`
    → `exit=2`, `SECRET_SOURCE_MISSING`; env-файл не изменился
    (`sudo sha256sum` до и после). `sudo $AG repo add s1 s3:$S3/$B/s1 --access-key-id $KID --secret-key-from-file "$OUT/ksec" --password-from-file "$OUT/s1.pass"`
    → `exit=2`, называет `--password-from-file`. Ключ другого бакета
    `$KIDX` (секрет в `$OUT/ksecx`), где по пути `s1` пусто:
    `sudo $AG repo add s1 s3:$S3/$B/s1 --access-key-id $KIDX --secret-key-from-file "$OUT/ksecx"`
    → `exit=4`, `REPOSITORY_CONFLICT`; env-файл и пароль `s1` не изменились,
    в хранилище нового репозитория не появилось.
54б. (П13) В выводе отказов шагов 49–54а значения ключей заменены
    `[REDACTED]`; `***` встречается только в адресах с паролем.
55. Утечки: `grep -cF "$SEC_MARK" "$OUT/all"` → `0`;
    `sudo journalctl -t sard-agent --no-pager | grep -cF "$SEC_MARK"` → `0`;
    во время шага 45 в другом терминале `ps -eo args | grep -cF "$SEC_MARK"`
    (несколько раз) → `0`.

## Часть 9. Хранилище SFTP (A8b-2)

Нужен сервер SFTP (третья ВМ или контейнер `test/e2e/sftp`): пользователь
`backup` только с ключами, каталог `/srv/sftp/repo` на запись,
`/srv/sftp/ro` только на чтение. Администратор сервера сообщает отпечаток:
`ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub` → `FP` (вида `SHA256:…`).

```bash
SH=<адрес сервера SFTP>; FP=<отпечаток ed25519 от администратора>
HOME_SA=$(getent passwd sard-agent | cut -d: -f6)   # /var/lib/sard-agent
sshsnap() { sudo find "$HOME_SA/.ssh" -printf '%u %g %m %n %s %p\n' 2>/dev/null | sort; }
```

56. Без клиента OpenSSH (только на копии ВМ или в контейнере):
    `sudo apt-get remove openssh-client` (`dnf remove openssh-clients`), затем
    `run sudo $AG repo add f0 sftp:backup@$SH:/srv/sftp/repo/f0 --host-key-fingerprint $FP`
    → `exit=1`, `SSH_CLIENT_MISSING`, называет программу,
    `sudo apt-get install openssh-client`, `sudo dnf install openssh-clients`,
    `dpkg -i`; `sshsnap` до и после совпадают. Вернуть клиент.
57. Без подтверждения: `sshsnap > "$OUT/ss0"; sleep 600 | sudo $AG repo add f1 sftp:backup@$SH:/srv/sftp/repo/f1; echo "exit=$?"`
    → сразу `exit=2`, `HOST_KEY_UNCONFIRMED`, отпечатки ключей сервера (среди
    них `FP`), `--host-key-fingerprint`; `sshsnap | diff - "$OUT/ss0"` → пусто.
58. Чужой отпечаток: `run sudo $AG repo add f1 sftp:backup@$SH:/srv/sftp/repo/f1 --host-key-fingerprint SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA`
    → `exit=5`, `HOST_KEY_MISMATCH`, называет данный отпечаток и `FP`;
    `sshsnap | diff - "$OUT/ss0"` → пусто.
59. С терминала: `sudo $AG repo add f1 sftp:backup@$SH:/srv/sftp/repo/f1`
    → показаны `$SH`, порт `22`, `ssh-ed25519`, `FP` и совет
    `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub`; ответить `no` →
    `exit=5`, `HOST_KEY_REJECTED`; `sshsnap | diff - "$OUT/ss0"` → пусто.
60. Первый запуск (ключа у службы нет: `sudo ls $HOME_SA/.ssh/id_ed25519` → нет файла):
    `T=$(date '+%F %T'); run sudo $AG repo add f1 sftp:backup@$SH:/srv/sftp/repo/f1 --host-key-fingerprint $FP`
    → `exit=2`, `SSH_KEY_NOT_AUTHORIZED`, `Permission denied (publickey)`;
    stdout — строка `ssh-ed25519 … sard-agent@<имя хоста>` (сохранить в
    `$OUT/pub`) и «add it to authorized_keys of backup on $SH»;
    `sudo stat -c '%U %G %a %n' $HOME_SA/.ssh $HOME_SA/.ssh/*` →
    `sard-agent sard-agent 700 …/.ssh`, `… 600 …/config`,
    `… 600 …/id_ed25519`, `… 644 …/id_ed25519.pub`, `… 600 …/known_hosts`;
    `sudo cat $HOME_SA/.ssh/known_hosts` → одна строка `$SH ssh-ed25519 …`;
    `sudo head -1 $HOME_SA/.ssh/config` → `# sard-agent begin $SH`, блок
    содержит `StrictHostKeyChecking yes`, `BatchMode yes`,
    `ServerAliveInterval 15`; `/etc/sard/agent.d/repo-f1.yaml` и
    `/etc/sard/secrets/restic-f1.pass` нет; служба не перезапущена;
    `audit "$T"` → `ssh key of service user sard-agent created`,
    `ssh host key of $SH trusted ssh-ed25519 $FP`.
61. Администратор добавляет `$OUT/pub` в `authorized_keys` пользователя
    `backup`. Повтор: `T=$(date '+%F %T'); sudo $AG repo add f1 sftp:backup@$SH:/srv/sftp/repo/f1`
    (в терминале, без флага отпечатка) → ничего не спрошено; `exit=0`;
    «created», repository_id (`Z`), открытая часть ключа и `FP`, «restarted»;
    `sudo cat /etc/sard/agent.d/repo-f1.yaml` → без `env_file`;
    `audit "$T"` → только `repository f1 added`. Источник `files` с
    репозиторием `f1` из консоли → `succeeded`.
62. Повтор ещё раз: `sshsnap > "$OUT/ss1"; sudo $AG repo add f1 sftp:backup@$SH:/srv/sftp/repo/f1`
    → `exit=0`, `unchanged`, `Z`; `sshsnap | diff - "$OUT/ss1"` → пусто.
62а. (П24) `sudo $AG repo add f1 sftp:backup@$SH:/srv/sftp/repo/f1 --password-from-file /nonexistent`
    → `exit=2`, называет `--password-from-file` (файл не открывался: нет
    сообщения о `/nonexistent`). (П20) Администратор временно убирает ключ
    из `authorized_keys`; повтор шага 62 → `exit=2`,
    `SSH_KEY_NOT_AUTHORIZED`, не `unchanged`, за время меньше 40 с; вернуть ключ.
62б. (П26) Для каждого хоста `a,b`, `*`, `*.example.com`, `h?`, `!h`, `a b`,
    `h#c` и адресов `sftp:u@h@x:/x`, `sftp:u,v@h:/x`:
    `run sudo $AG repo add bad "sftp:backup@<хост>:/x" --host-key-fingerprint $FP`
    → `exit=2`, `ADDRESS_INVALID`; `sshsnap | diff - "$OUT/ss1"` → пусто.
62в. (П25) `sudo chmod 0775 $HOME_SA; run sudo $AG repo add f1 sftp:backup@$SH:/srv/sftp/repo/f1`
    → `exit=2`, `SSH_HOME_INVALID`; `sudo chmod 0755 $HOME_SA`.
    `sudo chmod 0620 $HOME_SA/.ssh/known_hosts;` повтор → `exit=2`,
    `SSH_FILE_REJECTED`, путь `known_hosts`; вернуть `0600`.
    (П23) `sudo stat -c '%a' $HOME_SA/.ssh/id_ed25519.pub` → `644`.
63. Каталог только на чтение: `run sudo $AG repo add f2 sftp:backup@$SH:/srv/sftp/ro/f2`
    → `exit=2`, `STORAGE_ACCESS_DENIED`, `permission denied`.
64. Сервер молча пропал: на сервере SFTP
    `sudo iptables -I INPUT -p tcp --dport 22 -j DROP`;
    `time run sudo $AG repo add f3 sftp:backup@$SH:/srv/sftp/repo/f3 --connect-timeout 10s`
    → `exit=6`, `BACKEND_UNAVAILABLE`; `real` меньше 25 с. Убрать правило.
65. Подлог в `~/.ssh` (Р38): `sshsnap > "$OUT/ss2"; sudo cp /etc/shadow "$OUT/shadow0"`;
    `sudo -u sard-agent mv $HOME_SA/.ssh/known_hosts $HOME_SA/.ssh/kh.bak;
    sudo -u sard-agent ln -s /etc/shadow $HOME_SA/.ssh/known_hosts`;
    `run sudo $AG repo add f4 sftp:backup@$SH:/srv/sftp/repo/f4 --host-key-fingerprint $FP`
    → `exit=2`, `SSH_FILE_REJECTED`, путь `…/.ssh/known_hosts`;
    `sudo cmp /etc/shadow "$OUT/shadow0"` → совпадают; `sudo stat -c '%U %a' /etc/shadow`
    → прежние; `grep -c root: "$OUT/all"` → `0`. То же с жёсткой ссылкой
    (`ln` без `-s`, если `fs.protected_hardlinks=0`, иначе пропустить и
    отметить). Вернуть `kh.bak`; `sshsnap | diff - "$OUT/ss2"` → пусто.
66. Сменившийся ключ хоста: на сервере пересоздать ключ хоста
    (`sudo ssh-keygen -A` после удаления ed25519), получить новый `FP2`;
    `run sudo $AG repo add f5 sftp:backup@$SH:/srv/sftp/repo/f5`
    → `exit=5`, `HOST_KEY_CHANGED`, путь и номер строки known_hosts,
    `--replace-host-key`. (Н18) `sudo $AG repo add f5 sftp:backup@$SH:/srv/sftp/repo/f5 --replace-host-key --host-key-fingerprint $FP2`
    → `exit=0`; в known_hosts одна строка `$SH` с новым ключом; `audit` →
    `ssh host key of $SH replaced`. (П22) Перед заменой добавить в
    known_hosts строку-шаблон `*.<домен $SH>` со старым ключом
    (`sudo -u sard-agent` — часть QA): `HOST_KEY_CHANGED` и итог замены
    называют её номер; после замены она на месте байт в байт.
66а. (П21) В выводе шага 60 есть примечание: `id_ed25519`, `known_hosts` и
    `config` записаны и остаются для повтора.
67. `sudo $AG repo remove f1` → `exit=0`; stdout говорит, что ключ
    `$HOME_SA/.ssh/id_ed25519` и ключ хоста `$SH` остаются; `sshsnap` не
    изменился.
68. Временные файлы: во время шагов 60–61 в другом терминале
    `sudo inotifywait -m -r /tmp -e create` → ни одного события от
    `sard-agent`; после шагов `sudo find $HOME_SA/.ssh /etc/sard/secrets -name '*.tmp-*'` → пусто.

## Часть 10. SELinux и документация (A8b)

69. (Rocky/Oracle Linux) `sudo restorecon -nv -R /etc/sard $HOME_SA` → вывод
    пуст; `sudo systemctl restart sard-agent` → `active`; бэкапы `s1` и `f1`
    из консоли → `succeeded`; `sudo ausearch -m avc -ts recent | grep -c sard` → `0`.
70. `grep -nE 'sudo -u|tee |install -o|ssh-keyscan|ssh-keygen -t' docs/operator/05a-storage.md`
    → пусто, кроме двух исключений (П18): команда получения отпечатка на
    сервере SFTP (`ssh-keygen -lf`) и процедура снятия блокировки
    `sudo -u sard-agent … /usr/libexec/sard/restic … unlock`. Ручных процедур
    для ключа SSH, `known_hosts` и `~/.ssh/config` нет; `grep -c /usr/lib/sard/ docs/operator/05a-storage.md`
    → `0`; `grep -c /usr/libexec/sard/restic docs/operator/05a-storage.md` → не `0`;
    в документе есть `sudo sard-agent repo add` для S3 и SFTP, флаги
    `--secret-key-stdin` и `--host-key-fingerprint`, таблица причин Р34.
71. `sard-agent repo add --help` → `exit=0`; называет `--access-key-id`,
    `--secret-key-stdin`, `--secret-key-from-file`, `--region`,
    `--host-key-fingerprint`, `--replace-host-key`, `--connect-timeout`,
    коды `0 1 2 4 5 6 7`; сказано, что ключ хоста не принимается без
    подтверждения.

## Часть 11. Предустановки провайдеров (A8b-3, Р50)

Шаблоны адресов подтверждены владельцем 2026-10-09. Нужны бакет и ключ у
AWS (`$AWSB`, регион `$AWSR`, ключ `$AWSKID`, секрет в `$OUT/awssec`) и, по
возможности, у Backblaze B2 (`$B2B`, `$B2R` вида `us-west-004`, `$B2KID`,
`$OUT/b2sec`).

72. `run sudo $AG repo add p1 s3:$AWSB/p1 --provider aws --access-key-id $AWSKID --secret-key-from-file "$OUT/awssec"`
    → `exit=2`, сообщение называет `--region`; файлов `restic-p1.*` нет.
73. `run sudo $AG repo add p1 s3:$AWSB/p1 --provider aws --region $AWSR --access-key-id $AWSKID --secret-key-from-file "$OUT/awssec"`
    → `exit=0`, «created»; `sudo grep url /etc/sard/agent.d/repo-p1.yaml` →
    `s3:https://s3.$AWSR.amazonaws.com/$AWSB/p1`;
    `sudo grep -c "AWS_DEFAULT_REGION=$AWSR" /etc/sard/secrets/restic-p1.env` → `1`;
    бэкап `p1` из консоли → `succeeded`.
74. То же для B2: `--provider b2 --region $B2R` → `exit=0`; url
    `s3:https://s3.$B2R.backblazeb2.com/$B2B/p2`; бэкап → `succeeded`.
75. `run sudo $AG repo add p3 s3:$AWSB/p3 --provider yandex --region ru-central1 --access-key-id $AWSKID --secret-key-from-file "$OUT/awssec"`
    → `exit=2`, сообщение перечисляет `aws` и `b2`.
76. `run sudo $AG repo add p4 s3:https://s3.example.com/$AWSB/p4 --provider aws --region $AWSR --access-key-id $AWSKID --secret-key-from-file "$OUT/awssec"`
    → `exit=2`, называет `--provider` и схему или хост в адресе.
77. `grep -cF "$(cat "$OUT/awssec")" "$OUT/all"` → `0`.

Вручную не воспроизводятся и проверяются тестами `@local` с теми же
названиями (A8b): подмена каталога `~/.ssh` ссылкой между открытием и
записью, FIFO в `~/.ssh`, `umask 000` для env-файла и файлов ssh, сбои
fchown, rename и fsync в `~/.ssh` и `secrets`, отказ `ssh-keygen`, SIGKILL
restic после SIGTERM по таймауту подключения, хешированные записи
`known_hosts`, IPv6 в адресе SFTP, регион провайдера, которого нет.

Вручную не воспроизводятся и проверяются тестами `@local` с теми же
названиями: сбои fsync, rename и смены владельца при записи, недоступный
syslog, нечитаемый журнал команд, неудачный `systemctl restart`, отсутствие
systemd, `umask 000`, отсутствующий пользователь службы, `service.user`,
сбой смены владельца файлов `tls.*` после регистрации и при `--force`,
порядок «смена владельца до переименования» у `enroll`.
