<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-08 — красный CI на main после слияний A8a, F1, R1, F8

Ветка `claude/upbeat-dijkstra-rqynlc`.

## Причины и исправления

- **install-rpm.** A8a поменял подсказку postinstall на `sudo sard-agent
  enroll` (ADR 0050), а `scripts/test-agent-install.sh` ждал
  `sudo -u sard-agent sard-agent enroll`. Проверка ждёт текущую подсказку
  (как `scripts/test-postinstall.sh`).
- **e2e (PostgresqlRestoreTest, PostgresqlSourceTest).** F1 добавил
  `test/e2e/agent/Dockerfile.postgres` с раскладкой `/usr/lib/sard/`, а R1
  (ADR 0048) перенёс программы агента в `/usr/libexec/sard/`, куда смотрит
  `AgentImage`. Образы pg14/pg18 стартовали без агента. Пути исправлены, а
  также пример в `docs/operator/05a-storage.md`.
- **server (mutflow).** 2 упавших теста в прогоне `-Pmutflow.enabled=true`;
  лог CI их не называет — разбирается отдельно.
