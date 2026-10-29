<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-28 — PL7 (SARD-15): код выхода 3 у restic, неполный бэкап не должен выглядеть успехом

Трек QuickFix, `/quick-fix`. База — `main` f5ec8d8. Ветка сессии —
`ccr-84758809-7t4jw4` (её назначило окружение; `fix/sard-15-partial-backup`
из задачи не создавалась).

## Фаза 1 — аудит (только чтение кода)

Все строки ниже — **«полагаю» (чтение кода)**, если не сказано иное. Тесты в
фазе 1 не запускались; там, где поведение закреплено существующим тестом,
тест назван — его прогон в фазе 2.

Пути сервера — от `server/src/main/kotlin/dev/sard/server/`, консоли — от
`web/src/`.

### Агент

| # | Место | Что видит человек / что происходит | Оценка | file:line |
|---|---|---|---|---|
| A1 | files, основной снимок, код 3 | `*PartialError` + сводка; шаг FAILED, `BackupOutput` со снимком; сообщение — англ. строка «at least one source file could not be read: unreadable paths (N[, first 10]): "…"» | честно (по правилу 5); e2e FullChainTest закрепляет | `agent/internal/restic/backup.go:244-253`, `agent/internal/pluginhost/handler.go:166-171` |
| A2 | files, неполный основной снимок → отпечаток | при ошибке upload `Backup` возвращается до `withFingerprint`: у неполного снимка нет отпечатка и нет снимка `files.part=fingerprint`. Не «успех с пустым отпечатком» | честно | `agent/internal/pluginhost/source.go:115-118` |
| A3 | снимок отпечатка `files.part=fingerprint` (`--stdin`), код 3 | `restic.Backup` для stdin тоже превращает код 3 в `*PartialError`; `nameRepository` не именует репозиторий; шаг FAILED с **основным** (полным) снимком, сервер помечает его partial, сообщение «unreadable paths (…)» — про файл, которого на хосте нет | **дыра** (неверная причина; в принципе маловероятно — см. Q2) | `backup.go:188-191,244-253`, `fingerprint.go:92-99`, `handler.go:179` |
| A4 | postgresql основной снимок `--stdin`, код 3 | тот же путь: FAILED **со снимком** и partial — дамп выглядит как «неполный, но со снимком», на хосте `repo restore` его отдаст | **дыра** (задача прямо требует: stdin + код 3 = провал) | `source.go:145-153`, `backup.go:244-253` |
| A5 | postgresql снимок глобальных объектов (`backupExtra`, `--stdin`), код 3 | ошибка → `repositoryError{PartialError}`; шаг FAILED с полным основным снимком, причина «unreadable paths» | **дыра** (как A3) | `source.go:182-189` |
| A6 | самобэкап | это обычные запуски через агента плагинами postgresql и files (F6) — те же пути A1–A5 | следует A1–A5 | `server/.../selfbackup/SelfBackupPlan.kt:26-30` |
| A7 | отпечаток: нечитаемый контрольный путь | `checkControls`: состояние «unreadable: …» ≠ non-empty → FAILED до бэкапа | честно | `agent/plugins/files/fingerprint.go:148-158`, `check.go:37` |
| A8 | отпечаток: нечитаемый файл выборки SHA-256 | `hashStable` → false, файл не входит в выборку; факт `sample` = «K of M files hashed» | честно (исключается явно) | `agent/plugins/files/sample.go:166-187,207-212` |
| A9 | журнал агента на хосте | «step finished status=STEP_STATUS_FAILED» — без ID снимка и без причины | **дыра** для хоста без сервера (п. 6) | `agent/internal/executor/command.go:352` |
| A10 | `repo snapshots` / `repo restore` | неполный снимок ничем не отличается от полного | **дыра**, решение — Q4 | `agent/cmd/sard-agent/repo_snapshots_run.go`, `host_help.go:324-345` |

### Протокол

| # | Место | Оценка | file:line |
|---|---|---|---|
| P1 | `StepResult.message` — единственный носитель числа и путей; в `BackupOutput` нет ни признака partial, ни числа, ни путей. Сервер выводит partial из «не SUCCEEDED + есть вывод» и не отличает нечитаемые файлы от любой другой ошибки после снимка | **дыра** для i18n (п. 5, Q1) | `proto/sard/agent/v1/agent.proto:365-436`, `runs/ResultCheck.kt:279` |

### Сервер (отчёт субагента, выборочно перепроверено)

| # | Место | Что видит человек | Оценка | file:line |
|---|---|---|---|---|
| S1 | статус запуска | FAILED шаг → FAILED запуск | честно | `runs/RunModel.kt:27-34` |
| S2 | «первый бэкап» в чек-листе обзора | только `status = 'succeeded'`; закреплено тестом «Неуспешный шаг со снимком не отмечает первый бэкап» | честно (тест существует) | `fleet/Overviews.kt:16-18`, тест `api/OverviewApiIntegrationTest.kt:279` |
| S3 | давность (freshness), «давно не было бэкапа» | от `finished_at` последнего succeeded; эпизод закрывается только успехом | честно; отдельного теста на partial не нашёл | `freshness/FreshnessCheck.kt:24-28`, `freshness/Freshness.kt:77-91` |
| S4 | Prometheus `sard_source_last_backup_success_timestamp_seconds` | только succeeded | честно; теста на partial не нашёл | `prometheus/StateQueries.kt:22-27`, `metrics/StateGauges.kt:89` |
| S5 | счётчики `sard.run.finished{status}`, `step.results{outcome}` | partial = `failed`; отдельно не виден | честно, но неразличимо (Q5) | `agents/results/ResultsConfiguration.kt:23-32,79-88` |
| S6 | провалы подряд | partial считается провалом | честно | `notify/Notices.kt:170-176` |
| S7 | догоняющий запуск D16 | решается по моментам cron, статус не читается | неприменимо | `scheduler/Due.kt:36-45` |
| S8 | самобэкап `lastSuccessAt` | только SUCCEEDED | честно | `selfbackup/SelfBackupHealth.kt:15-16,66` |
| S9 | уведомление: причина | «Reason: » + сырая англ. строка агента (до 500 code points) даже при `sard.notify.language=ru` | **дыра** (i18n) | `notify/NoticeFormatter.kt:110-111,144-157` |
| S10 | уведомление: строка о снимке | «Снимок создан и пригоден для восстановления, но часть данных в него не попала» — а консоль неполный снимок не восстанавливает (R2 ВВ8) | **дыра** (противоречие) | `notify/NoticeWording.kt:125-126,182` |
| S11 | уведомление по расписанию: серия неполных | первый провал серии уведомляет, дальше тишина до успеха; давность (S3) потом заговорит | по ADR 0024, не дыра; отметить в документации | `notify/Notifications.kt:106-112` |
| S12 | проверка восстановления, база падения объёма | partial исключён | честно | `verify/VerificationCycles.kt:19`, `verify/Staleness.kt:22`, `runs/StepResults.kt:29-31` |

### Консоль

| # | Место | Что видит человек | Оценка | file:line |
|---|---|---|---|---|
| W1 | шаг FAILED со снимком на странице запуска | бейдж «failed», сырая англ. причина, блок снимка + предупреждение `run.partialNote` | отличим, но **дыры**: причина не на языке консоли; текст «пригоден для восстановления» противоречит блокировке; нет подсказки о правах/ссылки на документацию | `components/StepPanel.tsx:33,70-108`, `stepNotes.ts:10-11`, `locales/*.json:275` |
| W2 | список запусков, обзор, блок расписания | только бейдж «failed» — неполный неотличим от провала без снимка | **дыра** (отображение) | `components/RunsTable.tsx:84`, `components/ScheduleBlock.tsx:25-29` |
| W3 | список снимков | бейдж «неполный», `restorable=false`, причина `partial` | честно; но `source.partialWhy` = «пригоден для восстановления» — противоречие | `pages/Source.tsx:44-49,67-71`, `locales/*.json:243-244,798` |

### Итог фазы 1

Неполный бэкап **нигде не засчитан как успех** (S1–S8, S12 — по чтению кода;
S2 закреплён тестом). Дыры — в честности и внятности: A3–A5 (stdin + код 3),
A9–A10 (хост без сервера), P1/S9/W1 (причина не на языке консоли, нет числа и
путей в структурированном виде), S10/W1/W3 (текст «пригоден для
восстановления» противоречит R2 ВВ8), W2 (неполный неотличим в списках).

Открытые вопросы — в ответе владельцу (Q1–Q6); фаза 2 — после ревью.

### Решения владельца по фазе 1

Владелец принял все предложения и разделил работу: сейчас — `/quick-fix`
(только то, что не меняет поведение по спецификации), остальное — отдельным
`/ship-feature` со specifier. Хост без сервера: журнал агента сейчас, пометка
«полнота не подтверждена» в `repo snapshots`/`repo restore` — в `/ship-feature`.

## Фаза 2 — `/quick-fix` (coder → cleaner → architect)

| # | Было | Стало | Чем проверено |
|---|---|---|---|
| A3–A5 | `--stdin` + код 3 → «неполный со снимком», причина «unreadable paths» | `backupOutput.streamResult`: провал без снимка, ошибка не `ErrUnreadableSource`; дамп postgresql — FAILED без вывода; снимок отпечатка и глобальных объектов — FAILED с полным основным снимком и именем репозитория | исправлено (тесты `TestBackupFromStdinWithExitCode3*` в `agent/internal/restic`, `Test*StreamExit3*` в `agent/internal/pluginhost`) |
| A9 | «step finished» без причины и снимка | `message` и `snapshot_id` после маскирования | исправлено (`TestTheFinishedLineCarriesTheMessageAndTheSnapshotMasked`, `TestTheFinishedLineOfASucceededStepHasNoMessage`) |
| S10 | уведомление: «пригоден для восстановления» | «неполный… восстановить можно только на хосте командой repo restore» (ru, en) | исправлено (`RunNoticeFormatterTest`, `RunNotificationsIntegrationTest`); сценарии `run-notifications.feature` поправлены |
| W1, W3 | `run.partialNote`, `source.partialWhy`: «пригоден для восстановления» | тот же смысл, что в уведомлении | исправлено (строки локалей; компонентных тестов в web нет по правилам); `console-pages.feature:1238` поправлен |
| — | `docs/plugins/files.md` | что значит «неполный» и что делать | документация |

Решения по ходу:

- `itemsLine` (запасной вариант `streamExitError`) удалён по замечанию
  architect: при коде 3 restic 0.19 всегда печатает строку `exit_error`, и
  `fatalLine` уже называет пути; ветка была недостижима (покрытие 0.0%).
  Вместо неё — тест на код 3 без stderr.
- Порядок в `streamResult` (код 3 проверяется раньше ошибки потока) оставлен:
  при упавшем потоке EOF не отправляется, restic останавливается SIGTERM и
  выходит с 130, снимка нет. Порядок такой же, как в прежнем `result`.
  Если понадобится: `Unwrap` у `streamError` и пропуск ветки кода 3 при
  `errors.As(err, *streamError)`.

## Перенесено в `/ship-feature` (OQ)

- P1/S9/W1: поля `BackupOutput.unreadable_count`, `unreadable_paths` (до 10),
  аддитивно; локализованная причина с числом и путями в уведомлении и консоли,
  подсказка о правах со ссылкой на `docs/plugins/files.md`; агент N−1 —
  прежняя строка.
- Формулировка «часть данных не попала» для случая, когда основной снимок
  полный, а упал снимок отпечатка или глобальных объектов (ПГ17f) — уточнить
  вместе с P1.
- W2: пометка «снимок неполный» в списках запусков, на обзоре и в блоке
  расписания (признак в API списков, OpenAPI).
- S5: метрика неполных бэкапов в Prometheus.
- A10: `repo snapshots`/`repo restore` помечают снимок `files` без связанного
  снимка отпечатка (`files.part=fingerprint`) как «полнота не подтверждена»;
  ошибается только в сторону осторожности (снимки до V2 и с упавшим
  отпечатком тоже получат пометку).
- `fatalLine` не убирает повторы путей (было до PL7, косметика).

## Часть 2 — `/ship-feature` (specifier → coder → cleaner → architect → hardener)

Спецификация: `docs/specs/proto/partial-backup.feature` (НБ1–НБ16) и поправки
PL7-2 в `docs/specs/{agent,server,web}/*.feature`, ручная проверка —
`docs/qa/partial-backup.md`. Владелец утвердил её 2026-10-28 вместе с
рекомендованными ответами ВН1–ВН11 (`d90324b`).

| Срез | Коммит | Что | Гейт |
|---|---|---|---|
| PL7-2a | `aa00a66` | proto: `optional unreadable_count`, `unreadable_paths`; агент заполняет их, маскирует пути | proto, agent fast — PASSED; `buf breaking` зелёный |
| PL7-2b | `aba1854` | сервер: причина неполноты, REST, уведомление ru/en, `sard_run_partial_total{reason}`, OpenAPI | server fast — PASSED |
| PL7-2c | `95843c9` | консоль: пометка «неполный» в списках, нечитаемые файлы на языке консоли | web fast — PASSED |
| PL7-2d | `d5d8bce` | хост: колонка `COMPLETE` в `repo snapshots`, предупреждение `repo restore` | agent fast — PASSED (@restic с restic 0.19.1) |

Без автоматического теста (решение architect — допустимо, в QA):
- @restic «Настоящий restic с нечитаемым файлом…» — окружение работает от
  root, нечитаемый файл не сделать; путь покрыт тестами с фейком restic.
- @qa-only отображение в консоли — чистые функции покрыты Vitest, в браузере
  не проверялось.
- Сохранение после перезапуска сервера — косвенно, через чтение из базы в
  интеграционных тестах.

Окружение: один прогон гейта server упал — глобальный OOM убил исполнитель
тестов (96 контейнеров postgres, 15.7 ГБ). Я сначала принял это за
регрессию PL7 (сравнил один прогон с одним) — это было неверно: из шести
следующих прогонов HEAD пять прошли с пиком около 10 ГБ и 66–70 контейнеров,
как на базе (`d90324b`: 10.0 ГБ, 69). Каждый кешированный Spring-контекст
держит свой контейнер (до 32 на JVM, две JVM); в сбойных прогонах был лишний
Java-процесс неизвестного происхождения. Источник лишних контейнеров не
найден. Если повторится: метки `org.testcontainers.sessionId` у контейнеров и
командные строки крупных Java-процессов; корневое решение — один контейнер на
JVM с базой на контекст.

architect (1-й проход): CHANGES REQUIRED — контрактный тест тегов отпечатка
между `pluginhost` и `restore` (F1), тест порядка предупреждения Р92 (F2),
ADR (F3), CHECK `partial = (incomplete_reason IS NOT NULL)` (F4), точная
проверка stderr в интеграционном тесте restore.

OQ для specifier/владельца (не блокирует): сделать правило Р89 общим — любой
`<p>.part=fingerprint` + `<p>.main_snapshot=<id>` подтверждает снимок, — чтобы
`agent/internal/restore` не знал имени плагина `files`.
