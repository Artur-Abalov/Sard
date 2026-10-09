// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import org.springframework.boot.test.util.TestPropertyValues
import org.springframework.context.ApplicationContextInitializer
import org.springframework.context.ConfigurableApplicationContext
import java.nio.file.Files
import java.nio.file.Path

/**
 * The CA directory and the database are one installation (F4a, Р19), and every context of a test gets a database
 * of its own, so it gets a CA directory of its own: a new directory under `sard.test.pki-base` (build.gradle.kts).
 * A test that restarts the server or looks at the CA directory names it itself with `sard.pki.dir` in its
 * properties, and keeps it.
 */
class FreshPkiDirectory : ApplicationContextInitializer<ConfigurableApplicationContext> {
    override fun initialize(context: ConfigurableApplicationContext) {
        val environment = context.environment
        val base = System.getProperty("sard.test.pki-base") ?: return
        val named = environment.propertySources["Inlined Test Properties"]?.containsProperty("sard.pki.dir") == true
        if (named) return
        val directory = Files.createTempDirectory(Files.createDirectories(Path.of(base)), "pki-")
        TestPropertyValues.of("sard.pki.dir=$directory").applyTo(environment)
    }
}
