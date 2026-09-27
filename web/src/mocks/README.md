# Моки API (MSW)

Обработчики из этого каталога отвечают на запросы `src/api/client.ts` без
запущенного sard-server:

- в dev-сервере: `VITE_API_MOCKS=1 npm run dev` запускает service worker
  (`browser.ts`) до первого рендера;
- в Vitest: `vitest.setup.ts` поднимает `msw/node` (`node.ts`) для каждого
  тестового файла с `onUnhandledRequest: 'error'`, поэтому запрос без обработчика
  роняет тест, а не уходит в сеть.

Обработчики общие — `handlers.ts`. В production-сборку не попадает ни то, ни
другое: ветка в `main.tsx` там статически ложна, а скрипт воркера отдаёт из
`node_modules/msw` плагин Vite, который работает только в dev-сервере (в
`public/` воркера нет).

## Как добавить обработчик, когда схема OpenAPI расширится

1. Перегенерировать типы клиента: `make openapi` (или `npm run gen:api` после
   изменения `src/api/openapi.json`). Новый путь появится в `src/api/schema.d.ts`.
2. Положить пример данных в `fixtures.ts` с типом из схемы
   (`components['schemas'][...]`), чтобы тесты сравнивали с тем же объектом.
3. Добавить обработчик в `handlers.ts` через типизированный `http` из `http.ts`
   и помощник `response(status).json(body)`:

   ```ts
   http.get('/api/v1/agents/{id}', ({ params, response }) =>
     response(200).json({ ...agent, id: params.id }),
   )
   ```

   Путь, параметр, статус или тело, которых нет в схеме, — ошибка `tsc`.
   `contract.typecheck.ts` проверяет, что это по-прежнему так.

4. Если тесту нужен другой ответ, он переопределяет обработчик только для себя:
   `server.use(http.get(...))`. `vitest.setup.ts` сбрасывает обработчики после
   каждого теста.

Обработчики пишутся только для эндпоинтов, которые уже есть в схеме.
