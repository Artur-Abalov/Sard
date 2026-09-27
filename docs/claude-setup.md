# Перенос настроек Claude Code

Источник — репозиторий `Kanach-Luys` (Kotlin Multiplatform), только чтение.
Переносились только `CLAUDE.md`, `.claude/commands/`, `.claude/agents/`,
`.claude/skills/` и `.claude/settings.json`. В источнике нет `.claude/skills/`,
`settings.local.json` и блока `permissions`.

| Источник | Назначение | Решение | Причина |
|---|---|---|---|
| `CLAUDE.md` | `CLAUDE.md` | адаптировать (написан заново) | Сохранены структура (топология, определяющие ограничения, definition of done по модулям, правила, команды, заметки по языкам) и правила 1, 2, 4–6. Убраны KMP, Kover, `crap.py`, `gauntlet.sh`, graphify. |
| `.claude/agents/specifier.md` | `.claude/agents/specifier.md` | адаптировать | Путь `.feature` из `commonTest` заменён на `docs/specs/<module>/`. Добавлены инварианты Sard (агент не слушает порты, данные мимо сервера, ядро без расширений). |
| `.claude/agents/coder.md` | `.claude/agents/coder.md` | адаптировать | Команды Gradle KMP заменены на `go test`, `./gradlew :server:test`, `npm --prefix web test` и `./scripts/gate.sh`. Раздел KMP заменён разделом про стеки Sard, SPDX и зависимости. |
| `.claude/agents/cleaner.md` | `.claude/agents/cleaner.md` | адаптировать | Kover + `crap.py` заменены на `./scripts/crap.sh`. Оговорка про KMP заменена оговорками про байткод Kotlin, сгенерированный Go-код и веб. |
| `.claude/agents/architect.md` | `.claude/agents/architect.md` | адаптировать | graphify и KMP-граница измеримости заменены на `go list -deps` и `gradle dependencies`. Главная проверка — лицензионная граница Apache/AGPL и швы `crypto.Provider` / `sdk.Plugin`. |
| `.claude/agents/hardener.md` | `.claude/agents/hardener.md` | адаптировать | pitest отвергнут владельцем. Go — `go-mutesting`, Kotlin — mutflow (ADR 0006); оба запускаются через `./scripts/gate.sh <module>`. Разрешено только `mutflow:falsePositive` для доказанно эквивалентного мутанта. |
| `.claude/commands/ship-feature.md` | `.claude/commands/ship-feature.md` | адаптировать | Изменены только команды проверок (`gauntlet.sh` → `gate.sh`). |
| `.claude/commands/quick-fix.md` | `.claude/commands/quick-fix.md` | адаптировать | То же. |
| `.claude/commands/ship-client-feature.md` | — | не переносить | Целиком про KMP-клиент (`app/shared`, Android, iOS). |
| `.claude/commands/quick-fix-client.md` | — | не переносить | То же. |
| `settings.json`: хук PreToolUse Bash → `scripts/guard.sh` | `scripts/claude/guard.sh` | адаптировать (скрипт написан заново) | Идея универсальна. Сам скрипт лежит в `scripts/` источника, вне разрешённого списка, поэтому написан заново. Добавлены `go test -skip`, `-DskipTests`, `git reset --hard`, `git clean -f`; `rm -rf` разрешён только для каталогов сборки. |
| `settings.json`: хук PreToolUse Edit\|Write → `scripts/guard-edit.sh` | `scripts/claude/guard-edit.sh` | адаптировать (написан заново) | Защищённые файлы: `CLAUDE.md`, `.claude/settings.json`, `scripts/gate.sh`, `scripts/crap.sh`, `scripts/claude/*`, `.golangci.yml`, `config/detekt.yml`. Добавлен запрет `t.Skip`, `@Disabled`, `it.skip`/`.only`, `//nolint`, `eslint-disable`. |
| `settings.json`: хук PostToolUse Edit\|Write → `scripts/on-edit.sh` | `scripts/claude/on-edit.sh` | адаптировать (написан заново) | Маршрутизация по стекам Sard: `*.go` → `go vet` + `go test` пакета, `server/*.kt` → `:server:compileTestKotlin`, `*.proto` → `make lint-proto`, `web/src` → `npm run typecheck`. |
| `settings.json`: хук SubagentStop → `gauntlet.sh core fast` | `.claude/settings.json` | адаптировать | Теперь `./scripts/gate.sh all fast`. |
| `settings.json`: хуки PreToolUse graphify (×2) | — | не переносить | Абсолютный путь `/Users/…/.local/bin/graphify` с чужой машины; инструмента в Sard нет. |
| — | `.claude/settings.json` → `permissions` | написано заново | Разрешения для `make`, `./gradlew`, `go`, `npm`, `./.bin/buf`, `docker compose` и чтения git. |

## Найденные и удалённые чужие данные

- Абсолютный путь к домашнему каталогу автора источника (хуки graphify) — не перенесён.
- Имена модулей KMP (`core`, `composeApp`, `app/shared`, `:backend:`) — заменены.
- Секретов, токенов и адресов электронной почты в файлах настроек источника нет.

## Итоговое содержимое `.claude/`

`agents/{specifier,coder,cleaner,architect,hardener}.md`, `commands/{ship-feature,quick-fix}.md`, `settings.json` — совпадает с таблицей выше. Скрипты хуков — `scripts/claude/{guard,guard-edit,on-edit}.sh`.

Порядок работы (решение владельца): инструменты агентов и настройки перенесены **до** остальной работы; каркас (фазы 2–4) сделан без цепочки агентов, но через `scripts/gate.sh`; цепочка `/ship-feature` применяется к продуктовым фичам после каркаса.

## Проверка переноса (2026-09-27)

- `grep -rniE "kanach|/Users/|graphify|kover|gauntlet|commonMain|commonTest|iosMain|androidMain|composeApp|app/shared|crap\.py|credit-checker|:backend:|ktor" CLAUDE.md .claude scripts/claude` — совпадений нет.
- Konsist упоминается в `architect.md` как один из вариантов архитектурных тестов Kotlin — это общедоступная библиотека, а не имя из исходного проекта.
- Имя владельца `Artur-Abalov` встречается только как часть пути Go-модуля Sard (`github.com/Artur-Abalov/sard`) — согласовано с владельцем.
- Исходный репозиторий не изменялся: `git status` в его чекауте пуст.
- Каждая команда из `CLAUDE.md` запущена и завершилась с кодом 0 (список — в `docs/sessions/2026-09-26-skeleton.md`, фаза 5).
- Хуки проверены на примерах: `guard.sh` — 12 команд, `guard-edit.sh` — 7 правок (включая `mutflow:ignore` → блок, `mutflow:falsePositive` → разрешено), `on-edit.sh` — успешный и падающий Go-тест.
