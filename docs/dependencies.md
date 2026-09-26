# Прямые зависимости

Каждая сторонняя библиотека должна быть нужна каркасу сейчас. Лицензии сверены по файлам LICENSE в кэше модулей / POM на Maven Central / package.json в npm.
Совместимость: всё, что попадает в бинарники ядра, совместимо с AGPL-3.0; в Apache-2.0-модулях (`proto/gen/go`, `agent/plugins/sdk`) — только пермиссивные лицензии.

## Go

| Модуль | Зависимость | Версия | Лицензия | Где | Зачем |
|---|---|---|---|---|---|
| proto/gen/go (Apache-2.0) | google.golang.org/protobuf | v1.36.12 | BSD-3-Clause | runtime | сообщения protobuf |
| proto/gen/go (Apache-2.0) | google.golang.org/grpc | v1.84.0 | Apache-2.0 | runtime | gRPC-стабы `AgentService` |
| agent/plugins/sdk (Apache-2.0) | — | — | — | — | только stdlib |
| agent | google.golang.org/grpc, google.golang.org/protobuf | см. выше | Apache-2.0, BSD-3-Clause | runtime (через proto/gen/go) | транспорт к серверу |
| agent | go.yaml.in/yaml/v3 | v3.0.5 | MIT + Apache-2.0 | runtime | чтение YAML-конфига (поддерживаемый преемник архивного gopkg.in/yaml.v3) |
| agent | github.com/santhosh-tekuri/jsonschema/v6 | v6.0.3 | Apache-2.0 | **только тесты** | проверка `ConfigSchema()` плагинов по метасхеме draft 2020-12 |
| cli | github.com/spf13/cobra | v1.10.2 | Apache-2.0 | runtime | дерево подкоманд `sardctl` |
| cli | github.com/spf13/pflag (транзитивно) | v1.0.9 | BSD-3-Clause | runtime | флаги для cobra |

## Инструменты разработки (`tools/go.mod`, в бинарники не попадают)

| Инструмент | Версия | Лицензия | Зачем |
|---|---|---|---|
| github.com/bufbuild/buf | v1.73.0 | Apache-2.0 | lint и генерация proto |
| google.golang.org/protobuf/cmd/protoc-gen-go | v1.36.12 | BSD-3-Clause | генерация Go-сообщений |
| google.golang.org/grpc/cmd/protoc-gen-go-grpc | v1.6.2 | Apache-2.0 | генерация gRPC-стабов |
| github.com/golangci/golangci-lint/v2 | v2.14.0 | GPL-3.0 | линтер; запускается, не линкуется и не распространяется — на лицензию Sard не влияет |
| github.com/avito-tech/go-mutesting | v0.0.0-20251226130216-48d0401f00fb | MIT | мутационное тестирование Go |
