// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.testsupport

import org.junit.jupiter.api.ClassDescriptor
import org.junit.jupiter.api.ClassOrdererContext
import org.springframework.boot.test.context.SpringBootTest
import java.util.Optional
import kotlin.test.Test
import kotlin.test.assertEquals

class SpringContextsLastOrdererTest {
    class ZPlain

    class APlain

    @SpringBootTest
    class BDirect

    @Target(AnnotationTarget.CLASS)
    @Retention(AnnotationRetention.RUNTIME)
    @SpringBootTest
    annotation class Composed

    @Composed
    class AComposed

    private class Descriptor(
        private val type: Class<*>,
    ) : ClassDescriptor {
        override fun getTestClass(): Class<*> = type

        override fun getDisplayName(): String = type.simpleName

        override fun isAnnotated(annotationType: Class<out Annotation>): Boolean = false

        override fun <A : Annotation> findAnnotation(annotationType: Class<A>): Optional<A> = Optional.empty()

        override fun <A : Annotation> findRepeatableAnnotations(annotationType: Class<A>): List<A> = emptyList()
    }

    private class Context(
        private val descriptors: MutableList<out ClassDescriptor>,
    ) : ClassOrdererContext {
        override fun getClassDescriptors(): MutableList<out ClassDescriptor> = descriptors

        override fun getConfigurationParameter(key: String): Optional<String> = Optional.empty()
    }

    @Test
    fun `classes that start a Spring context run after all the others, each group by name`() {
        val descriptors =
            mutableListOf<ClassDescriptor>(
                Descriptor(ZPlain::class.java),
                Descriptor(AComposed::class.java),
                Descriptor(BDirect::class.java),
                Descriptor(APlain::class.java),
            )

        SpringContextsLastOrderer().orderClasses(Context(descriptors))

        assertEquals(
            listOf(APlain::class.java, ZPlain::class.java, AComposed::class.java, BDirect::class.java),
            descriptors.map { it.testClass },
        )
    }
}
