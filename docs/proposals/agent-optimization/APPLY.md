<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# Применение предложений по оптимизации конвейера агентов

Файлы в `scripts/claude/` и `.claude/settings.json` защищены `guard-edit.sh`;
агент их не правит. Здесь — готовые изменения, которые применяет владелец.
Сессия: `docs/sessions/2026-10-19-agent-pipeline-optimization.md`.

## Что применить (из корня репозитория)

```bash
git apply docs/proposals/agent-optimization/gate-changed.patch
git apply docs/proposals/agent-optimization/settings.patch
install -m 0755 docs/proposals/agent-optimization/scripts/arch-edges.sh scripts/claude/arch-edges.sh
install -m 0755 docs/proposals/agent-optimization/scripts/test-quiet.sh scripts/claude/test-quiet.sh
```

`docs-index.sh` уже лежит в `scripts/claude/` и ничего не требует.

Почему два скрипта поставляются файлами, а не лежат сразу в `scripts/claude/`:
охрана блокирует правку файла в `scripts/claude/` сразу после его создания,
а в обоих нашлись дефекты уже после создания (первая версия `arch-edges.sh`
считала пустой результат домена нарушением; `test-quiet.sh` не знал, что
`tools/` вне `go.work`). Исправленные версии проверены, первые удалены.

## Проверка после применения

```bash
./scripts/test-claude-helpers.sh      # 23 проверки, около 2 с
./scripts/claude/arch-edges.sh        # exit 0, строки PASS
./scripts/claude/docs-index.sh && git status --short docs   # второй запуск ничего не меняет
make license-check
```

Ручная проверка из задачи: запустить один `/quick-fix` с заведомо падающим тестом и
убедиться, что хук по-прежнему сообщает о падении агенту `coder` (кэш не должен
маскировать провал: провал не кэшируется, это проверено в `test-claude-helpers.sh`).

## Что патч `gate-changed.sh` меняет и чего больше не проверяет

1. Для `agent_type` = `specifier` или `architect` хук ничего не запускает: эти
   агенты не меняют файлы. Пустой или неизвестный `agent_type` по-прежнему
   запускает шлюзы (в измеренном дампе пустое значение было у 4 из 9 событий).
2. Если дерево (разница с базой плюс неотслеживаемые файлы) и список модулей
   совпадают с последним **прошедшим** запуском, шлюз пропускается. Кэш лежит в
   `build/.gate-ok/` (в `.gitignore`). `GATE_NO_CACHE=1` отключает его.
   Не проверяется при пропуске: внешнее состояние, не входящее в хеш дерева
   (Docker, сеть, обновлённые зависимости), и повторный прогон нестабильного теста.
   Шлюзы, которые `coder` и `cleaner` запускают сами через `gate.sh`, кэш не затрагивает.
3. Полный CI и `make gate` не затронуты.
