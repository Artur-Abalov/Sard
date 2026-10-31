// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

import org.springframework.beans.factory.config.BeanFactoryPostProcessor
import org.springframework.beans.factory.config.ConfigurableListableBeanFactory

/**
 * Refuses the start when more than one extension replaced a seam of the core (CORE1 Р22). The text names
 * every implementation by its bean name and the configuration that declares it, so an extension names its
 * beans after its id. Checked on the bean definitions, before any bean exists.
 */
internal fun singleImplementationOf(seam: Class<*>) =
    BeanFactoryPostProcessor { beans: ConfigurableListableBeanFactory ->
        val names = beans.getBeanNamesForType(seam, true, false)
        check(names.size <= 1) {
            val described = names.sorted().joinToString { "$it (${origin(beans, it)})" }
            "Several implementations of ${seam.simpleName}, only one may replace it: $described"
        }
    }

private fun origin(
    beans: ConfigurableListableBeanFactory,
    name: String,
): String {
    val definition = beans.getBeanDefinition(name)
    return definition.factoryBeanName ?: definition.beanClassName ?: "unknown"
}
