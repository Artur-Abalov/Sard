# language: ru
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# A8d. Проверка хоста агента: sard-agent repo check и sard-agent check.
#
# Часть фичи A8 «Настройка хоста агента из терминала»
# (docs/specs/agent/host-setup.feature: срезы A8a, A8b; repo-restore.feature:
# A8c). Правила 10 и 16 описания A8. Цель: оператор хоста одной командой
# узнаёт, готов ли хост к бэкапу и восстановлению, а если нет — что сломано и
# какой командой починить. Пригодно для Ansible и мониторинга.
#
# СТАТУС: ОДОБРЕНО владельцем 2026-10-14. Решения Ч1–Ч31 приняты. Вопросы
# ЧВ1–ЧВ14 закрыты: ЧВ1–ЧВ10 и ЧВ12–ЧВ14 — по рекомендации specifier (после
# «→»), ЧВ11 — иначе, решением владельца (Ч31). Пометки «# ЧВ<номер>» над
# сценариями оставлены как ссылки на решение.
#
# НАРЕЗКА (одобрено, ЧВ13). Над каждым правилом — комментарий «# A8d-1» …
# «# A8d-4». A8d-1: repo check, repo unlock, запись объявленных id службой
# (Ч4). A8d-2: sard-agent check без сервера (всё, кроме server.* и
# clock.skew), --json, --offline, права. A8d-3: server.*, clock.skew — нужен
# новый вызов proto и сервера (Ч15, ЧВ6); сценарии сервера пишутся после
# ответа на ЧВ6 в docs/specs/server. A8d-4: подсказка в ошибках шагов (правило
# 12) и документация оператора (правило 11). Каждая часть проходит
# ./scripts/gate.sh agent (A8d-3 — ещё proto и server).
#
# Опирается на (готово): A1 (secrets.CheckAll, права секретных файлов), A2b
# (классы и номера кодов выхода, язык сообщений), A5b (проверка restic, min
# и поставляемая версия — agent/internal/restic/restic-version), A8a и A8b
# (host-setup.feature: Р1–Р50, П1–П30 — agent.d, DUPLICATE_NAME, права
# запуска Р5 и П1, restic от имени пользователя службы Р6, --connect-timeout
# Р33, классы отказов хранилища Р34, Scrubber и RedactURL), A8c
# (repo-restore.feature: --no-lock Н26, разрешённый список env_file Р72),
# FXa (журнал принятых команд <executor.state_dir>/journal, ADR 0044), tlsid
# (ключ соответствует сертификату), ADR 0008 (сервер — не держатель ключей),
# ADR 0050 (права запуска).
#
# НЕ ВЛИТО и потому условно (поведение — заглушка, зафиксированная
# сценарием): C1 (продление сертификата), V1 (роль агента в сертификате), V0
# (проверка репозитория по учётке только для чтения), R2 (места
# восстановления). Когда срез влит, его проверка заменяет строку «пропущено»
# отдельной поправкой к этому файлу.
#
# Вне фичи: --fix; отправка итога на сервер или в консоль (ЧВ3 → нет);
# проверка восстановления данных (трек V); продление сертификата (C1);
# «последний снимок» в repo show (Н34 A8c перенёс его в A8d, но описания A8d
# в нём нет — ЧВ12).
#
# ---------------------------------------------------------------------------
# Решения для A8d (одобрены владельцем; Ч31 — решение владельца по ЧВ11)
#
#   Ч1. Строка проверки. Обе команды печатают в stdout по строке на проверку:
#       СТАТУС, ИДЕНТИФИКАТОР, ПРЕДМЕТ, СООБЩЕНИЕ. СТАТУС — OK, WARN, ERROR
#       или SKIP. ПРЕДМЕТ — имя репозитория, секрета или плагина, у проверок
#       хоста — «-». У каждой строки не OK следующая строка — подсказка с
#       префиксом «hint:»: готовая команда или ссылка на docs/operator
#       (Ч25). У SKIP подсказка — почему пропущено и как проверить. Последняя
#       строка — «summary: <n> ok, <n> warnings, <n> errors, <n> skipped».
#       Порядок строк фиксирован (Ч10); сообщения на английском (В2 A2b).
#   Ч2. Статус «пропущено» (SKIP, в JSON «skipped», ЧВ8). Проверка, которую
#       нельзя выполнить: --offline, не влитый срез (C1, V1, V0, R2), нет
#       systemd, нет SELinux-утилит, запуск от пользователя службы (Ч20),
#       провал проверки, от которой она зависит; а также проверка, которая
#       к этому хосту не применима, — единственный такой случай — программы
#       PostgreSQL не установлены (Ч31). SKIP на код выхода не влияет.
#   Ч3. repo check <имя> (правило 1) — шаги по порядку, первый ERROR
#       останавливает репозиторий, остальные шаги этого репозитория — SKIP с
#       сообщением «not checked: <идентификатор> failed»:
#         repository.storage      хранилище доступно: имя разрешается, порт
#                                 открыт, TLS хранилища проходит;
#         repository.credentials  учётные данные приняты: env_file читается
#                                 (Р30, Р72) и ключ S3, ключ SSH, ключ хоста
#                                 SFTP приняты;
#         repository.initialized  репозиторий есть (restic код 10 — нет);
#         repository.password     файл пароля есть, проходит A1 и открывает
#                                 репозиторий;
#         repository.id           id репозитория совпадает с объявленным
#                                 службой серверу (Ч4);
#         repository.locks        блокировки restic: число, хост, возраст (Ч5);
#         repository.integrity    только с --read-data (Ч7).
#       Классы отказов первых четырёх шагов — таблица Р34 (BACKEND_UNAVAILABLE,
#       S3_KEY_REJECTED, STORAGE_ACCESS_DENIED, BUCKET_NOT_FOUND,
#       SSH_KEY_NOT_AUTHORIZED, HOST_KEY_MISMATCH, WRONG_PASSWORD,
#       BACKEND_REFUSED) плюс REPOSITORY_NOT_INITIALIZED, PASSWORD_FILE_MISSING,
#       SECRET_FILE_REJECTED, ENV_FILE_MISSING; сообщение называет причину.
#       Шаги 1–4 — один вызов restic cat config, классифицированный по Р34;
#       шаг по классу отказа получает ERROR, предыдущие — OK.
#   Ч4. Объявленный id (repository.id). Служба при каждом Register
#       записывает объявленные имена и id репозиториев в файл
#       <executor.state_dir>/announced-repositories.json (0600, атомарно, Р4;
#       имя файла выбирает coder) — ЧВ5. check сравнивает текущий id (из
#       restic cat config) с записанным:
#         совпадает                  — OK;
#         отличается                 — ERROR: в хранилище другой репозиторий,
#                                      чем знает сервер; подсказка —
#                                      перезапустить службу и проверить
#                                      адрес (sudo systemctl restart
#                                      sard-agent);
#         записан пустой id          — WARN: служба не смогла прочитать id при
#                                      старте; подсказка — перезапуск;
#         записи нет (служба не
#         стартовала, имя добавлено
#         после старта)              — WARN: «not announced to the server yet»,
#                                      подсказка — перезапуск;
#         файла нет вовсе            — SKIP: служба ещё не регистрировалась.
#   Ч5. Блокировки (правило 2). Чтение — restic list locks --no-lock и cat
#       lock <id> --no-lock; у блокировки — хост, пользователь, pid, время,
#       exclusive. «Этот хост» — имя хоста, как его берёт restic (os.Hostname).
#       Возраст — часы агента минус время блокировки. Порог зависшей — 24 ч
#       (ЧВ1).
#         нет блокировок                         — OK «no locks»;
#         все моложе порога                      — OK, сообщение называет
#                                                  число, хосты и возраст
#                                                  (идёт бэкап или проверка);
#         своя старше порога, процесс pid жив    — WARN «held by running
#                                                  process <pid>», подсказка —
#                                                  проверить процесс;
#         своя старше порога, процесса нет       — WARN, подсказка —
#                                                  «sudo sard-agent repo unlock
#                                                  <имя> --remove-stale»;
#         чужая старше порога                    — WARN, сообщение называет
#                                                  хост и возраст, подсказка —
#                                                  снять на том хосте той же
#                                                  командой; агент её не
#                                                  снимает.
#       Строка называет каждую блокировку старше порога (хост, возраст в
#       часах, exclusive); моложе — числом.
#   Ч6. repo unlock <имя> --remove-stale (правило 2, ЧВ2). Отдельная
#       изменяющая команда, только от root (Р5: пользователь службы и
#       посторонний — PRIVILEGES_REQUIRED). Без --remove-stale — ошибка
#       использования, называющая флаг, ничего не снято. Перед снятием stderr
#       предупреждает: снятая блокировка живого процесса повредит его работу.
#       Снимает только блокировки этого хоста старше порога, процесс pid
#       которых не жив. Механизм — restic unlock (без --remove-all) от имени
#       пользователя службы; restic unlock снимает все блокировки, которые
#       сам считает зависшими (старше 30 мин на любом хосте), поэтому
#       непосредственно перед ним список перечитывается, и если restic снял бы
#       ещё хоть одну блокировку, не подходящую под правило (чужая; своя
#       моложе порога), — отказ STALE_LOCKS_FOREIGN (класс «использование»,
#       новая причина), сообщение называет каждую такую блокировку, restic
#       unlock не вызывается. Подходящих нет — код 0 «no stale locks of this
#       host», restic unlock не вызывается. Успех — код 0, число снятых,
#       строка аудита «repository <имя> stale locks removed: <n>» (Р9).
#       Остаётся гонка: блокировка, ставшая зависшей между перечитыванием и
#       restic unlock, будет снята — записать в справку.
#   Ч7. --read-data <процент> (правило 3). Только у repo check. Целое 1–100,
#       иначе ошибка использования, называющая флаг (ЧВ9). Добавляет шаг
#       repository.integrity: restic check --read-data-subset=<процент>% от
#       имени пользователя службы. Без флага restic check не запускается
#       вовсе, шага нет в выводе: проверка быстрая и не тянет данные
#       хранилища (ЧВ10). Ошибки целостности — ERROR, сообщение — число
#       ошибок restic (очищенное Scrubber), подсказка — docs/operator.
#       restic check берёт блокировку в хранилище; учётка без права записи
#       (отказ создать блокировку — access denied на locks/) — ERROR с
#       сообщением-заглушкой V0: «restic check needs to create a lock in the
#       repository and these credentials cannot write; checking with
#       read-only credentials is not supported yet», подсказка —
#       docs/operator; сырой текст restic в сообщении не повторяется (только
#       очищенная причина в отдельной строке «restic said:»). Когда V0 влит,
#       текст и поведение уточняются поправкой.
#       Репозиторий занят (решение владельца 2026-10-15). restic check не может
#       взять свою блокировку, пока идёт бэкап или восстановление (restic
#       код 11, «repository is locked by another process»): строка
#       repository.integrity — WARN, а не ERROR; сообщение объясняет причину и
#       просит повторить, когда бэкап закончится, подсказка — готовая команда
#       «sudo sard-agent repo check <имя> --read-data <процент>». Код выхода 8.
#       Репозиторий при этом не неисправен; ERROR остаётся для найденных ошибок
#       целостности и для отказов хранилища.
#   Ч8. repo check без имени (правило 4) — все репозитории в порядке
#       конфига (основной, затем фрагменты), по очереди. Нет ни одного —
#       одна строка OK «no repositories configured» с пояснением, как
#       подключить («sudo sard-agent repo add <name> <address>»), код 0
#       (решение владельца 2026-10-15: это не предупреждение — у хоста могут
#       быть только источники, которым репозиторий не нужен; строка OK не
#       имеет hint, пояснение — часть сообщения). То же у check: одна строка
#       repository.storage с предметом «-», OK, при любом --offline. Неизвестное имя —
#       REPOSITORY_UNKNOWN (использование, код 2) с именами подключённых, как
#       repo snapshots.
#   Ч9. Коды выхода (правило 6, ЧВ4). Обе команды:
#         0 — ни одной WARN и ERROR (SKIP допустимы);
#         8 — есть WARN, нет ERROR (новый код, класс «предупреждения»);
#         1 — есть хотя бы одна ERROR (класс «ошибка агента» A2b, здесь —
#             «хост не готов»);
#         2 — команда не выполнена: флаги, PRIVILEGES_REQUIRED,
#             REPOSITORY_UNKNOWN, SERVICE_USER_UNKNOWN у repo check;
#         6 — только SIGINT или SIGTERM команде (INTERRUPTED).
#       Сетевой отказ проверки — строка ERROR, а не код 6: итог хоста
#       определяется строками. repo unlock — коды A8a (0, 2, 6, 1).
#   Ч10. Состав и порядок sard-agent check (правило 5). Идентификаторы
#       стабильны между версиями (правило 7); новая проверка получает новый
#       идентификатор, старый не меняет смысла и не переиспользуется.
#         service.user        пользователь службы существует (Н1)
#         config.load         основной файл и agent.d читаются и разбираются
#         config.names        нет одного имени репозитория или секрета в
#                             двух местах (DUPLICATE_NAME, оба файла)
#         config.references   все файлы, на которые ссылается конфиг
#                             (tls.*, password_file, env_file, secrets.*,
#                             scripts.*, restic.path), существуют
#         files.permissions   права и владельцы (Ч11)
#         restic.binary       restic найден и его версия (Ч12)
#         tls.identity        ключ соответствует сертификату (tlsid)
#         tls.chain           сертификат выдан CA из tls.ca_file (Ч13)
#         tls.expiry          срок (Ч13)
#         tls.role            роль агента — SKIP до V1
#         server.reachable    адрес server.address разрешается, порт открыт
#         server.tls          TLS-рукопожатие с сертификатом агента
#         server.accepted     сервер принимает агента (Ч15)
#         clock.skew          расхождение часов с сервером (Ч16)
#         repository.storage … repository.locks — по репозиторию (Ч3), без
#                             --read-data
#         secret.readable     по секрету (Ч17)
#         plugin.declared     одна строка: плагины, которые агент объявит
#                             серверу, с версиями
#         plugin.programs     по плагину (Ч18)
#         restore.locations   SKIP до R2
#         selinux.labels      метки файлов агента (Ч19)
#         selinux.denials     недавние отказы для службы (Ч19)
#         service.running     служба запущена, с какого времени, перезапуски
#                             (Ч21)
#         service.steps       шаги в работе по журналу FXa (Ч21)
#       Провал config.load или config.names делает SKIP всё, что берёт
#       данные из конфига (Ч2); провал restic.binary — все repository.*;
#       провал service.user под root — все repository.* («restic runs as the
#       service user»); провал server.reachable — server.tls и дальше;
#       провал tls.identity — server.*.
#   Ч11. files.permissions — одна строка, статус худший; сообщение называет
#       каждый файл с нарушением и что не так, подсказка — команды chown и
#       chmod для каждого. Правила:
#         ERROR  секретные файлы A1 (tls.key_file, password_file, env_file,
#                secrets.*): владелец не пользователь службы или есть биты
#                группы и прочих — служба откажет в старте;
#         ERROR  каталоги secrets и tls: владелец не пользователь службы или
#                есть права группы и прочих;
#         WARN   основной конфиг, каталог agent.d или фрагмент доступен на
#                запись не только root (служба или посторонний могут менять
#                настройку, ADR 0050).
#       Конфиг, который служба не может прочитать, — не эта строка, а ERROR
#       config.load (он читается от имени службы, Ч20). Прочие отличия от Р3
#       (например фрагмент 0644) — не нарушение.
#   Ч12. restic.binary. Путь — restic.path или поставляемый
#       (/usr/libexec/sard/restic). Нет — ERROR RESTIC_NOT_FOUND; не
#       запускается — ERROR RESTIC_UNUSABLE; версия ниже min_version — ERROR
#       RESTIC_TOO_OLD; не ниже min, но не равна поставляемой (version из
#       restic-version) — WARN, подсказка — переустановить пакет агента или
#       убрать restic.path; равна — OK с версией.
#   Ч13. Сертификат. Нет tls.* в конфиге или файлов — tls.identity ERROR,
#       подсказка «sudo sard-agent enroll --server <server.address> --token
#       …». tls.chain: сертификат проверяется по tls.ca_file — бандлу CA,
#       записанному enroll после сверки отпечатка из токена (ЧВ7: отдельного
#       записанного отпечатка нет); не проходит — ERROR. tls.expiry: истёк
#       или ещё не действует — ERROR (подсказка — повторная регистрация,
#       docs/operator); до истечения < 30 дней — WARN «expires in <n> days;
#       automatic renewal is not available yet (C1)», подсказка — повторная
#       регистрация; иначе OK с датой. После C1 условие WARN — «< 30 дней и
#       продление не удалось» (поправка). tls.role — SKIP «agent role is
#       checked once V1 is available».
#       Поправка C1 фаза 3 (владелец 2026-10-19, передано координатором;
#       заменяет обе фразы о WARN выше): строки tls.* описывают пару, которую
#       выбрал бы старт (certpair.Select, П21), без уборки файлов; WARN —
#       остаток не больше 30/365 собственного срока сертификата, сообщение
#       «the certificate expires in <n> days (<дата>) and has not been renewed:
#       see renewal_last_error in sudo sard-agent status», подсказка «sudo
#       sard-agent status». Полностью — П45, П46
#       docs/specs/agent/agent-certificate-renewal.feature.
#   Ч14. Сервер (A8d-3). server.reachable — DNS и TCP к server.address;
#       server.tls — рукопожатие mTLS: сервер проверяется по tls.ca_file и
#       имени, клиент предъявляет сертификат агента. Отказы рукопожатия
#       различаются сообщением: сертификат сервера не от этого CA; имя не
#       совпадает; сервер не принял сертификат клиента. Каждое обращение —
#       --connect-timeout.
#   Ч15. server.accepted (ЧВ6). Новый унарный вызов AgentService, только
#       чтение (рабочее имя Probe): без побочных действий на сервере —
#       не меняет регистрацию, не открывает сессию, не трогает присутствие
#       агента; отвечает временем сервера. Проходит через проверку агента
#       сервера (ADR 0009): CERT_REVOKED, AGENT_REVOKED, CERT_UNKNOWN,
#       CERT_EXPIRED, а также CERT_IDENTITY_MISMATCH и CERT_MISSING (решение
#       владельца 2026-10-15, ПР) — ERROR с причиной, подсказка — повторная
#       регистрация. Отказ — статус UNAUTHENTICATED, а не поле ответа. Агент
#       сообщает только причину, если она состоит из заглавных букв, цифр и _ и
#       из домена sard.dev; текст статуса сервера в вывод не попадает. Отзыв
#       агента в консоли даёт CERT_REVOKED (сертификаты отзываются вместе с ним).
#       Недоступная база сервера — UNAVAILABLE с INTERNAL_RETRYABLE: ERROR
#       «the server could not tell whether it accepts this agent».
#       Сервер без этого вызова (UNIMPLEMENTED) — WARN «the server is older
#       than this agent; acceptance and clock were not checked», clock.skew —
#       SKIP. check никогда не вызывает Register и Connect: Register
#       перезаписал бы объявление службы, а Hello без шагов сделал бы шаги
#       службы lost (ADR 0038).
#   Ч16. clock.skew. |время сервера − часы агента| с поправкой на половину
#       времени ответа; > 1 мин — WARN (ЧВ1) с расхождением в секундах,
#       подсказка — включить синхронизацию времени (timedatectl
#       set-ntp true, docs/operator); иначе OK.
#   Ч17. secret.readable — по каждому секрету, на который ссылается конфиг
#       (secrets.*), и строки нет для password_file и env_file (их проверяет
#       repository.password и repository.credentials). Файл есть, обычный,
#       не пуст, читается (под root — secrets.ReadOwned, без перехода по
#       ссылке) — OK; иначе ERROR с путём и причиной. Значение никогда не
#       выводится и не сравнивается ни с чем.
#   Ч18. plugin.programs — по плагину, который объявляет агент. files —
#       программ не требует, OK. postgresql — pg_dump ищется в PATH агента
#       (как плагин, F1 ПГ8), psql и pg_dumpall — рядом с ним, pg_restore —
#       рядом (для ручного восстановления, A8c Р62); сообщение называет путь
#       и версию каждого (pg_dump --version). Нет pg_dump в PATH — SKIP
#       по Ч31 (а не WARN: решение владельца по ЧВ11). pg_dump есть, а psql
#       или pg_dumpall рядом нет — ERROR. pg_restore рядом нет — WARN. Версии
#       разных старших номеров рядом — WARN. (Последние три случая —
#       клиент PostgreSQL установлен не полностью; Ч31 их не меняет.)
#   Ч19. SELinux. Нет SELinux или он не enforcing (getenforce) — оба OK с
#       сообщением «SELinux is not enforcing». Enforcing:
#         selinux.labels  метка каждого файла и каталога агента (основной
#                         конфиг, agent.d, secrets, tls, restic, каталоги
#                         локальных репозиториев, restic.cache_dir,
#                         executor.state_dir) сравнивается с меткой по
#                         политике (matchpathcon); отличается — WARN со
#                         списком, подсказка «sudo restorecon -Rv <путь>»;
#         selinux.denials отказы AVC за последние 24 ч (ЧВ1) с comm sard-agent
#                         или restic (ausearch); есть — WARN с числом и
#                         первыми 5, подсказка — ausearch и docs/operator.
#                         Нет ausearch — SKIP с подсказкой пакета audit.
#   Ч20. Права запуска (правило 9). root — всё. Пользователь службы — всё,
#       что ему видно: selinux.denials — SKIP «needs root» (журнал аудита);
#       прочие проверки, упёршиеся в EACCES, — SKIP «needs root» с путём; в
#       конце строка summary и строка «some checks need root: run sudo
#       sard-agent check». Посторонний — PRIVILEGES_REQUIRED (класс
#       «использование», код 2), «sudo sard-agent check», ничего не
#       проверено. Проверка — сразу после разбора флагов (Р5, П1). Под root
#       restic и чтение файлов, к которым служба должна иметь доступ
#       (config.load, secret.readable), идут от имени пользователя службы:
#       проверяется то, что увидит служба. То же правило у repo check.
#   Ч21. Служба. Нет systemd (Р8: нет /run/systemd/system или systemctl) —
#       service.running SKIP. systemctl show sard-agent.service:
#         active                 — OK «running since <время UTC> (<возраст>)»;
#         active и NRestarts > 0 — WARN «restarted <n> times after failures»,
#                                  подсказка journalctl -u sard-agent;
#         inactive, failed,
#         activating (auto-restart) — ERROR, подсказка «sudo systemctl
#                                  start sard-agent» и journalctl.
#       service.steps — число записей журнала FXa (файлы *.json, не
#       временные): OK «<n> steps in progress» (0 — «no steps in progress»);
#       журнал не читается — WARN с причиной. Только systemctl show и
#       is-active, ничего не запускается и не перезапускается.
#   Ч22. --offline (правило 8): ни одного сетевого соединения и ни одного
#       запуска restic. server.*, clock.skew, repository.* — SKIP «offline».
#       restic.binary выполняется (restic version — не сеть).
#   Ч23. Только чтение (правило 10). check и repo check не создают, не
#       меняют и не удаляют файлов хоста (конфиг, agent.d, secrets, tls,
#       restic.cache_dir, executor.state_dir, ~/.ssh, drop-in), не берут
#       блокировку конфига, не вызывают systemctl кроме show и is-active, не
#       пишут строк аудита. restic вызывается с --no-lock и --no-cache, кроме
#       restic check с --read-data (блокировка в хранилище, не на хосте; кэш
#       restic check — свой временный). Исключение — repo unlock (Ч6).
#   Ч24. Секреты нигде (нефункциональное). Значения секретов, пароли
#       репозиториев, ключи S3, пароли в адресах не появляются в stdout,
#       stderr, --json, журнале агента и аргументах процессов; адрес —
#       RedactURL, текст restic и ssh — Scrubber.
#   Ч25. Подсказки (ЧВ14). Готовая команда — целиком, с sudo и именами
#       хоста; ссылка — https://github.com/Artur-Abalov/sard/blob/main/docs/operator/09-troubleshooting.md#<идентификатор
#       без точек> (как ссылка postinstall.sh); раздел с якорем на каждый
#       идентификатор проверки — часть A8d-4.
#   Ч26. --json (правило 7) — JSON Lines: по одному объекту на строку
#       stdout, в порядке Ч10, поля ровно: check, subject (строка или null),
#       status (ok, warning, error, skipped), message, hint (строка или null
#       у ok). Статус однозначно задаёт вклад строки в код выхода (Ч9):
#       warning — 8, error — 1, ok и skipped — ничего; исключений нет, в том
#       числе у Ч31. Строк summary и «needs root» нет: итог — код выхода. Ошибки
#       самой команды (флаги, PRIVILEGES_REQUIRED) — текстом в stderr, stdout
#       пуст (Р21).
#   Ч27. Время (нефункциональное). Каждое сетевое обращение ограничено
#       --connect-timeout (по умолчанию 10s, ЧВ9); истёк — ERROR этой
#       проверки (BACKEND_UNAVAILABLE для хранилища, «did not answer within»
#       для сервера), процесс restic получает SIGTERM группе и SIGKILL через
#       10 с (Р33). --timeout (по умолчанию 2m без --read-data, без предела с
#       ним) объемлет весь repo check одного репозитория; истёк — ERROR
#       TIMEOUT текущего шага. Проверки не ждут друг друга дольше своих
#       таймаутов.
#   Ч28. Подсказка в ошибках шагов (правило 12, A8d-4). Сообщение FAILED
#       шага бэкапа и восстановления, которое агент отдаёт серверу, для
#       причин BACKEND_UNAVAILABLE, STORAGE_ACCESS_DENIED, S3_KEY_REJECTED,
#       SSH_KEY_NOT_AUTHORIZED, WRONG_PASSWORD, PASSWORD_FILE_MISSING и
#       «программа плагина не найдена» (pg_dump, psql, pg_dumpall) кончается
#       строкой «run `sudo sard-agent check` on the host». Сервер показывает
#       текст как есть — изменений server и web нет (ЧВ3).
#   Ч29. Справка. check --help и repo check --help: флаги (--json,
#       --offline, --connect-timeout, --read-data, --timeout), нужен ли sudo,
#       что ничего не меняется, коды 0, 8, 1, 2, ссылка на список
#       идентификаторов. repo unlock --help — что снимается, что нет, гонка
#       Ч6. «sard-agent repo» без подкоманды перечисляет check и unlock;
#       «sard-agent» без команды — check.
#   Ч30. Документация оператора (правило 11, A8d-4, проверяется QA):
#       docs/operations/agent-install.md, agent-host-setup.md (после repo
#       add), docs/operator/02-install.md, 05-agents.md, 05a-storage.md —
#       «выполните sudo sard-agent check»; docs/operator/09-troubleshooting.md
#       — раздел на каждый идентификатор Ч10 (что значит, как починить), а
#       ручные процедуры диагностики агента (раздел «Агент не подключается»)
#       заменяются ссылкой на check. Итог postinstall.sh после первой
#       установки называет sudo sard-agent check последним шагом.
#   Ч31. Хост без программ PostgreSQL (решение владельца по ЧВ11). Нет
#       pg_dump в PATH агента (поиск — как у плагина, от имени службы, Ч20:
#       каталог PATH, который служба не может прочитать, — то же «нет») —
#       строка plugin.programs с предметом postgresql — SKIP, не WARN:
#         сообщение  начинается с неизменного текста «PostgreSQL client tools
#                    are not installed» и продолжается «PostgreSQL sources on
#                    this host will fail until pg_dump is in the PATH or
#                    pg_dump_path is set in the source»;
#         подсказка  пакет клиента (postgresql-client, postgresql) и
#                    docs/plugins/postgresql.md;
#         итог       считается в summary как skipped; на код выхода не
#                    влияет — хост только с источниками files без программ
#                    PostgreSQL получает 0.
#       В --json — status «skipped», message и hint как выше; новых полей и
#       статусов нет. Сочетание check «plugin.programs», subject
#       «postgresql», status «skipped» значит ровно это и ничего больше:
#       других причин SKIP у plugin.programs нет (поиск в PATH и --version
#       не требуют root и сети, поэтому ни Ч20 «needs root», ни Ч22
#       --offline эту строку не пропускают). Скрипт мониторинга, которому
#       нужна эта ситуация, проверяет эту тройку.
#       Почему SKIP, а не новый статус и не WARN с отдельным признаком:
#         - агент не знает, есть ли у хоста источники PostgreSQL (источники
#           определены на сервере), поэтому отсутствие клиента — не
#           неисправность хоста, а неприменимая к нему проверка; так и
#           определён SKIP (Ч2);
#         - SKIP уже даёт всё, что требует владелец: видимая строка с
#           сообщением и подсказкой, без влияния на код выхода;
#         - сохраняются инварианты для мониторинга: «есть warning ⇔ код 8»,
#           «есть error ⇔ код 1», четыре статуса и пять полей JSON (Ч26) —
#           WARN без влияния на код или пятый статус их бы сломали.
#       Ошибка шага плагина «pg_dump не найден» по-прежнему кончается
#       подсказкой check (Ч28): там источник PostgreSQL уже есть.
#   Ч32. repository.id в check --offline (решение владельца 2026-10-15).
#       Текущий id приходит из хранилища (restic cat config), а --offline не
#       обращается к хранилищу (Ч22): сравнивать не с чем. Поэтому offline
#       строка repository.id каждого репозитория — SKIP «offline» с подсказкой
#       «run `sudo sard-agent check` without --offline to make it», как и
#       остальные пять строк репозитория, и это при любом состоянии записи
#       объявленных id (Ч4): она есть, пуста, не содержит имени или файла нет.
#       Offline запись не читается вовсе: файл объявленных id пишет только
#       служба при Register, check и repo check его никогда не создают, не
#       меняют и не удаляют, а наличие или отсутствие файла не меняет ни одну
#       строку и ни код выхода offline. Онлайн порядок решения прежний (Ч4) и
#       такой: отказ предыдущего шага — SKIP «not checked: <шаг> failed»;
#       иначе нет файла — SKIP «service has not registered yet»; иначе
#       сравнение (OK, ERROR, WARN). Расхождение id, не пустое и не пропущенное,
#       поэтому видно только онлайн.
#   Ч33. (Выполнено в A8d-3: строки сервера сделаны, см. Ч14-Ч16.) Нарезка:
#       check до среза A8d-3. Строки server.reachable, server.tls,
#       server.accepted и clock.skew занимают своё место и идентификаторы
#       (Ч10), но проверка сервера ещё не сделана: онлайн каждая — SKIP «this
#       version of the agent does not check the server yet» с подсказкой
#       смотреть журнал службы; offline — SKIP «offline» (Ч22); после отказа
#       config.load, config.names или tls.identity — SKIP «not checked: <шаг>
#       failed». Код выхода они не меняют. Сценарии A8d-2, которые считают эти
#       строки OK (здоровый хост, сводка), верны с A8d-3; до него в сводке на
#       четыре skipped больше и на четыре ok меньше. Идентификаторы и порядок
#       для JSON уже окончательные.
#
# ---------------------------------------------------------------------------
# Вопросы владельцу по A8d — все закрыты 2026-10-14 (рекомендация specifier —
# после «→»; принята для всех, кроме ЧВ11)
#
#   ЧВ1. Пороги. → зависшая блокировка 24 ч; расхождение часов 1 мин;
#       сертификат 30 дней (из описания); отказы SELinux — за 24 ч. Все —
#       константы агента, без флагов.
#   ЧВ2. Снятие блокировки. → отдельная команда sudo sard-agent repo unlock
#       <имя> --remove-stale (Ч6), только свои блокировки старше порога с
#       мёртвым процессом; отказ, если restic unlock задел бы чужую. Вариант:
#       команды нет, подсказка «restic unlock» (тогда L1 и оператор снимают
#       сами, чужие тоже).
#   ЧВ3. Итог check в heartbeat. → нет, вне фичи.
#   ЧВ4. Коды выхода. → 0 / 8 (только предупреждения, новый код) / 1 (есть
#       ошибки) / 2 (команда не выполнена), Ч9. Вариант: коды Nagios (0, 1,
#       2), но 1 и 2 в A2b уже значат «ошибка агента» и «использование».
#   ЧВ5. С чем сравнивать repository_id. → служба записывает объявленное в
#       executor.state_dir (Ч4): работает с --offline и без изменений
#       сервера. Вариант: сервер отдаёт известные ему id в ответе Probe
#       (Ч15) — правда сервера, но только онлайн и правка server.
#   ЧВ6. Как узнать «сервер принимает агента» и время сервера. Сейчас
#       способа без побочных действий нет: Register перезаписывает
#       объявление, Connect с Hello теряет шаги, время сервер не отдаёт. →
#       новый вызов AgentService.Probe (proto — добавление, не ломает buf
#       breaking; сервер — только проверка агента и время), срез A8d-3 с
#       отдельной спецификацией docs/specs/server. Вариант: в A8d только
#       server.reachable и server.tls, accepted и clock — SKIP до отдельной
#       задачи.
#   ЧВ7. «CA, отпечаток которого записан при регистрации». Отпечаток сейчас
#       нигде не хранится, enroll записывает сам бандл tls.ca_file после
#       сверки отпечатка из токена. → проверять цепочку по tls.ca_file (Ч13),
#       без нового файла. Вариант: enroll начинает записывать отпечаток, check
#       сверяет и бандл с ним (ловит подмену tls.ca_file).
#   ЧВ8. Статус SKIP в выводе и JSON. Описание называет три статуса; условные
#       проверки (C1, V1, V0, R2, --offline, пользователь службы) требуют
#       четвёртого. → SKIP/«skipped», на код не влияет.
#   ЧВ9. Флаги. → --read-data — целое 1–100 (доля в процентах);
#       --connect-timeout по умолчанию 10s у check и repo check (у прочих
#       команд — 30s: «полная проверка — секунды»).
#   ЧВ10. Что делает repo check без --read-data. → только шаги Ч3 (cat
#       config, list locks), restic check не запускается: «без трафика
#       хранилища». Вариант: restic check без чтения данных (структура и
#       индексы) — дольше и тянет индексы.
#   ЧВ11. Нет pg_dump на хосте без PostgreSQL. Рекомендация specifier была
#       WARN (хост только с файлами всегда давал бы код 8) — ОТКЛОНЕНА.
#       Решение владельца: оператор предупреждён строкой с сообщением и
#       подсказкой, но это ничего не блокирует и не даёт ненулевого кода;
#       хост только с файлами — код 0. Моделирование — SKIP «not
#       applicable», Ч31.
#   ЧВ12. «Последний снимок» в repo show (Н34 A8c). → не в A8d (в описании
#       A8d его нет); отдельная задача.
#   ЧВ13. Нарезка A8d-1 … A8d-4. → да, как в шапке.
#   ЧВ14. Ссылки в подсказках — на main или на версию агента. → на main
#       (как postinstall.sh); на версию — когда будут теги релизов
#       (OQ-145).
#
# ---------------------------------------------------------------------------
# Классы кодов выхода A8d (номера A2b; 8 — новый, ЧВ4)
#   | 0 | в порядке        | ни одной WARN и ERROR; repo unlock — снято или нечего  |
#   | 8 | предупреждения   | есть WARN, нет ERROR                                   |
#   | 1 | есть ошибки      | есть ERROR                                             |
#   | 2 | использование    | флаги, PRIVILEGES_REQUIRED, REPOSITORY_UNKNOWN,        |
#   |   |                  | SERVICE_USER_UNKNOWN (repo check, repo unlock),        |
#   |   |                  | STALE_LOCKS_FOREIGN (repo unlock)                      |
#   | 6 | временная        | INTERRUPTED; repo unlock — BACKEND_UNAVAILABLE, TIMEOUT|
#   Коды 3, 4, 5 и 7 эти команды не возвращают.
#
# Соглашения сценариев A8d (вдобавок к host-setup.feature)
#   - Конфиг C, каталог D, пользователь службы U (uid 990), оператор alice,
#     «от root», «от пользователя службы», «от постороннего», «значения не
#     раскрыты», «код выхода класса X» — host-setup.feature. По умолчанию —
#     от root.
#   - «Здоровый хост»: в C репозиторий base (локальный D/base,
#     инициализирован, id REPO-A), секрет pg (D/secrets/pg), tls.* выданы
#     фейковым CA, сертификат действует ещё 200 дней; файлы и каталоги с
#     правами A1 и Р3; restic — фейк версии 0.19.1, на cat config отдаёт id
#     REPO-A, на list locks — пусто; служба объявила base с id REPO-A (Ч4);
#     systemd есть, служба active с NRestarts=0, журнал S пуст; SELinux не
#     enforcing; в PATH агента есть pg_dump, psql, pg_dumpall, pg_restore
#     версии 16.4; фейковый сервер (@fake) принимает агента и отдаёт время,
#     равное часам агента. Каждый сценарий начинается со здорового хоста.
#   - Часы агента — 2026-10-14T12:00:00Z, имя хоста — db1.
#   - «Строка X» — строка вывода с идентификатором X (и предметом, если
#     назван); «строка X — СТАТУС» — её статус.
#   - «Подсказка содержит Y» — строка «hint:» после строки проверки содержит Y.
#   - «Хост не изменён» — как в host-setup.feature, плюс executor.state_dir
#     и ~/.ssh пользователя службы; systemctl получал только show и
#     is-active.
#   - Блокировка «L(хост, возраст, pid)» — restic list locks отдаёт её id,
#     cat lock — JSON с hostname, time = часы минус возраст, pid;
#     «процесс жив» — фейк проверки pid отвечает «есть».
#   - Фейковый сервер (@fake) — TLS-слушатель с фейковым CA, отвечает на
#     Probe и записывает каждый вызов; Register и Connect записываются
#     отдельно.
#   - В примерах с причинами S3_KEY_REJECTED, STORAGE_ACCESS_DENIED и
#     BUCKET_NOT_FOUND репозиторий base подключён адресом s3 с env_file; с
#     SSH_KEY_NOT_AUTHORIZED и HOST_KEY_MISMATCH — адресом sftp (Р34).
#   - @start — старт агента с фейковым сервером, как в host-setup.feature.

Функция: Проверка хоста агента командами sard-agent repo check и sard-agent check

  Оператор хоста одной командой узнаёт, готов ли хост к бэкапу и
  восстановлению: по строке на проверку, со статусом и, если что-то не так,
  с готовой командой или ссылкой на документацию. Проверка ничего не меняет
  на хосте, работает в Ansible и мониторинге по коду выхода и JSON и никогда
  не выводит секреты.

  # ===========================================================================
  # A8d-1
  Правило: repo check проверяет репозиторий по шагам и останавливается на первой ошибке

    @local
    Сценарий: Здоровый репозиторий проходит все шаги
      Когда оператор выполняет repo check base
      Тогда код выхода класса «в порядке»
      И строки repository.storage, repository.credentials, repository.initialized, repository.password, repository.id и repository.locks с предметом base идут в этом порядке и каждая — OK
      И строки repository.integrity нет
      И последняя строка stdout — "summary: 6 ok, 0 warnings, 0 errors, 0 skipped"

    @local
    Структура сценария: Отказ шага называет причину и подсказку, следующие шаги пропущены
      Дано <условие>
      Когда оператор выполняет repo check base
      Тогда код выхода класса «есть ошибки»
      И строка <шаг> — ERROR и её сообщение называет <причина>
      И подсказка содержит <подсказка>
      И каждая строка после <шаг> — SKIP с сообщением "not checked: <шаг> failed"
      И каждая строка до <шаг> — OK

      Примеры:
        | условие                                                    | шаг                     | причина                    | подсказка                                  |
        | restic на cat config печатает "no such host"               | repository.storage      | BACKEND_UNAVAILABLE        | "09-troubleshooting.md#repositorystorage"  |
        | restic на cat config печатает "connection refused"         | repository.storage      | BACKEND_UNAVAILABLE        | "09-troubleshooting.md#repositorystorage"  |
        | restic на cat config печатает "x509 certificate"           | repository.storage      | BACKEND_UNAVAILABLE        | "09-troubleshooting.md#repositorystorage"  |
        | restic на cat config печатает "InvalidAccessKeyId"         | repository.credentials  | S3_KEY_REJECTED            | "09-troubleshooting.md#repositorycredentials" |
        | restic на cat config печатает "Access Denied"              | repository.credentials  | STORAGE_ACCESS_DENIED      | "09-troubleshooting.md#repositorycredentials" |
        | restic на cat config печатает "NoSuchBucket"               | repository.credentials  | BUCKET_NOT_FOUND           | "09-troubleshooting.md#repositorycredentials" |
        | ssh отвечает "Permission denied (publickey)"               | repository.credentials  | SSH_KEY_NOT_AUTHORIZED     | "authorized_keys"                          |
        | ssh отвечает "Host key verification failed"                | repository.credentials  | HOST_KEY_MISMATCH          | "09-troubleshooting.md#repositorycredentials" |
        | restic на cat config завершается кодом 10                  | repository.initialized  | REPOSITORY_NOT_INITIALIZED | "sudo sard-agent repo add base"            |
        | файла D/secrets/base.pass нет                              | repository.password     | PASSWORD_FILE_MISSING      | "sudo sard-agent repo add base"            |
        | файл D/secrets/base.pass с правами 0644                    | repository.password     | SECRET_FILE_REJECTED       | "sudo chmod 0600"                          |
        | restic на cat config завершается кодом 12                  | repository.password     | WRONG_PASSWORD             | "09-troubleshooting.md#repositorypassword" |

    @local
    Сценарий: Отказ доступа к S3 из-за env-файла называет файл, а не значения
      Дано в D/agent.d репозиторий obj с адресом s3 и env_file D/secrets/restic-obj.env с правами 0644
      Когда оператор выполняет repo check obj
      Тогда строка repository.credentials с предметом obj — ERROR и сообщение называет SECRET_FILE_REJECTED и путь D/secrets/restic-obj.env
      И подсказка содержит "sudo chmod 0600 D/secrets/restic-obj.env"
      И restic не вызывался кроме version

    @local
    Сценарий: Молчащее хранилище при repo check ограничено таймаутом подключения
      Дано restic на cat config не отвечает
      Когда оператор выполняет repo check base --connect-timeout 2s
      Тогда строка repository.storage — ERROR и сообщение называет BACKEND_UNAVAILABLE и "2s"
      И restic получил SIGTERM своей группе
      И команда завершилась не позже чем через 12 секунд от начала обращения
      И код выхода класса «есть ошибки»

    # ЧВ9
    @local
    Сценарий: Таймаут подключения по умолчанию 10 секунд
      Дано restic на cat config не отвечает
      Когда оператор выполняет repo check base
      Тогда сообщение строки repository.storage называет "10s"

    @local
    Сценарий: Шаги restic идут без блокировки и без кэша
      Когда оператор выполняет repo check base
      Тогда каждый запуск restic кроме version получил флаги --no-lock и --no-cache
      И restic check не вызывался

  # ===========================================================================
  # A8d-1
  Правило: repository id сравнивается с объявленным службой серверу

    @start
    Сценарий: Служба при регистрации записывает объявленные id репозиториев
      Дано restic на cat config отдаёт id REPO-A
      Когда агент стартует и выполняет Register с фейковым сервером
      Тогда в каталоге S есть файл объявленных репозиториев с base и REPO-A, владелец U, права 0600
      И временных файлов в S нет

    @local
    Сценарий: Repository id совпадает с объявленным
      Когда оператор выполняет repo check base
      Тогда строка repository.id — OK и сообщение называет REPO-A

    # ЧВ5
    @local
    Структура сценария: Расхождение с объявленным id
      Дано <объявление>
      Когда оператор выполняет repo check base
      Тогда строка repository.id — <статус>
      И сообщение содержит <текст>
      И подсказка содержит "sudo systemctl restart sard-agent"
      И код выхода класса <класс>

      Примеры:
        | объявление                                   | статус | текст                              | класс             |
        | служба объявила base с id REPO-B             | ERROR  | "REPO-B"                           | «есть ошибки»     |
        | служба объявила base с пустым id             | WARN   | "could not read"                   | «предупреждения»  |
        | служба объявила только репозиторий other     | WARN   | "not announced to the server yet"  | «предупреждения»  |

    @local
    Сценарий: Без записи об объявлении сравнение пропускается
      Дано файла объявленных репозиториев в S нет
      Когда оператор выполняет repo check base
      Тогда строка repository.id — SKIP и подсказка содержит "sudo systemctl start sard-agent"
      И код выхода класса «в порядке»

  # ===========================================================================
  # A8d-1
  Правило: repo check показывает блокировки restic с возрастом и хостом-владельцем

    @local
    Сценарий: Нет блокировок
      Когда оператор выполняет repo check base
      Тогда строка repository.locks — OK с сообщением "no locks"
      И restic получил list locks с --no-lock

    @local
    Сценарий: Свежие блокировки не предупреждение
      Дано блокировки L(db1, 10 мин, 4242) и L(db2, 2 ч, 77)
      Когда оператор выполняет repo check base
      Тогда строка repository.locks — OK и сообщение называет "2 locks", "db1" и "db2"
      И код выхода класса «в порядке»

    # ЧВ1 ЧВ2
    @local
    Сценарий: Своя зависшая блокировка с мёртвым процессом предлагает команду снятия
      Дано блокировка L(db1, 30 ч, 4242), процесс 4242 не жив
      Когда оператор выполняет repo check base
      Тогда строка repository.locks — WARN и сообщение называет "db1" и "30h"
      И подсказка содержит "sudo sard-agent repo unlock base --remove-stale"
      И код выхода класса «предупреждения»

    @local
    Сценарий: Своя старая блокировка живого процесса не предлагается к снятию
      Дано блокировка L(db1, 30 ч, 4242), процесс 4242 жив
      Когда оператор выполняет repo check base
      Тогда строка repository.locks — WARN и сообщение содержит "held by running process 4242"
      И подсказка не содержит "repo unlock"

    @local
    Сценарий: Чужая зависшая блокировка называет хост и не предлагается к снятию здесь
      Дано блокировка L(db2, 48 ч, 77)
      Когда оператор выполняет repo check base
      Тогда строка repository.locks — WARN и сообщение называет "db2" и "48h"
      И подсказка содержит "on db2"
      И подсказка не содержит "repo unlock base --remove-stale" как команду для этого хоста

    @local
    Сценарий: Блокировка ровно на пороге ещё не зависшая
      Дано блокировка L(db1, 24 ч, 4242), процесс 4242 не жив
      Когда оператор выполняет repo check base
      Тогда строка repository.locks — OK

    @local
    Сценарий: Непрочитанная блокировка не роняет шаг
      Дано restic list locks отдаёт id блокировки, а cat lock на неё завершается ошибкой "not found"
      Когда оператор выполняет repo check base
      Тогда строка repository.locks — OK и сообщение называет "1 lock disappeared while reading"

  # ===========================================================================
  # A8d-1
  Правило: repo unlock снимает только свои зависшие блокировки и только по явному флагу

    # ЧВ2
    @local
    Сценарий: Своя зависшая блокировка снимается с предупреждением и строкой аудита
      Дано блокировка L(db1, 30 ч, 4242), процесс 4242 не жив
      Когда оператор выполняет repo unlock base --remove-stale
      Тогда код выхода класса «в порядке»
      И stderr содержит предупреждение о повреждении работы живого процесса
      И restic получил unlock без --remove-all от uid 990
      И stdout называет "removed 1 stale lock"
      И строка аудита "repository base stale locks removed: 1"

    @local
    Сценарий: Без флага remove-stale ничего не снимается
      Дано блокировка L(db1, 30 ч, 4242), процесс 4242 не жив
      Когда оператор выполняет repo unlock base
      Тогда код выхода класса «использование»
      И сообщение называет "--remove-stale"
      И restic не вызывался кроме version
      И хост не изменён

    @local
    Сценарий: Чужая блокировка, которую задел бы restic unlock, отменяет снятие
      Дано блокировки L(db1, 30 ч, 4242) и L(db2, 48 ч, 77), процесс 4242 не жив
      Когда оператор выполняет repo unlock base --remove-stale
      Тогда код выхода класса «использование»
      И сообщение называет причину STALE_LOCKS_FOREIGN, "db2" и "48h"
      И restic unlock не вызывался
      И строк аудита нет

    @local
    Сценарий: Своя блокировка моложе порога, которую задел бы restic unlock, отменяет снятие
      Дано блокировки L(db1, 30 ч, 4242) и L(db1, 2 ч, 4243), процессы 4242 и 4243 не живы
      Когда оператор выполняет repo unlock base --remove-stale
      Тогда код выхода класса «использование»
      И сообщение называет причину STALE_LOCKS_FOREIGN и "2h"
      И restic unlock не вызывался

    @local
    Сценарий: Чужая свежая блокировка снятию не мешает
      Дано блокировки L(db1, 30 ч, 4242) и L(db2, 5 мин, 77), процесс 4242 не жив
      Когда оператор выполняет repo unlock base --remove-stale
      Тогда код выхода класса «в порядке»
      И restic unlock вызван один раз

    @local
    Сценарий: Нечего снимать
      Дано блокировка L(db1, 30 ч, 4242), процесс 4242 жив
      Когда оператор выполняет repo unlock base --remove-stale
      Тогда код выхода класса «в порядке»
      И stdout — "no stale locks of this host in repository base"
      И restic unlock не вызывался
      И строк аудита нет

    @local
    Сценарий: Список блокировок перечитывается непосредственно перед снятием
      Дано блокировка L(db1, 30 ч, 4242), процесс 4242 не жив
      И при втором чтении списка появляется L(db2, 48 ч, 77)
      Когда оператор выполняет repo unlock base --remove-stale
      Тогда сообщение называет причину STALE_LOCKS_FOREIGN и "db2"
      И restic unlock не вызывался

    @local
    Структура сценария: Права repo unlock
      Когда оператор выполняет repo unlock base --remove-stale <как>
      Тогда код выхода класса «использование»
      И сообщение называет причину PRIVILEGES_REQUIRED и "sudo sard-agent repo unlock base --remove-stale"
      И restic не вызывался

      Примеры:
        | как                    |
        | от пользователя службы |
        | от постороннего        |

    @local
    Сценарий: Недоступное хранилище при снятии
      Дано restic на list locks печатает "connection refused"
      Когда оператор выполняет repo unlock base --remove-stale
      Тогда код выхода класса «временная»
      И сообщение называет BACKEND_UNAVAILABLE
      И restic unlock не вызывался

    @integration
    Сценарий: Настоящий restic снимает свою зависшую блокировку и оставляет чужую свежую
      Дано в локальном репозитории настоящим restic создана блокировка хоста db1 с временем 30 ч назад и мёртвым pid
      И блокировка хоста db2 с временем 5 мин назад
      Когда оператор выполняет repo unlock base --remove-stale
      Тогда в каталоге locks репозитория осталась только блокировка db2

  # ===========================================================================
  # A8d-1
  Правило: read-data запускает лёгкую проверку целостности

    @local
    Сценарий: Проверка части данных
      Когда оператор выполняет repo check base --read-data 5
      Тогда restic получил check --read-data-subset=5% от uid 990
      И строка repository.integrity идёт после repository.locks и — OK
      И код выхода класса «в порядке»

    @local
    Сценарий: Найденные ошибки целостности
      Дано restic на check завершается кодом 1 и печатает "pack abc contains 1 errors"
      Когда оператор выполняет repo check base --read-data 5
      Тогда строка repository.integrity — ERROR
      И подсказка содержит "09-troubleshooting.md#repositoryintegrity"
      И код выхода класса «есть ошибки»

    @local
    Структура сценария: Недопустимый процент — ошибка использования без обращения к хранилищу
      Когда оператор выполняет repo check base --read-data <значение>
      Тогда код выхода класса «использование»
      И сообщение называет "--read-data"
      И restic не вызывался кроме version

      Примеры:
        | значение |
        | 0        |
        | 101      |
        | -5       |
        | 5.5      |
        | "abc"    |

    @local
    Сценарий: read-data у sard-agent check не принимается
      Когда оператор выполняет check --read-data 5
      Тогда код выхода класса «использование»
      И сообщение называет "--read-data" и "repo check"

    # V0 не влит — заглушка
    @local
    Сценарий: Учётка только на чтение даёт понятное сообщение вместо ошибки restic
      Дано restic на check печатает "unable to create lock in backend" и "Access Denied"
      Когда оператор выполняет repo check base --read-data 5
      Тогда строка repository.integrity — ERROR с сообщением "restic check needs to create a lock in the repository and these credentials cannot write"
      И сообщение содержит "checking with read-only credentials is not supported yet"
      И подсказка содержит "09-troubleshooting.md#repositoryintegrity"
      И stdout не содержит строки restic целиком, кроме очищенной причины после "restic said"

    @local
    Сценарий: Ошибка раньше шага целостности пропускает его
      Дано restic на cat config завершается кодом 12
      Когда оператор выполняет repo check base --read-data 5
      Тогда строка repository.integrity — SKIP
      И restic check не вызывался

    # Решение владельца 2026-10-15, Ч7
    @local
    Сценарий: Репозиторий занят работающим бэкапом
      Дано restic на check завершается кодом 11 и печатает "repository is locked by another process"
      Когда оператор выполняет repo check base --read-data 5
      Тогда строка repository.integrity — WARN
      И сообщение содержит "locked by another process" и просит повторить позже
      И подсказка содержит "sudo sard-agent repo check base --read-data 5"
      И код выхода класса «предупреждения»

    @integration
    Сценарий: Настоящий restic check отказывается, пока бэкап держит репозиторий
      Дано в локальном репозитории настоящим restic создана блокировка работающего бэкапа
      Когда оператор выполняет repo check base --read-data 100
      Тогда строка repository.integrity — WARN и сообщение содержит "locked by another process"
      И код выхода класса «предупреждения»

  # ===========================================================================
  # A8d-1
  Правило: repo check без имени проверяет все репозитории хоста по очереди

    @local
    Сценарий: Все репозитории в порядке конфига
      Дано в D/agent.d репозитории obj и arch
      Когда оператор выполняет repo check
      Тогда строки repository идут блоками base, затем obj, затем arch
      И запуски restic разных репозиториев не перекрываются по времени

    @local
    Сценарий: Ошибка одного репозитория не останавливает остальные
      Дано в D/agent.d репозиторий obj, restic на cat config для obj завершается кодом 12
      Когда оператор выполняет repo check
      Тогда строка repository.password с предметом obj — ERROR
      И все строки с предметом base — OK
      И код выхода класса «есть ошибки»

    @local
    Сценарий: Худший итог определяет код выхода
      Дано в D/agent.d репозиторий obj с блокировкой L(db1, 30 ч, 4242), процесс не жив
      Когда оператор выполняет repo check
      Тогда код выхода класса «предупреждения»

    @local
    Сценарий: Нет репозиториев
      Дано в C и D/agent.d нет репозиториев
      Когда оператор выполняет repo check
      Тогда код выхода класса «в порядке»
      И stdout содержит строку OK с сообщением "no repositories configured"
      И restic не вызывался кроме version

    @local
    Сценарий: repo check с неизвестным именем называет подключённые репозитории
      Когда оператор выполняет repo check nope
      Тогда код выхода класса «использование»
      И сообщение называет причину REPOSITORY_UNKNOWN и имя base
      И stdout пуст

  # ===========================================================================
  # A8d-1
  Правило: Права repo check

    @local
    Структура сценария: Кто может выполнять repo check
      Когда оператор выполняет repo check base <как>
      Тогда код выхода класса <класс>
      И <итог>

      Примеры:
        | как                    | класс           | итог                                                                  |
        | от root                | «в порядке»     | каждый запуск restic получил uid 990 и gid 990                        |
        | от пользователя службы | «в порядке»     | restic запущен без смены пользователя                                 |
        | от постороннего        | «использование» | сообщение называет PRIVILEGES_REQUIRED и "sudo sard-agent repo check" |

    @local
    Сценарий: Под root без пользователя службы repo check не выполняется
      Дано пользователя службы на хосте нет
      Когда оператор выполняет repo check base
      Тогда код выхода класса «использование»
      И сообщение называет причину SERVICE_USER_UNKNOWN
      И restic не вызывался

  # ===========================================================================
  # A8d-2
  Правило: sard-agent check печатает по строке на каждую проверку хоста

    # С A8d-3 строки server.reachable, server.tls, server.accepted и clock.skew
    # на здоровом хосте — OK (Ч14-Ч16), сводка "27 ok, 0 warnings, 0 errors,
    # 2 skipped" (SKIP остаются tls.role и restore.locations). До A8d-3 они были
    # SKIP (Ч33).
    @local
    Сценарий: Здоровый хост
      Когда оператор выполняет check
      Тогда код выхода класса «в порядке»
      И строки идут в порядке service.user, config.load, config.names, config.references, files.permissions, restic.binary, tls.identity, tls.chain, tls.expiry, tls.role, server.reachable, server.tls, server.accepted, clock.skew, шесть строк repository с предметом base, secret.readable с предметом pg, plugin.declared, plugin.programs с предметом files, plugin.programs с предметом postgresql, restore.locations, selinux.labels, selinux.denials, service.running, service.steps
      И строки tls.role и restore.locations — SKIP, остальные — OK
      И у строк OK нет строки подсказки

    @local
    Сценарий: Условные проверки до своих срезов пропущены с пояснением
      Когда оператор выполняет check
      Тогда строка tls.role — SKIP с сообщением "agent role is checked once V1 is available"
      И строка restore.locations — SKIP с сообщением "restore locations are checked once R2 is available"
      И код выхода класса «в порядке»

    @local
    Сценарий: Проверка репозитория в check — без чтения данных
      Когда оператор выполняет check
      Тогда restic check не вызывался
      И строки repository.integrity нет

  # ===========================================================================
  # A8d-2
  Правило: Каждая ошибка и предупреждение check сопровождаются подсказкой

    @local
    Структура сценария: Ошибки хоста с подсказками
      Дано <условие>
      Когда оператор выполняет check
      Тогда строка <проверка> — ERROR
      И подсказка содержит <подсказка>
      И код выхода класса «есть ошибки»

      Примеры:
        | условие                                                         | проверка          | подсказка                                         |
        | пользователя службы на хосте нет                                | service.user      | "09-troubleshooting.md#serviceuser"               |
        | C содержит неразбираемый YAML                                   | config.load       | "09-troubleshooting.md#configload"                |
        | фрагмент D/agent.d/x.yaml содержит ключ server                  | config.load       | "09-troubleshooting.md#configload"                |
        | репозиторий base есть и в C, и в D/agent.d/repo-base.yaml       | config.names      | "sudo sard-agent repo remove base"                |
        | secrets.pg указывает на несуществующий D/secrets/none           | config.references | "sudo sard-agent secret set pg"                   |
        | tls.key_file с правами 0644                                     | files.permissions | "sudo chmod 0600"                                 |
        | D/secrets/pg принадлежит root                                   | files.permissions | "sudo chown sard-agent"                           |
        | каталог D/secrets с правами 0755                                | files.permissions | "sudo chmod 0700"                                 |
        | restic не найден                                                | restic.binary     | "09-troubleshooting.md#resticbinary"              |
        | restic версии 0.18.0                                            | restic.binary     | "09-troubleshooting.md#resticbinary"              |
        | tls.key_file — ключ другого сертификата                         | tls.identity      | "sudo sard-agent enroll --force"                  |
        | tls.cert_file не задан в C                                      | tls.identity      | "sudo sard-agent enroll --server"                 |
        | сертификат выдан другим CA, чем tls.ca_file                     | tls.chain         | "sudo sard-agent enroll --force"                  |
        | сертификат истёк вчера                                          | tls.expiry        | "sudo sard-agent enroll --force"                  |
        | сертификат начинает действовать завтра                          | tls.expiry        | "09-troubleshooting.md#tlsexpiry"                 |
        | файл секрета D/secrets/pg — каталог                             | secret.readable   | "sudo sard-agent secret set pg"                   |
        | файл секрета D/secrets/pg — символьная ссылка                   | secret.readable   | "sudo sard-agent secret set pg"                   |
        | pg_dump в PATH есть, psql рядом нет                             | plugin.programs   | "postgresql-client"                               |
        | pg_dump в PATH есть, pg_dumpall рядом нет                       | plugin.programs   | "postgresql-client"                               |
        | служба failed                                                 | service.running   | "sudo systemctl start sard-agent"                 |

    @local
    Структура сценария: Предупреждения хоста с подсказками
      Дано <условие>
      Когда оператор выполняет check
      Тогда строка <проверка> — WARN
      И подсказка содержит <подсказка>
      И код выхода класса «предупреждения»

      Примеры:
        | условие                                                         | проверка          | подсказка                                         |
        | C доступен на запись группе пользователя службы                 | files.permissions | "sudo chmod g-w"                                  |
        | фрагмент D/agent.d/repo-x.yaml с правами 0666                   | files.permissions | "sudo chmod 0640"                                 |
        | restic версии 0.19.0 при поставляемой 0.19.1                    | restic.binary     | "restic.path"                                     |
        | сертификат сроком 365 суток истекает через 10 дней (поправка C1 фаза 3) | tls.expiry | "sudo sard-agent status"                  |
        | pg_restore рядом с pg_dump нет                                | plugin.programs   | "postgresql-client"                               |
        | pg_dump версии 16.4, а pg_dumpall рядом — 15.2                  | plugin.programs   | "postgresql-client"                               |
        | служба active с NRestarts=3                                     | service.running   | "journalctl -u sard-agent"                        |
        | каталог журнала S/journal не читается                           | service.steps     | "09-troubleshooting.md#servicesteps"              |

    @local
    Сценарий: Ошибка конфига пропускает проверки, которые зависят от него
      Дано C содержит неразбираемый YAML
      Когда оператор выполняет check
      Тогда строки config.names, config.references, files.permissions, tls.identity, tls.chain, tls.expiry, server.reachable, server.tls, server.accepted, clock.skew, secret.readable и все repository — SKIP с сообщением "not checked: config.load failed"
      И строки restic.binary, plugin.declared, service.running и service.steps выполнены
      И restic не вызывался кроме version

    @local
    Сценарий: Ссылка на несуществующий файл называет ключ и путь
      Дано password_file репозитория base указывает на D/secrets/none.pass
      Когда оператор выполняет check
      Тогда строка config.references — ERROR и сообщение называет "repositories.base.password_file" и D/secrets/none.pass

    @local
    Сценарий: Одно имя в двух местах называет оба файла
      Дано секрет pg определён и в C, и в D/agent.d/secret-pg.yaml
      Когда оператор выполняет check
      Тогда строка config.names — ERROR и сообщение называет DUPLICATE_NAME, C и D/agent.d/secret-pg.yaml

    @local
    Сценарий: Все нарушения прав в одной строке
      Дано tls.key_file с правами 0644 и D/secrets/pg с правами 0640
      Когда оператор выполняет check
      Тогда есть ровно одна строка files.permissions, она ERROR и называет оба пути
      И подсказка содержит команду chmod для каждого пути

    @local
    Сценарий: Неудобные, но допустимые права не нарушение
      Дано фрагмент D/agent.d/repo-x.yaml с правами 0644 и владельцем root
      Когда оператор выполняет check
      Тогда строка files.permissions — OK

    # Поправка C1 фаза 3: было «ровно через 30 дней — OK» без срока
    # сертификата; теперь порог — 30/365 срока (П46 agent-certificate-renewal).
    @local
    Сценарий: Сертификат на пороге 30 дней ещё в порядке
      Дано сертификат сроком 365 суток истекает через 30 суток и 1 секунду
      Когда оператор выполняет check
      Тогда строка tls.expiry — OK и сообщение называет дату окончания

    # Поправка C1 фаза 3: C1 влит, текст «automatic renewal is not available
    # yet» заменён (П46 agent-certificate-renewal.feature).
    @local
    Сценарий: Предупреждение о сроке отправляет к состоянию продления
      Дано сертификат сроком 365 суток истекает через 10 дней
      Когда оператор выполняет check
      Тогда сообщение строки tls.expiry содержит "expires in 10 days" и "has not been renewed"
      И не содержит "automatic renewal is not available yet"

    @local
    Сценарий: Список объявляемых плагинов с версиями
      Когда оператор выполняет check
      Тогда строка plugin.declared — OK и сообщение называет files и postgresql с их версиями

    @local
    Сценарий: Программы PostgreSQL названы с путём и версией
      Когда оператор выполняет check
      Тогда сообщение строки plugin.programs с предметом postgresql называет пути и версию 16.4 для pg_dump, psql, pg_dumpall и pg_restore

  # ===========================================================================
  # A8d-2
  Правило: Хост без программ PostgreSQL предупреждён строкой пропуска и не получает ненулевой код

    # ЧВ11 Ч31
    @local
    Сценарий: Без программ PostgreSQL строка плагина пропущена с сообщением и подсказкой
      Дано в PATH агента нет pg_dump, psql, pg_dumpall и pg_restore
      Когда оператор выполняет check
      Тогда строка plugin.programs с предметом postgresql — SKIP
      И сообщение начинается с "PostgreSQL client tools are not installed"
      И сообщение содержит "PostgreSQL sources on this host will fail"
      И подсказка содержит "postgresql-client" и "docs/plugins/postgresql.md"

    # ЧВ11 Ч31
    @local
    Сценарий: Хост только с источниками files без программ PostgreSQL завершается кодом 0
      Дано в PATH агента нет pg_dump, psql, pg_dumpall и pg_restore
      Когда оператор выполняет check
      Тогда код выхода — 0
      И строка plugin.programs с предметом files — OK
      # до A8d-3 строки сервера были SKIP (Ч33): "22 ok, 0 warnings, 0 errors, 7 skipped"
      И последняя строка stdout — "summary: 26 ok, 0 warnings, 0 errors, 3 skipped"

    # ЧВ11 Ч31
    @local
    Сценарий: В JSON отсутствие программ PostgreSQL — статус skipped без новых полей
      Дано в PATH агента нет pg_dump, psql, pg_dumpall и pg_restore
      Когда оператор выполняет check --json
      Тогда строка с check "plugin.programs" и subject "postgresql" — объект ровно с полями check, subject, status, message, hint
      И её status — "skipped"
      И её message начинается с "PostgreSQL client tools are not installed"
      И её hint содержит "postgresql-client"
      И ни у одной строки status не "warning" и не "error"
      И код выхода — 0

    # Ч31
    @local
    Структура сценария: Пропуск программ PostgreSQL имеет одну причину при любом режиме запуска
      Дано в PATH агента нет pg_dump, psql, pg_dumpall и pg_restore
      Когда оператор выполняет <команда>
      Тогда строка plugin.programs с предметом postgresql — SKIP
      И сообщение начинается с "PostgreSQL client tools are not installed"
      И сообщение не содержит "needs root" и "offline"

      Примеры:
        | команда                            |
        | check от root                      |
        | check от пользователя службы       |
        | check --offline                    |

    # Ч31
    @local
    Сценарий: При установленных программах PostgreSQL строка плагина не пропускается ни в каком режиме
      Когда оператор выполняет check --offline от пользователя службы
      Тогда строка plugin.programs с предметом postgresql — OK

    # Ч31
    @local
    Сценарий: Каталог PATH, который служба не может прочитать, равен отсутствию pg_dump
      Дано pg_dump есть только в каталоге PATH с правами 0700 и владельцем root
      Когда оператор выполняет check
      Тогда строка plugin.programs с предметом postgresql — SKIP
      И сообщение начинается с "PostgreSQL client tools are not installed"
      И код выхода — 0

    # Ч31
    @local
    Сценарий: Неполный клиент PostgreSQL по-прежнему ошибка
      Дано в PATH агента есть pg_dump, а pg_dumpall рядом нет
      Когда оператор выполняет check
      Тогда строка plugin.programs с предметом postgresql — ERROR
      И код выхода класса «есть ошибки»

    @local
    Сценарий: Секрет проверяется без вывода значения
      Дано значение D/secrets/pg — SECRET-MARKER-1
      Когда оператор выполняет check
      Тогда строка secret.readable с предметом pg — OK
      И значения не раскрыты

    @local
    Сценарий: Пустой файл секрета — ошибка
      Дано файл D/secrets/pg пуст
      Когда оператор выполняет check
      Тогда строка secret.readable с предметом pg — ERROR и сообщение содержит "empty"

    @local
    Сценарий: Служба запущена и шаги в работе
      Дано в S/journal две записи и один временный файл
      Когда оператор выполняет check
      Тогда строка service.running — OK и сообщение называет время старта в UTC
      И строка service.steps — OK с сообщением "2 steps in progress"

    @local
    Сценарий: Без systemd проверка службы пропущена
      Дано на хосте нет /run/systemd/system
      Когда оператор выполняет check
      Тогда строка service.running — SKIP с сообщением, что systemd нет
      И строка service.steps выполнена

    # Решение владельца 2026-10-15, Ч8
    @local
    Сценарий: Нет репозиториев — не предупреждение, а пояснение
      Дано в C и D/agent.d нет репозиториев
      Когда оператор выполняет check
      Тогда есть строка repository.storage с предметом "-" — OK с сообщением, которое начинается с "no repositories configured"
      И сообщение содержит "sudo sard-agent repo add"
      И у строки нет подсказки
      И код выхода класса «в порядке»
      И restic не вызывался кроме version

  # ===========================================================================
  # A8d-2
  Правило: SELinux проверяется, только если он enforcing

    @local
    Сценарий: SELinux не enforcing
      Когда оператор выполняет check
      Тогда строки selinux.labels и selinux.denials — OK с сообщением "SELinux is not enforcing"
      И matchpathcon и ausearch не вызывались

    @local
    Сценарий: Неверная метка файла агента
      Дано SELinux enforcing, метка D/secrets/pg отличается от метки по политике
      Когда оператор выполняет check
      Тогда строка selinux.labels — WARN и называет D/secrets/pg с текущей и ожидаемой меткой
      И подсказка содержит "sudo restorecon -Rv"

    # ЧВ1
    @local
    Сценарий: Недавние отказы SELinux для службы
      Дано SELinux enforcing, ausearch отдаёт 7 отказов AVC с comm restic за последние 24 ч
      Когда оператор выполняет check
      Тогда строка selinux.denials — WARN, сообщение называет 7 и первые 5 отказов
      И ausearch получил ограничение по времени 24 ч

    @local
    Сценарий: Нет ausearch — проверка отказов пропущена
      Дано SELinux enforcing, ausearch не найден
      Когда оператор выполняет check
      Тогда строка selinux.denials — SKIP и подсказка называет пакет audit

    @vm
    Сценарий: На Rocky с SELinux enforcing здоровый хост проходит check
      Дано ВМ Rocky 9 с SELinux enforcing после установки пакета, регистрации и repo add
      Когда оператор выполняет sudo sard-agent check
      Тогда код выхода 0 и строки selinux.labels и selinux.denials — OK

  # ===========================================================================
  # A8d-3
  Правило: check проверяет связь с сервером без побочных действий на нём

    @fake
    Сценарий: Сервер принимает агента
      Когда оператор выполняет check
      Тогда строки server.reachable, server.tls, server.accepted и clock.skew — OK
      И фейковый сервер получил ровно один вызов Probe с сертификатом агента
      И фейковый сервер не получил Register и Connect

    @fake
    Структура сценария: Отказы связи с сервером
      Дано <условие>
      Когда оператор выполняет check
      Тогда строка <проверка> — ERROR и сообщение содержит <текст>
      И подсказка содержит <подсказка>
      И строки после <проверка> до clock.skew включительно — SKIP

      Примеры:
        | условие                                              | проверка         | текст                     | подсказка                           |
        | имя server.address не разрешается                    | server.reachable | "no such host"            | "09-troubleshooting.md#serverreachable" |
        | порт server.address закрыт                           | server.reachable | "connection refused"      | "09-troubleshooting.md#serverreachable" |
        | сертификат сервера выдан другим CA                   | server.tls       | "not issued by"           | "09-troubleshooting.md#servertls"   |
        | имя в сертификате сервера не совпадает с адресом     | server.tls       | "name"                    | "09-troubleshooting.md#servertls"   |
        | сервер не принимает сертификат агента в рукопожатии  | server.tls       | "client certificate"      | "sudo sard-agent enroll --force"    |

    @fake
    Структура сценария: Сервер отказывает агенту
      Дано фейковый сервер отвечает на Probe отказом <причина>
      Когда оператор выполняет check
      Тогда строка server.accepted — ERROR и сообщение называет <причина>
      И подсказка содержит "sudo sard-agent enroll --force"
      И строка clock.skew — SKIP

      Примеры:
        | причина        |
        | AGENT_REVOKED          |
        | CERT_REVOKED           |
        | CERT_UNKNOWN           |
        | CERT_EXPIRED           |
        | CERT_IDENTITY_MISMATCH |
        | CERT_MISSING           |

    # ЧВ6
    @fake
    Сценарий: Сервер без вызова проверки — предупреждение, а не ошибка
      Дано фейковый сервер отвечает на Probe UNIMPLEMENTED
      Когда оператор выполняет check
      Тогда строка server.accepted — WARN с сообщением "the server is older than this agent"
      И строка clock.skew — SKIP
      И фейковый сервер не получил Register и Connect

    @fake
    Сценарий: Молчащий сервер ограничен таймаутом подключения
      Дано фейковый слушатель принимает соединение и не отвечает
      Когда оператор выполняет check --connect-timeout 2s
      Тогда строка server.tls — ERROR и сообщение содержит "did not answer within 2s"
      И проверка сервера заняла не больше 4 секунд

    # ЧВ1
    @fake
    Структура сценария: Расхождение часов
      Дано время сервера отличается от часов агента на <расхождение>
      Когда оператор выполняет check
      Тогда строка clock.skew — <статус>

      Примеры:
        | расхождение   | статус |
        | 59 секунд     | OK     |
        | 60 секунд     | OK     |
        | 61 секунду    | WARN   |
        | минус 5 минут | WARN   |

    @fake
    Сценарий: Предупреждение о часах называет расхождение и как исправить
      Дано время сервера на 300 секунд впереди часов агента
      Когда оператор выполняет check
      Тогда сообщение строки clock.skew называет "300s"
      И подсказка содержит "timedatectl set-ntp true"

    @stand
    Сценарий: check на хосте зарегистрированного агента против настоящего сервера
      Дано на стенде агент зарегистрирован и служба работает, идёт бэкап
      Когда оператор выполняет sudo sard-agent check
      Тогда строки server.* и clock.skew — OK
      И шаг бэкапа завершился succeeded, сессия службы не прервалась
      И у агента в консоли не изменились плагины и репозитории

  # ===========================================================================
  # A8d-2
  Правило: Общий код выхода check отражает худшую строку

    @local
    Структура сценария: Код выхода по строкам
      Дано <состав>
      Когда оператор выполняет check
      Тогда код выхода — <код>

      Примеры:
        | состав                                                                 | код |
        | все строки OK или SKIP                                                 | 0   |
        | одна WARN, остальные OK                                                | 8   |
        | одна WARN и одна ERROR                                                 | 1   |
        | пять SKIP после offline, остальные OK                                  | 0   |
        | SKIP plugin.programs postgresql без программ PostgreSQL, остальные OK  | 0   |
        | SKIP plugin.programs postgresql без программ PostgreSQL и одна WARN    | 8   |
        | SKIP plugin.programs postgresql без программ PostgreSQL и одна ERROR   | 1   |

    @local
    Сценарий: Ошибка флага — код использования и пустой stdout
      Когда оператор выполняет check --no-such-flag
      Тогда код выхода класса «использование»
      И stdout пуст

    @local
    Сценарий: Прерывание check
      Дано restic на cat config не отвечает
      Когда команда check получает SIGINT во время проверки репозитория
      Тогда restic получил SIGTERM своей группе
      И код выхода класса «временная»
      И stderr называет INTERRUPTED

  # ===========================================================================
  # A8d-2
  Правило: json выводит по объекту на проверку со стабильным идентификатором

    @local
    Сценарий: Каждая строка JSON — объект проверки
      Когда оператор выполняет check --json
      Тогда каждая строка stdout — объект JSON ровно с полями check, subject, status, message, hint
      И число строк равно числу строк текстового вывода без строк hint и summary
      И у строки с check "repository.password" subject — "base", у строки "config.load" subject — null
      И status каждой строки — одно из ok, warning, error, skipped
      И hint строк со status ok — null

    @local
    Сценарий: Ошибка в JSON несёт подсказку
      Дано restic на cat config завершается кодом 12
      Когда оператор выполняет check --json
      Тогда в строке с check "repository.password" status — "error", hint содержит "09-troubleshooting.md#repositorypassword"
      И код выхода класса «есть ошибки»

    @local
    Сценарий: Идентификаторы проверок совпадают с закреплённым списком
      Когда оператор выполняет check --json на хосте с enforcing SELinux и одним репозиторием
      Тогда множество значений check равно закреплённому в тесте списку Ч10 из 28 идентификаторов, включая repository.storage, repository.credentials, repository.initialized, repository.password, repository.id и repository.locks
      И repository.integrity в нём нет, он есть только у repo check с read-data
      И тест падает при переименовании любого идентификатора

    @local
    Сценарий: repo check тоже выводит JSON Lines
      Когда оператор выполняет repo check base --json
      Тогда stdout — шесть строк JSON с check от repository.storage до repository.locks и subject "base"

    @local
    Сценарий: Ошибка самой команды в JSON-режиме — текстом в stderr
      Когда оператор выполняет check --json от постороннего
      Тогда stdout пуст
      И stderr содержит "sard-agent check" и PRIVILEGES_REQUIRED

  # ===========================================================================
  # A8d-2
  Правило: offline не обращается к серверу и хранилищам

    @local
    Сценарий: Проверка без сети
      Когда оператор выполняет check --offline
      Тогда строки server.reachable, server.tls, server.accepted, clock.skew и все repository — SKIP с сообщением "offline"
      И фейковый слушатель сервера не получил ни одного соединения
      И restic не вызывался кроме version
      И строки config.load, files.permissions, restic.binary, tls.identity, tls.chain, tls.expiry, secret.readable, plugin.programs и service.running выполнены

    @local
    Сценарий: offline не делает ошибки сети предупреждениями
      Дано имя server.address не разрешается
      Когда оператор выполняет check --offline
      Тогда код выхода класса «в порядке»

    # Решение владельца 2026-10-15, Ч32
    @local
    Структура сценария: Offline id репозитория пропущен при любом состоянии записи объявленных id
      Дано <запись>
      Когда оператор выполняет check --offline
      Тогда строка repository.id с предметом base — SKIP с сообщением "offline"
      И подсказка содержит "without --offline"
      И файл объявленных репозиториев не читался

      Примеры:
        | запись                                           |
        | служба объявила base с id REPO-A                 |
        | служба объявила base с id REPO-B                 |
        | служба объявила base с пустым id                 |
        | служба объявила только репозиторий other         |
        | файла объявленных репозиториев в S нет           |

    @local
    Сценарий: Онлайн без файла объявленных id сравнение пропускается как прежде
      Дано файла объявленных репозиториев в S нет
      Когда оператор выполняет check
      Тогда строка repository.id — SKIP и подсказка содержит "sudo systemctl start sard-agent"
      И код выхода класса «в порядке»

  # ===========================================================================
  # A8d-2
  Правило: check под sudo видит всё, от пользователя службы — что может, посторонним — отказ

    @local
    Сценарий: От пользователя службы недоступное пропускается с пояснением
      Когда оператор выполняет check от пользователя службы на хосте с enforcing SELinux
      Тогда строка selinux.denials — SKIP с сообщением "needs root"
      И stdout после summary содержит "some checks need root: run sudo sard-agent check"
      И restic запущен без смены пользователя

    @local
    Сценарий: Посторонний получает отказ с готовой командой
      Когда оператор выполняет check от постороннего
      Тогда код выхода класса «использование»
      И сообщение называет причину PRIVILEGES_REQUIRED и "sudo sard-agent check"
      И stdout пуст
      И restic не вызывался

    @local
    Сценарий: Под root конфиг и секреты читаются от имени службы
      Дано каталог D/agent.d с правами 0700 и владельцем root
      Когда оператор выполняет check
      Тогда строка config.load — ERROR и сообщение говорит, что служба не может прочитать D/agent.d

  # ===========================================================================
  # A8d-2
  Правило: check и repo check ничего не меняют на хосте

    @local
    Сценарий: check на здоровом хосте ничего не меняет
      Когда оператор выполняет check
      Тогда хост не изменён
      И блокировка конфига D/agent.d/.sard-config.lock не бралась
      И фейк syslog не получил строк

    @local
    Сценарий: check на сломанном хосте тоже ничего не меняет
      Дано tls.key_file с правами 0644, служба failed, блокировка L(db1, 30 ч, 4242) с мёртвым процессом
      Когда оператор выполняет check
      Тогда хост не изменён
      И restic unlock не вызывался
      И systemctl не получал start и restart

    @local
    Сценарий: repo check с read-data не пишет на хосте
      Когда оператор выполняет repo check base --read-data 5
      Тогда в restic.cache_dir, S и D тот же набор файлов с тем же содержимым, владельцем и правами

    @local
    Сценарий: Одновременные check не мешают друг другу и бэкапу
      Дано restic отвечает с задержкой 1 с, а у base есть блокировка бэкапа L(db1, 1 мин, 4242), процесс жив
      Когда оператор одновременно выполняет check дважды
      Тогда оба завершились с кодом класса «в порядке»
      И ни один запуск restic не был без --no-lock

  # ===========================================================================
  # A8d-2
  Правило: Секреты не попадают в вывод check

    @local
    Сценарий: Значения не раскрыты ни в тексте, ни в JSON
      Дано значение секрета pg — SECRET-MARKER-1, пароль base — PASS-MARKER-1, в D/agent.d репозиторий obj с адресом, содержащим URL-MARKER, и env_file с ключом SECRET-MARKER-2
      И restic на cat config для obj печатает адрес с URL-MARKER и "Access Denied"
      Когда оператор выполняет check и check --json
      Тогда значения не раскрыты
      И ни один аргумент запущенных процессов не содержит SECRET-MARKER, PASS-MARKER и URL-MARKER
      И журнал агента не содержит их
      И адрес obj в выводе содержит "***"

  # ===========================================================================
  # A8d-2
  Правило: Полная проверка без read-data укладывается в секунды

    @local
    Сценарий: Проверка с отвечающими зависимостями быстрая
      Дано три репозитория, restic и сервер отвечают без задержки
      Когда оператор выполняет check
      Тогда команда завершилась быстрее 5 секунд

    @local
    Сценарий: Недоступные зависимости ограничены таймаутами
      Дано сервер и хранилища трёх репозиториев не отвечают
      Когда оператор выполняет check --connect-timeout 1s
      Тогда команда завершилась не позже чем через 4 × (1 + 10) секунд
      И каждая сетевая строка — ERROR с сообщением о таймауте

  # ===========================================================================
  # A8d-1 и A8d-2
  Правило: Справка называет флаги, права и коды

    @local
    Структура сценария: Справка команд проверки
      Когда оператор выполняет <команда> --help
      Тогда код выхода класса «в порядке»
      И stdout содержит <содержимое>

      Примеры:
        | команда     | содержимое                                                             |
        | check       | "--json", "--offline", "--connect-timeout", "sudo", "8", "only reads"  |
        | repo check  | "--read-data", "--json", "--timeout", "sudo", "8"                      |
        | repo unlock | "--remove-stale", "24", "this host", "never"                           |

    @local
    Сценарий: Список команд называет check и unlock
      Когда оператор выполняет repo без подкоманды и sard-agent без команды
      Тогда вывод repo перечисляет check и unlock
      И вывод sard-agent перечисляет check

  # ===========================================================================
  # A8d-4
  Правило: Ошибки шагов, которые видит консоль, отсылают к sard-agent check

    # ЧВ3
    @local
    Структура сценария: Подсказка в сообщении FAILED шага
      Дано <условие>
      Когда агент выполняет шаг BACKUP источника <плагин> в репозиторий base
      Тогда результат шага — FAILED
      И сообщение результата кончается строкой "run `sudo sard-agent check` on the host"

      Примеры:
        | условие                                           | плагин     |
        | restic на backup печатает "connection refused"    | files      |
        | restic на backup завершается кодом 12             | files      |
        | restic на backup печатает "Access Denied"         | files      |
        | файла пароля base нет                             | files      |
        | в PATH агента нет pg_dump                         | postgresql |

    @local
    Сценарий: Прочие отказы шага подсказку не получают
      Дано файлов источника files нет на хосте
      Когда агент выполняет шаг BACKUP источника files
      Тогда сообщение результата не содержит "sard-agent check"
