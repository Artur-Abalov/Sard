// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import com.fasterxml.jackson.annotation.JsonProperty
import org.springframework.context.annotation.Configuration
import org.springframework.core.convert.converter.Converter
import org.springframework.core.convert.converter.ConverterFactory
import org.springframework.format.FormatterRegistry
import org.springframework.web.bind.WebDataBinder
import org.springframework.web.bind.annotation.ControllerAdvice
import org.springframework.web.bind.annotation.InitBinder
import org.springframework.web.servlet.config.annotation.WebMvcConfigurer
import java.beans.PropertyEditorSupport

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

/**
 * The same rule for the binder: Spring falls back to `Enum.valueOf` by constant name when a converter
 * refuses ("ACTIVE"), and a property editor, which comes first, does not let it.
 */
class WireEnumEditor<T : Enum<*>>(
    type: Class<T>,
) : PropertyEditorSupport() {
    private val converter = WireEnumConverterFactory().getConverter(type)

    override fun setAsText(text: String?) {
        value = converter.convert(text.orEmpty())
    }
}

/** The enums that are request parameters (`?status=`) take their wire values and nothing else. */
@ControllerAdvice
class WireEnumBinding {
    @InitBinder
    fun bind(binder: WebDataBinder) {
        binder.registerCustomEditor(AgentStatus::class.java, WireEnumEditor(AgentStatus::class.java))
        binder.registerCustomEditor(
            EnrollmentTokenStatus::class.java,
            WireEnumEditor(EnrollmentTokenStatus::class.java),
        )
        binder.registerCustomEditor(RunStatus::class.java, WireEnumEditor(RunStatus::class.java))
    }
}

@Configuration(proxyBeanMethods = false)
class WireEnumConfiguration : WebMvcConfigurer {
    override fun addFormatters(registry: FormatterRegistry) {
        registry.addConverterFactory(WireEnumConverterFactory())
    }
}
