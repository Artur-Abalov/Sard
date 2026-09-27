# 0005 — Путь Go-модулей и процедура его смены

- Статус: принято
- Дата: 2026-09-26

## Контекст
Репозиторий живёт на личном аккаунте (`github.com/Artur-Abalov/sard`); в будущем возможен перенос в организацию. Путь Go-модуля виден внешним пользователям SDK и proto-кода.

## Решение
Префикс `github.com/Artur-Abalov/sard` задан в:
- `go.mod` каждого модуля (`agent`, `agent/plugins/sdk`, `cli`, `proto/gen/go`, `tools`);
- переменной `MODULE_PATH` в `Makefile`;
- `go_package_prefix` в `proto/buf.gen.yaml`;
- импортах Go-кода (неизбежно для Go).

## Процедура смены (например, на `github.com/sard-dev/sard`)
1. `git grep -l 'github.com/Artur-Abalov/sard' | xargs sed -i 's#github.com/Artur-Abalov/sard#github.com/sard-dev/sard#g'`
2. `make proto` — перегенерировать `proto/gen/go` с новым `go_package`.
3. `make build test lint`.
4. Для внешних пользователей: старый путь продолжает резолвиться через редирект GitHub, но модуль с новым путём — это новый модуль. Выпустить тег под новым путём и оставить в README заметку о миграции импорта.

## Отвергнуто
- Vanity import path (`sard.dev/...`) сейчас — нужен домен и хостинг `go-import` meta; можно ввести позже той же процедурой.
