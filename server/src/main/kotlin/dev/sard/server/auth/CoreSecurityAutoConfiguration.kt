// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.extension.AccessPolicy
import dev.sard.server.extension.AccessPolicyAutoConfiguration
import dev.sard.server.extension.SubjectSource
import dev.sard.server.extension.SubjectSourceAutoConfiguration
import org.springdoc.core.customizers.OpenApiCustomizer
import org.springframework.beans.factory.ObjectProvider
import org.springframework.beans.factory.config.ConfigurableListableBeanFactory
import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.autoconfigure.condition.ConditionalOnWebApplication
import org.springframework.boot.security.autoconfigure.SecurityAutoConfiguration
import org.springframework.boot.security.autoconfigure.UserDetailsServiceAutoConfiguration
import org.springframework.boot.security.autoconfigure.actuate.web.servlet.ManagementWebSecurityAutoConfiguration
import org.springframework.boot.security.autoconfigure.web.servlet.ServletWebSecurityAutoConfiguration
import org.springframework.boot.tomcat.TomcatConnectorCustomizer
import org.springframework.boot.tomcat.servlet.TomcatServletWebServerFactory
import org.springframework.boot.web.server.WebServerFactoryCustomizer
import org.springframework.context.annotation.Bean
import org.springframework.core.Ordered
import org.springframework.core.annotation.Order
import org.springframework.security.config.annotation.web.builders.HttpSecurity
import org.springframework.security.config.http.SessionCreationPolicy
import org.springframework.security.web.SecurityFilterChain
import org.springframework.security.web.context.SecurityContextHolderFilter
import org.springframework.security.web.firewall.RequestRejectedHandler
import org.springframework.security.web.session.DisableEncodeUrlFilter
import org.springframework.security.web.util.matcher.RequestMatcher
import org.springframework.web.servlet.mvc.method.annotation.RequestMappingHandlerMapping
import tools.jackson.databind.ObjectMapper

private const val PASSTHROUGH = "passthrough"

/**
 * The one security chain of the HTTP port (CORE1 Р34-Р53, ADR 0071): the headers of the API, the Origin check, the
 * session of the core as the security context, then the access rules. It replaces the filters and the interceptor the
 * server had before and leaves out of Spring Security everything that shows from outside: no default user, form or
 * Basic login, logout, CSRF token, remember-me, request cache, HttpSession or CORS.
 *
 * Access (Р35, Р40): outside /api/v1 everything passes, as do the public operations of the core and of the extensions
 * and the operations of the wizard; sign-out needs an authentication; every other request under /api/v1 needs one and
 * the answer of the access policy. The customizers of the extensions are applied last (Р41); they may not set access
 * rules, and a failure of one refuses the start (Р44), as does a security bean of an extension.
 *
 * It is ordered before the auto-configurations of Spring Boot that would start a default chain and a default user.
 */
@AutoConfiguration(
    before = [
        SecurityAutoConfiguration::class,
        ServletWebSecurityAutoConfiguration::class,
        UserDetailsServiceAutoConfiguration::class,
        ManagementWebSecurityAutoConfiguration::class,
    ],
    after = [
        AdminAuthAutoConfiguration::class,
        AccessPolicyAutoConfiguration::class,
        SubjectSourceAutoConfiguration::class,
    ],
)
@ConditionalOnWebApplication(type = ConditionalOnWebApplication.Type.SERVLET)
class CoreSecurityAutoConfiguration {
    companion object {
        /** Р44: no security bean of an extension; a static method, as a post-processor must exist before any bean. */
        @Bean
        @JvmStatic
        fun securityBeansCheck() =
            checkSecurityBeans(
                CoreSecurityAutoConfiguration::class.java.protectionDomain
                    ?.codeSource
                    ?.location,
            )
    }

    @Bean
    fun sardSecurityFilterChain(
        http: HttpSecurity,
        sessions: SessionStore,
        policy: ObjectProvider<AccessPolicy>,
        subjects: ObjectProvider<SubjectSource>,
        mappings: ObjectProvider<RequestMappingHandlerMapping>,
        objectMapper: ObjectMapper,
        beans: ConfigurableListableBeanFactory,
    ): SecurityFilterChain {
        val customizers = customizersOf(beans)
        checkPublicOperations(customizers)
        val open = PUBLIC_OPERATIONS + WIZARD_OPERATIONS
        disableWhatShowsFromOutside(http)
        http
            .securityContext { it.securityContextRepository(SessionSecurityContextRepository(sessions)) }
            .exceptionHandling {
                it
                    .authenticationEntryPoint(ProblemAuthenticationEntryPoint(objectMapper))
                    .accessDeniedHandler(ProblemAccessDeniedHandler(objectMapper))
            }.addFilterBefore(ApiHeadersFilter(), DisableEncodeUrlFilter::class.java)
            .addFilterBefore(OriginGuardFilter(objectMapper), SecurityContextHolderFilter::class.java)
            .authorizeHttpRequests { rules ->
                rules.requestMatchers(RequestMatcher { !isApiPath(it.requestURI) }).permitAll()
                rules.requestMatchers(OperationMatcher(open)).permitAll()
                rules.requestMatchers(OperationMatcher(publicOperationsOf(customizers))).permitAll()
                rules.requestMatchers(OperationMatcher(setOf(SIGN_OUT))).access(AuthenticatedAuthorizationManager())
                val operations = MvcOperationResolver(mappings)
                rules.anyRequest().access(AccessPolicyAuthorizationManager(policy, subjects, operations))
            }
        customizers.forEach { apply(it, http) }
        val chain = http.build()
        checkChainIntact(chain, customizers)
        return chain
    }

    /** Spring Security does not offer, create or write anything on its own that the server did not do before. */
    private fun disableWhatShowsFromOutside(http: HttpSecurity) {
        http
            .csrf { it.disable() }
            .cors { it.disable() }
            .headers { it.disable() }
            .anonymous { it.disable() }
            .logout { it.disable() }
            .requestCache { it.disable() }
            .servletApi { it.disable() }
            .sessionManagement { it.sessionCreationPolicy(SessionCreationPolicy.STATELESS) }
    }

    /** A customizer that fails, or sets access rules (refused once the chain's own are in), refuses the start. */
    private fun apply(
        named: NamedCustomizer,
        http: HttpSecurity,
    ) {
        runCatching { named.customizer.customize(http) }.onFailure { e ->
            throw IllegalStateException(
                "The security customizer ${describe(named.name, named.origin)} failed or set access rules for paths, " +
                    "which only the core may: ${e.message}",
                e,
            )
        }
    }

    /** The firewall runs ahead of the chain: its refusal writes the headers of the API itself (Р51). */
    @Bean
    fun apiRequestRejectedHandler(): RequestRejectedHandler = ApiRequestRejectedHandler()

    /**
     * Tomcat refuses an encoded "/" or "\\" in a path on its own, with its HTML error page; handed on, the firewall
     * of Spring Security refuses it as it does every other ambiguous path: 400 with no body and the headers of
     * the API (Р51).
     */
    @Bean
    fun encodedPathsToTheFirewall(): WebServerFactoryCustomizer<TomcatServletWebServerFactory> =
        WebServerFactoryCustomizer { factory ->
            factory.addConnectorCustomizers(
                TomcatConnectorCustomizer { connector ->
                    connector.encodedSolidusHandling = PASSTHROUGH
                    connector.encodedReverseSolidusHandling = PASSTHROUGH
                },
            )
        }

    /** The public operations of the extensions have an empty security requirement in OpenAPI, as the core's (Р41). */
    @Bean
    @Order(Ordered.HIGHEST_PRECEDENCE)
    fun extensionPublicOperationsDocumented(beans: ConfigurableListableBeanFactory): OpenApiCustomizer =
        OpenApiCustomizer { api -> describePublicOperations(api, publicOperationsOf(customizersOf(beans))) }
}
