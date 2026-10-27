<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-26 — S11: привязка секретов и защищённое подключение PostgreSQL

Ветка: `ccr-0f57882a-psrhuo` (задача — `feat/s11-secret-binding`). Основа:
main `545331b0`.

## Фаза 1 — исследование (код не менялся)

### Что есть (проверено чтением кода)

- **Хранение секрета.** `secrets: {имя: путь}` — плоская карта строк
  (`agent/internal/config/config.go:31`, фрагмент `overlay.go:90`).
  `secret set` пишет файл значения и фрагмент `secrets: {имя: путь}`
  (`agent/cmd/sard-agent/secret_set_run.go:70`, `hostsetup/layout.go:76`).
  Конфиг и фрагменты читаются с `KnownFields(true)` (`config.go:126`,
  `overlay.go:273`): агент N−1 не запустится с конфигом, в котором есть новый
  ключ.
- **Как плагин получает секрет.** Только по имени: `sdk.Host.Secret(name)`
  (`agent/plugins/sdk/plugin.go:110`). Хост не знает цели подключения.
  Реализации: шаг (`pluginhost/source.go:252`), восстановление
  (`pluginhost/restore_run.go:227`), откат прерванного восстановления
  (`pluginhost/journal_recovery.go:245`), песочница верификатора — только
  `sdk.SandboxPassword` (`verifier/sandbox.go:131`).
- **Где плагин postgresql читает пароль** — четыре места: `prepare.go:88`
  (бэкап, через `tools.go:39`), `fingerprint.go:79` (отпечаток V2),
  `restore_run.go:119` (R2), `restore_recover.go:74` (откат).
- **Параметры подключения** уже собираются в одном месте:
  `target.conninfo` (`plugins/postgresql/conninfo.go:33`) →
  `pgclient.Conninfo`; пароль — в `PGPASSWORD`, все `PG*` из окружения
  агента вычищаются (`pgclient/pgclient.go:33`). `require_auth` и
  `channel_binding` не задаются.
- **TLS.** Схема: `tls_mode ∈ {disable, require, verify-full}` (allow/prefer
  нет), сокет — только `disable`; по умолчанию `require` для TCP
  (`conninfo.go:57`, `schema.json:77`). `tls_root_cert` — путь на хосте, но
  **задаётся сервером** в конфиге источника (`schema.json:97`), так же как
  `pg_dump_path` / `pg_restore_path`.
- **Сервер.** Своей схемы postgresql нет: схема приходит от агента в
  Register. При сохранении источника `password_ref` сверяется с
  `agents.secret_names` (`server/.../runs/ConfigCheck.kt:24-33`, сообщение
  `unknown secret "x"`, 422 `invalid_config`). Консоль показывает это как
  ошибку поля; кодов причин шага (`SECRET_INVALID`) веб не знает — выводит
  `step.message` как есть (`web/src/components/StepPanel.tsx:25-35`).
- **Самобэкап F6.** Образ `sard-self` объявляет `secrets: sard-db` без
  привязки (`deploy/agent/Dockerfile:108`), сервер шлёт источник с
  `host=postgres` (по TCP в сети compose) и `tls_mode=disable`
  (`SelfBackupProperties.kt:54`). Правило «disable к сетевой цели — отказ»
  без привязки в образе сломает самобэкап.
- **Клиенты libpq.** Образ агента `-pg18` и e2e — postgres:18; пакет
  .deb/.rpm только `suggests: postgresql-client` (`deploy/agent/nfpm.yaml:32`),
  на хостах — версия дистрибутива. e2e восстанавливает в PostgreSQL 14 и 18.

### Выводы, влияющие на решение

- `require_auth` надо передавать **в строке подключения**, не через
  `PGREQUIREAUTH`: libpq < 16 молча игнорирует неизвестную переменную, но
  отвергает неизвестный параметр строки («invalid connection option»)
  до подключения — отказ без пакетов к серверу.
- `require_auth=scram-sha-256` отвергает и `trust`/`peer` — существующие
  источники через сокет с peer сломаются, если включить его и для сокета.
- Утечка пароля зависит от сервера (host:port), а не от базы: база в
  привязке — это минимальные права, не защита пароля.
- Поддельный сервер на `localhost:5432` может поднять любой локальный
  пользователь, пока PostgreSQL не запущен — для localhost TLS можно не
  требовать, но SCRAM — нужно.

### Контрольная точка 1 — решения владельца (2026-10-26)

1. Решение «можно ли отдать секрет» принимает хост агента (`Host.SecretFor`). — да.
2. Привязка — отдельный файл, который агент N−1 не читает, а не ключ в
   `agent.d` (владелец выбрал второй вариант): `secret-bindings/<имя>.yaml`
   рядом с главным конфигом.
3. Синтаксис цели — как предложено: `host[:port][/db]`, IPv6 в скобках, сокет
   `/dir[:port[/db]]`, `*` — только для порта и базы.
4. Разрешения — флаги на команду, общие для всех её `--target`. — да.
5. SCRAM по TCP и localhost, сокет — по привязке, `channel_binding=require` с
   TLS. — да.
6. «Ломать текущие установки можно, делать как правильно»: к серверу в сети
   по умолчанию verify-full, `require` и `disable` — только по `--allow-tls`.
7. CA в привязке (`--ca-file`); пути, которые задаёт сервер, — отдельный
   пункт: OQ-262.
8. libpq < 16: сеть — отказ с подсказкой, сокет — работает. — да.
9. Секретов без привязки нет, старых установок нет: переходного режима и
   предупреждений нет, привязка обязательна.
10. Самобэкап: привязку `sard-db` добавить.
11. Register: поле `secret_names` можно убрать, N−1 нет.

## Фаза 2 — привязка в агенте и подключение PostgreSQL

### Сделано
- SDK (Apache-2.0): `sdk.Target`, `sdk.Grant`, `sdk.Secret`,
  `sdk.BindingError` / `ErrSecretNotAllowed`; `Host.SecretFor` и
  `RecoveryHost.SecretFor` вместо `Secret` (`agent/plugins/sdk/secret.go`).
- `agent/internal/secretbind`: шаблон цели, файл привязки (строгий YAML),
  чтение с проверкой прав (не пишут группа и прочие, владелец root или
  агент, без ссылок).
- `pluginhost.Secrets`: `Allow` / `SecretFor`: сначала привязка, файл значения —
  только после неё. Хосты шага, восстановления и отката передают имя плагина.
  Песочница верификатора отдаёт свой пароль без привязки.
- Плагин postgresql: одна сборка параметров `target.secured` →
  `target.conninfo`, общая для проверки, отпечатка, pg_dump, pg_dumpall,
  восстановления и отката. В ней `require_auth`, `channel_binding`,
  умолчание verify-full и `sslrootcert`; подсказки к отказам libpq; причина
  восстановления `SECRET_NOT_ALLOWED`.
- `sard-agent secret set --plugin --target … [--auth] [--allow-tls]
  [--ca-file] [--allow-no-channel-binding]`, `PLUGIN_UNKNOWN`; `secret list`
  (BOUND_TO, `binding` в `--json`); `secret remove` удаляет привязку.
- Спецификация `docs/specs/agent/secret-binding.feature`, черновик ADR
  `docs/adr/00XX-draft-secret-binding.md`, OQ-262.

### Проверено
- Сценарий 1 (цель вне привязки → отказ до подключения): `binding_test.go`,
  `restore_binding_test.go`, `pluginhost/secrets_test.go`. Ни одна программа
  не запускается, файл секрета не читается.
- Сценарий 2 (сервер просит md5 или password): с настоящим psql 16 этого
  контейнера и поддельным сервером протокола
  (`libpq_integration_test.go`, `go test -tags libpq`): libpq отказывается,
  сообщение `PasswordMessage` не приходит. С `--auth scram-sha-256,md5`
  приходит. На сокете без `--auth` — приходит.
- Сценарий 3 (TLS): `binding_test.go`, матрица tls_mode × `--allow-tls` ×
  сеть, localhost, сокет.
- Сценарий 4 (libpq < 16): проверена только подсказка по тексту ошибки
  libpq («invalid connection option "require_auth"»). Что libpq < 16
  отвергает параметр строки до подключения, я **полагаю** по документации
  libpq. Клиента старше 16 в контейнере нет.
- Сценарий 7 (значение не утекает): `noLeak` в тестах плагина, проверки
  `assertValuesHidden` в тестах команды; `BindingError` не содержит пути.

### Гейт
- `./scripts/gate.sh sdk fast` — PASSED.
- `./scripts/gate.sh agent fast` — PASSED (lint, покрытие ≥ 80 %, CRAP ≤ 6,
  интеграционные тесты с restic, сборка стенда e2e).
  По дороге найдена и исправлена гонка в `session.readUntil`: отменённый шаг
  мог прийти как «psql session ended» вместо своей причины.
  `TestACancelledFingerprintReturnsTheCause` падал 5 раз из 300 уже на
  `main` 545331b0, после исправления — 0 из 300 (`-race -tags e2e`).
- Мутации: полный прогон агента по 12 шардам — в CI; локально — только
  изменённые файлы (`secretbind/*`, `pluginhost/secrets.go`, `secure.go`,
  `conninfo.go`, `secret_set_run.go`, `secret_list_run.go`). Счёт 0.951
  (239 убито, 16 выжило). Выжившие — эквивалентные мутанты (параметры
  tabwriter, право в `OpenFile` без `O_CREAT`, `defer Close`) и строки,
  которые были до S11 (возвраты `code` после разбора флагов).
- `go test -tags libpq -run Libpq ./plugins/postgresql/` — PASS на psql 16.15.
  В гейт не входит (тест требует клиент PostgreSQL на хосте);
  `scripts/gate.sh` правит владелец.

### Не сделано в фазе 2 (фаза 3)
- e2e: конфиги стенда объявляют секреты вручную — нужны файлы привязки
  (`PostgresStand.kt`, `VerificationPostgresTest.kt`,
  `StepLogRedactionTest.kt`), а в образе sard-self — привязка `sard-db`.
  Без этого e2e с PostgreSQL сейчас упадут.
- Register, сервер, консоль, `check` (A8d), документация оператора.

## Фаза 3 — Register, консоль, check, sard-self, e2e, документация (2026-10-27)

### Сделано
- Proto: `RegisterRequest.secrets` (13, `SecretInfo`: имя, плагин, цели,
  `auth`, `allow_tls`, `allow_no_channel_binding`, `binding_problem`).
  Поле 8 `secret_names` **осталось**. Владелец разрешил удалить его, но гейт
  proto (`buf breaking` FILE против `main`) запрещает удаление поля, а гейт
  ослаблять нельзя. Удаление требует решения владельца об исключении.
- Агент: `app.Agent.Bindings`, `secretInfos` (без значения, пути и
  `ca_file`); `sard-agent check` — строка `secret.bound` по каждому секрету;
  подсказки `secret.readable` и `config.references` включают `--plugin` и
  `--target`.
- SDK: `sdk.TargetKeyword` = `x-sard-target`; схемы postgresql (бэкап и
  восстановление) помечают `host`, `port`, `database`.
- Сервер: `SecretEntry` в `AgentSnapshot`, правила снимка (имя, плагин, до 64
  целей и методов, тексты до 1024 байт без NUL), `agents.secret_bindings`
  (миграция `V202610271000`), `SecretBindings` — та же грамматика целей, что у
  агента; `SourceView.secretWarnings`, REST `Source.secretWarnings`
  (обязательное поле).
- Веб: на странице источника — предупреждение со строками сервера и командой
  для хоста; моки и схемы postgresql в моках обновлены.
- sard-self: образ кладёт `secret-bindings/sard-db.yaml` (postgresql,
  `postgres:5432/sard`, `allow_tls: disable`); как поменять цель —
  `docs/operations/self-agent.md`.
- e2e: `AgentHost(bindings = …)`, `AgentHost.binding(...)`, привязка `pg-app`
  в `PgAgent` к серверам стенда; новый `SecretBindingTest` (цель вне привязки
  и отсутствие подключений на сервере, предупреждение консоли, md5,
  tls_mode disable без разрешения, секрет без привязки, бэкап с привязкой).
- Документация: `docs/plugins/postgresql.md` (привязка, аутентификация и TLS,
  сообщения), `docs/operations/agent-host-setup.md`, `docs/operator/05-agents.md`,
  `09-troubleshooting.md` (`secret.bound`), `docs/operations/self-agent.md`,
  ADR-черновик, спецификация.

### Проверено
- `./gradlew :server:test` для `SecretBindingsTest` (mutflow),
  `SnapshotRulesTest`, `SourcesIntegrationTest`, `RegisterIntegrationTest`,
  `SardServerIntegrationTest` (OpenAPI) — зелёные (Testcontainers, Docker
  поднят в контейнере сессии).
- `./scripts/gate.sh web` — PASSED. `make breaking-proto`, `buf lint` — без ошибок.

### Гейты фазы 3
- `./scripts/gate.sh server` (полный, с mutflow) — PASSED.
  - По дороге mutflow нашёл выживших мутантов `SecretBindings` внутри чужого
    теста `W2MutationTest`: `Sources` вызывал проверку привязок при каждом
    просмотре источника.
  - Предупреждения перенесены в отдельный компонент `SourceWarnings`, который
    вызывает слой API. `Sources` хранит и проверяет источник, как раньше.
- `./scripts/gate.sh agent fast` — PASSED; `proto`, `gen`, `sdk fast`, `web` — PASSED.
- Мутации агента на новых файлах (`hostcheck/secret.go`, `app/app.go`,
  `secretbind/store.go`) — 0.94. Выжили эквивалентные мутанты: права в
  `OpenFile` без `O_CREAT`, `defer Close`.
- Помехи окружения (не код S11):
  - Хук остановки `scripts/claude/gate-changed.sh` запускает гейт параллельно с
    уже идущим. Это портило `agent/.cover.out` (вставка NUL, «token too long»),
    `server/build/test-results` (`EOFException`) и классы тестов
    (`NoClassDefFoundError`); то же наблюдение, что в OQ-036.
  - Чистые прогоны получились, когда ход не завершался до конца гейта.

### Не проверено: e2e
Образы стенда не собираются в этой сессии.
- Maven Central отвечал 429 из сборки `server-jar`. Обход: jar собран
  локально (`./gradlew :server:bootJar`).
- Политика сети окружения отвечает 403 на `deb.debian.org` и не резолвит
  `apt.postgresql.org`. Без них не собираются образы агента на `postgres:18` и
  `postgres:14` (`-pg18`, `sard-agent-pg18:e2e`, `sard-agent-pg14:e2e`).

Поэтому сценарии 5 (бэкап, R2 и V5 с привязанным секретом) и 6, а также
`SecretBindingTest` и `PostgresqlSourceTest` с клиентом 14 **не запускались**.
Для них нужны разрешённые хосты в Network access окружения или прогон в CI.
Для сценария 4 стоит посмотреть e2e «pg_dump 14 против сервера 18»: в образе
postgres:14 libpq из PGDG может быть новее 16 — тогда шаг падает по версиям,
как раньше. Если libpq окажется 14, шаг упадёт на `require_auth`, и тест надо
будет поправить под новое сообщение.

## Ревью PR #109 (2026-10-27)

markdownlint упал на MD040 (блок без языка в `docs/plugins/postgresql.md`).
Владелец остановил прогоны CI до исправлений. Замечания CodeRabbit проверены по
коду, исправлено:

- `secret list` и строка `secret.bound` у `check` читают привязку так же, как
  агент: `secretbind.Store` и одно открытие без перехода по ссылке
  (`hostDeps.readBinding` = `secretbind.ReadFile`). Раньше `secret list` не
  проверял права и владельца файла, а `check` переходил по ссылке.
- При ошибке `config.load` строка `secret.bound` тоже идёт как SKIP, а не
  пропадает.
- Подсказка для секрета главного конфига больше не предлагает `secret set`,
  который отказывает с `DEFINED_IN_CONFIG`. Теперь она называет файл привязки,
  который пишется вручную.
- Цель в предлагаемой команде `sudo sard-agent secret set` берётся в кавычки
  оболочки, если в ней есть хоть один символ вне безопасного набора (агент:
  `sdk.BindingError`; сервер: предупреждение источника). Цель приходит из
  конфига, который прислал сервер.
- В привязке не больше 64 целей и 64 методов `auth`, как принимает сервер:
  больше `secret set` не запишет, а агент такой файл не примет.
- Dockerfile: комментарий перенесён внутрь heredoc (hadolint DL1000).
- Обновление с томом `sard_sard-self-config`: Docker не копирует в
  существующий том новый файл `secret-bindings/sard-db.yaml`. Шаг описан в
  `docs/operator/07-upgrade.md` и `docs/operations/self-agent.md`.

Docs-проверки прогнаны локально по всем файлам: markdownlint (0 ошибок),
`check-sudo-u.sh`, ссылки. Ссылки проверены своим офлайн-скриптом, потому что
lychee в окружении нет.

Гейты после правок: agent fast и sdk fast — PASSED. Мутации изменённых файлов:
`secretbind/binding.go` и `hostcheck/secret.go` — 0.98 вместе; единственный
выживший был эквивалентным, лишнее условие убрано. `sdk/secret.go` — 1.00.
`secret_list_run.go` — 0.84; выжившие в строках, которых правки не касались
(ширины `tabwriter`, ранний `return`). Сервер: тесты зелёные, mutflow без
выживших. В двух полных прогонах по одному разу упали тесты кода, которого
правки не касались. Оба проходят отдельно; это гонки:
- `FirstStartRestartIntegrationTest`: чужая DEBUG-строка между строками баннера;
- `SchedulerIntegrationTest`: очистка удаляет источник, на который планировщик
  только что записал `overdue_episodes`.

## Второй раунд ревью и слияние с main (2026-10-27)

На `8b986a29` не запускались CI и Docs: после слияния PR #107 (DB2) у PR
появился конфликт с `main`. `main` влит (`98000f32`). Конфликты были только в
документах. OQ-261 занял DB2, поэтому вопрос S11 о путях на хосте стал OQ-262.
Самобэкап после DB2 входит ролью `sard_backup`, но цель та же
(`postgres:5432/sard`), и привязка образа подходит.

Второй раунд CodeRabbit, исправлено:
- Восстановление и откат прерванного восстановления спрашивают секрет и для
  служебной базы `postgres` (`securedFor`). Раньше пароль, разрешённый только
  для новой базы, уходил и в `postgres`: через неё создаётся и удаляется база.
- При включённом TLS в строке подключения стоит `gssencmode=disable`. libpq
  по умолчанию сначала пробует шифрование GSS, а у такого соединения нет TLS,
  то есть ни проверки сертификата, ни channel binding.
- `check` и `secret list` (запуск от root) читают привязку как служба
  (`secretbind.AsUser`): файл root `0600` служба не прочитает, и теперь это ERROR.
- `secret set` переписывает привязку с теми же байтами, если агент её не примет
  (права, владелец). Раньше команда отвечала `unchanged`.
- `secret set` пишет привязку раньше значения. Шаг, попавший между записями,
  отправит старое значение только туда, куда разрешает новая привязка, а новое
  значение не встретит старую привязку.

Гейты agent fast и sdk fast — PASSED. Мутации `store.go` и `secure.go`: 0.93.
Выжившие в новом коде убиты или убраны. Проверка сокета в условии GSS была
лишней: схема не даёт TLS сокету. Остальные выжившие — прежние эквивалентные
мутанты `ReadFile`.
