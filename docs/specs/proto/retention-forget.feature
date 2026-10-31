# language: ru
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# L1a. Хранение по сроку — контракт: шаг FORGET с политикой, итог забывания и
# нижний предел хоста в Register. Агент — docs/specs/agent/retention-forget.feature
# (там же разбиение L1 на срезы, вопросы владельца ВL1–ВL5 и решения КL).
# Ручная проверка — docs/qa/retention-forget.md.
#
# СТАТУС: ответы владельца на ВL1–ВL5 внесены 2026-10-31 (см. agent-файл).
# КП1–КП5 не оспорены и сохранены; КП6 [НОВОЕ 2026-10-31] ждёт явного
# утверждения. Имена полей — предложение specifier, окончательные имена —
# coder при сохранении смысла. Сверено с main 2026-10-31: номера Action 5,
# RunStep 12, StepResult 15 и RepositoryInfo 6 свободны и не зарезервированы.
#
# Решения specifier
#   КП1. Новое действие ACTION_FORGET = 5 в enum Action. ACTION_PRUNE и
#        ACTION_CHECK добавляет срез L1c, не этот.
#   КП2. RunStep.forget = 12 (ForgetTask) — что забывать, только для
#        ACTION_FORGET:
#          message ForgetTask {
#            string source_id = 1;                 // значение метки sard.source
#            RetentionPolicy policy = 2;
#            repeated string keep_snapshot_ids = 3; // только ДОБАВЛЯЕТ защиту
#                                                   // (правило 11: последний
#                                                   // проверенный снимок)
#            repeated string incomplete_snapshot_ids = 4; // подсказка сервера
#                                                   // о неполных снимках (правило 5)
#            google.protobuf.Duration incomplete_max_age = 5;
#          }
#          message RetentionPolicy {
#            uint32 keep_last = 1; uint32 keep_daily = 2; uint32 keep_weekly = 3;
#            uint32 keep_monthly = 4; uint32 keep_yearly = 5;
#            google.protobuf.Duration keep_within = 6;
#          }
#        Поля со списком снимков К УДАЛЕНИЮ в контракте нет (правило 8 описания:
#        сервер передаёт только политику). Это закреплено сценарием ниже.
#   КП3. StepResult.output: ForgetOutput forget = 15 (14 занят reject_reason
#        вне oneof):
#          message ForgetOutput {
#            repeated string forgotten_snapshot_ids = 1;     // полные id, основные
#                                                            // и части, которых
#                                                            // больше нет в репозитории
#            repeated string kept_by_floor_snapshot_ids = 2; // политика забыла бы,
#                                                            // предел хоста сохранил
#            repeated string remaining_snapshot_ids = 3;     // все снимки источника
#                                                            // после forget
#            google.protobuf.Timestamp listed_at = 4;        // когда агент перечислил
#                                                            // оставшиеся
#            MaintenanceFailure failure = 5;
#            string lock_holder = 6;  // кто держит блокировку: хост, PID,
#                                     // пользователь, время — как говорит restic
#          }
#          enum MaintenanceFailure {
#            MAINTENANCE_FAILURE_UNSPECIFIED = 0;
#            MAINTENANCE_FAILURE_REPOSITORY_LOCKED = 1;
#            MAINTENANCE_FAILURE_DELETE_DENIED = 2;
#            MAINTENANCE_FAILURE_STORAGE_UNAVAILABLE = 3;
#          }
#        Вывод заполняется и при FAILED, CANCELLED, TIMED_OUT, если агент успел
#        перечислить снимки: сервер должен знать, что уже забыто (КL9).
#   КП4. RepositoryInfo.retention_floor = 6 (RetentionFloor) — нижний предел
#        хоста для этого репозитория:
#          message RetentionFloor {
#            bool disabled = 1;
#            uint32 keep_last = 2;
#            google.protobuf.Duration keep_within = 3;
#            RetentionFloorOrigin origin = 4;
#          }
#          enum RetentionFloorOrigin {
#            RETENTION_FLOOR_ORIGIN_UNSPECIFIED = 0;
#            RETENTION_FLOOR_ORIGIN_BUILT_IN = 1;          // зашит в агента
#            RETENTION_FLOOR_ORIGIN_CONFIG_DEFAULT = 2;    // retention_floor.default
#            RETENTION_FLOOR_ORIGIN_CONFIG_REPOSITORY = 3; // retention_floor.repositories.<имя>
#          }
#        Отсутствие сообщения — агент старше L1a: сервер (L1b) не шлёт ему
#        FORGET и говорит «обновите агента» (по образцу RegisterRequest.verifier).
#   КП5. Изменение только добавочное: buf breaking против PROTO_BASE зелёный,
#        сгенерированный Go-код (proto/gen/go) и JVM-код (:proto-jvm) обновлены.
#   КП6. [НОВОЕ 2026-10-31, ответ владельца ВL1 б: «форсировать нельзя»]
#        В контракте нет способа обойти нижний предел хоста: ни в одном
#        сообщении, которое сервер шлёт агенту (всё, что достижимо из
#        ConnectResponse), нет поля «force», «remove_all», «override» и
#        подобного, и нет сообщения RetentionFloor — предел только
#        сообщается агентом (RegisterRequest → RepositoryInfo), сервер его не
#        задаёт. Агент дополнительно отклоняет FORGET с неизвестными полями
#        ForgetTask и RetentionPolicy (КL15): будущее поле этих сообщений
#        требует проверки версии агента на сервере. Тот же запрет действует
#        для ACTION_PRUNE и ACTION_CHECK, когда их добавит L1c.
#
# Теги
#   @buf   make lint (buf lint), make breaking-proto, make proto без изменений в
#          рабочем дереве (шлюз ./scripts/gate.sh proto)
#   @wire  тест Go на сгенерированных типах
#
# Названия сценариев цитируются именами тестов, поэтому в них нет символов
# . : ; / < > [ ] \

Функция: Шаг FORGET и нижний предел хоста в контракте агента

  Сервер передаёт агенту политику хранения источника, а не список снимков к
  удалению; агент сообщает, что забыто, и какой нижний предел хранения держит
  его хост.

  Правило: Контракт расширен без поломки

    @buf
    Сценарий: Контракт с шагом FORGET проходит buf lint
      Когда выполняется buf lint
      Тогда код выхода 0

    @buf
    Сценарий: Шаг FORGET и предел хоста не ломают совместимость с базовой веткой
      Когда выполняется buf breaking против PROTO_BASE
      Тогда код выхода 0

    @buf
    Сценарий: Сгенерированный код соответствует контракту с шагом FORGET
      Когда выполняется make proto
      Тогда рабочее дерево не изменилось

  Правило: Сервер не может назвать снимки к удалению

    @wire
    Сценарий: В задании FORGET нет поля со списком снимков к удалению
      Когда перечисляются поля сообщения ForgetTask по его дескриптору
      Тогда имена полей ровно source_id, policy, keep_snapshot_ids, incomplete_snapshot_ids и incomplete_max_age

    @wire
    Сценарий: В политике хранения только правила restic forget без списков снимков
      Когда перечисляются поля сообщения RetentionPolicy по его дескриптору
      Тогда имена полей ровно keep_last, keep_daily, keep_weekly, keep_monthly, keep_yearly и keep_within

  Правило: Сервер не может выключить или ослабить нижний предел хоста

    @wire
    Сценарий: Ни одно сообщение от сервера к агенту не несёт предел хоста
      Когда обходятся все типы сообщений, достижимые по полям из ConnectResponse
      Тогда среди них нет RetentionFloor

    @wire
    Сценарий: Ни одно сообщение от сервера к агенту не несёт поля обхода предела
      Когда обходятся все поля всех типов сообщений, достижимых из ConnectResponse
      Тогда ни одно имя поля не содержит force, remove_all, override, floor или unsafe

  Правило: Старый агент читается как агент без предела

    @wire
    Сценарий: RepositoryInfo без поля предела разбирается как отсутствующий предел
      Дано байты RepositoryInfo с полями 1–5 и без поля 6
      Когда они разбираются сгенерированным кодом
      Тогда retention_floor отсутствует

    @wire
    Сценарий: Выключенный предел отличается от отсутствующего
      Дано RepositoryInfo с retention_floor disabled true и origin RETENTION_FLOOR_ORIGIN_CONFIG_REPOSITORY
      Когда он сериализуется и разбирается снова
      Тогда retention_floor присутствует
      И disabled равно true

  Правило: Задание и итог переживают сериализацию

    @wire
    Сценарий: RunStep с заданием FORGET переживает сериализацию
      Дано RunStep с action ACTION_FORGET, repository_name "main" и ForgetTask с source_id "s1", keep_daily 7, keep_within 336 ч, keep_snapshot_ids "a1", incomplete_snapshot_ids "b2" и incomplete_max_age 168 ч
      Когда он сериализуется и разбирается снова
      Тогда все перечисленные значения совпадают

    @wire
    Структура сценария: Итог FORGET с причиной сбоя переживает сериализацию
      Дано StepResult со статусом <статус> и ForgetOutput с forgotten_snapshot_ids "a1", kept_by_floor_snapshot_ids "b2", remaining_snapshot_ids "b2", listed_at T0, failure <сбой> и lock_holder "<держатель>"
      Когда он сериализуется и разбирается снова
      Тогда все перечисленные значения совпадают

      Примеры:
        | статус                | сбой                                     | держатель                    |
        | STEP_STATUS_SUCCEEDED | MAINTENANCE_FAILURE_UNSPECIFIED          |                              |
        | STEP_STATUS_FAILED    | MAINTENANCE_FAILURE_REPOSITORY_LOCKED    | PID 4242 on db1 by sard-agent |
        | STEP_STATUS_FAILED    | MAINTENANCE_FAILURE_DELETE_DENIED        |                              |
        | STEP_STATUS_FAILED    | MAINTENANCE_FAILURE_STORAGE_UNAVAILABLE  |                              |
        | STEP_STATUS_CANCELLED | MAINTENANCE_FAILURE_UNSPECIFIED          |                              |
