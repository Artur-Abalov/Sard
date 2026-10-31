# language: ru
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# L1 выпуск 1, ручной PRUNE — контракт: действие PRUNE, его итог и список
# действий, которые агент выполняет без плагина. Агент —
# docs/specs/agent/retention-prune.feature; сервер —
# docs/specs/server/retention-policy.feature; ручная проверка —
# docs/qa/retention-policy.md. Разбиение L1, ВL1–ВL5, ДL1–ДL7, КL1–КL16 и
# КП1–КП6 — в заголовке docs/specs/agent/retention-forget.feature.
#
# СТАТУС: УТВЕРЖДЕНО владельцем 2026-10-31 (передано координатором), КП7–КП11
# без изменений, целиком в выпуске 1.
# Сверено с proto/sard/agent/v1/agent.proto 2026-10-31: Action 6, RunStep 13,
# StepResult 16 и RegisterRequest 14 свободны и не зарезервированы.
#
# Решения specifier (имена полей — предложение, окончательные — coder при
# сохранении смысла)
#   КП7. ACTION_PRUNE = 6 в enum Action: удалить из репозитория данные, на
#        которые не ссылается ни один снимок (restic prune). PRUNE ничего не
#        забывает: ни снимков, ни правил хранения в нём нет (КL16, ВL1 в).
#   КП8. RunStep.prune = 13 (PruneTask) — только для ACTION_PRUNE:
#          message PruneTask {}
#        Полей нет намеренно: серверу нечего передать агенту, кроме имени
#        репозитория (RunStep.repository_name) и срока (RunStep.timeout).
#        Сообщение есть, чтобы шаг PRUNE явно говорил «это PRUNE» (агент
#        отклоняет PRUNE без PruneTask, как FORGET без ForgetTask) и чтобы
#        будущее поле PruneTask старый агент отклонял, а не пропускал (КП6
#        распространяется на PRUNE: в PruneTask нет и не будет поля force,
#        remove_all, override, unsafe, max_unused и подобного без новой
#        спецификации; см. КП10).
#   КП9. StepResult.output: PruneOutput prune = 16:
#          message PruneOutput {
#            optional uint64 freed_bytes = 1;           // сколько удалил prune,
#                                                       // как его сводка restic
#            optional uint64 repository_size_bytes = 2; // сколько осталось, как
#                                                       // сводка restic
#            MaintenanceFailure failure = 3;
#            string lock_holder = 4;                    // как ForgetOutput
#          }
#        optional: «restic не сообщил» отличимо от нуля. MaintenanceFailure —
#        тот же enum, что у FORGET (КП3), новых значений нет.
#   КП10. RegisterRequest.builtin_actions = 14 (repeated Action) — действия,
#        которые агент выполняет сам, без плагина. Агент роли source с
#        PRUNE сообщает ACTION_FORGET и ACTION_PRUNE; верификатор — пустой
#        список (он их отклоняет, КL13). Агент L1a поля не знает и не шлёт:
#        сервер читает пустой список как «PRUNE не умеет» и отказывает
#        «обновите агента». Для FORGET признаком по-прежнему служит
#        RepositoryInfo.retention_floor (КП4): агент L1a FORGET умеет, а
#        builtin_actions не шлёт.
#        Почему не версия агента (agent_version), как предлагалось в задаче:
#        сервер не помечает нерелизные версии (docs/specs/server/agent-install.feature,
#        В4, Р2 — «v1.3.2-5-gabc1234-dirty», «0.0.0-dev», «qa» не
#        сравниваются), значит у сборок из git и стендов QA отказ был бы
#        либо всегда, либо никогда. Список действий детерминирован, не
#        зависит от схемы версий и проверяется тестом @wire. Будущее поле
#        ForgetTask или PruneTask (КL15) требует такого же признака — нового
#        значения в этом списке или поля Register, — а не сравнения версий.
#   КП11. Изменение только добавочное: buf breaking против PROTO_BASE зелёный,
#        proto/gen/go и :proto-jvm обновлены (как КП5).
#
# Теги — как в docs/specs/proto/retention-forget.feature
#   @buf   make lint (buf lint), make breaking-proto, make proto без изменений в
#          рабочем дереве (шлюз ./scripts/gate.sh proto)
#   @wire  тест Go на сгенерированных типах
#
# Названия сценариев цитируются именами тестов, поэтому в них нет символов
# . : ; / < > [ ] \

Функция: Шаг PRUNE и встроенные действия агента в контракте

  Сервер просит агента удалить из репозитория данные, на которые не ссылается
  ни один снимок, и ничего не может добавить к этой просьбе; агент сообщает,
  сколько места освобождено, и заранее говорит, какие действия он выполняет
  без плагина.

  Правило: Контракт расширен без поломки

    @buf
    Сценарий: Контракт с шагом PRUNE проходит buf lint
      Когда выполняется buf lint
      Тогда код выхода 0

    @buf
    Сценарий: Шаг PRUNE и встроенные действия не ломают совместимость с базовой веткой
      Когда выполняется buf breaking против PROTO_BASE
      Тогда код выхода 0

    @buf
    Сценарий: Сгенерированный код соответствует контракту с шагом PRUNE
      Когда выполняется make proto
      Тогда рабочее дерево не изменилось

  Правило: В задании PRUNE нечего обойти

    @wire
    Сценарий: В задании PRUNE нет ни одного поля
      Когда перечисляются поля сообщения PruneTask по его дескриптору
      Тогда полей нет

    @wire
    Сценарий: Ни одно сообщение от сервера к агенту с шагом PRUNE не несёт поля обхода
      Когда обходятся все поля всех типов сообщений, достижимых из ConnectResponse
      Тогда ни одно имя поля не содержит force, remove_all, override, floor, unsafe, max_unused или repack

  Правило: Итог PRUNE отличает ноль от «restic не сообщил»

    @wire
    Структура сценария: Итог PRUNE переживает сериализацию
      Дано StepResult со статусом <статус> и PruneOutput с freed_bytes <освобождено>, repository_size_bytes <размер>, failure <сбой> и lock_holder "<держатель>"
      Когда он сериализуется и разбирается снова
      Тогда все перечисленные значения совпадают

      Примеры:
        | статус                | освобождено | размер      | сбой                                    | держатель                     |
        | STEP_STATUS_SUCCEEDED | 1073741824  | 53687091200 | MAINTENANCE_FAILURE_UNSPECIFIED         |                               |
        | STEP_STATUS_SUCCEEDED | 0           | 0           | MAINTENANCE_FAILURE_UNSPECIFIED         |                               |
        | STEP_STATUS_FAILED    | 0           | 0           | MAINTENANCE_FAILURE_REPOSITORY_LOCKED   | PID 4242 on db1 by sard-agent |
        | STEP_STATUS_FAILED    | 0           | 0           | MAINTENANCE_FAILURE_DELETE_DENIED       |                               |
        | STEP_STATUS_FAILED    | 0           | 0           | MAINTENANCE_FAILURE_STORAGE_UNAVAILABLE |                               |

    @wire
    Сценарий: Итог PRUNE без сводки restic отличим от нулевой сводки
      Дано StepResult со статусом STEP_STATUS_SUCCEEDED и PruneOutput без freed_bytes и repository_size_bytes
      Когда он сериализуется и разбирается снова
      Тогда freed_bytes отсутствует
      И repository_size_bytes отсутствует

    @wire
    Сценарий: RunStep с заданием PRUNE переживает сериализацию
      Дано RunStep с action ACTION_PRUNE, repository_name "main", timeout 12 ч и пустым PruneTask
      Когда он сериализуется и разбирается снова
      Тогда action, repository_name и timeout совпадают
      И PruneTask присутствует

  Правило: Агент заранее сообщает свои встроенные действия

    @wire
    Сценарий: Register со встроенными действиями переживает сериализацию
      Дано RegisterRequest с builtin_actions ACTION_FORGET и ACTION_PRUNE
      Когда он сериализуется и разбирается снова
      Тогда builtin_actions равно ACTION_FORGET и ACTION_PRUNE в том же порядке

    @wire
    Сценарий: Register агента без поля встроенных действий читается как пустой список
      Дано байты RegisterRequest с полями 1–13 и без поля 14
      Когда они разбираются сгенерированным кодом
      Тогда builtin_actions пуст
