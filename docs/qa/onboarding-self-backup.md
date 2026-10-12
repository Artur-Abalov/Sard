<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# QA: онбординг — самобэкап и подтверждение копии ключей (F4b)

Сценарии: `docs/specs/server/onboarding-self-backup.feature`,
`docs/specs/web/onboarding-self-backup.feature`. Контракт К10–К17 и решения
specifier Р20–Р30 — в заголовке серверной спецификации; решения консоли
Рк9–Рк18 — в заголовке веб-спецификации. Шаг с пометкой `[OQ-NNN]` меняется
вместе с решением по этому вопросу (`docs/open-questions.md`).

СТАТУС: черновик specifier 2026-10-12, ждёт утверждения вместе со
спецификациями.

Процедура из шести частей:

- **Часть 1 — REST API на чистой установке compose**: ограничение D8, команда,
  привязка, первый бэкап, подтверждение, карточка.
- **Часть 2 — консоль**, дважды: на моках и против сервера.
- **Часть 3 — сбои**: хранилище недоступно, сосед остановлен, база недоступна,
  перезапуск сервера посреди шагов.
- **Часть 4 — смена хранилища после онбординга** `[OQ-206]`.
- **Часть 5 — обновлённая установка** `[OQ-201]`.
- **Часть 6 — секреты, журнал, документация.**

Ожидаемый результат — после «→» в каждом шаге. Любое расхождение — дефект.

Точные границы (первые бэкапы до и после привязки, одновременные
подтверждения, отметка «обновлённая» при миграции) вручную не проверяются — это
делают тесты сценариев с управляемыми часами и базой.

## Подготовка

Нужны: Docker, `curl`, `jq`, Chromium или Firefox. Команды — из корня
репозитория. Подготовка F4a (`docs/qa/onboarding-setup.md`, «Подготовка») — та
же; ниже только добавления.

```bash
make down; docker volume rm sard_postgres-data sard_sard-pki sard_sard-self-channel sard_sard-self-config sard_sard-self-state 2>/dev/null
rm -f deploy/.env; cp deploy/.env.example deploy/.env    # пароль базы и версию — по комментариям файла
sudo chgrp 10001 deploy/.env && chmod 640 deploy/.env
make up
DC="docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.build.yml --env-file deploy/.env"
API=http://localhost:8080/api/v1
QA=$(mktemp -d)
J=$QA/admin                                              # cookie администратора
get()  { curl -sS -b $J "$API/$1"; }
send() { curl -sS -i -b $J -X "$1" "$API/$2" -H 'Content-Type: application/json' "${@:3}"; }
steps() { get onboarding | jq -c '[.steps[].state, .restriction]'; }
# MinIO в сети compose — хранилище S3 для самобэкапа
docker run -d --name sard-minio --network sard_default \
  -e MINIO_ROOT_USER=sardqa -e MINIO_ROOT_PASSWORD=sardqa-secret-key-0001 \
  minio/minio server /data
docker run --rm --network sard_default --entrypoint sh minio/mc -c \
  'mc alias set m http://sard-minio:9000 sardqa sardqa-secret-key-0001 && mc mb m/sard-self'
```

Пройти мастер F4a (`docs/qa/onboarding-setup.md`, часть 1, шаги ввода кода,
подтверждения CA и шага admin) с паролем `correct-horse-battery`, сохранив
cookie `sard_session` в `$J`.

→ `make up` завершается, сервер `healthy`; `sard-minio` запущен, бакет
`sard-self` создан; `get session` → `200`.

## Часть 1. REST API

### Состояние после шага admin `[Р20]` `[Р21]`

1. `steps`
   → `["done","done","pending","upcoming","full"]`.
2. `get self-backup | jq -c '{configured, agentId, repositories, firstBackupDone, keysConfirmedAt, passwordRevealCommand}'`
   → `configured` false, `agentId` не null (сосед зарегистрирован),
   `repositories` `[]`, `firstBackupDone` false, `keysConfirmedAt` null,
   `passwordRevealCommand` null.
3. `get self-backup | jq '.plan'`
   → два элемента: `self_database` (`postgresql`, includes с именем базы и
   `postgres:5432`) и `self_keys` (`files`, includes `/var/lib/sard/pki` и
   `/etc/sard/install`, excludes `*.dump`, `*.tgz`, `*.tar`, `*.tar.gz`,
   `*.zst`). Ни `sard-db`, ни `password` в выводе нет.

### Ограничение D8 `[Р22]` `[OQ-209]`

4. `send POST enrollment-tokens -d '{}' | grep -E '^HTTP|"code"'`
   → `409`, `"code":"onboarding_incomplete"`.
5. `for r in "POST sources" "PUT sources/$(uuidgen)" "DELETE sources/$(uuidgen)" "POST sources/$(uuidgen)/runs" "PUT sources/$(uuidgen)/schedule" "POST agents/$(uuidgen)/revoke" "POST enrollment-tokens/$(uuidgen)/revoke"; do set -- $r; send $1 $2 -d '{}' | grep -oE '"code":"[a-z_]+"'; done`
   → семь раз `"code":"onboarding_incomplete"`.
6. `for p in agents sources runs overview ca enrollment-tokens self-backup agent-install onboarding; do curl -sS -o /dev/null -w "$p %{http_code}\n" -b $J $API/$p; done`
   → каждый `200`.
7. `curl -sS -o /dev/null -w '%{http_code}\n' -X POST $API/enrollment-tokens -H 'Content-Type: application/json' -d '{}'`
   (без cookie) → `401`. С `-b $J -H 'Origin: https://evil.example'` → `403`.

### Команда repo add `[Р23]` `[Р24]` `[OQ-204]` `[OQ-205]`

8. `get 'self-backup/repository-command?storage=s3&provider=aws&region=eu-central-1&bucket=acme-sard&path=prod' | jq .`
   → `repositoryName` `self`, `address` `s3:acme-sard/prod`, `commands` —
   три строки: `read -r -p "S3 access key ID: " SARD_S3_KEY_ID`; строка
   `docker compose run --rm --no-deps -u 0 --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER --cap-add SETUID --cap-add SETGID self-agent repo add`
   с `--provider aws --region eu-central-1 --access-key-id "$SARD_S3_KEY_ID"`,
   именем `self`, адресом и `--no-restart --config /etc/sard/self/agent.yaml`;
   `docker compose restart self-agent`. Нет `--secret-key`, `--password`,
   `exec`.
9. `get 'self-backup/repository-command?storage=sftp&user=backup&host=backup.example.net&port=2222&path=/srv/sard' | jq -r .address`
   → `sftp://backup@backup.example.net:2222//srv/sard`.
10. `get 'self-backup/repository-command?storage=local' | jq -r '.address, .commands[0]'`
    → `/var/lib/sard-self/self`; строка `docker compose run …` без `read`
    `[OQ-208]`.
11. Отказы — каждый `422 validation_failed` с полем:
    `bucket=acme;reboot` → `bucket`; `endpoint=https://AKIA:secret@minio.example.net` (с
    `provider=custom`) → `endpoint`; `path=$(id)` → `path`; `storage=gcs` →
    `storage`; `port=70000` (sftp) → `port`.
    Пример: `curl -sS -b $J "$API/self-backup/repository-command?storage=s3&provider=aws&region=eu-central-1&bucket=acme%3Breboot" | jq -c '{code, errors}'`.
12. `get self-backup | jq .configured` → `false` (запрос команды ничего не записал).

### Репозиторий от соседа и привязка `[OQ-203]`

13. Получить команду для своего MinIO:
    `get 'self-backup/repository-command?storage=s3&provider=custom&endpoint=http://sard-minio:9000&bucket=sard-self' | jq -r '.commands[]' > $QA/cmd.sh; cat $QA/cmd.sh`
    → три строки, адрес `s3:http://sard-minio:9000/sard-self`.
14. В терминале, из каталога `deploy/` (там compose-файл с сервисом
    `self-agent`; при сборке из исходников добавить `-f docker-compose.build.yml`
    к `docker compose` в строках), выполнить строки `$QA/cmd.sh` по одной: на
    вопрос — `sardqa`; на вопросы `repo add` о секретном ключе — дважды
    `sardqa-secret-key-0001` (без эха).
    → `repo add` печатает создание репозитория и просьбу сохранить копию
    пароля; `restart self-agent` завершается.
15. `for i in $(seq 1 30); do get self-backup | jq -c '.repositories'; sleep 2; done | tail -1`
    → `[{"name":"self","backend":"s3","repositoryId":"<64 hex>","local":false}]`.
16. `docker inspect $( $DC ps -q self-agent ) --format '{{.HostConfig.CapDrop}} {{.HostConfig.CapAdd}}'`
    → `[ALL] []`.
17. `send PUT self-backup/repository -d '{"repositoryName":"self","location":"s3:https://AKIA:x@sard-minio:9000/sard-self"}' | grep -E '^HTTP|"code"'`
    → `422`, `validation_failed` у `location`.
18. `send PUT self-backup/repository -d '{"repositoryName":"self","location":"s3:http://sard-minio:9000/sard-self"}' | head -1`
    → `200`. `steps` → `["done","done","pending","upcoming","full"]`.
    `get self-backup | jq -r .passwordRevealCommand`
    → `docker compose exec self-agent sard-agent repo password self --reveal --config /etc/sard/self/agent.yaml`.

### Расписание и первый бэкап `[Р20]`

19. `K=$(get self-backup | jq -r '.sources[] | select(.role=="self_keys") | .sourceId'); send PUT sources/$K/schedule -d '{"cron":"30 1 * * *","timezone":"Europe/Moscow","enabled":true}' | head -1`
    → `200` (расписание системного источника разрешено до завершения).
20. `send POST self-backup/keys-confirmation -d '{"copiedOutsideServer":true}' | grep -E '^HTTP|"code"'`
    → `409`, `first_backup_pending`.
21. `send POST self-backup/runs -d '' | sed -n '/^{/p' | jq -c '[.runs[] | {role, started}]'`
    → `[{"role":"self_database","started":true},{"role":"self_keys","started":true}]`.
22. Ждать: `for id in $(get self-backup | jq -r '.sources[].lastRun.id // empty'); do get runs/$id | jq -r .status; done`
    повторять, пока оба не завершатся.
    → оба `succeeded`; `get self-backup | jq .firstBackupDone` → `true`;
    `steps` → `["done","done","done","pending","full"]`.

### Карточка и подтверждение `[Р28]` `[OQ-202]`

23. `get self-backup/recovery-card | jq .`
    → ровно поля `repositoryName` `self`, `backend` `s3`, `location`
    `s3:http://sard-minio:9000/sard-self`, `repositoryId` (как в шаге 15),
    `caFingerprint` (как `get ca | jq -r .fingerprint`), `sardVersion` (как
    `get status | jq -r .version`), `generatedAt` (сейчас), `recoveryProcedure`
    (ссылка на `docs/operator/06-data-and-backup.md`).
24. В каталоге `deploy/`: выполнить `passwordRevealCommand` из шага 18.
    → на stdout — пароль; на stderr — предупреждение, что пароль на экране.
    Значение ни в одном ответе API шагов 1–23 не встречалось (проверить
    `grep` по сохранённым выводам, если сохранялись).
25. `send POST self-backup/keys-confirmation -d '{"copiedOutsideServer":false}' | grep -E '^HTTP|"code"'`
    → `422`, `validation_failed`.
26. `send POST self-backup/keys-confirmation -d '{"copiedOutsideServer":true}' | head -1`
    → `204`. `steps` → `["done","done","done","done","none"]`.
27. `send POST enrollment-tokens -d '{}' | head -1` → `201`.
28. Повтор шага 26 → `204`; `$DC logs server | grep -c 'Onboarding completed'` → `1`.
29. `$DC logs server | grep 'keys_confirmed completed'`
    → одна строка с адресом клиента, `repository=self` и `repository_id=`.

## Часть 2. Консоль

Дважды: на моках (`cd web && VITE_API_MOCKS=1 VITE_MOCK_ONBOARDING=1 npm run dev`,
затем `VITE_MOCK_SELF_BACKUP_FAIL=1`, затем `VITE_MOCK_ONBOARDING=upgraded`) и против
сервера (чистая установка, Подготовка заново).

1. Пройти шаги кода, CA и администратора.
   → после шага администратора консоль остаётся на `/setup` и показывает шаг
   самобэкапа; список шагов: CA и администратор выполнены, самобэкап текущий,
   копия ключей — следующий; метки «скоро» нет.
2. Открыть `/agents` вручную. → консоль возвращает на `/setup` `[Рк9]`.
3. Шаг самобэкапа. → выбрано S3; варианты AWS, Backblaze B2, свой адрес; SFTP;
   локальное. Ни одного поля для ключа, идентификатора ключа или пароля; текст
   говорит, что ключи вводятся в терминале сервера.
4. Выбрать «Локальное». → предупреждение «понимаю, что бэкап лежит на этой же
   машине и погибнет вместе с ней» с отметкой; кнопка дальше неактивна до
   отметки. Вернуться к S3.
5. S3, свой адрес: `http://sard-minio:9000`, бакет `acme;x` → «Показать команду».
   → ошибка у поля бакета, команды нет. Бакет `sard-self`.
   → блок из трёх строк, кнопка копирования копирует их построчно.
6. Выполнить команду в терминале (как часть 1, шаг 14; против свежей установки).
   → без действий в браузере мастер в течение минуты переходит к составу и
   расписанию; пока ждёт — «ждём репозиторий от sard-self» и подсказка
   `docker compose logs self-agent`; кнопки «Пропустить» нет.
7. Состав. → элементы `plan` как в ответе сервера; предупреждение, что ключ CA
   в бэкапе защищён только паролем репозитория.
8. Расписание: время 01:30, пояс `Europe/Moscow`, сохранить.
   → `get sources/<id>/schedule` обоих системных источников — `30 1 * * *`,
   `Europe/Moscow`.
9. «Запустить первый бэкап». → прогресс базы и ключей; после двух успехов —
   шаг копии ключей. (На моках с `VITE_MOCK_SELF_BACKUP_FAIL=1`: у базы текст
   ошибки шага и «Повторить», нет «Пропустить» и «Далее»; «Повторить» →
   успех.)
10. Шаг копии ключей. → команда показа пароля с кнопкой копирования, текст: без
    пароля репозитория и доступа к хранилищу восстановление невозможно, сервер
    пароль не знает; «Завершить» неактивна.
11. Ссылка на карточку. → страница с восемью полями; «Печать» → в PDF только
    карточка, без навигации и баннеров; «Скачать .txt» → файл с теми же
    значениями; в файле нет `sardqa-secret-key-0001` и пароля шага 24 части 1.
12. Закрыть вкладку; `$DC restart server`; открыть консоль, войти.
    → `/setup`, шаг копии ключей `[переживает перезапуск]`.
13. Отметка «копия … сохранена вне этой ВМ» → «Завершить».
    → главная страница; `/agents`, «Токены» доступны; выпуск токена проходит.
14. Открыть `/setup`. → переход на `/`.
15. Переключить RU ↔ EN на шагах 3, 4 и карточке. → все надписи переводятся,
    команды и значения сервера — нет; ключей i18n на экране нет.
16. Моки `VITE_MOCK_ONBOARDING=upgraded` `[OQ-201]`. → обычная консоль, на
    каждой странице баннер «Настройте самобэкап» со ссылкой на `/setup`;
    выпуск токена — сообщение `onboarding_incomplete` со ссылкой на `/setup`.

## Часть 3. Сбои

1. Чистая установка, мастер F4a, команда и привязка, как часть 1 шаги 13–18.
   `docker stop sard-minio`; `send POST self-backup/runs -d ''`.
   → хотя бы один запуск `failed` с непустым сообщением шага (restic не достучался
   до хранилища); `steps` → самобэкап `pending`. В консоли — текст ошибки и
   «Повторить».
2. `docker start sard-minio`; повтор `send POST self-backup/runs -d ''`.
   → оба `succeeded`; самобэкап `done`.
3. Ожидание репозитория при остановленном соседе: свежая установка, мастер F4a,
   `$DC stop self-agent`, запросить команду.
   → команда собирается (`repositories` из последнего Register); мастер ждёт
   и показывает подсказку `docker compose logs self-agent`.
4. Без соседа (`SARD_SELF_DIR` пуст, запуск jar вне compose как в
   `docs/qa/self-agent.md`, шаг 52) `[OQ-207]`: пройти мастер F4a, затем
   `get 'self-backup/repository-command?storage=local'`.
   → `422 self_agent_missing`; `steps` → самобэкап `pending`, `full`; консоль
   объясняет, что агента sard-self нет.
5. База недоступна: `$DC stop postgres`; `send POST enrollment-tokens -d '{}'`
   и `send POST self-backup/keys-confirmation -d '{"copiedOutsideServer":true}'`.
   → оба `503 unavailable`. `$DC start postgres` → `steps` как до остановки.
6. `$DC restart server` на каждом из состояний: без привязки; привязан без
   успехов; первые бэкапы прошли; онбординг завершён.
   → `steps` после перезапуска такие же, как до.

## Часть 4. Смена хранилища после онбординга `[OQ-206]`

1. После части 1: команда для `storage=local` → `repositoryName` `self-2`
   `[OQ-210]`. Выполнить её в терминале.
2. `send PUT self-backup/repository -d '{"repositoryName":"self-2","confirmLocalStorage":true}' | head -1`
   → `200`. `get self-backup | jq -c '{firstBackupDone, keysConfirmedAt, warnings}'`
   → `false`, `null`, `warnings` содержит `local_storage` и `keys_unconfirmed`.
   `steps` → все `done`, `none`; выпуск токена — `201`.
3. Подтверждение → `409 first_backup_pending`. `send POST self-backup/runs -d ''`,
   дождаться двух успехов, подтверждение → `204`; `keys_unconfirmed` нет.
4. В консоли — баннер F6 о локальном хранилище и (до шага 3) о
   неподтверждённой копии ключей со ссылкой на `/settings`; раздел «Самобэкап»
   в настройках повторяет экраны шагов 3 и 4.

## Часть 5. Обновлённая установка `[OQ-201]`

Выполнимо, только когда есть выпущенная версия без F4b (0.1.0-beta.N, OQ-145).

1. Поднять compose этой версии, пройти её мастер, зарегистрировать обычный
   агент Y, создать источник S с расписанием.
2. Обновить образы до версии с F4b (`docs/operator/07-upgrade.md`).
   → `steps` → `["done","done","pending","upcoming","new_agents"]`.
3. `send POST enrollment-tokens -d '{}'` → `409 onboarding_incomplete`. Агент Y
   online, расписание S срабатывает; изменение S → `200`.
4. Действующий токен, выпущенный до обновления: `sard-agent enroll` на новом
   хосте → отказ с причиной `ONBOARDING_INCOMPLETE`; токен в API остаётся
   неиспользованным.
5. Консоль — обычная, баннер «Настройте самобэкап» на каждой странице.
6. Пройти шаги 3 и 4 → `none`; `enroll` тем же токеном проходит.

## Часть 6. Секреты, журнал, документация

1. `$DC logs server 2>&1 | grep -cE 'sardqa-secret-key-0001|<пароль шага 24>'`
   → `0`.
2. В браузере после части 2: адрес, история, localStorage, sessionStorage,
   IndexedDB → ни секрета хранилища, ни пароля репозитория.
3. `docs/operator/02-install.md` описывает шаги самобэкапа и копии ключей
   (команда `repository-command`, `self-agent repo add`, `repo password`,
   `keys-confirmation`, `onboarding_incomplete`); `07-upgrade.md` —
   `new_agents` и `ONBOARDING_INCOMPLETE`; `09-troubleshooting.md` — все коды
   F4b; `06-data-and-backup.md` — мастер и карточку восстановления.
   → всё на месте, команды документации совпадают с ответами сервера.

## Уборка

```bash
docker rm -f sard-minio; make down; rm -rf "$QA"
```
