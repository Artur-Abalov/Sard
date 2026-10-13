<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# Сессия 2026-10-12: A8c-1 — `repo snapshots` и предустановки yandex, vk, selectel (coder)

Ветка `ccr-bb1484f1-25wm55`. Спецификация (утверждена владельцем 2026-10-12) —
`docs/specs/agent/repo-restore.feature` (правила и сценарии с пометкой `# A8c-1`,
Р53–Р57, Н26, Н33, Н35) и `docs/specs/agent/host-setup.feature` (Р51, сценарии
A8c-1, изменённые «wasabi» и справка `repo add`). Ручная проверка —
`docs/qa/repo-restore.md` часть 1, `docs/qa/host-setup.md` часть 12.
A8c-2 (`repo restore` каталога) и A8c-3 (PostgreSQL) не тронуты.

## Что сделано

- `internal/restic`: `CLI.Snapshots(ctx, tags)` — `restic snapshots --json
  --no-lock [--tag …]`, тип `restic.Snapshot`. Не добавлен в интерфейс
  `restic.Repository`: его читает только `internal/restore`.
- `internal/restore` (новый пакет, Р68): `List` (порядок по времени и id, UTC,
  значения тегов `sard.source`, `sard.run`, `sard.step`, пустое значение — нет
  значения), `CheckSource` (Р57), `ListFailure` (`REPOSITORY_NOT_INITIALIZED` для
  кода 10 restic, иное — как у остальных команд, `repoinit.FromRestic`).
  Зависит от интерфейса `Lister`, не от файловой системы; A8c-2 и R2 используют
  тот же пакет.
- `refusal.RepositoryNotInitialized` (класс «использование»).
- `repoconnect.Bound.Attempt` — первое обращение к хранилищу под
  `--connect-timeout` для произвольного вызова (как `Inspect` и `CheckLock`).
- `package main`: `repo snapshots` (`repo_snapshots_run.go`), флаги `--source`
  и `--connect-timeout` у команд, у которых нет флагов S3 (`offerSource`,
  `offerConnectTimeout`), справка, строка «want a subcommand» называет
  `snapshots`. Порядок: флаги → права → конфиг → имя → restic → файлы пароля и
  ключей → список. `SECRET_FILE_REJECTED` называется в сообщении (текст A1
  отдельно причины не содержит). Пароль из файла добавлен в `Scrubber`: текст
  restic его не показывает.
- Предустановки: одна таблица `providerTable` в `hostsetup/s3.go` (строка:
  хост, регион по умолчанию, «только этот регион»); `ExpandProvider` теперь
  возвращает и регион (подставленный попадает в `AWS_DEFAULT_REGION`),
  `ProviderNames()` строит список в отказе и в справке. `boundary_test.go`:
  в package main нет хостов новых провайдеров и нет разбора тегов `sard.*` и
  `postgresql.*` (вне справки).
- Документация: `docs/operations/agent-host-setup.md` (раздел «Снимки
  репозитория: repo snapshots», предустановки, права, коды выхода),
  `docs/operator/05a-storage.md` (таблица `--provider`).

## Красный прогон

Тесты `repo snapshots` падали с «want a subcommand»; тест `ExpandProvider` не
собирался (3 значения, `ProviderNames` нет); справка `repo add` не называла
`yandex`, `vk`, `selectel`. При написании теста секретов выяснилось, что пароль
из файла, напечатанный restic, редактируется лишь случайным пересечением с
маркером значения `env`-файла — добавлен `passwordValues` в `Scrubber`.

## Решения и отклонения

- Таблица: колонки разделены одним пробелом без выравнивания, чтобы заголовок
  был буквально `ID TIME HOST SOURCE RUN PATHS` (QA сверяет первую строку).
  Пути с пробелом в таблице неоднозначны — для разбора есть `--json`.
- ADR не добавлен: решения Н26 (`--no-lock`) и Н27 записываются в ADR 0057
  вместе с `repo restore` (A8c-2, Н36).
- Время restic с часовым поясом хоста разбирается как `time.Time` и печатается
  в UTC без долей секунды.
- Формат `restic snapshots --json` проверен на настоящем restic 0.19.1
  (интеграционный тест): плоский массив без группировки.

## Не автоматизировано

Часть 1 `docs/qa/repo-restore.md` (шаги на ВМ и стенде: S3, SFTP, реальное
молчащее хранилище) и часть 12 `docs/qa/host-setup.md` — ручной QA. Домены
yandex, vk, selectel приняты владельцем как в Н35; доступность провайдеров
`@local`-тестами не проверяется.

## Для specifier

- Н35 оставлял сомнение по домену vk; владелец принял `hb.ru-msk.vkcloud-storage.ru`.
