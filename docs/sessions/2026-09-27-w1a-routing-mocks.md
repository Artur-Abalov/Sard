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

### Ответы владельца на вопросы фазы 1
1. Заменить React Router на TanStack Router — да, с ADR.
2. Тест роутинга — вариант (а): без рендера, окружение `node`, ADR 0011 не меняется по сути.
3. `public/mockServiceWorker.js` не коммитим.
4. Воркер держим вне `public/` и отдаём только в dev, чтобы в `dist` не было ничего от MSW.
5. Дашборд остаётся на `/`.
6. QueryClient в контексте роутера; в тестах новый с `retry: false`.
7. Только маршруты из задачи.

## Фаза 2 — роутинг, макет, layout-маршрут с beforeLoad

### Решения и причины
- TanStack Router, маршруты файлами в `web/src/routes/`, дерево генерирует `@tanstack/router-plugin` (ADR 0014). `routeTree.gen.ts` коммитится: `tsc -b` в гейте идёт раньше сборки и на чистом клоне без этого файла падает. SPDX-заголовок ставит сам плагин (`routeTreeFileHeader`), license-check не менялся.
- Дерево: `__root` (Root + devtools в dev, `notFoundComponent`) → `_app` (безпутевой, `beforeLoad: guard(location)`, компонент `Layout` с AppShell) → `/`, `/agents`, `/sources`, `/runs`, `/runs/$runId`.
- `guard` вынесен в `src/auth/guard.ts` как отдельная функция. W1b меняет только её тело, а тест проверяет вызов через `vi.mock(..., { spy: true })` без внедрения тестовых хуков в контекст роутера.
- Компоненты не объявляются в файлах маршрутов: правило oxlint `only-export-components` при `--deny-warnings` роняет lint. Добавлено `allowExportNames: ["Route"]` — это настройка под соглашение роутера, а не отключение правила. `Root` вынесен в `src/Root.tsx`, карточка запуска читает параметр через `getRouteApi`.
- `languages` вынесен из `i18n.ts` в `src/languages.ts`. Иначе импорт `Layout` в тесте запускал инициализацию i18next, которая обращается к `document` и падает в окружении `node` (`ReferenceError: document is not defined`, `src/i18n.ts:13`).
- 404 в тесте проверяется публичным API: совпал только `__root__`, и у него `notFoundComponent === NotFound`. Флаг `_notFound` — внутреннее поле роутера, на него не опираюсь.
- Навигация — `createLink` над Mantine `NavLink`, активный пункт через `activeProps={{ active: true }}`.
- На 404 добавлена ссылка на дашборд: без неё со страницы некуда уйти (строки в i18n).

### Отвергнуто
- Внедрять `guard` через контекст роутера — это проектирование API сессии, работа W1b.
- Проверять 404 через `match._notFound` — внутреннее поле, сломается при обновлении.
- Условие `typeof document` в `i18n.ts` ради тестов — ветка ради теста вместо разделения модуля.

### Проверено (команды запускались)
- Тесты написаны первыми и падали: модулей `./auth/guard` и `./router` не было.
- `npm run lint`, `npm run typecheck`, `npm test` (15 тестов), `npm run build` — exit 0. `./scripts/gate.sh web` — PASSED. `./scripts/license-check.sh` — 116 файлов OK.
- Контроль теста защиты: без строки `beforeLoad` в `_app.tsx` падают 5 из 15 тестов.
- Контроль типизации: если под `@ts-expect-error` поставить существующий путь, tsc выдаёт `TS2578 Unused '@ts-expect-error'`.
- Devtools: в `dist/assets` только `index-*.js` и `index-*.css`, чанка `Devtools-*` нет. Контрольная сборка с `true` вместо `import.meta.env.DEV` создаёт чанк `Devtools-*.js`. `grep -i devtools dist` находит только встроенный в React `__REACT_DEVTOOLS_GLOBAL_HOOK__`.
- Браузер (Playwright, dev-сервер без бэкенда):
  - `/` → «Дашборд»; клик «Агенты» → `/agents`; клик «Запуски» → `/runs`, пункт подсвечен (`data-active`).
  - `/runs/42` → «Запуск 42»; `/nope` → «Страница не найдена» без навбара.
  - Запрос `/api/v1/status` ушёл через прокси на `:8080` (`ECONNREFUSED`).
- Headless Chromium без локали даёт в devtools Query `RangeError: Incorrect locale information provided` (`new Intl.Locale` внутри devtools). С `locale: 'ru-RU'` ошибок нет. Это только dev, не наш код.

### Изменённые файлы
`web/package.json`, `web/package-lock.json`, `web/vite.config.ts`, `web/.oxlintrc.json`, `web/.prettierignore`, `web/src/main.tsx`, `web/src/i18n.ts`, `web/src/languages.ts`, `web/src/App.tsx` → `web/src/Layout.tsx`, `web/src/Root.tsx`, `web/src/Devtools.tsx`, `web/src/NavLink.tsx`, `web/src/router.ts`, `web/src/router.test.ts`, `web/src/links.typecheck.tsx`, `web/src/auth/guard.ts`, `web/src/auth/guard.test.ts`, `web/src/routes/**`, `web/src/routeTree.gen.ts`, `web/src/pages/{Agents,Sources,Runs,Run,NotFound}.tsx`, `web/src/locales/{ru,en}.json`, `docs/adr/0014-tanstack-router.md`, `docs/adr/{0001,0011,README}.md`, `docs/dependencies.md`.

### Открытые вопросы
- Актуальность `routeTree.gen.ts` в CI не проверяется отдельно: плагин пересоздаёт файл при `npm test` и `npm run build`, но гейт не сверяет его с `git diff`. Предлагаю добавить проверку в CI по образцу `schema.d.ts` — не сделано, ждёт решения.
- Страница 404 рендерится без AppShell, потому что маршрут вне `_app`. Для страницы входа (W1b) это правильно; нужна ли оболочка на 404 — вопрос.

## Ревью фазы 2
- Владелец: проверять актуальность `routeTree.gen.ts` в CI — да. 404 без AppShell — оставить.

### Проверка дерева маршрутов в CI
- Первая попытка — `git diff` после гейта — не работает. Контроль показал, что `npm run build` на устаревшем дереве падает на `tsc -b`, до плагина не доходит, и файл остаётся устаревшим.
- Решение: `npm run gen:routes` (`web/scripts/gen-routes.mjs`) вызывает `@tanstack/router-generator` 1.167.38. Это та же единственная копия, что использует плагин (`npm ls` показывает `deduped`). Настройки перенесены из `vite.config.ts` в `web/tsr.config.json`, их читают и плагин, и скрипт. В CI шаг «Generated route tree matches src/routes» стоит до гейта, по образцу `schema.d.ts`. Каталог `.tanstack/` (временные файлы генератора) добавлен в `web/.gitignore`.
- Отвергнуто: `@tanstack/router-cli` — лишние yargs и chokidar и своя закреплённая версия генератора.

### Проверено (команды запускались)
- Плагин (`vite build`) и `gen:routes` дают байт-в-байт одинаковый файл: `git diff --exit-code` после сборки пуст.
- Если испортить дерево, `gen:routes` восстанавливает его, и проверка проходит.
- Если добавить маршрут без перегенерированного дерева, `gen:routes` + `git diff --exit-code` дают exit 1.
- `./scripts/gate.sh web` — PASSED, license-check — 117 файлов OK.
- Сам CI-шаг в GitHub Actions не запускался — не проверено.
