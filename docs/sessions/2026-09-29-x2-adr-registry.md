# Сессия 2026-09-29: X2 — ADR D5–D7, номера черновиков, сверка реестра

Ветка `claude/zealous-hopper-jz1z7r` от `main` @ `ff97ae1`. Только документы и комментарии; код продукта, миграции (S6) и REST (S8b) вне задачи.

## Фаза 0 — исследование и вопросы

Один субагент только на чтение: упоминания «00XX», формат ADR 0019–0021, строки реестра, факты keepalive и `UnimplementedEnrollmentTokensApi`. Ключевые места перечитаны напрямую.

### Проверено
- План этапа 1 в репозитории не хранится; ссылка на него есть только в `docs/specs/agent/agent-enroll.feature:118`.
- Проверок markdown и ссылок в CI нет: для документации — только `make license-check` (`.github/workflows/ci.yml`), и он пропускает `docs/*` (`scripts/license-check.sh:26`).
- Контракт S8a уже исходит из D6: `RunStatus` с `dispatched` (`server/.../api/ApiEnums.kt:39`), `RunActiveProblem.activeRunId` (`Problems.kt:93-100`), `@RunActive` на запуске и удалении источника (`SourcesController.kt:165,177`), 422 `unknown_repository` (`SourcesController.kt:134`). В ADR 0013 у `runs` не было ни `source_id`, ни `dispatched`.
- keepalive: `application.yaml:36-41`, `KeepaliveIntegrationTest.kt:58`, `AgentStreamIntegrationTest.kt:69`.
- Отзыв: стримы закрываются сверкой (`AgentConnections.kt:39-45`, тесты `AgentStreamIntegrationTest.kt:187,197`); REST отзыва нет, в `AgentsController.kt` только `GET` (:105, :113).
- `UnimplementedEnrollmentTokensApi` (`UnimplementedApi.kt:24-37`) — единственная реализация `EnrollmentTokensApi` в `server/src/main`.
- `EnrollRequest` не несёт `agent_id` (`agent.proto:46-54`), `Enroll` всегда создаёт нового агента (`Enrollment.kt:116`) — основание ограничения безопасности D5.
- В постановке для D5 названы «В5–В7» спецификации A2b; там это `TOKEN_FOREIGN_CA`, имя хоста и путь конфига. Поведение D5 — решения владельца 6, 7, В18, В19, правила «Существующая идентичность без --force не трогается» и «--force регистрирует новую личность…».

### Ответы владельца
1. План этапа 1 в репозиторий не добавлять; обновлённую копию — файлом вне репозитория.
2. Реестр: OQ-004 — в закрытые; OQ-023 — остаётся открытым, сужен до API/UI; REST токенов — из закрытых в открытые как OQ-037.
3. Фаза 1 — отдельный PR; фаза 2 — после.
4. Ссылки D5 — на фактические места спецификации.

## Фаза 1 — ADR 0022 (D6)

- `docs/adr/0022-one-active-run-per-source.md`: решение, отвергнутое (очередь, параллельные запуски, неявный workflow, репозиторий при запуске, проверка в коде), последствия, отложенное.
- ADR 0013, целевая схема: `sources.repository_name`, `runs.source_id NOT NULL`, `workflow_id NULL` только при `trigger = 'manual'`, `dispatched` в `runs.status`, `definition NULL` без workflow, частичный уникальный индекс D6.
- Два пункта сверх постановки, вынесены на ревью: `dispatched` в `runs.status` (следует из D6 и ответа 2 владельца в журнале S8a) и `(workflow_id IS NULL) = (definition IS NULL)` (мой вывод).
- PR [Artur-Abalov/Sard#23](https://github.com/Artur-Abalov/Sard/pull/23), CI зелёный (10 проверок).

## Фаза 2 — D5, D7, номера, реестр

- `0023-agent-re-enrollment.md` (D5), `0024-notification-policy.md` (D7) — ссылками на спецификацию A2b и ADR 0026, без повторения их содержимого.
- Черновики: `00XX-draft-grpc-error-model.md` → `0025-grpc-error-model.md`, `00XX-draft-agent-stream-manager.md` → `0026-agent-stream-manager.md`; статус «принято», индекс `docs/adr/README.md`.
- «00XX» заменено в ADR 0009, 0013, 0026, `docs/open-questions.md`, `docs/dependencies.md`, `agent-enroll.feature`, в комментариях `StreamCloseReason.kt`, `AgentSessionRegistry.kt`, `AgentStreamSettings.kt`, `application.yaml`, `classify.go`. Журналы сессий не правились — это история (`docs/open-questions.md:3`).
- Реестр: OQ-004 — «Закрыто при сверке 2026-09-29»; OQ-023 — «задача», S8b/W2; OQ-037 — REST токенов, «задача», S8b; прежняя строка «закрыто» удалена. У каждой правки — file:line.

### Проверки (проведены)
- `grep -rn 00XX` вне `docs/sessions/` — пусто.
- Относительные ссылки `](…)` в `docs/**/*.md` (кроме журналов) — все файлы существуют; пути `docs/adr/NNNN-*.md` в репозитории — несуществующие только в старых журналах (`2026-09-27-a5a-restic-wrapper.md:250,308`, `2026-09-27-w1a-routing-mocks.md:67,125`), до этой сессии.
- `make license-check` — OK; `gofmt -l agent/internal/enroll/` — пусто; `./gradlew :server:spotlessCheck` — OK.

### Не проверено
- Номера 0023–0026 верны, только если до слияния в `main` не попадёт другой ADR.
