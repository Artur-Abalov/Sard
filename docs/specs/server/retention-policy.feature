# language: ru
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# L1 выпуск 1 — сервер: политика хранения источника, политика установки по
# умолчанию, забывание (FORGET) после каждого успешного бэкапа и вручную,
# сверка забытых снимков, ручная очистка хранилища (PRUNE) репозитория, виды
# запусков forget и prune, уборка H1 запусков забытых снимков, метрики,
# события расширений и политика доступа для новых операций.
# Агент — docs/specs/agent/retention-forget.feature (L1a, утверждено) и
# docs/specs/agent/retention-prune.feature; контракт —
# docs/specs/proto/retention-forget.feature и docs/specs/proto/retention-prune.feature;
# консоль — docs/specs/web/retention-policy.feature; ручная проверка —
# docs/qa/retention-policy.md (и часть 3 docs/qa/retention-forget.md).
# Разбиение L1 (обновлено под выпуск 2026-10-31), ВL1–ВL5, ДL1–ДL7, КL1–КL16,
# КП1–КП6, П1–П11 — в заголовке docs/specs/agent/retention-forget.feature.
#
# СТАТУС: УТВЕРЖДЕНО владельцем 2026-10-31 (решение передано координатором
# /ship-feature) вместе со всеми решениями КС1–КС17 и К1–К9, с сокращением
# объёма выпуска 1:
#   ВР1 — ответ А, но вся уборка H1 запусков забытых снимков и запусков
#         обслуживания — в выпуске 2; в выпуске 1 уборка H1 не меняется
#         (server-retention.feature действует как есть) и запуски forget и
#         prune не трогает.
#   ВР2 — в выпуске 1 вариант Б: события расширений о prune не публикуются;
#         вариант А (sourceId nullable + repositoryId) — в выпуске 2. События
#         forget с kind "forget" — в выпуске 1.
#   ВР3 — в выпуске 1 вариант Б: только предупреждение в консоли и
#         документации; 409 repository_busy, удержание prune в очереди и
#         защита снимков идущих восстановлений и проверок через
#         keep_snapshot_ids — в выпуске 2. keep_snapshot_ids в выпуске 1 —
#         только последний проверенный снимок (ВL5).
# Сценарии выпуска 2 помечены тегом @выпуск2 и в выпуске 1 не реализуются и
# не проверяются. Теги @вр1, @вр2, @вр3 остаются ссылкой на вопрос.
#
# ---------------------------------------------------------------------------
# Что уже есть (сверено с кодом и спецификациями 2026-10-31)
#   - snapshots.forgotten_at есть в схеме (V202609301800__results.sql), его
#     никто не ставит; REST Snapshot уже несёт forgottenAt, restorable и
#     restoreBlockedReason forgotten (console-restore.feature, КВ1);
#     восстановление не restorable снимка — 422 snapshot_not_restorable.
#   - runs.kind — backup | restore (V202610191400__console_restore.sql);
#     проверка восстановления — trigger verification; runs.source_id NOT NULL;
#     run_steps.action — backup | restore | verify | run; REST RunKind —
#     backup | restore; Run.sourceId и Run.sourceName обязательны и не null.
#   - snapshots.step_id NOT NULL со ссылкой на run_steps; записи проверок и
#     задания V4 ссылаются на snapshots и не удаляются никогда (DB2, Р4).
#   - H1 (server-retention.feature): запуск со строкой snapshots не удаляется
#     никогда; «уборка не удаляет и не меняет строки snapshots»; последний
#     успешный запуск источника каждого вида не удаляется.
#   - J1 (run-cancel-deadlines.feature): статусы, outcome, отмена
#     POST /runs/{id}/cancel, срок шага, ожидание офлайн-агента
#     sard.run.agent-wait.max, отсрочка после старта.
#   - D6: один активный запуск kind backup на источник (индекс только по
#     backup).
#   - Расширения (server-core-extensions.feature, ADR 0070): RunEvent.kind —
#     строка (backup, restore, verification), RunEvent.sourceId — UUID, не
#     null; settings.changed с перечнем setting; политика доступа видит
#     операцию как метод и шаблон пути OpenAPI; actor — из SubjectSource.
#   - Метрики (server-metrics.feature): sard_run_duration_seconds и
#     sard_runs_active с kind backup | restore | verify; sard_run_finished_total
#     считает все запуски (docs/operator/14-monitoring.md).
#   - Уведомления S9a/S9b: планируются все завершённые запуски.
#   - F6: системные источники отвечают 409 system_source на замену и
#     удаление; меняется только их расписание.
#
# ---------------------------------------------------------------------------
# Изменения контракта (OpenAPI экспортирует сервер; openapi.json, schema.d.ts
# и моки — make openapi). Имена — предложение, окончательно — coder.
#   К1. RetentionPolicy (ответ): preset (standard | minimum | custom),
#       keepLast, keepDaily, keepWeekly, keepMonthly, keepYearly,
#       keepWithinDays — int32 >= 0, 0 — правило выключено. Все обязательны.
#       RetentionPolicyInput: preset обязателен; при custom поля keep* не
#       обязательны (не передано — 0); при standard и minimum любое поле keep*
#       в теле — 422 validation_failed у этого поля.
#       Готовые варианты (ДL2): standard — keepDaily 7, keepWeekly 4,
#       keepMonthly 6; minimum — keepDaily 7; остальные поля 0.
#       Проверки custom: каждое поле >= 0; keepWithinDays <= 36500; хотя бы
#       одно поле > 0, иначе 422 validation_failed у поля policy с сообщением
#       "retention policy is empty" (агент такую политику отклоняет, КL7).
#   К2. Source + retention (RetentionPolicy | null; null — «хранение не
#       настроено»). Только чтение: задаётся через К3.
#   К3. GET /api/v1/sources/{sourceId}/retention -> 200 SourceRetention; 401;
#       403 access_denied; 404 not_found; 503.
#       PUT /api/v1/sources/{sourceId}/retention, тело {policy:
#       RetentionPolicyInput | null} -> 200 SourceRetention; 401; 403
#       origin_rejected | access_denied; 404 not_found; 409
#       onboarding_incomplete; 422 validation_failed; 503. Для системного
#       источника самобэкапа разрешено (КС13).
#       SourceRetention: policy (RetentionPolicy | null); repositoryId (string |
#       null — агент источника не сообщил id репозитория); maintenanceAgent
#       ({agentId, hostname} | null); hostFloor ({disabled, keepLast,
#       keepWithinDays, origin: built_in | config_default | config_repository}
#       | null — предел обслуживающего агента для этого репозитория, null, если
#       агента нет или он его не сообщил); floorExtendsPolicy (boolean, КС16);
#       forgetBlockedReason и pruneBlockedReason (MaintenanceBlockedReason |
#       null, КС3, КС4); lastForget и lastPrune (MaintenanceRun | null —
#       последний по queuedAt запуск forget источника и prune его репозитория,
#       в любом статусе).
#       MaintenanceBlockedReason: retention_not_configured (только forget) |
#       unknown_repository | agent_revoked | maintenance_owner_required |
#       agent_outdated.
#       MaintenanceRun: runId, kind, status (RunStatus), outcome (RunOutcome),
#       queuedAt, finishedAt | null, forgottenCount | null, keptByFloorCount |
#       null, freedBytes | null, repositorySizeBytes | null, failure
#       (repository_locked | delete_denied | storage_unavailable | null),
#       message | null.
#   К4. GET /api/v1/retention-defaults -> 200 RetentionDefaults {policy:
#       RetentionPolicy | null}; 401; 403; 503. PUT того же пути, тело {policy:
#       RetentionPolicyInput | null} -> 200 RetentionDefaults; 401; 403
#       origin_rejected | access_denied; 409 onboarding_incomplete; 422
#       validation_failed; 503.
#   К5. POST /api/v1/sources/{sourceId}/forget (без тела) -> 201 Run (kind
#       forget, trigger manual, requestedBy "admin", status queued); 401; 403
#       origin_rejected | access_denied; 404 not_found (нет, удалён, чужой
#       тенант); 409 retention_not_configured | unknown_repository |
#       agent_revoked | maintenance_owner_required | agent_outdated |
#       maintenance_active (MaintenanceActiveProblem с activeRunId) |
#       onboarding_incomplete; 503.
#   К6. POST /api/v1/repositories/{repositoryId}/prune (без тела) -> 201 Run
#       (kind prune, trigger manual, requestedBy "admin", sourceId null,
#       sourceName null, repository заполнен); 401; 403 origin_rejected |
#       access_denied; 404 not_found (ни один неотозванный агент тенанта не
#       сообщает этот repositoryId); 409 maintenance_owner_required |
#       agent_outdated | maintenance_active | onboarding_incomplete; 503.
#       [Выпуск 2, ВР3: + 409 repository_busy.]
#   К7. RunKind + forget, prune. RunTrigger + after_backup. StepAction +
#       forget, prune. Run и RunSummary: sourceId и sourceName становятся
#       nullable (null только у prune); + repository ({repositoryId,
#       repositoryName} | null — у forget и prune заполнен, repositoryName —
#       имя у обслуживающего агента). RunStep + forget (ForgetSummary | null:
#       policy — отправленная RetentionPolicy, forgottenCount,
#       keptByFloorCount, remainingCount, listedAt | null, failure, lockHolder
#       | null) и prune (PruneSummary | null: freedBytes | null,
#       repositorySizeBytes | null, failure, lockHolder | null). У шагов forget
#       и prune plugin — пустая строка. GET /api/v1/runs?kind= принимает
#       forget и prune.
#   К8. Snapshot + forgottenReason (policy | missing | null): policy — снимок
#       назван агентом в forgotten_snapshot_ids; missing — снимка не оказалось
#       в репозитории при сверке (КС8). null, пока снимок не забыт.
#   К9. ErrorCode + retention_not_configured, maintenance_owner_required,
#       agent_outdated, maintenance_active. [Выпуск 2, ВР3: + repository_busy.]
#       MaintenanceActiveProblem — как RunActiveProblem, activeRunId — уже
#       стоящий в очереди или идущий запуск обслуживания.
#
# Новые настройки сервера (КС15; не длительность или <= 0 — сервер не
# стартует, сообщение называет настройку)
#   sard.maintenance.forget-timeout (SARD_MAINTENANCE_FORGET_TIMEOUT), 1h —
#       RunStep.timeout шага FORGET.
#   sard.maintenance.prune-timeout (SARD_MAINTENANCE_PRUNE_TIMEOUT), 12h —
#       RunStep.timeout шага PRUNE.
#
# Сообщения сервера (runs.message и run_steps.message; английские; сценарии
# проверяют префикс)
#   «retention policy removed before dispatch»
#   «repository maintenance owner required: several agents hold the repository»
#   «agent does not support <forget|prune>: update the agent»
#   «the repository is no longer reported by the agent»
#
# ---------------------------------------------------------------------------
# Решения specifier (ждут утверждения)
#   КС1. Политика хранится значениями. preset — подпись, которую выбрал
#        администратор; сервер не «узнаёт» готовый вариант в custom. Будущая
#        смена определения варианта сохранённые политики не меняет.
#   КС2. Политика установки по умолчанию — «стандарт» (ДL2) на новой и на
#        обновлённой установке. Она копируется в источник при его создании
#        и больше с ним не связана: смена или снятие политики установки
#        существующие источники не меняет. Источники, созданные до этого
#        выпуска, после обновления остаются без политики («хранение не
#        настроено»): обновление сервера ничего не удаляет.
#   КС3. Обслуживающий агент репозитория источника — единственный
#        неотозванный агент роли source, чей последний Register сообщает
#        repository_id репозитория источника (id берётся из последнего
#        Register агента источника по имени репозитория источника).
#        Причины, по которым обслуживание не выполняется:
#          unknown_repository — агент источника не сообщает репозиторий с
#            этим именем или сообщает его без repository_id;
#          agent_revoked — агент источника отозван;
#          maintenance_owner_required — таких агентов роли source больше
#            одного (назначение владельца обслуживания ВL2 — в следующем
#            выпуске); верификаторы не считаются.
#        Выбор повторяется при отправке шага: если к этому моменту причина
#        появилась, шаг не отправляется, запуск — failed с сообщением.
#   КС4. Совместимость N−1. FORGET отправляется, только если в последнем
#        Register обслуживающего агента у этого репозитория есть
#        retention_floor (КП4); иначе agent_outdated. PRUNE — только если
#        builtin_actions агента содержит ACTION_PRUNE (КП10); иначе
#        agent_outdated. Бэкап и восстановление на таком агенте работают как
#        раньше. Проверка — и при создании запуска, и при отправке.
#   КС5. Забывание после бэкапа. Когда запуск kind backup (любой trigger,
#        кроме verification) становится succeeded, сервер после фиксации
#        итога бэкапа создаёт запуск kind forget, trigger after_backup,
#        requestedBy null — если у источника есть политика, обслуживание не
#        заблокировано (КС3, КС4) и у источника нет незавершённого forget
#        (тогда новый не создаётся: стоящий в очереди возьмёт свежие данные
#        при отправке, КС7). Сбой создания пишется ERROR и итог бэкапа не
#        меняет. Неполный бэкап (failed со снимком) и поздний итог
#        потерянного шага forget не создают.
#   КС6. Очередь обслуживания. На репозиторий (тенант, repository_id) — не
#        больше одного запуска forget или prune в dispatched или running;
#        остальные ждут в queued и отправляются по queuedAt. На источник — не
#        больше одного незавершённого forget; на репозиторий — не больше
#        одного незавершённого prune; ручной запрос сверх этого — 409
#        maintenance_active. Бэкапы и восстановления очередь обслуживания не
#        ждут: конфликт с ними решает блокировка restic на агенте (КL12).
#   КС7. Задание FORGET составляется при отправке шага и записывается в шаг;
#        повторная отправка после переподключения несёт записанное задание:
#          source_id — id источника (значение метки sard.source);
#          policy — текущая политика источника; политики нет — шаг не
#            отправляется, запуск failed «retention policy removed before
#            dispatch»;
#          keep_snapshot_ids — restic id снимка последней проверки passed
#            источника (ВL5), если он этого repository_id и не забыт.
#            [Выпуск 2, ВР3: + снимки, которые восстанавливают активные
#            запуски restore и проверяют открытые задания проверки источника.]
#          incomplete_snapshot_ids — restic id снимков источника этого
#            repository_id с partial true, не забытых; incomplete_max_age —
#            168 ч (ДL3), только если список непуст;
#          repository_name — имя репозитория у обслуживающего агента;
#          timeout — sard.maintenance.forget-timeout;
#          plugin, config_json, tags и snapshot_id — пустые (КL10, КL15).
#   КС8. Сверка (КL9). По итогу FORGET с listed_at (любой статус, в том числе
#        поздний итог по J1 Р-J8):
#          а) строкам snapshots этого источника и этого repository_id, чей
#             restic id есть в forgotten_snapshot_ids и которые ещё не забыты,
#             ставится forgotten_at = listed_at и forgottenReason policy;
#          б) строкам этого источника и repository_id, которые ещё не забыты,
#             которых нет в remaining_snapshot_ids и чей шаг бэкапа сервер
#             зафиксировал раньше отправки шага FORGET, ставится forgotten_at =
#             listed_at и forgottenReason missing. Условие «зафиксирован
#             раньше отправки» по часам сервера защищает снимок бэкапа, который
#             записывался в репозиторий во время перечисления;
#          в) id других источников, частей и неизвестных серверу снимков
#             ничего не меняют; уже забытая строка не меняется.
#        Без listed_at сверки нет; итог с забытыми id без listed_at — неверный
#        итог (failed «invalid result: …» по ResultCheck), строки не меняются.
#   КС9. [ВЫПУСК 2. В выпуске 1 уборка H1 не меняется: запуски забытых
#        снимков, строки snapshots и запуски forget и prune она не трогает.]
#        Уборка H1 (поправка server-retention.feature, ответ на требование
#        H1 к L1; ВР1 — ответ А): правило «запуск со строкой snapshots не удаляется»
#        действует только для незабытого снимка. Забытая строка snapshots
#        удаляется уборкой вместе со своим запуском бэкапа (раньше него, в той
#        же пачке), когда запуск по прочим правилам H1 подлежит удалению
#        (старше sard.retention.runs, не последний успешный бэкап источника,
#        на него не ссылается живая строка) и на снимок не ссылаются задание
#        или запись проверки V4. Иначе остаются оба. Запуски forget и prune
#        уходят по общему сроку запусков; последний успешный forget источника
#        и последний успешный prune репозитория не удаляются никогда. Тег kind
#        sard.retention.deleted и строка итога прохода получают snapshot.
#   КС10. Обслуживание — не бэкап. Запуски forget и prune: не дают уведомлений
#        (S9a, S9b, расписание; уведомления 22–25 — следующий выпуск); не
#        закрывают эпизоды просрочки и не входят в серии расписания; не
#        считаются бэкапом самобэкапа (firstBackupDone, свежесть, failing) и
#        не двигают sard_source_last_backup_success_timestamp_seconds; не
#        занимают индекс D6.
#   КС11. Метрики (П9): kind метрик sard_run_duration_seconds и
#        sard_runs_active получает значения forget и prune — 15 комбинаций
#        гистограммы и 15 серий активных запусков с нулями;
#        sard_run_finished_total считает и их, как все запуски;
#        docs/operator/14-monitoring.md называет новые значения.
#   КС12. События расширений и политика доступа (П10, П11). Запуски forget
#        дают run.created, run.started, run.status_changed с kind "forget",
#        trigger manual или after_backup, sourceId источника; actor — субъект
#        сессии у ручного, null у after_backup. Запуски prune в выпуске 1
#        событий run.* не дают (ВР2, вариант Б); в выпуске 2 — kind "prune",
#        trigger manual, sourceId null, repositoryId. settings.changed
#        получает setting source_retention (objectId — id источника) и
#        retention_defaults (objectId null); повтор без изменения события не
#        даёт (Р20). Новые операции К3–К6 подчинены политике доступа (Р17,
#        Р40) как метод и шаблон пути OpenAPI; ограничение онбординга full
#        (Р22) — 409 onboarding_incomplete, перечень разрешённых операций не
#        меняется.
#   КС13. Самобэкап F6 входит в выпуск без отдельной механики: системные
#        источники — обычные источники для политики и забывания;
#        PUT /retention для них разрешён (как PUT /schedule). Системные
#        источники, созданные после обновления (привязка самобэкапа), получают
#        политику установки; созданные раньше — без политики. Встроенный
#        агент sard-self обычно единственный держатель своего репозитория, и
#        обслуживание его выполняет он. Честное ограничение — в документации
#        (agent/retention-prune.feature, @docs): предел sard-self лежит на
#        машине сервера.
#   КС14. forgottenReason различает «забыт по политике» и «не найден в
#        репозитории»: консоль не пишет «удалён по политике» про снимок,
#        который оператор удалил руками.
#   КС15. Сроки шагов обслуживания — свойства установки (не по источнику):
#        forget 1 ч (ожидание блокировки 5 мин + перечисление + удаление
#        пачками), prune 12 ч. Ожидание офлайн-агента — как у ручного запуска
#        (J1 Р-J10: создание + sard.run.agent-wait.max) и для after_backup.
#   КС16. floorExtendsPolicy = true тогда и только тогда, когда у источника
#        есть политика, есть hostFloor, он не выключен и НЕ (policy.keepLast
#        >= hostFloor.keepLast И policy.keepWithinDays >=
#        hostFloor.keepWithinDays). То есть есть снимки, которые политика
#        отпустила бы, а предел хоста держит. Консоль показывает фразу правила
#        10 по этому признаку, сама не сравнивает.
#   КС17. Удалённый источник новых forget не получает (ручной — 404, бэкапов
#        больше нет); уже созданный forget удалённого источника выполняется.
#
# ---------------------------------------------------------------------------
# Поправки к утверждённым спецификациям (вносятся вместе с этой; сценарии
# ниже их закрепляют)
#   - server-retention.feature: в выпуске 1 НЕ меняется. В выпуске 2
#     сценарий «Уборка не удаляет и не меняет строки snapshots» заменяется
#     правилом КС9; в перечнях тега kind и строки итога прохода — snapshot.
#   - server-metrics.feature: «есть для каждой из 9 комбинаций» — 15
#     комбинаций; «остальные 6 серий sard_runs_active» — остальные 12 (КС11).
#   - server-core-extensions.feature Р18: kind событий запуска — backup,
#     restore, verification, forget (выпуск 1); prune, sourceId nullable и
#     repositoryId — выпуск 2 (ВР2). Р20 — + source_retention,
#     retention_defaults (выпуск 1).
#   - run-notifications.feature: планируются завершённые запуски kind backup
#     и restore и проверки, не forget и prune (КС10).
#   - onboarding-self-backup.feature Р20: firstBackupDone считает запуски
#     kind backup.
#   - proto/retention-forget.feature КП4 и ADR 0071 «Последствия»: сервер не
#     отправляет FORGET агенту без retention_floor — закреплено здесь (КС4).
#
# ---------------------------------------------------------------------------
# Вопросы владельцу — ОТВЕЧЕНЫ 2026-10-31 (ВР1 — А в выпуске 2; ВР2 — Б в
# выпуске 1, А в выпуске 2; ВР3 — Б в выпуске 1, А в выпуске 2; см. СТАТУС).
# Исходная постановка сохранена:
#   ВР1. Строки snapshots забытых снимков и уборка H1. Ваше решение H1:
#        «snapshots уборка не трогает никогда»; требование H1 к L1: запуск
#        забытого снимка должен стать доступен уборке. Строка snapshots
#        ссылается на шаг бэкапа (step_id NOT NULL), поэтому запуск нельзя
#        удалить, пока строка жива.
#        (А, рекомендация) Забытая строка удаляется вместе со своим запуском,
#        когда тот по правилам H1 подлежит удалению, кроме снимков, на
#        которые ссылаются задание или запись проверки (они вечны по DB2) —
#        КС9. Консоль показывает «удалён по политике» ещё ~180 дней.
#        (Б) Строка живёт всегда, связь со шагом обнуляется: Snapshot.runId и
#        stepId становятся nullable (изменение контракта REST), таблица
#        snapshots растёт на каждый бэкап без предела.
#   ВР2. События расширений о запуске prune (П10). RunEvent.sourceId — UUID не
#        null в публичном API extension.**; у prune источника нет.
#        (А, рекомендация) В этой версии sourceId становится nullable и
#        добавляется repositoryId: изменение API между версиями X.Y.Z
#        допустимо (Р1 заморожен «в пределах X.Y.Z»), дамп ABI обновляется,
#        образец расширения пересобирается; внешних расширений до пилота нет.
#        (Б) События prune в этом выпуске не публикуются; аудит ручной
#        очистки расширением невозможен до следующего выпуска.
#   ВР3. Ручной prune и идущие восстановление или проверка (часть правила 16,
#        полностью отложенного). prune держит исключительную блокировку и
#        удаляет файлы pack; восстановление, идущее без блокировки, может
#        потерять файл посреди чтения и закончиться ложным провалом.
#        (А, рекомендация, дёшево) POST prune отвечает 409 repository_busy,
#        пока в этом repository_id идёт запуск restore или попытка проверки;
#        стоящий в очереди prune не отправляется, пока они идут. Обратное
#        (проверка, начатая во время prune, — «не состоялась», а не «провал»)
#        — в следующем выпуске; до тех пор — известное ограничение в
#        документации.
#        (Б) Ничего в выпуске, только предупреждение в диалоге консоли и в
#        документации.
#
# ---------------------------------------------------------------------------
# Теги — как в docs/specs/server/rest-api.feature и server-retention.feature
#   @http     HTTP-запросы к запущенному контексту сервера (Spring Boot test,
#             PostgreSQL в Testcontainers); часы подставляются тестом
#   @grpc     дополнительно агент — тестовый клиент gRPC со своим сертификатом
#   @server   интеграционный тест сервера без HTTP (уборка, метрики, события)
#   @startup  проверяется стартом контекста с заданными свойствами
#   @unit     модульный тест без Spring и базы, mutflow
#   @qa       дополнительно проходится вручную по docs/qa/retention-policy.md
#   @вр1 @вр2 @вр3  сценарий связан с вопросом ВР1, ВР2, ВР3
#   @выпуск2  сценарий выпуска 2: в выпуске 1 не реализуется и не проверяется
#
# Общие соглашения
#   - «Администратор» — запрос с действующей сессией администратора тенанта
#     A; изменяющий запрос без заголовка Origin. «Ответ N code» и «ошибка у
#     поля F» — как в rest-api.feature.
#   - T0 = 2026-11-30T12:00:00Z — время часов сервера.
#   - Агент G — роль source, hostname "db1", онлайн; его последний Register:
#     плагин files, репозиторий "qa" с repository_id "r0" и retention_floor
#     keep_last 7, keep_within 336 ч, origin RETENTION_FLOOR_ORIGIN_BUILT_IN;
#     builtin_actions ACTION_FORGET и ACTION_PRUNE.
#   - «Агент L1a» — тот же Register без builtin_actions. «Агент до L1a» —
#     без builtin_actions и без retention_floor у репозиториев.
#   - «Агент H тоже видит r0» — второй неотозванный агент роли source, в
#     Register которого есть репозиторий (под любым именем) с repository_id
#     "r0". «Верификатор V видит r0» — то же для агента роли verifier.
#   - Источник S — files на G с репозиторием "qa", политика «стандарт», если
#     не сказано иное. Источник T — второй источник на G с репозиторием "qa".
#   - «Снимок sN» — строка snapshots источника S, repository_id "r0", restic id
#     из 64 hex, шаг бэкапа зафиксирован сервером до T0 − 1 ч.
#   - «Отправлен шаг FORGET» — G получил RunStep с action ACTION_FORGET;
#     «итог FORGET» — StepResult этого шага с ForgetOutput.
#   - Названия сценариев цитируются именами тестов Kotlin, поэтому в них нет
#     символов . : ; / < > [ ] \

Функция: Политика хранения источника, забывание и ручная очистка хранилища на сервере

  Администратор один раз выбирает, сколько хранить, — и после каждого
  успешного бэкапа сервер просит агента забыть лишнее, а консоль честно
  показывает, что забыто, что держит хост и сколько места освободила очистка.

  # ===========================================================================
  Правило: Политика источника задаётся готовым вариантом или своими правилами

    @http @qa
    Структура сценария: Готовый вариант сохраняется своими значениями
      Когда администратор отправляет PUT retention источника S с policy preset <вариант>
      Тогда ответ 200
      И policy в ответе — preset <вариант>, keepLast <last>, keepDaily <daily>, keepWeekly <weekly>, keepMonthly <monthly>, keepYearly 0 и keepWithinDays 0
      И GET источника S отдаёт ту же policy в retention

      Примеры:
        | вариант  | last | daily | weekly | monthly |
        | standard | 0    | 7     | 4      | 6       |
        | minimum  | 0    | 7     | 0      | 0       |

    @http
    Сценарий: Своя политика сохраняется с указанными полями, остальные равны нулю
      Когда администратор отправляет PUT retention источника S с policy preset custom, keepLast 3 и keepWithinDays 30
      Тогда ответ 200
      И policy — preset custom, keepLast 3, keepWithinDays 30, остальные поля 0

    @http
    Сценарий: Своя политика со значениями стандарта остаётся своей
      Когда администратор отправляет PUT retention источника S с policy preset custom, keepDaily 7, keepWeekly 4 и keepMonthly 6
      Тогда preset в ответе custom

    @http
    Структура сценария: Недопустимая политика отклоняется с полем и ничего не меняет
      Дано у источника S политика «минимум»
      Когда администратор отправляет PUT retention источника S с <тело>
      Тогда ответ 422 validation_failed с ошибкой у поля <поле>
      И политика S осталась «минимум»

      Примеры:
        | тело                                                   | поле                  |
        | policy preset custom без полей                         | policy                |
        | policy preset custom, все поля 0                       | policy                |
        | policy preset custom, keepLast -1                      | policy.keepLast       |
        | policy preset custom, keepWithinDays 36501             | policy.keepWithinDays |
        | policy preset standard и keepLast 3                    | policy.keepLast       |
        | policy preset minimum и keepDaily 7                    | policy.keepDaily      |
        | policy preset weekly                                   | policy.preset         |
        | policy без preset                                      | policy.preset         |

    @http
    Сценарий: Пустая своя политика называет причину словами агента
      Когда администратор отправляет PUT retention источника S с policy preset custom, все поля 0
      Тогда сообщение ошибки у поля policy содержит "retention policy is empty"

    @http
    Сценарий: Границы допустимых значений принимаются
      Когда администратор отправляет PUT retention источника S с policy preset custom, keepLast 2147483647 и keepWithinDays 36500
      Тогда ответ 200

    @http
    Сценарий: Повторное сохранение той же политики события не даёт
      Дано у источника S политика «стандарт», сохранённая в T0 − 1 д
      Когда администратор отправляет PUT retention источника S с policy preset standard
      Тогда ответ 200
      И событие settings.changed не опубликовано

    @http
    Структура сценария: Политика неизвестного, удалённого или чужого источника недоступна
      Когда администратор отправляет <запрос> для источника <источник>
      Тогда ответ 404 not_found

      Примеры:
        | запрос          | источник              |
        | PUT retention   | несуществующего       |
        | PUT retention   | мягко удалённого      |
        | PUT retention   | тенанта B             |
        | GET retention   | тенанта B             |

    @http
    Сценарий: Без сессии политика не читается и не меняется
      Когда клиент без сессии отправляет PUT retention источника S с policy preset standard
      Тогда ответ 401
      И политика S не изменилась

    @http
    Сценарий: Изменяющий запрос с чужим Origin отклоняется
      Когда администратор отправляет PUT retention источника S с заголовком Origin https://evil.example
      Тогда ответ 403 origin_rejected
      И политика S не изменилась

    @http
    Сценарий: Недоступная база даёт 503 и ничего не меняет
      Дано база данных недоступна
      Когда администратор отправляет PUT retention источника S с policy preset minimum
      Тогда ответ 503 unavailable

    @http
    Сценарий: Одновременные сохранения разных политик оставляют одну из них целиком
      Когда 20 потоков одновременно отправляют PUT retention источника S, нечётные — «минимум», чётные — своя keepLast 3
      Тогда все ответы 200
      И политика S равна ровно одной из двух целиком, без смешения полей

  # ===========================================================================
  Правило: Источник без политики ничего не забывает и помечен «хранение не настроено»

    @http @qa
    Сценарий: Снятие политики переводит источник в состояние хранение не настроено
      Когда администратор отправляет PUT retention источника S с policy null
      Тогда ответ 200 с policy null и forgetBlockedReason retention_not_configured
      И GET источника S отдаёт retention null

    @http @grpc
    Сценарий: После успешного бэкапа источника без политики forget не создаётся
      Дано у источника S нет политики
      Когда бэкап S завершается succeeded
      Тогда запусков kind forget у S нет
      И G не получил шага FORGET

    @http
    Сценарий: Ручной forget источника без политики отклоняется
      Дано у источника S нет политики
      Когда администратор отправляет POST forget источника S
      Тогда ответ 409 retention_not_configured
      И запусков не прибавилось

    @startup
    Сценарий: Источники, созданные до выпуска, после обновления остаются без политики
      Дано база версии до L1b с источниками S и T и снимками у каждого
      Когда сервер новой версии стартует и применяет миграции
      Тогда у S и T retention null
      И ни одна строка snapshots не забыта

  # ===========================================================================
  Правило: Политика установки по умолчанию копируется в новый источник

    @http @qa
    Сценарий: На новой установке политика по умолчанию — стандарт
      Дано чистая установка
      Когда администратор запрашивает GET retention-defaults
      Тогда policy — preset standard, keepDaily 7, keepWeekly 4, keepMonthly 6

    @startup
    Сценарий: После обновления политика по умолчанию — стандарт
      Дано база версии до L1b
      Когда сервер новой версии стартует
      Тогда GET retention-defaults отдаёт preset standard

    @http
    Сценарий: Новый источник получает копию политики по умолчанию
      Дано политика по умолчанию — своя keepLast 5
      Когда администратор создаёт источник N
      Тогда retention источника N — своя keepLast 5

    @http
    Сценарий: Без политики по умолчанию новый источник не настроен
      Дано политика по умолчанию null
      Когда администратор создаёт источник N
      Тогда retention источника N равен null

    @http
    Сценарий: Смена политики по умолчанию не меняет существующие источники
      Дано источник N создан при политике по умолчанию «стандарт»
      Когда администратор отправляет PUT retention-defaults с policy preset minimum
      Тогда retention источника N по-прежнему «стандарт»

    @http
    Сценарий: Недопустимая политика по умолчанию отклоняется с полем
      Когда администратор отправляет PUT retention-defaults с policy preset custom, все поля 0
      Тогда ответ 422 validation_failed с ошибкой у поля policy
      И GET retention-defaults отдаёт прежнюю политику

    @http
    Сценарий: Политика по умолчанию одна на тенант
      Дано администратор тенанта A сохранил политику по умолчанию «минимум»
      Когда администратор тенанта B запрашивает GET retention-defaults
      Тогда policy — preset standard

  # ===========================================================================
  Правило: Обслуживание выполняет единственный агент роли source, который видит репозиторий

    @http @grpc @qa
    Сценарий: Карточка хранения называет обслуживающего агента и его предел
      Когда администратор запрашивает GET retention источника S
      Тогда repositoryId "r0"
      И maintenanceAgent — agentId G и hostname "db1"
      И hostFloor — disabled false, keepLast 7, keepWithinDays 14, origin built_in
      И forgetBlockedReason и pruneBlockedReason равны null

    @http @grpc
    Структура сценария: Предел из Register виден с происхождением
      Дано последний Register G несёт у "qa" retention_floor <предел>
      Когда администратор запрашивает GET retention источника S
      Тогда hostFloor — <в ответе>

      Примеры:
        | предел                                                  | в ответе                                                      |
        | keep_last 10, keep_within 720 ч, CONFIG_DEFAULT          | disabled false, keepLast 10, keepWithinDays 30, config_default |
        | keep_last 3, keep_within 48 ч, CONFIG_REPOSITORY         | disabled false, keepLast 3, keepWithinDays 2, config_repository |
        | disabled true, CONFIG_REPOSITORY                         | disabled true, keepLast 0, keepWithinDays 0, config_repository |

    @http @grpc
    Сценарий: Новый Register с другим пределом сразу виден в карточке
      Дано GET retention источника S отдаёт hostFloor keepLast 7
      Когда G присылает Register с retention_floor keep_last 30 у "qa"
      Тогда GET retention источника S отдаёт hostFloor keepLast 30

    @http @grpc @qa
    Сценарий: Второй агент роли source в том же репозитории блокирует обслуживание с причиной
      Дано агент H тоже видит r0
      Когда администратор запрашивает GET retention источника S
      Тогда maintenanceAgent null и hostFloor null
      И forgetBlockedReason и pruneBlockedReason равны maintenance_owner_required

    @http @grpc
    Сценарий: Верификатор, который видит репозиторий, не мешает обслуживанию
      Дано верификатор V видит r0
      Когда администратор запрашивает GET retention источника S
      Тогда maintenanceAgent — G
      И forgetBlockedReason равен null

    @http @grpc
    Сценарий: Отозванный второй агент не мешает обслуживанию
      Дано агент H тоже видит r0 и отозван
      Когда администратор запрашивает GET retention источника S
      Тогда maintenanceAgent — G

    @http @grpc
    Сценарий: Ручной forget при двух агентах в репозитории отклоняется
      Дано агент H тоже видит r0
      Когда администратор отправляет POST forget источника S
      Тогда ответ 409 maintenance_owner_required
      И запусков не прибавилось

    @http @grpc
    Сценарий: После бэкапа при двух агентах в репозитории forget не создаётся
      Дано агент H тоже видит r0
      Когда бэкап S завершается succeeded
      Тогда запусков kind forget у S нет

    @http @grpc
    Структура сценария: Обслуживание без известного репозитория или агента блокируется
      Дано <условие>
      Когда администратор запрашивает GET retention источника S
      Тогда forgetBlockedReason и pruneBlockedReason равны <причина>
      И POST forget источника S отвечает 409 <причина>

      Примеры:
        | условие                                                   | причина            |
        | последний Register G не сообщает репозиторий "qa"         | unknown_repository |
        | последний Register G сообщает "qa" с пустым repository_id | unknown_repository |
        | агент G отозван                                           | agent_revoked      |

    @http @grpc
    Сценарий: Офлайн-агент не блокирует обслуживание
      Дано агент G офлайн
      Когда администратор запрашивает GET retention источника S
      Тогда forgetBlockedReason равен null

  # ===========================================================================
  Правило: Агент старой версии получает понятный отказ, бэкап и восстановление работают

    @http @grpc @qa
    Сценарий: Агент до L1a не получает FORGET и помечен «обновите агента»
      Дано G — агент до L1a
      Когда администратор запрашивает GET retention источника S
      Тогда forgetBlockedReason и pruneBlockedReason равны agent_outdated
      И hostFloor null
      И POST forget источника S отвечает 409 agent_outdated

    @http @grpc
    Сценарий: Агент L1a забывает, но не очищает
      Дано G — агент L1a
      Когда администратор запрашивает GET retention источника S
      Тогда forgetBlockedReason равен null
      И pruneBlockedReason равен agent_outdated
      И POST prune репозитория r0 отвечает 409 agent_outdated

    @http @grpc
    Сценарий: После бэкапа на агенте до L1a forget не создаётся
      Дано G — агент до L1a
      Когда бэкап S завершается succeeded
      Тогда запусков kind forget у S нет
      И G не получил шага FORGET

    @http @grpc @qa
    Сценарий: Бэкап на агенте до L1a работает как раньше
      Дано G — агент до L1a
      Когда администратор запускает бэкап S
      Тогда G получает шаг BACKUP, и после итога SUCCEEDED запуск succeeded

    @http @grpc
    Сценарий: Восстановление на агенте до L1a работает как раньше
      Дано G — агент до L1a, у S есть снимок s1
      Когда администратор отправляет POST restores снимка s1 с agentId G
      Тогда ответ 201 и G получает шаг RESTORE

    @http @grpc
    Сценарий: Агент, откатанный на старую версию после создания forget, шага не получает
      Дано запуск forget S в queued
      И G переподключается с Register агента до L1a
      Когда наступает отправка шагов G
      Тогда G не получил шага FORGET
      И запуск forget failed с сообщением "agent does not support forget: update the agent"

    @http @grpc
    Сценарий: Второй агент, появившийся после создания forget, останавливает отправку
      Дано запуск forget S в queued, G офлайн
      И агент H тоже видит r0
      Когда G подключается
      Тогда G не получил шага FORGET
      И запуск forget failed с сообщением "repository maintenance owner required: several agents hold the repository"

  # ===========================================================================
  Правило: Успешный бэкап запускает забывание по политике

    @http @grpc @qa
    Структура сценария: Успешный бэкап любого триггера создаёт forget
      Когда бэкап S с trigger <триггер> завершается succeeded
      Тогда у S создан запуск kind forget, trigger after_backup, requestedBy null, status queued
      И его repository — repositoryId "r0", repositoryName "qa"
      И G получает шаг FORGET

      Примеры:
        | триггер  |
        | manual   |
        | schedule |
        | catch_up |

    @http @grpc
    Структура сценария: Неуспешный бэкап forget не создаёт
      Когда бэкап S завершается <итог>
      Тогда запусков kind forget у S нет

      Примеры:
        | итог                                 |
        | failed без снимка                    |
        | failed со снимком, partial true      |
        | cancelled                            |
        | failed с шагом timed_out             |
        | failed с шагом lost                  |
        | agent_unavailable                    |

    @http @grpc
    Сценарий: Поздний успешный итог потерянного шага forget не создаёт
      Дано бэкап S закрыт сервером как failed с шагом lost
      Когда G присылает поздний SUCCEEDED с выводом бэкапа
      Тогда строка snapshots записана
      И запусков kind forget у S нет

    @http @grpc
    Структура сценария: Восстановление и проверка восстановления forget не создают
      Когда <запуск> завершается succeeded
      Тогда запусков kind forget у S нет

      Примеры:
        | запуск                                  |
        | запуск restore снимка S                 |
        | попытка проверки восстановления S       |

    @http @grpc
    Сценарий: Пока forget источника не завершён, новый после бэкапа не создаётся
      Дано запуск forget S в queued, потому что идёт prune репозитория r0
      Когда ещё один бэкап S завершается succeeded
      Тогда у S ровно один незавершённый запуск forget

    @http @grpc
    Сценарий: Сбой создания forget не меняет итог бэкапа
      Дано создание запуска forget падает с ошибкой базы
      Когда бэкап S завершается succeeded
      Тогда бэкап S succeeded
      И журнал сервера содержит строку ERROR о создании forget источника S
      И результат шага бэкапа подтверждён агенту ResultAck

    @http @grpc
    Сценарий: Бэкап самобэкапа с политикой запускает forget
      Дано системный источник «Sard: database» с политикой «стандарт» на встроенном агенте, который один видит свой репозиторий
      Когда бэкап самобэкапа этого источника завершается succeeded
      Тогда у него создан запуск kind forget, trigger after_backup

  # ===========================================================================
  Правило: Забывание можно запустить вручную из консоли

    @http @grpc @qa
    Сценарий: Ручной forget создаёт запуск и отправляет шаг
      Когда администратор отправляет POST forget источника S
      Тогда ответ 201 Run с kind forget, trigger manual, requestedBy "admin", status queued, sourceId S, agentId G
      И G получает шаг FORGET

    @http
    Сценарий: Второй ручной forget, пока первый не завершён, отклоняется со ссылкой
      Дано администратор отправил POST forget источника S, запуск F в running
      Когда администратор снова отправляет POST forget источника S
      Тогда ответ 409 maintenance_active с activeRunId F
      И запусков не прибавилось

    @http @grpc
    Сценарий: Одновременные ручной forget и завершение бэкапа дают один forget
      Когда в один момент администратор отправляет POST forget источника S и бэкап S завершается succeeded
      Тогда у S ровно один незавершённый запуск forget
      И если ответ POST — 409, то в нём activeRunId этого запуска

    @http
    Сценарий: Двадцать одновременных ручных forget дают один запуск
      Когда 20 потоков одновременно отправляют POST forget источника S
      Тогда ровно один ответ 201
      И остальные 19 — 409 maintenance_active с activeRunId созданного запуска

    @http
    Сценарий: Ручной forget удалённого источника недоступен
      Дано источник S мягко удалён
      Когда администратор отправляет POST forget источника S
      Тогда ответ 404 not_found

    @http @grpc
    Сценарий: Удаление источника не отменяет его уже созданный forget
      Дано запуск forget S в running
      Когда администратор удаляет источник S
      Тогда запуск forget S по-прежнему running
      И после итога агента он завершается по этому итогу

    @http
    Сценарий: Ручной forget с ограничением онбординга full отклоняется
      Дано онбординг не завершён, ограничение full
      Когда администратор отправляет POST forget источника S
      Тогда ответ 409 onboarding_incomplete

  # ===========================================================================
  Правило: Сервер передаёт агенту только политику и подсказки, без списка удаления

    @grpc @qa
    Сценарий: Шаг FORGET несёт политику источника и ничего лишнего
      Дано у S своя политика keepLast 3, keepDaily 7 и keepWithinDays 30
      Когда G получает шаг FORGET источника S
      Тогда RunStep — action ACTION_FORGET, repository_name "qa", пустые snapshot_id, plugin, config_json и tags, без verify
      И ForgetTask source_id равен id S
      И policy — keep_last 3, keep_daily 7, keep_within 720 ч, остальные поля 0
      И timeout равен 1 ч

    @grpc
    Сценарий: Последний проверенный снимок передаётся как защищённый
      Дано у S снимки s1, s2, s3, последняя проверка passed — снимка s1, более поздняя проверка failed — снимка s2
      Когда G получает шаг FORGET источника S
      Тогда keep_snapshot_ids — restic id s1

    @grpc @вр3 @выпуск2
    Сценарий: Снимки идущего восстановления и открытой проверки передаются как защищённые
      Дано у S запуск restore снимка s2 в running и открытое задание проверки снимка s3
      Когда G получает шаг FORGET источника S
      Тогда keep_snapshot_ids содержит restic id s2 и s3

    @grpc
    Сценарий: Без проверок, восстановлений и неполных снимков списки подсказок пусты
      Дано у S нет проверок passed, восстановлений и снимков partial
      Когда G получает шаг FORGET источника S
      Тогда keep_snapshot_ids и incomplete_snapshot_ids пусты
      И incomplete_max_age не задан

    @grpc
    Сценарий: Неполные снимки передаются подсказкой со сроком семь дней
      Дано у S снимки s1 и s2 с partial true и снимок s3 с partial false
      Когда G получает шаг FORGET источника S
      Тогда incomplete_snapshot_ids — restic id s1 и s2
      И incomplete_max_age равен 168 ч

    @grpc
    Сценарий: Забытые снимки и снимки другого репозитория в подсказки не попадают
      Дано у S забытый неполный снимок s1, последняя проверка passed — снимка s4 в репозитории с repository_id "r9"
      Когда G получает шаг FORGET источника S
      Тогда keep_snapshot_ids и incomplete_snapshot_ids не содержат s1 и s4

    @grpc
    Сценарий: Политика берётся на момент отправки, а не создания запуска
      Дано запуск forget S в queued при политике «стандарт», G офлайн
      И администратор меняет политику S на «минимум»
      Когда G подключается и получает шаг FORGET
      Тогда policy — keep_daily 7, остальные поля 0

    @grpc
    Сценарий: Политика, снятая до отправки, не отправляется
      Дано запуск forget S в queued, G офлайн
      И администратор снимает политику S
      Когда G подключается
      Тогда G не получил шага FORGET
      И запуск forget failed с сообщением "retention policy removed before dispatch"

    @grpc
    Сценарий: Повторная отправка после переподключения несёт то же задание
      Дано G получил шаг FORGET, принял его и отключился
      И администратор меняет политику S
      Когда G переподключается без этого шага в Hello
      Тогда G получает тот же command_id с той же policy и теми же списками

    @grpc
    Сценарий: Шаг FORGET для другого имени репозитория у обслуживающего агента
      Дано репозиторий r0 у G называется "main", источник S создан с этим именем
      Когда G получает шаг FORGET источника S
      Тогда repository_name равен "main"

  # ===========================================================================
  Правило: На репозиторий — не больше одного обслуживания одновременно

    @http @grpc @qa
    Сценарий: Второе забывание того же репозитория ждёт в очереди
      Когда бэкапы S и T завершаются succeeded один за другим
      Тогда G получил шаг FORGET источника S
      И forget T в queued, шага FORGET источника T G не получил

    @http @grpc
    Сценарий: Итог забывания отправляет следующее обслуживание репозитория
      Дано forget S в running и forget T в queued
      Когда G присылает итог forget S
      Тогда G получает шаг FORGET источника T

    @http @grpc
    Сценарий: Забывание ждёт идущего prune репозитория
      Дано запуск prune r0 в running
      Когда бэкап S завершается succeeded
      Тогда forget S в queued, G не получил шага FORGET

    @http @grpc
    Сценарий: Итог prune отправляет ждущее забывание
      Дано запуск prune r0 в running и forget S в queued
      Когда G присылает итог prune
      Тогда G получает шаг FORGET источника S

    @http @grpc
    Сценарий: Забывания разных репозиториев идут одновременно
      Дано источник Q на G с репозиторием "other", repository_id "r1"
      Когда бэкапы S и Q завершаются succeeded
      Тогда G получил шаги FORGET обоих источников, не дожидаясь итогов

    @http @grpc
    Сценарий: Ожидание в очереди обслуживания при онлайн-агенте не считается ожиданием агента
      Дано forget T в queued за идущим forget S, G онлайн
      Когда администратор запрашивает запуск forget T
      Тогда outcome queued, а не waiting_for_agent

    @server
    Сценарий: Два экземпляра сервера не отправляют два обслуживания одного репозитория
      Дано два экземпляра сервера на одной базе и в очереди forget S и forget T одного репозитория r0
      Когда оба экземпляра одновременно выполняют отправку шагов
      Тогда в dispatched или running ровно один запуск обслуживания r0

    @http @grpc
    Сценарий: Бэкап не ждёт очереди обслуживания
      Дано forget S в running
      Когда администратор запускает бэкап T
      Тогда G получает шаг BACKUP T, не дожидаясь итога forget S

    @http @grpc
    Сценарий: Обслуживание не занимает источник для бэкапа
      Дано forget S в running
      Когда администратор запускает бэкап S
      Тогда ответ 201, а не 409 run_active

  # ===========================================================================
  Правило: Итог забывания помечает снимки забытыми и честно называет причину

    @http @grpc @qa
    Сценарий: Забытые агентом снимки помечаются удалёнными по политике
      Дано у S снимки s1, s2, s3
      Когда G присылает итог FORGET SUCCEEDED с forgotten_snapshot_ids s1, s2, remaining_snapshot_ids s3 и listed_at T0
      Тогда у s1 и s2 forgottenAt T0 и forgottenReason policy
      И у s3 forgottenAt null
      И запуск forget succeeded

    @http @grpc
    Сценарий: Шаг FORGET в REST показывает сводку итога
      Когда G присылает итог FORGET SUCCEEDED с 4 забытыми, 6 сохранёнными пределом и 9 оставшимися снимками и listed_at T0
      Тогда шаг forget в REST несёт forgottenCount 4, keptByFloorCount 6, remainingCount 9, listedAt T0 и failure null
      И его policy — отправленная политика

    @http @grpc
    Сценарий: Снимок, которого нет в репозитории, помечается не найденным
      Дано у S снимки s1, s2, s3, их бэкапы зафиксированы до отправки шага FORGET
      Когда G присылает итог FORGET SUCCEEDED с пустым forgotten_snapshot_ids, remaining_snapshot_ids s3 и listed_at T0
      Тогда у s1 и s2 forgottenAt T0 и forgottenReason missing

    @http @grpc
    Сценарий: Снимок бэкапа, зафиксированный после отправки FORGET, сверкой не помечается
      Дано шаг FORGET S отправлен в T0 − 10 мин
      И бэкап S со снимком s9 зафиксирован в T0 − 5 мин
      Когда G присылает итог FORGET с remaining_snapshot_ids без s9 и listed_at T0 − 1 мин
      Тогда у s9 forgottenAt null

    @http @grpc
    Сценарий: Итог провала с перечислением тоже помечает забытые снимки
      Когда G присылает итог FORGET FAILED с failure MAINTENANCE_FAILURE_STORAGE_UNAVAILABLE, forgotten_snapshot_ids s1, remaining_snapshot_ids s2, s3 и listed_at T0
      Тогда у s1 forgottenAt T0 и forgottenReason policy
      И запуск forget failed, шаг failed с failure storage_unavailable

    @http @grpc
    Структура сценария: Итог без перечисления ничего не сверяет
      Когда G присылает итог FORGET <статус> с ForgetOutput только с failure <сбой>
      Тогда ни у одного снимка S forgottenAt не изменился
      И шаг forget несёт failure <в REST> и listedAt null

      Примеры:
        | статус    | сбой                                    | в REST              |
        | FAILED    | MAINTENANCE_FAILURE_STORAGE_UNAVAILABLE | storage_unavailable |
        | FAILED    | MAINTENANCE_FAILURE_REPOSITORY_LOCKED   | repository_locked   |
        | FAILED    | MAINTENANCE_FAILURE_DELETE_DENIED       | delete_denied       |
        | CANCELLED | MAINTENANCE_FAILURE_UNSPECIFIED         | null                |

    @http @grpc @qa
    Сценарий: Отказ хранилища в удалении даёт провал с причиной
      Когда G присылает итог FORGET FAILED с сообщением "storage account has no delete permission: assign repository maintenance to a host whose account can delete" и failure MAINTENANCE_FAILURE_DELETE_DENIED
      Тогда запуск forget failed с этим сообщением
      И lastForget источника S несёт failure delete_denied и это сообщение

    @http @grpc
    Сценарий: Занятый репозиторий называет держателя блокировки
      Когда G присылает итог FORGET FAILED с failure MAINTENANCE_FAILURE_REPOSITORY_LOCKED и lock_holder "PID 4242 on db2 by sard-agent"
      Тогда шаг forget несёт failure repository_locked и lockHolder "PID 4242 on db2 by sard-agent"

    @http @grpc
    Сценарий: Отказ агента от шага FORGET виден как отклонение
      Когда G присылает итог FORGET REJECTED с сообщением "retention policy is empty"
      Тогда запуск forget failed, шаг rejected с этим сообщением
      И ни один снимок не помечен забытым

    @http @grpc
    Сценарий: Забытые id другого источника не помечают его снимки
      Дано у T снимок t1 в r0
      Когда G присылает итог FORGET источника S с forgotten_snapshot_ids t1 и listed_at T0
      Тогда у t1 forgottenAt null

    @http @grpc
    Сценарий: Неизвестные серверу id в итоге ничего не меняют
      Когда G присылает итог FORGET с forgotten_snapshot_ids двух id частей и одного id, которого нет в snapshots, и listed_at T0
      Тогда итог принят, запуск succeeded
      И ни одна строка snapshots не изменилась

    @http @grpc
    Сценарий: Снимок того же источника в другом репозитории не помечается
      Дано у S снимок s7 в репозитории с repository_id "r9"
      Когда G присылает итог FORGET репозитория r0 с remaining_snapshot_ids без s7 и listed_at T0
      Тогда у s7 forgottenAt null

    @http @grpc
    Сценарий: Повторный итог не меняет уже записанное время забывания
      Дано итог FORGET с forgotten_snapshot_ids s1 и listed_at T0 записан и подтверждён
      Когда G присылает тот же итог повторно с listed_at T0 плюс 1 ч
      Тогда у s1 forgottenAt T0
      И G получает ResultAck

    @http @grpc
    Сценарий: Поздний итог FORGET после потери шага всё равно помечает забытые снимки
      Дано шаг forget S закрыт сервером как lost
      Когда G присылает поздний итог SUCCEEDED с forgotten_snapshot_ids s1 и listed_at T0
      Тогда у s1 forgottenAt T0 и forgottenReason policy
      И статус запуска forget не изменился, lateStatus succeeded

    @http @grpc
    Сценарий: Итог с забытыми снимками без времени перечисления — неверный итог
      Когда G присылает итог FORGET SUCCEEDED с forgotten_snapshot_ids s1 и без listed_at
      Тогда запуск forget failed с сообщением, начинающимся с "invalid result"
      И у s1 forgottenAt null

    @http @grpc
    Сценарий: Итог FORGET с выводом бэкапа — неверный итог
      Когда G присылает итог шага FORGET SUCCEEDED с BackupOutput вместо ForgetOutput
      Тогда запуск forget failed с сообщением, начинающимся с "invalid result"

    @http @grpc
    Сценарий: Десять тысяч забытых снимков помечаются одним итогом
      Дано у S 10001 снимок
      Когда G присылает итог FORGET с 10000 из них в forgotten_snapshot_ids и listed_at T0
      Тогда ровно 10000 строк snapshots S получили forgottenAt T0
      И итог подтверждён ResultAck не позже чем через 30 секунд

  # ===========================================================================
  Правило: Забытый снимок не восстанавливается и не проверяется

    @http @qa
    Сценарий: Забытый снимок виден в списке с датой и не восстанавливается
      Дано снимок s1 S забыт по политике в T0
      Когда администратор запрашивает снимки S
      Тогда s1 в списке с forgottenAt T0, forgottenReason policy, restorable false и restoreBlockedReason forgotten

    @http
    Сценарий: Восстановление забытого снимка отклоняется
      Дано снимок s1 S забыт
      Когда администратор отправляет POST restores снимка s1
      Тогда ответ 422 snapshot_not_restorable
      И запусков не прибавилось

    @server
    Сценарий: Цикл проверки не выбирает забытый снимок
      Дано у S самый новый целый снимок s3 забыт, снимок s2 целый и не забыт
      Когда цикл проверки создаёт задание для S
      Тогда задание проверяет снимок s2

    @http
    Сценарий: Проверка по запросу не выбирает забытый снимок
      Дано у S все целые снимки, кроме s1, забыты
      Когда администратор отправляет POST verifications источника S
      Тогда созданное задание проверяет снимок s1

    @server
    Сценарий: Источник, все снимки которого забыты, не попадает в проверку
      Дано у S все снимки забыты
      Когда цикл проверки проходит по источникам
      Тогда задание для S не создано

  # ===========================================================================
  Правило: Хранилище очищается вручную командой prune репозитория

    @http @grpc @qa
    Сценарий: Ручной prune создаёт запуск репозитория и отправляет шаг
      Когда администратор отправляет POST prune репозитория r0
      Тогда ответ 201 Run с kind prune, trigger manual, requestedBy "admin", status queued, sourceId null, sourceName null, agentId G
      И repository — repositoryId "r0", repositoryName "qa"
      И G получает RunStep с action ACTION_PRUNE, repository_name "qa", пустым PruneTask, timeout 12 ч и пустыми plugin, config_json, tags и snapshot_id

    @http @grpc @qa
    Сценарий: Итог prune несёт освобождённое место и размер репозитория
      Когда G присылает итог PRUNE SUCCEEDED с freed_bytes 1073741824 и repository_size_bytes 53687091200
      Тогда запуск prune succeeded
      И шаг prune несёт freedBytes 1073741824 и repositorySizeBytes 53687091200
      И lastPrune в карточке хранения S и T несёт те же числа

    @http @grpc
    Сценарий: Итог prune без сводки restic показывает неизвестные числа
      Когда G присылает итог PRUNE SUCCEEDED без freed_bytes и repository_size_bytes
      Тогда шаг prune несёт freedBytes null и repositorySizeBytes null

    @http @grpc
    Структура сценария: Провал prune несёт причину
      Когда G присылает итог PRUNE FAILED с failure <сбой> и сообщением "<сообщение>"
      Тогда запуск prune failed с этим сообщением, шаг prune несёт failure <в REST>

      Примеры:
        | сбой                                    | в REST              | сообщение                                                                                      |
        | MAINTENANCE_FAILURE_DELETE_DENIED       | delete_denied       | storage account has no delete permission: assign repository maintenance to a host whose account can delete |
        | MAINTENANCE_FAILURE_REPOSITORY_LOCKED   | repository_locked   | repository is locked                                                                           |
        | MAINTENANCE_FAILURE_STORAGE_UNAVAILABLE | storage_unavailable | storage unavailable                                                                            |

    @http @grpc
    Сценарий: Итог prune ничего не помечает забытым
      Дано у S снимки s1, s2
      Когда G присылает итог PRUNE SUCCEEDED
      Тогда у s1 и s2 forgottenAt null

    @http
    Сценарий: Второй prune того же репозитория, пока первый не завершён, отклоняется
      Дано запуск prune P репозитория r0 в queued
      Когда администратор отправляет POST prune репозитория r0
      Тогда ответ 409 maintenance_active с activeRunId P

    @http
    Сценарий: Prune неизвестного или чужого репозитория недоступен
      Когда администратор отправляет POST prune репозитория "r404", который не сообщает ни один агент тенанта
      Тогда ответ 404 not_found

    @http @grpc
    Сценарий: Prune репозитория, который видят два агента роли source, отклоняется
      Дано агент H тоже видит r0
      Когда администратор отправляет POST prune репозитория r0
      Тогда ответ 409 maintenance_owner_required

    @http @grpc
    Сценарий: Prune ждёт идущего forget репозитория
      Дано forget S в running
      Когда администратор отправляет POST prune репозитория r0
      Тогда ответ 201 со status queued
      И G не получил шага PRUNE до итога forget S

    @http @grpc
    Сценарий: Prune не создаётся автоматически
      Дано бэкап S завершился succeeded, создан forget S
      Когда G присылает итог forget S SUCCEEDED
      Тогда запусков kind prune нет

    @http @grpc @вр3 @выпуск2
    Сценарий: Prune при идущем восстановлении из этого репозитория отклоняется
      Дано запуск restore снимка S в running
      Когда администратор отправляет POST prune репозитория r0
      Тогда ответ 409 repository_busy
      И запусков kind prune нет

    @http @grpc @вр3 @выпуск2
    Сценарий: Prune в очереди не отправляется, пока идёт проверка восстановления репозитория
      Дано prune r0 в queued и попытка проверки снимка S в running
      Когда наступает отправка шагов G
      Тогда G не получил шага PRUNE

    @http @grpc @вр3 @выпуск2
    Сценарий: Prune в очереди отправляется после завершения проверки восстановления
      Дано prune r0 в queued и попытка проверки снимка S в running
      Когда попытка проверки завершается
      Тогда G получает шаг PRUNE

    @http
    Сценарий: Prune с ограничением онбординга full отклоняется
      Дано онбординг не завершён, ограничение full
      Когда администратор отправляет POST prune репозитория r0
      Тогда ответ 409 onboarding_incomplete

  # ===========================================================================
  Правило: Запуски обслуживания живут по правилам J1 — статусы, сроки, отмена, ожидание агента

    @http @grpc
    Сценарий: Список запусков фильтруется по видам обслуживания
      Дано у S запуски backup, forget и restore, у r0 запуск prune
      Когда администратор запрашивает GET runs с kind forget
      Тогда в ответе только запуск forget
      И GET runs с kind prune отдаёт только запуск prune с sourceId null

    @http @grpc
    Сценарий: Отмена запуска forget в очереди не отправляет шаг
      Дано forget S в queued
      Когда администратор отправляет POST cancel этого запуска
      Тогда ответ 200 со status cancelled
      И G не получил шага FORGET

    @http @grpc @qa
    Сценарий: Отмена идущего prune отправляет агенту CancelStep
      Дано prune r0 в running
      Когда администратор отправляет POST cancel этого запуска
      Тогда G получает CancelStep этого шага
      И после итога CANCELLED запуск cancelled

    @http @grpc
    Сценарий: Срок шага FORGET задаётся настройкой
      Дано sard.maintenance.forget-timeout равен 30m
      Когда G получает шаг FORGET
      Тогда timeout равен 30 мин
      И шаг forget в REST несёт timeoutSeconds 1800

    @http @grpc
    Сценарий: Агент, молчащий дольше срока prune, даёт timed_out сервером
      Дано sard.maintenance.prune-timeout равен 1h и G принял шаг PRUNE в T0
      Когда наступает T0 плюс 1 ч плюс окно потери, итога нет, G на связи
      Тогда шаг prune timed_out с timedOutBy server
      И G получает CancelStep

    @http @grpc
    Сценарий: Ручной forget для офлайн-агента ждёт его до срока ожидания
      Дано G офлайн, sard.run.agent-wait.max 24h
      Когда администратор отправляет POST forget источника S в T0
      Тогда запуск forget queued с outcome waiting_for_agent и agentWaitUntil T0 плюс 24 ч

    @http @grpc
    Сценарий: Forget, не дождавшийся агента, завершается агент недоступен
      Дано forget S в queued с agentWaitUntil T0 плюс 24 ч, G офлайн
      Когда наступает T0 плюс 24 ч
      Тогда запуск forget agent_unavailable

    @http @grpc
    Сценарий: Отзыв обслуживающего агента закрывает его обслуживание как потерянное
      Дано forget S в running
      Когда администратор отзывает агента G
      Тогда шаг forget lost с сообщением "agent revoked"

    @startup
    Структура сценария: Недопустимый срок обслуживания останавливает старт
      Дано <настройка> равна <значение>
      Когда сервер стартует
      Тогда старт отклонён сообщением, называющим <настройка>

      Примеры:
        | настройка                        | значение |
        | sard.maintenance.forget-timeout  | 0s       |
        | sard.maintenance.forget-timeout  | -1h      |
        | sard.maintenance.prune-timeout   | soon     |

  # ===========================================================================
  Правило: Обслуживание не выдаёт себя за бэкап

    @server
    Структура сценария: Запуски forget и prune не дают уведомлений
      Дано настроен канал Telegram
      Когда запуск <вид> завершается <статус>
      Тогда строк notification_deliveries для этого запуска нет

      Примеры:
        | вид              | статус    |
        | forget S ручной  | succeeded |
        | forget S ручной  | failed    |
        | forget S после бэкапа | failed |
        | prune r0         | succeeded |
        | prune r0         | failed    |

    @server
    Сценарий: Успешный forget не закрывает эпизод просрочки расписания
      Дано у S открыт эпизод просрочки бэкапа
      Когда запуск forget S завершается succeeded
      Тогда эпизод просрочки открыт

    @server
    Сценарий: Успешный forget не двигает метрику последнего успешного бэкапа
      Дано последний успешный бэкап S завершился в T0 минус 2 д
      Когда запуск forget S завершается succeeded в T0 и проходит тик метрик
      Тогда sard_source_last_backup_success_timestamp_seconds источника S равен T0 минус 2 д

    @http
    Сценарий: Успешный forget системного источника не засчитывается как первый бэкап самобэкапа
      Дано самобэкап привязан, бэкапов системных источников после привязки нет
      Когда ручной forget каждого системного источника завершается succeeded
      Тогда GET self-backup отдаёт firstBackupDone false

  # ===========================================================================
  Правило: Самобэкап получает политику как обычный источник

    @http @qa
    Сценарий: Политику системного источника можно задать
      Дано системный источник «Sard: database»
      Когда администратор отправляет PUT retention этого источника с policy preset minimum
      Тогда ответ 200
      И замена этого источника через PUT sources по-прежнему отвечает 409 system_source

    @http
    Сценарий: Системные источники, созданные привязкой после обновления, получают политику по умолчанию
      Дано политика по умолчанию «стандарт», самобэкап не привязан
      Когда администратор привязывает репозиторий самобэкапа
      Тогда у обоих системных источников retention «стандарт»

  # ===========================================================================
  Правило: Уборка H1 и запуски забытых снимков и обслуживания

    # Выпуск 1: уборка H1 не меняется (server-retention.feature как есть).
    @server
    Сценарий: Уборка выпуска 1 не удаляет запуск забытого снимка и строку снимка
      Дано старый запуск backup S succeeded, его снимок забыт, у S есть более свежий успешный бэкап
      Когда проходит уборка
      Тогда запуск, его шаги и строка snapshots на месте с прежним forgottenAt

    @server
    Сценарий: Уборка выпуска 1 не удаляет запуски forget и prune
      Дано старые завершённые запуски forget S и prune r0, succeeded и failed
      Когда проходит уборка
      Тогда все эти запуски и их шаги на месте
      И проход завершается без ошибки в журнале сервера

    @server @вр1 @выпуск2
    Сценарий: Старый запуск бэкапа забытого снимка удаляется вместе со строкой снимка
      Дано старый запуск backup S succeeded, его снимок забыт, на снимок не ссылаются задание и запись проверки
      И у S есть более свежий успешный бэкап
      Когда проходит уборка
      Тогда запуск, его шаги и строка snapshots удалены
      И итог прохода называет snapshot=1

    @server @вр1 @выпуск2
    Сценарий: Запуск незабытого снимка по-прежнему не удаляется
      Дано старый запуск backup S succeeded, его снимок не забыт
      Когда проходит уборка
      Тогда запуск и строка snapshots на месте

    @server @вр1 @выпуск2
    Сценарий: Забытый снимок с записью проверки остаётся вместе со своим запуском
      Дано старый запуск backup S, его снимок забыт, на снимок ссылается запись проверки passed
      Когда проходит уборка
      Тогда запуск и строка snapshots на месте

    @server @вр1 @выпуск2
    Сценарий: Свежий запуск забытого снимка не удаляется до срока
      Дано запуск backup S завершён ровно на границе срока запусков, его снимок забыт
      Когда проходит уборка
      Тогда запуск и строка snapshots на месте

    @server @вр1 @выпуск2
    Сценарий: Последний успешный бэкап остаётся, даже если его снимок забыт
      Дано единственный успешный бэкап S старый, его снимок забыт
      Когда проходит уборка
      Тогда запуск и строка snapshots на месте

    @server @выпуск2
    Структура сценария: Старые запуски обслуживания удаляются, последний успешный остаётся
      Дано у <чей> 3 старых запуска <вид> со статусом succeeded и 2 старых со статусом failed
      Когда проходит уборка
      Тогда остался только последний по finished_at succeeded запуск <вид>
      И 4 остальных удалены

      Примеры:
        | чей              | вид    |
        | источника S      | forget |
        | репозитория r0   | prune  |

    @server @выпуск2
    Сценарий: Последний успешный forget одного источника не защищает forget другого
      Дано у S и у T по одному старому успешному forget
      Когда проходит уборка
      Тогда оба запуска на месте

    @server @выпуск2
    Сценарий: Активный запуск обслуживания не удаляется
      Дано старый по queuedAt запуск prune r0 в running
      Когда проходит уборка
      Тогда запуск на месте

    @server @вр1 @выпуск2
    Сценарий: Восстановление из консоли видит все незабытые снимки после уборки
      Дано у S 800 старых снимков, 300 из них забыты и не проверялись, и у S есть свежий успешный бэкап
      Когда проходит уборка
      Тогда список снимков S содержит все 500 незабытых
      И варианты восстановления каждого из них отдаются с ответом 200

  # ===========================================================================
  Правило: Метрики считают обслуживание отдельными видами

    @server
    Сценарий: Гистограмма длительности есть для всех видов, включая обслуживание
      Дано сервер стартовал, запусков не было
      Когда клиент делает scrape
      Тогда sard_run_duration_seconds_count есть для каждой из 15 комбинаций kind backup, restore, verify, forget, prune и status succeeded, failed, cancelled со значением 0

    @server
    Сценарий: Завершённый forget попадает в гистограмму своего вида
      Когда запуск forget начат в T0 и завершается succeeded в T0 плюс 40 секунд
      Тогда sard_run_duration_seconds_count с kind forget и status succeeded вырос на 1
      И sard_run_duration_seconds_sum с kind forget вырос на 40
      И sard_run_duration_seconds_count с kind backup не изменился

    @server
    Сценарий: Активные запуски обслуживания считаются по виду
      Дано 2 запуска forget в queued, 1 prune в running и 1 backup в running
      Когда проходит тик
      Тогда sard_runs_active с kind forget и status queued равен 2
      И sard_runs_active с kind prune и status running равен 1
      И sard_runs_active с kind backup и status running равен 1
      И остальные 12 серий sard_runs_active равны 0

    @server
    Сценарий: Счётчик завершённых запусков учитывает обслуживание
      Когда запуск prune завершается failed
      Тогда sard_run_finished_total с status failed вырос на 1

  # ===========================================================================
  Правило: Новые операции подчинены политике доступа, события называют вид и субъекта

    @http
    Структура сценария: Политика доступа видит новые операции по шаблону пути
      Дано политика доступа, которая запрещает операцию <операция> и записывает вызовы
      Когда администратор выполняет <операция>
      Тогда ответ 403 access_denied
      И политика получила операцию <операция> ровно один раз
      И ничего не изменилось и запусков не прибавилось

      Примеры:
        | операция                                         |
        | GET /api/v1/sources/{sourceId}/retention          |
        | PUT /api/v1/sources/{sourceId}/retention          |
        | GET /api/v1/retention-defaults                    |
        | PUT /api/v1/retention-defaults                    |
        | POST /api/v1/sources/{sourceId}/forget            |
        | POST /api/v1/repositories/{repositoryId}/prune    |

    @http
    Структура сценария: Изменение политики даёт событие настроек
      Когда администратор выполняет <запрос>, который меняет сохранённую политику
      Тогда событие settings.changed получено с setting <setting>, objectId <объект> и actor — субъект сессии

      Примеры:
        | запрос                      | setting            | объект    |
        | PUT retention источника S   | source_retention   | id S      |
        | PUT retention-defaults      | retention_defaults | null      |

    @http @grpc
    Сценарий: Ручной forget даёт событие создания с субъектом
      Когда администратор отправляет POST forget источника S
      Тогда событие run.created получено с kind forget, trigger manual, sourceId S и actor — субъект сессии администратора

    @http @grpc
    Сценарий: Итог ручного forget даёт события начала и завершения с видом forget
      Дано администратор отправил POST forget источника S
      Когда G принимает шаг и присылает итог SUCCEEDED
      Тогда события run.started и итоговое run.status_changed получены с kind forget, trigger manual и sourceId S

    @http @grpc
    Сценарий: Forget после бэкапа даёт события без субъекта
      Когда бэкап S завершается succeeded и создаётся forget
      Тогда событие run.created этого forget получено с kind forget, trigger after_backup и actor null

    @http @grpc @вр2
    Сценарий: Ручной prune в выпуске 1 событий запуска не даёт
      Дано администратор отправил POST prune репозитория r0 и получил 201
      Когда G принимает шаг и присылает итог PRUNE SUCCEEDED
      Тогда запуск prune succeeded
      И событий run.created, run.started и run.status_changed с runId этого запуска нет

    @http @grpc @вр2 @выпуск2
    Сценарий: Ручной prune даёт события запуска с репозиторием и без источника
      Когда администратор отправляет POST prune репозитория r0
      Тогда событие run.created получено с kind prune, trigger manual, sourceId null, repositoryId "r0" и actor — субъект сессии

    @http
    Сценарий: Отклонённый запрос обслуживания событий не даёт
      Дано у источника S нет политики
      Когда администратор отправляет POST forget источника S
      Тогда ответ 409 retention_not_configured
      И событий run.created нет

    @http @contract
    Сценарий: Новые операции и поля описаны в OpenAPI
      Когда клиент запрашивает /v3/api-docs
      Тогда в нём есть операции К3–К6 с ответами 401, 403 и 503
      И RunKind содержит forget и prune, RunTrigger — after_backup, StepAction — forget и prune
      И Snapshot содержит forgottenReason, Source — retention
      И ErrorCode содержит retention_not_configured, maintenance_owner_required, agent_outdated и maintenance_active

  # ===========================================================================
  Правило: Предел хоста сравнивается с политикой на сервере

    @unit
    Структура сценария: Признак «предел хоста держит больше политики»
      Дано политика <политика> и предел хоста <предел>
      Когда сервер вычисляет floorExtendsPolicy
      Тогда результат <признак>

      Примеры:
        | политика                         | предел                        | признак |
        | стандарт                         | 7 снимков и 14 дней           | true    |
        | своя keepLast 7, keepWithinDays 14 | 7 снимков и 14 дней         | false   |
        | своя keepLast 10, keepWithinDays 30 | 7 снимков и 14 дней        | false   |
        | своя keepLast 10, keepWithinDays 13 | 7 снимков и 14 дней        | true    |
        | своя keepLast 6, keepWithinDays 30  | 7 снимков и 14 дней        | true    |
        | стандарт                         | выключен                      | false   |
        | стандарт                         | нет предела                   | false   |
        | нет политики                     | 7 снимков и 14 дней           | false   |
