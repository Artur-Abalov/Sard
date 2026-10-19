<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-19 — V4: сервер проверки восстановления (очередь, расписание, выдача)

Ветка `ccr-44680043-8al2ss`: её назначила сессия. В задаче указана
`feat/v4-verification-server`, переименовать можно по решению владельца. Это
продуктовая возможность: после контрольной точки 1 работа идёт по
`/ship-feature` (specifier → coder → cleaner → architect → hardener).

## Фаза 1 — исследование

«Проверил» — видел в коде (file:line). «Полагаю» — не проверено.

### Чего нет в репозитории (проверил)

- `sard-restore-verification.md` и `docs/spikes/v0-restore-verification.md`:
  нет ни в дереве, ни в истории (`git log --all -- '*restore-verification*'`
  пусто). Копию в `docs/design/` положить нечем. Цифры V0 (запас диска,
  `restic check` с учёткой только на чтение) учесть нельзя — параметры
  остаются настраиваемыми, значения по умолчанию ниже помечены как мои.
- Таблицы `restore_verifications` нет; есть только цель её FK —
  `snapshots (tenant_id, id, source_id)` (`V202609301800__results.sql:65-66`).
- На агенте нет измерения свободного места и определения Docker/Podman;
  `agent/internal/sandbox/sandbox.go` — заглушка (`ErrNotImplemented`).

### Что есть (проверил)

| Тема | Факт | Где |
|---|---|---|
| `ACTION_VERIFY` | `= 3`, сервер маппит его в обе стороны; VERIFY-шаги нигде не создаются | `agent.proto:379`; `DispatchParts.kt:68`; `RegisterRequests.kt:21` |
| Ключи верификатора | Register уже несёт `RepositoryInfo.repository_id` (restic id, не секрет) | `agent.proto:418-429`; `app/app.go:87-99` |
| Плагины проверки | `Plugin.actions` содержит VERIFY, только если плагин реализует `sdk.Verifier`; встроенные (files, postgresql) — нет | `pluginhost/handler.go:67-72`; `sdk/plugin.go:135-140` |
| Исполнение VERIFY сегодня | restore в `<state_dir>/restore/<command_id>` → `Verify` → `VerifyOutput{snapshot_id, checks}` с одной проверкой | `pluginhost/handler.go:163-182` |
| `VerifyOutput` | `snapshot_id=1, checks=2`; стадий и уровня нет | `agent.proto:326-338` |
| N−1 | Незнакомое сообщение сервера агент молча пропускает; незнакомое действие — `REJECTED` | `transport.go:362-377`; `executor/command.go:128-145` |
| Роль | `AgentRole.allows`: verifier — только VERIFY; бэкап на verifier отвергается во всех слоях | `RunModel.kt:94-109`; `AgentOffer.kt:314-356`; `StepDispatcher.kt:195,264` |
| Машина | `agents.machine_id` (64 hex), `MachineAgents.withMachineId` — без вызовов, оставлен для V4 | `V202610141200__agent_role.sql:20-22`; `fleet/MachineAgents.kt` |
| Выбор агента | Нет: `run_steps.agent_id` = `source.agentId` при создании запуска | `runs/Runs.kt:204` |
| D6 | `runs_active_source_key ON runs (tenant_id, source_id) WHERE status IN ('queued','dispatched','running')` | `V202609301200__runs.sql:62-63` |
| Триггер | `Trigger.VERIFICATION` уже есть, уведомления о таких запусках отключены | `RunModel.kt:66`; `notify/Notifications.kt:81` |
| Снимок | `snapshots`: `snapshot_id, repository_id, total_bytes, agent_id, partial, forgotten_at` | `V202609301800__results.sql:51-73` |
| Потеря шага | `lost_deadline`, `StepDispatcher.tick` → `LOST`; окно = 2 × heartbeat | `StepTransitions.kt:39`; `DispatchConfiguration.kt:44-58` |
| Расписание | `Scheduler`: `for update skip locked`, `Clock` внедряется | `scheduler/Scheduler.kt:29-31,122` |
| Уведомления | Нет шины: планировщик над таблицами-субъектами; эпизоды с частичным уникальным индексом «один открытый» | `notify/Deliveries.kt`; `V202610121200__source_freshness.sql` |
| `lastVerifiedAt()` | Единственный потребитель — `GET /status` (`lastVerifiedRestoreAt`); в источнике и обзоре поля нет | `api/StatusController.kt:26,34,39` |
| ADR 0013 | `restore_verifications`: snapshot/source FK, step, verifier, `key_holders`, `status IN ('passed','failed')`, `checks`, `verified_at` | `0013-multitenancy-and-schema.md:195-204` |
| e2e | Настоящие агенты в контейнерах; стенд-образ с тегом `e2e` и плагином `e2e-slow` | `test/e2e/.../T3Agent.kt`; ADR 0036 |

Полагаю: до V5 в боевой сборке ни один агент не объявит плагин с VERIFY, и
каждая проверка будет заканчиваться «нет верификатора: нет плагина». Это
честное состояние, но его стоит показать явно (вопрос 3).

### Предложение к контрольной точке 1

Подробности — в ответе владельцу в сессии. Кратко:

- Доставка через существующие `runs`/`run_steps` (`trigger = verification`,
  шаг VERIFY на верификаторе): dispatch S6a, прогресс, результаты S7a и потеря
  FXs переиспользуются. `runs_active_source_key` пересоздаётся с
  `AND trigger <> 'verification'`, чтобы проверка не блокировала бэкап (D6).
- Две таблицы: `verification_jobs` (очередь, одно открытое на источник) и
  `restore_verifications` (история, статусы V-D10).
- Расписание цикла — своя таблица на тенант, SKIP LOCKED как у F3a.
- proto: `RegisterRequest.verifier = 11`, `Heartbeat.verifier = 3`,
  `StepProgress.stage = 8`, `VerifyOutput.stages/level/verdict/...`.

Решения владельца внесу сюда после ревью.

### Решение владельца 1 (2026-10-19)

1. Доставка — через `runs`/`run_steps` (`trigger = verification`, шаг VERIFY на
   верификаторе). `runs_active_source_key` пересоздаётся с
   `AND trigger <> 'verification'`; проверки исключаются из свежести (F6) и из
   истории бэкапов в REST.
2. Дизайн-документ владелец прислал текстом; копия —
   `docs/design/sard-restore-verification.md` (раздел про `ClaimJob` заменён
   V-D8, см. пометку в начале копии). Следствия — в «Решении владельца 2».
3. e2e для V4 не делается: без исполнителя V5 он ничего не доказывает. Пункт 10
   стратегии проверки снят; поведение проверяется серверными тестами с
   фейковым агентом на стриме (`StreamIntegrationSupport`).
4. Всё, что зависит от V5, — заглушка: агент сообщает свободное место и
   песочницу как «неизвестно», `pluginhost` не меняется. Неизвестное место не
   исключает верификатор, а идёт в причины как предупреждение.
5. `/status` — самая старая из последних успешных проверок по источникам с
   включённой проверкой; в DTO источника — `lastVerifiedAt`,
   `lastVerifiedLevel`, `verificationStatus`.
6. Доставка уведомлений откладывается до V6. Субъекты событий (пометка
   перехода в записи проверки, эпизоды «не проверялся N дней») остаются в V4 —
   подтверждено владельцем.
7. Значения по умолчанию приняты: проверка включена; цикл 04:30 по поясу
   установки, срок задания — до следующего цикла; таймаут шага 6 ч; место =
   `total_bytes` × 2 + 1 ГиБ; N = 7 дней; одна проверка на верификатор, выбор —
   меньше проверок за 24 ч, затем id; `config_json` источника уходит как есть.

### Решение владельца 2 (2026-10-19): дизайн-документ и что из него следует

- Стадия несёт уровень, к которому относится: 0 — подготовка и уборка (сверка
  хеша отпечатка, удаление песочницы), 1 — `restic check`, 2 — извлечение,
  3 — запуск приложения, 4 — сравнение отпечатков. Достигнутый уровень — самый
  высокий L, для которого все стадии уровней ≤ L прошли или уровень
  неприменим (у файлов нет уровня 3). Уровень 5 (время восстановления)
  достигнут, когда достигнут 4 и прислано время восстановления. Провал стадии
  уровня 0 (хеш не совпал) — «провал» с уровнем 0.
- Вердикт «провал» или «не можем проверить» выносит агент (расширение не в
  образе, мало диска — «не можем проверить»); «сбой инфраструктуры» и «нет
  верификатора» — сервер.
- Аренда из документа — это потеря шага FXs: окно `lost-after-heartbeats` ×
  heartbeat, продлевается heartbeat'ами; своей аренды V4 не вводит.
- Отпечаток в шаге — непрозрачные байты и их SHA-256: формат задаёт V2.
- Предупреждение «объём упал больше чем на 30%» требует числа файлов из
  отпечатка — V2/V5, в V4 нет.
- Документ: у верификатора нет доступа к боевым системам. Конфигурация
  источника (адреса, имена баз) верификатору не нужна — всё нужное есть в
  отпечатке. Предложено владельцу отменить п. 7 «`config_json` уходит как
  есть» и слать `{}`; ответа пока нет.

### Решение владельца 3 (2026-10-19): без `/ship-feature`

«Мы не делаем это через полноценный конвейер — это очень долго, и у этой задачи
нет классического поведения». Фазы 2 и 3 идут напрямую: тест первым, затем код,
`./scripts/gate.sh <модуль> fast` перед коммитом.

## Фаза 2 — очередь, цикл, выдача, результаты, повторы

### Что сделано

- **proto** (только добавления, `buf breaking` зелёный): `RegisterRequest.verifier`
  и `Heartbeat.verifier` (`VerifierStatus`: свободное место, песочница),
  `StepProgress.stage`, `VerifyOutput.stages/verdict/mismatches/restore_duration`,
  `VerifyStage`, `StageStatus`, `VerifyVerdict`, `FingerprintMismatch`,
  `RunStep.verify` (`VerifyTask`: задание, `repository_id`, отпечаток, хеш, срок).
- **Агент**: каждый агент шлёт пустой `VerifierStatus` («неизвестно» до V5);
  исполнение проверки не менялось (решение 4).
- **Миграция** `V202610191200__restore_verification.sql`: D6-индекс без
  `trigger = 'verification'`; `run_steps.verify_task`; `sources.verification_enabled`
  (по умолчанию включено); статус верификатора в `agents`; глобальная
  `verification_schedule`; `verification_jobs` (одно открытое на источник);
  `restore_verifications` по ADR 0013.
- **Сервер** (`verify/`): `VerifierChoice` (правило выдачи и причины),
  `AttemptOutcome` и `Levels` (V-D2, V-D10), `CycleMoments` (момент цикла),
  `VerifierPool`, `VerificationCycles`, `VerificationQueue`,
  `VerificationSettlement`, `VerificationHistory`, `VerificationLoop` (такт раз в
  `sard.verification.interval`). Попытка — запуск с `trigger = verification` и
  шагом VERIFY на верификаторе (`runs/VerificationRuns`), выдачу, прогресс,
  результаты и потерю шага делают существующие S6a, S7a и FXs.
- Проверки исключены из D6, свежести (F6), уведомлений о запусках, здоровья
  самобэкапа, обзора и истории запусков в REST.

### Отступления от плана фазы 1 (на ревью)

1. **Цикл — один на установку, без строки на тенант.** Строка цикла на каждый
   тенант, создаваемая тактом, мешала удалению тенанта в фикстурах и не нужна:
   задача говорит «одно расписание на установку». Расписание — глобальная
   `verification_schedule` (пишет только REST фазы 3; без строки — значения по
   умолчанию). Момент цикла вычисляется: последний момент cron ≤ сейчас;
   источник получает задание цикла, если с этого момента у него его не было.
   Свой замок циклу не нужен: повторный такт или второй сервер ничего не
   создают.
2. **«Нет верификатора» в тенанте без единого верификатора — состояние, а не
   запись.** Такой тенант не получает ни заданий, ни строк; REST фазы 3 покажет
   «нет верификатора» по факту отсутствия верификаторов. Запись «нет
   верификатора» с причинами пишется, когда верификаторы есть, но ни один не
   подошёл до срока. «Без спама» так выполняется по построению.
3. **Стадии по ходу (`StepProgress.stage`)** в proto есть, сервер их пока не
   хранит: итоговый `VerifyOutput` несёт все стадии. Хранение и показ — фаза 3
   вместе с REST записи проверки.
4. **Heartbeat.verifier** сервер пока не читает: до V5 значения всегда
   «неизвестно», а Register их уже сохраняет.
5. Попытка без вердикта — не только `lost`, но и `rejected`, `timed_out`,
   `cancelled`: три подряд — «сбой инфраструктуры».

### Проверки (проверил)

- `VerifierChoiceTest` (13), `AttemptOutcomeTest` (14), `CycleMomentsTest` (5),
  `VerificationOutputJsonTest` (3), `DispatchPartsTest` (8, в том числе «в
  RunStep проверки нет полей с секретами»), `ProtoReportsTest` (6),
  `RegisterRequestsTest`, `RegisterIntegrationTest`.
- `VerificationIntegrationTest` (11, PostgreSQL): пункты стратегии 1, 2, 4, 5,
  7, 9.
- Гейт агента и proto — зелёные; `buf breaking` против `origin/main` — зелёный.
- `./scripts/gate.sh server fast` — PASSED: покрытие 96.8% (инструкции), ни одной
  функции с CRAP > 6. По дороге гейт поймал 31 замечание detekt и 6 функций с CRAP
  > 6 — исправлены упрощением, порогов не трогал; три старых теста обновлены под
  решение 1 (миграция в списке, новые поля JSON VERIFY, запуску проверки не нужен
  workflow), добавлен схемный тест «активная проверка не занимает D6».
- Мутационный гейт (`./scripts/gate.sh server` полный) в фазе 2 не запускал.

Полагаю, но не проверял: два сервера могут одновременно выдать одному
верификатору два задания (занятость читается без блокировки) — окно в один
такт; приемлемо для MVP, отмечу в ADR.

### Решение владельца 4 (2026-10-19)

«Согласен со всем, слать {}, начинай фазу 3». Отступления фазы 2 приняты; шаг
проверки несёт `config_json = {}`.

## Фаза 3 — события, lastVerifiedAt(), REST, ADR

### Что сделано

- **`{}` вместо конфигурации источника** в шаге VERIFY (`runs/VerificationRuns`),
  закреплено интеграционным тестом.
- **События для V6** (миграция дополнена, ветка не влита):
  `restore_verifications.recovered` («снова прошла» — успех сразу после провала);
  `verification_stale_episodes` («не проверялся успешно дольше N дней», один эпизод
  на поворот; `sard.verification.stale-after`, по умолчанию 7 дней; отсчёт от
  последнего успеха или первого целого снимка). «Провалилась» — сама запись
  `failed`. Доставка — V6.
- **`lastVerifiedAt()`**: `RecordedRestoreVerifications` вместо заглушки —
  самая старая из последних успешных проверок по источникам с включённой
  проверкой и целым снимком; `null`, пока у одного из них её нет.
- **REST** (`VerificationsController`): состояние проверки источника и его
  включение; история и запись со стадиями; «проверить сейчас» (202, 409
  `verification_active`, 422 `snapshot_unusable`); расписание цикла; верификаторы
  с причинами для источника. `Source` получил `verificationEnabled`,
  `verificationStatus`, `lastVerifiedAt`, `lastVerifiedLevel`.
- **Веб**: `make openapi`, типы, словарь ошибок (ru/en), моки
  (`api/verifications.ts`, `verification.ts`), README моков.
- **ADR** `00XX-draft-verification-server.md`; ADR 0013 (системный вызов 9),
  ADR 0010 (заглушка `/status` заменена).

### Проверки (проверил)

- `StalenessTest` (6), `VerificationPropertiesTest` (3), `StatusControllerTest`.
- `VerificationIntegrationTest` (16): добавлены пункты 6 (провал → снова прошла;
  эпизод «дольше 7 дней» открывается один раз и закрывается успехом; выключение
  проверки закрывает эпизод без успеха — фейковые часы) и 8 (`lastVerifiedAt`;
  источник самобэкапа проверяется так же), `{}` в шаге.
- `VerificationsApiIntegrationTest` (7): все эндпоинты, 409 и 422.
- `ApiSerializationIntegrationTest`: образцы всех новых схем сверены с OpenAPI.
- `./scripts/gate.sh web fast` — PASSED.
- `./scripts/gate.sh server fast` — PASSED: покрытие 96.7%, ни одной функции с
  CRAP > 6. По дороге гейт поймал длинные строки, магические индексы колонок,
  `TooManyFunctions` в обработчике ошибок и пять функций с CRAP 6.1–8 — исправлены
  разбиением и тестом маппинга (`VerificationMappingTest`); контрактный тест
  перечисляет новые операции, `make openapi` перегенерировал веб.
- Гейты proto и агента в фазе 3 не трогались (изменений нет).

### Не сделано / вне фазы

- Пункт 10 (e2e) снят решением владельца 2.
- Мутационный гейт (полный `./scripts/gate.sh server`) не запускал.
- Хранение стадий по ходу (`StepProgress.stage`) не делал: итоговые стадии
  приходят в `VerifyOutput`, показ по ходу — забота V6, если понадобится.

## CI красный на `3efeba6`: мутационный гейт

Джоба `server` (мутации mutflow) упала, остальные зелёные. Мутационный гейт я
локально не запускал — это и пропустило.

- `CycleMomentsTest` вызывал настоящий `CronSchedule`: его мутанты (около 20)
  выживали в чужом тесте. Тест переведён на фейковые `Fires`.
- Цикл `while` в `CycleMoments.of` под мутацией условия становился
  бесконечным (`MutationTimedOutException`). Переписан приёмом `Due.missed`:
  `generateSequence` с пределом `MOMENT_LIMIT` (50 000 > 46 080 моментов
  минутного cron за 32 дня) и тест на предел.
- Выжившие на границах: `RegisterRequests` (`>= 0`) и `AttemptOutcome`
  (`level >= 4`) — добавлены тесты для 0 свободных байт и для уровней 4 и 3.

Проверил: мутационный прогон (`-Pmutflow.enabled=true`) классов `verify.*`,
`RegisterRequestsTest`, `ProtoReportsTest`, `DispatchPartsTest`,
`VerificationMappingTest`, `VerificationOutputJsonTest`, `ResultCheckTest`,
`StatusControllerTest` — без выживших и таймаутов; `./scripts/gate.sh server fast`
— PASSED. Полный мутационный гейт по всему серверу локально не гонял (в CI
~22 минуты) — смотрю результат CI.
