# language: ru
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# F1. Плагин-источник `postgresql` агента: логический дамп одной базы
# PostgreSQL (`pg_dump -Fc`) потоком в репозиторий источника через
# `restic --stdin`. Первый плагин базы данных и первый плагин с секретом.
#
# Статус: ЧЕРНОВИК. Решения ПГ1–ПГ22 предложены specifier и ждут ответа
# владельца; до подтверждения спецификация coder не передаётся.
#
# Правила владельца (описание F1, по смыслу)
#   1. Действия: на этапе 1 только BACKUP; проверка восстановления — этап 2.
#   2. Способ: `pg_dump` одной базы в формате custom, потоком в restic, без
#      промежуточного файла на диске хоста. Без pg_basebackup, WAL, PITR.
#   3. Конфиг: хост или путь к сокету, порт, база, пользователь, имя секрета с
#      паролем, режим TLS (disable, require, verify-full), необязательно —
#      исключаемые схемы и таблицы. Значение пароля в конфиге невозможно.
#   4. Поле с именем секрета помечено в схеме; по пометке сервер при
#      сохранении источника проверяет, что секрет есть у агента (S8b), а
#      консоль предлагает выбор из имён секретов агента.
#   5. `pg_dump` берётся с хоста и должен быть не старше сервера; старше или
#      не найден — FAILED до начала дампа с понятной причиной (обе версии;
#      какой пакет поставить). Путь к `pg_dump` можно задать в конфиге.
#   6. PREPARING: подключение, права на чтение, версии. Нет доступа или
#      неверный пароль — FAILED с причиной от PostgreSQL, без пароля.
#   7. Ошибка во время дампа — FAILED; неполный дамп не становится снимком,
#      который выглядит успешным.
#   8. Метаданные снимка: версия сервера, версия pg_dump, имя базы, формат.
#   9. Имя файла в снимке предсказуемо.
#  10. Документация: как восстановить базу из снимка руками; роли и
#      глобальные объекты в дамп одной базы не входят — сказано явно.
#  11. Пароль передаётся pg_dump переменной окружения процесса или временным
#      .pgpass 0600 и никогда — аргументом командной строки.
#  Нефункциональные: пароль не появляется в логах шага, журнале агента,
#  тексте результата и списке процессов; e2e — база с данными → бэкап →
#  pg_restore в чистый PostgreSQL → данные совпадают, минимум две версии.
#  Вне фичи: проверка восстановления в песочнице (этап 2), роли и глобальные
#  объекты, кластеры, реплики, WAL, pg_basebackup, самобэкап Sard (F6),
#  `sard-agent secret set` и `sard-agent repo restore` (A8).
#
# Что уже есть и что добавляет F1
#   Уже есть (A6a, A6b, A7, S8b, W2) и только закрепляется сценариями:
#     - валидация конфига по схеме до Prepare, нарушение — REJECTED;
#       неизвестный секрет — REJECTED (pluginhost/schema.go, ADR 0027);
#     - пометка поля-секрета `"format": "sard-secret"` (sdk.SecretFormat):
#       агент, сервер (ConfigCheck.kt, ADR 0031) и консоль (schemaForm.ts)
#       её уже понимают — правило 4 выполняется без правки server и web (ПГ5);
#     - поток в restic: EOF на stdin только после успешного Stream, иначе
#       SIGTERM restic и снимка нет (restic/backup.go, ADR 0027);
#     - маскирование секретов агента в логах и результате шага (ADR 0033);
#     - фазы ACCEPTED → PREPARING → DUMPING → UPLOADING; отмена и таймаут.
#   Новое в F1:
#     - Prepare, Dump, Stream плагина postgresql (сейчас ErrNotImplemented);
#     - новая схема конфига (сейчас заглушка с `host[:port]`);
#     - метки снимка от плагина: аддитивное поле в sdk.Dump (ПГ11);
#     - RESTORE — отложенное восстановление, как у files (ПГ13);
#     - docs/plugins/postgresql.md, ADR (пишет coder), e2e с PostgreSQL.
#
# Затронутые модули: agent (plugins/postgresql, internal/pluginhost — метки
#   плагина), sdk (agent/plugins/sdk, Apache-2.0: аддитивное поле Dump.Tags),
#   docs (этот файл, QA, docs/plugins/postgresql.md, ADR, журнал сессии,
#   examples/workflows/postgres-nightly.yaml), test/e2e (образ агента с
#   клиентом PostgreSQL, сквозные тесты), упаковка агента (Suggests, ПГ21).
#   Не затронуты: proto, server, web, cli.
#
# Решения (предложены specifier; ЖДУТ ОТВЕТА ВЛАДЕЛЬЦА)
#   ПГ1. Проверки PREPARING без Go-драйвера PostgreSQL. Агенту разрешены
#       только stdlib, gRPC, protobuf, YAML и валидатор JSON Schema, поэтому
#       подключение, пароль, права и версия сервера проверяются запуском
#       `psql` с хоста: `psql -X -w -A -t -v ON_ERROR_STOP=1 -c <запрос>`.
#       `psql` берётся из того же каталога, что и выбранный `pg_dump`
#       (ПГ8): во всех дистрибутивах они в одном пакете (postgresql-client
#       в Debian и Ubuntu, postgresql в RHEL и Fedora, postgresql-client в
#       Alpine, официальный образ postgres). Версия клиента — `pg_dump
#       --version`. Отвергнуто: Go-драйвер (запрещён правилом 7); своя
#       реализация протокола PostgreSQL на stdlib (SCRAM, MD5, TLS,
#       SSLRequest — сотни строк чувствительного кода, расходится с libpq по
#       pg_hba, сервисным файлам и способам входа); пробный `pg_dump
#       --schema-only` (читает весь каталог базы, версию сервера пришлось бы
#       выковыривать из комментария дампа, а она нужна до старта restic —
#       метки задаются при запуске restic).
#   ПГ2. Что значит «права на чтение» в PREPARING. Проверяется: подключение
#       и вход к базе (CONNECT), версия сервера и роль. Если роль не
#       суперпользователь и не член `pg_read_all_data`, в лог шага уходит
#       одна строка WARN с именем роли (шаг продолжается). Права на отдельные
#       таблицы проверяет сам pg_dump: он берёт ACCESS SHARE на каждую
#       выгружаемую таблицу до выгрузки данных и с учётом исключений, поэтому
#       нехватка SELECT — FAILED в UPLOADING с причиной pg_dump, называющей
#       таблицу, снимка нет. Отступление от буквы правила 6: точная проверка
#       по таблицам в PREPARING требовала бы повторить в агенте семантику
#       шаблонов исключения pg_dump. Документация советует выдавать роли
#       `pg_read_all_data` (PG 14+), а при RLS — `BYPASSRLS`.
#   ПГ3. Пароль — только в переменной окружения PGPASSWORD процессов
#       `psql` и `pg_dump`; без .pgpass. На Linux окружение процесса видно
#       только его пользователю и root — тем же, кто читает файл секрета;
#       временный файл остался бы на диске после SIGKILL агента, а формат
#       .pgpass требует экранирования `:` и `\`. Значение секрета — содержимое
#       файла без завершающих `\r\n` (как у маскировщика, OQ-118); пустое
#       значение или значение с байтом NUL — FAILED в PREPARING с именем
#       секрета. Переменные `PG*` и `LC_ALL` из окружения агента дочерним
#       процессам не передаются; задаётся `LC_MESSAGES=C`, чтобы сообщения
#       libpq и pg_dump были на английском, как прочие сообщения агента.
#   ПГ4. Параметры подключения — строкой conninfo в `--dbname=` (psql и
#       pg_dump): host, port, dbname, user, sslmode, sslrootcert, а также
#       connect_timeout=30, keepalives_idle=60, keepalives_interval=10,
#       keepalives_count=6, application_name=sard-agent. Каждое значение в
#       одинарных кавычках, `\` и `'` экранированы: имя базы вида
#       "host=evil" — имя, а не строка подключения. Пароля в строке нет.
#       Таймауты не настраиваются на этапе 1: без них недоступный хост или
#       полуоткрытое соединение держали бы шаг до OS-таймаута TCP (часы).
#   ПГ5. Пометка поля-секрета — существующая `"format": "sard-secret"`
#       (ADR 0027), а не новое `x-sard-secret: true` из описания: агент,
#       сервер (S8b) и консоль (W2) её уже понимают, общее правило для
#       будущих плагинов уже действует. Поле называется `password_ref`, как
#       в agent.example.yaml, ADR 0008 и proto.
#   ПГ6. Поля схемы (draft 2020-12, additionalProperties false; у каждого
#       поля title, description, examples и x-sard-i18n ru):
#         host           строка, обязательно: имя хоста, IPv4, IPv6 без
#                        скобок, или абсолютный путь КАТАЛОГА сокета
#                        (например /var/run/postgresql); 1…255 байт; без
#                        запятой (libpq читает её как список хостов), без
#                        `@` и пробелов, без "/" кроме пути, начинающегося с "/"
#         port           целое 1…65535, по умолчанию 5432 (для сокета —
#                        номер в имени файла .s.PGSQL.<port>)
#         database       строка 1…63 байта, обязательно
#         user           строка 1…63 байта, обязательно
#         password_ref   строка, format sard-secret, обязательно
#         tls_mode       disable, require, verify-full; по умолчанию require
#                        для TCP; при host-сокете допустим только disable
#                        (TLS над сокетом не работает) и он же — умолчание
#         tls_root_cert  НОВОЕ, нет в описании: абсолютный путь к CA-файлу
#                        на хосте агента; допустимо только при verify-full;
#                        без него libpq ищет ~/.postgresql/root.crt
#                        пользователя агента
#         exclude_schemas, exclude_tables  массивы шаблонов pg_dump, 0…256
#                        элементов, каждый 1…1024 байта, без повторов
#         pg_dump_path   абсолютный путь, необязательно
#       Ни одно строковое поле не допускает NUL.
#   ПГ7. Версии сравниваются по старшему номеру (major), как это делает сам
#       pg_dump: major pg_dump < major сервера — FAILED; та же major с более
#       старой минорной — допустимо. Сервер ниже минимальной версии (ПГ19) —
#       FAILED с версией сервера и минимумом. Версия pg_dump — первый токен
#       после "(PostgreSQL)" в `pg_dump --version` (17.2, 18beta1, 18rc1);
#       версия сервера — из server_version_num (160004 → 16.4).
#   ПГ8. Поиск pg_dump: pg_dump_path, иначе PATH агента. Нет — FAILED:
#       «pg_dump not found …: install the PostgreSQL client package
#       (postgresql-client on Debian/Ubuntu, postgresql on RHEL/Fedora) or
#       set pg_dump_path». pg_dump_path задан, но файла нет или он не
#       исполняемый — FAILED с этим путём. Нет psql рядом — то же с psql.
#   ПГ9. Аргументы pg_dump: --format=custom --compress=0 --no-password
#       --lock-wait-timeout=300000 и по одному --exclude-schema=<шаблон> и
#       --exclude-table=<шаблон> (значение слито с флагом — шаблон с "-"
#       не флаг). --compress=0: сжатый дамп restic почти не дедуплицирует,
#       а сжимает restic сам (репозиторий v2). Ожидание блокировок таблиц —
#       не больше 5 минут, как ожидание репозитория в A6b (Ф10).
#   ПГ10. Целостность снимка. Stream возвращает успех только когда pg_dump
#       завершился с кодом 0, весь его stdout передан restic и вывод
#       непустой и начинается с сигнатуры "PGDMP" архива custom. Иначе
#       Stream возвращает ошибку, и существующий механизм A6a останавливает
#       restic до EOF: снимок не сохраняется, блокировок не остаётся.
#       Принятый остаточный риск — окно, описанное в ADR 0027 («Оборотная
#       сторона»).
#   ПГ11. Метки снимка. В sdk.Dump аддитивно добавляется Tags (ключ →
#       значение, Apache-2.0, обратно совместимо); агент пишет их restic как
#       `<плагин>.<ключ>=<значение>` после меток шага. У postgresql:
#       postgresql.server_version=16.4, postgresql.pg_dump_version=18.0,
#       postgresql.database=<имя по правилу ПГ12>, postgresql.format=custom.
#       Ключ, совпавший с ключом метки шага, — FAILED до запуска restic.
#   ПГ12. Имя файла в снимке — `<база>.dump`, где байты имени базы вне
#       [A-Za-z0-9_-] записаны как %XX (UTF-8, заглавные hex): app →
#       app.dump, "my db" → my%20db.dump, "a.b" → a%2Eb.dump. Правило
#       обратимо, имя никогда не "." и не "..", без "/" и запятой; то же
#       кодирование — у метки postgresql.database. Альтернатива на выбор
#       владельца — постоянное имя database.dump.
#   ПГ13. Действия в Register: BACKUP и RESTORE (общее правило адаптера A6a).
#       RESTORE — отложенное восстановление, как у files (Ф5): FAILED с
#       «restore for the postgresql plugin is not implemented yet», restic
#       не запускается. VERIFY не объявлен (плагин не sdk.Verifier), RUN —
#       REJECTED.
#   ПГ14. Фазы: PREPARING — ПГ1–ПГ8; DUMPING — мгновенно (Dump только
#       называет файл и метки); UPLOADING — pg_dump и restic одновременно,
#       bytes_processed по статусам restic.
#   ПГ15. Отмена и таймаут: psql или pg_dump получают SIGTERM, через 10 с —
#       SIGKILL (как restic); после шага не остаётся ни процессов psql,
#       pg_dump, restic, ни сессии application_name sard-agent на сервере
#       PostgreSQL дольше 10 с.
#   ПГ16. Документация docs/plugins/postgresql.md: роль для бэкапа
#       (LOGIN, pg_read_all_data, при RLS — BYPASSRLS); секрет вручную до A8
#       (файл 0600 владельца агента, `secrets:` в agent.yaml, перезапуск
#       агента); поля конфига; восстановление руками: `restic dump <снимок>
#       /<база>.dump` или `restic restore`, затем `createdb` и `pg_restore`
#       в новую базу (после A8 — `sard-agent repo restore`); pg_restore не
#       старше pg_dump из метки postgresql.pg_dump_version; роли, табличные
#       пространства и прочие глобальные объекты в дамп не входят — их
#       выгружают `pg_dumpall --globals-only` отдельно, на новом кластере —
#       создать роли заранее или `pg_restore --no-owner --no-acl`.
#   ПГ17 (открытый вопрос 1). Глобальные объекты — не в F1. Снимок
#       `--stdin` — один файл; второй файл значит второй снимок или смену
#       формата потока, а `pg_dumpall --globals-only` требует прав шире,
#       чем чтение одной базы. Документация говорит, как сделать руками
#       (ПГ16); отдельная фича — после F1.
#   ПГ18 (открытый вопрос 2). Одна база на источник: один снимок — один
#       дамп, у каждой базы своё расписание, статус и восстановление;
#       несколько баз — несколько источников.
#   ПГ19 (открытый вопрос 3). Минимальная версия сервера — PostgreSQL 14
#       (самая старая из поддерживаемых сообществом на октябрь 2026;
#       pg_read_all_data появился в 14). Сервер 13 и старше — FAILED в
#       PREPARING. e2e — серверы 14 и 18 (крайние поддерживаемые).
#   ПГ20. e2e-образ агента для F1 строится на официальном образе postgres:18
#       (pg_dump и psql 18) с теми же собранными sard-agent и restic (ADR
#       0045); для сценария старого pg_dump — на postgres:14. Сборка образа
#       ничего не скачивает, кроме базового образа.
#   ПГ21. Пакеты deb и rpm агента получают необязательную зависимость
#       (Suggests: postgresql-client в deb, Suggests: postgresql в rpm);
#       обязательной нет — агент без баз клиент PostgreSQL не требует.
#   ПГ22. Сообщения шага — одна строка на английском; ошибка libpq или
#       pg_dump цитируется (последняя строка с "error:" или "FATAL:"), полный
#       stderr psql и pg_dump — строками лога шага (WARN для строк с
#       "error:", "FATAL:", "warning:", иначе INFO).
#
# Теги
#   @unit      тест Go: плагин postgresql через адаптер pluginhost и
#              настоящий исполнитель; psql и pg_dump — фейковый запуск
#              процессов с выводом, снятым с настоящих версий 14 и 18
#              (testdata/); restic — фейковый Executor или restic 0.19.1
#   @restic    интеграционный тест Go (build tag integration): настоящий
#              restic 0.19.1, локальный репозиторий R, фейковый pg_dump —
#              исполняемый файл теста, пишущий заданные байты и код выхода
#   @register  контрактный тест снимка Register (app/register_contract_test.go)
#   @doc       тест читает docs/plugins/postgresql.md и ищет строки
#   @e2e       Kotlin, Testcontainers (test/e2e): PostgreSQL 14 и 18, образ
#              агента ПГ20, сервер Sard, шаг запускается через REST
#   @qa        дополнительно проходится вручную по docs/qa/postgresql-plugin.md
#
# Общие соглашения
#   - «Шаг postgresql» — RunStep: plugin "postgresql", action BACKUP,
#     repository_name R (настроен и инициализирован), tags {sard.source: s1,
#     sard.run: r1}, без timeout, config_json — указанный конфиг.
#   - «Конфиг K» — {"host": "db", "database": "app", "user": "backup",
#     "password_ref": "pg-app", "tls_mode": "disable"}.
#   - «Секрет pg-app» — секрет агента со значением P = "p'a:s\\s w0rd-Ж";
#     в нём кавычка, двоеточие, обратная косая, пробел и не-ASCII.
#   - «Пароль не раскрыт» — P не встречается ни в сообщении шага, ни в
#     строках его лога, ни в журнале агента, ни в аргументах командной
#     строки (/proc/<pid>/cmdline) ни одного дочернего процесса агента.
#   - «Снимков не прибавилось» — `restic snapshots --json` в R даёт столько
#     же снимков, сколько до шага; «блокировок нет» — `restic list locks` пуст.
#   - «Дамп не запускался» — ни pg_dump без --version, ни restic backup не
#     вызывались.
#   - «Результат без вывода» — у StepResult нет поля output.
#   - Названия сценариев цитируются именами тестов, поэтому в них нет
#     символов . : ; / < > [ ] \

Функция: Плагин postgresql — логический дамп базы PostgreSQL в репозиторий

  Администратор указывает базу PostgreSQL и имя секрета с паролем, и агент
  сохраняет дамп базы в формате custom в репозиторий источника, не кладя его
  на диск хоста. Из снимка базу восстанавливают pg_restore. Пароль не
  покидает хост и нигде не печатается.

  Предыстория:
    Дано агент с плагином postgresql, репозиторием R и секретом pg-app
    И репозиторий R инициализирован, его id равен RID

  # ---------------------------------------------------------------------------
  Правило: Плагин объявляет бэкап, восстановление отложено

    @register @qa
    Сценарий: В Register плагин postgresql объявляет действия BACKUP и RESTORE
      Когда агент собирает снимок для Register
      Тогда у плагина postgresql список actions — ровно [ACTION_BACKUP, ACTION_RESTORE]
      И версия плагина postgresql равна версии агента

    @unit
    Сценарий: Восстановление плагином postgresql завершается FAILED без restic
      Дано шаг postgresql с action ACTION_RESTORE и snapshot_id "abc"
      Когда агент выполняет шаг
      Тогда статус шага FAILED
      И сообщение говорит, что восстановление для плагина postgresql ещё не реализовано
      И restic не запускался
      И psql и pg_dump не запускались
      И результат без вывода

    @unit
    Структура сценария: Проверка и запуск скрипта плагином postgresql отклоняются
      Дано шаг postgresql с action <действие> и snapshot_id "abc"
      Когда агент получает шаг
      Тогда статус шага REJECTED
      И сообщение называет плагин postgresql и действие <действие>
      И psql и pg_dump не запускались

      Примеры:
        | действие      |
        | ACTION_VERIFY |
        | ACTION_RUN    |

  # ---------------------------------------------------------------------------
  Правило: Схема конфига называет секрет по имени и готова для формы консоли

    @register
    Сценарий: Схема postgresql проходит ограничения Register
      Когда агент собирает снимок для Register
      Тогда config_schema плагина postgresql — один JSON-объект не больше 65536 байт без \u0000
      И схема компилируется как JSON Schema draft 2020-12

    @unit @qa
    Сценарий: Поле password_ref помечено как имя секрета агента
      Когда читается config_schema плагина postgresql
      Тогда свойство password_ref имеет type string и format sard-secret
      И password_ref входит в required
      И ни одно другое свойство не имеет format sard-secret

    @unit
    Сценарий: В схеме нет поля для значения пароля
      Когда читается config_schema плагина postgresql
      Тогда additionalProperties схемы равно false
      И ни одно свойство не называется password, passwd, dsn, url или conninfo

    @unit @qa
    Структура сценария: У каждого поля схемы есть заголовок, описание, пример и перевод
      Когда читается config_schema плагина postgresql
      Тогда у свойства <поле> непустые title и description
      И у свойства <поле> непустой массив examples, каждый элемент которого проходит схему этого свойства
      И у свойства <поле> есть x-sard-i18n ru с непустыми title и description

      Примеры:
        | поле            |
        | host            |
        | port            |
        | database        |
        | user            |
        | password_ref    |
        | tls_mode        |
        | tls_root_cert   |
        | exclude_schemas |
        | exclude_tables  |
        | pg_dump_path    |

    @unit
    Сценарий: Описание исключений ссылается на синтаксис шаблонов pg_dump
      Когда читается config_schema плагина postgresql
      Тогда description свойств exclude_schemas и exclude_tables содержит ссылку на документацию pg_dump о шаблонах

  # ---------------------------------------------------------------------------
  Правило: Неверный конфиг отклоняется до любых действий

    @unit
    Структура сценария: Конфиг, нарушающий схему, отклоняется с указанием поля
      Дано шаг postgresql с config_json <конфиг>
      Когда агент выполняет шаг
      Тогда статус шага REJECTED
      И сообщение называет поле <поле>
      И psql и pg_dump не запускались
      И результат без вывода

      Примеры:
        | конфиг                                                         | поле             |
        | {}                                                             | (root)           |
        | K без host                                                     | (root)           |
        | K без database                                                 | (root)           |
        | K без user                                                     | (root)           |
        | K без password_ref                                             | (root)           |
        | K и "password" "x"                                             | (root)           |
        | K с host ""                                                    | /host            |
        | K с host "a,b"                                                 | /host            |
        | K с host "u@db"                                                | /host            |
        | K с host "postgresql   db"                                     | /host            |
        | K с host "postgresql://u:x@db/app"                             | /host            |
        | K с host "run/postgresql"                                      | /host            |
        | K с host из 256 байт                                           | /host            |
        | K с port 0                                                     | /port            |
        | K с port 65536                                                 | /port            |
        | K с port "5432"                                                | /port            |
        | K с database ""                                                | /database        |
        | K с database из 64 байт                                        | /database        |
        | K с user ""                                                    | /user            |
        | K с password_ref 7                                             | /password_ref    |
        | K с tls_mode "prefer"                                          | /tls_mode        |
        | K с host "/var/run/postgresql" и tls_mode "require"            | /tls_mode        |
        | K с tls_mode "require" и tls_root_cert "/etc/ca.pem"           | /tls_root_cert   |
        | K с tls_mode "verify-full" и tls_root_cert "ca.pem"            | /tls_root_cert   |
        | K с exclude_schemas "audit"                                    | /exclude_schemas |
        | K с exclude_schemas [""]                                       | /exclude_schemas/0 |
        | K с exclude_tables ["a", "a"]                                  | /exclude_tables  |
        | K с exclude_tables из 257 шаблонов                             | /exclude_tables  |
        | K с exclude_tables [шаблон из 1025 байт]                       | /exclude_tables/0 |
        | K с pg_dump_path "pg_dump"                                     | /pg_dump_path    |
        | не JSON                                                        | (root)           |

    @unit
    Структура сценария: Строка с символом NUL отклоняется
      Дано шаг postgresql с конфигом K, в котором <поле> содержит \u0000
      Когда агент выполняет шаг
      Тогда статус шага REJECTED
      И сообщение называет поле <поле>

      Примеры:
        | поле              |
        | /host             |
        | /database         |
        | /user             |
        | /exclude_tables/0 |
        | /pg_dump_path     |

    @unit
    Структура сценария: Допустимые значения на границе пределов принимаются схемой
      Дано шаг postgresql с конфигом K, в котором <что>
      Когда агент проверяет конфиг шага
      Тогда нарушений схемы нет

      Примеры:
        | что                                                  |
        | port 1                                               |
        | port 65535                                           |
        | host "::1"                                           |
        | host "10.0.0.5"                                      |
        | host "db-1.example.com"                              |
        | host "/var/run/postgresql" и нет tls_mode            |
        | database из 63 байт                                  |
        | database "my db"                                     |
        | exclude_schemas из 256 различных шаблонов            |
        | tls_mode "verify-full" и tls_root_cert "/etc/ca.pem" |

    @unit
    Сценарий: Неизвестный секрет отклоняет шаг до подключения
      Дано шаг postgresql с конфигом K и password_ref "nope", которого нет на хосте
      Когда агент выполняет шаг
      Тогда статус шага REJECTED
      И сообщение называет секрет nope
      И psql и pg_dump не запускались

    @e2e
    Сценарий: Сервер не сохраняет источник postgresql со ссылкой на неизвестный секрет
      Дано агент X зарегистрирован с секретом pg-app и плагином postgresql
      Когда администратор создаёт источник postgresql агента X с конфигом K и password_ref "nope"
      Тогда ответ 422 с ошибкой у поля config/password_ref, называющей nope

    @unit
    Сценарий: Шаг с неизвестным репозиторием отклоняется
      Дано шаг postgresql с repository_name "nope", которого нет на хосте
      Когда агент получает шаг
      Тогда статус шага REJECTED
      И psql и pg_dump не запускались

  # ---------------------------------------------------------------------------
  Правило: pg_dump и psql берутся с хоста, их отсутствие объясняется

    @unit @qa
    Сценарий: Отсутствие pg_dump в PATH проваливает шаг с подсказкой о пакете
      Дано в PATH агента нет pg_dump
      И шаг postgresql с конфигом K
      Когда агент выполняет шаг
      Тогда статус шага FAILED
      И сообщение говорит, что pg_dump не найден, и называет пакеты postgresql-client и postgresql и поле pg_dump_path
      И дамп не запускался
      И последняя фаза прогресса — PREPARING
      И результат без вывода

    @unit
    Структура сценария: Негодный pg_dump_path проваливает шаг и называет путь
      Дано файл "/opt/pg/bin/pg_dump" <состояние>
      И шаг postgresql с конфигом K и pg_dump_path "/opt/pg/bin/pg_dump"
      Когда агент выполняет шаг
      Тогда статус шага FAILED
      И сообщение называет "/opt/pg/bin/pg_dump" и причину "<причина>"
      И дамп не запускался

      Примеры:
        | состояние                     | причина                   |
        | не существует                 | no such file or directory |
        | существует без права x        | permission denied         |
        | является каталогом            | is a directory            |

    @unit
    Сценарий: Указанный pg_dump_path используется вместо pg_dump из PATH
      Дано в PATH агента есть pg_dump 16 и в "/opt/pg18/bin" есть pg_dump 18 и psql 18
      И шаг postgresql с конфигом K и pg_dump_path "/opt/pg18/bin/pg_dump"
      Когда агент выполняет шаг
      Тогда запускались "/opt/pg18/bin/psql" и "/opt/pg18/bin/pg_dump"
      И pg_dump из PATH не запускался

    @unit
    Сценарий: Отсутствие psql рядом с pg_dump проваливает шаг с подсказкой о пакете
      Дано в каталоге выбранного pg_dump нет psql
      Когда агент выполняет шаг postgresql с конфигом K
      Тогда статус шага FAILED
      И сообщение называет psql, его ожидаемый путь и пакеты postgresql-client и postgresql
      И дамп не запускался

    @unit
    Сценарий: Непонятный ответ pg_dump о версии проваливает шаг
      Дано pg_dump --version выводит "something else" и завершается кодом 0
      Когда агент выполняет шаг postgresql с конфигом K
      Тогда статус шага FAILED
      И сообщение содержит "something else"
      И дамп не запускался

  # ---------------------------------------------------------------------------
  Правило: pg_dump не старше сервера, сервер не старше минимальной версии

    @unit
    Структура сценария: Версии разбираются из ответа pg_dump и сервера
      Дано pg_dump --version выводит "<вывод pg_dump>"
      И сервер сообщает server_version_num <номер>
      Когда агент выполняет шаг postgresql с конфигом K
      Тогда версия pg_dump определена как <версия pg_dump>
      И версия сервера определена как <версия сервера>

      Примеры:
        | вывод pg_dump                                          | номер  | версия pg_dump | версия сервера |
        | pg_dump (PostgreSQL) 18.0                              | 180000 | 18.0           | 18.0           |
        | pg_dump (PostgreSQL) 17.2 (Ubuntu 17.2-1.pgdg24.04+1)  | 160004 | 17.2           | 16.4           |
        | pg_dump (PostgreSQL) 18beta1                           | 140013 | 18beta1        | 14.13          |
        | pg_dump (PostgreSQL) 18rc1                             | 180000 | 18rc1          | 18.0           |

    @unit @qa
    Сценарий: pg_dump старше сервера проваливает шаг до дампа и называет обе версии
      Дано pg_dump версии 16.4 и сервер PostgreSQL версии 18.0
      Когда агент выполняет шаг postgresql с конфигом K
      Тогда статус шага FAILED
      И сообщение содержит "16.4" и "18.0" и говорит поставить pg_dump 18 или новее или задать pg_dump_path
      И дамп не запускался
      И последняя фаза прогресса — PREPARING

    @e2e
    Сценарий: Настоящий pg_dump 14 против сервера 18 проваливает шаг до дампа
      Дано агент в образе с pg_dump 14 и сервер PostgreSQL 18 с базой app
      Когда администратор запускает источник postgresql на эту базу
      Тогда шаг FAILED, сообщение содержит версию pg_dump 14 и версию сервера 18
      И в репозитории не прибавилось снимков

    @unit
    Структура сценария: pg_dump новее или той же старшей версии подходит
      Дано pg_dump версии <pg_dump> и сервер PostgreSQL версии <сервер>
      Когда агент выполняет шаг postgresql с конфигом K
      Тогда проверка версий не проваливает шаг
      И pg_dump запускается для дампа

      Примеры:
        | pg_dump | сервер |
        | 18.0    | 14.13  |
        | 16.2    | 16.4   |
        | 16.4    | 16.4   |

    @unit
    Сценарий: Сервер старше PostgreSQL 14 не поддерживается
      Дано pg_dump версии 18.0 и сервер PostgreSQL версии 13.16
      Когда агент выполняет шаг postgresql с конфигом K
      Тогда статус шага FAILED
      И сообщение содержит "13.16" и минимальную версию 14
      И дамп не запускался

  # ---------------------------------------------------------------------------
  Правило: Подготовка проверяет подключение и вход и сообщает причину PostgreSQL

    @unit
    Сценарий: Фазы шага идут от подготовки к загрузке
      Когда агент успешно выполняет шаг postgresql с конфигом K
      Тогда фазы прогресса по порядку — ACCEPTED, PREPARING, DUMPING, UPLOADING
      И psql запускался в фазе PREPARING, а pg_dump для дампа — в фазе UPLOADING

    @unit @e2e @qa
    Структура сценария: Ошибка подключения или входа проваливает шаг с причиной PostgreSQL
      Дано <сбой>
      Когда агент выполняет шаг postgresql с конфигом K
      Тогда статус шага FAILED
      И сообщение содержит <причину>
      И пароль не раскрыт
      И дамп не запускался
      И последняя фаза прогресса — PREPARING
      И результат без вывода

      Примеры:
        | сбой                                                     | причину                                       |
        | пароль в секрете pg-app не подходит                      | password authentication failed for user       |
        | базы app на сервере нет                                  | database "app" does not exist                 |
        | роли backup на сервере нет                               | password authentication failed for user       |
        | pg_hba сервера не пускает роль backup к базе app         | no pg_hba.conf entry                          |
        | роль backup без права CONNECT к базе app                 | permission denied for database "app"          |
        | имя хоста db не разрешается                              | could not translate host name "db"            |
        | на порту сервера никто не слушает                        | Connection refused                            |
        | tls_mode require, а сервер без TLS                       | server does not support SSL                   |
        | tls_mode verify-full, а сертификат сервера на другое имя | does not match host name                      |
        | tls_mode verify-full, а CA из tls_root_cert не тот       | certificate verify failed                     |

    @unit
    Сценарий: Недоступный хост проваливает подготовку не позже чем через 30 секунд
      Дано пакеты к хосту сервера PostgreSQL теряются без ответа
      Когда агент выполняет шаг postgresql с конфигом K
      Тогда psql получил connect_timeout 30 в строке подключения
      И статус шага FAILED с причиной libpq о таймауте

    @unit
    Сценарий: Роль без pg_read_all_data получает предупреждение, шаг продолжается
      Дано роль backup не суперпользователь и не член pg_read_all_data
      Когда агент выполняет шаг postgresql с конфигом K
      Тогда в логе шага ровно одна строка WARN, называющая роль backup и pg_read_all_data
      И pg_dump запускается для дампа

    @unit
    Сценарий: Роль с pg_read_all_data предупреждения не получает
      Дано роль backup — член pg_read_all_data
      Когда агент выполняет шаг postgresql с конфигом K
      Тогда в логе шага нет строки WARN о правах роли

    @e2e @qa
    Сценарий: Таблица без права чтения проваливает шаг без снимка и называется
      Дано в базе app таблица secret_t, на которую у роли backup нет SELECT
      Когда администратор запускает источник postgresql на базу app
      Тогда шаг FAILED, сообщение содержит причину pg_dump с именем secret_t
      И снимков не прибавилось
      И блокировок нет

    @e2e
    Сценарий: Таблица без права чтения под исключением не мешает бэкапу
      Дано в базе app таблица secret_t, на которую у роли backup нет SELECT
      Когда администратор запускает источник postgresql на базу app с exclude_tables ["secret_t"]
      Тогда шаг SUCCEEDED

  # ---------------------------------------------------------------------------
  Правило: Пароль передаётся только окружением процесса и нигде не печатается

    @unit
    Сценарий: Пароль доходит до psql и pg_dump только через PGPASSWORD
      Когда агент выполняет шаг postgresql с конфигом K
      Тогда psql и pg_dump получили PGPASSWORD, равный P
      И ни один аргумент psql и pg_dump не содержит P
      И ни один файл в каталогах состояния и временных файлов агента не содержит P

    @unit
    Сценарий: Завершающий перевод строки в файле секрета не входит в пароль
      Дано файл секрета pg-app содержит P и "\r\n" в конце
      Когда агент выполняет шаг postgresql с конфигом K
      Тогда psql и pg_dump получили PGPASSWORD, равный P

    @unit
    Структура сценария: Пустой или содержащий NUL секрет проваливает шаг до подключения
      Дано файл секрета pg-app содержит <содержимое>
      Когда агент выполняет шаг postgresql с конфигом K
      Тогда статус шага FAILED
      И сообщение называет секрет pg-app и <причину>
      И psql и pg_dump не запускались

      Примеры:
        | содержимое          | причину            |
        | пустую строку       | is empty           |
        | только "\n"         | is empty           |
        | "ab" NUL "cd"       | contains a NUL byte |

    @unit
    Сценарий: Переменные PG и LC_ALL окружения агента не влияют на подключение
      Дано окружение агента содержит PGHOST=other, PGPASSWORD=wrong, PGSSLMODE=disable, PGOPTIONS=-c x=1 и LC_ALL=ru_RU.UTF-8
      Когда агент выполняет шаг postgresql с конфигом K
      Тогда окружение psql и pg_dump не содержит PGHOST, PGSSLMODE, PGOPTIONS и LC_ALL
      И PGPASSWORD равен P
      И LC_MESSAGES равен C

    @unit
    Сценарий: Имя базы, похожее на строку подключения, остаётся именем базы
      Дано шаг postgresql с конфигом K и database "app' host=evil password=x"
      Когда агент выполняет шаг
      Тогда строка подключения psql и pg_dump содержит dbname со значением "app' host=evil password=x" в кавычках и с экранированной кавычкой
      И ключ host в строке подключения ровно один и равен "db"
      И в строке подключения нет ключа password

    @unit
    Сценарий: Параметры подключения передаются строкой подключения с таймаутами
      Когда агент выполняет шаг postgresql с конфигом K и port 6432
      Тогда строка подключения psql и pg_dump содержит host db, port 6432, dbname app, user backup, sslmode disable
      И содержит connect_timeout 30, keepalives_idle 60, keepalives_interval 10, keepalives_count 6, application_name sard-agent

    @unit
    Сценарий: Без tls_mode для TCP-хоста используется require
      Дано шаг postgresql с конфигом K без tls_mode
      Когда агент выполняет шаг
      Тогда строка подключения содержит sslmode require

    @unit
    Сценарий: Для сокета без tls_mode используется disable
      Дано шаг postgresql с конфигом K с host "/var/run/postgresql" без tls_mode
      Когда агент выполняет шаг
      Тогда строка подключения содержит host "/var/run/postgresql" и sslmode disable

    @unit
    Сценарий: tls_root_cert передаётся как sslrootcert
      Дано шаг postgresql с конфигом K, tls_mode verify-full и tls_root_cert "/etc/sard/pg-ca.pem"
      Когда агент выполняет шаг
      Тогда строка подключения содержит sslmode verify-full и sslrootcert "/etc/sard/pg-ca.pem"

    @unit
    Сценарий: Сообщение PostgreSQL с паролем маскируется
      Дано psql завершается ошибкой, текст которой содержит P
      Когда агент выполняет шаг postgresql с конфигом K
      Тогда статус шага FAILED
      И пароль не раскрыт

    @e2e @qa
    Сценарий: Пароль не виден в списке процессов во время дампа
      Дано база app такого размера, что дамп идёт дольше 5 секунд
      Когда администратор запускает источник postgresql на базу app
      Тогда во время фазы UPLOADING ни одна командная строка процессов контейнера агента не содержит P
      И после шага ни логи шага, ни сообщение шага, ни журнал агента не содержат P

  # ---------------------------------------------------------------------------
  Правило: Успешный бэкап — дамп custom одним файлом с предсказуемым именем

    @unit
    Сценарий: pg_dump запускается в формате custom без сжатия и с ограничением ожидания блокировок
      Когда агент выполняет шаг postgresql с конфигом K
      Тогда pg_dump получил аргументы --format=custom, --compress=0, --no-password и --lock-wait-timeout=300000
      И restic backup получил --stdin и --stdin-filename=app.dump

    @unit
    Сценарий: Исключения передаются pg_dump по одному слитно с флагом
      Дано шаг postgresql с конфигом K, exclude_schemas ["audit", "-x"] и exclude_tables ["public.log_*"]
      Когда агент выполняет шаг
      Тогда pg_dump получил аргументы --exclude-schema=audit, --exclude-schema=-x и --exclude-table=public.log_* в этом порядке

    @unit
    Структура сценария: Имя файла дампа в снимке строится из имени базы
      Дано шаг postgresql с конфигом K и database "<база>"
      Когда агент выполняет шаг
      Тогда restic backup получил --stdin-filename=<файл>

      Примеры:
        | база    | файл                 |
        | app     | app.dump             |
        | My_DB-2 | My_DB-2.dump         |
        | my db   | my%20db.dump         |
        | a.b     | a%2Eb.dump           |
        | ..      | %2E%2E.dump          |
        | x,y     | x%2Cy.dump           |
        | склад   | %D1%81%D0%BA%D0%BB%D0%B0%D0%B4.dump |

    @restic
    Сценарий: Успешный поток даёт снимок с одним файлом дампа
      Дано фейковый pg_dump выводит архив из 1 МиБ с сигнатурой PGDMP и завершается кодом 0
      Когда агент выполняет шаг postgresql с конфигом K
      Тогда статус шага SUCCEEDED
      И сообщение шага пустое
      И в R ровно один новый снимок, и его id равен snapshot_id результата
      И снимок содержит ровно один файл app.dump, байт в байт равный выводу pg_dump
      И repository_id результата равен RID
      И total_bytes результата равен 1048576

    @restic
    Сценарий: Снимок получает метки шага и метки плагина
      Дано pg_dump версии 18.0 и сервер PostgreSQL версии 16.4
      Когда агент выполняет шаг postgresql с конфигом K
      Тогда у снимка snapshot_id в R ровно метки sard.run=r1, sard.source=s1, postgresql.database=app, postgresql.format=custom, postgresql.pg_dump_version=18.0 и postgresql.server_version=16.4

    @unit
    Сценарий: Метка шага с ключом метки плагина проваливает шаг до restic
      Дано шаг postgresql с конфигом K и tags {postgresql.format: x}
      Когда агент выполняет шаг
      Тогда статус шага FAILED
      И сообщение называет ключ postgresql.format
      И restic не запускался

    @unit
    Сценарий: Счётчик байт в загрузке не убывает
      Дано restic выводит статусы с bytes_done 100, 400, 1024
      И интервал прогресса исполнителя не прореживает эти статусы
      Когда агент выполняет шаг postgresql с конфигом K
      Тогда прогресс UPLOADING приходит с bytes_processed 100, 400, 1024 по порядку

    @restic
    Сценарий: Дамп не пишется на диск хоста
      Дано фейковый pg_dump выводит архив из 64 МиБ
      Когда агент выполняет шаг postgresql с конфигом K
      Тогда статус шага SUCCEEDED
      И за время шага в каталоге состояния агента и во временном каталоге не появилось файла больше 1 МиБ

    @unit
    Сценарий: Предупреждения pg_dump попадают в лог шага и не проваливают его
      Дано pg_dump пишет в stderr "pg_dump: warning: there are circular foreign-key constraints" и завершается кодом 0
      Когда агент выполняет шаг postgresql с конфигом K
      Тогда статус шага SUCCEEDED
      И в логе шага строка WARN с этим текстом

    @e2e
    Сценарий: Два источника на одну базу бэкапятся одновременно
      Дано два источника postgresql на базу app в один репозиторий
      Когда администратор запускает оба одновременно
      Тогда оба шага SUCCEEDED с различными snapshot_id

  # ---------------------------------------------------------------------------
  Правило: Ошибка дампа никогда не даёт снимка, который выглядит успешным

    @restic
    Структура сценария: Сбой pg_dump проваливает шаг и не оставляет снимка
      Дано фейковый pg_dump <поведение>
      Когда агент выполняет шаг postgresql с конфигом K
      Тогда статус шага FAILED
      И сообщение называет pg_dump и <причину>
      И снимков не прибавилось
      И блокировок нет
      И процессов pg_dump и restic не осталось
      И результат без вывода

      Примеры:
        | поведение                                                                       | причину                     |
        | выводит 10 МиБ архива, пишет "pg_dump: error: connection lost" и выходит кодом 1 | connection lost             |
        | выводит 10 МиБ архива и убит сигналом KILL                                       | signal: killed              |
        | ничего не выводит и выходит кодом 0                                              | empty output                |
        | выводит "-- plain SQL" и выходит кодом 0                                         | not a custom-format archive |

    @e2e @qa
    Сценарий: Остановка сервера PostgreSQL посреди дампа проваливает шаг без снимка
      Дано база app такого размера, что дамп идёт дольше 10 секунд
      Когда администратор запускает источник postgresql на базу app и сервер PostgreSQL останавливается во время фазы UPLOADING
      Тогда шаг FAILED, сообщение называет pg_dump и потерю соединения
      И снимков не прибавилось
      И блокировок нет

    @unit
    Сценарий: Сбой репозитория во время дампа останавливает pg_dump
      Дано restic завершается ошибкой хранилища, пока pg_dump ещё пишет
      Когда агент выполняет шаг postgresql с конфигом K
      Тогда статус шага FAILED
      И сообщение называет репозиторий R и причину restic
      И процесс pg_dump остановлен
      И результат без вывода

  # ---------------------------------------------------------------------------
  Правило: Отмена и таймаут останавливают psql, pg_dump и restic

    @unit
    Сценарий: Отмена во время подготовки не запускает дамп
      Дано psql ждёт ответа сервера
      Когда сервер присылает CancelStep во время фазы PREPARING
      Тогда статус шага CANCELLED
      И процесс psql остановлен
      И дамп не запускался

    @e2e @qa
    Структура сценария: Остановленный во время загрузки шаг не оставляет снимка, процессов и сессии
      Дано база app такого размера, что дамп идёт дольше 10 секунд
      И источник postgresql на базу app<таймаут>
      Когда администратор запускает источник и <событие> во время фазы UPLOADING
      Тогда шаг <статус>
      И снимков не прибавилось
      И блокировок нет
      И процессов pg_dump и restic в контейнере агента не осталось
      И через 10 секунд в pg_stat_activity сервера нет сессии с application_name sard-agent

      Примеры:
        | таймаут          | событие                       | статус    |
        |                  | администратор отменяет прогон | CANCELLED |
        | и таймаутом 3s   | истекает таймаут шага         | TIMED_OUT |

  # ---------------------------------------------------------------------------
  Правило: Из снимка база восстанавливается стандартными средствами PostgreSQL

    @e2e
    Структура сценария: Бэкап и pg_restore в чистый PostgreSQL дают те же данные
      Дано сервер PostgreSQL <версия> с базой app, в которой таблицы с данными, последовательность, представление и большой объект
      И роль backup с pg_read_all_data и секрет агента с её паролем
      Когда администратор запускает источник postgresql на базу app
      И снимок восстанавливается restic dump файла app.dump и pg_restore в новую базу чистого PostgreSQL <версия>
      Тогда шаг бэкапа SUCCEEDED
      И для каждой таблицы число строк и md5 упорядоченного содержимого совпадают с исходной базой
      И значение последовательности и большой объект совпадают

      Примеры:
        | версия |
        | 14     |
        | 18     |

    @e2e
    Сценарий: Исключённые схема и таблица не восстанавливаются
      Дано база app со схемой audit и таблицей public.log_x
      Когда администратор запускает источник postgresql на базу app с exclude_schemas ["audit"] и exclude_tables ["public.log_x"]
      И снимок восстанавливается pg_restore в новую базу
      Тогда в новой базе нет схемы audit и таблицы public.log_x
      И остальные таблицы совпадают с исходной базой

    @e2e
    Сценарий: Пустая база сохраняется и восстанавливается
      Дано база empty без пользовательских объектов
      Когда администратор запускает источник postgresql на базу empty
      Тогда шаг SUCCEEDED
      И pg_restore файла empty.dump в новую базу завершается кодом 0

    @e2e
    Сценарий: Шаблон исключения, которому ничего не соответствует, не ошибка
      Когда администратор запускает источник postgresql на базу app с exclude_tables ["nothing_*"]
      Тогда шаг SUCCEEDED

  # ---------------------------------------------------------------------------
  Правило: Документация говорит, как настроить роль, секрет и восстановить базу

    @doc @qa
    Сценарий: Документация плагина объясняет роль и секрет
      Когда читается docs/plugins/postgresql.md
      Тогда он показывает создание роли с LOGIN и GRANT pg_read_all_data и говорит о BYPASSRLS при RLS
      И показывает секрет в secrets agent.yaml с файлом 0600 владельца агента и перезапуском агента
      И говорит, что пароль в конфиге источника не пишется, только имя секрета в password_ref
      И называет пакеты postgresql-client и postgresql и поле pg_dump_path

    @doc @qa
    Сценарий: Документация плагина объясняет восстановление руками
      Когда читается docs/plugins/postgresql.md
      Тогда он показывает restic dump снимка в файл и pg_restore в новую базу после createdb
      И говорит, что pg_restore должен быть не старше версии из метки postgresql.pg_dump_version
      И говорит явно, что роли и прочие глобальные объекты в дамп не входят, и показывает pg_dumpall --globals-only и pg_restore --no-owner --no-acl
      И описывает правило имени файла дампа с примером my%20db.dump
