<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 2026-10-12 — Q2: не обещать того, чего нет (заглушки плагинов и README)

Ветка `fix/q2-no-stub-plugins` от `main @ d10fb3b`. Волна «сейчас» финализации этапа 1,
принцип 5. Конфигурация и документация, не продуктовая функция: через `/ship-feature` не
идёт, но проходит `make gate`.

## Фаза 1: исследование

Ниже «проверил» — прочитал код сам; «полагаю» — не прослежено до конца.

### Где живут заглушки

- Проверил: `agent/plugins/builtin.go:20-25` — `Builtin()` = postgresql, mysql, files,
  network + `stand(...)`. Единственный потребитель в продукте — `cmd/sard-agent/main.go`
  через `plugins.Registry` и `plugins.Handlers`.
- Проверил: `agent/plugins/mysql` и `agent/plugins/network` — `Prepare`, `Dump`, `Stream`
  возвращают `sdk.ErrNotImplemented`. SDK (`agent/plugins/sdk`) на них не ссылается: примером
  для авторов плагинов они не служат.
- Проверил: тесты, которые сломаются от удаления из реестра:
  - `agent/plugins/stand_test.go:17` `TestReleaseBuildRegistersTheFourSourcePluginsOnly` —
    ровно `files, mysql, network, postgresql`;
  - `agent/plugins/stand_e2e_test.go` (тег `e2e`) — тот же список плюс `e2e-slow`;
  - `server/.../agents/AgentSeamIntegrationTest.kt:174-177` — строки `agent_plugins`
    настоящего агента.
- Проверил: `agent/plugins/builtin_test.go:61-82` `TestEveryPluginMethodIsNotImplementedYet`
  проверяет только mysql и network (остальные в `written`); без них станет пустым.
- Тесты, где `"mysql"` — просто строка (фейковые плагины, имя секрета), не затронуты:
  `sdk/registry_test.go`, `internal/app/app_test.go`, `internal/pluginhost/*_test.go`,
  `RegisterIntegrationTest.kt`, `SourcesIntegrationTest.kt` (по отчёту субагента; выборочно
  проверил `SourcesIntegrationTest` — там это плагин, которого у агента нет).
- Комментарии e2e `RunStepSeamTest.kt:72`, `ResultAckSeamTest.kt:110` перечисляют встроенные
  плагины — поправить текст.
- Проверил: ADR 0036, доказательство (а), называет четыре плагина — нужна поправка.

### Обещания в документации и консоли

- Проверил: `README.md:3-5` — «PostgreSQL, MySQL, files, MikroTik/Eltex configurations,
  later 1C»; `README.md:13-16` — статус называет files, но не postgresql.
- Проверил: `docs/design/DESIGN.md:10` — портрет пользователя «(PostgreSQL, MySQL, 1С,
  файлы, MikroTik/Eltex, S3)».
- По отчёту субагента: в `web/src` (страницы, моки, i18n) MySQL, MikroTik, Eltex не
  встречаются; `"network"` в локалях — сетевая ошибка. В `docs/operator`, `docs/plugins`,
  `docs/specs`, `docs/demo.md`, `examples/`, `deploy/` — нет.
- История (ADR 0051, журналы сессий) не правится.
- `docs/roadmap.md` нет.

### Источник с плагином, которого агент больше не объявляет

Register на каждом подключении заменяет набор плагинов агента целиком
(проверил: `registration/Registration.kt:16-23` и заголовок миграции
`V202609281400__agent_register.sql`). Источник при этом остаётся: в `sources.plugin`
внешнего ключа нет.

| Путь | Что сейчас | Проверил |
|---|---|---|
| `GET` списка и карточки источника | 200, плагин и конфиг как есть, без отметки | по отчёту |
| `PUT` с тем же плагином | 422 `unknown_plugin`, поле `plugin` | `AgentOffer.kt:52-63` |
| Форма правки в консоли | предупреждение `form.pluginGone`, выбор плагина заново | `SourceForm.tsx:227-250`, `ru.json:230` |
| Ручной запуск | 409 `unknown_plugin`, запуск не создаётся; консоль: «Агент не предлагает этот плагин» | `Runs.kt:179`, `ru.json:122` |
| Расписание | строка журнала `REFUSED`/`UNKNOWN_PLUGIN`, алерт после серии пропусков | `Firing.kt:29-35` |
| Шаг, уже стоявший в очереди | агент отклоняет «unknown plugin», запуск FAILED с причиной | `executor/command.go:107-112` |

Ошибок 500 и тихих провалов нет. Пробел: список и карточка источника не показывают, что
плагин пропал, — это видно только при запуске, правке или в журнале расписания.

Попутно (по отчёту, проверил `Runs.kt:179`): старт запуска вызывает `AgentOffer.require`
без `requireBackup`, в отличие от создания и правки.

### Контрольная точка 1: ответы владельца (2026-10-12)

1. Отметку «плагин недоступен» на карточке и в списке не делать: только убрать
   неработающие плагины.
2. Заглушки — удалить, без тега `dev`.
3. ADR и поправка к ADR 0036; плюс архитектурный тест.
4. План источников — на двух языках.
5. `DESIGN.md` — поправить.
6. `requireBackup` при старте запуска не проверять: плагин без `backup` во встроенном
   реестре — ошибка того, кто его пишет.

## Фаза 2: изменения

- Удалены `agent/plugins/mysql` и `agent/plugins/network`; `Builtin()` — `postgresql`,
  `files` (+ стенд).
- Тесты реестра: `TestReleaseBuildRegistersTheWorkingSourcePluginsOnly` (ровно `files`,
  `postgresql`), тест сборки стенда, `AgentSeamIntegrationTest` (строки `agent_plugins`).
  Удалён `TestEveryPluginMethodIsNotImplementedYet`: проверял только заглушки.
- Архитектурный тест `agent/plugins/release_test.go` (`!e2e`):
  `TestEveryPluginPackageIsRegisteredByABuild` и `TestEveryReleasePluginIsDocumented`.
  Тест сначала увидел падения: проверил, вернув пакет `mysql` без регистрации («registered
  by no build») и с регистрацией без документации («has no docs/plugins/mysql.md»), затем
  убрал его.
- ADR 0056; поправка доказательства (а) в ADR 0036; строка в `docs/adr/README.md`.
- `README.md`: первый абзац и статус — только files и postgresql, проверка восстановления
  и другие источники — «в плане» со ссылкой на `docs/roadmap.en.md`; ссылки на
  `docs/plugins/` и план в разделе документации.
- `docs/roadmap.md` и `docs/roadmap.en.md` — «план, не обещание».
- `docs/design/DESIGN.md:10` — портрет пользователя без перечня неработающих источников.
- Комментарии e2e `RunStepSeamTest`, `ResultAckSeamTest`.
- Консоль и i18n не менялись: неработающие источники там не названы (поиск по `web/src`).

Не тронуто намеренно: `README.md:11` («Sard owns sources, workflows, schedules, restore
verification, reports and multi-tenancy») — это раздел ответственности между Sard и
restic, а не список возможностей; переписать при Q3/B1.
