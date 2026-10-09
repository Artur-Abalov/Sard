<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-09 — F6: самобэкап Sard

Ветка `claude/wonderful-brahmagupta-o95roh` (сессия привязана к ней вместо
`feat/f6-self-backup`). Основа — `main` на `ea01ad2`, F3a уже влита.

## Фаза 1 — исследование (СТОП, ждёт ревью)

Кода нет. Ниже то, что прочитано в репозитории (file:line). Вопросы
владельцу и предложения — в конце.

### Что уже есть

**Сосед F5.**
- `deploy/docker-compose.yml:87-110` — сервис `self-agent`, uid 10001,
  `read_only`, `cap_drop: ALL`. Тома: `sard-pki:/var/lib/sard/pki:ro`,
  `sard-self-channel:/run/sard-self:ro` (`enroll-token`, `db-password`),
  `sard-self-config:/etc/sard/self` (rw, сюда ложатся `agent.d/repo-*.yaml`),
  `sard-self-state:/var/lib/sard-self`.
- `.env` и compose-файл не смонтированы ни в один контейнер.
- Конфиг соседа вшит в образ (`deploy/agent/Dockerfile:73-94`):
  `secrets: sard-db: /run/sard-self/db-password`, репозиториев нет (D17).
- **Клиента PostgreSQL в образе агента нет** (`00XX-draft-self-agent.md:26`).
  Образ на `ubuntu:24.04`, где штатный клиент — 16-й, а сервер в compose — 18-й.
- Роль `sard_self`: `V202610071200__self_dump_role.sql:22-32`. В ней
  `pg_read_all_data`, `default_transaction_read_only`, `CONNECTION LIMIT 4`.
  Отозваны TEMP, CREATE в `public` и `lo_*`. Пароль генерирует сервер в
  `SARD_SELF_DIR/db-password` (`SelfAgentSetup.kt:14-44`).
- Встроенность — `agents.builtin` (`V202610081200__self_agent.sql:8-10`),
  берётся из токена (`Enrollment.kt:123`), чтение — `Agents.builtinLive`
  (`fleet/Agents.kt:155`). В Register флага нет.
- В e2e-модуле соседа нет. F5 проверяет `scripts/test-self-agent.sh` на живом
  compose, и он не подключён ни к Makefile, ни к CI.

**Плагины.**
- postgresql (`agent/plugins/postgresql/schema.json`):
  - поля `host`, `database`, `user`, `password_ref` (`sard-secret`),
    `pg_dump_path`, `include_globals` (по умолчанию true),
    `globals_role_passwords`;
  - глобальные объекты без паролей ролей работают без суперпользователя
    (`dump.go:67-69`, e2e с ролью `pg_read_all_data`).
- files (`agent/plugins/files/schema.json`):
  - поля `paths`, `exclude`, `one_file_system`;
  - каждый перечисленный путь должен существовать (`plugin.go:51`).
- Теги снимка:
  - `sard.step`, `sard.run`, `sard.source` (`DispatchParts.kt:47-61`);
  - `postgresql.*` (`dump.go:39-45,79-86`);
  - files своих тегов не ставит.
- Репозиторий в Register: `name`, `backend`, `repository_id`,
  `crypto_provider` (`agent.proto:383-395`). Backend `local` — всё, что не
  azure, b2, gs, rclone, rest, s3, sftp или swift (`config.go:192-203`).
- `repo add` (A8a) — только локальный путь, иначе `BACKEND_NOT_SUPPORTED`.
  Команда требует root и перезапускает systemd-службу. Для контейнера соседа
  порядок не описан. A8b в main нет.

**Сервер.**
- `sources` (`V202609301200__runs.sql:23-38`):
  - системного признака нет;
  - удаление мягкое;
  - агент задан по id, репозиторий по имени.
- Проверки источника — `AgentOffer.require` (`runs/AgentOffer.kt:36`).
- Запуск — `Runs.start(tenantId, sourceId): RunView` (`runs/Runs.kt:92`).
  При активном запуске — `RunActive` (D6).
- Расписание F3a — одно на источник (`V202610091200__schedules.sql`).
  `Schedules.set(tenantId, sourceId, ScheduleDraft(cron, timezone, enabled))`
  (`scheduler/Schedules.kt:127`).
- «Упал» и «восстановился» — не события, а `Telling` при отправке
  (`notify/Notifications.kt:41-67`).
- Уведомления строго по запускам: `notification_deliveries.run_id NOT NULL`,
  ключ `(tenant, run, channel)` (`V202610021200__notifications.sql`). Событию
  без запуска места нет.
- Консоль на каждой странице читает только `GET /api/v1/session`
  (`web/src/auth/session.ts:13`). `/overview` и `/status` читает только
  дашборд. Место для баннера — `web/src/Layout.tsx:127-131`, над `<Outlet/>`.
- Часы — бин `Clock` (`ClockAutoConfiguration.kt`), в тестах `MovableClock`.

**Документация, которая противоречит постановке.**
- `docs/operations/pki.md` («Почему бэкапить отдельно»),
  `docs/operator/06-data-and-backup.md:16-17` и
  `docs/operations/self-agent.md` («Бэкап CA храните отдельно от бэкапа базы»)
  требуют держать CA отдельно от базы. Постановка F6 даёт один репозиторий
  на оба источника. См. вопрос 2.

### Вопросы и ответы владельца

1. Сосед без клиента PostgreSQL. Решение — отдельный образ на `postgres:18`.
   Ответ: «да», и сразу думать о клиентах. Решение: общий образ
   `sard-agent-pg` (`<версия>-pg18`). У агента-пакета на хосте клиента
   pg_dump берётся из пакетов хоста: плагин отказывает, если pg_dump старше
   сервера (`prepare.go:147-149`).
2. CA и база в одном репозитории. Ответ: да; правило `pki.md` меняется в ADR.
3. `.env` и compose — монтировать каталог установки `:ro`, `.env` с группой
   10001 и правами 0640, исключить ручные бэкапы. Ответ: да.
4. «Давно не было успеха» вне очереди запусков. Ответ: «косяк, нужно
   нормальное решение, а не костыль». Предложено: свежесть любого источника с
   расписанием, эпизоды просрочки, очередь уведомлений про предметы (запуск
   или эпизод), на ней же тревога о пропусках F3b. Это фаза 3, ждёт
   подтверждения направления.
5. Репозиторий соседу добавляется через онбординг командой
   `docker compose exec -u 0 self-agent sard-agent repo add …`. Проектировать
   под это.
6. Глобальные объекты не нужны: «пока что полагаю да».
7. Порог — 1,5 интервала cron: да.
8. Баннер читает `GET /api/v1/self-backup`: да.
9. Контракт REST: да.
10. Новый встроенный агент: переводится повторной привязкой. Да.
11. Адрес базы — из datasource сервера: да.
12. В обход `/ship-feature`.
13. Соседа — в e2e-стенд: «необходимо».
14. Номера ADR — на усмотрение, при слиянии.

## Фаза 2 — привязка, системные источники, расписание, REST (2026-10-09)

**Сервер.**
- Миграция `V202610101200__self_backup.sql`:
  - `sources.system_role` (`self_database` | `self_keys`), уникальна среди
    живых источников тенанта;
  - уникальность имени — только среди источников администратора;
  - `self_backups` (`id`, `tenant_id`, `bound_at`).
- `selfbackup/`:
  - `SelfBackupProperties` (`sard.self-backup.*`): cron `0 3 * * *` в поясе
    сервера, база из JDBC URL сервера, роль `sard_self`, секрет `sard-db`,
    `pg_dump_path` 18-го клиента, каталоги CA и установки, исключения;
  - `SelfBackupPlan` — два конфига;
  - `SelfBackups` — привязка, состояние и «запустить сейчас». Привязка —
    одна транзакция: источники, их расписания (`Schedules.setIn`) и строка
    привязки. Повтор без изменений ничего не пишет. Новый репозиторий или
    агент переводит те же источники, расписания сохраняются.
- `Sources.replace` и `delete` на системном источнике → 409 `system_source`.
- REST:
  - `GET /api/v1/self-backup`, `PUT /api/v1/self-backup/repository`,
    `POST /api/v1/self-backup/runs`;
  - `Source.systemRole`;
  - коды `self_agent_missing`, `repository_not_initialized`,
    `local_storage_unconfirmed`, `self_backup_not_configured`,
    `system_source`.
- По ходу:
  - `TenancyIntegrationTest` и `TenantSchemaRulesTest` потребовали
    `@TenantId` и `UNIQUE (tenant_id, id)` у `self_backups`;
  - мутанты показали, что колонка `local_storage_confirmed` нигде не
    читается — удалена. «Локальное» выводится из backend в Register,
    подтверждение — только ворота привязки.

**Веб.**
- OpenAPI и `schema.d.ts` перегенерированы (`make openapi`).
- Моки `/self-backup` с теми же отказами.
- 409 `system_source` в моках источников.
- Метка «системный»: карточка без «Изменить» и «Удалить», в списке — метка.
- Коды ошибок в `errors.ts` и локалях.

**Развёртывание.**
- `deploy/docker-compose.yml`: `./:/etc/sard/install:ro` в `self-agent`.
- Установка (`02-install`, `11-offline`, `demo`): `sudo chgrp 10001 .env &&
  chmod 640 .env`.
- `scripts/test-self-agent.sh`, проверка 7:
  - CA и каталог установки смонтированы `:ro`, запись отказывает;
  - `.env` и ключ CA читаются;
  - `sard_self` читает базу и не может сделать update, create, temp или
    large object даже с выключенным read-only.

**Тесты (проверено):**
- `SelfBackupPropertiesTest` (9), `SelfBackupPlanTest` (3),
  `SelfBackupIntegrationTest` (16), `SelfBackupApiIntegrationTest` (7) —
  проверки 1, 5 и 6 постановки. Плюс контракт и сериализация OpenAPI.
- `make gate M=web` fast: PASSED (Vitest 445).
- `gate server fast`: PASSED, coverage 96,6% (instructions), CRAP ≤ 6
  (сначала пять новых функций были выше — упрощены).
- mutflow по `dev.sard.server.selfbackup.*`: выживших нет.

**Не проверено (полагаю):**
- Проверка 7 (`test-self-agent.sh`) не запускалась: нужны собранные образы
  сервера и агента. Запуск — фаза 3, вместе с e2e.
- Работа `repo add` в контейнере соседа — фаза 3.

**Окружение.** Docker в контейнере сессии запускается вручную. Docker Hub
отвечает 429, образы берутся через `mirror.gcr.io`. Maven Central
периодически отвечает 429, гейт перезапускается с паузой.
