# QA: S8a — контракт REST API этапа 1

Сценарии: `docs/specs/web/s8a-api-contract.feature`. Схема контракта:
`docs/specs/web/api-v1-contract.md`.

Моки MSW в браузере перехватывают только запросы страницы (service worker),
поэтому `curl` до них не доходит: проверки dev-режима выполняются в консоли
DevTools открытой страницы. Шаги с пометкой [П] зависят от предложенных
значений и выполняются после их подтверждения.

Предусловия: Node 24, JDK 25 и Docker (для шага 11), рабочая копия ветки
без локальных изменений. Команды — из `web/`, если не сказано иное.

## Автоматические проверки

1. `npm ci --no-audit --no-fund` — код выхода 0.
2. `npm run gen:api && git diff --exit-code -- src/api/schema.d.ts` — код
   выхода 0, различий нет.
3. `npm run lint` — код выхода 0; в выводе Redocly ноль ошибок
   (`validated` без `error`), oxlint и Prettier без замечаний.
4. `npm run typecheck` — код выхода 0.
5. `npm test` — код выхода 0, все тесты зелёные, среди них тесты
   `fetchStatus` из `src/api/client.test.ts` (файл не изменён:
   `git diff origin/main -- src/api/client.test.ts` пуст).
6. Каждый сценарий покрыт тестом (из корня репозитория):
   ```bash
   grep -E '^\s*Scenario( Outline)?:' docs/specs/web/s8a-api-contract.feature \
     | sed -E 's/^\s*Scenario( Outline)?: //' \
     | while read -r t; do grep -rqF -- "$t" web/src web/scripts server/src scripts .github || echo "НЕ ПОКРЫТ: $t"; done
   ```
   Ожидается: пустой вывод.
7. `npm run build`, затем `test ! -e dist/mockServiceWorker.js &&
   ! grep -rq VITE_API_MOCKS dist` — код выхода 0.

## Отрицательные проверки (каждую откатить `git checkout -- .` после шага)

8. В `src/api/openapi.yaml` заменить любую `$ref` на
   `#/components/schemas/NoSuchSchema`, выполнить `npm run lint` — ненулевой
   код выхода, в выводе упомянут `NoSuchSchema`.
9. Добавить в `openapi.yaml` новое свойство в схему `Source`, не запуская
   `gen:api`; выполнить `npm run gen:api && git diff --exit-code --
   src/api/schema.d.ts` — код выхода 1 (так же падает шаг CI job web).
10. Удалить `dumping` из перечисления фаз шага в `openapi.yaml`, запустить
    сверку перечислений (команда — по реализации; входит в гейт) —
    ненулевой код выхода, в сообщении названы перечисление фаз и значение
    `dumping`. Затем добавить `trace` в уровни логов — та же проверка
    падает и называет `trace`.
11. Из корня: `./scripts/gate.sh server fast` и проверка путей springdoc из
    job server — проходят. Удалить путь `/api/v1/status` из `openapi.yaml`
    и повторить проверку путей — она падает и называет `GET /api/v1/status`.

## Dev-режим с моками

12. `VITE_API_MOCKS=1 npm run dev`, открыть `http://localhost:5173/` —
    дашборд показывает версию `0.0.0-mock`; в консоли нет ошибок MSW о
    необработанных запросах.
13. В консоли DevTools:
    `await fetch('/api/v1/session').then(r => r.status)` → `200` (сессия
    открыта без входа).
14. `await fetch('/api/v1/agents').then(r => r.json())` — два агента, у
    одного `online: true`, у другого `false`; `nextCursor: null`. Карточка
    онлайн-агента (`/api/v1/agents/<id>`) содержит `plugins` с объектом
    `configSchema`, `repositories`, `secretNames`, `scriptNames`, и никаких
    других ключей, кроме перечисленных в схеме контракта.
    `fetch('/api/v1/agents/' + crypto.randomUUID())` → 404, тело с
    `code: "not_found"`, `Content-Type: application/problem+json`.
15. Токены:
    ```js
    const c = await fetch('/api/v1/enrollment-tokens', {method: 'POST',
      headers: {'Content-Type': 'application/json'}, body: '{"ttlSeconds":300}'})
    c.status                       // 201
    const t = await c.json()       // token по шаблону sard_…, enrollCommand с этим token
    const l = await fetch('/api/v1/enrollment-tokens').then(r => r.json())
    l.items.some(i => 'token' in i || 'enrollCommand' in i)   // false
    l.items.find(i => i.id === t.id).status                   // "active"
    ```
    `ttlSeconds` 299 и 604801 → 422 [П: `ttl_out_of_range`]; 604800 → 201.
16. Отзыв: активный токен → 200, `status: "revoked"`; повторно → 200, тот же
    `revokedAt`; использованный → 409, `code: "token_already_used"` [П],
    `agentId` онлайн-агента; просроченный → 409, `code: "token_expired"` [П];
    случайный UUID → 404.
17. Запуски: `POST /api/v1/runs` для источника с идущим запуском → 409,
    `activeRunId` равен id идущего запуска. Для источника без активного
    запуска → 201, `status: "queued"`, `trigger: "manual"`; повтор того же
    запроса → 409 с `activeRunId` только что созданного запуска.
    `action: "restore"` → 422.
18. Логи: взять id шага успешного запуска (`/api/v1/runs/<id>`, `steps[0].id`)
    и выполнить
    ```js
    let after = 0, all = []
    for (;;) {
      const p = await fetch(`/api/v1/runs/${run}/steps/${step}/logs?afterSeq=${after}&limit=100`).then(r => r.json())
      if (p.items.length === 0) break
      all.push(...p.items); after = p.nextAfterSeq
    }
    all.length                                            // несколько сотен
    new Set(all.map(l => l.seq)).size === all.length      // true
    all.every((l, i) => i === 0 || l.seq > all[i - 1].seq) // true
    ```
19. Источник: создать на онлайн-агенте с `repositoryName`, которого нет в его
    карточке → 422, `code: "repository_unknown_to_agent"` [П]. `DELETE`
    источника с идущим запуском → 409 с `activeRunId`.
20. `await fetch('/api/v1/session', {method: 'DELETE'}).then(r => r.status)` →
    `204`; затем `/api/v1/agents` → 401, `code: "unauthenticated"`;
    `/api/v1/status` → 200.
21. [П] Перезагрузить страницу — состояние моков вернулось к фикстурам,
    `/api/v1/session` снова 200, созданных в шагах 15–17 объектов нет.
22. Остановить dev-сервер; без запущенного sard-server выполнить `npm run dev`
    без `VITE_API_MOCKS` и открыть страницу — запрос `/api/v1/status` уходит в
    прокси Vite и завершается ошибкой прокси (5xx), а не ответом мока; в
    консоли нет сообщения `[MSW] Mocking enabled`.
