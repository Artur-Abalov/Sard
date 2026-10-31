// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import org.springframework.beans.factory.annotation.AnnotatedBeanDefinition
import org.springframework.beans.factory.config.BeanDefinition
import org.springframework.beans.factory.config.ConfigurableListableBeanFactory
import java.net.URL

/** The code source a class is loaded from; null for a class that cannot be loaded. */
internal fun locationOf(
    className: String,
    loader: ClassLoader?,
): String? =
    loadedOrNull(className, loader)
        ?.protectionDomain
        ?.codeSource
        ?.location
        ?.toString()

private fun loadedOrNull(
    className: String,
    loader: ClassLoader?,
): Class<*>? = runCatching { Class.forName(className, false, loader) }.getOrNull()

internal fun isFromCore(
    beans: ConfigurableListableBeanFactory,
    name: String,
    core: URL?,
): Boolean {
    val coreLocation = core?.toString() ?: return false
    return declaringClassOf(beans, name)?.let { locationOf(it, beans.beanClassLoader) } == coreLocation
}

internal fun originOf(
    beans: ConfigurableListableBeanFactory,
    name: String,
): String = declaringClassOf(beans, name) ?: "unknown"

/**
 * The class whose code declares the bean: the configuration of a `@Bean` method, else the bean's own class; a
 * singleton registered by hand has no definition, and its class is the answer.
 */
private fun declaringClassOf(
    beans: ConfigurableListableBeanFactory,
    name: String,
): String? =
    if (beans.containsBeanDefinition(name)) {
        declaringClassOf(beans.getBeanDefinition(name))
    } else {
        beans.getType(name)?.name
    }

private fun declaringClassOf(definition: BeanDefinition): String? =
    (definition as? AnnotatedBeanDefinition)?.factoryMethodMetadata?.declaringClassName ?: definition.beanClassName
