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

### Решения владельца по вопросам фазы 1

1. Тенант в обработчиках — явно: `AgentPrincipal` из gRPC `Context` → `TenantSessions.inTenant(principal.tenantId)`; `TenantResolver` не трогаем.
2. Запрос по serial есть; тенант и агент записи обязаны совпасть с URI SAN, иначе `CERT_IDENTITY_MISMATCH`.
3. Миграция `agents.revoked_at`.
4. Health — без сертификата; reflection выключен.
5. Отдельный код `CERT_EXPIRED`.
6. `proto-google-common-protos` — явная зависимость и строка в `docs/dependencies.md`.

## Фаза 2 — перехватчик, политика, коды отказов (тесты 1–5)

Окружение сессии (в репозиторий не попадает): JDK 25 из apt (`openjdk-25-jdk-headless`), запущен `dockerd`, образ `postgres:18-alpine`; Maven Central отвечал 429 — зеркало Maven Central в `~/.gradle/init.d/mirror.gradle.kts`.

Тесты писались первыми (красный → зелёный): `AgentIdentityTest`, `AgentAuthenticatorTest`, `AgentAuthStatusTest`, затем `AgentAuthIntegrationTest`.

Код:
- `pki/CertificateAuthority.kt` — `AgentIdentity.uri()`, `parse(uri)` (строго `sard://tenants/<uuid>/agents/<uuid>`, нижний регистр), `of(X509Certificate)`; `Certificates.agent` строит SAN через `uri()` — формат в одном месте.
- `agents/AgentAuthentication.kt` — `AgentAuthFailure` (`CERT_MISSING, CERT_UNKNOWN, CERT_IDENTITY_MISMATCH, CERT_REVOKED, CERT_EXPIRED, AGENT_REVOKED`), `AgentPrincipal` (+ `Context.Key`), `PresentedCertificate` (serial `toString(16)` — как пишет Enroll), `CertificateStanding`, `AgentAuthenticator`. Порядок проверок: нет сертификата → нет записи → SAN ≠ запись → сертификат отозван → `now >= not_after` записи → агент отозван.
- `agents/AgentCertificateStandings.kt` — второй вызов `TenantSessions.system`: HQL `AgentCertificateRecord join Agent` по serial (вносится в список ADR 0013 в фазе 3). Кэша нет.
- `agents/AgentAuthStatus.kt` — `UNAUTHENTICATED`, сообщение `agent certificate rejected`, `ErrorInfo(reason, domain="sard.dev")` через `StatusProto`.
- `agents/AgentAuthInterceptor.kt` — если сервис не в списке открытых: `TRANSPORT_ATTR_SSL_SESSION` → `peerCertificates[0]` (`SSLPeerUnverifiedException` = нет сертификата) → аутентификатор; успех — `Contexts.interceptCall` с принципалом; отказ — `call.close` + лог `reason, serial, agent, method` (сертификат не логируется).
- `agents/AgentAuthConfiguration.kt` — `UNAUTHENTICATED_SERVICES = {EnrollmentService, grpc.health.v1.Health}`; перехватчик — `@GlobalServerInterceptor`, `@Order(HIGHEST_PRECEDENCE)`.
- `V202609281200__agent_revocation.sql` — `agents.revoked_at`; поле `Agent.revokedAt`.
- `application.yaml` — `spring.grpc.server.reflection.enabled: false` (по метаданным Boot по умолчанию `true`).
- `server/build.gradle.kts` — `proto-google-common-protos:2.64.1`; `docs/dependencies.md`.

Тесты (`AgentAuthIntegrationTest`, PostgreSQL в Testcontainers, TLS на случайном порту, агенты выпускаются настоящим `Enrollment`, два тенанта на тест):
1. Каждый метод из `AgentServiceGrpc.getServiceDescriptor().methods` без сертификата → `UNAUTHENTICATED`/`CERT_MISSING`.
2. `sard.test.v1.Probe` (`ProbeService`, `BindableService` только в тестовой конфигурации, без proto — маршаллер байтов) без сертификата → `CERT_MISSING`.
3. Enroll без сертификата проходит до конца (агент создан в тенанте токена); health → `SERVING`; reflection → `UNIMPLEMENTED` (не зарегистрирован).
4. Сертификат живого агента: все методы AgentService → `UNIMPLEMENTED`; `Probe/Whoami` возвращает `tenant/agent/serial` из `Context`.
5. Отказы: другой CA → `UNAVAILABLE` на рукопожатии (зафиксировано: до перехватчика не доходит); наш CA без записи → `CERT_UNKNOWN`; `revoked_at` сертификата → `CERT_REVOKED`; `not_after` записи в прошлом → `CERT_EXPIRED`; `agents.revoked_at` → `AGENT_REVOKED`; SAN называет другого агента, чем запись → `CERT_IDENTITY_MISMATCH`.

Изменён тест S1: `SardServerIntegrationTest` «agent service answers UNIMPLEMENTED» вызывал AgentService без сертификата; по ADR 0009 теперь это `UNAUTHENTICATED` — ожидание обновлено, «заглушки отвечают UNIMPLEMENTED» проверяет тест 4 с сертификатом. Список миграций в том же классе дополнен `202609281200`.

Не проверено (полагаю): сертификат с истёкшим собственным сроком X.509 отклоняется JSSE на рукопожатии (PKIX проверяет даты). Проверить тестом нельзя без внедрения часов в CA сервера (`Clock.systemUTC()` в `PkiAutoConfiguration`); достижимый путь `CERT_EXPIRED` — `not_after` записи.

Проверка:
- `./gradlew :server:test` — exit 0, 146 тестов, 0 упавших, 0 пропущенных.
- `./scripts/gate.sh server fast` — `PASSED`; покрытие 94.4% (instructions); CRAP максимум 6.0 (`OpenApiConfigurationKt.objectSchemas`, существующий), в новом коде ≤ 5. Первый прогон показал CRAP 10 у `authenticate` (CC 10 из-за null-safe ветвлений) — функция разделена на `authenticate` + `judge`.
- `./gradlew -Pmutflow.enabled=true :server:test --rerun` — exit 0 (ADR 0006: выживший мутант валит сборку); классы `@MutFlowTest` прогнаны многократно (например, 30 запусков тестов `AgentAuthenticatorTest` в отчёте JUnit).
- `make license-check` — 223 files OK.

Следующее (фаза 3): тенант в обработчиках через `principal` → `inTenant`, тест 6 (JPA в обработчике и в сообщении стрима после открытия; поток обработчика — проверить, что `Context` доходит), ADR 0009 и 0013, заметка для S5 о закрытии стримов при отзыве.

## Фаза 3 — тенант в вызове и стриме (тест 6), ADR

Проверено по байткоду `io.grpc.kotlin.ServerCalls` (grpc-kotlin-stub 1.5.0): определение метода вызывает `GrpcContextElement.current()` и кладёт его в контекст корутины обработчика. `GrpcContextElement` — `ThreadContextElement`, поэтому `Context` перехватчика восстанавливается на любом потоке этой корутины, включая `withContext(Dispatchers.IO)` и обработку каждого сообщения стрима.

Код:
- `agents/AgentSessions.kt` — `inTenant { session -> }` = `TenantSessions.inTenant(principal.tenantId)`; принципал из `AgentPrincipal.KEY`; без аутентифицированного вызова — `IllegalStateException` (тенант не угадывается). Бин в `AgentAuthConfiguration`. Обработчики S4/S5 ходят в базу только через него.
- `ProbeService` (тест) — корутинные `Agents` (unary) и `AgentsStream` (bidi): `withContext(Dispatchers.IO)` → `AgentSessions.inTenant` → HQL `select id from Agent` без условия по тенанту.

Тест 6 (`AgentAuthIntegrationTest`), два тенанта и по агенту в каждом:
- `Agents` с сертификатом агента acme видит только его id, с сертификатом агента globex — только свой.
- `AgentsStream`: три сообщения, каждое отправляется после ответа на предыдущее (то есть уже после открытия стрима); каждый ответ — только агент тенанта сертификата; стрим закрывается `OK`.
- `AgentsStream` без сертификата — `CERT_MISSING` при открытии.
- Контрольная проверка, что тест не проходит впустую: временно заменил в `ProbeService` контекст на `GrpcContextElement(Context.ROOT)` — упали ровно два теста тенанта (16 тестов, 2 failed); изменение откачено.

ADR:
- 0009 — раздел «Перехватчик агентов»: политика «запрещено всё, что не разрешено явно», список открытых сервисов, reflection выключен, проверка цепочки на TLS-слое, порядок проверок, таблица кодов отказа, стрим; в «Отвергнуто» — список закрытых методов, кэш принципалов, тенант из сообщения.
- 0013 — второй вызов `system` (`AgentCertificateStandings.of`), тенант gRPC-вызова через `AgentSessions`; `agents.revoked_at` в целевой схеме; пункт «Отложено» про тенант gRPC-потока закрыт.
- 0014 — уточнены строки про SAN (тенант подтверждается записью), список открытых сервисов и отзыв.

### Для S5 (задача)
- **Закрыть открытые стримы `Connect` при отзыве агента или сертификата.** Сейчас проверка выполняется только при открытии вызова; стрим, открытый до `agents.revoked_at`/`agent_certificates.revoked_at`, живёт до разрыва. Варианты: реестр открытых стримов по `(tenant_id, agent_id, serial)` и закрытие с `UNAUTHENTICATED` (`AGENT_REVOKED`/`CERT_REVOKED`) из операции отзыва; либо периодическая перепроверка на `Heartbeat`. Нужен тест: отзыв во время открытого стрима закрывает его.
- Также для S5: истечение `not_after` во время долгого стрима — та же перепроверка.

Проверка:
- `./scripts/gate.sh server fast` — `PASSED`; 149 тестов, 0 упавших, 0 пропущенных; покрытие 94.2% (instructions); CRAP в пределах порога.
- `./gradlew -Pmutflow.enabled=true :server:test --rerun` — exit 0.
- `make license-check` — OK.

Итог по Definition of done: `./gradlew :server:test` проходит; каждый метод AgentService (по дескриптору) и незаявленный сервис без сертификата отклоняются (тесты 1, 2); JPA в обработчиках агента фильтруется по тенанту сертификата, в том числе в стриме (тест 6); ADR 0009 и 0013 обновлены; задача для S5 записана выше.
