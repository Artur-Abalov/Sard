// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.database

import dev.sard.server.EmbeddedPostgres
import dev.sard.server.TestAdmin
import org.testcontainers.postgresql.PostgreSQLContainer
import java.io.File
import java.io.OutputStream
import java.io.PrintStream
import java.sql.Connection
import java.sql.DriverManager
import java.util.concurrent.atomic.AtomicInteger

/** The roles of Sard that log in and get a password; sard_monitor has none. */
val LOGIN_ROLES = listOf("sard_migrator", "sard_app", "sard_backup")

/** The text of deploy/db/bootstrap.sql without the psql meta-commands (`\set ...`), which JDBC cannot run. */
object BootstrapScript {
    val text: String by lazy {
        val path = requireNotNull(System.getProperty("sard.test.bootstrap-sql")) { "run the tests with Gradle" }
        File(path).readLines().filterNot { it.startsWith("\\") }.joinToString("\n")
    }
}

/** What a database goes through before a server starts on it (DB2): db-init's bootstrap, passwords, then migrate. */
object Preparation {
    /** What [role] connects with in the tests. */
    fun password(role: String) = "$role-password"

    /** Runs deploy/db/bootstrap.sql as the superuser of [admin], then gives the login roles their passwords. */
    fun bootstrap(admin: Connection) {
        admin.createStatement().use { it.execute(BootstrapScript.text) }
        for (role in LOGIN_ROLES) {
            admin.createStatement().use { it.execute("alter role $role password '${password(role)}'") }
        }
    }

    /** The migrate command against [url], as sard_migrator, with [extensions] as the locations of extensions. */
    fun migrate(
        url: String,
        extensions: List<String> = emptyList(),
    ): MigrationOutcome {
        val settings = MigrationSettings(url, "sard_migrator", password("sard_migrator"), extensions)
        return Migrator(settings, PrintStream(OutputStream.nullOutputStream())).run()
    }
}

/** A PostgreSQL cluster of its own with its superuser; each test class that needs roles to be absent has one. */
class Cluster(
    val container: PostgreSQLContainer = EmbeddedPostgres.container().also { it.start() },
) : AutoCloseable {
    private val counter = AtomicInteger()

    init {
        TestAdmin.register(container)
    }

    /** The superuser's connection to [database]. */
    fun admin(database: String = container.databaseName): Connection =
        DriverManager.getConnection(url(database), container.username, container.password)

    fun url(database: String = container.databaseName): String {
        val address = "${container.host}:${container.firstMappedPort}"
        return "jdbc:postgresql://$address/$database"
    }

    fun newDatabase(): String {
        val name = "db_${counter.incrementAndGet()}"
        admin().use { c -> c.createStatement().use { it.execute("create database $name") } }
        return name
    }

    /** Runs deploy/db/bootstrap.sql in [database] as the superuser, then gives the login roles their passwords. */
    fun bootstrap(database: String = container.databaseName) {
        admin(database).use(Preparation::bootstrap)
    }

    fun password(role: String) = Preparation.password(role)

    /** [role] connected to [database] by the password the tests gave it. */
    fun connect(
        role: String,
        database: String = container.databaseName,
    ): Connection = DriverManager.getConnection(url(database), role, password(role))

    /** One value of a query as the superuser. */
    fun <T> query(
        sql: String,
        database: String = container.databaseName,
        read: (java.sql.ResultSet) -> T,
    ): T =
        admin(database).use { c ->
            c.createStatement().use { s ->
                s.executeQuery(sql).use { rs ->
                    rs.next()
                    read(rs)
                }
            }
        }

    override fun close() = container.stop()
}

/**
 * What a database's catalog says about the roles of Sard, as text: the roles with their attributes and settings, who is
 * a member of whom, and for the database [database] its schema, the owner and the privileges of every object in it,
 * the default privileges and the privileges of the database. Two databases that are alike have the same text.
 */
fun Cluster.catalog(database: String = container.databaseName): String {
    val queries =
        listOf(
            """select 'role ' || r.rolname || ' login=' || r.rolcanlogin || ' super=' || r.rolsuper || ' createrole=' ||
                      r.rolcreaterole || ' createdb=' || r.rolcreatedb || ' repl=' || r.rolreplication || ' bypassrls=' ||
                      r.rolbypassrls || ' limit=' || r.rolconnlimit
               from pg_roles r where r.rolname like 'sard\_%' order by 1""",
            """select 'setting ' || r.rolname || ' ' || s.setconfig::text from pg_db_role_setting s
               join pg_roles r on r.oid = s.setrole where r.rolname like 'sard\_%' and s.setdatabase = 0 order by 1""",
            """select 'member ' || u.rolname || ' of ' || g.rolname || ' inherit=' || m.inherit_option || ' set=' || m.set_option
               from pg_auth_members m join pg_roles g on g.oid = m.roleid join pg_roles u on u.oid = m.member
               where u.rolname like 'sard\_%' order by 1""",
            """select 'schema ' || n.nspname || ' owner=' || n.nspowner::regrole || ' acl=' || coalesce(n.nspacl::text, '-')
               from pg_namespace n where n.nspname in ('sard', 'public') order by 1""",
            """select 'relation ' || c.relname || ' kind=' || c.relkind::text || ' owner=' || c.relowner::regrole || ' acl=' ||
                      coalesce(c.relacl::text, '-')
               from pg_class c where c.relnamespace = 'sard'::regnamespace and c.relkind in ('r', 'p', 'S', 'v',
                   'm') order by 1""",
            """select 'function ' || replace(p.oid::regprocedure::text, 'sard.', '') || ' owner=' || p.proowner::regrole || ' acl=' ||
                      coalesce(p.proacl::text, '-') || ' config=' || coalesce(p.proconfig::text, '-')
               from pg_proc p where p.pronamespace = 'sard'::regnamespace order by 1""",
            """select 'default ' || d.defaclrole::regrole || ' ' || coalesce(d.defaclnamespace::regnamespace::text, '*') || ' ' ||
                      d.defaclobjtype::text || ' ' || d.defaclacl::text from pg_default_acl d order by 1""",
            """select 'database acl=' || coalesce(d.datacl::text, '-')
               from pg_database d where d.datname = current_database()""",
        )
    return admin(database).use { c ->
        queries.joinToString("\n") { sql ->
            c.createStatement().use { s ->
                s.executeQuery(sql).use { rs ->
                    generateSequence {
                        if (rs.next()) {
                            rs.getString(1)
                        } else {
                            null
                        }
                    }.joinToString("\n")
                }
            }
        }
    }
}
