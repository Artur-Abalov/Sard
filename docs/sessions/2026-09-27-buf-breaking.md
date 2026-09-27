# Сессия 2026-09-27: buf breaking (P1)

## Решения
- `make breaking-proto`: `buf breaking` против `PROTO_BASE` (по умолчанию `origin/main`), категория `FILE` из `proto/buf.yaml`. Входит в `./scripts/gate.sh proto`.
- CI (`proto`): `fetch-depth: 0`; PR сравнивается со своей базой, пуш в `main` — с `github.event.before`.
- Kotlin-код по-прежнему генерирует protobuf-gradle-plugin в `:proto-jvm` (ADR 0002, 0010), а не `buf generate`: перенос не даёт выигрыша, а удалённые плагины BSR закрыты прокси.
- Правило в ADR 0007: ломающее изменение контракта — новый пакет `v2`.

## Проверено (команды запускались)
- `./scripts/gate.sh proto` — PASSED.
- Контроль: переименование поля `EnrollRequest.hostname` ловится `make breaking-proto` и против `origin/main`, и против SHA коммита (форма, которой пользуется CI на пуше).
