<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# QA: снимки и восстановление на хосте командами `sard-agent` (A8c)

Сценарии: `docs/specs/agent/repo-restore.feature` (Р52–Р71). Предустановки
российских S3 (Р51) проверяет `docs/qa/host-setup.md`, часть 12.
УТВЕРЖДЕНО 2026-10-12 (ответы Н26–Н38 — «да», как рекомендовано); шаги,
которые зависят от ответа, помечены «(Н<номер>)». Поправки Р72–Р79 —
ответы владельца 2026-10-13, переданы координатором; шаги помечены
«(Р<номер>)». В A8c-2 `--overwrite` недоступен (Р74): шаги с ним помечены
«(A8c-2b)» и до этого среза проверяют отказ (шаг 20а); новые проверки — часть 8.
Ответы владельца 2026-10-14 на вопросы A8c-3 (Р83–Р86) проверяет часть 9;
ответы на вопросы architect A8c-3 (Р87, Р88) — часть 10.

Выполнима после реализации A8c. Ожидаемый результат — после «→». Любое
расхождение — дефект.

Коды: `0` успех, `1` ошибка агента (`RESTORE_INCOMPLETE`, `SETPRIV_MISSING`,
`RESTIC_OUTPUT_UNEXPECTED`, restic), `2` использование (в том числе `ENV_FILE_INVALID`,
`BACKEND_NOT_SUPPORTED`), `6` временная (`BACKEND_UNAVAILABLE`, `TIMEOUT`,
`INTERRUPTED`, `TARGET_BUSY`), `7` запись (`TARGET_WRITE`).

## Стенды

- **ВМ-1 Debian 12** и **ВМ-1R Rocky 9** (`getenforce` → `Enforcing`) после
  `docs/qa/host-setup.md`, части 0–11: агент зарегистрирован, служба
  `active`, подключены `main` (локальный `/srv/sard/main`), `s1` (S3) и `f1`
  (SFTP).
- На ВМ-1 — PostgreSQL 16 с базой `app` (таблица `t`, 1000 строк, владелец
  роль `app_rw`) и базой `my db` (с пробелом); секрет `pg` для роли
  `backup` с `pg_read_all_data`, как `docs/qa/postgresql-plugin.md`.
- **ВМ-2** — чистая Debian 12 без регистрации: только пакет агента; сервер
  Sard с неё недоступен (шаг 40).

В консоли созданы источники и выполнены бэкапы (каждый — `succeeded`):

| источник | плагин | репозиторий | что |
|---|---|---|---|
| `qa-files` | files | `main` | `/srv/qa-data` (см. ниже), дважды |
| `qa-files-s3` | files | `s1` | `/srv/qa-data` |
| `qa-files-sftp` | files | `f1` | `/srv/qa-data` |
| `qa-pg` | postgresql | `main` | база `app`, `include_globals: true` |
| `qa-pg-noglob` | postgresql | `main` | база `app`, `include_globals: false` |
| `qa-pg-space` | postgresql | `main` | база `my db` |

```bash
sudo install -d -o root -g root -m 0755 /srv/qa-data/sub
printf 'a\n' | sudo tee /srv/qa-data/a.txt >/dev/null; sudo chown 1001:1001 /srv/qa-data/a.txt; sudo chmod 0640 /srv/qa-data/a.txt
printf 'b\n' | sudo tee /srv/qa-data/sub/b.txt >/dev/null; sudo chown postgres:postgres /srv/qa-data/sub/b.txt
sudo touch -d '2020-01-02 03:04:05' /srv/qa-data/sub/b.txt
```

Кроме того — снимок без тегов Sard, сделанный вручную от имени службы:

```bash
# allow-sudo-u: restic от пользователя службы: ручной снимок для проверки
sudo -u sard-agent /usr/libexec/sard/restic --repo /srv/sard/main \
  --password-file /etc/sard/secrets/restic-main.pass --cache-dir /var/cache/sard/restic backup /etc/hostname
```

Переменные (идентификаторы источников — из консоли, адрес страницы источника):

```bash
AG=sard-agent
OUT=$(mktemp -d)
run()  { "$@" >"$OUT/o" 2>"$OUT/e"; echo "exit=$?"; cat "$OUT/o" "$OUT/e"; cat "$OUT/o" "$OUT/e" >> "$OUT/all"; }
audit() { sudo journalctl -t sard-agent --since "$1" --no-pager -o cat; }
SF=<id источника qa-files>; SP=<id qa-pg>; SPN=<id qa-pg-noglob>; SPS=<id qa-pg-space>
SFS3=<id qa-files-s3>; SFSFTP=<id qa-files-sftp>
PASS_MARK=$(sudo cat /etc/sard/secrets/restic-main.pass)   # только для grep утечек
K=/var/cache/sard/restic
owned_root() { sudo find "$K" /srv/sard/main /var/lib/sard-agent/.ssh -user root 2>/dev/null; }
```

## Часть 1. `repo snapshots`

1. `run sudo $AG repo snapshots main`
   → `exit=0`; первая строка `ID TIME HOST SOURCE RUN PATHS`; строки снимков
   `qa-files` с `$SF`, `/srv/qa-data`; снимков `qa-pg` по два на запуск:
   `/app.dump` и `/app.globals.sql` с `$SP` и одним и тем же RUN; ручной
   снимок `/etc/hostname` с `-` в SOURCE и RUN; время каждой строки
   оканчивается на `Z` (Н33) и идёт по возрастанию; ID — 8 символов.
2. `sudo $AG repo snapshots main --source $SF` → только два снимка
   `qa-files`. `sudo $AG repo snapshots main --source no-such-source` →
   `exit=0`, ровно `no snapshots of source no-such-source in repository main`.
3. `sudo $AG repo snapshots main --json | jq -r '.repository, (.snapshots | length)'`
   → `main` и общее число снимков; `… | jq -r '.snapshots[] | select(.source == null) | .paths[0]'`
   → `/etc/hostname`; `… | jq -r '.snapshots[0].id | length'` → `64`;
   `… | jq -e '.snapshots[] | select(.paths[0]=="/app.dump") | .tags | index("postgresql.part=database")'` → не `null`.
4. Права: `sudo -u sard-agent $AG repo snapshots main; echo "exit=$?"` → <!-- allow-sudo-u: проверка отказа от пользователя службы -->
   `exit=0`; `$AG repo snapshots main; echo "exit=$?"` (без sudo) → `exit=2`,
   `PRIVILEGES_REQUIRED`.
5. Только чтение (Н26): `T=$(date '+%F %T'); sudo find /srv/sard/main/locks -type f | wc -l`
   (запомнить), `sudo $AG repo snapshots main`, снова счёт → тот же;
   `audit "$T"` → пусто; `owned_root` → пусто.
6. Пустой репозиторий: `sudo $AG repo add empty /srv/sard/empty` (новый),
   `sudo $AG repo snapshots empty` → `exit=0`, `no snapshots in repository empty`;
   `--json` → `{"repository":"empty","snapshots":[]}`. Затем
   `sudo $AG repo remove empty`.
7. Отказы: `sudo $AG repo snapshots nope` → `exit=2`, `REPOSITORY_UNKNOWN`,
   называет `main`. `sudo mv /etc/sard/secrets/restic-main.pass "$OUT/keep.pass"; sudo $AG repo snapshots main; echo "exit=$?"`
   → `exit=2`, `PASSWORD_FILE_MISSING`; вернуть файл (`sudo mv "$OUT/keep.pass" /etc/sard/secrets/restic-main.pass`).
   `sudo $AG repo snapshots main --source 'a,b'` → `exit=2`, называет `--source`.
8. Недоступное S3: на машине хранилища закрыть порт
   (`sudo iptables -I INPUT -p tcp --dport <порт> -j DROP`);
   `time run sudo $AG repo snapshots s1 --connect-timeout 10s` → `exit=6`,
   `BACKEND_UNAVAILABLE`, `10s`; `real` меньше 25 с. Убрать правило.
9. `sudo $AG repo snapshots s1` и `sudo $AG repo snapshots f1` → `exit=0`, по
   снимку `qa-files-s3` и `qa-files-sftp`.

## Часть 2. Выбор снимка и цель

10. `ID=$(sudo $AG repo snapshots main --source $SF --json | jq -r '.snapshots[-1].id')`;
    `ID8=${ID:0:8}`.
11. Неверный снимок: `sudo $AG repo restore main abc --target /srv/r1`,
    `… main ZZZZZZZZ …`, `… main latest --target /srv/r1` → каждый `exit=2`
    (последний называет `--source`); `ls -d /srv/r1` → нет каталога.
    `sudo $AG repo restore main eeeeeeee --target /srv/r1` → `exit=2`,
    `SNAPSHOT_UNKNOWN`, `repo snapshots main`; каталога нет.
12. Без `--target`: `sudo $AG repo restore main $ID8` → `exit=2`, называет `--target`.
13. Права: `sudo -u sard-agent $AG repo restore main $ID8 --target /tmp/r; echo "exit=$?"` <!-- allow-sudo-u: проверка отказа от пользователя службы -->
    → `exit=2`, `PRIVILEGES_REQUIRED`, `sudo sard-agent repo restore` (Н28);
    `ls -d /tmp/r` → нет.
14. Непригодная цель: `sudo $AG repo restore main $ID8 --target relative`,
    `… --target /srv/sard/main/r`, `… --target /var/cache/sard/restic/r` →
    каждый `exit=2`, `TARGET_INVALID`. Ссылка:
    `sudo ln -s /etc /srv/qa-link; sudo $AG repo restore main $ID8 --target /srv/qa-link/r; echo "exit=$?"`
    → `exit=2`, `TARGET_INVALID`, называет `/srv/qa-link`; `ls -d /etc/r` → нет;
    `sudo rm /srv/qa-link`.
15. Непустая цель: `sudo install -d /srv/r2; echo keep | sudo tee /srv/r2/keep.txt >/dev/null`;
    `T=$(date '+%F %T'); sudo $AG repo restore main $ID8 --target /srv/r2; echo "exit=$?"`
    → `exit=2`, `TARGET_NOT_EMPTY`, сообщение говорит, что нужна пустая или
    новая цель (с A8c-2b — и `--overwrite`); `ls -A /srv/r2` → только
    `keep.txt`; `audit "$T"` → пусто.
16. Пустой том (Н30): `truncate -s 64M "$OUT/vol.img"; sudo mkfs.ext4 -q -F "$OUT/vol.img"`;
    `sudo install -d /srv/r3; sudo mount -o loop "$OUT/vol.img" /srv/r3`;
    `ls -A /srv/r3` → `lost+found`;
    `sudo $AG repo restore main $ID8 --target /srv/r3` → `exit=0`.
17. Корень (Н31): `sudo $AG repo restore main $ID8 --target /` → `exit=2`,
    `ROOT_TARGET_REFUSED`, `--allow-root-target` (A8c-2b: то же с
    `--overwrite`; в A8c-2 `--overwrite` отказывает раньше, шаг 20а);
    `… --target / --allow-root-target` → `exit=2`,
    `TARGET_NOT_EMPTY`; `… --target /srv/r9 --allow-root-target` → `exit=2`,
    называет `--allow-root-target`.

## Часть 3. Восстановление каталога

18. `T=$(date '+%F %T'); run sudo $AG repo restore main $ID --target /srv/r4`
    → `exit=0`; stdout: `main`, `$ID`, время, `$SF`, RUN, `/srv/r4`, число
    файлов и байт, `restored`;
    `sudo stat -c '%U %G %a' /srv/r4` → `root root 700`;
    `sudo diff -r /srv/qa-data /srv/r4/srv/qa-data` → пусто;
    `sudo stat -c '%u %g %a %Y' /srv/qa-data/a.txt /srv/r4/srv/qa-data/a.txt`
    → строки совпадают (`1001 1001 640 …`); то же для `sub/b.txt` (владелец
    `postgres`, время 2020-01-02);
    `audit "$T"` → одна строка `repository main snapshot $ID8 restored to /srv/r4`,
    ваше имя и uid; `owned_root` → пусто.
19. Часть путей: `sudo $AG repo restore main $ID --target /srv/r5 --path /srv/qa-data/sub`
    → `exit=0`; `sudo find /srv/r5 -type f` → только `/srv/r5/srv/qa-data/sub/b.txt`.
    `sudo $AG repo restore main $ID --target /srv/r6 --path /srv/nothing` →
    `exit=2`, `PATH_NOT_IN_SNAPSHOT`; `ls -d /srv/r6` → нет.
    `--path '/srv/*'` → `exit=2`, называет `--path`.
20. (A8c-2b) `--overwrite`: `echo changed | sudo tee /srv/r4/srv/qa-data/a.txt >/dev/null; echo x | sudo tee /srv/r4/extra.txt >/dev/null`;
    `sudo $AG repo restore main $ID --target /srv/r4 --overwrite` → `exit=0`;
    `sudo cat /srv/r4/srv/qa-data/a.txt` → `a`; `/srv/r4/extra.txt` на месте.
20а. (Р74, A8c-2) `sudo find /srv/r4 -printf '%u %m %s %T@ %p\n' | sort > "$OUT/r4"`;
    `sudo $AG repo restore main $ID --target /srv/r4 --overwrite; echo "exit=$?"`
    и `sudo $AG repo restore main $ID --target / --allow-root-target --overwrite; echo "exit=$?"`
    → каждый раз сразу `exit=2`, сообщение называет `--overwrite` и что
    восстанавливать нужно в пустой или новый каталог;
    `sudo find /srv/r4 -printf '%u %m %s %T@ %p\n' | sort | diff - "$OUT/r4"` → пусто.
21. `latest`: `sudo $AG repo restore main latest --source $SF --target /srv/r7`
    → `exit=0`, stdout называет полный id последнего снимка `qa-files`
    (сверить с `repo snapshots main --source $SF`).
22. Ручной снимок без тегов: `MID=$(sudo $AG repo snapshots main --json | jq -r '.snapshots[] | select(.source==null) | .id')`;
    `sudo $AG repo restore main $MID --target /srv/r8` → `exit=0`, источник
    и запуск `-`; `sudo cmp /etc/hostname /srv/r8/etc/hostname` → совпадают.
23. (A8c-2b) Корень по-настоящему (только на копии ВМ): `sudo rm /srv/qa-data/sub/b.txt`;
    `sudo $AG repo restore main $ID --target / --overwrite --allow-root-target --path /srv/qa-data/sub`
    → `exit=0`; `sudo stat -c '%U %Y' /srv/qa-data/sub/b.txt` → `postgres`, время 2020-01-02.
24. Одновременно: создать большой источник (`/srv/big`, 4 ГБ, как шаг 19
    `host-setup.md`), бэкап из консоли, `BID=<его id>`;
    `sudo install -d /srv/r10`;
    `sudo $AG repo restore main $BID --target /srv/r10 & sleep 2; sudo $AG repo restore main $BID --target /srv/r10; echo "exit=$?"; wait`
    (Р79: существующая цель блокируется до проверки пустоты)
    → вторая `exit=6`, `TARGET_BUSY`, `/srv/r10`; первая `exit=0`.
25. Прерывание: `sudo $AG repo restore main $BID --target /srv/r11`, через 2 с
    Ctrl-C → `exit=6`, `INTERRUPTED`, сообщение говорит, что в цель ничего
    не перенесено (Р80); `sudo ls -A /srv/r11` → пусто или каталога нет; `pgrep -u root,sard-agent restic` → пусто;
    `audit` → строка `… restore to /srv/r11 failed: INTERRUPTED`.
26. Нет места: `truncate -s 64M "$OUT/small.img"; sudo mkfs.ext4 -q -F "$OUT/small.img"; sudo install -d /srv/r12; sudo mount -o loop "$OUT/small.img" /srv/r12`;
    `sudo $AG repo restore main $BID --target /srv/r12; echo "exit=$?"` →
    `exit=7`, `TARGET_WRITE`, `/srv/r12`. Отмонтировать.
27. Хранилище пропало посреди: восстановление `$BID` из `s1` (после бэкапа
    `/srv/big` в `s1`) в `/srv/r13`; через 3 с закрыть порт хранилища
    (шаг 8) → не позже чем через 10 мин `exit=6`, `BACKEND_UNAVAILABLE`,
    сообщение говорит, что в цель ничего не перенесено (Р80); `sudo ls -A /srv/r13`
    → пусто или каталога нет. Убрать правило.
28. После шагов 18–27: `owned_root` → пусто; бэкап `qa-files` из консоли →
    `succeeded`.

## Часть 4. PostgreSQL

29. `T=$(date '+%F %T'); run sudo $AG repo restore main latest --source $SP --target /srv/pg1`
    → `exit=0`; `sudo ls -A /srv/pg1` → ровно `app.dump`, `app.globals.sql`;
    `sudo stat -c '%U %G %a' /srv/pg1/*` → `root root 600` дважды (Н29);
    `sudo head -c 5 /srv/pg1/app.dump` → `PGDMP`;
    stdout содержит по порядку `psql -X -d postgres -f '/srv/pg1/app.globals.sql'`,
    `createdb 'app'`, `pg_restore -d 'app' '/srv/pg1/app.dump'`, версию
    `pg_dump` и что база не восстановлена; `audit "$T"` → строка `restored to /srv/pg1`.
30. Напечатанные команды работают: поднять второй кластер
    (`sudo pg_createcluster 16 qa --start`, порт `5433`);
    `sudo chown postgres: /srv/pg1/*`; от имени `postgres` выполнить три
    команды из шага 29 с `-p 5433` → `sudo -u postgres psql -p 5433 -d app -tAc 'select count(*) from t'` <!-- allow-sudo-u: psql от суперпользователя PostgreSQL, не sard-agent -->
    → `1000`; `sudo -u postgres psql -p 5433 -tAc "select 1 from pg_roles where rolname='app_rw'"` → `1`.
31. Без глобальных: `sudo $AG repo restore main latest --source $SPN --target /srv/pg2`
    → `exit=0`; `sudo ls -A /srv/pg2` → только `app.dump`; stdout говорит, что
    глобальных объектов нет, содержит `pg_restore --no-owner --no-acl` и не
    содержит `psql`.
32. Снимок глобальных отдельно: `GID=$(sudo $AG repo snapshots main --source $SP --json | jq -r '[.snapshots[] | select(.paths[0]=="/app.globals.sql")][-1].id')`;
    `sudo $AG repo restore main $GID --target /srv/pg3` → `exit=0`; только
    `app.globals.sql`; в stdout нет `pg_restore`.
33. Имя с пробелом: `sudo $AG repo restore main latest --source $SPS --target /srv/pg4`
    → файлы `my%20db.dump`, `my%20db.globals.sql`; stdout содержит
    `createdb 'my db'`.
34. `--path`: `sudo $AG repo restore main latest --source $SP --target /srv/pg5 --path /app.dump`
    → `exit=2`, называет `--path`; каталога нет.
35. (A8c-2b) `--overwrite`: `echo notes | sudo tee /srv/pg1/notes.txt >/dev/null`;
    `sudo $AG repo restore main latest --source $SP --target /srv/pg1 --overwrite`
    → `exit=0`; `notes.txt` на месте; `app.dump` — новый inode
    (`stat -c %i` до и после различаются).
36. Без клиента PostgreSQL: на ВМ-2 (часть 5) шаг 29 проходит, хотя
    `command -v pg_restore psql createdb` → пусто.
37. Временные файлы: во время шага 29 в другом терминале
    `sudo inotifywait -m /srv/pg1 /tmp -e create` → в `/srv/pg1` события
    `.app.dump.tmp-…` и `.app.globals.sql.tmp-…`, в `/tmp` от `sard-agent` —
    ни одного; после шага `sudo find /srv/pg1 -name '.*.tmp-*'` → пусто.
38. Сбой посреди дампа: восстановление снимка `qa-pg` из большой базы
    (`pgbench -i -s 200 app`, бэкап) в `/srv/pg6`, через 2 с Ctrl-C →
    `exit=6`, `INTERRUPTED`, сообщение говорит, что файла дампа нет;
    `sudo ls -A /srv/pg6` → пусто или каталога нет.

## Часть 5. Восстановление без сервера и регистрации (ВМ-2)

39. На ВМ-2: `sudo apt install ./sard-agent_…deb`; `sard-agent enroll` НЕ
    выполнять; `ls /etc/sard/tls` → нет каталога; `systemctl is-active sard-agent`
    → не `active`.
40. Сервер недоступен: `sudo iptables -I OUTPUT -p tcp -d <адрес сервера> -j REJECT`.
41. Копия пароля `s1` из `docs/qa/host-setup.md` шаг 50 (`$OUT/s1.pass`)
    перенесена на ВМ-2.
    `sudo $AG repo add s1 s3:$S3/$B/s1 --access-key-id $KID --secret-key-from-file "$OUT/ksec" --password-from-file "$OUT/s1.pass"`
    → `exit=0`, «attached», stdout говорит, что изменение вступит при старте службы.
42. `sudo $AG repo snapshots s1` → снимки `qa-files-s3` с хостом ВМ-1.
43. `sudo $AG repo restore s1 latest --source $SFS3 --target /srv/dr`
    → `exit=0`; `sudo find /srv/dr -type f` → файлы `/srv/dr/srv/qa-data/…`,
    содержимое совпадает с ВМ-1 (`sha256sum`); `sudo iptables -L OUTPUT -v -n`
    → счётчик правила шага 40 равен `0` (к серверу не обращались).
44. То же для SFTP: `repo add f1 …` на ВМ-2 (отпечаток `FP`, ключ ВМ-2
    добавлен администратором в `authorized_keys`), `repo restore f1 latest --source $SFSFTP --target /srv/dr2`
    → `exit=0`. Во время восстановления в другом терминале
    `ps -o user=,comm= -C ssh` → только `sard-agent`.
45. `sudo find /var/cache/sard/restic /var/lib/sard-agent/.ssh -user root` → пусто.
    Убрать правило шага 40.

## Часть 6. SELinux (Rocky/Oracle Linux, ВМ-1R)

46. Шаги 18 и 29 на ВМ-1R → `exit=0`; `sudo restorecon -nv -R /srv/pg1` →
    пусто; `sudo ausearch -m avc -ts recent | grep -cE 'sard|restic'` → `0`.
47. `sudo systemctl restart sard-agent` → `active`; бэкапы `main`, `s1`, `f1`
    из консоли → `succeeded`.

## Часть 7. Справка, утечки, документация

48. `$AG repo snapshots --help` и `$AG repo restore --help` → `exit=0`; флаги и
    коды из сценариев «Справка repo snapshots» и «Справка repo restore»;
    `$AG repo; echo "exit=$?"` → `exit=2`, перечисляет `snapshots` и `restore`.
49. Утечки: `grep -cF "$PASS_MARK" "$OUT/all"` → `0`;
    `grep -cF "$(cat "$OUT/ksec")" "$OUT/all"` → `0`;
    `sudo journalctl -t sard-agent --no-pager | grep -cF "$PASS_MARK"` → `0`;
    во время шага 43 `ps -eo args | grep -cF "$(cat "$OUT/ksec")"` (несколько раз) → `0`.
50. Документация: в `docs/operations/agent-host-setup.md` есть раздел о
    снимках и восстановлении с `sudo sard-agent repo snapshots`,
    `sudo sard-agent repo restore`, `--allow-root-target`, восстановлением на
    новом хосте без регистрации и `restorecon -R`; (Р82) раздел и ADR 0059
    советуют восстанавливать в новую цель или в существующую root 0700 и
    проверять `find <цель> -perm /6000`, прежде чем что-то выносить;
    `grep -nE 'restic (snapshots|dump|restore)' docs/plugins/postgresql.md docs/operations/agent-host-setup.md docs/operator/*.md`
    → пусто, кроме `docs/operator/06-data-and-backup.md` (самобэкап сервера,
    вне A8c); `docs/plugins/postgresql.md`, «Восстановление руками», ведёт через
    `sard-agent repo restore`.

## Часть 8. Поправки 2026-10-13 (Р72–Р79, A8c-2)

51. Разрешённый список env_file (Р72). `S1ENV=/etc/sard/secrets/restic-s1.env`;
    `sudo cp -p $S1ENV "$OUT/s1.env.bak"`. Для каждой строки
    `RCLONE_PASSWORD_COMMAND=touch /root/pwned`,
    `AWS_SHARED_CREDENTIALS_FILE=/etc/shadow`, `HTTPS_PROXY=http://127.0.0.1:9`,
    `SSL_CERT_FILE=/etc/shadow`:
    `echo '<строка>' | sudo -u sard-agent tee -a $S1ENV >/dev/null` (так её <!-- allow-sudo-u: запись в env_file от владельца файла, пользователя службы -->
    дописал бы нарушитель — пользователь службы);
    `T=$(date '+%F %T'); run sudo $AG repo restore s1 latest --source $SFS3 --target /srv/e1`
    → `exit=2`, `ENV_FILE_INVALID`, путь `$S1ENV`, номер строки и имя
    переменной, без её значения; `ls -d /srv/e1 /root/pwned` → нет обоих;
    `audit "$T"` → пусто. Вернуть файл (`sudo cp -p "$OUT/s1.env.bak" $S1ENV`).
    С `HTTPS_PROXY` в файле `sudo $AG repo snapshots s1` → не `ENV_FILE_INVALID`
    (от пользователя службы действует прежний запрещающий список).
52. Локальный репозиторий с env_file (Р72): в `/etc/sard/agent.d/repo-main.yaml`
    добавить `env_file` на файл `sard-agent 0600` со строкой
    `HTTPS_PROXY=http://127.0.0.1:9` (часть QA, `sudoedit`);
    `sudo $AG repo restore main $ID --target /srv/e2` → `exit=2`,
    `ENV_FILE_INVALID`. Убрать `env_file`.
53. rclone (Р72): `/etc/sard/agent.d/repo-rc.yaml` с `url: rclone:remote:x`
    (часть QA); `sudo $AG repo restore rc 00000000 --target /srv/e3` →
    `exit=2`, `BACKEND_NOT_SUPPORTED`, `rclone`; `ls -d /srv/e3` → нет.
    Удалить фрагмент.
54. Временный каталог (Р73): во время шага 18 в другом терминале
    `sudo inotifywait -m /srv/r4 -e create,moved_to` → сначала
    `.sard-restore-…` (каталог), затем `moved_to srv`; во время работы
    `sudo stat -c '%U %a' /srv/r4/.sard-restore-*` → `root 700`;
    `ps -o args= -C restic` (от root) содержит `--target /proc/self/fd/3/root`
    и не содержит `/srv/r4`; после шага `ls -A /srv/r4` → только `srv`.
55. Снимок с файлом в корне (Р73): ручной stdin-снимок
    `echo hello | sudo -u sard-agent /usr/libexec/sard/restic --repo /srv/sard/main --password-file /etc/sard/secrets/restic-main.pass --cache-dir /var/cache/sard/restic backup --stdin --stdin-filename root.txt`; <!-- allow-sudo-u: ручной снимок restic от пользователя службы для проверки -->
    его id — `RID`; `sudo $AG repo restore main $RID --target /srv/e4` →
    `exit=0`; `sudo cat /srv/e4/root.txt` → `hello`.
56. Подмена внутри цели (Р73; только на копии ВМ): цель `/srv/e5`
    (`sudo install -d -m 0777 /srv/e5`), от имени `sard-agent` цикл
    `while :; do mkdir -p /srv/e5/srv 2>/dev/null; rm -rf /srv/e5/srv; ln -sfn /etc /srv/e5/srv; done &`;
    `sudo find /etc -printf '%u %m %p\n' | sort > "$OUT/etc0"`;
    `sudo $AG repo restore main $ID --target /srv/e5; echo "exit=$?"` →
    `exit=2` (`TARGET_NOT_EMPTY`, если цикл успел создать запись) или
    `exit=7` (`TARGET_WRITE`, запись `srv` появилась во время
    восстановления); остановить цикл;
    `sudo find /etc -printf '%u %m %p\n' | sort | diff - "$OUT/etc0"` → пусто;
    `sudo find /srv/e5 -name '.sard-restore-*'` → пусто.
57. Остаток после аварии (Р73): `sudo $AG repo restore main $BID --target /srv/e6 &`,
    через 3 с `sudo pkill -KILL -f 'sard-agent repo restore'`;
    `sudo pkill -KILL restic`; `ls -A /srv/e6` → `.sard-restore-…`;
    `sudo $AG repo restore main $ID --target /srv/e6` → `exit=2`,
    `TARGET_NOT_EMPTY`, называет `.sard-restore-…` как остаток прерванного
    восстановления и советует удалить. `sudo rm -rf /srv/e6`.
58. Квота и только чтение (Р76): том шага 26 очистить и перемонтировать
    только для чтения: `sudo rm -rf /srv/r12/*; sudo mount -o remount,ro /srv/r12`;
    `sudo $AG repo restore main $ID --target /srv/r12` → `exit=7`,
    `TARGET_WRITE`. С квотой ext4 (`usrquota`, `setquota -u root 100 100 0 0`
    на этом томе, если QA умеет) → `exit=7`, `TARGET_WRITE`; иначе отметить
    как непроверенное (есть `@local`).
59. setpriv (Р77; копия ВМ): `sudo mv /usr/bin/setpriv /usr/bin/setpriv.qa`;
    `T=$(date '+%F %T'); run sudo $AG repo restore f1 latest --source $SFSFTP --target /srv/e7`
    → `exit=1`, `SETPRIV_MISSING`, `setpriv`, `util-linux`; `ls -d /srv/e7` →
    нет; `audit "$T"` → пусто. `sudo $AG repo restore s1 latest --source $SFS3 --target /srv/e8`
    → `exit=0` (S3 setpriv не нужен). Вернуть `setpriv`.
60. Пароль каналом (Р78): во время шага 18 `sudo cat /proc/$(pgrep -u root -n restic)/environ | tr '\0' '\n' | grep RESTIC_PASSWORD`
    → нет `RESTIC_PASSWORD_FILE=/etc/sard/secrets/…` и нет `RESTIC_PASSWORD=`;
    `ps -o args= -C restic` (от root) — `--password-file /proc/self/fd/…`;
    `sudo ls -l /proc/$(pgrep -u root -n restic)/fd` → дескриптор пароля —
    `pipe:`. FIFO: `sudo mv /etc/sard/secrets/restic-main.pass "$OUT/m.pass"; sudo -u sard-agent mkfifo -m 0600 /etc/sard/secrets/restic-main.pass`; <!-- allow-sudo-u: FIFO от пользователя службы для проверки -->
    `time sudo $AG repo restore main $ID --target /srv/e9` → сразу `exit=2`,
    `SECRET_FILE_REJECTED`, `real` меньше 5 с. Вернуть файл пароля.
61. `--source` с id (Р75): `sudo $AG repo restore main $ID8 --source $SF --target /srv/e10`
    → `exit=0`; `sudo $AG repo restore main $ID8 --source $SP --target /srv/e11`
    → `exit=2`, `SNAPSHOT_UNKNOWN`, называет `$ID8` и `$SP`.

## Часть 9. Ответы 2026-10-14 (Р83–Р86, A8c-3)

Снимки этой части делаются вручную от имени службы и в конце удаляются:

```bash
# allow-sudo-u: restic от пользователя службы: ручные снимки для проверки
R="sudo -u sard-agent /usr/libexec/sard/restic --repo /srv/sard/main --password-file /etc/sard/secrets/restic-main.pass --cache-dir /var/cache/sard/restic"
```

62. Снимок PostgreSQL неожиданной формы (Р83). Два пути:
    `$R backup --tag postgresql.part=database --tag postgresql.database=app /etc/hostname /etc/hosts`;
    без имени базы: `echo x | $R backup --stdin --stdin-filename app.dump --tag postgresql.part=database`.
    Их id — `X1` и `X2` (`$R snapshots --json | jq -r '.[-2:][].id'`). Для
    каждого: `T=$(date '+%F %T'); run sudo $AG repo restore main <id> --target /srv/u1`
    → `exit=1`, `RESTIC_OUTPUT_UNEXPECTED`, первые 8 символов id, сообщение
    говорит, что восстановить каталогом нельзя и ничего не записано;
    `ls -d /srv/u1` → нет; `audit "$T"` → пусто.
63. Нет тега версии pg_dump (Р84):
    `sudo cat /srv/pg1/app.dump | $R backup --stdin --stdin-filename app.dump --tag postgresql.part=database --tag postgresql.database=app`;
    его id — `X3`; `run sudo $AG repo restore main $X3 --target /srv/u2` →
    `exit=0`; `sudo ls -A /srv/u2` → только `app.dump`; stdout содержит
    `not older than the version of pg_dump that made the backup`, и в этой
    строке нет номера версии (`grep 'pg_dump that made' "$OUT/o" | grep -cE '[0-9]+\.[0-9]+'` → `0`).
64. Аудит при отказе после создания цели (Р85): том шага 58 только для
    чтения (`mountpoint -q /srv/r12 || sudo mount -o loop,ro "$OUT/small.img" /srv/r12; sudo mount -o remount,ro /srv/r12`);
    `T=$(date '+%F %T'); run sudo $AG repo restore main latest --source $SP --target /srv/r12`
    → `exit=7`, `TARGET_WRITE`; `audit "$T"` → одна строка
    `… restore to /srv/r12 failed: TARGET_WRITE`. Для сравнения (Р81):
    `T=$(date '+%F %T'); sudo $AG repo restore main $ID --target /srv/r12`
    → `exit=7`; `audit "$T"` → пусто. Отмонтировать `/srv/r12`.
65. Удалить снимки части: `$R forget $X1 $X2 $X3`;
    `sudo $AG repo snapshots main` → их нет.

## Часть 10. Ответы 2026-10-14 на вопросы architect (Р87, Р88, A8c-3)

66. Остаток временного файла после аварии (Р88). Нужен снимок большой базы
    шага 38 (последний снимок базы источника `$SP`):
    `sudo $AG repo restore main latest --source $SP --target /srv/e12 &`;
    `until sudo find /srv/e12 -maxdepth 1 -name '.app.dump.tmp-*' 2>/dev/null | grep -q .; do sleep 0.2; done`;
    `sudo pkill -KILL -f 'sard-agent repo restore'; sudo pkill -KILL restic`;
    `sudo ls -A /srv/e12` → ровно `.app.dump.tmp-…`; запомнить
    `sudo stat -c '%i %s' /srv/e12/.app.dump.tmp-*` → `S0`.
    `T=$(date '+%F %T'); run sudo $AG repo restore main latest --source $SP --target /srv/e12`
    → `exit=2`, `TARGET_NOT_EMPTY`, сообщение называет
    `/srv/e12/.app.dump.tmp-…` как остаток прерванного восстановления
    (`remains of an interrupted restore`) и советует удалить; `audit "$T"` →
    пусто; `sudo stat -c '%i %s' /srv/e12/.app.dump.tmp-*` → то же `S0`
    (агент файл не удалил и не изменил). То же восстановлением каталога:
    `sudo $AG repo restore main $ID --target /srv/e12` → `exit=2`, называет
    тот же файл. `sudo rm -rf /srv/e12`.
67. Подмена файла PostgreSQL в цели, куда пишут другие (Р87; только на
    копии ВМ): `sudo install -d -m 0777 /srv/e13`;
    `echo FOREIGN | sudo -u nobody tee /tmp/qa-foreign >/dev/null`; от имени <!-- allow-sudo-u: запись чужого файла от nobody для проверки подмены -->
    `nobody` цикл
    `while :; do for f in /srv/e13/.app.dump.tmp-*; do [ -e "$f" ] && mv "$f" /srv/e13/stolen 2>/dev/null && cp /tmp/qa-foreign "$f"; done; done &`.
    20 раз: очистить цель `sudo find /srv/e13 -mindepth 1 -delete`;
    `T=$(date '+%F %T'); run sudo $AG repo restore main latest --source $SP --target /srv/e13`.
    Каждый запуск → либо `exit=0` и `sudo stat -c '%U %G %a' /srv/e13/app.dump`
    → `root root 600`, `sudo head -c 5 /srv/e13/app.dump` → `PGDMP`; либо
    `exit=7`, `TARGET_WRITE`, сообщение называет `/srv/e13/app.dump`,
    `is not the file the agent wrote` и что файл не удалён; тогда
    `stat -c %U /srv/e13/app.dump` → `nobody`, `cat /srv/e13/app.dump` →
    `FOREIGN`, в `/srv/e13` нет `app.globals.sql`, `audit "$T"` → строка
    `… restore to /srv/e13 failed: TARGET_WRITE`; `sudo stat -c '%U %a' /srv/e13/stolen`
    (если есть) → `root 600`. Ни разу: `exit=0` при `app.dump` не от root.
    Если цикл ни разу не успел — отметить подмену как непроверенную (есть
    `@local`). Остановить цикл, `sudo rm -rf /srv/e13 /tmp/qa-foreign`.

Вручную не воспроизводятся и проверяются тестами `@local` с теми же
названиями: EXDEV при переносе, `fstat` временного каталога с чужим
владельцем, ссылка, созданная restic во временном каталоге, при его
удалении, 10000 снимков, неоднозначный префикс id, `umask 000` для цели и
файлов PostgreSQL, права до записи содержимого, кавычка в имени базы, ошибки
отдельных файлов (`RESTORE_INCOMPLETE`), подмена ссылкой компонента цели между
проверкой и созданием, отсутствие пользователя службы, Scrubber для строк
restic с паролем и ключом, сбой записи файла глобальных объектов после
готового дампа (Р86: дампа в цели не остаётся), путь снимка PostgreSQL не в
корне и неразбираемое имя базы (Р83), снимок глобальных объектов неожиданной
формы к снимку базы (Р83), имя файла PostgreSQL, пропавшее сразу после
переименования, и чужой файл под именем законченного дампа или временного
файла при взятии назад (Р87), имена, похожие на временный файл, но не того
вида (Р88).
