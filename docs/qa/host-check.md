<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# QA: проверка хоста агента `sard-agent repo check` и `sard-agent check` (A8d)

Сценарии: `docs/specs/agent/host-check.feature` (Ч1–Ч30). ЧЕРНОВИК: ждёт
одобрения владельца вместе со спецификацией; шаги, зависящие от ответа на
вопрос, помечены «(ЧВ<номер>)». Части помечены срезом (A8d-1 … A8d-4) и
выполняются после реализации своего среза.

Ожидаемый результат — после «→». Любое расхождение — дефект.

Коды: `0` в порядке, `8` только предупреждения (ЧВ4), `1` есть ошибки,
`2` команда не выполнена (флаги, `PRIVILEGES_REQUIRED`, `REPOSITORY_UNKNOWN`,
`SERVICE_USER_UNKNOWN`, `STALE_LOCKS_FOREIGN`), `6` прерывание и временные
отказы `repo unlock`.

## Стенды

- **ВМ-1 Debian 12** и **ВМ-1R Rocky 9** (`getenforce` → `Enforcing`) после
  `docs/qa/host-setup.md`, части 0–11: агент зарегистрирован, служба
  `active`, подключены `main` (локальный `/srv/sard/main`), `s1` (S3) и `f1`
  (SFTP), секрет `pg`; на ВМ-1 PostgreSQL 16 и клиент `postgresql-client-16`.
- **ВМ-2** — второй хост с тем же репозиторием `main` (общий каталог по NFS
  или `s1`), для чужих блокировок.
- Часы ВМ синхронизированы (`timedatectl` → `System clock synchronized: yes`).

```bash
AG=sard-agent
OUT=$(mktemp -d)
run()  { "$@" >"$OUT/o" 2>"$OUT/e"; echo "exit=$?"; cat "$OUT/o" "$OUT/e"; cat "$OUT/o" "$OUT/e" >> "$OUT/all"; }
audit() { sudo journalctl -t sard-agent --since "$1" --no-pager -o cat; }
snap() { sudo find /etc/sard /var/cache/sard /var/lib/sard-agent /etc/systemd/system/sard-agent.service.d \
          -printf '%p %u %g %m %s %T@\n' 2>/dev/null | sort; }
PASS_MARK=$(sudo cat /etc/sard/secrets/restic-main.pass)   # только для grep утечек
PG_MARK=$(sudo cat /etc/sard/secrets/pg)
S3_MARK=$(sudo sed -n 's/^AWS_SECRET_ACCESS_KEY=//p' /etc/sard/secrets/restic-s1.env)
```

## Часть 1. `repo check` (A8d-1)

1. `run sudo $AG repo check main`
   → `exit=0`; шесть строк `OK` в порядке `repository.storage`,
   `repository.credentials`, `repository.initialized`, `repository.password`,
   `repository.id`, `repository.locks`, у каждой предмет `main`; строки
   `hint:` нет; последняя — `summary: 6 ok, 0 warnings, 0 errors, 0 skipped`.
2. `run sudo $AG repo check` → блоки `main`, `s1`, `f1` в порядке
   `sudo $AG repo list`; `exit=0`.
3. `run sudo $AG repo check nope` → `exit=2`, `REPOSITORY_UNKNOWN`,
   называет `main`, `s1`, `f1`; stdout пуст.
4. Неверный пароль: `sudo cp -p /etc/sard/secrets/restic-main.pass "$OUT/keep.pass"; printf 'wrong' | sudo tee /etc/sard/secrets/restic-main.pass >/dev/null`;
   `run sudo $AG repo check main` → `exit=1`; `repository.password` —
   `ERROR`, `WRONG_PASSWORD`, строка `hint:` со ссылкой
   `…/09-troubleshooting.md#repositorypassword`; `repository.id` и
   `repository.locks` — `SKIP`, `not checked: repository.password failed`.
   Вернуть файл: `sudo cp -p "$OUT/keep.pass" /etc/sard/secrets/restic-main.pass`.
5. Нет файла пароля: `sudo mv /etc/sard/secrets/restic-main.pass "$OUT/keep.pass"`;
   `run sudo $AG repo check main` → `exit=1`, `PASSWORD_FILE_MISSING`,
   подсказка `sudo sard-agent repo add main`. Вернуть файл.
6. Права A1: `sudo chmod 0644 /etc/sard/secrets/restic-main.pass`;
   `run sudo $AG repo check main` → `exit=1`, `SECRET_FILE_REJECTED`,
   подсказка `sudo chmod 0600 /etc/sard/secrets/restic-main.pass`;
   `sudo chmod 0600 /etc/sard/secrets/restic-main.pass`.
7. Недоступное S3: на машине хранилища
   `sudo iptables -I INPUT -p tcp --dport <порт> -j DROP`;
   `time run sudo $AG repo check s1 --connect-timeout 3s` → `exit=1`;
   `repository.storage` — `ERROR`, `BACKEND_UNAVAILABLE`, `3s`; `real`
   меньше 15 с. Без `--connect-timeout` → сообщение называет `10s` (ЧВ9).
   Убрать правило.
8. Неверный ключ S3: заменить секретный ключ в `restic-s1.env` на другой
   (сохранив копию), `run sudo $AG repo check s1` → `repository.credentials`
   — `ERROR`, `STORAGE_ACCESS_DENIED` или `S3_KEY_REJECTED`; вывод не
   содержит ни старого, ни нового ключа. Вернуть файл.
9. SFTP без ключа: на сервере SFTP убрать ключ агента из `authorized_keys`;
   `run sudo $AG repo check f1` → `repository.credentials` — `ERROR`,
   `SSH_KEY_NOT_AUTHORIZED`, подсказка называет `authorized_keys`. Вернуть.
10. Неинициализированный: `sudo $AG repo add tmp /srv/sard/tmp` (создаст),
    `sudo -u sard-agent rm -rf /srv/sard/tmp/*`; `run sudo $AG repo check tmp` <!-- allow-sudo-u: порча репозитория от имени владельца файлов -->
    → `repository.initialized` — `ERROR`, `REPOSITORY_NOT_INITIALIZED`.
    `sudo $AG repo remove tmp`.
11. Права: `sudo -u sard-agent $AG repo check main; echo "exit=$?"` → `exit=0`. <!-- allow-sudo-u: проверка запуска от пользователя службы -->
    `$AG repo check main; echo "exit=$?"` (без sudo) → `exit=2`,
    `PRIVILEGES_REQUIRED`, `sudo sard-agent repo check`.

## Часть 2. `repository.id` (A8d-1, ЧВ5)

12. `sudo systemctl restart sard-agent`; `sudo ls -l /var/lib/sard-agent/` →
    файл объявленных репозиториев, владелец `sard-agent`, `0600`.
    `run sudo $AG repo check main` → `repository.id` — `OK`, id совпадает с
    `sudo $AG repo show main --json | jq -r .repository_id`.
13. Подмена репозитория (служба объявила старый id, в хранилище — новый):
    `sudo mv /srv/sard/main /srv/sard/main.keep; sudo install -d -o sard-agent -g sard-agent -m 0700 /srv/sard/main`;
    `sudo -u sard-agent /usr/libexec/sard/restic init -r /srv/sard/main --password-file /etc/sard/secrets/restic-main.pass` <!-- allow-sudo-u: новый репозиторий на месте старого, от имени владельца файлов -->
    (службу не перезапускать). `run sudo $AG repo check main` →
    `repository.id` — `ERROR`, называет оба id, подсказка
    `sudo systemctl restart sard-agent`; `exit=1`.
    Вернуть: `sudo rm -rf /srv/sard/main; sudo mv /srv/sard/main.keep /srv/sard/main`.
14. Новый репозиторий без перезапуска: `sudo $AG repo add tmp /srv/sard/tmp --no-restart`;
    `run sudo $AG repo check tmp` → `repository.id` — `WARN`,
    `not announced to the server yet`, `exit=8`. `sudo $AG repo remove tmp`.

## Часть 3. Блокировки и `repo unlock` (A8d-1, ЧВ1, ЧВ2)

15. Свежая своя: в одном терминале долгий бэкап
    (запуск источника `qa-files` из консоли на большом каталоге); во
    втором — `run sudo $AG repo check main` → `repository.locks` — `OK`,
    называет `1 lock` и имя хоста ВМ-1; `exit=0`. Бэкап завершается
    `succeeded`.
16. Зависшая своя: создать блокировку и состарить её —
    `sudo -u sard-agent /usr/libexec/sard/restic -r /srv/sard/main --password-file /etc/sard/secrets/restic-main.pass backup /etc/hostname &` <!-- allow-sudo-u: ручная блокировка restic от имени службы -->
    и сразу `sudo kill -9 %1` (в `locks/` останется файл блокировки с
    мёртвым pid). Файл блокировки зашифрован, время в нём не поправить,
    поэтому часы ВМ переводятся на 25 ч вперёд:
    `sudo timedatectl set-ntp false; sudo date -s '+25 hours'`.
    `run sudo $AG repo check main` → `repository.locks` — `WARN`, называет
    хост ВМ-1 и возраст `25h`, подсказка
    `sudo sard-agent repo unlock main --remove-stale`; `exit=8`.
17. `run sudo $AG repo unlock main` → `exit=2`, называет `--remove-stale`;
    `sudo ls /srv/sard/main/locks | wc -l` → 1.
18. `T=$(date '+%F %T'); run sudo $AG repo unlock main --remove-stale` →
    `exit=0`, предупреждение в stderr, `removed 1 stale lock`;
    `sudo ls /srv/sard/main/locks | wc -l` → 0;
    `audit "$T"` → `repository main stale locks removed: 1`, `alice`.
19. Повтор `run sudo $AG repo unlock main --remove-stale` → `exit=0`,
    `no stale locks of this host in repository main`; строки аудита нет.
20. Чужая: повторить шаг 16 на ВМ-2 (общий репозиторий), часы ВМ-1 по-прежнему
    +25 ч. На ВМ-1 `run sudo $AG repo check main` → `WARN`, называет хост
    ВМ-2, подсказка «on <ВМ-2>», без команды для ВМ-1.
    `run sudo $AG repo unlock main --remove-stale` → `exit=2`,
    `STALE_LOCKS_FOREIGN`, называет ВМ-2; блокировка ВМ-2 на месте. Снять её
    на ВМ-2 той же командой → `exit=0`.
21. Права: `sudo -u sard-agent $AG repo unlock main --remove-stale; echo "exit=$?"` <!-- allow-sudo-u: проверка отказа от пользователя службы -->
    → `exit=2`, `PRIVILEGES_REQUIRED`.
22. Вернуть часы: `sudo timedatectl set-ntp true`; дождаться синхронизации.

## Часть 4. `--read-data` (A8d-1)

23. `time run sudo $AG repo check main --read-data 5` → `exit=0`; седьмая
    строка `repository.integrity` — `OK`.
24. Без флага: `run sudo $AG repo check main` → строки
    `repository.integrity` нет; `pgrep -f 'restic.*check'` во время
    выполнения → ничего.
25. `sudo $AG repo check main --read-data 0`, `… 101`, `… 5.5` → каждый
    `exit=2`, называет `--read-data`. `sudo $AG check --read-data 5` →
    `exit=2`, называет `repo check`.
26. Порча данных (только тестовая ВМ): удалить один файл в
    `/srv/sard/main/data/*/`; `run sudo $AG repo check main --read-data 100`
    → `repository.integrity` — `ERROR`, подсказка
    `…#repositoryintegrity`; `exit=1`. Восстановить репозиторий из копии.
27. Учётка только на чтение (V0 — заглушка): в Garage выдать ключ только на
    чтение, подключить `s1ro` (`sudo $AG repo add s1ro … --access-key-id <ro>`
    — подключение может отказать на записи; тогда записать `env`-файл и
    фрагмент вручную по `host-setup.md`); `run sudo $AG repo check s1ro --read-data 5`
    → `repository.integrity` — `ERROR`, текст
    `restic check needs to create a lock in the repository and these credentials cannot write`
    и `not supported yet`; сырой многострочный вывод restic не показан.

## Часть 5. `sard-agent check` на здоровом хосте (A8d-2)

28. `time run sudo $AG check` → `exit=0`; строки в порядке Ч10: `service.user`,
    `config.*`, `files.permissions`, `restic.binary`, `tls.*`, `server.*`,
    `clock.skew`, по шесть `repository.*` на `main`, `s1`, `f1`,
    `secret.readable pg`, `plugin.declared`, `plugin.programs files`,
    `plugin.programs postgresql`, `restore.locations`, `selinux.*`,
    `service.running`, `service.steps`; `tls.role` и `restore.locations` —
    `SKIP` с упоминанием V1 и R2; `real` меньше 10 с.
29. `plugin.programs postgresql` называет пути и версию 16 для `pg_dump`,
    `psql`, `pg_dumpall`, `pg_restore`; `service.running` — время старта в
    UTC; `restic.binary` — `0.19.1`; `tls.expiry` — дата окончания.

## Часть 6. Ошибки и предупреждения check с подсказками (A8d-2)

Каждый шаг: сломать, `run sudo $AG check`, проверить строку, код и
подсказку, вернуть.

30. `sudo chmod 0644 /etc/sard/tls/agent.key` → `files.permissions` —
    `ERROR`, подсказка `sudo chmod 0600 /etc/sard/tls/agent.key`; `exit=1`.
31. `sudo chown root /etc/sard/secrets/pg` → `files.permissions` — `ERROR`,
    подсказка `sudo chown sard-agent`. Вернуть владельца.
32. `sudo chmod g+w /etc/sard/agent.yaml` → `files.permissions` — `WARN`,
    `exit=8`. `sudo chmod g-w /etc/sard/agent.yaml`.
33. Дубликат: скопировать блок `repositories.main` основного конфига в
    `/etc/sard/agent.d/zz.yaml` → `config.names` — `ERROR`,
    `DUPLICATE_NAME`, оба файла; зависимые строки — `SKIP`
    `not checked: …`. Удалить `zz.yaml`.
34. Битый YAML в `/etc/sard/agent.d/zz.yaml` → `config.load` — `ERROR`,
    называет файл; `restic.binary`, `service.*` выполнены. Удалить.
35. `secrets.pg` на несуществующий файл (правкой фрагмента
    `secret-pg.yaml` на копии) → `config.references` — `ERROR`, называет
    ключ и путь, подсказка `sudo sard-agent secret set pg`. Вернуть.
36. `restic.path: /usr/bin/restic` с restic из дистрибутива другой версии
    → `restic.binary` — `WARN` (не ниже 0.19.0) или `ERROR`
    `RESTIC_TOO_OLD`. Убрать `restic.path`.
37. Сертификат: `sudo cp -p /etc/sard/tls/agent.key "$OUT/k"`; подложить
    другой ключ (`openssl genpkey -algorithm ed25519`) с владельцем
    `sard-agent` `0600` → `tls.identity` — `ERROR`, подсказка
    `sudo sard-agent enroll --force`; `server.*` — `SKIP`. Вернуть ключ.
38. Срок < 30 дней: на стенде с сервером, выдающим короткие сертификаты (или
    часы ВМ вперёд на срок минус 10 дней при `set-ntp false`) → `tls.expiry`
    — `WARN`, `expires in`, `automatic renewal is not available yet`.
    Вернуть часы.
39. `sudo systemctl stop sard-agent` → `service.running` — `ERROR`,
    подсказка `sudo systemctl start sard-agent`; `sudo systemctl start sard-agent`.
40. pg_dump: `sudo mv /usr/bin/pg_dump "$OUT/"` (Debian — ссылка
    pg_wrapper) → `plugin.programs postgresql` — `WARN` (ЧВ11), подсказка
    `postgresql-client`. Вернуть.
41. Нет репозиториев (отдельная ВМ или временно убрать фрагменты в `$OUT`)
    → строка `repository.storage` с предметом `-` — `WARN`
    `no repositories configured`, подсказка `sudo sard-agent repo add`.

## Часть 7. `--json`, `--offline`, коды (A8d-2)

42. `sudo $AG check --json | jq -c '{check,subject,status}'` → по объекту на
    строку; `sudo $AG check --json | jq -r 'keys|join(",")' | sort -u` →
    ровно `check,hint,message,status,subject`;
    `… | jq -r .check | sort -u | wc -l` → 28 на ВМ-1R (Ч10).
43. Сравнить с предыдущей версией агента после обновления пакета:
    множество `check` не сократилось и не переименовано (правило 7).
44. `sudo $AG check --json --no-such-flag; echo "exit=$?"` → `exit=2`, stdout
    пуст.
45. `sudo ss -tnp | grep sard-agent` в цикле во время
    `run sudo $AG check --offline` → соединений процесса check нет;
    `server.*`, `clock.skew`, все `repository.*` — `SKIP` `offline`;
    `exit=0`.
46. Ansible: задача `command: sard-agent check --json` с
    `failed_when: rc not in [0, 8]` → проходит на здоровом хосте и при
    шаге 32; падает при шаге 30.

## Часть 8. Права запуска и только чтение (A8d-2)

47. `sudo -u sard-agent $AG check; echo "exit=$?"` на ВМ-1R → работает; <!-- allow-sudo-u: проверка запуска от пользователя службы -->
    `selinux.denials` — `SKIP` `needs root`; после `summary` строка
    `some checks need root: run sudo sard-agent check`.
48. `$AG check; echo "exit=$?"` (alice без sudo) → `exit=2`,
    `PRIVILEGES_REQUIRED`, `sudo sard-agent check`; stdout пуст.
49. Только чтение: `snap > "$OUT/before"; T=$(date '+%F %T')`;
    `sudo $AG check; sudo $AG repo check`; `snap > "$OUT/after"`;
    `diff "$OUT/before" "$OUT/after"` → пусто; `audit "$T"` → пусто;
    `sudo ls /srv/sard/main/locks` → пусто (если бэкап не идёт);
    `sudo journalctl -u sard-agent --since "$T"` → нет перезапуска.
50. Во время бэкапа (шаг 15) `sudo $AG check` → бэкап `succeeded`,
    `repository.locks` — `OK`.

## Часть 9. SELinux (A8d-2, ВМ-1R)

51. Здоровый хост → `selinux.labels` и `selinux.denials` — `OK`.
52. `sudo chcon -t tmp_t /etc/sard/secrets/pg` → `selinux.labels` — `WARN`,
    называет файл, текущую и ожидаемую метку, подсказку
    `sudo restorecon -Rv /etc/sard/secrets/pg`. `sudo restorecon -Rv /etc/sard`.
53. Вызвать отказ: временно подключить локальный репозиторий в
    каталог с чужой меткой (`/srv/sard/bad`, `chcon -t httpd_sys_content_t`)
    и запустить бэкап из консоли → после него `selinux.denials` — `WARN` с
    числом и примерами `restic`; подсказка называет `ausearch`. Удалить
    репозиторий, `restorecon`.
54. На ВМ-1 (Debian, без SELinux) → обе строки `OK`, `SELinux is not enforcing`.

## Часть 10. Сервер и часы (A8d-3, ЧВ6)

55. Здоровый хост → `server.reachable`, `server.tls`, `server.accepted`,
    `clock.skew` — `OK`. В консоли у агента не изменились плагины,
    репозитории и время последнего Register; `sudo journalctl -u sard-agent`
    не показывает разрыва потока.
56. `server.address` на несуществующее имя (копия конфига,
    `--config`) → `server.reachable` — `ERROR`, `no such host`; остальные
    `server.*`, `clock.skew` — `SKIP`.
57. Порт сервера закрыт межсетевым экраном (DROP) →
    `time run sudo $AG check --connect-timeout 3s` → `server.reachable` или
    `server.tls` — `ERROR` с `3s`; `real` — не больше суммы таймаутов.
58. Отозвать агента в консоли → `server.accepted` — `ERROR`,
    `AGENT_REVOKED`, подсказка `sudo sard-agent enroll --force`; `exit=1`.
    (Перерегистрировать ВМ по `host-setup.md`.)
59. Часы: `sudo timedatectl set-ntp false; sudo date -s '+3 minutes'` →
    `clock.skew` — `WARN`, расхождение около `180s`, подсказка
    `timedatectl set-ntp true`; `exit=8`. Вернуть синхронизацию.
60. Старый сервер (предыдущий выпуск без Probe) → `server.accepted` — `WARN`
    `the server is older than this agent`, `clock.skew` — `SKIP`.

## Часть 11. Секреты в выводе (все срезы)

61. `grep -F -e "$PASS_MARK" -e "$PG_MARK" -e "$S3_MARK" "$OUT/all"` → ничего.
    Повторить для вывода `sudo $AG check --json` и для
    `sudo journalctl -u sard-agent -t sard-agent --since today`.
62. Во время `sudo $AG check` — `ps -eo args | grep -F -e "$PASS_MARK" -e "$S3_MARK"`
    в цикле → ничего.

## Часть 12. Подсказка в ошибках шагов (A8d-4, ЧВ3)

63. Закрыть порт S3 (шаг 7) и запустить бэкап источника `qa-files-s3` из
    консоли → запуск `failed`; текст ошибки шага в консоли кончается
    `run `sudo sard-agent check` on the host`. Убрать правило.
64. Испортить пароль `main` (шаг 4) и запустить `qa-files` → то же. Вернуть.
65. Убрать `pg_dump` (шаг 40) и запустить `qa-pg` → то же. Вернуть.
66. Источник `files` с несуществующим каталогом → ошибка шага без этой
    строки.

## Часть 13. Документация (A8d-4, Ч30)

67. `docs/operations/agent-install.md`, `docs/operations/agent-host-setup.md`
    (после `repo add`), `docs/operator/02-install.md`, `05-agents.md`,
    `05a-storage.md` — каждый говорит выполнить `sudo sard-agent check`.
68. Свежая установка пакета на чистой ВМ → итог postinstall называет
    `sudo sard-agent check`.
69. `docs/operator/09-troubleshooting.md` — раздел с якорем на каждый
    идентификатор из шага 42 (без точек: `repositorypassword`); каждая
    ссылка `hint:` из шагов 1–60 открывается на существующий якорь.
    Раздел «Агент не подключается» не содержит ручных команд
    (`openssl s_client`, `restic cat config`, `ls -l /etc/sard/...`), только
    ссылку на check.
70. `sudo $AG check --help`, `sudo $AG repo check --help`,
    `sudo $AG repo unlock --help` → флаги, `sudo`, коды `0`, `8`, `1`, `2`;
    у unlock — что чужие блокировки не снимаются, порог 24 ч и гонка Ч6.
