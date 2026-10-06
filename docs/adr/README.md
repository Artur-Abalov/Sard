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
| [0028](0028-schema-i18n-x-sard-i18n.md) | Переводы `config_schema` плагинов: ключ `x-sard-i18n` |
| [0029](0029-files-plugin-agent-decisions.md) | Плагин files (A6b): RestoreDeferred, OneFileSystem, `--retry-lock`, Ф12, разбор stderr |
| [0030](0030-repo-init-lock.md) | Блокировка `sard-agent repo init` в `restic.cache_dir` |
| [0031](0031-server-config-validation.md) | Проверка конфига источника на сервере: JSON Schema 2020-12 |
| [0032](0032-rest-api-stage1-implementation.md) | Реализация REST API этапа 1 (S8b) |
| [0033](0033-step-log-redaction.md) | Маскирование логов и результата шага на агенте (A7c, A7b) |
| [0034](0034-console-w2-decisions.md) | Консоль W2: признак `partial`, обзор по запросу, адресная строка |
| [0035](0035-console-design-system.md) | Дизайн-система консоли: токены в `theme.ts`, шрифты, статусы этапа 1 |
| [0036](0036-e2e-stand-plugin-build-tag.md) | Плагин стенда e2e (`e2e-slow`) за тегом сборки; CA и база стенда в томах (T3s) |
| [0037](0037-agent-install-from-server.md) | Установка агента с сервера: адрес раздачи, версии, ключ релизов (U1b) |
| [0039](0039-server-image-release.md) | Образ сервера и релиз: GHCR, amd64 и arm64, офлайн-архив |
| [0040](0040-console-served-by-server.md) | Консоль отдаёт sard-server и она входит в образ: jar, SPA-fallback, кэш, CSP, версия |
