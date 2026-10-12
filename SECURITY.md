# Security policy · Политика безопасности

[English](#english) · [Русский](#русский)

## English

### Supported versions

Sard is in early development and ships pre-releases only. Fixes go to `main`
and into the next release; older releases are not patched. Please check that
the issue reproduces on the latest release or on `main`.

### How to report

Do not open a public issue, discussion or pull request for a vulnerability.

1. Preferred: GitHub private vulnerability reporting,
   [Report a vulnerability](https://github.com/Artur-Abalov/Sard/security/advisories/new).
2. Or email the maintainer: `Abalov.artur.artashesi@gmail.com`, subject
   starting with `[Sard security]`.

You may write in English or Russian.

### What to send

- the affected component (server, agent, CLI, web console, release
  artifacts) and version or commit;
- what an attacker needs in advance (network position, an account, access to
  an agent host) and what they gain;
- steps to reproduce or a proof of concept, and logs with secrets removed;
- whether and when you plan to publish.

### What to expect

The maintainer is one person, so the times below are targets, not guarantees:

- acknowledgement within 5 working days;
- an assessment (accepted or not, and why) within 14 days;
- a fix or mitigation within 90 days of the report for an accepted issue,
  sooner when it is severe.

### Trust model: what counts as a vulnerability

Sard is built so that the server is a control plane, not a key holder
([ADR 0008](docs/adr/0008-crypto-provider.md)). Repositories, storage
credentials, secrets and scripts are defined on the agent host and referenced
by name only; the agent runs only what is allowed locally: built-in plugins,
repositories from its own configuration and scripts from its local allowlist.
The agent dials out to the server over mutual TLS; hosts open no inbound ports.

In scope, for example:

- the server, or someone controlling it, making an agent run a command,
  script or plugin action its host did not allow, or read or write outside
  what its configuration names;
- a storage key, repository password, secret value or credential reaching the
  server, the database, the console, notifications or logs;
- connecting as an agent or as the server without valid mutual TLS
  credentials, or getting a certificate without enrollment;
- bypassing login or authorization in the REST API or the console;
- installing an agent or release artifact that is not signed with the Sard
  release key while verification reports success.

Usually out of scope:

- an attacker who already has root on an agent host: storage credentials
  live there by design;
- the REST API answering plain HTTP on `127.0.0.1:8080`: it is meant to sit
  behind a TLS proxy ([docs/operator/10-security.md](docs/operator/10-security.md));
- vulnerabilities in restic, PostgreSQL or other dependencies that Sard does
  not make worse: report them upstream, and to us if Sard's use exposes them;
- denial of service by sheer traffic volume, and missing hardening without a
  demonstrated impact.

### Disclosure

We prefer coordinated disclosure: we agree on a publication date with you,
publish a GitHub security advisory with the fixed release and credit you
unless you ask us not to. If no fix is ready 90 days after the report, you
are free to publish.

## Русский

### Поддерживаемые версии

Sard на ранней стадии разработки, выходят только предварительные релизы.
Исправления попадают в `main` и в следующий релиз; старые релизы не
исправляются. Проверьте, что проблема воспроизводится на последнем релизе
или на `main`.

### Как сообщить

Не открывайте публичный issue, обсуждение или pull request об уязвимости.

1. Предпочтительно: приватное сообщение об уязвимости на GitHub,
   [Report a vulnerability](https://github.com/Artur-Abalov/Sard/security/advisories/new).
2. Или письмо сопровождающему: `Abalov.artur.artashesi@gmail.com`, тема
   начинается с `[Sard security]`.

Писать можно на русском или английском.

### Что прислать

- затронутый компонент (сервер, агент, CLI, веб-консоль, артефакты релиза)
  и версию или коммит;
- что атакующему нужно заранее (положение в сети, учётная запись, доступ к
  хосту агента) и что он получает;
- шаги воспроизведения или proof of concept, журналы без секретов;
- собираетесь ли вы публиковать находку и когда.

### Сроки ответа

Проект сопровождает один человек, поэтому сроки ниже — цель, а не гарантия:

- подтверждение получения — в течение 5 рабочих дней;
- оценка (принято или нет и почему) — в течение 14 дней;
- исправление или обходной путь для принятой уязвимости — в течение 90 дней
  после сообщения, для серьёзных — раньше.

### Модель доверия: что считается уязвимостью

Сервер Sard — плоскость управления, а не хранитель ключей
([ADR 0008](docs/adr/0008-crypto-provider.md)). Репозитории, учётные данные
хранилищ, секреты и скрипты описываются на хосте агента, а сервер ссылается
на них только по имени; агент выполняет только то, что разрешено локально:
встроенные плагины, репозитории из своего конфига и скрипты из локального
списка разрешённых. Агент сам подключается к серверу по взаимному TLS;
входящие порты на хостах не открываются.

Уязвимость, например:

- сервер или тот, кто им управляет, заставляет агента выполнить команду,
  скрипт или действие плагина, которое хост не разрешил, или читать и писать
  за пределами того, что названо в его конфиге;
- ключ хранилища, пароль репозитория, значение секрета или учётные данные
  попадают на сервер, в базу, в консоль, в уведомления или в журналы;
- подключение в роли агента или сервера без действующих сертификатов
  взаимного TLS или получение сертификата в обход регистрации;
- обход входа или авторизации в REST API или консоли;
- установка агента или артефакта релиза, не подписанного ключом релизов
  Sard, при том что проверка сообщает об успехе.

Обычно не уязвимость:

- атакующий уже имеет root на хосте агента: учётные данные хранилища лежат
  там по замыслу;
- REST API отвечает по обычному HTTP на `127.0.0.1:8080`: он рассчитан на
  работу за TLS-прокси ([docs/operator/10-security.md](docs/operator/10-security.md));
- уязвимости restic, PostgreSQL и других зависимостей, которые Sard не
  усугубляет: сообщайте их авторам, а нам — если Sard делает их достижимыми;
- отказ в обслуживании одним объёмом трафика и отсутствие защитной настройки
  без показанного ущерба.

### Раскрытие

Мы за согласованное раскрытие: договариваемся с вами о дате публикации,
публикуем GitHub security advisory вместе с исправленным релизом и
благодарим вас по имени, если вы не против. Если через 90 дней после
сообщения исправления нет, вы вправе опубликовать находку.
