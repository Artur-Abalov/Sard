# 0011 — Инструменты веба: oxlint вместо ESLint, TypeScript 6

- Статус: принято
- Дата: 2026-09-27

## Контекст
Задача предполагала «ESLint + Prettier из шаблона Vite». Актуальный шаблон `create-vite` (Vite 8) генерирует проект с **oxlint** и TypeScript 6; ESLint в нём больше нет.

## Решение
- Линтер — oxlint из шаблона (+ правило `eslint/complexity` с порогом 8, проверено на функции со сложностью 10), форматирование — Prettier. `npm run lint` = `oxlint --deny-warnings && prettier --check .`.
- TypeScript 6 из шаблона. `openapi-typescript` 7.13 (последняя) объявляет peer `typescript ^5`; несовпадение принято явно через `overrides` в `web/package.json`. Проверено: генерация `src/api/schema.d.ts` и `tsc -b` проходят.
- Тесты — Vitest, только чистые функции (`src/format.ts`); компонентных тестов нет (решение владельца: UI-тесты дорогие). Маршруты тестируются без рендера (ADR 0014).
- Клиент API: `make openapi` выгружает `/v3/api-docs` из интеграционного теста сервера в `web/src/api/openapi.json` (коммитится), `npm run gen:api` генерирует `schema.d.ts` (коммитится, с SPDX-заголовком). CI проверяет, что оба файла актуальны.

## Отвергнуто
- Добавить ESLint поверх шаблона — лишние зависимости, дублирует oxlint.
- Откатить TypeScript до 5.9 ради peer-зависимости — если override перестанет работать, это запасной вариант.
