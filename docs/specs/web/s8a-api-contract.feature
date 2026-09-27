# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# S8a — контракт REST API этапа 1: спецификация OpenAPI, типы, моки MSW.
# Схема контракта: docs/specs/web/api-v1-contract.md.
# QA: docs/qa/s8a-api-contract.md.
#
# Теги:
#   @spec     — проверка над файлом web/src/api/openapi.yaml
#   @mock     — поведение моков MSW (Vitest, если не сказано иное)
#   @ci       — проверки гейта и CI
#   @proposed — сценарий зависит от значения, предложенного спецификатором
#               (открытые вопросы отчёта S8a); до подтверждения не реализуется.
#
# Каждый сценарий реализуется тестом, в имени которого цитируется заголовок.

Feature: Контракт REST API этапа 1
  Чтобы страницы этапа 1 (W2) и сервер (S8b) делались параллельно,
  как разработчик Sard
  я хочу один рукописный контракт OpenAPI, типы из него и моки, которые ему следуют.

  # ---------------------------------------------------------------------------
  Rule: Спецификация проходит линтер и сохраняет существующий контракт

    @spec @ci
    Scenario: Линтер Redocly не находит ошибок в спецификации
      Given файл web/src/api/openapi.yaml из рабочей копии
      When запускается npm run lint в web/
      Then линтер Redocly завершается с кодом 0
      And в его выводе ноль ошибок

    @spec @ci
    Scenario: Ошибка в спецификации роняет линтер
      Given в openapi.yaml есть ссылка на несуществующую схему
      When запускается npm run lint в web/
      Then команда завершается с ненулевым кодом
      And в выводе названа несуществующая ссылка

    @spec
    Scenario: Спецификация объявлена как OpenAPI 3.1
      When читается поле openapi спецификации
      Then его значение начинается с "3.1."

    @spec
    Scenario: Эндпоинт статуса описан как прежде
      When читается операция GET /api/v1/status
      Then она не требует сессии
      And ответ 200 содержит обязательные поля version (строка) и lastVerifiedRestoreAt (date-time или null)

  # ---------------------------------------------------------------------------
  Rule: Общие соглашения контракта

    @spec
    Scenario: Все пути лежат под /api/v1
      When перечисляются пути спецификации
      Then каждый путь начинается с "/api/v1/"

    @spec
    Scenario: Тенант не передаётся ни в пути, ни в параметрах
      When перечисляются пути и параметры всех операций
      Then ни путь, ни имя параметра не содержат "tenant" без учёта регистра

    @spec
    Scenario: Идентификаторы описаны как UUID
      When перечисляются свойства схем и параметры пути с именем id или с суффиксом Id
      Then у каждого из них format равен uuid

    @spec
    Scenario: Отметки времени описаны как RFC 3339
      When перечисляются свойства схем с суффиксом At
      Then у каждого из них format равен date-time

    @spec
    Scenario: Списки постраничные по курсору
      When перечисляются операции GET, возвращающие коллекцию, кроме логов шага
      Then каждая принимает параметры cursor и limit
      And её ответ 200 — объект с обязательными items (массив) и nextCursor (строка или null)

    @spec @proposed
    Scenario: Параметр limit списков ограничен и имеет значение по умолчанию
      When читается параметр limit постраничных операций
      Then минимум равен 1, максимум 200, значение по умолчанию 50

    @spec
    Scenario: Ошибки описаны как problem+json с машинным кодом
      When перечисляются ответы всех операций с кодом 4xx
      Then у каждого единственный тип содержимого application/problem+json
      And его схема — Problem, в которой поле code обязательно

    @spec
    Scenario: Cookie-сессия объявлена схемой безопасности
      When читаются схемы безопасности спецификации
      Then есть схема apiKey в cookie с именем sard_session

    @spec
    Scenario: Каждая операция, кроме входа и статуса, требует сессии и документирует 401
      When перечисляются все операции, кроме POST /api/v1/session и GET /api/v1/status
      Then у каждой действует cookie-схема безопасности
      And у каждой описан ответ 401 с problem+json

    @spec
    Scenario: Вход и статус доступны без сессии
      When читаются операции POST /api/v1/session и GET /api/v1/status
      Then у обеих список требований безопасности пуст

    @spec
    Scenario: В контракте этапа 1 нет отмены запуска и workflowId
      When перечисляются пути и свойства схем
      Then нет пути, оканчивающегося на "/cancel"
      And ни одна схема не содержит свойства workflowId

  # ---------------------------------------------------------------------------
  Rule: Перечисления совпадают с proto

    @spec @ci
    Scenario: Фазы шага совпадают с StepPhase из proto
      When сравниваются значения перечисления фаз шага в спецификации и StepPhase без UNSPECIFIED
      Then множества равны: accepted, preparing, dumping, uploading, restoring, verifying

    @spec @ci
    Scenario: Действия совпадают с Action из proto
      When сравниваются значения перечисления действий в спецификации и Action без UNSPECIFIED
      Then множества равны: backup, restore, verify, run

    @spec @ci
    Scenario: Уровни логов совпадают с LogLevel из proto
      When сравниваются значения уровня строки лога в спецификации и LogLevel без UNSPECIFIED
      Then множества равны: debug, info, warn, error

    @spec @ci
    Scenario: Каждый StepStatus из proto входит в статусы шага API
      When сравниваются статусы шага в спецификации и StepStatus без UNSPECIFIED
      Then succeeded, failed, cancelled, timed_out и rejected есть среди статусов шага API

    @spec @ci
    Scenario: Статусы шага, которых нет в proto, не сверяются
      Given статусы шага API содержат queued, dispatched, running и lost
      When запускается сверка перечислений
      Then сверка проходит

    @spec @ci
    Scenario: Действие запуска — подмножество действий proto
      When читается перечисление действия в теле создания запуска
      Then его значения — ровно backup и verify
      And каждое из них есть в Action из proto

    @spec @ci
    Scenario: Новое значение в proto без правки спецификации ломает сборку
      Given в proto-перечислении StepPhase есть значение, которого нет в спецификации
      When запускается сверка перечислений
      Then сверка падает и называет перечисление и недостающее значение

    @spec @ci
    Scenario: Лишнее значение в спецификации ломает сборку
      Given в перечислении уровней логов спецификации есть значение trace, которого нет в LogLevel
      When запускается сверка перечислений
      Then сверка падает и называет перечисление и лишнее значение

  # ---------------------------------------------------------------------------
  Rule: Сгенерированные типы и CI

    @ci
    Scenario: schema.d.ts совпадает со спецификацией
      Given закоммиченные openapi.yaml и schema.d.ts
      When CI выполняет npm run gen:api и git diff по schema.d.ts
      Then различий нет и шаг проходит

    @ci
    Scenario: Не перегенерированный schema.d.ts роняет CI
      Given openapi.yaml изменён, а schema.d.ts не перегенерирован
      When CI выполняет npm run gen:api и git diff по schema.d.ts
      Then шаг завершается с ненулевым кодом

    @ci
    Scenario: Мок с телом не по схеме не проходит проверку типов
      Given обработчик мока отвечает телом, в котором нет обязательного поля схемы
      When запускается npm run typecheck
      Then проверка типов завершается ошибкой

    @ci
    Scenario: Мок для пути вне спецификации не проходит проверку типов
      Given обработчик мока объявлен для пути, которого нет в спецификации
      When запускается npm run typecheck
      Then проверка типов завершается ошибкой

    @ci
    Scenario: Каждый путь сервера описан в спецификации
      Given сервер отдаёт в /v3/api-docs только пары метод+путь, описанные в openapi.yaml
      When выполняется проверка в job server
      Then проверка проходит

    @ci
    Scenario: Путь сервера, которого нет в спецификации, роняет job server
      Given сервер отдаёт в /v3/api-docs операцию, которой нет в openapi.yaml
      When выполняется проверка в job server
      Then проверка падает и называет метод и путь этой операции

    @ci
    Scenario: Описанные, но не реализованные сервером пути не роняют job server
      Given в openapi.yaml есть пути, которых сервер пока не отдаёт
      When выполняется проверка в job server
      Then проверка проходит

  # ---------------------------------------------------------------------------
  Rule: Схемы токенов регистрации не раскрывают токен

    @spec
    Scenario: Элемент списка и карточка токена не содержат строки токена
      When читается схема токена, которую возвращают список и карточка
      Then её свойства — ровно id, status, createdAt, expiresAt, usedAt, revokedAt, agentId
      And среди них нет token и enrollCommand

    @spec
    Scenario: Ответ на создание токена содержит токен и команду
      When читается схема ответа 201 на POST /api/v1/enrollment-tokens
      Then обязательны id, token, enrollCommand и expiresAt
      And token описан шаблоном формата из docs/specs/enrollment-token.md

    @spec
    Scenario: Срок жизни токена ограничен в схеме
      When читается свойство ttlSeconds тела создания токена
      Then оно необязательное, целое, минимум 300, максимум 604800, по умолчанию 86400

    @spec
    Scenario: Статусы токена — четыре значения
      When читается перечисление статуса токена
      Then его значения — ровно active, used, expired, revoked

    @spec
    Scenario: Отзыв токена описан ответами 200, 404 и 409
      When читается операция POST /api/v1/enrollment-tokens/{tokenId}/revoke
      Then описаны ответы 200 с токеном, 404 и 409
      And схема ответа 409 допускает поле agentId

  # ---------------------------------------------------------------------------
  Rule: Схемы агентов, источников и запусков

    @spec
    Scenario: Карточка агента содержит только описанные поля
      When читается схема ответа GET /api/v1/agents/{agentId}
      Then её свойства — ровно id, hostname, online, lastSeenAt, registeredAt, agentVersion, os, arch, protocolVersion, plugins, repositories, secretNames, scriptNames

    @spec
    Scenario: Плагин агента несёт схему конфигурации объектом
      When читается схема плагина в карточке агента
      Then её свойства — name, version, actions, configSchema
      And configSchema имеет тип object

    @spec
    Scenario: Репозиторий агента описан без учётных данных
      When читается схема репозитория в карточке агента
      Then её свойства — ровно name, backend, repositoryId, cryptoProvider

    @spec
    Scenario: При создании источника обязательны агент и репозиторий
      When читается схема тела POST /api/v1/sources
      Then обязательны agentId, repositoryName, name, plugin и config
      And config имеет тип object

    @spec
    Scenario: Изменение источника допускает только имя и конфигурацию
      When читается схема тела PATCH /api/v1/sources/{sourceId}
      Then её свойства — ровно name и config
      And дополнительные свойства запрещены

    @spec
    Scenario: Удаление источника с активным запуском описано ответом 409
      When читается операция DELETE /api/v1/sources/{sourceId}
      Then описаны ответы 204, 404 и 409
      And схема ответа 409 допускает поле activeRunId

    @spec
    Scenario: Создание запуска описывает отказ при активном запуске
      When читается операция POST /api/v1/runs
      Then описаны ответы 201, 409 и 422
      And схема ответа 409 допускает поле activeRunId

    @spec
    Scenario: Источник запуска только для чтения
      When читается свойство trigger схемы запуска
      Then оно readOnly
      And его значения — ровно schedule, manual, verification

    @spec
    Scenario: Логи шага читаются по seq
      When читается операция логов шага
      Then она принимает параметры afterSeq и limit
      And строка лога содержит seq, time, level и text

  # ---------------------------------------------------------------------------
  Rule: Сессия в моках

    @mock
    Scenario: В тестах мок начинает без сессии
      Given моки в режиме Vitest
      When клиент запрашивает GET /api/v1/session
      Then ответ 401 с code unauthenticated

    @mock
    Scenario: Вход с верным паролем открывает сессию
      Given моки в режиме Vitest без сессии
      When клиент отправляет POST /api/v1/session с паролем из фикстур
      Then ответ 204

    @mock
    Scenario: Открытая сессия сообщает тенант и срок
      Given моки в режиме Vitest и выполнен вход
      When клиент запрашивает GET /api/v1/session
      Then ответ 200
      And tenantId равен 00000000-0000-0000-0000-000000000001
      And expiresAt позже текущего времени

    @mock
    Scenario: Неверный пароль не открывает сессию
      Given моки в режиме Vitest без сессии
      When клиент отправляет POST /api/v1/session с неверным паролем
      Then ответ 401 с code invalid_credentials
      And последующий GET /api/v1/session отвечает 401

    @mock @proposed
    Scenario: Серия неверных паролей блокирует вход
      Given моки в режиме Vitest и пять неверных попыток входа подряд
      When клиент отправляет POST /api/v1/session с верным паролем
      Then ответ 429 с code too_many_attempts
      And заголовок Retry-After содержит положительное целое число секунд

    @mock
    Scenario: Выход закрывает сессию
      Given моки в режиме Vitest и выполнен вход
      When клиент отправляет DELETE /api/v1/session
      Then ответ 204
      And последующий GET /api/v1/agents отвечает 401

    @mock
    Scenario Outline: Защищённая операция без сессии отвечает 401
      Given моки в режиме Vitest без сессии
      When клиент вызывает <операция>
      Then ответ 401 с code unauthenticated

      Examples:
        | операция                                          |
        | GET /api/v1/session                               |
        | DELETE /api/v1/session                            |
        | GET /api/v1/agents                                |
        | GET /api/v1/agents/{A1}                           |
        | POST /api/v1/enrollment-tokens                    |
        | GET /api/v1/enrollment-tokens                     |
        | GET /api/v1/enrollment-tokens/{active}            |
        | POST /api/v1/enrollment-tokens/{active}/revoke    |
        | GET /api/v1/sources                               |
        | POST /api/v1/sources                              |
        | GET /api/v1/sources/{S1}                          |
        | PATCH /api/v1/sources/{S1}                        |
        | DELETE /api/v1/sources/{S1}                       |
        | GET /api/v1/sources/{S1}/snapshots                |
        | POST /api/v1/runs                                 |
        | GET /api/v1/runs                                  |
        | GET /api/v1/runs/{R1}                             |
        | GET /api/v1/runs/{R1}/steps/{R1 step}/logs        |

    @mock
    Scenario: Защищённая операция без сессии ничего не меняет
      Given моки в режиме Vitest без сессии
      When клиент отправляет POST /api/v1/enrollment-tokens
      Then состояние мока совпадает с фикстурами: новых токенов нет

    @mock
    Scenario: Статус доступен без сессии
      Given моки в режиме Vitest без сессии
      When клиент запрашивает GET /api/v1/status
      Then ответ 200 с фикстурой статуса

    @mock
    Scenario: В dev-режиме мок начинает с открытой сессией
      Given состояние моков создано так, как его создаёт dev-режим (VITE_API_MOCKS=1)
      When клиент запрашивает GET /api/v1/session
      Then ответ 200

    @mock
    Scenario: Каждый тест начинает с фикстур
      Given предыдущий тест создал токен, источник и запуск
      When следующий тест после входа запрашивает списки токенов, источников и запусков
      Then в них только фикстуры

  # ---------------------------------------------------------------------------
  Rule: Агенты в моках

    Background:
      Given моки в режиме Vitest и выполнен вход

    @mock
    Scenario: Список агентов содержит онлайн- и офлайн-агента
      When клиент запрашивает GET /api/v1/agents
      Then в items есть агент с online true и агент с online false
      And nextCursor равен null

    @mock
    Scenario: Карточка агента показывает плагины, репозитории и имена секретов
      When клиент запрашивает карточку онлайн-агента
      Then в plugins есть плагин с непустыми actions и configSchema-объектом
      And в repositories есть репозиторий с name, backend и repositoryId
      And secretNames и scriptNames — непустые массивы строк

    @mock
    Scenario: Карточка неизвестного агента — 404
      When клиент запрашивает GET /api/v1/agents/ со случайным UUID
      Then ответ 404 с code not_found

    @mock @proposed
    Scenario: Идентификатор агента не в формате UUID — 400
      When клиент запрашивает GET /api/v1/agents/not-a-uuid
      Then ответ 400 с code invalid_parameter

  # ---------------------------------------------------------------------------
  Rule: Постраничная выдача в моках

    Background:
      Given моки в режиме Vitest и выполнен вход

    @mock
    Scenario: Обход списка запусков по страницам возвращает все запуски без повторов
      When клиент обходит GET /api/v1/runs с limit=1, передавая nextCursor, пока он не null
      Then собранные id совпадают с id запусков из фикстур
      And ни один id не повторяется

    @mock
    Scenario: Последняя страница имеет nextCursor null
      When клиент запрашивает GET /api/v1/runs с limit, равным числу запусков в фикстурах
      Then items содержит все запуски
      And nextCursor равен null

    @mock
    Scenario: Пустой результат — пустой items и nextCursor null
      When клиент запрашивает GET /api/v1/runs с sourceId источника без запусков
      Then ответ 200 с items [] и nextCursor null

    @mock @proposed
    Scenario Outline: limit вне допустимого диапазона отклоняется
      When клиент запрашивает GET /api/v1/runs с limit=<limit>
      Then ответ 400 с code invalid_parameter

      Examples:
        | limit |
        | 0     |
        | 201   |
        | -1    |
        | abc   |

    @mock @proposed
    Scenario Outline: limit на границе диапазона принимается
      When клиент запрашивает GET /api/v1/runs с limit=<limit>
      Then ответ 200

      Examples:
        | limit |
        | 1     |
        | 200   |

    @mock @proposed
    Scenario: Курсор, не выданный сервером, отклоняется
      When клиент запрашивает GET /api/v1/runs с cursor=garbage
      Then ответ 400 с code invalid_cursor

  # ---------------------------------------------------------------------------
  Rule: Токены регистрации в моках

    Background:
      Given моки в режиме Vitest и выполнен вход

    @mock
    Scenario: Токен без срока создаётся на сутки
      When клиент отправляет POST /api/v1/enrollment-tokens с пустым объектом
      Then ответ 201
      And expiresAt позже момента запроса на 86400 секунд с допуском 5 секунд

    @mock
    Scenario Outline: Срок жизни на границе диапазона принимается
      When клиент отправляет POST /api/v1/enrollment-tokens с ttlSeconds=<ttl>
      Then ответ 201
      And expiresAt позже момента запроса на <ttl> секунд с допуском 5 секунд

      Examples:
        | ttl    |
        | 300    |
        | 604800 |

    @mock @proposed
    Scenario Outline: Срок жизни вне диапазона отклоняется
      When клиент отправляет POST /api/v1/enrollment-tokens с ttlSeconds=<ttl>
      Then ответ 422 с code ttl_out_of_range
      And список токенов не изменился

      Examples:
        | ttl    |
        | 299    |
        | 604801 |
        | 0      |

    @mock
    Scenario: Ответ на создание содержит токен и команду регистрации
      When клиент создаёт токен
      Then token соответствует шаблону ^sard_[A-Za-z0-9_-]{43}\.[0-9a-f]{64}$
      And enrollCommand равен "sard-agent enroll --server <адрес> --token <token>" с тем же token

    @mock
    Scenario: Созданный токен виден в списке активным и без строки токена
      Given клиент создал токен
      When клиент запрашивает GET /api/v1/enrollment-tokens
      Then в items есть токен с тем же id и status active
      And у этого элемента нет ключей token и enrollCommand

    @mock
    Scenario: Карточка созданного токена не содержит строки токена
      Given клиент создал токен
      When клиент запрашивает карточку этого токена
      Then ответ 200 со status active
      And в теле нет ключей token и enrollCommand

    @mock
    Scenario: Список токенов содержит все четыре статуса и ни одной строки токена
      When клиент запрашивает GET /api/v1/enrollment-tokens
      Then статусы в items — active, used, expired и revoked
      And ни в одном элементе нет ключей token и enrollCommand

    @mock
    Scenario: Использованный токен связан с агентом
      When клиент запрашивает GET /api/v1/enrollment-tokens?status=used
      Then в items один токен
      And его agentId — id онлайн-агента из фикстур, usedAt не null

    @mock
    Scenario Outline: Фильтр по статусу возвращает только токены этого статуса
      When клиент запрашивает GET /api/v1/enrollment-tokens?status=<status>
      Then у всех элементов items status равен <status>

      Examples:
        | status  |
        | active  |
        | used    |
        | expired |
        | revoked |

    @mock @proposed
    Scenario: Неизвестный статус в фильтре отклоняется
      When клиент запрашивает GET /api/v1/enrollment-tokens?status=deleted
      Then ответ 400 с code invalid_parameter

    @mock
    Scenario: Карточка неизвестного токена — 404
      When клиент запрашивает карточку токена со случайным UUID
      Then ответ 404 с code not_found

    @mock
    Scenario: Отзыв активного токена
      When клиент отзывает активный токен
      Then ответ 200 со status revoked
      And revokedAt не null

    @mock
    Scenario: Повторный отзыв идемпотентен
      Given клиент отозвал активный токен
      When клиент отзывает этот токен ещё раз
      Then ответ 200 со status revoked
      And revokedAt совпадает с revokedAt первого отзыва

    @mock
    Scenario: Отзыв использованного токена отклоняется со ссылкой на агента
      When клиент отзывает использованный токен
      Then ответ 409 с code token_already_used
      And agentId в ответе — id агента, связанного с токеном
      And токен остаётся со status used

    @mock
    Scenario: Отзыв просроченного токена отклоняется
      When клиент отзывает просроченный токен
      Then ответ 409 с code token_expired
      And токен остаётся со status expired

    @mock
    Scenario: Отзыв неизвестного токена — 404
      When клиент отзывает токен со случайным UUID
      Then ответ 404 с code not_found

  # ---------------------------------------------------------------------------
  Rule: Источники в моках

    Background:
      Given моки в режиме Vitest и выполнен вход

    @mock
    Scenario: Создание источника
      When клиент создаёт источник на онлайн-агенте с репозиторием и плагином из его карточки
      Then ответ 201 с новым id и переданными полями
      And источник появляется в GET /api/v1/sources

    @mock
    Scenario: Источник на офлайн-агенте проверяется по его последней регистрации
      When клиент создаёт источник на офлайн-агенте с репозиторием из его карточки
      Then ответ 201

    @mock
    Scenario: Репозиторий, неизвестный агенту, отклоняется
      When клиент создаёт источник на онлайн-агенте с repositoryName, которого нет в его карточке
      Then ответ 422 с code repository_unknown_to_agent
      And список источников не изменился

    @mock @proposed
    Scenario: Репозиторий другого агента отклоняется
      When клиент создаёт источник на онлайн-агенте с репозиторием из карточки офлайн-агента
      Then ответ 422 с code repository_unknown_to_agent

    @mock @proposed
    Scenario: Плагин, неизвестный агенту, отклоняется
      When клиент создаёт источник с plugin, которого нет в карточке агента
      Then ответ 422 с code plugin_unknown_to_agent

    @mock @proposed
    Scenario: Несуществующий агент отклоняется
      When клиент создаёт источник со случайным agentId
      Then ответ 422 с code agent_not_found

    @mock @proposed
    Scenario Outline: Источник без обязательного поля отклоняется
      When клиент создаёт источник без поля <поле>
      Then ответ 422 с code validation_failed
      And в errors есть элемент с pointer /<поле>

      Examples:
        | поле           |
        | agentId        |
        | repositoryName |
        | name           |
        | plugin         |
        | config         |

    @mock @proposed
    Scenario: Имя живого источника не может повториться
      When клиент создаёт источник с именем существующего источника
      Then ответ 409 с code source_name_taken

    @mock
    Scenario: Изменение имени и конфигурации источника
      When клиент отправляет PATCH источника с новыми name и config
      Then ответ 200 с новыми name и config
      And agentId, plugin и repositoryName не изменились
      And updatedAt позже прежнего

    @mock @proposed
    Scenario Outline: Неизменяемое поле источника не меняется
      When клиент отправляет PATCH источника с полем <поле>
      Then ответ 422 с code immutable_field
      And источник не изменился

      Examples:
        | поле           |
        | agentId        |
        | plugin         |
        | repositoryName |

    @mock @proposed
    Scenario: PATCH с пустым телом ничего не меняет
      When клиент отправляет PATCH источника с пустым объектом
      Then ответ 200 с прежними полями

    @mock
    Scenario: Удаление источника без активного запуска
      When клиент удаляет источник без активного запуска
      Then ответ 204
      And GET этого источника отвечает 404

    @mock @proposed
    Scenario: История запусков удалённого источника остаётся доступной
      Given клиент удалил источник, у которого есть завершённые запуски
      When клиент запрашивает GET /api/v1/runs с sourceId этого источника
      Then в items есть его завершённые запуски

    @mock @proposed
    Scenario: Имя удалённого источника можно занять снова
      Given клиент удалил источник
      When клиент создаёт источник с тем же именем
      Then ответ 201

    @mock
    Scenario: Удаление источника с активным запуском отклоняется
      When клиент удаляет источник с идущим запуском
      Then ответ 409 с code source_has_active_run
      And activeRunId равен id идущего запуска
      And источник по-прежнему доступен

    @mock
    Scenario Outline: Операция над неизвестным источником — 404
      When клиент вызывает <операция> со случайным UUID источника
      Then ответ 404 с code not_found

      Examples:
        | операция                             |
        | GET /api/v1/sources/{id}             |
        | PATCH /api/v1/sources/{id}           |
        | DELETE /api/v1/sources/{id}          |
        | GET /api/v1/sources/{id}/snapshots   |

    @mock
    Scenario: Снимки источника
      When клиент запрашивает снимки источника с успешным бэкапом
      Then в items есть снимок со snapshotId, repositoryName, repositoryId, totalBytes, addedBytes и createdAt
      And runId снимка — id успешного запуска

    @mock
    Scenario: У источника без бэкапов снимков нет
      When клиент запрашивает снимки источника без запусков
      Then ответ 200 с items [] и nextCursor null

  # ---------------------------------------------------------------------------
  Rule: Запуски в моках

    Background:
      Given моки в режиме Vitest и выполнен вход

    @mock
    Scenario: Ручной бэкап источника без активного запуска
      When клиент отправляет POST /api/v1/runs с sourceId источника без активного запуска и action backup
      Then ответ 201
      And status равен queued, trigger равен manual, action равен backup
      And steps содержит один шаг со status queued

    @mock
    Scenario: Второй запуск при активном отклоняется со ссылкой на идущий
      When клиент отправляет POST /api/v1/runs для источника с идущим запуском
      Then ответ 409 с code run_already_active
      And activeRunId равен id идущего запуска
      And число запусков источника не изменилось

    @mock
    Scenario: Запуск, только что поставленный в очередь, тоже активен
      Given клиент создал запуск бэкапа источника
      When клиент отправляет POST /api/v1/runs для того же источника
      Then ответ 409 с code run_already_active
      And activeRunId равен id созданного запуска

    @mock
    Scenario: Одновременные запуски одного источника создают ровно один запуск
      When клиент одновременно отправляет два POST /api/v1/runs для одного источника без активного запуска
      Then один ответ 201 и один ответ 409
      And activeRunId в ответе 409 равен id из ответа 201

    @mock
    Scenario: После завершённых запусков новый запуск разрешён
      When клиент отправляет POST /api/v1/runs для источника, у которого только успешный и неуспешный запуски
      Then ответ 201

    @mock
    Scenario: Проверка восстановления источника со снимком
      When клиент отправляет POST /api/v1/runs с action verify для источника со снимком
      Then ответ 201 с action verify

    @mock @proposed
    Scenario: Проверка восстановления без снимков отклоняется
      Given клиент создал источник на онлайн-агенте
      When клиент отправляет POST /api/v1/runs с action verify для этого источника
      Then ответ 422 с code no_snapshots

    @mock @proposed
    Scenario: Действие, которого нет у плагина, отклоняется
      When клиент отправляет POST /api/v1/runs с action verify для источника офлайн-агента, чей плагин умеет только backup
      Then ответ 422 с code action_not_supported

    @mock @proposed
    Scenario Outline: Действие вне этапа 1 отклоняется
      When клиент отправляет POST /api/v1/runs с action <action>
      Then ответ 422 с code validation_failed

      Examples:
        | action  |
        | restore |
        | run     |
        | delete  |

    @mock @proposed
    Scenario: Запуск несуществующего источника отклоняется
      When клиент отправляет POST /api/v1/runs со случайным sourceId
      Then ответ 422 с code source_not_found

    @mock
    Scenario: Запуск источника офлайн-агента ставится в очередь
      When клиент отправляет POST /api/v1/runs с action backup для источника офлайн-агента
      Then ответ 201 со status queued

    @mock
    Scenario: Список запусков содержит успешный, неуспешный и идущий
      When клиент запрашивает GET /api/v1/runs
      Then статусы в items включают succeeded, failed и running

    @mock
    Scenario: Фильтр запусков по источнику
      When клиент запрашивает GET /api/v1/runs с sourceId источника с двумя завершёнными запусками
      Then в items ровно эти два запуска

    @mock
    Scenario: Фильтр запусков по статусу
      When клиент запрашивает GET /api/v1/runs со status=running
      Then у всех элементов items status равен running
      And items не пуст

    @mock
    Scenario: Карточка неуспешного запуска показывает причину
      When клиент запрашивает карточку неуспешного запуска
      Then у его шага status failed и непустой message

    @mock
    Scenario: Карточка идущего запуска показывает фазу и прогресс
      When клиент запрашивает карточку идущего запуска
      Then у его шага status running, phase uploading
      And bytesProcessed не больше bytesTotal

    @mock @proposed
    Scenario: Карточка успешного бэкапа показывает снимок и размеры
      When клиент запрашивает карточку успешного запуска
      Then у его шага status succeeded, finishedAt не null
      And output шага содержит snapshotId, totalBytes и addedBytes

    @mock
    Scenario: Карточка неизвестного запуска — 404
      When клиент запрашивает карточку запуска со случайным UUID
      Then ответ 404 с code not_found

  # ---------------------------------------------------------------------------
  Rule: Логи шага в моках

    Background:
      Given моки в режиме Vitest и выполнен вход

    @mock
    Scenario: Обход логов по seq возвращает все строки без повторов
      When клиент читает логи шага успешного запуска страницами, передавая nextAfterSeq как afterSeq, пока items не пуст
      Then собрано столько строк, сколько в фикстуре (не меньше нескольких сотен)
      And seq строго возрастают и не повторяются

    @mock
    Scenario: Первая страница логов начинается с первой строки
      When клиент запрашивает логи шага без afterSeq
      Then первая строка имеет наименьший seq фикстуры

    @mock
    Scenario: После последней строки логов страница пуста
      When клиент запрашивает логи шага с afterSeq, равным seq последней строки
      Then ответ 200 с items []
      And nextAfterSeq равен переданному afterSeq

    @mock
    Scenario: Страница логов не длиннее limit
      When клиент запрашивает логи шага с limit=100
      Then в items не больше 100 строк

    @mock @proposed
    Scenario Outline: Некорректные параметры логов отклоняются
      When клиент запрашивает логи шага с <параметр>
      Then ответ 400 с code invalid_parameter

      Examples:
        | параметр    |
        | limit=0     |
        | limit=1001  |
        | afterSeq=-1 |

    @mock
    Scenario: Логи неизвестного шага — 404
      When клиент запрашивает логи шага со случайным UUID в существующем запуске
      Then ответ 404 с code not_found

    @mock
    Scenario: Шаг из другого запуска — 404
      When клиент запрашивает логи шага успешного запуска по пути неуспешного запуска
      Then ответ 404 с code not_found

  # ---------------------------------------------------------------------------
  Rule: Существующий статус и дашборд продолжают работать

    @mock
    Scenario: Прежний тест клиента статуса проходит без изменений
      Given src/api/client.test.ts не изменён
      When запускается npm test
      Then тесты fetchStatus проходят

    # Отображение дашборда в браузере проверяется вручную: docs/qa/s8a-api-contract.md,
    # шаг 12 (компонентных тестов в web нет, CLAUDE.md).

    @ci
    Scenario: Production-сборка не содержит моков
      When выполняется npm run build
      Then в web/dist нет файла mockServiceWorker.js
      And в собранных JS-файлах нет строки VITE_API_MOCKS и кода обработчиков моков
