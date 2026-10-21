# 2026-10-21 — разбор техдолга по реестру открытых вопросов

Не продуктовая фича: без конвейера `/ship-feature`. Механика (тесты, рефакторинг
без смены поведения, CI, web) и правка документации.

## Решения владельца

- Код агента, связанный с PostgreSQL, не трогать (OQ-213 оставлен открытым;
  правка `agent/internal/restore/postgres_write.go` откачена).
- OQ-048 и OQ-071 делаются отдельно.
- В этой сессии — только механика пункта 1 разбора и правка документации;
  пункты, ждущие решения владельца (OQ-154, OQ-137, OQ-156, OQ-138, OQ-155,
  OQ-236, OQ-036), не начинались.

## Базовая линия до правок

`./scripts/gate.sh <m> fast`: sdk, tools, agent, cli — PASSED. `./scripts/crap.sh`:
худший CRAP 6.0 (порог 6) — десяток функций `cmd/sard-agent` и три в `tools`.
TODO/FIXME/XXX/HACK в коде нет. Гейт `server` не прогонялся: Docker-демон в
окружении не запущен (OQ-095).

## Сделано

- OQ-087, OQ-094, OQ-108, OQ-127, OQ-128, OQ-129, OQ-145, OQ-214, OQ-215,
  OQ-234 закрыты — подробности в «Закрыто при сверке», 2026-10-21.
- `agent/internal/enroll/classify_test.go`: `dialClassifyServerManual` берёт
  `t.Cleanup(srv.Stop)` (необязательное замечание architect, журнал
  2026-10-13-gate-mutation-performance).
- README: тег быстрого старта `v0.1.0-beta.1`; восстановление из консоли есть,
  «пока нет» — восстановления поверх данных (OQ-230).
- В реестр внесены пункты журналов, которых в нём не было: OQ-237…OQ-249.

## Проверка

- `./scripts/gate.sh web` — PASSED.
- `./scripts/gate.sh agent fast` — PASSED (после всех правок агента).
- go-mutesting по `agent/internal/redact/base64.go` — 1.000 (20/20),
  `agent/internal/redact/redact.go` — 1.000 (92/92, 18 skipped). До правки:
  0.950 и 0.974 (1 и 3 выживших).
- Новые тесты видели красными: OQ-127 — с убранным из списка модулем,
  OQ-128 — на мутанте `3→4`, OQ-129 — с выключенной проверкой контекста.

## Открыто

- Мутационный прогон всего модуля agent (`./scripts/gate.sh agent`) не
  запускался: менялись только `redact` и тесты.

## Проверка мёртвого кода (решение владельца: «Все это добавляй. В kotlin используй detekt»)

ADR `docs/adr/00XX-draft-dead-code.md`.

- Инструменты: `golang.org/x/tools/cmd/deadcode` v0.50.0 в `tools/go.mod` и `GO_TOOLS`
  (`make tools`); `knip` 6.40.0 в `web` (`npm run lint`, `web/knip.json`).
- Находки при включении (вывод инструментов):
  - `deadcode -test -tags e2e`: agent — 1 (`restore.Held.Dir`), cli, tools, sdk — 0. Без `-test`
    — 54, из них 45 в тестовых пакетах `certtest`, `testplugin` и плагине стенда `e2eslow`.
  - `unparam`: agent — 29 (2 в коде: `certpair.decide`, параметр `now`, и
    `pluginhost.restoreDatabase`; 27 в тестах). Исправлено 25, остались 4 в коде PostgreSQL.
  - `knip`: 22 неиспользуемых экспорта и 3 типа — всё использовалось только внутри своего
    файла, `export` снят; `tsc` чистый.
  - detekt с `UnusedImport` (конфиг подменён init-скриптом Gradle, проверка подмены — порог
    сложности 1 даёт 1675 нарушений): 0 находок.
- Код PostgreSQL агента не менялся (решение владельца): пять находок — OQ-250; `deadcode` и
  `unparam` в шлюз включаются после OQ-235 патчем из приложения ADR.
- `scripts/gate.sh`, `.golangci.yml`, `server/config/detekt.yml` защищены: правку `detekt.yml`
  (`UnusedImport`) применяет владелец.
- `./scripts/gate.sh web` — PASSED; markdownlint по изменённым документам — 0 ошибок.
