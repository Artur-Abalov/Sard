# Сессия 2026-09-27: W1a — роутинг и моки API (техническая часть)

Ветка: `claude/routing-api-mocks-jutq7g` (в задаче названа `feat/w1a-routing-mocks`, но среда сессии требует эту).

## Фаза 1 — инвентаризация

### Проверено (файлы прочитаны, команды запускались)
- `web/node_modules` отсутствует; `git status` чистый.
- Установленные `@tanstack/*`: только `@tanstack/react-query` 5.104.0 и транзитивный `@tanstack/query-core` 5.104.0 (`web/package-lock.json`). Router, router-plugin, devtools, msw, openapi-msw, jsdom/happy-dom, @testing-library не установлены.
- Роутинг сейчас — **react-router 8.4.0**: `web/src/main.tsx` (`BrowserRouter`), `web/src/App.tsx` (`<Routes><Route path="/" …>`). ADR 0001 (`docs/adr/0001-react-web-ui.md:10`) выбирает React Router.
- QueryClient создаётся в `web/src/main.tsx` с настройками по умолчанию; `Dashboard.tsx` зовёт `useQuery({ queryKey: ['status'], queryFn: fetchStatus })`.
- Vitest: `web/vite.config.ts:20-21` — `include: ['src/**/*.test.ts']`, `environment: 'node'`, setup-файлов нет.
- ADR 0011 (`docs/adr/0011-web-tooling.md:12`) и CLAUDE.md: только чистые функции, компонентных тестов нет.
- `scripts/license-check.sh` требует SPDX в `*.ts`/`*.js` из `git ls-files --cached --others --exclude-standard`; исключений для сгенерированных файлов нет (`schema.d.ts` проходит, потому что `gen-api.mjs` дописывает заголовок).
- `.oxlintrc.json` и `.prettierignore` исключают `dist` и `src/api/schema.d.ts`.
- `tsconfig.app.json` без `strict`.
- `src/api/client.ts` экспортирует только `fetchStatus` и тип `Status`; `client` не экспортируется.
- Web-гейт (`scripts/gate.sh:114-118`): lint, typecheck, test, build; покрытия и мутаций для web нет.

### Противоречия задачи с репозиторием (вынесены в вопросы)
1. Задача требует TanStack Router, а ADR 0001 и код используют React Router. Нужно новое ADR (0014), которое заменит часть ADR 0001.
2. Тест роутинга (рендер разделов, 404) — это компонентный тест. Он противоречит ADR 0011 и CLAUDE.md, требует DOM-окружения (новая зависимость) и `*.test.tsx` в `include`.
3. `mockServiceWorker.js` в `public/` и возможный `routeTree.gen.ts` не пройдут license-check без заголовка или исключения.

### Открытые вопросы
Заданы владельцу одним списком; код до ответа не пишется.
