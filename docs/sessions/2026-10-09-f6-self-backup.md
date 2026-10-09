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

### Вопросы — список отдан владельцу

См. сообщение сессии. Ответы — в следующем разделе после ревью.
