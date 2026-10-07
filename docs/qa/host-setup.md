# QA: настройка хоста агента командами `sard-agent` (A8a)

Сценарии: `docs/specs/agent/host-setup.feature`. **Черновик: спецификация ждёт
утверждения владельцем**, ответы на открытые вопросы могут изменить шаги
(особенно Н1, Н4–Н10). Классы и номера кодов выхода — A2b (ADR 0025).

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
или `dnf install ./dist/…rpm`), `sard-server` доступен (`make up` на другой
машине или `docs/operator/02-install.md`), агент зарегистрирован по
`docs/qa/agent-enroll.md`, служба запущена (`systemctl is-active sard-agent` →
`active`). В `/etc/sard/agent.yaml` нет ключей `repositories` и `secrets`
(см. открытый вопрос О1). Оператор — обычный пользователь с `sudo`.

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

## Часть 1. Права запуска

1. `sudo -u sard-agent $AG secret set db --stdin </dev/null; echo "exit=$?"`
   → `exit=2`; `PRIVILEGES_REQUIRED`; сообщение содержит
   `sudo sard-agent secret set db --stdin`.
2. `$AG secret list; echo "exit=$?"` (обычный пользователь, без sudo)
   → `exit=2`; `PRIVILEGES_REQUIRED`, `sudo`.
3. `sudo -u sard-agent $AG secret list; echo "exit=$?"` → `exit=0`.
4. `snap > "$OUT/s0"`; повторить шаги 1–3; `snap | diff - "$OUT/s0"` → пусто.

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
   `$SECRET`; основной конфиг не изменён (`sudo sha256sum /etc/sard/agent.yaml`
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
    `… set -db …` → `exit=2`, `NAME_INVALID`.
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
28. Заглушки: `sudo $AG repo add s3x s3:https://s3.example.com/b/x`,
    `… sftpx sftp:u@h:/x`, `… restx rest:https://u:URL-MARKER@h/x`
    → каждый раз `exit=2`, `BACKEND_NOT_SUPPORTED`, вид адреса;
    `grep -c URL-MARKER "$OUT/all"` → `0` (выполнять через `run`).
29. Путь: `sudo $AG repo add r1 relative` → `exit=2`, `LOCAL_PATH_INVALID`;
    `sudo $AG repo add r2 /etc/hostname` → `exit=2`, `LOCAL_PATH_INVALID`.
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

36. `sudo ls -Z /etc/sard/agent.d /etc/sard/secrets` → те же метки, что даёт
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

40. Для `secret set`, `secret list`, `secret remove`, `repo add`, `repo show`,
    `repo remove`, `repo password`, `repo list`: `$AG <команда> --help` →
    `exit=0`; флаги из сценария «Справка команды…», сказано, нужен ли sudo,
    коды выхода.
41. `grep -cF "$SECRET" "$OUT/all"` → `0`; `grep -c URL-MARKER "$OUT/all"` → `0`;
    `sudo journalctl -t sard-agent --no-pager | grep -cF "$SECRET"` → `0`.

Вручную не воспроизводятся и проверяются тестами `@local` с теми же
названиями: сбои fsync, rename и смены владельца при записи, недоступный
syslog, нечитаемый журнал команд, неудачный `systemctl restart`, отсутствие
systemd, `umask 000`, отсутствующий пользователь службы, `service.user`.
