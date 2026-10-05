# language: ru
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# OQ-001, OQ-050. Отказы, после которых агент не работает, пока оператор не
# исправит причину: окончательный отказ сервера на Register и неподдерживаемый
# crypto_provider репозитория.
#
# OQ-001. Транспорт агента (A3) останавливается на FAILED_PRECONDITION,
# UNAUTHENTICATED и PERMISSION_DENIED, а INVALID_ARGUMENT на Register повторяет
# бесконечно с задержкой до минуты. Сервер отвечает INVALID_ARGUMENT на
# HOSTNAME_INVALID, FIELD_INVALID, NAME_INVALID, NAME_DUPLICATE,
# SNAPSHOT_TOO_LARGE и CONFIG_SCHEMA_INVALID (ADR 0025, таблица Register). При
# остановке агент выходит с кодом 1, и systemd (Restart=on-failure) через 10 с
# запускает его снова: отказ повторяется вечно. Причину из ErrorInfo агент не
# печатает — оператор видит только "register rejected".
#
# OQ-050. Конфиг принимает любой crypto_provider, агент всегда шифрует
# встроенным restic AES и сообщает серверу настроенное значение.
#
# Статус: утверждено владельцем 2026-10-05 (Р1–Р11 как предложены; по Р2 и
# Р11 — ответы владельца ниже).
#
# Решения владельца (2026-10-05, docs/sessions/2026-10-05-x2c-docs.md, «AX»)
#   1. OQ-001. INVALID_ARGUMENT на Register — окончательный отказ (ADR 0025,
#      «Повтор»). На любой окончательный отказ Register (INVALID_ARGUMENT,
#      FAILED_PRECONDITION, UNAUTHENTICATED, PERMISSION_DENIED) агент перестаёт
#      переподключаться и выходит с отдельным кодом выхода; юнит
#      sard-agent.service получает RestartPreventExitStatus с этим кодом, и
#      systemd агента не перезапускает. Оператор правит причину и перезапускает
#      службу сам. UNAVAILABLE и прочие временные концы соединения
#      повторяются, как раньше.
#   2. OQ-050. Репозиторий с заданным crypto_provider, отличным от встроенного
#      ("restic-aes"; пусто — встроенный), — агент не стартует: ошибка с именем
#      репозитория и значением, ненулевой код выхода, до любого подключения к
#      серверу. Проверка — при старте службы, не при чтении конфига, поэтому
#      sard-agent repo init и repo list сохраняют свою причину
#      CRYPTO_PROVIDER_UNSUPPORTED (repo-init.feature, С2, П6, В9).
#
# Решения specifier (ждут утверждения владельцем)
#   Р1. Код выхода окончательного отказа Register — 78. Это EX_CONFIG из
#       sysexits.h: systemctl status показывает его как "status=78/CONFIG",
#       что подсказывает оператору, где искать. Номер не пересекается с 0, 1,
#       2 старта агента и с 3–7 enroll (ADR 0025, «A2b — коды выхода»), лежит
#       вне 126, 127 и 128+N (оболочка, сигналы), вне 200–243 (собственные коды
#       systemd вроде 203/EXEC) и не равен 255. Код один для всех четырёх gRPC-
#       кодов: правило «не перезапускать» у них общее, а различает их строка
#       отказа (Р4). Код 78 возвращается, даже если остановка исполнителя после
#       отказа тоже вернула ошибку. Альтернатива — 8 (без имени в systemd).
#   Р2. Окончательны на Register ровно четыре кода решения 1. Любой другой код
#       Register (UNAVAILABLE, INTERNAL, UNKNOWN, DEADLINE_EXCEEDED,
#       RESOURCE_EXHAUSTED, ABORTED, UNIMPLEMENTED и прочие) и любая ошибка до
#       Register (сервер не слушает, рукопожатие TLS не прошло) повторяются с
#       прежней задержкой. ADR 0025 называет повторяемым только UNAVAILABLE —
#       для остальных кодов владелец оставил повтор (ответ О1 ниже); ADR
#       0025 правится под это.
#   Р3. Конец потока Connect никогда не окончателен, с каким бы кодом сервер
#       его ни закрыл (HELLO_REQUIRED — FAILED_PRECONDITION,
#       AGENT_DUPLICATE_SESSION — ALREADY_EXISTS и т. д.): StreamCloseReason
#       обещает, что агент переподключается после любого из них, а клон ВМ
#       должен продолжать попытки (ADR 0023, 0026). Решение 1 касается только
#       ответа на Register.
#   Р4. Строка отказа Register — одна строка stderr с префиксом
#       "sard-agent: ". Она содержит:
#         - код и текст статуса gRPC в нынешнем виде Go: "code = InvalidArgument
#           desc = register rejected";
#         - если в деталях статуса есть ErrorInfo домена sard.dev —
#           "reason <причина>", затем пары metadata "ключ=значение" в порядке
#           ключей, всё через ", " (пример: "reason SNAPSHOT_TOO_LARGE,
#           field=repositories, limit=256"); ErrorInfo другого домена не
#           показывается, как в enroll (classify.go);
#         - в конце — "not reconnecting: fix the cause, then restart the agent".
#       Управляющие символы в причине, metadata и тексте статуса выводятся
#       экранированными, как в Go ("\n" — два символа), строка не рвётся.
#       Остальная формулировка не фиксируется. Запись журнала "the server
#       refused the agent; not reconnecting" остаётся.
#   Р5. Если агента остановили (SIGTERM, SIGINT), пока шёл Register, остановка
#       важнее отказа: код 0, строки отказа нет — как сейчас.
#   Р6. В юните добавляется RestartPreventExitStatus=78; Restart=on-failure и
#       RestartSec=10s не меняются. После кода 78 служба остаётся в состоянии
#       failed, systemctl restart sard-agent запускает её как обычно.
#   Р7. Порядок старта агента (дополняет С1 agent-tls-identity.feature и С5
#       repo-init.feature): конфиг → crypto_provider → права секретных файлов
#       (A1) → ключ и сертификат (agent-tls-identity) → restic → hostname →
#       строка «connecting to …» и сеть. crypto_provider — свойство текста
#       конфига, файлов не касается; в repo init он тоже проверяется раньше
#       файлов (С5 repo-init.feature).
#   Р8. Допустимы только отсутствующий или пустой crypto_provider и ровно
#       "restic-aes" (crypto.ResticAESName). Сравнение точное: регистр и
#       пробелы значимы, как в repo init (С2 repo-init.feature).
#   Р9. Проверяются все репозитории конфига, а не только используемые. Если
#       неподдерживаемых несколько, сообщение называет первый по порядку
#       конфига, одной строкой.
#   Р10. Сообщение о crypto_provider — одна строка stderr с префиксом
#       "sard-agent: ". Содержит причину CRYPTO_PROVIDER_UNSUPPORTED (как
#       RESTIC_NOT_FOUND в ошибках restic при старте), "crypto_provider", имя
#       репозитория и значение в кавычках Go (управляющие символы экранированы)
#       и "restic-aes". Не содержит url репозитория. Текст рекомендуется взять
#       тот же, что у repo init; точная формулировка не фиксируется.
#   Р11. Код выхода при неподдерживаемом crypto_provider — 1, как у всех
#       локальных ошибок старта (конфиг, A1, ключ и сертификат, restic; С2
#       agent-tls-identity.feature). systemd будет перезапускать агента каждые
#       10 с, и каждый раз агент выйдет до сети: сервер не нагружается, в
#       журнале повторяется понятная строка. Код 78 оставлен за отказом
#       сервера; распространить его на все детерминированные локальные ошибки
#       старта владелец не стал (ответ О2 ниже).
#
# Ответы владельца на вопросы specifier (2026-10-05)
#   О1. Окончательны на Register ровно четыре кода Р2; прочие коды
#       (INTERNAL, UNKNOWN, DEADLINE_EXCEEDED, …) повторяются — это сбои
#       сервера или прокси. ADR 0025, «Повтор», поправить под это.
#   О2. Локальные ошибки старта, включая crypto_provider, выходят с кодом 1,
#       как сейчас (Р11); код 78 — только отказ сервера.
#
# Вне фичи: смена конфига без перезапуска, отзыв агента (сделан в S8b,
#       сценарии — rest-api.feature), серверная защита от частых повторов Register, поддержка других
#       crypto.Provider.
#
# Теги
#   @transport  тест пакета транспорта агента с фейковым сервером AgentService
#               по TLS и ручными часами (как тесты A3); сервер считает вызовы
#               Register и Connect и отвечает, как сказано в шаге; журнал агента
#               — в памяти теста
#   @start      запуск агента в процессе теста (run с аргументами --config C)
#               с фейковым сервером AgentService по TLS, как в @transport
#               (там, где он нужен), или с сервером, который только считает
#               TCP-подключения (agent-tls-identity.feature); stderr — буфер
#               теста
#   @unit       тест читает deploy/agent/sard-agent.service из репозитория
#   @repo       команды sard-agent repo в окружении тестов repo-init.feature
#   @qa         дополнительно проходится вручную по docs/qa/agent-start-refusals.md
#
# Общие соглашения
#   - «Конфиг C» — server.address фейкового сервера; tls.* — согласованная
#     пара ключа и сертификата, выданная тестовым CA (проходит
#     agent-tls-identity); restic.path — исполняемый restic, проходящий
#     проверку версии; два репозитория: main (локальный url, password_file с
#     правами 0600) и offsite (url "rest:http://qa:URL-MARKER@127.0.0.1:9/offsite",
#     тот же password_file); crypto_provider не задан, если шаг не говорит
#     иначе.
#   - «Строка отказа» — строка stderr, начинающаяся с "sard-agent: ", по Р4.
#   - «Суффикс отказа» — "not reconnecting: fix the cause, then restart the agent".
#   - «Сервер не получил подключений» — фейковый сервер не принял ни одного
#     TCP-соединения.
#   - «Агент переподключился» — после задержки по ручным часам сервер получил
#     следующий вызов Register.
#   - Названия сценариев цитируются в комментарии над тестом Go, поэтому в них
#     нет символов . : ; / < > [ ] \

Функция: Отказы, после которых агент ждёт оператора

  Если сервер окончательно отказал агенту в регистрации или конфиг требует
  шифрования, которого агент не умеет, агент сразу завершается, объясняет
  причину и не перезапускается сам, а не повторяет одну и ту же ошибку вечно.

  # ---------------------------------------------------------------------------
  Правило: Окончательный отказ Register завершает агента с кодом 78

    @start @qa
    Структура сценария: Отказ Register с причиной сервера завершает агента с кодом 78
      Дано сервер отвечает на Register кодом <код> и ErrorInfo домена sard.dev с причиной <причина> и metadata <metadata>
      Когда агент запускается с конфигом C
      Тогда код выхода 78
      И сервер получил ровно один вызов Register и ни одного вызова Connect
      И строка отказа одна и содержит "code = <код Go>" и "desc = register rejected"
      И строка отказа содержит "<показ>"
      И строка отказа оканчивается суффиксом отказа

      Примеры:
        | код                 | код Go             | причина               | metadata                                   | показ                                                    |
        | INVALID_ARGUMENT    | InvalidArgument    | HOSTNAME_INVALID      | field=hostname                             | reason HOSTNAME_INVALID, field=hostname                  |
        | INVALID_ARGUMENT    | InvalidArgument    | FIELD_INVALID         | field=repositories[1].backend              | reason FIELD_INVALID, field=repositories[1].backend      |
        | INVALID_ARGUMENT    | InvalidArgument    | NAME_INVALID          | field=repositories[0].name                 | reason NAME_INVALID, field=repositories[0].name          |
        | INVALID_ARGUMENT    | InvalidArgument    | NAME_DUPLICATE        | field=repositories[1].name                 | reason NAME_DUPLICATE, field=repositories[1].name        |
        | INVALID_ARGUMENT    | InvalidArgument    | SNAPSHOT_TOO_LARGE    | limit=256, field=repositories              | reason SNAPSHOT_TOO_LARGE, field=repositories, limit=256 |
        | INVALID_ARGUMENT    | InvalidArgument    | CONFIG_SCHEMA_INVALID | field=plugins[0].config_schema             | reason CONFIG_SCHEMA_INVALID, field=plugins[0].config_schema |
        | FAILED_PRECONDITION | FailedPrecondition | PROTOCOL_UNSUPPORTED  | min_supported=2, max_supported=3           | reason PROTOCOL_UNSUPPORTED, max_supported=3, min_supported=2 |

    @start
    Структура сценария: Отказ Register без ErrorInfo тоже завершает агента с кодом 78
      Дано сервер отвечает на Register кодом <код> с текстом статуса "refused-marker" без деталей
      Когда агент запускается с конфигом C
      Тогда код выхода 78
      И сервер получил ровно один вызов Register и ни одного вызова Connect
      И строка отказа одна и содержит "code = <код Go>" и "refused-marker"
      И строка отказа не содержит "reason "
      И строка отказа оканчивается суффиксом отказа

      Примеры:
        | код                 | код Go             |
        | INVALID_ARGUMENT    | InvalidArgument    |
        | FAILED_PRECONDITION | FailedPrecondition |
        | UNAUTHENTICATED     | Unauthenticated    |
        | PERMISSION_DENIED   | PermissionDenied   |

    @start
    Сценарий: ErrorInfo чужого домена не выдаётся за причину сервера
      Дано сервер отвечает на Register кодом INVALID_ARGUMENT и ErrorInfo домена other.example с причиной NAME_DUPLICATE
      Когда агент запускается с конфигом C
      Тогда код выхода 78
      И строка отказа не содержит "NAME_DUPLICATE"

    @start
    Сценарий: Управляющие символы в ответе сервера не разрывают строку отказа
      Дано сервер отвечает на Register кодом INVALID_ARGUMENT и ErrorInfo домена sard.dev с причиной "BAD" перевод строки "REASON" и metadata field со значением "x" перевод строки "y"
      Когда агент запускается с конфигом C
      Тогда код выхода 78
      И stderr содержит ровно одну строку
      И строка отказа содержит причину и значение field, где вместо перевода строки стоят обратная косая черта и буква n

    @transport
    Сценарий: Отказ INVALID_ARGUMENT после здорового потока прекращает переподключение
      Дано агент зарегистрировался и держал поток Connect дольше 30 секунд по ручным часам
      И сервер закрыл поток кодом UNAVAILABLE
      И на следующий Register сервер отвечает кодом INVALID_ARGUMENT с причиной NAME_DUPLICATE
      Когда агент переподключается
      Тогда агент прекращает работу с окончательным отказом
      И сервер получил ровно два вызова Register и один вызов Connect
      И после отказа агент не ждал задержки переподключения
      И в журнале агента после отказа нет записи "reconnecting"

    @start
    Сценарий: Остановка агента во время отказа Register завершает его с кодом 0
      Дано сервер на Register сначала останавливает агента, как это сделал бы SIGTERM, а затем отвечает кодом INVALID_ARGUMENT
      Когда агент запускается с конфигом C
      Тогда код выхода 0
      И stderr не содержит суффикс отказа

  # ---------------------------------------------------------------------------
  Правило: Временные отказы и концы потока повторяются, как раньше

    @transport
    Структура сценария: Прочий код ответа на Register повторяется после задержки
      Дано сервер отвечает на первый Register кодом <код>
      Когда агент подключается
      Тогда агент не прекращает работу
      И агент переподключился

      Примеры:
        | код                |
        | UNAVAILABLE        |
        | INTERNAL           |
        | UNKNOWN            |
        | DEADLINE_EXCEEDED  |
        | RESOURCE_EXHAUSTED |
        | ABORTED            |
        | UNIMPLEMENTED      |

    @transport
    Структура сценария: Поток, закрытый сервером с любым кодом, ведёт к переподключению
      Дано агент зарегистрировался и открыл поток Connect
      И сервер закрывает поток кодом <код>, <детали>
      Когда поток заканчивается
      Тогда агент не прекращает работу
      И агент переподключился

      Примеры:
        | код                 | детали                                                      |
        | UNAVAILABLE         | ErrorInfo домена sard.dev с причиной SESSION_EXPIRED        |
        | FAILED_PRECONDITION | ErrorInfo домена sard.dev с причиной HELLO_REQUIRED         |
        | ALREADY_EXISTS      | ErrorInfo домена sard.dev с причиной AGENT_DUPLICATE_SESSION |
        | INVALID_ARGUMENT    | без деталей                                                 |
        | UNAUTHENTICATED     | без деталей                                                 |
        | PERMISSION_DENIED   | без деталей                                                 |

    @start @qa
    Сценарий: Недоступный сервер не останавливает агента
      Дано по server.address конфига C никто не слушает
      Когда агент запускается с конфигом C и останавливается через 3 секунды
      Тогда код выхода 0
      И stderr не содержит суффикс отказа

  # ---------------------------------------------------------------------------
  Правило: systemd не перезапускает агента после окончательного отказа

    @unit @qa
    Сценарий: Юнит службы запрещает перезапуск после кода 78
      Дано файл deploy/agent/sard-agent.service
      Когда тест читает раздел Service
      Тогда RestartPreventExitStatus равен коду выхода окончательного отказа агента, то есть 78
      И Restart равен on-failure

  # ---------------------------------------------------------------------------
  Правило: Неподдерживаемый crypto_provider останавливает агента до подключения

    @start @qa
    Структура сценария: Неподдерживаемый crypto_provider одного репозитория останавливает агента
      Дано crypto_provider репозитория offsite — <значение>, у main не задан
      Когда агент запускается с конфигом C
      Тогда код выхода 1
      И stderr — ровно одна строка, она начинается с "sard-agent: "
      И stderr содержит CRYPTO_PROVIDER_UNSUPPORTED, crypto_provider и restic-aes
      И stderr содержит имя offsite в двойных кавычках и значение <значение> в двойных кавычках
      И stderr не содержит "URL-MARKER"
      И stdout не содержит "connecting to"
      И сервер не получил подключений

      Примеры:
        | значение                      |
        | gost                          |
        | RESTIC-AES                    |
        | restic-aes с пробелом в конце |
        | restic                        |

    @start
    Структура сценария: Встроенный или не заданный crypto_provider не мешает старту
      Дано crypto_provider репозитория main — <main>, репозитория offsite — <offsite>
      Когда агент запускается с конфигом C
      Тогда агент подключается к серверу
      И stderr не содержит "CRYPTO_PROVIDER_UNSUPPORTED"

      Примеры:
        | main         | offsite      |
        | не задан     | не задан     |
        | ""           | не задан     |
        | "restic-aes" | не задан     |
        | "restic-aes" | ""           |

    @start
    Сценарий: Из нескольких неподдерживаемых crypto_provider называется первый по порядку конфига
      Дано crypto_provider репозитория main — "gost", репозитория offsite — "aes-gcm"
      Когда агент запускается с конфигом C
      Тогда код выхода 1
      И stderr содержит имя main и значение gost, каждое в двойных кавычках
      И stderr не содержит "offsite" и "aes-gcm"

    @start
    Сценарий: Перевод строки в crypto_provider не разрывает сообщение
      Дано crypto_provider репозитория offsite — "gost" перевод строки "x"
      Когда агент запускается с конфигом C
      Тогда код выхода 1
      И stderr — ровно одна строка
      И stderr содержит значение в двойных кавычках, где вместо перевода строки стоят обратная косая черта и буква n

    @start
    Сценарий: Неподдерживаемый crypto_provider сообщается раньше нарушения прав секретного файла
      Дано crypto_provider репозитория offsite — "gost"
      И password_file репозиториев имеет права 0644
      Когда агент запускается с конфигом C
      Тогда код выхода 1
      И stderr содержит "CRYPTO_PROVIDER_UNSUPPORTED"
      И stderr не содержит "-rw-r--r--"

    @start
    Сценарий: Неподдерживаемый crypto_provider сообщается раньше проблемы с restic
      Дано crypto_provider репозитория offsite — "gost"
      И restic.path указывает на несуществующий файл
      Когда агент запускается с конфигом C
      Тогда код выхода 1
      И stderr содержит "CRYPTO_PROVIDER_UNSUPPORTED"
      И stderr не содержит "RESTIC_NOT_FOUND"

    @repo @qa
    Сценарий: repo init одного репозитория не спотыкается о crypto_provider другого
      Дано crypto_provider репозитория offsite — "gost"
      И репозиторий main ещё не инициализирован
      Когда оператор выполняет sard-agent repo init main с конфигом C
      Тогда код выхода класса «успех»
      И stderr не содержит "CRYPTO_PROVIDER_UNSUPPORTED"
