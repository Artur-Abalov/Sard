<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# DB2 — роли PostgreSQL, bootstrap.sql и пароли базы (coder)

Дата: 2026-10-26. Ветка `ccr-d47045a1-rm8xdu`. Спецификация —
`docs/specs/server/database-roles.feature` (83 сценария, решения Р1–Р14),
ручная проверка — `docs/qa/database-roles.md`. ADR — [0069](../adr/0069-database-roles.md).

## Что сделано
- **Роли и схема.** `deploy/db/bootstrap.sql` создаёт пять ролей, схему `sard`
  (владелец `sard_owner`), права по умолчанию и чинит базу, развёрнутую
  суперпользователем; пароли не задаёт. Повторим. Миграции переписаны: роль
  `sard_self` и миграция `V202610071200` удалены, секции `step_logs` и очистка улик
  проверок — функции в `V202609301800` и `V202610211300`, записи проверок только
  дописываются.
- **`migrate`.** Команда образа сервера (`ServerCommand`), `Migrator`: Flyway от
  `sard_migrator`, `SET ROLE sard_owner`, `lock_timeout` 5 с; миграции расширений —
  `classpath:db/migration-extensions`, своя таблица истории, только если они есть.
- **Сервер.** Работает как `sard_app`, миграций не применяет
  (`MIGRATIONS_NOT_APPLIED`), пароль читает из файла (`SARD_DB_PASSWORD_FILE`),
  предупреждает о лишних правах (`sard_database_role_excess`), ставит
  `sard.tenant_id` в каждой транзакции тенанта, создаёт и удаляет секции журнала
  функциями, очищает улики функцией и отказывается стартовать при сроке улик меньше
  предела базы. Логика `sard_self` (`SelfAgentSetup`, `DumpRole`) удалена.
- **Установка.** В `deploy/docker-compose.yml` службы `db-init` и `migrate`, тома
  паролей по потребителю, `pg_hba.conf` без входа суперпользователя по сети, случайный
  пароль суперпользователя; `deploy/db/db-init.sh`; `.env.example` без паролей базы.
- **Тесты и сценарии CI.** `scripts/test-bootstrap.sh` (PostgreSQL 16, 17, 18),
  проверки 10–16 в `scripts/test-self-agent.sh`, задача `bootstrap` в `ci.yml`.
- **Документы.** ADR 0069; поправки ADR 0013 и 0061; раздел 16 руководства оператора
  (роли, где пароли, смена пароля, аварийный доступ, внешний кластер, переход на
  enterprise); разделы 2, 3, 6, 7, 9, 12, 14, 15; QA; заменённые сценарии в других
  `.feature` помечены `@заменено-db2`.

## Решения по ходу
1. Пароль соседа остаётся по пути `/run/sard-self/db-password`: том канала
   монтируется `:ro`, поэтому `db-init` оставляет в нём пустой `db-password`, поверх
   которого монтируется файл тома `sard-db-backup` (`volume.subpath`, проверено на
   Docker 29 и Compose 5).
2. Одна функция сверх спецификации не добавлена: три функции (секции, улики) и
   таблица `retention_floors`, ограничение записано в ADR и сверяется тестом
   `DatabaseObjectsAdrTest` с базой.
3. Серверная проверка миграций идёт через одно соединение пула
   (`SingleConnectionDataSource`): Flyway просит второе для обратных вызовов, а пул
   в одно соединение (ADR 0013) на нём вставал.
4. Метка счётчика уборки `restore_verification` и `verification_job` заменена на
   `verification_evidence`; записи проверок не удаляются никогда.
5. Спецификация и PostgreSQL расходятся в одном примере: `grant select on runs to
   public` от `sard_app` не даёт SQLSTATE 42501, а предупреждение «no privileges
   were granted» (у роли есть право на таблицу, но нет права его передавать).
   Каталог не меняется, чего сценарий и добивается; тест
   `GRANT от имени sard_app ничего не выдаёт` проверяет именно это.
6. Проверки 5 и 9 `scripts/test-self-agent.sh` (`UPGRADE_FROM`) и сценарий `@upgrade`
   отложены до первого выпуска с DB2 (решение владельца); в CI `UPGRADE_FROM` не
   задаётся, текст ветки оставлен.

## Не проверено здесь
Живой стек `docker compose up` с образами сервера и соседа (`scripts/test-self-agent.sh`,
e2e `test/e2e`) и учения `ServerRecoveryDrillTest` в этой среде не собирались:
образы и пакеты агента не построены. Запущены: `db-init` из compose на настоящем
PostgreSQL 18.6, `bootstrap.sql` на 16 и 17 (`scripts/test-bootstrap.sh`) и все тесты
`:server`.

## Исправления по ревью architect (coder)
- verify-offline в `release.yml` скачивает все файлы установки и ставит их блоком `offline-install` страницы `docs/operator/11-offline.md` дословно; `DeployDatabaseComposeTest` проверяет и скачивание, и `cp … db/`.
- `sard_app` только читает истории миграций: `Migrator` отзывает запись после каждого `migrate`, `bootstrap.sql` держит обе таблицы в `read_only`, ADR 0069 п. 12.
- Только `postgres` и `db-init` монтируют сокет `postgres-socket` (тест).
- Функции `SECURITY DEFINER`: `search_path = pg_catalog, sard, pg_temp`, имена отношений с `sard.` (миграции переписаны, Р1).
- ADR 0069 п. 5 приведён к коду: проверяются только миграции ядра. OQ-261: отложенный `@upgrade`.
- `db-init.sh` отвергает файл пароля не из 64 hex-символов и задаёт пароль из файла при каждом запуске.
- `scripts/test-release-signing.sh` собирает набор файлов из `deploy/db/*`.
- Тесты, подделывавшие историю от `sard_app`, делают это суперпользователем (`TestAdmin`).
