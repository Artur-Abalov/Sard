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

### Вопросы владельцу (трудные первыми)

См. сообщение в чате; ответы — ниже, после ревью.
