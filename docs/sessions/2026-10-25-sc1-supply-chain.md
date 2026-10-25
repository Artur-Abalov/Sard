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
