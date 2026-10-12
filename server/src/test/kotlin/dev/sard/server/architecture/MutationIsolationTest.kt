// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.architecture

import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.junit.jupiter.api.Tag
import org.junit.platform.commons.support.AnnotationSupport
import org.springframework.boot.test.context.SpringBootTest
import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private const val ISOLATED_TAG = "mutflow-isolated"

/** Every compiled class of the test source set, loaded without initialisation. */
private fun testClasses(): List<Class<*>> {
    val root =
        File(
            MutationIsolationTest::class.java.protectionDomain.codeSource.location
                .toURI(),
        )
    check(root.isDirectory) { "expected the test classes as a directory, got $root" }
    return root
        .walkTopDown()
        .filter { it.isFile && it.extension == "class" }
        .map {
            it
                .relativeTo(root)
                .path
                .removeSuffix(".class")
                .replace(File.separatorChar, '.')
        }.map { Class.forName(it, false, MutationIsolationTest::class.java.classLoader) }
        .toList()
}

private fun Class<*>.isTagged(): Boolean {
    val tags = AnnotationSupport.findRepeatableAnnotations(this, Tag::class.java)
    return tags.any { it.value == ISOLATED_TAG }
}

private fun Class<*>.isSpringMutationTest() =
    AnnotationSupport.isAnnotated(this, MutFlowTest::class.java) &&
        AnnotationSupport.isAnnotated(this, SpringBootTest::class.java)

/**
 * mutflow keeps one session for the whole JVM (a static field), so a test that is both a mutation test
 * and a Spring integration test must run in the Gradle task that gives every class its own JVM
 * (`isolatedTest`, selected by this tag). A class that carries one but not the other is not a candidate.
 */
class MutationIsolationTest {
    @Test
    fun `a mutation test that starts a Spring context runs in the isolated task`() {
        val unisolated = testClasses().filter { it.isSpringMutationTest() && !it.isTagged() }

        assertEquals(emptyList(), unisolated.map { it.name }, "add @Tag(\"$ISOLATED_TAG\") to these classes")
    }

    @Test
    fun `the isolated task is not empty`() {
        assertTrue(testClasses().any { it.isSpringMutationTest() && it.isTagged() })
    }

    @Test
    fun `only mutation tests that start a Spring context carry the isolation tag`() {
        val misplaced = testClasses().filter { it.isTagged() && !it.isSpringMutationTest() }

        assertEquals(emptyList(), misplaced.map { it.name })
    }
}
