# Сессия 2026-10-04: T2b — e2e всей цепочки бэкапа

Ветка `claude/compassionate-noether-9osu5a` (назначена окружением; в
постановке — `feat/t2b-full-chain`). База — `main` @ `2a21a0f`.

## Фаза 1 — исследование, план данных, запуск команд в контейнере агента

Два субагента только на чтение (стенд `:e2e`; спецификации и схема S7a/S8b),
ключевые места перечитаны напрямую.

### Окружение сессии
- `dockerd` поднят вручную (29.6.2, overlayfs); JDK 25 поставлен из apt
  (`openjdk-25-jdk-headless`, после `apt-get update`).
- Сборка образа сервера в облачной среде упирается в 429 Maven Central
  (OQ-132); обход прошлых сессий — `E2E_SERVER_BUILD_FLAGS` с `--network host`
  и CA прокси. В этой фазе образы не собирались.

### Проверенные факты (file:line)
- **S8b в `main`, REST работает.** `Unimplemented*Api` удалён; токены, агенты,
  источники, запуски, снимки, логи — `server/src/main/kotlin/dev/sard/server/api/*ApiImpl.kt`.
  Запуск: `POST /api/v1/sources` (`SourceInput{name, agentId, plugin,
  repositoryName, config}`, конфиг проверяется по `config_schema` из Register,
  ADR 0031) → `POST /api/v1/sources/{id}/runs` → `GET /api/v1/runs/{id}`
  (`steps[].backup{snapshotId, totalBytes, addedBytes, repositoryId, partial}`);
  `GET /api/v1/sources/{id}/snapshots`. Вход — `POST /api/v1/session`, cookie
  `sard_session`; запрос без `Origin` пропускается (`OriginGuardFilter.kt:18-41`).
  Значит, условие «SQL до S8b» уже снято: запуск можно ставить через REST сразу.
- **Токен** — уже REST: `EnrollmentTokens.create` (`test/e2e/.../EnrollmentTokens.kt:31-43`),
  токен регистрируется как секрет журнала (`:42`).
- **Регистрация** — Kotlin-клиент `AgentEnroller.enroll` (`AgentEnroller.kt:31-75`,
  «REPLACEMENT POINT (A2)»); файлы TLS кладутся в контейнер вручную
  (`AgentContainer.kt:38-42`). Используется в `AgentConnectTest`,
  `RegistrationTest`, `ResultAckSeamTest`, `StepLogRedactionTest`, `RunStepSeamTest`.
  `RegistrationTest` (`:22-39`) проверяет серверную сторону S2/S3 клиентом с
  хоста (`renewCertificate` → `UNIMPLEMENTED` / `UNAUTHENTICATED`, повтор токена →
  `StatusException`).
- **Образ агента** (`test/e2e/agent/Dockerfile:10-20`): distroless `nonroot`,
  оболочки нет; `/usr/lib/sard/{sard-agent,restic}`; `/etc/sard` — root;
  `/var/lib/sard-agent/` и `/var/cache/sard/restic/` — 65532, 0700;
  `USER 65532`; `ENTRYPOINT sard-agent`. Команды в контейнере сейчас никто не
  выполняет: файлы читаются `copyFileFromContainer`, разовые запуски —
  `AgentImage.run` (переопределённая точка входа).
- **`sard-agent enroll`** (`agent/cmd/sard-agent/enroll_flags.go:47-59`):
  `--server`, `--token`/`--token-file`, `--force`, `--config`, `--timeout`;
  пишет файлы `tls.*` конфига, каталоги должны быть доступны на запись
  (`enroll_run.go:153-158`). Коды выхода 0–7.
- **`sard-agent repo init <имя>`**, `--generate-password` (пароль 0600, без
  перезаписи), блокировка в `restic.cache_dir`, каталог не создаётся; коды
  0/1/2/4/6/7 (`docs/specs/agent/repo-init.feature:122-127,270-278`).
  `repository_id` агент сообщает в Register (`restic cat config` перед каждым
  подключением, `agent/internal/app/app.go:74-88`); пустой id сервер хранит как
  NULL. Единственный `@e2e` сценарий спецификации — «После repo init и
  перезапуска агента сервер знает repository_id» (`repo-init.feature:1390-1396`).
- **files**: конфиг `paths`, `exclude`, `one_file_system`
  (`agent/plugins/files/config.go:24-26`). Несуществующий путь верхнего уровня —
  FAILED в PREPARING без restic, `"<путь>": no such file or directory`
  (`check.go:23,121-130`). Нечитаемый файл внутри дерева — FAILED с
  `BackupOutput` (`at least one source file could not be read: unreadable paths
  (N): "<путь>"`, `restic/backup.go:58-88`; `pluginhost/handler.go:93-103`).
  `@e2e` сценариев в `files-plugin.feature` нет.
- **Сервер** (S7a + S8b): успешный бэкап → `run_steps.output`
  `{"kind":"backup",…,"repositoryId":…,"partial":false}` и строка `snapshots`;
  FAILED с корректным выводом → вывод и снимок с `partial = true`
  (`ResultCheck.kt:105-121`, `StepResults.kt:142-162`). OQ-046 фактически
  закрыт S8b.
- **`agent-enroll.feature`, `@e2e`**: 10 сценариев, один из них — структура с
  4 примерами (13 случаев): строки 184, 202, 208, 339, 499, 538, 559, 599,
  738 (`TOKEN_USED/UNKNOWN/EXPIRED/REVOKED`), 752 (`INTERNAL_RETRYABLE` —
  остановка PostgreSQL). В OQ-028 написано «8» — не сходится. Автоматизированным
  сценарий считается, если имя теста цитирует его название
  (`agent-enroll.feature:162`); IPv6, недоступный сервер, не-root — `@fake`, не `@e2e`.
- **Ожидание**: общего помощника нет; в каждом тесте своя копия `await`
  (60 с, опрос 500 мс). `ResultAckSeamTest.kt:47,128` — фиксированная пауза 5 с
  («тишина» после перезапуска).
- **Pending.kt**: `TransportExecutorPending` (условие выполнено),
  `FullChainT2Pending` (PostgreSQL и workflow — вне этапа),
  `StreamBreakT3Pending` (остаётся).

### План (черновик, на ревью)

**Запуск команд без оболочки.** Разовые контейнеры того же образа с
переопределённой точкой входа и **общими именованными томами**:
- том `state` → `/var/lib/sard-agent` (Docker заполняет пустой том
  содержимым и владельцем каталога образа — 65532, 0700): `tls/`, `repo/`
  (локальный репозиторий), `data/` (источник), `restore/`, пароль репозитория;
- том `cache` → `/var/cache/sard/restic` (блокировка `repo init`).
- Конфиг — копия в каждый контейнер (`/etc/sard/agent.yaml`, только чтение),
  `tls.*` указывают в `/var/lib/sard-agent/tls/`.

Порядок: `sard-agent enroll --token-file` (токен файлом, не в argv) →
`sard-agent repo init main --generate-password` → долгоживущий контейнер
агента на тех же томах → REST: источник и запуск → опрос `GET /runs/{id}` →
`restic restore` разовым контейнером в `restore/` → выгрузка дерева архивом
(`copyArchiveFromContainerCmd`) → сравнение на хосте. Пароль репозитория
тест не читает никогда (restic получает `--password-file`).

**Одна точка замены регистрации.** `AgentEnroller.enroll` запускает
`sard-agent enroll` в разовом контейнере на томе агента и возвращает
`agent_id` (из stdout) и том; `AgentContainer` монтирует том вместо
копирования PEM. Kotlin-клиент удаляется.

**Данные (эталон до бэкапа).** Дерево генерируется в тесте из фиксированного
зерна, эталон (путь → SHA-256 и байты) снимается при генерации, до копирования
в контейнер; копирование — tar с владельцем 65532:
- пустой файл; 1 байт; 4 КиБ; ~3 МиБ псевдослучайных (больше чанка restic);
- вложенность 3 уровня, имя с пробелом и не-ASCII;
- исключение: `cache/` (каталог) и `*.tmp`, по одному файлу каждого вида —
  в восстановленном дереве отсутствуют.

Сравнение: множество путей и типов (файл/каталог) совпадает, содержимое
каждого файла побайтово равно эталону. Контроль самого теста: юнит-тест
сравнителя (испорченный байт, лишний и пропавший файл — падает) и один
ручной прогон с порчей восстановленного файла, результат — в журнал.

**Отрицательные пути** (тот же стенд и агент, отдельные источники):
- путь из `paths` не существует → шаг `failed`, сообщение содержит путь,
  снимка нет;
- нечитаемый файл (владелец root, 0000; агент — 65532) → шаг `failed`,
  `backup.partial = true`, строка `snapshots`; restore из этого снимка даёт
  дерево, равное эталону без нечитаемого файла.

**Транспорт + исполнитель** (бывший `TransportExecutorPending`): шаг `files`
доходит до агента, прогресс переводит шаг в `running` (по `started_at`/
`bytes_processed`, а не по наблюдению фазы — фаза меняется быстрее опроса),
результат записан, надгробие `acked/<sha256(id)>.json` на месте, файла
результата нет.

**Классы (по одной установке на класс):**
1. `FullChainTest` — цепочка, отрицательные пути, транспорт + исполнитель,
   сценарий repo-init `@e2e`.
2. `AgentEnrollTest` — сценарии `@e2e` `agent-enroll.feature`, имена тестов
   цитируют названия.
3. `AgentEnrollRetryableTest` — `INTERNAL_RETRYABLE` (останавливает
   PostgreSQL стенда, поэтому отдельно).

**Общий помощник ожидания** `Await.until(what, timeout) { … }` — новые тесты;
копии в существующих тестах не трогаю (сохранить существующее).

### Ответы владельца (2026-10-04)
1. `RegistrationTest` — вариант (а): регистрация только через `sard-agent enroll`,
   Kotlin-клиент удаляется; проверка повторного токена — сценарий `TOKEN_USED`.
2. Автоматизировать все сценарии `@e2e` `enroll` (13 случаев), OQ-028 поправить.
3. Сценарий repo-init «после `repo init` и перезапуска агента сервер знает
   `repository_id`» — включить в цепочку.
4. Перезапуск посреди шага — оставить для T3 (в `Pending.kt`).
5. Сравнение — пути, типы, побайтовое содержимое; без прав, времени и симлинков.
6. Контроль теста — юнит-тест сравнителя и один ручной прогон с порчей.
7. Реестр — закрыть OQ-028, OQ-046, снять устаревшее условие OQ-045.

## Фаза 2 — регистрация через enroll, сценарии `@e2e`, транспорт + исполнитель

Ревью фазы 1 — ответы выше, «Пошёл».

### Сделано
- **Хост агента** `AgentHost`: два именованных тома (`/var/lib/sard-agent`,
  `/var/cache/sard/restic`), каждая команда — контейнер образа на них с
  программой в точке входа; готовность разового контейнера — «остановился»
  (своя `StartupCheckStrategy`, код выхода оценивает тест). Конфиг одинаков для
  всех команд, файлы TLS — на томе состояния. Тома создаёт и удаляет
  `SardEnvironment.volume()` (с метками сессии Testcontainers для Ryuk).
- **Одна точка регистрации** `AgentEnroller`: `sard-agent enroll --server
  sard-server:9090 --token-file /etc/sard/token` (токен файлом 0600, не в
  командной строке), agent_id — из строки итога «Enrolled as agent …».
  Kotlin-клиент Enroll удалён, `bcpkix` убран из `:e2e` и `docs/dependencies.md`.
  `AgentContainer.of(agent, files)` — агент на томах хоста; вызовы в
  `AgentConnectTest`, `RunStepSeamTest`, `ResultAckSeamTest`,
  `StepLogRedactionTest` поменялись только сигнатурой, проверки те же.
- `RegistrationTest` — оставлена серверная проверка «без сертификата —
  UNAUTHENTICATED» (корень берётся из рукопожатия); `renewCertificateStatus`
  перенесён в `ServerTls`.
- `SardApi` — REST от имени администратора (вход, cookie); `EnrollmentTokens`
  поверх него: `issue` (id + токен), `status`, `agentOf`, `revoke`; `expire` —
  единственная запись в БД (сдвиг `created_at`/`expires_at` в прошлое).
- `Await` — общий опрос с таймаутом для новых тестов; копии в старых не тронуты.
- `AgentEnrollTest` — 12 случаев, `AgentEnrollRetryableTest` — 1 (остановка
  PostgreSQL `docker stop`/`start`, своя установка). Имена тестов — названия
  сценариев; у структуры — «название — пример».
- `FullChainTest` — «транспорт + исполнитель»: enroll → `repo init main
  --generate-password` → агент → `POST /api/v1/sources` и `/runs` → шаг files
  `succeeded`, `started_at` есть (прогресс дошёл), `output.repositoryId` = id
  из Register, запуск `succeeded`, строка `snapshots`, надгробие
  `acked/<sha256>.json` есть, `results/` — нет. `Backups` — помощник REST и
  чтения записей сервера.
- `Pending.kt`: `TransportExecutorPending` удалён; шаг «перезапуск посреди
  шага» перенесён в `StreamBreakT3Pending`. `FullChainT2Pending` — в фазе 3.
- Спецификация `agent-enroll.feature:144-146` — пометка, что все `@e2e`
  автоматизированы и где. Реестр: OQ-028 закрыт, OQ-045 — условие снято.
  README e2e — новые классы, раздел «Хост агента и регистрация».

### Проверено (запуском)
- `make e2e-images` (флаги сборки с прокси, как в прошлых сессиях) — exit 0
  с первой попытки, 429 не было; версия образов `d081cc3`.
- `./gradlew :e2e:test` — 29 тестов в 13 классах. Первый прогон без
  `-Pe2e.version`: 3 падения проверок версии (`AgentConnectTest`,
  `AgentImageSmokeTest`, `ServerSmokeTest` ждали `dev`, образы — `d081cc3`).
  Повтор трёх классов с `-Pe2e.version=d081cc3` — зелёные. Остальные 26 —
  зелёные в первом прогоне. Новые классы: `AgentEnrollTest` 12/12 (дважды),
  `AgentEnrollRetryableTest` 1/1 (дважды: отдельно и в общем прогоне),
  `FullChainTest` 1/1 (дважды).
- В `AgentEnrollRetryableTest` при остановленной БД сервер ответил
  `INTERNAL_RETRYABLE` (код 6) — проверяется строкой в stderr.
- Контроль: из «уже использован» убрана первая регистрация — тест падает
  `expected: <3> but was: <0>`; файл восстановлен.
- `make license-check` — 675 files OK. Линтеров на `:e2e` нет (spotless и
  detekt к модулю не подключены).

### Не проверено
- `make e2e` целиком (сборка образов + тесты одной командой) и job `e2e` в
  CI — после фазы 3.

## Фаза 3 — полная цепочка, отрицательные пути, Pending.kt

Ревью фазы 2 — «Пошёл» (владелец).

### Сделано
- `SourceTree` — дерево из фиксированного зерна, эталон снимается при
  генерации (до копирования на хост и до бэкапа): пустой файл, 1 байт, 4 КиБ,
  3 МиБ + 17 байт псевдослучайных, вложенность 3 уровня, имя с пробелом и
  кириллицей; исключения — каталог `cache/` (абсолютный шаблон) и `*.tmp`;
  по запросу — нечитаемый файл (владелец root, 0000; агент — 65532).
- `TreeDiff` — сравнение по множеству путей, типу (файл/каталог) и
  содержимому; `TreeDiffTest` (5, без контейнеров).
- `AgentHost.restore` — `restic restore <snapshot> --repo … --password-file …
  --no-cache --target <новый каталог>` разовым контейнером, дерево выгружается
  `docker cp` архивом (`copyArchiveFromContainerCmd`).
- `FullChainTest` (4):
  - цепочка: агент запущен до репозитория → в Register `repository_id` пуст →
    `repo init --generate-password` → `docker restart` агента → сервер знает
    `repository_id` и `backend` из вывода команды (сценарий `@e2e`
    `repo-init.feature`) → источник с исключениями и запуск через REST → шаг и
    запуск `succeeded`, `snapshots` (`partial = false`) → restore → различий нет;
  - транспорт + исполнитель (из фазы 2, на том же дереве);
  - несуществующий путь → `failed`, сообщение `"<путь>": no such file or
    directory`, запуск `failed`, снимка нет;
  - нечитаемый файл → `failed`, сообщение называет путь, `output.partial =
    true`, `snapshots.partial = true`; restore даёт дерево, равное эталону без
    нечитаемого файла.
- `Pending.kt` — остался только `StreamBreakT3Pending`; `FullChainT2Pending`
  удалён. Его шаг «проверка восстановления сервером, `lastVerifiedRestoreAt`»
  в контракт этапа 1 не входит (нет ни действия, ни эндпоинта) — заготовки нет,
  записано в README.
- ADR 0020 — раздел «Изменения T2b» (хост агента на томах, закрытые точки
  замены, побайтовое сравнение, `Await`). Спецификация `repo-init.feature:291`
  — пометка автоматизации `@e2e`. Реестр: OQ-046 закрыт (S8b + e2e).

### Проверено (запуском)
- Новые тесты зелёные с первого прогона (`./gradlew :e2e:test` по классам).
- Контроль теста: в цепочке перед сравнением испорчен один байт
  восстановленного `nested/имя с пробелом.txt` — тест падает
  `expected: <[]> but was: <[content differs: nested/имя с пробелом.txt]>`;
  файл восстановлен (`grep -c CONTROL` = 0).
- `make e2e` (флаги сборки сервера с прокси) **дважды подряд** на коммите
  `5c4bb33`: exit 0 оба раза; 37 тестов в 13 классах, 0 упавших, 0 пропущенных
  (сводка по `test/e2e/build/test-results/test/*.xml`). Второй прогон —
  285 с вместе со сборкой образов из кэша. Время классов первого прогона:
  `FullChainTest` 50 с, `AgentEnrollRetryableTest` 50 с, `AgentEnrollTest` 39 с.
  429 не было ни разу.
- После прогона томов `sard-e2e-*` не осталось (`docker volume ls`).

### Не проверено
- Job `e2e` в CI: CI запускается только на PR и push в `main`, PR не открыт.

### Открыто
- Ручная QA `docs/qa/agent-enroll.md` и часть 2 `docs/qa/files-plugin.md`
  (OQ-045) не прогонялись; не-root через пакет и IPv6 — только `@fake`.
- Старые тесты (`RunStepSeamTest`, `ResultAckSeamTest`, `StepLogRedactionTest`,
  `AgentConnectTest`) держат свои копии `await`; `ResultAckSeamTest` — паузу
  «тишины» 5 с. Не менялись (сохранить существующее); кандидат на чистку.
