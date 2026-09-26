# 0002 — Go-код из proto коммитится

- Статус: принято
- Дата: 2026-09-26

## Контекст
Контракт сервер ↔ агент описан в `proto/sard/agent/v1/agent.proto`. Go-код нужен агенту и сторонним плагинам; Kotlin/Java-код нужен только серверу.

## Решение
- Go-код генерируется `make proto` (buf + локальные `protoc-gen-go` и `protoc-gen-go-grpc`, версии закреплены в `tools/go.mod`) в `proto/gen/go` — отдельный Go-модуль `github.com/Artur-Abalov/sard/proto/gen/go` под Apache-2.0 — и **коммитится**.
- Kotlin/Java-код генерируется при сборке сервера (protobuf-gradle-plugin + grpc-kotlin) и не коммитится.
- CI и `scripts/gate.sh proto` проверяют, что `make proto` не даёт диффа.

## Причины
- Сторонний плагин и SDK импортируют контракт обычным `go get`, без buf/protoc в своей сборке.
- Сборка агента и CLI не требует генератора — меньше инструментов в цепочке и в CI.
- Серверу генерация при сборке ничего не стоит: protobuf-gradle-plugin скачивает `protoc` из Maven Central.

## Отвергнуто
- Удалённые плагины Buf Schema Registry в `buf.gen.yaml`: сетевой доступ к buf.build при каждой генерации, в окружении разработки он закрыт; локальные плагины воспроизводимее.
- `clean: true` в `buf.gen.yaml`: удалил бы `proto/gen/go/go.mod`; устаревшие файлы ловит проверка диффа.

## Последствия
- Изменение `.proto` без `make proto` роняет CI.
- Версии генераторов меняются только через `tools/go.mod`.
