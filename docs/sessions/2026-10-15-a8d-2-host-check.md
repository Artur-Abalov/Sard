<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# Сессия 2026-10-15: A8d-2 — `sard-agent check` без сервера (coder)

Ветка `ccr-325929b5-timg9n`. Спецификация —
`docs/specs/agent/host-check.feature` (сценарии с пометкой `# A8d-2`, решения
Ч10–Ч31, новые Ч32 и Ч33), QA — `docs/qa/host-check.md`, части 5–9 и 11.
Решения — ADR 0063 (раздел «A8d-2»).

## Решения владельца, применённые в начале сессии
1. `repo check --read-data` под бэкапом (restic код 11) — WARN, а не ERROR:
   `repo_check_steps.go` (`integrityStep`), тесты A8d-1 и интеграционный тест
   приведены, справка `repo check` говорит об этом.
2. `check` и `repo check` без репозиториев — OK с пояснением (`noRepositoriesLine`).
3. `repository.id` offline — SKIP «offline», запись объявленных id не читается
   (Ч32).

## Что сделано (тест первым)
- `internal/hostcheck`: `perms.go`, `tls.go`, `configcheck.go`, `postgres.go`,
  `selinux.go`, `service.go`, `secret.go`, `lines.go`, `ids.go` — решения по
  фактам, таблицы тестов без хоста. `secrets.Info.GID`.
- `cmd/sard-agent`: `check_cmd.go` (ход, зависимости, права), `check_files.go`,
  `check_host.go`, `check_repos.go`, `check_help.go`; флаги `--offline` и
  отказ `--read-data` у `check` (`repo_flags.go`); `hostDeps.progExec` и
  `loadConfig`; список команд в справке `sard-agent`.
- Тесты: `check_test.go`, `check_run_test.go`, `check_postgres_test.go`,
  `check_state_test.go`, `check_fixture_test.go` (здоровый хост, фейковые
  программы, PKI).

## Находки и решения
- **Нарезка и сервер.** Сценарии A8d-2 («Здоровый хост», сводка) считают строки
  `server.*` и `clock.skew` OK, а нарезка относит проверку сервера к A8d-3.
  Решено: строки на месте, SKIP (Ч33). До A8d-3 сводка здорового хоста — на
  четыре `skipped` больше. Спецификация и QA дополнены.
- **config.names и config.load.** Дублирование имени возвращает
  `config.DuplicateNameError`; это config.names ERROR при config.load OK,
  хотя конфига для дальнейших проверок нет.
- **Чтение «глазами службы» под root.** Нет способа читать от имени службы в
  процессе root без смены uid; права судятся по битам (`hostcheck.CanRead`) с
  основной группой службы, дополнительные группы не учитываются.
- **Программы PostgreSQL ищутся запуском** `<dir>/<tool> --version` от имени
  службы: каталог, который служба не может пройти, равен отсутствию (Ч31).
  PATH — процесса `check` (под sudo `secure_path`), не юнита.
- **ausearch и 24 часа.** Формат `-ts` зависит от локали, поэтому запрос
  `-ts yesterday`, а окно режет разбор по времени записи аудита.
- **`systemctl show`**, а не `is-active`: `Systemd.Active` пишет в фейк-журнал
  вызовов, а показать время старта и число перезапусков им нельзя.
- В сообщении OK `repository.storage` теперь адрес с `***` вместо пароля (Ч24).

## Не сделано (вне среза)
Проверка сервера и часов (A8d-3), подсказки в ошибках шагов и документация
оператора (A8d-4).
