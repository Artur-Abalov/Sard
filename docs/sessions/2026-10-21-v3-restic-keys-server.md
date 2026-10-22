<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-21 — V3: ключи restic, срез 2 из 3 (сервер) (coder)

Спецификация: `docs/specs/server/restic-keys.feature` (КС1–КС8, ВС1); ADR
`docs/adr/0067-restic-keys.md`. Срез 1 (контракт и агент) — журнал
`2026-10-21-v3-restic-keys-agent.md`. Срез 3 (консоль) не начинался.

## Сделано

- **Признак из Register (КС4).** `registration.RecoveryKey` (present, absent,
  unknown); `recoveryKeyOf` отображает `RecoveryKeyState` исчерпывающим `when`:
  `UNSPECIFIED` и `UNRECOGNIZED` — unknown, не absent. Миграция
  `V202610211200__repository_recovery_key.sql`: колонка
  `agent_repositories.recovery_key` (`present|absent|unknown`, по умолчанию
  unknown, поэтому строки до V3 — unknown). Register пишет её вместе с остальным
  снимком, а карточка и список читают из базы: перезапуск ничего не меняет.
- **REST агентов.** `AgentRepository.recoveryKey`; элемент `GET /api/v1/agents`
  получил `repositories` (см. «Замечания к спецификации»).
- **`GET /api/v1/repositories` (КС5, КС6).** `fleet.RepositoryRules` — чистые
  решения (признак из последнего Register с известным значением, при равенстве
  меньший agentId как строка, то есть в порядке PostgreSQL; один держатель на
  агента; команда); `fleet.Repositories` читает неотозванных агентов тенанта;
  `RepositoriesController` и `RepositoriesApiImpl`.
- **Карточка верификатора (КС7).** `AgentDetails.verifierSetup` (null у
  источника); имя вне `[A-Za-z0-9._-]` берётся в одинарные кавычки, имя с
  одинарной кавычкой или управляющим символом даёт `null` в обеих командах.
- **Самобэкап (КС1, КС2).** `recoveryKeyCommand` в `SelfBackupView` и
  `SelfBackup` (null до привязки), `selfbackup.recoveryKeyCommand(name)`;
  `passwordRevealCommand` остаётся (ВС1). Команду встроенного агента в
  `fleet.Repositories` передаёт конструктор: `fleet` не знает про `selfbackup`.
- **OpenAPI.** `make openapi`: `web/src/api/openapi.json` и `schema.d.ts`.
- **e2e (В2 агента).** Агент без терминала требует `--recovery-key-file` или
  `--no-recovery-key` при создании репозитория: `AgentHost.repoInit` и `repoAdd`
  добавляют `--no-recovery-key`, если вызывающий не выбрал сам; `SelfAgent`,
  `RecoveryOldVm` и `scripts/test-agent-install.sh` — тоже.

## Замечания к спецификации

1. **Список агентов без `repositories`.** КС4 и сценарий «Агент старше V3…»
   ждут `recoveryKey` в элементе `GET /api/v1/agents`, а у элемента такого
   массива не было. Добавлено `AgentSummary.repositories` (те же поля, что в
   карточке); спецификации это нужно подтвердить.
2. **Имена, которых не бывает.** `SnapshotRules` (`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
   и CHECK таблицы не пускают в базу имя с пробелом, кавычкой или переводом
   строки, поэтому сценарии КС7 «имя с пробелом» и «имя, которое нельзя безопасно
   вставить» недостижимы по HTTP. Правило реализовано и проверено на уровне
   `RepositoryRulesTest`; HTTP-сценариев для них нет.
3. **Кавычки в `recoveryKeyCommand` (КС6).** Спецификация правил кавычек для
   него не задаёт; применены правила КС7.
4. **Подсказки `InstallCommands` и `RepositoryCommands`.** Их исполняет
   оператор в терминале, где `repo add` по умолчанию печатает ключ
   восстановления один раз (правило 10 агентской спецификации), поэтому флаги
   не добавлялись; менять нужно только неинтерактивные вызовы (e2e, скрипты).

## Исправления по замечаниям architect (срез A: сервер и e2e)

- **B1.** `recoveryKeyCommand` в одноразовом контейнере compose не мог
  перезапустить соседа (нет systemd: `hostsetup.Applier.Apply` отвечает
  «перезапустите агент сами»), поэтому `self-agent` не вызывал Register заново и
  сервер и баннер показывали `absent`. Команда собирается из того же префикса
  одноразового запуска, что и `repo add` (`oneOffAgent` в `selfbackup`), с
  `--no-restart` и продолжением ` && docker compose restart self-agent`.
  Тест: «Команда ключа восстановления перезапускает соседа, иначе сервер не
  узнает признак». Обновлены точные строки в тестах API, описание
  `SelfBackup.recoveryKeyCommand`, OpenAPI (`make openapi`) и мок
  `web/src/mocks/commands.ts` (там живёт `recoveryKeyCommand` мока
  `mocks/api/selfBackup.ts`).
- **Правка заметки КС2** в `restic-keys.feature` (фактическая поправка, не смена
  поведения): прежнее «команда перезапускает соседа по Р8 сама» неверно для
  compose; теперь вид команды включает `--no-restart` и `&& docker compose
  restart self-agent`.
- **e2e.** `SelfRecoveryKeyTest` (команда самобэкапа выпускает ключ, сервер
  узнаёт признак за 60 с; повтор печатает `unchanged`) и `HostRecoveryKeyTest`
  (`key add` на хосте: `present`; `key remove`: `absent`). Компилируются
  (`:e2e:compileTestKotlin`); на стенде НЕ запускались (нужны образы `make
  e2e-images`).
- **N1/N2.** `fleet/RepositoryRules.kt` разрезан без смены поведения:
  `RepositoryHolders.kt` (holder, `latest`, `latestSource`), `RecoveryKeyRules.kt`
  (`RecoveryKeyRules.summaries`, `withoutRecoveryKey`), `VerifierSetupRules.kt`
  (`VerifierSetupRules.verifierSetup`), `AgentCommands.kt` (три команды для
  хоста). Две функции кавычек лежат рядом в новом пакете `shell`
  (`shellWord` всегда отвечает, `shellWordOrNull` отказывает на кавычке и
  управляющем символе: так требует спецификация КС6/КС7); `fleet` не импортирует
  `selfbackup`. `RepositoryRulesTest` разрезан так же.
- **N7.** `FleetImportsTest`: `fleet` не импортирует `selfbackup`, `api`,
  `agents`, `dev.sard.proto`, `org.springframework.web` (проверено: с запретным
  импортом тест краснеет).
