// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.sample

import dev.sard.server.SardServerApplication
import org.springframework.boot.WebApplicationType
import org.springframework.boot.builder.SpringApplicationBuilder
import java.nio.file.Files
import kotlin.test.Test
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

/** The stray controller of the sample sits in a package of the core's namespace, outside /api/v1 (Р27, @startup). */
class SampleStrayControllerStartupTest {
    @Test
    fun `Лишний контроллер образца в пакете ядра останавливает старт`() {
        val container = SampleDatabase.container().also { it.start() }
        try {
            SampleDatabase.bootstrap(container)
            SampleDatabase.migrate(container)

            val failure =
                assertFailsWith<Throwable> {
                    SpringApplicationBuilder(SardServerApplication::class.java)
                        .web(WebApplicationType.SERVLET)
                        .run(
                            "--SARD_DB_URL=${container.jdbcUrl}",
                            "--SARD_DB_USER=sard_app",
                            "--SARD_DB_PASSWORD=sard_app-password",
                            "--server.port=0",
                            "--spring.grpc.server.port=0",
                            "--sard.pki.dir=${Files.createTempDirectory("sample-pki")}",
                            "--sard.sample.stray-controller=true",
                        ).close()
                }

            val text = generateSequence(failure) { it.cause }.map { it.message.orEmpty() }.joinToString()
            assertTrue("sample" in text && "/sample/stray" in text, text)
        } finally {
            container.stop()
        }
    }
}
