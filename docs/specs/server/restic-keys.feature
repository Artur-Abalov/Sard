# language: ru
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# V3. Ключи restic — сервер: признак ключа восстановления (ответы владельца
# В1, В3), команды в карточке верификатора (правило 16, ответ В8), ключ
# восстановления в самобэкапе F4b (правило 12, ответ ВС1). Агентская часть и
# все правила описания — docs/specs/agent/restic-keys.feature; контракт —
# docs/specs/proto/restic-keys.feature. Ручная проверка — docs/qa/restic-keys.md.
#
# СТАТУС: ЧЕРНОВИК, ждёт утверждения владельцем (ответы на вопросы получены
# 2026-10-21).
#
# Решения specifier
#   КС1. SelfBackupState (К12 docs/specs/server/onboarding-self-backup.feature)
#        дополняется полем recoveryKeyCommand — string или null: команда,
#        которая на машине сервера выпускает ключ восстановления репозитория
#        привязки; null до привязки. Добавочное изменение OpenAPI.
#   КС2. Вид команды — префикс строки 1 Р24 F4b, затем
#        «self-agent repo key add <имя> --for recovery --if-missing --config
#        /etc/sard/self/agent.yaml»:
#          docker compose run --rm --no-deps -u 0 --cap-add CHOWN --cap-add
#          DAC_OVERRIDE --cap-add FOWNER --cap-add SETUID --cap-add SETGID
#          self-agent repo key add <имя> --for recovery --if-missing --config
#          /etc/sard/self/agent.yaml
#        --if-missing: если repo add уже выпустил ключ восстановления при
#        создании репозитория (правило 10), команда отвечает «unchanged».
#        Точная форма подтверждается сценарием @e2e (как Р27 F4b). Команда
#        перезапускает соседа по Р8 сама (К19 агентской части), поэтому сервер
#        получает признак из нового Register.
#   КС3. passwordRevealCommand остаётся (ответ ВС1); подтверждение копии
#        ключей (К14), шаги онбординга (Р20) и журнал (Р30) не меняются.
#   КС4. Признак у агента. Карточка агента (GET /api/v1/agents/{id}) и его
#        элемент в GET /api/v1/agents: каждый элемент repositories получает
#        recoveryKey — «present», «absent» или «unknown» (RECOVERY_KEY_STATE_*
#        из последнего Register этого агента; UNSPECIFIED и агент старше V3 —
#        «unknown»). Хранится вместе с прочими данными последнего Register,
#        переживает перезапуск сервера.
#   КС5. Признак репозитория. Новый GET /api/v1/repositories — сессия
#        администратора, тенант сессии (ADR 0013); 401 без сессии. Ответ:
#          {"items":[{"repositoryId","recoveryKey","reportedAt",
#                     "reportedByAgentId","recoveryKeyCommand",
#                     "holders":[{"agentId","hostname","role","repositoryName"}]}],
#           "withoutRecoveryKey":<число>}
#        Элемент — один repository_id, о котором сообщает хотя бы один
#        неотозванный агент тенанта; пустой repository_id не входит. holders —
#        все такие агенты, по agentId. recoveryKey берётся из последнего по
#        времени Register среди holders, где признак известен (present или
#        absent); при равном времени — меньший agentId; известного нет —
#        «unknown», reportedAt и reportedByAgentId — null. Порядок items — по
#        repositoryId. withoutRecoveryKey — число items с «absent»; «unknown»
#        (агенты до V3, сбой чтения ключей) в него не входит.
#   КС6. recoveryKeyCommand элемента — при «absent», иначе null: для
#        источника-держателя с самым поздним Register —
#        «sudo sard-agent repo key add <repositoryName> --for recovery»; если
#        держатель — встроенный агент sard-self, — recoveryKeyCommand
#        самобэкапа (КС2) с этим именем. Держатели только верификаторы —
#        null (верификатор ключей не выпускает, К17).
#   КС7. Карточка верификатора (ответ В8). У агента с ролью verifier карточка
#        получает verifierSetup — массив по каждому repository_id, о котором
#        сообщает неотозванный агент-источник тенанта и не сообщает этот
#        верификатор: {repositoryId, sourceAgentId, sourceHostname,
#        repositoryName, keyAddCommand, repoAddTemplate}, по repositoryId.
#        Источник — держатель с самым поздним Register, при равенстве —
#        меньший agentId. keyAddCommand —
#        «sudo sard-agent repo key add <repositoryName> --for verifier --label
#        vrf-<первые 8 шестнадцатеричных знаков agentId верификатора без
#        дефисов>». repoAddTemplate — ровно
#        «sudo sard-agent repo add <repositoryName> <address> --password-from-file <file>»
#        с буквальными «<address>» и «<file>»: адрес сервер не знает (ADR 0008),
#        точную команду печатает key add на источнике. Имя вне набора
#        [A-Za-z0-9._-] — в одинарных кавычках; имя с одинарной кавычкой или
#        управляющим символом — keyAddCommand и repoAddTemplate null. У агента
#        роли source поля нет (null).
#   КС8. Сервер не получает и не хранит паролей, ключей, их id и меток; в
#        журнале сервера признак не пишется.
#
# Теги — как в docs/specs/server/onboarding-self-backup.feature (@http, @grpc,
# @e2e).
#
# Общие соглашения
#   - Тенант T; агенты-источники A (hostname src1) и B (src2), верификатор W
#     (agentId 3f2a9c1e-…, hostname vrf1). «A регистрирует base r1 present» —
#     Register агента A с репозиторием base, repository_id r1, recovery_key
#     RECOVERY_KEY_STATE_PRESENT.
#   - Названия сценариев цитируются именами тестов, поэтому в них нет
#     символов . : ; / < > [ ] \

Функция: Признак ключа восстановления, команды ключей и самобэкап — сервер

  Сервер узнаёт от агентов, у каких репозиториев нет ключа восстановления, и
  показывает это администратору без визита на хост; карточка верификатора
  даёт готовую команду выпуска ключа; самобэкап просит выпустить ключ
  восстановления.

  # ===========================================================================
  Правило: Сервер хранит признак ключа восстановления из Register агента

    @grpc @http
    Структура сценария: Карточка агента показывает признак ключа восстановления репозитория
      Дано A регистрирует base r1 с recovery_key <признак>
      Когда администратор запрашивает карточку агента A
      Тогда repositories содержит base с recoveryKey "<значение>"

      Примеры:
        | признак                        | значение |
        | RECOVERY_KEY_STATE_PRESENT     | present  |
        | RECOVERY_KEY_STATE_ABSENT      | absent   |
        | RECOVERY_KEY_STATE_UNSPECIFIED | unknown  |

    @grpc @http
    Сценарий: Агент старше V3 без поля признака показывается как неизвестно
      Дано A присылает Register с base r1 без поля recovery_key
      Когда администратор запрашивает список агентов
      Тогда элемент A содержит base с recoveryKey "unknown"

    @grpc @http
    Сценарий: Новый Register заменяет признак агента
      Дано A регистрирует base r1 absent
      И затем A регистрирует base r1 present
      Когда администратор запрашивает карточку агента A
      Тогда repositories содержит base с recoveryKey "present"

    @http
    Сценарий: Признак переживает перезапуск сервера
      Дано A зарегистрировал base r1 absent
      Когда сервер перезапускается и администратор запрашивает карточку агента A
      Тогда repositories содержит base с recoveryKey "absent"

  # ===========================================================================
  Правило: Список репозиториев показывает признак по repository_id

    @http @qa
    Сценарий: Репозиторий без ключа восстановления считается в withoutRecoveryKey
      Дано A регистрирует base r1 absent, B регистрирует extra r2 present
      Когда администратор запрашивает GET /api/v1/repositories
      Тогда items — r1 с recoveryKey "absent" и r2 с recoveryKey "present"
      И withoutRecoveryKey равно 1

    @http
    Сценарий: Признак репозитория берётся из самого позднего Register с известным значением
      Дано в T0 A регистрирует base r1 absent
      И в T0 плюс 1 минута B регистрирует base2 r1 present
      И в T0 плюс 2 минуты W регистрирует base r1 unspecified
      Когда администратор запрашивает GET /api/v1/repositories
      Тогда r1 имеет recoveryKey "present", reportedByAgentId B и reportedAt T0 плюс 1 минута
      И holders r1 — A, B и W по agentId с их repositoryName и ролью

    @http
    Сценарий: При равном времени Register признак берётся у меньшего agentId
      Дано A и B в одно время T0 регистрируют r1, A absent, B present, и agentId A меньше
      Когда администратор запрашивает GET /api/v1/repositories
      Тогда r1 имеет recoveryKey "absent" и reportedByAgentId A

    @http
    Сценарий: Репозиторий только с неизвестным признаком не считается без ключа
      Дано A регистрирует base r1 unspecified
      Когда администратор запрашивает GET /api/v1/repositories
      Тогда r1 имеет recoveryKey "unknown", reportedAt null и reportedByAgentId null
      И withoutRecoveryKey равно 0

    @http
    Сценарий: Репозиторий без id не входит в список
      Дано A регистрирует base с пустым repository_id
      Когда администратор запрашивает GET /api/v1/repositories
      Тогда items пуст и withoutRecoveryKey равно 0

    @http
    Сценарий: Сообщение отозванного агента не учитывается
      Дано A регистрирует base r1 absent, затем A отозван
      Когда администратор запрашивает GET /api/v1/repositories
      Тогда items не содержит r1

    @http
    Сценарий: Репозитории другого тенанта не видны
      Дано агент тенанта T2 регистрирует base r9 absent
      Когда администратор тенанта T запрашивает GET /api/v1/repositories
      Тогда items не содержит r9 и withoutRecoveryKey равно 0

    @http
    Сценарий: Список репозиториев требует сессии администратора
      Когда клиент без cookie запрашивает GET /api/v1/repositories
      Тогда ответ 401

    @http
    Сценарий: Репозиторий без ключа восстановления получает команду для источника
      Дано A регистрирует base r1 absent
      Когда администратор запрашивает GET /api/v1/repositories
      Тогда recoveryKeyCommand r1 равно "sudo sard-agent repo key add base --for recovery"

    @http
    Сценарий: Репозиторий с ключом восстановления команды не получает
      Дано A регистрирует base r1 present
      Когда администратор запрашивает GET /api/v1/repositories
      Тогда recoveryKeyCommand r1 равно null

    @http
    Сценарий: Репозиторий только у верификатора команды не получает
      Дано W регистрирует base r1 absent, источники r1 не сообщают
      Когда администратор запрашивает GET /api/v1/repositories
      Тогда recoveryKeyCommand r1 равно null

    @http
    Сценарий: Репозиторий встроенного агента получает команду самобэкапа
      Дано встроенный агент sard-self регистрирует self r5 absent
      Когда администратор запрашивает GET /api/v1/repositories
      Тогда recoveryKeyCommand r5 содержит "self-agent repo key add self --for recovery --if-missing --config /etc/sard/self/agent.yaml"

    @http
    Сценарий: Список репозиториев не содержит сведений о ключах, кроме признака
      Дано A регистрирует base r1 present
      Когда администратор запрашивает GET /api/v1/repositories
      Тогда тело ответа не содержит ключей "password", "keyId", "label" и "keys"

  # ===========================================================================
  Правило: Карточка верификатора даёт команды для ключа верификатора

    @http @qa
    Сценарий: Карточка верификатора содержит команду key add для источника и шаблон repo add
      Дано A регистрирует base r1 present, W не сообщает r1
      Когда администратор запрашивает карточку агента W
      Тогда verifierSetup содержит r1 с sourceAgentId A, sourceHostname "src1" и repositoryName "base"
      И keyAddCommand r1 равно "sudo sard-agent repo key add base --for verifier --label vrf-3f2a9c1e"
      И repoAddTemplate r1 равно "sudo sard-agent repo add base <address> --password-from-file <file>"

    @http
    Сценарий: Репозиторий, который верификатор уже держит, не предлагается
      Дано A регистрирует base r1 present, W регистрирует base r1 present
      Когда администратор запрашивает карточку агента W
      Тогда verifierSetup не содержит r1

    @http
    Сценарий: Источник для команды — держатель с самым поздним Register
      Дано в T0 A регистрирует base r1, в T0 плюс 1 минута B регистрирует main r1
      Когда администратор запрашивает карточку агента W
      Тогда verifierSetup r1 имеет sourceAgentId B и keyAddCommand с "repo key add main"

    @http
    Сценарий: Имя репозитория с пробелом берётся в одинарные кавычки
      Дано A регистрирует репозиторий "off site" r1
      Когда администратор запрашивает карточку агента W
      Тогда keyAddCommand r1 равно "sudo sard-agent repo key add 'off site' --for verifier --label vrf-3f2a9c1e"

    @http
    Структура сценария: Имя, которое нельзя безопасно вставить в команду, команды не получает
      Дано A регистрирует репозиторий <имя> r1
      Когда администратор запрашивает карточку агента W
      Тогда verifierSetup содержит r1 с keyAddCommand null и repoAddTemplate null

      Примеры:
        | имя                    |
        | с одинарной кавычкой   |
        | с переводом строки     |

    @http
    Сценарий: У агента-источника нет блока верификатора
      Когда администратор запрашивает карточку агента A
      Тогда verifierSetup равно null

    @http
    Сценарий: Отозванный источник не предлагается верификатору
      Дано A регистрирует base r1, затем A отозван
      Когда администратор запрашивает карточку агента W
      Тогда verifierSetup не содержит r1

  # ===========================================================================
  Правило: Состояние самобэкапа содержит команду выпуска ключа восстановления

    @http
    Сценарий: После привязки состояние самобэкапа содержит команду ключа восстановления
      Дано самобэкап привязан к репозиторию "self"
      Когда администратор запрашивает состояние самобэкапа
      Тогда recoveryKeyCommand равно "docker compose run --rm --no-deps -u 0 --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER --cap-add SETUID --cap-add SETGID self-agent repo key add self --for recovery --if-missing --config /etc/sard/self/agent.yaml"

    @http
    Сценарий: До привязки команды ключа восстановления нет
      Дано самобэкап не привязан
      Когда администратор запрашивает состояние самобэкапа
      Тогда recoveryKeyCommand равно null

    @http
    Сценарий: Команда ключа восстановления называет имя текущей привязки
      Дано самобэкап привязан к репозиторию "self-2"
      Когда администратор запрашивает состояние самобэкапа
      Тогда recoveryKeyCommand содержит "self-agent repo key add self-2 --for recovery"

    # ВС1
    @http
    Сценарий: Команда пароля хоста остаётся в состоянии самобэкапа
      Дано самобэкап привязан к репозиторию "self"
      Когда администратор запрашивает состояние самобэкапа
      Тогда passwordRevealCommand равно "docker compose exec self-agent sard-agent repo password self --reveal --config /etc/sard/self/agent.yaml"

    @e2e @qa
    Сценарий: Команда ключа восстановления на машине сервера выпускает ключ и сервер узнаёт признак
      Дано самобэкап привязан к репозиторию "self" без ключа восстановления и первый бэкап прошёл
      Когда на машине сервера в терминале выполняется recoveryKeyCommand из состояния самобэкапа
      Тогда команда завершается кодом 0 и печатает пароль ключа один раз
      И repo key list self в соседе sard-self показывает ключ с назначением recovery
      И в течение 60 секунд GET /api/v1/repositories показывает репозиторий self с recoveryKey "present"

    @e2e
    Сценарий: Повтор команды ключа восстановления ничего не меняет
      Дано recoveryKeyCommand уже выполнена на машине сервера
      Когда она выполняется снова
      Тогда команда завершается кодом 0 и печатает "unchanged"
      И repo key list self показывает ровно один ключ с назначением recovery

  # ===========================================================================
  Правило: Признак доходит от хоста до сервера

    @e2e @qa
    Сценарий: Выпуск ключа восстановления на хосте меняет признак на сервере
      Дано агент-источник на стенде подключил репозиторий base с --no-recovery-key
      И GET /api/v1/repositories показывает base с recoveryKey "absent"
      Когда на хосте выполняется sudo sard-agent repo key add base --for recovery --to-file с путём на хосте
      Тогда в течение 60 секунд GET /api/v1/repositories показывает base с recoveryKey "present"

    @e2e
    Сценарий: Удаление ключа восстановления на хосте меняет признак на сервере
      Дано у репозитория base агента-источника стенда есть ключ восстановления, сервер показывает "present"
      Когда на хосте выполняется sudo sard-agent repo key remove base с id этого ключа и --yes
      Тогда в течение 60 секунд GET /api/v1/repositories показывает base с recoveryKey "absent"
