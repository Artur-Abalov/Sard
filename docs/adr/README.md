# Architecture Decision Records

| № | Решение |
|---|---|
| [0001](0001-react-web-ui.md) | React для веб-интерфейса |
| [0002](0002-generated-go-proto-committed.md) | Go-код из proto коммитится |
| [0003](0003-mantine-ui-kit.md) | Mantine как UI-кит |
| [0004](0004-hybrid-license-and-cla.md) | Гибридная лицензия AGPL-3.0 + Apache-2.0 и CLA |
| [0005](0005-go-module-path.md) | Путь Go-модулей и процедура его смены |
| [0006](0006-mutation-testing.md) | Мутационное тестирование: go-mutesting и mutflow |
| [0007](0007-quality-gates.md) | Шлюзы качества и инструменты |
| [0008](0008-crypto-provider.md) | Модель доверия: сервер — управляющая плоскость, ключи на хостах |
| [0009](0009-agent-transport-mtls.md) | Транспорт агента: исходящий gRPC и mTLS (план) |
| [0010](0010-server-stack.md) | Стек сервера: Spring Boot 4.1, встроенный gRPC, proto/jvm |
| [0011](0011-web-tooling.md) | Инструменты веба: oxlint, TypeScript 6 |
| [0012](0012-tree-deviations.md) | Расхождения с целевым деревом каталогов |
| [0013](0013-multitenancy-and-schema.md) | Мультитенантность и схема БД сервера |
| [0014](0014-local-ca.md) | Локальный CA сервера (D4) |
| [0015](0015-tanstack-router.md) | TanStack Router вместо React Router, тесты маршрутов без рендера |
| [0016](0016-msw-api-mocks.md) | Моки API на MSW с типами из OpenAPI |
| [0017](0017-restic-shipped-with-agent.md) | restic поставляется вместе с агентом |
| [0018](0018-agent-packaging.md) | Упаковка агента: tar.gz, deb, rpm с restic и лицензиями |
| [0019](0019-rest-contract-stage1.md) | Контракт REST API этапа 1: заглушки на сервере и общие соглашения |
| [0020](0020-e2e-harness.md) | Сквозные тесты: Testcontainers для JVM, образы из текущего кода |
| [0021](0021-admin-password-login.md) | Вход администратора по паролю (решение D2) |
| [0022](0022-one-active-run-per-source.md) | Один активный запуск на источник (решение D6) |
| [0023](0023-agent-re-enrollment.md) | Повторная регистрация агента и клоны ВМ (решение D5) |
| [0024](0024-notification-policy.md) | Политика уведомлений (решение D7) |
| [0025](0025-grpc-error-model.md) | Модель ошибок gRPC: `google.rpc.ErrorInfo` с доменом `sard.dev` |
| [0026](0026-agent-stream-manager.md) | Менеджер стримов агентов: реестр в памяти и правило дубликата |
| [0027](0027-plugin-sdk.md) | SDK плагинов-источников: шаги, два способа бэкапа, валидация, секреты |
