<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 0063 — Проверка хоста агента: `repo check`, `repo unlock`, запись объявленных id, `check` (A8d-1, A8d-2, A8d-3, A8d-4)

- Статус: принято (A8d-1 … A8d-4); решения владельца — 2026-10-14 (`docs/specs/agent/host-check.feature`, Ч1–Ч31, ЧВ1–ЧВ14) и 2026-10-15 (Ч32, Ч33, WARN занятого репозитория, OK без репозиториев)
- Дата: 2026-10-14, дополнено 2026-10-15
- Дополняет: ADR 0025 (классы кодов выхода), ADR 0050 (права запуска), ADR 0059 (restic без блокировки и кэша), ADR 0044 (журнал шагов, `executor.state_dir`)

## Контекст
Оператор хоста одной командой должен узнать, готов ли репозиторий к бэкапу и
восстановлению, а если нет — что сломано и какой командой чинить; вывод должен
годиться для Ansible и мониторинга. Срез A8d-1 — `repo check`, `repo unlock`
и запись объявленных службой id. Остальное (`sard-agent check` целиком, сервер,
подсказки в ошибках шагов) — A8d-2…A8d-4; общие типы сделаны так, чтобы те срезы
их переиспользовали.

## Решение

### Пакет `internal/hostcheck`
Общее для всех срезов A8d: статус (`OK`, `Warn`, `Error`, `Skip`), строка проверки
(`Line`), вывод (`Printer`: текст с подсказкой `hint:` и итогом `summary:` или
JSON Lines ровно с пятью полями), счётчик (`Tally`), ссылка на раздел руководства
(`HelpURL`), чтение блокировок restic (`Lock`, `LockLine`, `PlanUnlock`) и запись
объявленных id (`AnnouncedRepository`, `IDLine`). Пакет ничего не узнаёт о хосте:
команды собирают факты и передают их. Новая проверка получает новую константу
идентификатора в своей команде; идентификаторы не меняют смысла (Ч10).

### Код выхода 8
`0` — ни одной WARN и ERROR, `8` — есть WARN и нет ERROR, `1` — есть ERROR,
`2` — команда не выполнена, `6` — только SIGINT или SIGTERM. SKIP на код не влияет.
Сетевой отказ проверки — строка ERROR, а не код 6. Код 8 новый и в таблице
`repoClassCodes` не нужен: его даёт итог строк, а не класс отказа
(`exitOfWorst`). Коды Nagios не взяты: 1 и 2 в A2b уже значат «ошибка агента» и
«использование».

### Запись объявленных id
Служба при построении каждого `RegisterRequest` (`app.Agent.Announced`,
вызывается оттуда же) пишет `<executor.state_dir>/announced-repositories.json`:
`{"version":1,"repositories":[{"name","repository_id"}]}`. Файл создаётся
временным файлом в том же каталоге (`.tmp-announced-*`, 0600, владелец —
процесс службы), `fsync`, `rename`, `fsync` каталога; сбой не оставляет
временного файла. Файл записывает то, что агент предложил серверу, а не то, что сервер принял:
запись делается при построении запроса, до ответа. В файле имена и id, ни
ключа, ни адреса. Невозможность
записать — предупреждение в журнале, Register идёт дальше. `repo check`
читает файл и сравнивает id (Ч4); работает с `--offline` будущего среза A8d-2 и
не требует правок сервера (ЧВ5).

### restic: без блокировки и кэша
`restic.Options.ReadOnly` (`CLI.WithReadOnly`) добавляет `--no-lock --no-cache`
к командам репозитория; так идут `cat config`, `list locks` и `cat lock`.
`restic check --read-data-subset=<n>%` берёт блокировку в хранилище и свой
временный кэш (Ч23), `restic unlock` — без `--remove-all`. Прочие вызовы
wrapper не менялись.

### Что в какой шаг
Шаги 1–4 `repo check` — файлы репозитория и один `cat config`: сначала `env_file`
(`credentials`), затем crypto_provider и файл пароля (`password`), потом restic.
Если отказ найден по файлам, restic не вызывается, а предшествующие шаги в выводе
OK (так требует таблица спецификации: «каждая строка до шага — OK»). Причина
отказа restic → шаг: `BACKEND_UNAVAILABLE`, `TIMEOUT` — storage;
`S3_KEY_REJECTED`, `STORAGE_ACCESS_DENIED`, `BUCKET_NOT_FOUND`,
`SSH_KEY_NOT_AUTHORIZED`, `HOST_KEY_*`, `BACKEND_REFUSED` — credentials (хранилище
ответило, но не приняло нас); `REPOSITORY_NOT_INITIALIZED`,
`RESTIC_OUTPUT_UNEXPECTED` — initialized; `WRONG_PASSWORD` — password.
Классы `x509` (→ `BACKEND_UNAVAILABLE`), `Permission denied (publickey` (→
`SSH_KEY_NOT_AUTHORIZED`) и `Host key verification failed` (→ `HOST_KEY_MISMATCH`)
различаются по тексту в самом `repo check` (`reclassify`), а не в
`restic.fatalKind`/`repoinit`: существующие тесты A8b требуют, чтобы `x509` не
был сетевой ошибкой, и менять поведение `repo add` в этом срезе незачем.

### Блокировки
Своя блокировка — имя хоста как у `os.Hostname` (restic его берёт так же).
Возраст — по часам агента. Зависшая — строго старше 24 часов (ЧВ1). «Процесс
жив» — `kill(pid, 0)` (EPERM — жив), только для блокировок этого хоста. Шаг
`repository.locks` (список и все `cat lock`) — одно обращение к хранилищу для
`--connect-timeout`. Блокировка, которую `cat lock` не прочитал или чей JSON не
разобрался, считается исчезнувшей («disappeared while reading»).

### `repo unlock <имя> --remove-stale`
Только root (Р5). Без флага — ошибка использования до любых обращений.
Список читается, и если снимать нечего (нет своей зависшей блокировки с мёртвым
процессом), команда заканчивается успехом, `restic unlock` не вызывается. Иначе
список читается ещё раз; всё, что `restic unlock` снял бы сверх правила, —
блокировка старше 30 минут на любом хосте либо своя с мёртвым процессом, — даёт
`STALE_LOCKS_FOREIGN` (класс «использование», код 2) с хостом и возрастом каждой.
Остаётся гонка между вторым чтением и `restic unlock`: блокировка, ставшая
зависшей за это время, будет снята (в справке). Число снятых — число
подходящих блокировок второго чтения; в системный журнал пишется
`repository <имя> stale locks removed: <n>`.

### Таймауты
`--connect-timeout` по умолчанию 10 с (у остальных команд 30 с), `--timeout` —
2 мин на репозиторий; с `--read-data` без `--timeout` предела нет. Бюджет
(`repoContext`) создаётся на каждый репозиторий отдельно.

## Последствия
- `restic check` с `--read-data` под работающим бэкапом того же репозитория
  заканчивается WARN с пояснением («repository is locked by another process»,
  повторить позже; код выхода 8): restic не берёт его блокировку, пока идёт
  бэкап. Это поведение restic 0.19.1, измеренное интеграционным тестом. Решение
  владельца 2026-10-15: репозиторий не неисправен, поэтому не ERROR (первая
  версия A8d-1 давала ERROR). Только код 11 при живом контексте; ошибки
  целостности и отказы хранилища остаются ERROR.
- Интеграционный тест «чужая свежая блокировка остаётся» не воспроизводит чужой
  хост настоящим restic (для этого нужно другое имя машины, то есть пространство
  имён UTS); блокировка этого хоста выдаётся агенту за чужую подменой
  `deps.hostname`, настоящий restic при этом ничего не снимает.
- `hostDeps` получил `pidAlive`; `repo` без подкоманды перечисляет `check` и
  `unlock`.

## A8d-2: `sard-agent check`

### Состав: решения в `hostcheck`, факты в `cmd/sard-agent`
`check` — те же строки (`Line`, `Printer`), что у `repo check`. Решение о том,
что значит факт, принимает `internal/hostcheck` и проверяет таблицами без хоста:
`PermissionsLine` и `CanRead` (права, Ч11), `TLSIdentityLine`/`TLSChainLine`/
`TLSExpiryLine` (сертификат, Ч13), `ConfigLoadLine`/`ConfigNamesLine`/
`ReferencesLine` (конфиг), `PostgresLine` (Ч18, Ч31), `SELinuxLabelsLine`/
`SELinuxDenialsLine`, `ServiceRunningLine`/`ServiceStepsLine`, `SecretLine`
(Ч17) и малые строки (`NotCheckedLine`, `OfflineLine`, `NeedsRootLine`).
`cmd/sard-agent/check_*.go` собирают факты (stat, чтение файлов, запуск
программ) через `hostDeps` и передают их. Новое в `hostDeps`: `progExec` —
`restic.Executor` для программ хоста (getenforce, matchpathcon, ausearch,
`systemctl show`, программы PostgreSQL) и `loadConfig`. `secrets.Info` получил
`GID`: чтение «глазами службы» судится по битам прав.

### Зависимости между проверками (Ч10)
Порядок строк фиксирован. Отказ (ERROR) проверки пропускает зависимые
(`NotCheckedLine`): config.load → config.names, config.references,
files.permissions, tls.*, server.*, repository.*, secret.readable (config.names
— то же для зависящих от него); service.user → repository.*, secret.readable;
restic.binary → repository.*; tls.identity → server.*. restic.binary,
plugin.*, service.* идут и при сломанном конфиге, а restic.binary берёт
restic.path из того, что удалось прочитать из основного файла
(`config.Parse`). Совпадение имени в двух местах — не отказ config.load:
config.load OK, config.names ERROR.

### Права запуска (Ч20)
Под root конфиг читается самим процессом, но «видит» его служба: файлы и
agent.d судятся по правам (`CanRead` с uid и основной группой службы), и каталог,
который служба не прочитает, — ERROR config.load. Секреты читаются
`secrets.ReadOwned` с uid службы (под root и под службой одинаково), программы
PostgreSQL запускаются от имени службы: каталог PATH, где их не запустить,
равен отсутствию программ (Ч31). Пользователь службы: отказ файловой системы
(EACCES) в проверке файла — SKIP «needs root» с путём, selinux.denials — SKIP
«needs root»; после summary (только в тексте) строка «some checks need root».
PATH для поиска pg_dump — `PATH` процесса; под sudo это `secure_path`, а не PATH
службы из юнита: известное приближение.

### SELinux (Ч19)
Включено ли enforcing, определяет `getenforce`; нет программы или не
`Enforcing` — обе строки OK «SELinux is not enforcing». Метки — один запуск
`matchpathcon -V` по существующим путям агента (конфиг, agent.d, secrets, tls,
сами файлы секретов и сертификата, restic, кэш, state_dir, локальные
репозитории). Отказы — `ausearch -m avc -ts yesterday` (верхняя граница), а
окно 24 часа выдерживает сам разбор по времени записи аудита (`ParseDenials`):
формат даты у `ausearch` зависит от локали, время записи — нет.

### Служба (Ч21)
`systemctl show sard-agent.service -p ActiveState -p SubState -p NRestarts -p
ActiveEnterTimestamp` с `TZ=UTC` (время разбирается как UTC); `Systemd.Present`
решает, есть ли systemd; `Active`/`Restart` не вызываются, так что `check`
по-прежнему ничего не запускает. Шаги в работе — число `*.json` в
`<state_dir>/journal`, без временных `.tmp-*`.

### `--offline` и `repository.id` (Ч32, решение владельца)
Offline не обращается к хранилищу, поэтому текущего id нет. Строка
`repository.id` — SKIP «offline» при любом состоянии записи объявленных id
(Ч4): запись offline не читается, и её отсутствие или расхождение ничего не
меняет. Запись пишет только служба при Register; `check` и `repo check` её
никогда не трогают. Онлайн порядок: отказ предыдущего шага → SKIP «not checked»,
нет файла → SKIP «has not registered yet», иначе сравнение. Расхождение id видно
только онлайн — это сознательная цена `--offline` без сервера и хранилища.

### Нарезка с A8d-3 (Ч33)
Строки `server.*` и `clock.skew` занимают свои места и идентификаторы, но проверка
сервера не сделана: онлайн — SKIP «this version of the agent does not check the
server yet», offline — SKIP «offline». Код выхода они не меняют, а список из 28
идентификаторов и порядок JSON уже окончательные. Сводка здорового хоста до
A8d-3: на четыре `skipped` больше, чем в сценариях со строками сервера.

### Остальные решения владельца 2026-10-15
- Без репозиториев `check` и `repo check` — OK «no repositories configured» с
  пояснением, как добавить (Ч8): хосту с источниками без репозиториев он не
  нужен. Это сняло противоречие Ч8 и шага 41 QA.
- В сообщение OK строки `repository.storage` вошёл адрес репозитория, пароль в
  нём скрыт `RedactURL` (`***`); так Ч24 «адрес — RedactURL» выполняется и в
  выводе `check`.

## A8d-3: сервер и часы (`server.*`, `clock.skew`)

Решения владельца 2026-10-15: ПР1–ПР10 и ПРВ1–ПРВ5 (`docs/specs/server/agent-probe.feature`).

### Контракт: `AgentService.Probe`
Добавление в proto (`buf breaking` проходит): `rpc Probe(ProbeRequest) returns
(ProbeResponse)`, `ProbeRequest {}`, `ProbeResponse { Timestamp server_time = 1; }`.
Запрос не несёт ничего: агента сервер знает по сертификату (ADR 0009). Сервер
(`agents/AgentProbe`) отвечает показанием `Clock` в момент ответа, не держит ни
хранилища, ни сессий, ни потоков и поэтому не может ничего изменить: не пишет
`last_seen_at`, не открывает сессию, не трогает шаги и объявление, не пишет метрик
и не логирует успешный вызов на INFO и выше. Агент без Register принимается.
Отказ — не поле ответа, а статус `UNAUTHENTICATED` с `ErrorInfo` существующего
перехватчика (ПРВ1): «принят» значит `OK`, а Probe не входит в
`UNAUTHENTICATED_SERVICES`.

### Агент: `internal/serverprobe` и `hostcheck`
`serverprobe.Run` делает три шага и останавливается на первом отказавшем: TCP к
`server.address` (`server.reachable`), рукопожатие mTLS по `tls.ca_file` и
сертификату агента (`server.tls`), вызов Probe (`server.accepted`, `clock.skew`).
Каждое обращение ограничено `--connect-timeout` (по умолчанию 10 с); молчащий
слушатель — ERROR `server.tls` «did not answer within 10s». Пакет собирает факты
(`hostcheck.ServerFacts`), а что они значат, решает `hostcheck` (`server.go`) и
проверяет таблицами. Конфигурацию TLS собирает `cmd/sard-agent/check_server.go`
из файлов, прочитанных так же, как читают остальные проверки; `serverprobe` не
читает файлов. check не вызывает Register и Connect (Ч15, ADR 0038).

Рукопожатие TLS 1.3 для клиента заканчивается раньше, чем сервер судит о его
сертификате, и отказ приходит оповещением при первом чтении. Поэтому после
рукопожатия агент читает до 500 мс (ALPN `h2`, сервер gRPC сразу шлёт SETTINGS);
оповещение «remote error: tls: … certificate» даёт ERROR `server.tls` «did not
accept the client certificate», подсказка `enroll --force`. Так же разбирается
оповещение, пришедшее только в первом вызове.

Разбор ответа Probe: `OK` — принят; `UNAUTHENTICATED` — ERROR с причиной из
`ErrorInfo` домена `sard.dev`, если она состоит из `A-Z`, цифр и `_` (до 40
знаков), иначе без причины; подсказка `sudo sard-agent enroll --force`.
CERT_IDENTITY_MISMATCH и CERT_MISSING показываются так же ERROR, как прочие
причины (ПРВ5). `UNIMPLEMENTED` — WARN «the server is older than this agent;
acceptance and clock were not checked», `clock.skew` — SKIP. Иной код —
ERROR «the server could not tell whether it accepts this agent: code = <код>
(<причина>)»: текст статуса сервера никогда не копируется в вывод.

### Часы (Ч16)
`skew = server_time − (Sent + RTT/2)`, где `Sent` — время хоста перед вызовом
(`deps.clock`), `RTT` — измеренная длительность вызова. `|skew| > 1 мин` — WARN
(60 с — ещё OK), сообщение `the clock of this host is <n>s behind|ahead of the server's`,
подсказка `sudo timedatectl set-ntp true`.

### Зависимости и `--offline`
`server.reachable` → `server.tls` → `server.accepted` → `clock.skew`: ERROR
предыдущей пропускает следующие («not checked: <шаг> failed»); WARN
`server.accepted` пропускает только `clock.skew` (нет времени сервера). Как и
раньше, все четыре пропускаются после отказа `config.load`, `config.names`,
`tls.identity`. `--offline` — SKIP «offline», сервер не спрашивается. Служба
(пользователь службы) без права читать файл TLS — SKIP «needs root». Заглушка
Ч33 (`UnavailableLine`) удалена; сводка здорового хоста — `27 ok, 0 warnings,
0 errors, 2 skipped`.

### Следствия
- Отзыв агента в консоли отзывает и его сертификаты, а CERT_REVOKED проверяется
  раньше AGENT_REVOKED, поэтому check показывает CERT_REVOKED; шаги QA
  `host-check.md` 58 и `agent-start-refusals.md` 6а исправлены.
- Проверка Probe из cron или мониторинга раз в минуту ничего не пишет в базу и
  в журнал сервера.

## A8d-4: подсказка в ошибках шагов и документация оператора

### Подсказка (Ч28)
`internal/pluginhost` (`hint.go`) дописывает к сообщению FAILED шага бэкапа,
восстановления или проверки отдельную последнюю строку
``run `sudo sard-agent check` on the host`` (ошибка `hinted`: `Unwrap` сохраняет
причину для `errors.Is`). Подсказку получают: `restic.ErrNetwork`
(BACKEND_UNAVAILABLE), `restic.ErrWrongPassword`, `restic.ErrPasswordFile` вместе
с `fs.ErrNotExist` (PASSWORD_FILE_MISSING), отказы ключа и доступа
(S3_KEY_REJECTED, STORAGE_ACCESS_DENIED, SSH_KEY_NOT_AUTHORIZED) и ошибка плагина, обёрнутая
`sdk.ErrProgramMissing` (postgresql: нет pg_dump, psql, pg_dumpall). Остальное (нет каталога источника, блокировка,
BUCKET_NOT_FOUND, HOST_KEY_*, `REJECTED`) строки не получает.

- Классы S3_KEY_REJECTED и STORAGE_ACCESS_DENIED определяет та же таблица, что у
  `repo add` и `repo check` (Р34): `repoinit.RemoteReason`. SSH_KEY_NOT_AUTHORIZED
  в сообщении шага отдельно не отличается: строка ssh «Permission denied
  (publickey)» входит в класс «доступ отказан». Ограничение: если restic не
  печатает строку ssh в свою фатальную строку (OQ-155), подсказки не будет.
- Программа плагина распознаётся по `errors.Is(err, sdk.ErrProgramMissing)`:
  аддитивная ошибка-признак в `plugins/sdk` (Apache-2.0), которую плагин
  оборачивает через `%w`. Текст сообщения плагина `pluginhost` не знает и не
  сравнивает; прежнее решение (сопоставление начала текста, «единственный способ,
  не расширяя SDK») отменено как нарушающее границу: хост знал слова одного
  плагина. Текст сообщений postgresql не изменился (слово «not found» теперь
  текст самой ошибки-признака). Связь закреплена тестами `plugins/postgresql`
  (`errors.Is` для pg_dump, psql, pg_dumpall) и `pluginhost`.
- Сообщение шага перестало быть однострочным только для этих причин; тесты F1,
  требовавшие одной строки у сообщения плагина, теперь проверяют плагинную часть
  и последнюю строку подсказки.
- Сервер и web не менялись: консоль показывает текст как есть (ЧВ3).

### Документация (Ч30)
`docs/operator/09-troubleshooting.md`: раздел «Проверка хоста агента:
sard-agent check» с подразделом `### ` на каждый идентификатор (заголовок —
идентификатор в обратных кавычках; якорь GitHub — идентификатор без точек:
`#repositorypassword`). 29 якорей: 28 строк `check` и `repository.integrity` у
`repo check --read-data`. Ручные шаги раздела «Агент не подключается» заменены
командой `check`. `agent-install.md`, `agent-host-setup.md`, `02-install.md`,
`05-agents.md`, `05a-storage.md` говорят выполнить `sudo sard-agent check` после
установки и `repo add`; итог `postinstall.sh` называет её последним шагом.

Тест `cmd/sard-agent/check_docs_test.go` читает руководство и проверяет, что
у каждого идентификатора ровно один якорь и что каждая ссылка
`09-troubleshooting.md#…` в спецификации указывает на существующий якорь. Якоря
считаются как у GitHub (нижний регистр, без знаков препинания); проверка
ссылок в CI (lychee с `--include-fragments`) подтверждает то же независимо.
