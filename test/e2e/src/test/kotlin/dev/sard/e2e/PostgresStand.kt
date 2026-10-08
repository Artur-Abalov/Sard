// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.testcontainers.containers.GenericContainer
import org.testcontainers.containers.wait.strategy.Wait
import org.testcontainers.images.builder.Transferable
import org.testcontainers.utility.DockerImageName
import java.time.Duration
import java.util.UUID
import kotlin.test.assertEquals

/**
 * The password P of the spec: a quote, a colon, a backslash, a space and a non-ASCII letter. It is the
 * password of the role `backup` and the content of the agent's secret `pg-app`.
 */
internal const val PG_PASSWORD = "p'a:s\\s w0rd-Ж"

/** The superuser password of the servers of the stand (used from the host's side only through `docker exec`). */
private const val PG_ADMIN_PASSWORD = "admin"

/**
 * A PostgreSQL server of the official image on the environment's network (F1 ПГ20), reachable from
 * the agents by [alias]. [sql] and [query] run psql inside the container as its superuser.
 */
internal class PgServer(
    sard: SardEnvironment,
    val major: Int,
    val alias: String = "db",
    image: String = E2e.pgServerImage(major),
) {
    val container: GenericContainer<*> =
        sard.track(
            "pg$major-$alias",
            GenericContainer<Nothing>(DockerImageName.parse(image)).apply {
                withNetwork(sard.dockerNetwork)
                withNetworkAliases(alias)
                withEnv("POSTGRES_PASSWORD", PG_ADMIN_PASSWORD)
                // The image starts a temporary server to initialise, then the real one: two messages.
                waitingFor(Wait.forLogMessage(".*database system is ready to accept connections.*", 2).withStartupTimeout(STARTUP))
            },
        )

    fun start(): PgServer = apply { container.start() }

    /** Runs each statement with `psql -U postgres -d [database]`; fails unless psql exits 0. */
    fun sql(
        vararg statements: String,
        database: String = "postgres",
    ): String {
        val flags = statements.flatMap { listOf("-c", it) }
        val result = container.execInContainer("psql", "-U", "postgres", "-d", database, "-X", "-A", "-t", "-v", "ON_ERROR_STOP=1", *flags.toTypedArray())
        assertEquals(0, result.exitCode, "psql ${statements.toList()}: ${result.stderr}")
        return result.stdout
    }

    /** The one value or row [statement] returns, without the line ending. */
    fun query(
        statement: String,
        database: String = "app",
    ): String = sql(statement, database = database).trim()

    /** Runs [command] in the container; returns its result without judging the exit code. */
    fun exec(vararg command: String) = container.execInContainer(*command)

    /** Puts [bytes] at [path] of the server's disk, readable by everyone. */
    fun put(
        path: String,
        bytes: ByteArray,
    ) = container.copyFileToContainer(Transferable.of(bytes), path)

    /**
     * The database `app` of the scenarios: tables with data, a sequence, a view, a large object; the role
     * `backup` with the password [PG_PASSWORD] and `pg_read_all_data`; and the roles of the restore scenario
     * (F1 ПГ17i): `app_owner` owns the tables, `app_rw` and `app_ro` log in, `app_admin` has CREATEROLE and
     * BYPASSRLS.
     */
    fun seed() {
        val q = "\$\$"
        sql(
            "create role backup login password $q$PG_PASSWORD$q",
            "grant pg_read_all_data to backup",
            // pg_read_all_data does not cover large objects (docs/plugins/postgresql.md).
            "alter role backup set lo_compat_privileges = on",
            "create database app",
        )
        sql(
            "create role app_owner nologin",
            "create role app_rw login password 'qa-rw-pass' in role app_owner",
            "alter role app_rw set search_path = public",
            "create role app_ro login createdb",
            "create role app_admin login createrole bypassrls",
        )
        sql(
            "create table t(id int primary key, v text)",
            "insert into t select g, md5(g::text) from generate_series(1, 20000) g",
            "create table u(id int primary key, note text)",
            "insert into u select g, repeat('u', g % 7) from generate_series(1, 500) g",
            "alter table t owner to app_owner",
            "alter table u owner to app_owner",
            "create sequence s",
            "select setval('s', 42)",
            "create view big_t as select id from t where id > 19000",
            "select lo_from_bytea(4242, 'a large object'::bytea)",
            database = "app",
        )
    }

    /** What the tables, the sequence, the large object and the roles of a [seed]ed database look like (F1 ПГ17i). */
    fun fingerprint(database: String): List<String> =
        listOf(
            "select count(*) || ' ' || md5(string_agg(v, '' order by id)) from t",
            "select count(*) || ' ' || md5(string_agg(note, '' order by id)) from u",
            "select last_value from s",
            "select encode(lo_get(4242), 'escape')",
            "select string_agg(tablename || '>' || tableowner, ',' order by tablename) from pg_tables where schemaname = 'public'",
            "select count(*) from big_t",
        ).map { query(it, database) } + clusterFingerprint()

    /** The roles of the cluster but the bootstrap superuser: attributes, membership, settings. */
    private fun clusterFingerprint(): List<String> =
        listOf(
            """select string_agg(rolname || ':' || rolcanlogin || rolcreatedb || rolcreaterole || rolbypassrls || rolinherit || rolsuper,
                  ',' order by rolname) from pg_roles where rolname <> 'postgres' and rolname not like 'pg\_%'""",
            """select coalesce(string_agg(r.rolname || '>' || m.rolname, ',' order by r.rolname, m.rolname), '') from pg_auth_members a
                  join pg_roles r on r.oid = a.member join pg_roles m on m.oid = a.roleid where r.rolname <> 'postgres'""",
            """select coalesce(string_agg(rolname || '=' || c, ',' order by rolname, c), '') from pg_roles r, unnest(r.rolconfig) c
                  where r.rolconfig is not null""",
        ).map { query(it, "postgres") }

    private companion object {
        val STARTUP: Duration = Duration.ofMinutes(3)
    }
}

/**
 * An agent host on the official postgres image of [major] (F1 ПГ20), enrolled, its repository `main`
 * initialised, the secret `pg-app` holding [PG_PASSWORD], the agent running and registered. Sources of the
 * `postgresql` plugin are created and run through the REST API like the console does.
 */
internal class PgAgent(
    private val sard: SardEnvironment,
    val major: Int,
    hostname: String,
) {
    val agent: EnrolledAgent =
        AgentEnroller.enroll(sard, EnrollmentTokens.create(sard), hostname = hostname, local = LOCAL, image = E2e.pgAgentImage(major))
    val container: GenericContainer<*>

    init {
        val init = agent.host.repoInit(REPOSITORY, "--generate-password")
        assertEquals(0, init.code, init.stderr)
        sard.secret(PG_PASSWORD)
        container = sard.track("agent-$hostname", AgentContainer.of(agent, mapOf(SECRET_FILE to "$PG_PASSWORD\n"))).apply { start() }
        Await.until("repository_id of $REPOSITORY in Register of $hostname") { Backups.repositoryId(sard, agent.agentId, REPOSITORY) != null }
    }

    /** Creates a source `[name]` of the plugin `postgresql` with [config] and starts a run of it. */
    fun backup(
        name: String,
        config: String = config(),
    ): Backups.Started = Backups.start(sard, agent.agentId, name, config, plugin = "postgresql", repository = REPOSITORY)

    /** Runs the source again, as the console's "run now" does. */
    fun run(sourceId: UUID): Backups.Started = Backups.run(sard, sourceId)

    /** The message of the finished step, "" when it has none. */
    fun finish(started: Backups.Started): Backups.Step = Backups.awaitFinished(sard, started.stepId, Duration.ofMinutes(3))

    /** Replaces the file of the secret `pg-app`; the agent reads it again for every step. */
    fun setPassword(password: String) {
        sard.secret(password)
        container.copyFileToContainer(TarFiles.ownedByAgent("$password\n"), SECRET_FILE)
    }

    /** The phase the step of [started] is in, as the server recorded it; null before the first progress. */
    fun phase(started: Backups.Started): String? =
        sard.database().use { connection ->
            connection.prepareStatement("SELECT phase FROM run_steps WHERE id = ?").use { q ->
                q.setObject(1, started.stepId)
                q.executeQuery().use { if (it.next()) it.getString(1) else null }
            }
        }

    /** The ids of the snapshots in the repository of this host (restic in a container on its disk). */
    fun snapshots(vararg filter: String): List<String> {
        val exit = restic("snapshots", "--json", *filter)
        return SNAPSHOT_ID.findAll(exit.stdout).map { it.groupValues[1] }.toList()
    }

    /** How many locks the repository holds. */
    fun locks(): Int = restic("list", "locks").stdout.lines().count { it.isNotBlank() }

    /** The id of the snapshot of the global objects linked to the snapshot [main]. */
    fun globalsOf(main: String): String = snapshots("--tag", "postgresql.main_snapshot=$main").single()

    /** The tags of a snapshot, as `restic snapshots` lists them. */
    fun tags(snapshot: String): Set<String> {
        val json = restic("snapshots", "--json", snapshot).stdout
        val list = Regex("\"tags\":\\[([^\\]]*)]").find(json)?.groupValues?.get(1).orEmpty()
        return Regex("\"([^\"]*)\"").findAll(list).map { it.groupValues[1] }.toSet()
    }

    /** The bytes of [file] (a path in the snapshot, as in `/app.dump`) restored from [snapshot]. */
    fun restored(
        snapshot: String,
        file: String,
    ): ByteArray {
        var bytes: ByteArray? = null
        val target = "${AgentHost.STATE_DIR}/restored-$snapshot"
        val exit =
            agent.host.run(
                AgentImage.RESTIC_BINARY, "restore", snapshot, "--repo", REPOSITORY_URL, "--password-file", PASSWORD_FILE,
                "--no-cache", "--target", target,
            ) { c -> bytes = c.copyFileFromContainer("$target$file") { it.readAllBytes() } }
        check(exit.code == 0) { "restic restore exited ${exit.code}: ${exit.stderr}" }
        return checkNotNull(bytes) { "$file is not in snapshot $snapshot" }
    }

    /** The command lines of every process of the agent's container (a shell reads /proc: the image has one). */
    fun processes(): String {
        val result = container.execInContainer("sh", "-c", "for p in /proc/[0-9]*; do tr '\\0' ' ' < \$p/cmdline; echo; done")
        return result.stdout
    }

    private fun restic(vararg args: String): AgentHost.Exit {
        val exit = agent.host.run(AgentImage.RESTIC_BINARY, *args, "--repo", REPOSITORY_URL, "--password-file", PASSWORD_FILE, "--no-cache")
        check(exit.code == 0) { "restic ${args.first()} exited ${exit.code}: ${exit.stderr}" }
        return exit
    }

    companion object {
        const val REPOSITORY = "main"
        const val REPOSITORY_URL = "${AgentHost.STATE_DIR}/repo"
        const val PASSWORD_FILE = "${AgentHost.STATE_DIR}/$REPOSITORY.pass"
        const val SECRET_FILE = "/etc/sard/pg-app"
        private val SNAPSHOT_ID = Regex("\"id\":\"([0-9a-f]{64})\"")

        private val LOCAL =
            """
            repositories:
              - name: $REPOSITORY
                url: $REPOSITORY_URL
                password_file: $PASSWORD_FILE
            secrets:
              pg-app: $SECRET_FILE
            """.trimIndent() + "\n"

        /**
         * The config K of the spec (agent/plugins/postgresql/config.go) for the server `db`, with
         * [extra] fields (JSON object members, such as `"port":6432`) after the base ones. TLS is off by
         * default: the official image has no TLS.
         */
        fun config(
            vararg extra: String,
            database: String = "app",
            host: String = "db",
            user: String = "backup",
            tlsMode: String = "disable",
        ): String =
            "{\"host\":\"$host\",\"database\":\"$database\",\"user\":\"$user\",\"password_ref\":\"pg-app\"," +
                (extra.toList() + "\"tls_mode\":\"$tlsMode\"").joinToString(",") + "}"

        /** Same, with the global objects off: one snapshot (the spec's K). */
        fun configK(
            vararg extra: String,
            database: String = "app",
            host: String = "db",
            user: String = "backup",
            tlsMode: String = "disable",
        ): String = config(*extra, "\"include_globals\":false", database = database, host = host, user = user, tlsMode = tlsMode)
    }
}
