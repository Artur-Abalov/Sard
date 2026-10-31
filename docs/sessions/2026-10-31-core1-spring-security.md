<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# CORE1 (SARD-7) — HTTP-безопасность сервера на Spring Security (coder)

Дата: 2026-10-31. Спецификация `docs/specs/server/spring-security.feature` (Р34–Р53), ADR 0071.

## Сделано
1. Зависимость `spring-boot-starter-security` (замки `--write-locks`, контрольные суммы `--write-verification-metadata`
   с дополнением, не заменой; строка в `docs/dependencies.md`).
2. Единая цепочка `SecurityFilterChain` (`CoreSecurityAutoConfiguration`): `ApiHeadersFilter` → `OriginGuardFilter` →
   `SecurityContextHolderFilter` с `SessionSecurityContextRepository` → `ExceptionTranslationFilter` →
   `AuthorizationFilter`. Без CSRF, форм, Basic, выхода, request cache, anonymous, HttpSession, заголовков Spring Security.
3. `AccessPolicyAuthorizationManager` вместо `AccessPolicyInterceptor`; операцию определяет `MvcOperationResolver` по
   `RequestMappingHandlerMapping` на обёртке запроса. `OperationMatcher` — метод и точный путь.
4. Публичные `SubjectAuthentication` и `SardSecurityCustomizer`; `SUBJECT_REQUEST_ATTRIBUTE`, `SessionAuthFilter` и бин
   `sessionAuthFilterRegistration` удалены. Проверки старта Р44 (`SecuritySeamChecks`).
5. Вход паролем: `SessionApiImpl` спрашивает `AdministratorLogin` (`ProviderManager` с `AdministratorPasswordProvider`),
   не бин.
6. `ApiRequestRejectedHandler` (400 без тела, с заголовками API); Tomcat отдаёт закодированные `/` и `\` брандмауэру.
7. `NoDefaultUser` — `EnvironmentPostProcessor`, исключающий `UserDetailsServiceAutoConfiguration` для любого приложения
   на ядре.
8. Образец: `sard.sample.public-ping`, `own-chain`, `public-outside`, `SampleSecurityCustomizer`.
9. ABI-выгрузка, ADR 0071, правки ADR 0021 и 0070, `docs/extensions.md`, спецификации admin-login, console-serving и
   server-core-extensions.

## Что узнали
- В Spring Security 7 у `AuthorizationFilter` нет `shouldFilterAllDispatcherTypes`; повторный проход по ERROR-диспетчеризации
  безвреден: путь `/error` лежит вне `/api/v1`, а политику спрашивают только для путей под ним.
- Tomcat сам отвечает HTML-страницей на закодированные `/` и `\`; чтобы пути отклонял брандмауэр (Р51), соединению
  задано `encodedSolidusHandling=passthrough`.
- `@RestController` в пакетах `dev.sard.server.*` тестов попадает в OpenAPI всех тестовых контекстов
  (`web/src/api/openapi.json` совпадает с документом сервера): тестовые контроллеры безопасности лежат в
  `dev.sard.testext.security`.
- Тесты `OnboardingGuardApiIntegrationTest` с обходными путями (`;`, `..`) теперь ждут 400 брандмауэра вместо 409.
- Web-моки воспроизводят только функциональные заголовки (`Content-Type`, `Retry-After`, `Set-Cookie`), заголовки
  безопасности — нет, поэтому моки не менялись.

## Отклонения / долги
Замечания architect и что сделано (сессия 2):
- (a) Мастер и субъект: `administratorSession` требовала только аутентификацию. Теперь `administratorSession(SubjectSource)`:
  аутентифицирован, не анонимный (`Authentication?.isSignedIn()`, тот же предикат в `requireAuthentication`) и шов называет
  субъекта. Тест `SecurityExtensionApiIntegrationTest`.
- (b) Настройщик мог сузить единственную цепочку (`securityMatcher`): `checkChainIntact` после `build()` — отказ старта с
  именами бинов. Тест `SecurityStartupChecksTest`.
- (c) Автоконфигурации безопасности Spring gRPC были активны (бины `grpcSecurity`, `coroutineSecurityContextInterceptor`
  на 9090): тест `AgentAuthIntegrationTest` сначала упал с этими тремя бинами, затем они исключены.
- (d) `NoDefaultUser` терял список исключений оператора: заменён `SecurityAutoConfigurationFilter`
  (`AutoConfigurationImportFilter`), свойств не трогает; тест через `AutoConfigurationImportSelector` с `exclude[0]`.
- (e) Документация: KDoc и ADR 0021, 0071, OQ-035, `docs/extensions.md` (предупреждение об `oauth2Login`) приведены в
  соответствие с кодом; атрибут сессии — зацепка `TenantResolver`, не контракт расширения.
- Тестовые пробелы: сценарий «Событие планировщика после запроса администратора…» покрыт на уровне `CoreSubjectSource`
  (контекст не виден другому потоку), не как @http с тиком планировщика. Р46 закреплён тестом архитектуры. Порядок
  настройщиков по `@Order`, затем по имени бина, а не по id расширения — вопрос specifier, код не менялся.
