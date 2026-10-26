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

### Открытые вопросы — переданы на контрольную точку 1

См. ответ в сессии; решения будут записаны сюда после ревью.
