// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import com.fasterxml.jackson.annotation.JsonProperty
import org.springframework.context.annotation.Configuration
import org.springframework.core.convert.converter.Converter
import org.springframework.core.convert.converter.ConverterFactory
import org.springframework.format.FormatterRegistry
import org.springframework.web.servlet.config.annotation.WebMvcConfigurer

/**
 * Query parameters take the same enum values as JSON bodies (?status=timed_out),
 * not the Kotlin constant names Spring converts by default.
 */
class WireEnumConverterFactory : ConverterFactory<String, Enum<*>> {
    override fun <T : Enum<*>> getConverter(targetType: Class<T>): Converter<String, T> {
        val byWire = targetType.enumConstants.associateBy { wireName(targetType, it) }
        return Converter { source ->
            byWire[source] ?: throw IllegalArgumentException("unknown ${targetType.simpleName}: $source")
        }
    }

    private fun wireName(
        type: Class<*>,
        constant: Enum<*>,
    ): String = type.getField(constant.name).getAnnotation(JsonProperty::class.java)?.value ?: constant.name
}

@Configuration(proxyBeanMethods = false)
class WireEnumConfiguration : WebMvcConfigurer {
    override fun addFormatters(registry: FormatterRegistry) {
        registry.addConverterFactory(WireEnumConverterFactory())
    }
}
