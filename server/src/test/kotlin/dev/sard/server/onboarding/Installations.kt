// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import ch.qos.logback.classic.Level
import ch.qos.logback.classic.Logger
import ch.qos.logback.classic.spi.ILoggingEvent
import ch.qos.logback.core.read.ListAppender
import dev.sard.server.pki.MovableClock
import dev.sard.server.selfagent.Logged
import org.slf4j.LoggerFactory
import org.springframework.boot.Banner
import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.autoconfigure.AutoConfigurationPackage
import org.springframework.boot.autoconfigure.EnableAutoConfiguration
import org.springframework.boot.builder.SpringApplicationBuilder
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.context.ConfigurableApplicationContext
import org.springframework.context.annotation.ComponentScan
import org.springframework.context.annotation.Configuration
import org.springframework.core.type.classreading.MetadataReader
import org.springframework.core.type.classreading.MetadataReaderFactory
import org.springframework.core.type.filter.TypeFilter
import org.testcontainers.postgresql.PostgreSQLContainer
import org.testcontainers.utility.DockerImageName
import tools.jackson.databind.ObjectMapper
import java.nio.file.Files
import java.nio.file.Path
import java.util.concurrent.atomic.AtomicInteger

/** One PostgreSQL for the restart tests; each installation gets a database of its own in it. */
object TestPostgres {
    private val container: PostgreSQLContainer by lazy {
        PostgreSQLContainer(DockerImageName.parse("postgres:18-alpine")).also { it.start() }
    }
    private val counter = AtomicInteger()

    fun newDatabase(): Database {
        val name = "installation_${counter.incrementAndGet()}"
        container.createConnection("").use { it.createStatement().execute("create database $name") }
        val url = "jdbc:postgresql://${container.host}:${container.firstMappedPort}/$name"
        return Database(url, container.username, container.password)
    }
}

class Database(
    val url: String,
    val user: String,
    val password: String,
) {
    /** The variables the server and its commands read. */
    fun environment(): Map<String, String> {
        val connection = mapOf("SARD_DB_URL" to url, "SARD_DB_USER" to user)
        return connection + ("SARD_DB_PASSWORD" to password)
    }

    fun properties(): Map<String, Any> =
        mapOf(
            "spring.datasource.url" to url,
            "spring.datasource.username" to user,
            "spring.datasource.password" to password,
        )
}

/** Keeps the test classes of the surrounding module out of the component scan of a server started by hand. */
class WithoutTestClasses : TypeFilter {
    override fun match(
        reader: MetadataReader,
        factory: MetadataReaderFactory,
    ): Boolean {
        val metadata = reader.annotationMetadata
        // The auto-configurations are imported by @EnableAutoConfiguration; one that a test declares is not offered.
        return metadata.hasAnnotation(TestConfiguration::class.java.name) ||
            metadata.hasAnnotation(AutoConfiguration::class.java.name) ||
            reader.classMetadata.className == "dev.sard.server.SardServerApplication"
    }
}

@Configuration(proxyBeanMethods = false)
@EnableAutoConfiguration
// The entities are found from the package of the application class, as for SardServerApplication.
@AutoConfigurationPackage(basePackages = ["dev.sard.server"])
@ComponentScan(
    basePackages = ["dev.sard.server"],
    excludeFilters = [
        ComponentScan.Filter(
            type = org.springframework.context.annotation.FilterType.CUSTOM,
            classes = [WithoutTestClasses::class],
        ),
    ],
)
class ServerUnderTest

/** What a start is given besides the installation. */
class StartOptions(
    /** The setup codes the server issues at this start, in turn. */
    val codes: List<String> = listOf(CODE),
    val properties: Map<String, Any> = emptyMap(),
    /** Beans registered before the context is refreshed, by name (a replacement SessionApi, say). */
    val beans: Map<String, Any> = emptyMap(),
    val clock: MovableClock = MovableClock(T0),
)

/** A started server, its clock, its client and what it logged while starting. */
class RunningServer(
    val context: ConfigurableApplicationContext,
    val clock: MovableClock,
    val startLog: List<Logged>,
) : AutoCloseable {
    val port: Int = checkNotNull(context.environment.getProperty("local.server.port")).toInt()
    val grpcPort: Int get() = checkNotNull(context.environment.getProperty("local.grpc.server.port")).toInt()
    val client = FirstStartClient(port, context.getBean(ObjectMapper::class.java))
    val logText get() = startLog.joinToString("\n") { it.text }

    fun <T : Any> bean(type: Class<T>): T = context.getBean(type)

    override fun close() = context.close()
}

/**
 * The CA directory, the database and the channel of an installation, which outlive its servers: a restart is
 * [RunningServer.close] and [start] again.
 */
class Installation(
    val directory: Path,
    val database: Database = TestPostgres.newDatabase(),
) {
    val pkiDir: Path = directory.resolve("pki")
    val selfDir: Path = Files.createDirectories(directory.resolve("self"))

    fun start(options: StartOptions = StartOptions()): RunningServer {
        val capture = LogCapture()
        val context = capture.use { run(options, it) }
        return RunningServer(context, options.clock, capture.events())
    }

    /** A start that must fail: what it threw and what it logged. */
    fun startFailing(options: StartOptions = StartOptions()): Pair<Throwable, List<Logged>> {
        val capture = LogCapture()
        var failure: Throwable? = null
        try {
            capture.use { run(options, it).close() }
        } catch (e: Exception) {
            failure = e
        }
        return checkNotNull(failure) { "the server started" } to capture.events()
    }

    private fun run(
        options: StartOptions,
        capture: LogCapture,
    ): ConfigurableApplicationContext {
        val codes = options.codes.iterator()
        val properties =
            database.properties() +
                mapOf(
                    "server.port" to 0,
                    "spring.grpc.server.port" to 0,
                    "sard.pki.dir" to pkiDir.toString(),
                    "sard.agent.stream.check-interval" to "1h",
                    "SARD_AGENT_DOWNLOADS" to "false",
                ) + options.properties
        // Two servers that start together race in the setup of the logging system (Spring Boot sets system properties):
        // the lock is held until the initializer below, which runs once logging is set up.
        loggingSetUp.lock()
        try {
            return SpringApplicationBuilder(ServerUnderTest::class.java)
                .bannerMode(Banner.Mode.OFF)
                .initializers(
                    { context: ConfigurableApplicationContext ->
                        loggingSetUp.unlock()
                        // Logging is set up again by every start: what is attached before that is gone.
                        capture.attach()
                        val beans = context.beanFactory
                        beans.registerSingleton("clock", options.clock)
                        beans.registerSingleton("setupCodeGenerator", SetupCodeGenerator { codes.next() })
                        options.beans.forEach { (name, bean) -> beans.registerSingleton(name, bean) }
                    },
                ).run(*properties.map { (name, value) -> "--$name=$value" }.toTypedArray())
        } finally {
            if (loggingSetUp.isHeldByCurrentThread) loggingSetUp.unlock()
        }
    }

    private companion object {
        val loggingSetUp =
            java.util.concurrent.locks
                .ReentrantLock()
    }
}

/** Everything logged from the moment a server's context is being set up, at DEBUG. */
class LogCapture : AutoCloseable {
    private val appender = ListAppender<ILoggingEvent>()
    private val root = LoggerFactory.getLogger(org.slf4j.Logger.ROOT_LOGGER_NAME) as Logger
    private var level: Level? = null
    private var attached = false

    fun attach() {
        if (attached) return
        attached = true
        appender.start()
        level = root.level
        root.level = Level.DEBUG
        root.addAppender(appender)
    }

    override fun close() {
        if (!attached) return
        root.detachAppender(appender)
        appender.stop()
        root.level = level
        attached = false
    }

    fun events(): List<Logged> =
        appender.list.map { event ->
            val causes = generateSequence(event.throwableProxy) { it.cause }.mapNotNull { it.message }
            Logged(event.level, (listOf(event.formattedMessage) + causes).joinToString("\n"))
        }
}
