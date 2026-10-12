// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.testsupport

import org.junit.jupiter.api.ClassDescriptor
import org.junit.jupiter.api.ClassOrderer
import org.junit.jupiter.api.ClassOrdererContext
import org.junit.platform.commons.support.AnnotationSupport
import org.springframework.boot.test.context.SpringBootTest

/**
 * Runs the classes that start a Spring context (@SpringBootTest, directly or through a composed
 * annotation) after all the others. mutflow keeps its session in one static field, so any thread of a
 * cached context (stream sweeper, scheduler, notifications) that runs mutated code while a unit test
 * is inside MutFlow.underTest registers touchpoints with that test. Unit classes therefore run while no
 * context exists yet (ADR 00XX, mutflow isolation). Within a group the order is by class name.
 */
class SpringContextsLastOrderer : ClassOrderer {
    override fun orderClasses(context: ClassOrdererContext) {
        context.classDescriptors.sortWith(
            compareBy<ClassDescriptor>({ startsContext(it) }, { it.testClass.name }),
        )
    }

    private fun startsContext(descriptor: ClassDescriptor): Boolean =
        AnnotationSupport.isAnnotated(descriptor.testClass, SpringBootTest::class.java)
}
