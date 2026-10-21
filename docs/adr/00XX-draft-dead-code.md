<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 00XX — Проверка мёртвого кода в шлюзе: deadcode и unparam (Go), knip (web), detekt (Kotlin) (черновик)

- Статус: черновик (номер — при слиянии); решение владельца 2026-10-21 («Все это добавляй. В kotlin используй detekt»)
- Дата: 2026-10-21
- Дополняет: ADR 0006 (мутационное тестирование), ADR 0011 (линтер веба)

## Контекст
Линтеры шлюза видели только неиспользуемое внутри файла или пакета: `unused` в golangci-lint —
неэкспортируемые имена, `tsc` (`noUnusedLocals`, `noUnusedParameters`) — локальные имена, detekt —
приватные члены. Код, достижимый, но ни на что не влияющий, находило только мутационное тестирование
(эквивалентные мутанты OQ-128, OQ-213, OQ-239), а экспортированное, но никем не вызываемое, —
ничто.

## Решение
1. **Go, `golang.org/x/tools/cmd/deadcode`** (`tools/go.mod`, `.bin/deadcode`). Шлюз
   (`gate_go_basic`, модули sdk, tools, agent, cli) запускает `deadcode -test -tags e2e ./...`
   и падает на любой строке вывода. Корни — `main` и тестовые бинари: функция, которую вызывает
   только тест, не мертва (тестовые пакеты `certtest` и `testplugin` иначе были бы мёртвыми
   целиком; что они не попадают в бинарь, проверяет `agent/cmd/sard-agent/imports_test.go`).
   Тег `e2e` добавляет плагины стенда e2e (`plugins/e2eslow`). Сгенерированный `proto/gen/go`
   не проверяется: `gate_gen` его только собирает.
2. **Go, `unparam`** в `.golangci.yml`: параметр, который всегда получает одно значение, и
   результат, который никто не читает, — в коде и в тестах.
3. **web, `knip`** (devDependency) в `npm run lint`: неиспользуемые файлы, экспорты, типы и
   зависимости npm. Точки входа — Vite, Vitest и `src/**/*.typecheck.{ts,tsx}` (проверки типов
   при компиляции, `web/knip.json`).
4. **Kotlin, detekt**: в 2.0.0-alpha.6 по умолчанию уже включены `UnusedParameter`,
   `UnusedPrivateClass`, `UnusedPrivateFunction`, `UnusedPrivateProperty`, `UnusedVariable`,
   `UnreachableCode`, `UnreachableCatchBlock`; `server/config/detekt.yml` включает выключенный
   по умолчанию `UnusedImport`.

## Порядок включения
Решение владельца 2026-10-21: код PostgreSQL агента не меняется до его переезда в плагин
(OQ-235). Там пять находок: `deadcode` — `restore.Held.Dir`; `unparam` —
`pluginhost.restoreDatabase` (параметр `step`) и три хелпера тестов `plugins/postgresql`. Поэтому:
- сразу: `knip` в `npm run lint`, правило `UnusedImport` detekt, `deadcode` в `tools/`, остальные
  находки исправлены;
- после OQ-235: пункты 1 и 2 в шлюзе (`scripts/gate.sh`, `.golangci.yml`; OQ-250).

## Последствия
- Находки исправлены без подавлений (правило 2 CLAUDE.md); до OQ-235 остаются только пять находок в коде PostgreSQL.
- Код, который вызывают только тесты (например, `enroll.WriteIdentity`, `hostcheck.CheckIDs`,
  `transport.RefusalLine`), проверкой не считается мёртвым: `deadcode` без `-test` перечисляет
  его, это отдельный разбор, не шлюз.
- Kotlin: публичный и `internal` код без вызовов detekt не видит — он проверяет файлы по
  отдельности. Инструмента в шлюзе для этого нет.
- Ещё один шаг шлюза Go: `deadcode` на модуле agent — 11 с в облачном окружении разработки
  (2026-10-21, анализ всей программы).

## Отклонённые варианты
- **`deadcode` без `-test`.** Шлюз падал бы на тестовых пакетах и на функциях-обёртках для
  тестов; их перенос в `_test.go` невозможен, когда ими пользуются тесты других пакетов.
- **`unparam` только вне тестов.** Исключение путей — то же подавление; тестовые хелперы с
  параметром-константой лгут о своём назначении так же, как код.

## Приложение: шлюз после OQ-235

`scripts/gate.sh` и `.golangci.yml` защищены (правит владелец); применить `git apply`:

```diff
--- a/scripts/gate.sh
+++ b/scripts/gate.sh
@@ -37,7 +37,7 @@
 }
 
 need_tools() {
-  [ -x "$BIN/crap" ] && [ -x "$BIN/golangci-lint" ] && [ -x "$BIN/go-mutesting" ] || make -C "$ROOT" tools
+  [ -x "$BIN/crap" ] && [ -x "$BIN/golangci-lint" ] && [ -x "$BIN/go-mutesting" ] && [ -x "$BIN/deadcode" ] || make -C "$ROOT" tools
 }
 
 # Go modules outside go.work (tools) are built with GOWORK=off.
@@ -51,6 +51,13 @@
   [ -z "$unformatted" ] || die "$m: gofmt needed: $unformatted"
   (cd "$dir" && GOWORK="$(go_env "$m")" go vet ./...) || die "$m: go vet"
   (cd "$dir" && GOWORK="$(go_env "$m")" "$BIN/golangci-lint" run --config "$ROOT/.golangci.yml" ./...) || die "$m: golangci-lint"
+  # Dead code (ADR 00XX-draft-dead-code): a function no main and no test reaches.
+  # The e2e tag adds the plugins of the e2e stand to the agent.
+  say "$m" "deadcode"
+  local dead
+  dead="$(cd "$dir" && GOWORK="$(go_env "$m")" "$BIN/deadcode" -test -tags e2e ./...)" || die "$m: deadcode"
+  [ -z "$dead" ] || die "$m: unreachable functions:
+$dead"
 }
 
 gate_go() {
--- a/.golangci.yml
+++ b/.golangci.yml
@@ -1,11 +1,13 @@
 # SPDX-License-Identifier: AGPL-3.0-only
 # Copyright 2026 Artur Abalov
 #
-# Default golangci-lint linters plus a cyclomatic complexity gate (<= 8).
+# Default golangci-lint linters plus a cyclomatic complexity gate (<= 8) and
+# unparam: parameters and results that never carry anything (ADR 00XX-draft-dead-code).
 version: "2"
 linters:
   enable:
     - gocyclo
+    - unparam
   settings:
     gocyclo:
       min-complexity: 8
```
