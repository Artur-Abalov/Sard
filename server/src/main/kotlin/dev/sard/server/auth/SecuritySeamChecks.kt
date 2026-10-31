// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.extension.Operation
import dev.sard.server.extension.SardSecurityCustomizer
import org.springframework.beans.factory.annotation.AnnotatedBeanDefinition
import org.springframework.beans.factory.config.BeanDefinition
import org.springframework.beans.factory.config.BeanFactoryPostProcessor
import org.springframework.beans.factory.config.ConfigurableListableBeanFactory
import org.springframework.core.annotation.Order
import org.springframework.security.config.annotation.web.configuration.WebSecurityCustomizer
import org.springframework.security.web.SecurityFilterChain
import java.net.URL

private const val API_PREFIX = "/api/v1/"

/** The security customizers of the extensions with the bean name of each, in the order they are applied (Р41). */
internal class NamedCustomizer(
    val name: String,
    val origin: String,
    val customizer: SardSecurityCustomizer,
)

/** How the checks name an extension: by its bean and the configuration that declares it. */
internal fun describe(
    name: String,
    origin: String,
) = "'$name' ($origin)"

/**
 * Refuses the start when a bean other than the core's chain or its customizer takes part in the security of the HTTP
 * port (CORE1 Р44): a `SecurityFilterChain` of an extension would stand beside the chain of the core and take the
 * paths it matches out of the Origin check and the session; a `WebSecurityCustomizer` would leave paths out of the
 * chain altogether. "Of the core" is by code source, as for controllers (Р27). It guards against a mistake and is no
 * security boundary (ADR 0070 item 8).
 */
internal fun checkSecurityBeans(core: URL?) =
    BeanFactoryPostProcessor { beans: ConfigurableListableBeanFactory ->
        val foreign =
            listOf(SecurityFilterChain::class.java, WebSecurityCustomizer::class.java)
                .flatMap { type -> beans.getBeanNamesForType(type, true, false).map { type to it } }
                .filter { (_, name) -> !isFromCore(beans, name, core) }
        check(foreign.isEmpty()) {
            val described =
                foreign.joinToString { (type, name) -> "${type.simpleName} ${describe(name, originOf(beans, name))}" }
            "An extension takes part in the security of the HTTP port with a bean of its own: $described. " +
                "Only the chain of the core guards /api/v1; an extension adds its sign-in with a " +
                "${SardSecurityCustomizer::class.java.simpleName} bean."
        }
    }

/** The public operations an extension declares must be under /api/v1; anything else is open to the world. */
internal fun checkPublicOperations(customizers: List<NamedCustomizer>) {
    val outside =
        customizers.flatMap { named ->
            named.customizer.publicOperations
                .filterNot { it.pathTemplate.startsWith(API_PREFIX) }
                .map { operation -> describe(named.name, named.origin) to operation }
        }
    check(outside.isEmpty()) {
        val described =
            outside.joinToString { (extension, operation) -> "$extension declares the public operation $operation" }
        "A public operation of an extension outside /api/v1 would be open without a session: $described"
    }
}

/** Every public operation the customizers declare, the ones of the core not included. */
internal fun publicOperationsOf(customizers: List<NamedCustomizer>): Set<Operation> =
    customizers.flatMapTo(linkedSetOf()) { it.customizer.publicOperations }

/** The customizers of the extensions by `@Order`, then by bean name, which an extension names after its id (Р41). */
internal fun customizersOf(beans: ConfigurableListableBeanFactory): List<NamedCustomizer> =
    beans
        .getBeanNamesForType(SardSecurityCustomizer::class.java)
        .sorted()
        .map { name ->
            val order = beans.findAnnotationOnBean(name, Order::class.java)?.value ?: Int.MAX_VALUE
            Triple(order, name, beans.getBean(name, SardSecurityCustomizer::class.java))
        }.sortedBy { it.first }
        .map { (_, name, customizer) -> NamedCustomizer(name, originOf(beans, name), customizer) }
