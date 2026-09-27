// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import dev.sard.server.pki.PkiProperties
import org.springframework.boot.context.properties.EnableConfigurationProperties
import org.springframework.boot.test.context.runner.ApplicationContextRunner
import org.springframework.context.annotation.Configuration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

@Configuration(proxyBeanMethods = false)
@EnableConfigurationProperties(PkiProperties::class)
private class PkiPropertiesConfiguration

// Kotlin gives an inner lambda class a name built from its enclosing function, so a non-inline
// lambda directly inside a Cyrillic test name used to fail to compile on a non-UTF-8 host
// locale. That is no longer required: the build now fails fast with a clear message if
// sun.jnu.encoding isn't UTF-8 (see server/build.gradle.kts, best-effort — a stale Kotlin
// compile daemon keeps its old environment even after this check is added; stop it with
// `./gradlew --stop` and `pkill -f KotlinCompileDaemon`). `make gate`/`make gate-fast`/CI set
// LC_ALL=C.UTF-8 for the gradle tasks that compile this module; running scripts/gate.sh or
// ./gradlew directly needs `export LC_ALL=C.UTF-8` first. Kept anyway for readability: these
// ASCII-named helpers keep the lambdas out of the test methods.
private fun runner() =
    ApplicationContextRunner()
        .withUserConfiguration(PkiPropertiesConfiguration::class.java, AgentEndpointConfiguration::class.java)
        .withPropertyValues(
            "sard.pki.dir=/tmp/unused",
            "sard.pki.server-names=sard.example.com,localhost,127.0.0.1",
            "spring.grpc.server.port=9090",
        )

private fun startupFailure(endpoint: String): Throwable {
    var failure: Throwable? = null
    runner().withPropertyValues("sard.agent.endpoint=$endpoint").run { context -> failure = context.startupFailure }
    return checkNotNull(failure) { "the context started for endpoint '$endpoint'" }
}

private fun resolvedAddress(endpoint: String): AgentEndpoint {
    var resolved: AgentEndpoint? = null
    runner().withPropertyValues("sard.agent.endpoint=$endpoint").run { context ->
        assertEquals(null, context.startupFailure)
        resolved = context.getBean(AgentEndpoint::class.java)
    }
    return checkNotNull(resolved)
}

/** Rule "Адрес в команде регистрации покрыт сертификатом сервера": a bean that fails context startup. */
class AgentEndpointConfigurationTest {
    @Test
    fun `Адрес с хостом вне сертификата не даёт серверу стартовать`() {
        val failure = startupFailure("backup.example.org:9090")
        val cause = generateSequence(failure) { it.cause }.first { it is InvalidAgentEndpointException }
        val message = cause.message.orEmpty()
        assertTrue("backup.example.org" in message, message)
        for (name in listOf("sard.example.com", "localhost", "127.0.0.1")) assertTrue(name in message, message)
    }

    @Test
    fun `Заданный адрес с хостом из сертификата попадает в команду как есть`() {
        assertEquals("localhost:19090", resolvedAddress("localhost:19090").address)
    }

    @Test
    fun `Незаданный адрес для агентов не мешает серверу стартовать`() {
        assertEquals("sard.example.com:9090", resolvedAddress("").address)
    }
}
