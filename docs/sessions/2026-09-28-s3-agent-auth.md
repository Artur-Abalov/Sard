# Сессия 2026-09-28: S3 — аутентификация агентов на gRPC

Ветка `claude/amazing-franklin-kt0xry` (назначена средой; в задании — `feat/s3-agent-auth`) от `main` @ `8f7efdc`. S2a в `main`: `V202609271200__enrollment.sql`, `persistence/AgentCertificateRecord.kt`.

## Фаза 1 — исследование

Два субагента только на чтение (gRPC/TLS/потоки; тенантность/S2a/ADR). Ключевые места перечитаны напрямую: `application.yaml:20-29`, `PkiAutoConfiguration.kt:57-77`, `TenantResolver.kt`, `TenantSessions.kt:15-45`, миграции, ADR 0013:47-49,75-84,185, ADR 0014:53, `Certificates.kt`.

### Находки

gRPC и TLS:
- Spring Boot 4.1.1 со своим стартером `spring-boot-starter-grpc-server` (`server/build.gradle.kts:35`): Spring gRPC 1.1.1, `grpc-netty` 1.83.1 (не shaded), grpc-kotlin 1.5.0 (версии — из BOM Boot 4.1.1 на Maven Central, локального кэша нет).
- Сервисы — `@GrpcService`, корутинные `*CoroutineImplBase`: `agents/EnrollmentGrpcService.kt:24-28`, `agents/AgentGrpcService.kt:13-14` (пустой, всё UNIMPLEMENTED). Перехватчиков нет (grep `ServerInterceptor|GlobalServerInterceptor|ServerBuilderCustomizer` пуст).
- TLS: `application.yaml:26-29` — `bundle: sard-grpc`, `client-auth: optional`, `secure: true`. Доверие — только сертификаты CA: `PkiAutoConfiguration.kt:70-77` (`trustOnly`, пустой `KeyStore` + `ca.caBundlePem()`, стандартный `TrustManagerFactory`). Обходящего проверку `TrustManager` в main нет.
- Чужой CA отклоняется на рукопожатии — уже проверено тестом: `GrpcTlsIntegrationTest.kt:128-133` ожидает `UNAVAILABLE`. Для теста 5 фиксируем: сертификат другого CA — отказ рукопожатия, до перехватчика не доходит (CERT_UNKNOWN для него недостижим).
- `Grpc.TRANSPORT_ATTR_SSL_SESSION` в коде не используется; полагаю, netty-транспорт его выставляет — проверю тестом в фазе 2.
- Потоки: обработчики — корутины; Enroll уходит в `withContext(enrollmentDispatcher)` = `Dispatchers.IO` (`EnrollmentConfiguration.kt:34-35`), executor gRPC не настроен (по умолчанию). Полагаю, grpc-kotlin добавляет `GrpcContextElement`, и `io.grpc.Context` перехватчика виден и после `withContext` — проверю тестом 6.
- Стартер тянет `grpc-services`: reflection включён по умолчанию, health — полагаю, тоже (условие `NotDisabledAndHasBindableServiceOrExplicitlyEnabled`). Оба сейчас доступны без сертификата.
- `proto-google-common-protos` 2.64.1 на classpath транзитивно через `grpc-protobuf`; в `docs/dependencies.md` не указан. Proto менять не нужно — `ErrorInfo` кладётся через `StatusProto`.

Тенантность и S2a:
- `TenantResolver` в ядре — константа `DEFAULT_TENANT_ID` (`TenancyAutoConfiguration.kt:15-19`, `@ConditionalOnMissingBean`); `ThreadLocal`/контекста запроса нет. Мост — `HibernateTenantBridge.kt:17-31`.
- Явный тенант — `TenantSessions.inTenant(id)` / `system { }` (`TenantSessions.kt:21-41`); вызовы `system` перечислены в ADR 0013:48-49 (сейчас один — `EnrollmentTokens.ownerOf`). Поиск сертификата по serial — второй вызов, вносится в список (журнал S2a:109).
- `agent_certificates`: `serial TEXT PK CHECK '^[0-9a-f]{32}$'`, `tenant_id`, `agent_id`, `issued_at`, `not_after`, `revoked_at` (`V202609271200__enrollment.sql:27-38`). Serial пишется `BigInteger.toString(16)`.
- **У `agents` нет `revoked_at`** (grep по миграциям: только `agent_certificates`). ADR 0013:76 планирует его на этапе «регистрация».
- SAN: `sard://tenants/<tenant>/agents/<agent>` (`Certificates.kt` профиль агента), CN = agent_id, EKU clientAuth. Парсера обратно нет.
- ADR 0014:53 и ADR 0013:185: тенант — «из сертификата, без запроса к базе». Задание S3 требует запроса по serial (отзыв) — расхождение, см. вопросы.

### Предлагаемый дизайн (ждёт ответов)

- `AgentAuthInterceptor` — `ServerInterceptor`, бин с `@GlobalServerInterceptor` (применяется ко всем сервисам, включая зарегистрированные в тесте), наивысший приоритет.
- Разрешено без сертификата — явное множество имён сервисов: `sard.agent.v1.EnrollmentService` (+ health, см. вопрос 4). Всё остальное: нет `SSLSession`/сертификата → `CERT_MISSING`; иначе разбор SAN → `system`-поиск по serial → проверки → `Contexts.interceptCall` с `AgentPrincipal(agentId, tenantId, serial)` в `Context.Key`.
- Отказ — `StatusProto` с `Status{UNAUTHENTICATED}` + `ErrorInfo(reason, domain="sard.dev")`; лог — reason, serial, agent_id.
- Проверка — при старте вызова (`interceptCall`), то есть при открытии стрима `Connect`; принципал живёт в `Context` всего вызова.
- Тенант в обработчиках — см. вопрос 1.
- Тестовый сервис без proto: `ServerServiceDefinition` с ручными `MethodDescriptor` (unary и bidi) на маршаллере байтов, регистрируется в тестовой конфигурации.
