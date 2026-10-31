// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.sample

import dev.sard.server.auth.JdbcAdministrators
import dev.sard.server.auth.PasswordHasher
import dev.sard.server.database.MigrationSettings
import dev.sard.server.database.Migrator
import org.springframework.beans.factory.InitializingBean
import org.springframework.boot.test.util.TestPropertyValues
import org.springframework.context.ApplicationContextInitializer
import org.springframework.context.ConfigurableApplicationContext
import org.springframework.boot.jdbc.autoconfigure.JdbcConnectionDetails
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.DependsOn
import org.springframework.jdbc.core.JdbcTemplate
import org.testcontainers.postgresql.PostgreSQLContainer
import org.testcontainers.utility.DockerImageName
import java.io.File
import java.io.OutputStream
import java.io.PrintStream
import java.nio.file.Files
import java.sql.DriverManager
import java.time.Instant

const val ADMIN_PASSWORD = "sample-admin-password-2026"
private val LOGIN_ROLES = listOf("sard_migrator", "sard_app", "sard_backup")

private fun password(role: String) = "$role-password"

private val pin: Map<String, String> by lazy {
    File(requireNotNull(System.getProperty("sample.postgres-pin-file")) { "run the tests with Gradle" })
        .readLines()
        .map { it.trim() }
        .filter { it.isNotEmpty() && !it.startsWith("#") }
        .associate { it.substringBefore("=") to it.substringAfter("=").removeSurrounding("\"") }
}

/** The database of an installation, as db-init and migrate leave it: the image and initdb of deploy/postgres.env. */
object SampleDatabase {
    fun container(): PostgreSQLContainer =
        PostgreSQLContainer(DockerImageName.parse(pin.getValue("SARD_POSTGRES_IMAGE")).asCompatibleSubstituteFor("postgres"))
            .withEnv("POSTGRES_INITDB_ARGS", pin.getValue("SARD_POSTGRES_INITDB_ARGS"))
            .withEnv("TZ", "UTC")

    /** db-init: deploy/db/bootstrap.sql without psql's meta-commands, then the passwords of the login roles. */
    fun bootstrap(container: PostgreSQLContainer) {
        val script = File(requireNotNull(System.getProperty("sample.bootstrap-sql"))).readLines()
        container.createConnection("").use { connection ->
            connection.createStatement().use { it.execute(script.filterNot { line -> line.startsWith("\\") }.joinToString("\n")) }
            for (role in LOGIN_ROLES) {
                connection.createStatement().use { it.execute("alter role $role password '${password(role)}'") }
            }
        }
    }

    /** The migrate step; [extensions] are the locations of extensions, the default one (the sample's) if null. */
    fun migrate(
        container: PostgreSQLContainer,
        extensions: List<String>? = null,
    ) {
        val user = "sard_migrator"
        val out = PrintStream(OutputStream.nullOutputStream())
        val settings =
            if (extensions == null) {
                MigrationSettings(container.jdbcUrl, user, password(user))
            } else {
                MigrationSettings(container.jdbcUrl, user, password(user), extensions)
            }
        Migrator(settings, out).run()
    }

    /** A query as the superuser of the cluster. */
    fun <T> query(
        container: PostgreSQLContainer,
        sql: String,
        read: (java.sql.ResultSet) -> T,
    ): T =
        DriverManager.getConnection(container.jdbcUrl, container.username, container.password).use { connection ->
            connection.createStatement().use { statement -> statement.executeQuery(sql).use { rs -> rs.next(); read(rs) } }
        }
}

/** A prepared installation (db-init and migrate with the sample on the classpath) and a server that connects to it. */
@TestConfiguration(proxyBeanMethods = false)
class SampleInstallation {
    @Bean
    fun postgres(): PostgreSQLContainer =
        SampleDatabase.container().also {
            it.start()
            SampleDatabase.bootstrap(it)
            SampleDatabase.migrate(it)
        }

    @Bean
    fun appConnection(postgres: PostgreSQLContainer) =
        object : JdbcConnectionDetails {
            override fun getJdbcUrl(): String = postgres.jdbcUrl

            override fun getUsername(): String = "sard_app"

            override fun getPassword(): String = password("sard_app")
        }

    /** The wizard's work, done: the keys are confirmed and there is an administrator. */
    @Bean
    @DependsOn("flywayInitializer")
    fun completedInstallation(jdbc: JdbcTemplate) =
        InitializingBean {
            jdbc.update("insert into onboarding_steps (step, completed_at) values ('keys_confirmed', now()) on conflict do nothing")
            JdbcAdministrators(jdbc).create(PasswordHasher().hash(ADMIN_PASSWORD), Instant.now())
        }
}

/** The CA directory and the database are one installation: every context gets a directory of its own. */
class FreshPkiDirectory : ApplicationContextInitializer<ConfigurableApplicationContext> {
    override fun initialize(context: ConfigurableApplicationContext) {
        TestPropertyValues.of("sard.pki.dir=${Files.createTempDirectory("sample-pki")}").applyTo(context.environment)
    }
}
