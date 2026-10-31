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

/** The sample must not go round the security chain of the core (spring-security.feature, @startup, Р44). */
class SampleSecurityStartupTest {
    private fun failureOf(property: String): String {
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
                            "--$property=true",
                        ).close()
                }
            return generateSequence(failure) { it.cause }.map { it.message.orEmpty() }.joinToString()
        } finally {
            container.stop()
        }
    }

    @Test
    fun `Своя цепочка безопасности образца останавливает старт`() {
        val text = failureOf("sard.sample.own-chain")

        assertTrue("sample" in text && "SardSecurityCustomizer" in text, text)
    }

    @Test
    fun `Публичная операция образца вне api v1 останавливает старт`() {
        val text = failureOf("sard.sample.public-outside")

        assertTrue("sample" in text && "GET /sample/open" in text, text)
    }
}
