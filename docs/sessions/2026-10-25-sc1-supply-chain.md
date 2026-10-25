<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-25 — SC1: цепочка поставки

Ветка `ccr-2627484e-s43oz7`: её назначила сессия. В задаче указана
`feat/sc1-supply-chain`; переименовать можно по решению владельца.

## Фаза 1 — исследование (СТОП, ждёт ревью)

Два агента только на чтение (CI и релиз; входы сборки), HEAD `c3646a8`.
«Проверил» — видел в файле; «полагаю» — вывод, не проверенный запуском.
Окружение: github.com отвечает 403 через прокси, демона Docker нет —
релиз, тег на форке и pull образов отсюда не выполнить; проверки 3, 4, 7
возможны только в GitHub владельца.

### Что есть сейчас (проверил)

Действия CI
- `release.yml` уже закреплён по SHA с комментарием версии, `permissions: {}`
  сверху, права по задачам, `persist-credentials: false`, подпись — только
  в окружении `release` (`:246`), секреты minisign — только в задаче `sign`
  (`:261-262`), ключ во временном файле с `umask 077` (`release-sign.sh`).
- `ci.yml`, `docs.yml`, `qodana_code_quality.yml` — по тегу
  (`actions/checkout@v7`, `docker/setup-*-action@v3`,
  `crazy-max/ghaction-github-runtime@v3`, `lycheeverse/lychee-action@v2`,
  `JetBrains/qodana-action@v2026.1`, `actions/checkout@v3`); в `ci.yml`
  смешаны `upload-artifact@v7` и `@v4` (`:375`, `:381`).
- `qodana_code_quality.yml`: нет `permissions` сверху, у задачи
  `contents: write, pull-requests: write, checks: write` на `pull_request`;
  `checkout@v3` оставляет токен в `.git/config`; секрет `QODANA_TOKEN_*`
  доступен PR из того же репозитория; нет SPDX-заголовка.
- `pull_request_target` и `workflow_run` нет нигде; `${{ github.event.* }}`
  внутри `run:` нет.
- Бота обновлений нет (`.github/` — только 4 workflow).

Образы и зависимости
- Все `FROM` — по тегу: `deploy/agent/Dockerfile:29,32` (`ubuntu:24.04`),
  `deploy/server/Dockerfile:29,44,64,99` (`eclipse-temurin:25-jre/jdk`,
  `node:24-slim`), `test/e2e/*/Dockerfile`, `test/packages/*.Dockerfile`.
- По digest — только PostgreSQL, из одного места `deploy/postgres.env`
  (ADR 0068, копии сверяет `DeployPostgresTest`). Это образец для D24.
- По тегу в e2e: `E2e.kt:73` `postgres:$major`, `GarageS3.kt:123`
  `dxflrs/garage:v2.1.0`, `Makefile:155-156` `postgres:18`/`postgres:14`.
- Имя образа `ghcr.io/artur-abalov/...` зашито в `offline-archive.sh`,
  `deploy/docker-compose.yml:40,113`, `deploy/.env.example` и документации;
  `release.yml:325-326` выводит его из владельца репозитория — D24 не
  соблюдён.
- Gradle: нет `distributionSha256Sum` в `gradle-wrapper.properties`, нет
  dependency locking, нет `gradle/verification-metadata.xml`.
- Go: `go.sum` есть у всех модулей с зависимостями; `GOFLAGS=-mod=readonly`
  и `go mod verify` не используются (полагаю, `go build` в модульном режиме
  и так не меняет go.sum — проверю в фазе 2).
- npm: `package-lock.json` v3, везде `npm ci`; но `test -d web/node_modules ||`
  в `Makefile`/`gate.sh` пропускает проверку, если каталог уже есть (в CI
  каталог чистый — для CI не дыра).
- apt/dnf-пакеты (`minisign`, `gnupg`, `rpm`, `openssh-client`) — без версий.
- Node, JDK в CI — только мажорная версия.

restic
- `scripts/fetch-restic.sh`: версия, sha256 на архитектуру и отпечаток
  ключа — в `agent/internal/restic/restic-version`, ключ — в
  `restic-signing-key.asc`. sha256 сверяется всегда дважды (`:82-83`),
  подпись GPG `SHA256SUMS.asc` с проверкой подписанта по `VALIDSIG`
  (`:58-72`) — только при `REQUIRE_SIGNATURE=1`, а его ставит лишь
  `release.yml:92`. В CI сборка проверяет только sha256.
- Других скачиваемых при сборке бинарников нет: инструменты собираются из
  `tools/go.mod` (закреплены go.sum), Gradle — через wrapper (без суммы).

Релиз и офлайн
- minisign подписывает только `SHA256SUMS` пакетов агента.
- `SHA256SUMS.server` (офлайн-архивы образов, compose, env) **не подписан**
  (`release.yml:437`), а `docs/operator/11-offline.md` проверяет только его.
  То есть офлайн-установка сейчас не проверяет подлинность образов вовсе.
- Образы в GHCR без подписи, SBOM и provenance. Quickstart ничего не
  проверяет.
- ADR 0043 уже отверг cosign без ключа (`:121`).
- Порядок задач: `sign` → `publish` → `image` → `server-assets`. Образ несёт
  `SHA256SUMS.minisig`, поэтому строится после `sign`; значит, подпись
  образов и `SHA256SUMS.server` требует ключей **после** `sign`.

Время CI на PR — около 1–1,5 ч, критический путь — мутации (до 90 мин).
Быстрые проверки (pin-check, actionlint, zizmor, govulncheck) добавятся в
параллельную задачу и путь не удлинят (полагаю).

### Вопросы к контрольной точке 1

Трудные первыми; рекомендация — первый вариант.

1. **Подпись образов.** (а) cosign с ключом: ключ — в секретах окружения
   `release`, `cosign.pub` — в `deploy/release/` рядом с `sard-release.pub`;
   проверка `cosign verify --key cosign.pub`. Запись в Rekor: включить
   (бандл с отметкой журнала лежит в реестре, онлайн-проверка полная) или
   выключить (`--tlog-upload=false`; тогда проверка требует
   `--insecure-ignore-tlog` — в документации это выглядит плохо).
   (б) Без cosign: minisign подписывает манифест digest образов — один
   ключ, но `cosign verify` и политики допуска в Kubernetes не работают.
   (в) (а) + дополнительно keyless. Рекомендую (а) с записью в Rekor;
   офлайн — п. 2.
2. **Офлайн-проверка образа.** `docker save` теряет подписи из реестра.
   Предложение: подписать minisign `SHA256SUMS.server` и манифест образов
   (`images.json`: имя, тег, digest манифеста, ID конфигурации на
   архитектуру); после `docker load` проверка сравнивает
   `docker image inspect --format {{.Id}}` с манифестом — нужен только
   minisign, который у клиента уже есть. cosign-подписи и SBOM — тоже в
   архиве для тех, кто хочет cosign офлайн.
3. **Второе подтверждение релиза.** Ключи нужны и в `sign`, и после
   `image` (cosign, `SHA256SUMS.server`). Варианты: (а) вторая задача в
   окружении `release` — второе ручное подтверждение; (б) задачи `image` и
   `server-assets` сами в окружении `release` (пушат и подписывают сразу;
   одно подтверждение, но шире круг задач с ключом). Рекомендую (а).
4. **Бот.** Renovate (приложение GitHub): закрепляет и обновляет SHA и
   digest сам, regex-менеджеры для `restic-version`, `postgres.env`,
   `GarageS3.kt`, суммы Gradle wrapper; группировка и расписание гибче.
   Dependabot: свой, без внешнего приложения, но не умеет файлы вне
   стандартных манифестов. Рекомендую Renovate. Оба не обновляют
   `verification-metadata.xml` — нужна задача, которая на PR бота
   перегенерирует его и пушит коммит (права `contents: write` только на
   ветках `renovate/*` из этого репозитория). Допустимо, или регенерация
   руками?
5. **Сканеры и порог.** govulncheck (agent, cli, sdk) — на PR, блокирует
   любую достижимую уязвимость. Зависимости сервера и web и образы — syft
   (SBOM) + grype (по этому же SBOM: сканируем то, что публикуем). На PR —
   только grype по lock-файлам, если менялись файлы зависимостей; образы —
   по расписанию на main и на релизе. Порог: исправимые critical и high
   блокируют; неисправимые — предупреждение. Исключения —
   `security/vuln-exceptions.yaml` (id, пакет, обоснование, срок ≤ 90
   дней); скрипт падает на просроченном или без обоснования. Альтернатива —
   Trivy (один инструмент, но в 2026 году был взлом тегов его действия —
   полагаю, проверю). Куда сообщать о красном ночном скане — только
   красный запуск или issue (`issues: write` в этой задаче)?
6. **SBOM.** CycloneDX JSON (сканеры читают его, полагаю — и формат ФСТЭК
   основан на CycloneDX); syft. На образ сервера, образ агента (обе
   архитектуры) и пакеты агента (бинарь Go + restic); в `SHA256SUMS`,
   значит под minisign; к образу — `cosign attest`. SPDX — только если
   нужен заказчикам.
7. **Provenance.** `actions/attest-build-provenance` — keyless, Sigstore
   публичный; клиенту не обязательно. Включить как дополнительное, или не
   включать вовсе, чтобы не обещать проверку, которая в закрытом контуре
   не работает?
8. **Qodana.** Сузить до `contents: read` + `checks: write`, закрепить по
   SHA, `persist-credentials: false`, SPDX — или удалить workflow?
9. **apt без версий** (`minisign`, `gnupg`, `openssh-client` в образе).
   Базовый образ по digest, версии пакетов не закрепляем (без
   snapshot-зеркала сборка начнёт падать при удалении версии) — принятый
   риск в ADR. Согласны?
10. **Инструменты CI.** actionlint, zizmor, govulncheck — из `tools/go.mod`
    или по SHA/sum; cosign, syft, grype — бинари релизов с sha256 в одном
    файле закреплений (как `restic-version`), без раздувания go.sum.
    Согласны?
11. **Тестовый релиз.** Предрелизный тег в основном репозитории
    (`v0.1.0-rc.0`? — не путать с `rc.1`) или форк с тестовыми ключами?
    Тег и подтверждение окружения — только у владельца; установку
    Renovate — тоже.

### Решения владельца на контрольной точке 1 (2026-10-25)

1. Подпись образов — (а) cosign с ключом, запись в Rekor.
2. Офлайн — minisign над `SHA256SUMS.server` и манифестом образов: да.
3. Ключи после `image` — (а) вторая задача в окружении `release`, второе подтверждение.
4. Бот — Renovate; перегенерация `verification-metadata.xml` автоматизируется.
5. Сканеры — красный ночной скан открывает issue; Trivy изучить (ниже).
6. SBOM — CycloneDX JSON, syft: да.
7. Provenance — да, как дополнительное.
8. Qodana — на паузу (не работает пока нормально).
9. apt без версий — «а нельзя без риска?» (ответ ниже, решение ждёт).
10. Инструменты CI — да.
11. Тестовый релиз — предрелиз `X.Y.Z-beta.N` в основном репозитории.

## Фаза 2 — закрепление, права CI, бот, сканирование (СТОП, ждёт ревью)

### Сделано

Закрепление
- `scripts/check-pins.sh` (тест `test-check-pins.sh`, `make check-pins`, CI
  `licenses`): `uses:` только по 40-значному SHA с `# v<версия>`, `FROM`,
  `image:` compose и `*IMAGE*` в env — только по digest; исключение —
  `allow-unpinned: <причина>` на строке или над ней (наши образы релиза в
  compose: подписываются cosign в фазе 3; образы, которые compose собирает сам).
- Действия `ci.yml`, `docs.yml` — по SHA (версии как в `release.yml`, где они
  совпадали; иначе — последняя в той же мажорной); `upload-artifact@v4` → v7.0.1;
  `persist-credentials: false` у каждого checkout.
- Базовые образы Dockerfile — по digest индекса (multi-arch). Образы тестов —
  в одном месте, `test/images.env` (D24, как `deploy/postgres.env`); скрипты
  берут их через `scripts/lib/test-image.sh` (`test_image debian:12`),
  Kotlin — `TestImages` (сервер) и `E2e.testImage` (e2e); тег без закрепления
  — ошибка теста, а не pull по тегу. Матрица дистрибутивов в `ci.yml` и
  `release.yml` не менялась: она передаёт теги, скрипты их разрешают.
- Gradle: `distributionSha256Sum` (сверена со страницей gradle.org/release-checksums;
  `downloads.gradle.org` из окружения закрыт); блокировка всех конфигураций
  (`LockMode.STRICT`, `buildscript-gradle.lockfile`, `*/gradle.lockfile`);
  `gradle/verification-metadata.xml` (sha256, 629 компонентов), пишется
  `scripts/gradle-lock.sh` с нуля, в режиме мутаций (надмножество
  конфигураций), с protoc и protoc-gen-grpc-java для пяти платформ.
- restic: подпись `SHA256SUMS` обязательна в каждой сборке, ключ по
  умолчанию — из репозитория, `REQUIRE_SIGNATURE` убран (OQ-007 закрыт).
- Go: `go.sum` есть везде; npm — `npm ci` уже везде.

Права CI
- Qodana на паузе: только `workflow_dispatch`, `contents: read` + `checks: write`.
- `scripts/check-workflow-secrets.sh` (тест, `make check-pins`): нет
  `pull_request_target`/`workflow_run`; в workflow, которые запускают PR,
  — ни одного секрета, кроме токена workflow; ключи подписи
  (`MINISIGN_*`, `COSIGN_*`) — только в задаче окружения `release` в
  workflow, который запускается только тегом; никогда — все секреты разом.
- actionlint (`tools/ci`, отдельный модуль: конфликт `yaml/v4` с
  `tools/go.mod`) и zizmor (образ по digest, `tools/images.env`) —
  `make lint-workflows`, CI `supply-chain`.

Бот
- `.github/renovate.json5`: еженедельно (пн до 7:00 МСК), безопасность — сразу,
  мажорные — по запросу из dependency dashboard; группы: действия, PostgreSQL
  установки, образы тестов (только digest), базовые образы, Gradle, Go, web,
  тулчейн Go; regex-менеджеры для `restic-version`, `postgres.env`,
  `.env.example`, `docker-compose.build.yml`, `test/images.env`,
  `tools/images.env`, образов в workflow.
- `.github/workflows/renovate-pins.yml`: на ветке `renovate/*` задача с
  токеном только на чтение пишет lock-файлы, `verification-metadata.xml` и
  sha256 restic (`fetch-restic.sh --update-pins` — только из
  `SHA256SUMS`, подписанного закреплённым ключом); задача с правом записи
  кода ветки не запускает, принимает патч только для этих файлов,
  коммитит и запускает `ci.yml` (`workflow_dispatch`: пуш токеном workflow
  CI не запускает).

Сканирование
- `tools/cmd/vulngate` + `tools/internal/vulngate` (гейт tools: покрытие 100 %,
  CRAP ≤ 6, мутации 0.946): блокирует то, что вызывает код (govulncheck), и
  исправимые critical/high (grype); неисправимые critical — предупреждение;
  исключения — `security/vuln-exceptions`: `<id> <до YYYY-MM-DD> <причина>`,
  не дальше 90 дней, просроченное валит скан.
- `scripts/vuln-scan.sh go|deps|image`; govulncheck — на каждом PR (`ci.yml`,
  `supply-chain`); grype по lock-файлам — на PR, меняющих зависимости
  (`security.yml`), и каждую ночь вместе с образами последнего релиза;
  красный ночной скан открывает или дополняет issue, чистый — закрывает.
- `test/vuln/` — фикстуры с заведомо уязвимыми версиями (x/text v0.3.7 с
  вызовом `ParseAcceptLanguage`, lodash 4.17.15); `scripts/test-vuln-scan.sh`:
  без исключений — exit 2, с исключением на каждый ID — 0, с просроченным — 1.

### Trivy (изучил, источники — разборы Aqua, Snyk, Wiz, SafeDep)

19 марта 2026 года атакующий переписал 75–76 из 76–77 тегов
`aquasecurity/trivy-action` (кража секретов CI перед настоящим сканом) и
выпустил вредоносный бинарник Trivy v0.69.4; началось с неполной ротации
ключей после взлома в феврале. Закреплённые по SHA безопасного коммита
пользователи не пострадали. Вывод: сам сканер годный, но инфраструктура
его релизов была скомпрометирована. Выбран grype (Anchore): тот же
производитель, что у syft (SBOM фазы 3), grype сканирует тот же SBOM;
запускается образом по digest; в задачах сканирования нет секретов, кроме
токена только на чтение (у `report` — `issues: write`). Смена на Trivy —
одна строка в `tools/images.env` и формат в vulngate.

### Вопрос 9: apt «без риска»

Подлинность apt-пакетов уже проверяется подписью архива дистрибутива; риск
— плавающие версии (сегодня одна, завтра другая). Убрать его можно для
образов: снимок архива на дату (`apt-get update --snapshot <дата>`, Ubuntu
24.04 и Debian 13, snapshot.ubuntu.com / snapshot.debian.org), дату двигает
Renovate. В отгружаемых образах apt один — `openssh-client` в образе агента
(обе базы). Остаётся неустранимым: образ раннера GitHub (не закрепляется) и
apt на нём (`minisign`, `gnupg`, `rpm`) — принятый риск в ADR, либо
self-hosted раннеры (вне объёма). Не сделано: собрать образ здесь нельзя
(нет демона Docker), поддержку `--snapshot` в базах полагаю, не проверял.

## CI первого прогона PR #88 (фаза 2)

- `selftest` (security.yml) — зелёный: фикстуры `test/vuln` блокируются,
  исключение пропускает, просроченное валит. **Проверка 5 — проверил в CI.**
- `deps` — красный по делу: Tomcat 11.0.24 (3 critical, исправлено в 11.0.25),
  Jackson 2.21.5 и 3.1.5 (high). Исправлено поднятием Spring Boot 4.1.1 → 4.1.2
  (Tomcat 11.0.27, Jackson 2.21.7, 3.1.7; заодно grpc 1.84.1, protobuf 4.36.2,
  Hibernate 7.4.13 — патч-релиз BOM), `docs/dependencies.md` обновлён.
- `server`, `server-image`, `e2e` — красные: чистая сборка не прошла
  `verification-metadata.xml` (нет `.module`/`.pom` промежуточных версий BOM:
  junit-bom 5.10.2 и др.). Причина — метаданные писались с тёплым кэшем.
  `scripts/gradle-lock.sh` пишет их из пустого Gradle home; сборка `--offline`
  в этом home (bootJar, тест, jacoco, detekt, spotless, e2e, режим мутаций)
  прошла — **проверил локально**.
- `supply-chain` — красный: shellcheck SC2086 в `renovate-pins.yml`
  (локальный actionlint шёл без shellcheck в PATH). Исправлено.

## Фаза 3 — подпись образов, SBOM, происхождение, офлайн, документация

### Сделано
- `release.yml`:
  - `sbom-packages` (после `reproduce`): SBOM CycloneDX на архитектуру
    (`scripts/release-sbom.sh packages`), `SHA256SUMS` заново — подпись
    `sign` покрывает SBOM;
  - `image`: `images.json` (digest индекса каждого образа), происхождение
    (`attest-build-provenance`, в реестр);
  - `sbom-images`: SBOM каждого образа и архитектуры (syft по digest);
  - `assets` → `sign-server` (окружение `release`, второе подтверждение):
    cosign `sign --recursive` и `attest --type cyclonedx` по digest индекса,
    minisign над `SHA256SUMS.server` («sard-server <версия>»), сразу проверка
    публичными ключами → `server-assets` (проверка ещё раз, загрузка,
    происхождение файлов);
  - `publish`: происхождение файлов релиза;
  - `version`: без `deploy/release/cosign.pub` релиз не начинается;
  - `verify-quickstart`: блок проверки образов из `docs/operator/10-security.md`
    дословно; `verify-offline`: блок из `docs/operator/11-offline.md` дословно,
    в `unshare --net` (сначала проверка, что сети нет).
- `scripts/release-cosign.sh` (sign | attest | verify), `release-sbom.sh`,
  `release-verify-server.sh`; `release-sign.sh` подписывает и
  `SHA256SUMS.server`. `test-release-signing.sh` — 6 новых случаев подделки
  файлов сервера (в том числе подпись агента над теми же суммами) —
  **проверил локально**, тестовым ключом.
- D24: `deploy/release/identity.env` (имена образов, пути ключей);
  `check-identity.sh` (+ тест) сверяет копии; `release.yml`, `security.yml`,
  `offline-archive.sh` читают его.
- Документация: `docs/operator/10-security.md` «Проверка релиза»,
  `11-offline.md`, `docs/release.md` (шаги 7–9: ключ cosign, Renovate, два
  подтверждения; смена ключа), `SECURITY.md` (оба языка), README, ADR
  `00XX-draft-supply-chain.md`.
- Офлайн-блок проверки — **проверил локально** в `unshare --net` на
  подписанных тестовым ключом файлах.

### Не проверено (полагаю)
- cosign v3.1.4 из образа: `sign --recursive`, `attest`, `verify-attestation`
  по тегу, `env://COSIGN_PRIVATE_KEY` — без демона Docker и реестра здесь не
  запускал. Проверки 3 и 4 — только тестовым релизом.
- syft из образа: запись в смонтированный каталог, каталог Go-бинарей в
  распакованном tar.gz.
- grype: запись отчёта в смонтированный каталог — проверено CI (`deps`).

### Нужно от владельца
1. `cosign generate-key-pair` (docs/release.md, шаг 7): `cosign.pub` — в
   `deploy/release/` коммитом в этот PR или следующий; секреты
   `COSIGN_PRIVATE_KEY`, `COSIGN_PASSWORD` — в окружение `release`.
2. Установить приложение Renovate (шаг 8) — проверка 7.
3. Тестовый релиз `vX.Y.Z-beta.N` после слияния — проверки 3 и 4.
4. Вопрос 9 (apt): снимок архива для образа агента — делать или принять риск.

## Решения владельца после фазы 3 (2026-10-25)

- Вопрос 9: apt-пакеты без версий — **принятый риск** (ADR
  `00XX-draft-supply-chain`, «Принятый риск»): снимок архива и собственная
  сборка `openssh-client` отвергнуты.
- Renovate установлен. Он открыл onboarding PR #89 («Configure Renovate»):
  в `main` ещё нет конфигурации. Его не сливать — конфигурация приходит
  с PR #88 (`.github/renovate.json5`); после слияния #88 Renovate закроет #89
  сам. Проверка 7 — после слияния: первый PR обновления закреплённого
  действия или образа.
- Ключ cosign — владелец посмотрит позже.

## CI PR #88 после фазы 3

- `supply-chain`: govulncheck нашёл вызываемые уязвимости в stdlib Go 1.27.1 и
  `golang.org/x/net` v0.57.0 — Go 1.27.3 во всех модулях, `x/net` v0.60.0.
- `submit-gradle` (автоматическая отправка графа зависимостей GitHub) и
  тёплый локальный кэш: `.module` части BOM не было в метаданных — второй
  проход `gradle-lock.sh` с `--refresh-dependencies`; три суммы сверены с
  Maven Central вручную; сборка проходит в тёплом home и офлайн в чистом.
- Ревью CodeRabbit, два круга: 12 исправлены с тестами (в том числе
  `release-cosign.sh` проходил проверку образов с пустым списком — исправлено,
  `test-release-cosign.sh`); один — вопрос владельцу: считать ли `t.TempDir()`
  файловой системой хоста для тестов модуля tools (`crap` и `vulngate`).
- `server`: `DeployPostgresTest` (DB1) нашёл `postgres:18-alpine` в образцах
  `test-check-pins.sh` — образцы переведены на образ redis, обновление
  PostgreSQL их больше не ломает.
- Третий круг CodeRabbit: `tags-ignore:` без `branches` считался запуском
  только по тегам, хотя такой workflow запускает любой другой тег, —
  исправлено, тест в `test-check-workflow-secrets.sh`.
