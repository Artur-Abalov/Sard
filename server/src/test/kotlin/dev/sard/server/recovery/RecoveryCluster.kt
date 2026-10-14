// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.recovery

import dev.sard.server.onboarding.Database
import org.flywaydb.core.Flyway
import org.testcontainers.postgresql.PostgreSQLContainer
import org.testcontainers.utility.DockerImageName
import java.sql.DriverManager
import java.util.concurrent.atomic.AtomicInteger

private const val OWNER_PASSWORD = "appowner-password"

/**
 * A PostgreSQL cluster of its own for the tests about roles: roles belong to the cluster, so a role made by
 * one test must not be seen by another. It logs every statement, to prove a password never reaches its log.
 */
object RecoveryCluster {
    val container: PostgreSQLContainer by lazy {
        PostgreSQLContainer(DockerImageName.parse("postgres:18-alpine"))
            .withCommand("postgres", "-c", "log_statement=all")
            .also { it.start() }
    }
    private val counter = AtomicInteger()

    private fun admin(block: (java.sql.Statement) -> Unit) = container.createConnection("").use { c -> c.createStatement().use(block) }

    /** No role of the dump in the cluster, as in a cluster that never ran a server. */
    fun dropDumpRole() = admin { it.execute("drop role if exists sard_self") }

    /**
     * A database as pg_restore --no-owner leaves it in a new cluster: every migration recorded and its tables there,
     * the role sard_self not, and the privileges of PUBLIC that the migration took away back at their defaults.
     */
    fun restoredDatabase(superuser: Boolean = true): Database {
        dropDumpRole()
        val name = "restored_${counter.incrementAndGet()}"
        val base = "jdbc:postgresql://${container.host}:${container.firstMappedPort}/$name"
        admin { it.execute("create database $name") }
        Flyway
            .configure()
            .dataSource(base, container.username, container.password)
            .load()
            .migrate()
        admin { it.execute("drop role if exists sard_self") }
        DriverManager.getConnection(base, container.username, container.password).use { c ->
            c.createStatement().use {
                it.execute("grant temporary on database $name to public")
                it.execute("grant execute on function lo_create(oid), lo_creat(integer) to public")
            }
        }
        return if (superuser) Database(base, container.username, container.password) else ownedBy(name, base)
    }

    private fun ownedBy(
        name: String,
        url: String,
    ): Database {
        val owner = "appowner_${counter.get()}"
        admin { it.execute("create role $owner login nosuperuser password '$OWNER_PASSWORD'") }
        DriverManager.getConnection(url, container.username, container.password).use { c ->
            c.createStatement().use {
                it.execute("alter database $name owner to $owner")
                it.execute("alter schema public owner to $owner")
                it.execute(
                    "do $$ declare t text; begin for t in select tablename from pg_tables where schemaname = 'public' " +
                        "loop execute format('alter table %I owner to $owner', t); end loop; end $$",
                )
            }
        }
        return Database(url, owner, OWNER_PASSWORD)
    }
}
