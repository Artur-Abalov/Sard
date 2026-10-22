<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# QA: ключи restic — ключ верификатора и ключ восстановления (V3)

Спецификация: `docs/specs/agent/restic-keys.feature`,
`docs/specs/proto/restic-keys.feature`, `docs/specs/server/restic-keys.feature`,
`docs/specs/web/restic-keys.feature`.

Статус: черновик, вместе со спецификацией ждёт утверждения владельцем (ответы
на вопросы В1–В10, ВС1 получены 2026-10-21).

## Стенд

- ВМ-источник SRC: Debian 12 или Ubuntu 24.04, агент из пакета, зарегистрирован
  токеном роли source, имя хоста `src1`.
- ВМ-верификатор VRF: Oracle Linux 9, SELinux enforcing (`getenforce` →
  `Enforcing`), агент из пакета, зарегистрирован токеном роли verifier.
- Хранилище S3 (стенд Garage F2 или MinIO) с бакетом `acme`; два ключа: `RW`
  (чтение и запись) и `RO` (только чтение того же бакета).
- Сервер Sard с завершённым онбордингом для шагов 60–66 — отдельная чистая
  установка.
- Перед каждым разделом: `sudo journalctl -t sard-agent --since now` в
  отдельном терминале — смотреть строки аудита.

Во всех шагах «пароль не виден» значит: строки пароля нет ни в выводе
`journalctl -u sard-agent`, ни в `journalctl -t sard-agent`, ни в
`/etc/sard/agent.yaml`, ни в `/etc/sard/agent.d/`, ни в
`/var/lib/sard-agent/executor/` (проверка: `sudo grep -rF '<пароль>' …` ничего
не находит).

## A. Ключ восстановления при создании репозитория (SRC)

1. `sudo sard-agent repo add base s3:https://<garage>/acme/base --access-key-id RW`
   в терминале, ввести секретный ключ `RW`.
   Ожидается: код 0; после init stdout печатает пароль ключа восстановления
   один раз и «this password is stored nowhere else»; затем совет хранить ключ
   вне инфраструктуры. Записать пароль как `REC`.
2. Проверить «пароль `REC` не виден».
   Ожидается: grep ничего не находит.
3. `sudo sard-agent repo key list base`.
   Ожидается: две строки: ключ хоста (PURPOSE `-`, CURRENT `*`) и ключ
   `recovery`, HOST `src1`; stderr пуст.
4. Убедиться, что в бакете нет префикса `base2`, затем
   `sudo sard-agent repo add base2 s3:https://<garage>/acme/base2 --access-key-id RW --secret-key-from-file /root/rw.key < /dev/null | cat`.
   Ожидается: код 2, `RECOVERY_KEY_OUTPUT_MISSING`, называет
   `--recovery-key-file` и `--no-recovery-key`; в бакете нет `base2`;
   `/etc/sard/agent.d/repo-base2.yaml` нет.
5. Повторить шаг 4 с `--recovery-key-file /root/base2.rk`.
   Ожидается: код 0; `stat -c '%U %a' /root/base2.rk` → `root 600`; в stdout нет
   пароля; `restic -r s3:…/base2 --password-file /root/base2.rk snapshots`
   (с окружением RW) завершается кодом 0.
6. Повторить шаг 4 для `base3` с `--no-recovery-key`.
   Ожидается: код 0; stderr предупреждает о потере бэкапов вместе с хостом; в
   `journalctl -t sard-agent` строка «repository recovery key skipped» с
   `base3` и вашим пользователем.

6а. В `/etc/sard/agent.yaml` добавить репозиторий `init1` (локальный путь
   `/srv/init1`, `password_file /etc/sard/secrets/restic-init1.pass`), затем
   `sudo sard-agent repo init init1 --generate-password` в терминале.
   Ожидается: код 0; после init пароль ключа восстановления один раз;
   `sudo sard-agent repo key list init1` показывает ключ `recovery`.
6б. Добавить `init2` так же и выполнить
   `sudo sard-agent repo init init2 --generate-password < /dev/null | cat`.
   Ожидается: код 2, `RECOVERY_KEY_OUTPUT_MISSING`; каталог `/srv/init2` не
   содержит репозитория, файла пароля нет.

## B. Ключ верификатора (SRC)

7. `sudo sard-agent repo key add base --for verifier --label vrf-1`.
   Ожидается: код 0; пароль один раз; stdout содержит
   `sudo sard-agent repo add base s3:https://<garage>/acme/base --password-from-file`
   и совет использовать учётку только на чтение (`--access-key-id`). Записать
   пароль как `VK`.
8. `sudo sard-agent repo key add base --for verifier --label vrf-1`.
   Ожидается: код 2, `KEY_EXISTS`, подсказка `--label` и `--if-missing`; key
   list не изменился.
9. То же с `--if-missing`.
   Ожидается: код 0, «unchanged», пароль не печатается, строки аудита нет.
10. `sudo sard-agent repo key add base --for verifier --label vrf-2 | cat`.
    Ожидается: код 2, `KEY_OUTPUT_MISSING`, подсказка `--to-file`; новых ключей
    нет.
11. `sudo sard-agent repo key add base --for verifier --label vrf-2 --to-file /root/vrf2.key`.
    Ожидается: код 0; служба не перезапускалась (`systemctl show -p
    ActiveEnterTimestamp sard-agent` не изменился); `stat -c '%U %G %a'` → `root root 600`; в stdout нет
    пароля; повтор той же команды с `--label vrf-3` и тем же путём — код 2,
    `KEY_FILE_EXISTS`, файл не изменён.
12. `sard-agent repo key add base --for recovery` без sudo.
    Ожидается: код 2, `PRIVILEGES_REQUIRED` и готовая команда с `sudo`.
13. `sudo sard-agent repo key add base --for other`.
    Ожидается: код 2, `KEY_LABEL_REQUIRED`.

## C. Список и предупреждения (SRC)

14. `sudo -u sard-agent sard-agent repo key list base --json | jq .` <!-- allow-sudo-u: проверка, что пользователь службы может прочитать список ключей -->
    Ожидается: `keys` — 4 ключа (хост, recovery, vrf-1, vrf-2) с полными id,
    `current: true` только у ключа хоста; `warnings: []`.
15. На тестовом репозитории `base3` (без ключа восстановления):
    `sudo sard-agent repo key list base3`.
    Ожидается: код 0; stderr содержит `SINGLE_KEY` и `NO_RECOVERY_KEY` с
    командой `sudo sard-agent repo key add base3 --for recovery`.
16. `sudo sard-agent repo check base3; echo $?`.
    Ожидается: строка `repository.keys` — WARN «no recovery key; only one key»,
    hint с командой key add; код 8.
17. `sudo sard-agent check`.
    Ожидается: `repository.keys` для `base` — OK «… recovery key present», для
    `base3` — WARN.

## D. Верификатор (VRF)

18. Скопировать `VK` на VRF в `/root/vk` (`install -m 600`).
    `sudo sard-agent repo add base s3:https://<garage>/acme/base --access-key-id RO --password-from-file /root/vk`.
    Ожидается: код 0; нет `STORAGE_ACCESS_DENIED` и нет предупреждения о
    записи; `ls -Z /etc/sard/secrets/restic-base.pass` — метка каталога secrets.
19. На VRF повторить подключение под другим именем ключом `RW`:
    `sudo sard-agent repo add base-rw … --access-key-id RW --password-from-file /root/vk`.
    Ожидается: код 0; stderr «these storage credentials can write» и
    объяснение. Затем `sudo sard-agent repo remove base-rw`.
20. На VRF: `sudo sard-agent repo add nope s3:https://<garage>/acme/empty --access-key-id RO --password-from-file /root/vk`.
    Ожидается: код 2, `REPOSITORY_NOT_INITIALIZED`; в бакете нет `empty`.
21. На VRF: `sudo sard-agent repo key add base --for recovery`,
    `sudo sard-agent repo key remove base <любой id> --yes`,
    `sudo sard-agent repo init base --generate-password`.
    Ожидается: каждая — код 2, `VERIFIER_READ_ONLY`.
22. На VRF: `sudo sard-agent repo key list base`.
    Ожидается: код 0; CURRENT `*` у ключа `verifier vrf-1`.
23. `sudo systemctl restart sard-agent` на VRF; в консоли открыть карточку
    агента VRF.
    Ожидается: репозиторий `base` с тем же repository_id, что у SRC; в
    `journalctl -u sard-agent` нет `VK`.
24. `sudo ausearch -m avc -ts recent | grep sard` на VRF.
    Ожидается: пусто.

## E. Отзыв ключей (SRC)

25. `sudo sard-agent repo key list base` — записать id `vrf-2` как `ID2`.
    `sudo sard-agent repo key remove base ID2`, ответить `no`.
    Ожидается: код 2, `CONFIRMATION_REJECTED`; ключ на месте.
26. Повторить, ответить `yes`.
    Ожидается: перед вопросом показаны id, `verifier`, `vrf-2`, `src1`, время;
    код 0; stdout «removing a key does not change the repository master key …
    to fully revoke access create a new repository»; строка аудита «repository
    key removed» с `ID2`.
27. `restic -r s3:… --password-file /root/vrf2.key snapshots` (с окружением RW).
    Ожидается: ошибка неверного пароля.
28. `sudo sard-agent repo key remove base <id ключа хоста> --yes`.
    Ожидается: код 2, `KEY_IN_USE` с объяснением.
29. На `base3` (один ключ): `sudo sard-agent repo key remove base3 <id> --yes`.
    Ожидается: код 2, `LAST_KEY`.
30. `echo | sudo sard-agent repo key remove base <id vrf-1>`.
    Ожидается: код 2, `CONFIRMATION_REQUIRED`, подсказка `--yes`; ключ на месте.
31. `sudo sard-agent repo key remove base deadbeef --yes`.
    Ожидается: код 0, «nothing to remove».
32. После шага 26 на VRF выполнить `sudo sard-agent repo snapshots base`.
    Ожидается: работает (удалён `vrf-2`, а не `vrf-1`). Удалить `vrf-1` на SRC
    и повторить на VRF — `WRONG_PASSWORD`.

## F. Самобэкап (чистая установка сервера)

33. Пройти мастер до шага 4 самобэкапа (F4b) с хранилищем S3.
    Ожидается: шаг 4 объясняет, что нужны ключ восстановления (или пароль
    хоста) и доступ к хранилищу; первой показана команда
    `docker compose run … self-agent repo key add self --for recovery --if-missing --config /etc/sard/self/agent.yaml`
    с кнопкой копирования; ниже — команда пароля хоста.
34. Выполнить команду на машине сервера от root.
    Ожидается: код 0; пароль ключа один раз (или «unchanged», если ключ
    восстановления уже выдан при `repo add` шага 3).
35. Выполнить её второй раз.
    Ожидается: код 0, «unchanged».
36. Отметить «ключ восстановления и доступ к хранилищу сохранены вне этой ВМ»,
    «Завершить».
    Ожидается: онбординг завершён, как в `docs/qa/onboarding-self-backup.md`.
37. Открыть карточку восстановления, «Скачать .txt».
    Ожидается: текст содержит строку о ключе восстановления (или пароле хоста)
    и доступе к хранилищу; ни пароля, ни учётных данных в нём нет.
38. В инструментах разработчика браузера просмотреть все ответы `/api/v1/self-backup*`.
    Ожидается: ни один не содержит пароль из шага 34.

## G. Признак на сервере и баннер (SRC, VRF, консоль обычной установки)

40. На SRC подключить `base4` (новый S3-префикс) с `--no-recovery-key`
    (команда перезапускает службу). Открыть консоль.
    Ожидается: в течение минуты на каждой странице баннер «нет ключа
    восстановления» с числом 1; страница репозиториев показывает id `base4`,
    держателя `src1`, отметку «нет ключа» и команду
    `sudo sard-agent repo key add base4 --for recovery`.
41. Выполнить эту команду на SRC.
    Ожидается: код 0, пароль один раз, stdout «restarted»; в течение минуты
    баннер пропадает, `base4` — «есть ключ».
42. `sudo sard-agent repo key remove base4 <id ключа recovery> --yes`.
    Ожидается: код 0, «restarted»; в течение минуты баннер возвращается.
43. Карточка агента SRC.
    Ожидается: у каждого репозитория отметка признака; `base` — «есть ключ».
44. Карточка агента VRF.
    Ожидается: блок «ключ верификатора» по репозиториям SRC, которых VRF не
    держит (`base4`): команда
    `sudo sard-agent repo key add base4 --for verifier --label vrf-<8 знаков id VRF>`
    и шаблон `sudo sard-agent repo add base4 <address> --password-from-file <file>`.
    Выполнить команду на SRC: напечатанная команда `repo add` совпадает с
    шаблоном, кроме адреса и файла.
45. В инструментах разработчика просмотреть ответы `/api/v1/repositories` и
    `/api/v1/agents/*`.
    Ожидается: нет паролей, id ключей и меток — только `recoveryKey`.
46. Остановить агент SRC, подменить restic недоступным хранилищем (отключить
    сеть к Garage на SRC), запустить агент.
    Ожидается: агент подключается; в `journalctl -u sard-agent` строка WARN с
    именем репозитория без учётных данных; консоль показывает признак
    «неизвестно», баннер не считает такие репозитории. Вернуть сеть.

## H. Документация

47. Открыть раздел документации оператора о ключах restic.
    Ожидается: описаны три команды, хранение ключа восстановления, учётка
    только на чтение для верификатора и то, что удаление ключа не меняет
    мастер-ключ — полная смена доступа означает новый репозиторий.
