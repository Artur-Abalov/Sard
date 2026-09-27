# 0006 — Мутационное тестирование: go-mutesting (Go) и mutflow (Kotlin)

- Статус: принято
- Дата: 2026-09-27

## Контекст
Шлюз качества (ADR 0007) требует мутационного тестирования. pitest отвергнут владельцем по опыту: на байткоде Kotlin он порождает мусорных мутантов (null-проверки компилятора, state machine корутин, bridge-методы аргументов по умолчанию, методы data class).

## Решение
- **Go** — `avito-tech/go-mutesting` (MIT), порог score ≥ 0.80 в `scripts/gate.sh`.
- **Kotlin** — `mutflow` 1.5.0 (Apache-2.0, Maven Central): плагин компилятора K2, мутирует IR до генерации байткода, поэтому мутанты — в написанном коде, а не в сгенерированном компилятором.
  - Режим STRICT: **любой** выживший мутант роняет сборку — строже порога 0.80.
  - Тестовые классы помечаются `@MutFlowTest`, проверяемый вызов оборачивается в `MutFlow.underTest { }`; мутируется только код, достигнутый внутри таких блоков. Неохваченный код ловит шлюз покрытия (JaCoCo ≥ 80%).
  - mutflow компилирует исходники второй раз (`mutatedMain`), и JaCoCo в этом прогоне видит 0%. Поэтому по умолчанию `mutflow.enabled=false` (`gradle.properties`), а `scripts/gate.sh server` делает два прогона: `:server:check` (покрытие) и `-Pmutflow.enabled=true :server:test --rerun` (мутации).
  - Цели мутаций: `dev.sard.server.**`. Сгенерированный proto-код живёт в отдельном проекте `proto/jvm` и не мутируется.
  - Подавление: `// mutflow:falsePositive <причина>` допустим только для доказанно эквивалентного мутанта. `mutflow:ignore`, `@SuppressMutations`, режимы `LENIENT`/`DISABLED` запрещены хуком `scripts/claude/guard-edit.sh`.

## Проверено
- На каркасе mutflow нашёл и убил 4/4 мутанта в `ExtensionRegistry`. В `StatusController` мутантов 0: набор операторов mutflow не мутирует elvis (`?:`) и структурный код — это ограничение инструмента, а не признак полноты тестов.

## Отвергнуто
- pitest (+ платный плагин Arcmutate) — решение владельца.
- kaputt — есть процентный порог и отчёты в формате pitest, но не опубликован, один автор, месяц истории.
- komust, mutant-kraken, stryker4k — альфа, заброшены или не поддерживают Kotlin/JVM.

## Последствия
- Каждый релиз mutflow жёстко привязан к версии Kotlin (1.0.2+ → 2.4.x): обновлять Kotlin и mutflow вместе.
- Интеграция mutflow с Gradle требует явной зависимости `processMutatedMainResources → bootBuildInfo` (`server/build.gradle.kts`).
