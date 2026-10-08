// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import com.github.dockerjava.api.model.AccessMode
import com.github.dockerjava.api.model.Bind
import com.github.dockerjava.api.model.ExposedPort
import com.github.dockerjava.api.model.Volume
import org.junit.jupiter.api.extension.AfterAllCallback
import org.junit.jupiter.api.extension.BeforeAllCallback
import org.junit.jupiter.api.extension.ExtensionContext
import org.junit.jupiter.api.extension.TestWatcher
import org.testcontainers.DockerClientFactory
import org.testcontainers.containers.GenericContainer
import org.testcontainers.containers.Network
import org.testcontainers.containers.output.ToStringConsumer
import org.testcontainers.containers.startupcheck.OneShotStartupCheckStrategy
import org.testcontainers.postgresql.PostgreSQLContainer
import org.testcontainers.utility.DockerImageName
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.nio.file.Files
import java.nio.file.Path
import java.security.SecureRandom
import java.sql.Connection
import java.sql.DriverManager
import java.time.Duration
import java.util.HexFormat

/**
 * One Sard installation per test class: its own network, PostgreSQL and
 * sard-server from the image of the current code. Register it as a static
 * extension:
 *
 * ```
 * companion object {
 *     @JvmField @RegisterExtension val sard = SardEnvironment()
 * }
 * ```
 *
 * Nothing is shared between classes: no fixed container names, host ports are
 * random, the server's CA is created on its first start. As in
 * deploy/docker-compose.yml, the CA ([PKI_DIR]) and the database's data live on
 * volumes of their own, so [recreateServer] gives a new server container the
 * same CA (T3s, stand limit С1). When a test or the
 * start fails, the output of every container, with secrets masked, goes to
 * `test/e2e/build/e2e-logs/<class>/<test>/<container>.log`.
 *
 * [serverEnv] adds or overrides server variables (a test that breaks the
 * configuration on purpose uses it).
 */
class SardEnvironment(
    serverEnv: Map<String, String> = emptyMap(),
) : BeforeAllCallback,
    AfterAllCallback,
    TestWatcher {
    // Not "network": inside a container's apply {} that name is the container's own getNetwork().
    private val sardNetwork = Network.newNetwork()
    private val dbPassword = randomHex()
    /** The administrator's password (`SARD_ADMIN_PASSWORD`), for signing in to the REST API. */
    internal val adminPassword = randomHex()
    private val secrets = mutableSetOf(dbPassword, adminPassword)
    private val containers = linkedMapOf<String, Tracked>()
    // Output of server containers [recreateServer] removed; [serverLogs] starts with it.
    private val retiredServerLogs = StringBuilder()
    private val volumes = mutableListOf<String>()

    // Created in [start], when Docker is up; the containers mount them on creation.
    private lateinit var pkiVolume: String
    private lateinit var dbVolume: String

    val postgres: PostgreSQLContainer =
        track(
            POSTGRES_ALIAS,
            PostgreSQLContainer(DockerImageName.parse(E2e.POSTGRES_IMAGE)).apply {
                withNetwork(sardNetwork)
                withNetworkAliases(POSTGRES_ALIAS)
                withDatabaseName("sard")
                withUsername("sard")
                withPassword(dbPassword)
                withCreateContainerCmdModifier { it.hostConfig?.withBinds(Bind(dbVolume, Volume(PG_DATA_DIR))) }
            },
        )

    val server: GenericContainer<*> =
        track(
            SERVER_ALIAS,
            GenericContainer<Nothing>(DockerImageName.parse(E2e.serverImage)).apply {
                withNetwork(sardNetwork)
                withNetworkAliases(SERVER_ALIAS)
                withEnv(defaultServerEnv() + serverEnv)
                withExposedPorts(HTTP_PORT, GRPC_PORT)
                withCreateContainerCmdModifier { it.hostConfig?.withBinds(Bind(pkiVolume, Volume(PKI_DIR))) }
                waitingFor(HealthyOrExited("/actuator/health", HTTP_PORT).withStartupTimeout(STARTUP_TIMEOUT))
            },
        )

    /** Base URL of the REST API as the host sees it. */
    val httpBase: String get() = "http://${server.host}:${hostPort(HTTP_PORT)}"

    /** The gRPC port for agents as the host sees it; TLS names are those of [AGENT_ENDPOINT]. */
    val grpcHost: String get() = server.host
    val grpcPort: Int get() = hostPort(GRPC_PORT)

    // Read from Docker each time, not from the start's cached inspect: a restart may move the port.
    private fun hostPort(port: Int): Int =
        server.currentContainerInfo.networkSettings.ports.bindings
            .getValue(ExposedPort.tcp(port))
            .first()
            .hostPortSpec
            .toInt()

    /** The Docker network of this installation, for containers a test adds (the agent). */
    val dockerNetwork: Network get() = sardNetwork

    /** Masks [value] in every log this environment writes (tokens, keys a test handles). */
    fun secret(value: String) {
        secrets += value
    }

    /** Adds [container] to the log collection and stops it with the environment. */
    fun <T : GenericContainer<*>> track(
        name: String,
        container: T,
    ): T {
        val output = ToStringConsumer()
        container.withLogConsumer(output)
        containers[name] = Tracked(container, output)
        return container
    }

    /**
     * A new named Docker volume, removed with the environment (and by the Testcontainers reaper if
     * the run dies first): an agent host's disk that outlives the containers run on it.
     */
    fun volume(): String {
        val name = "sard-e2e-${randomHex()}"
        val labels = DockerClientFactory.DEFAULT_LABELS + (DockerClientFactory.TESTCONTAINERS_SESSION_ID_LABEL to DockerClientFactory.SESSION_ID)
        DockerClientFactory.instance().client().createVolumeCmd().withName(name).withLabels(labels).exec()
        volumes += name
        return name
    }

    /**
     * Starts PostgreSQL, then the server; on failure writes the logs under
     * `<logsDir>/<testClass>/start/` and rethrows.
     */
    fun start(testClass: String) {
        try {
            pkiVolume = volume()
            dbVolume = volume()
            postgres.start()
            server.start()
        } catch (e: RuntimeException) {
            writeLogs(testClass, "start")
            throw e
        }
    }

    /**
     * Removes the server's container and starts a new one from the same image, env, network alias
     * and volumes: the CA and the database survive, the host ports change ([httpBase], [grpcPort]
     * follow). Agents dial the alias, so they see a server that went away and came back.
     */
    fun recreateServer() {
        retiredServerLogs.append(server.logs)
        server.stop()
        server.start()
    }

    /**
     * A move to a new machine (F8, ADR 0052): the CA of the running server is copied, owner-only and
     * owned by the server's user, into a volume of its own; the server is replaced by one with an empty
     * CA volume that imports from there (`SARD_PKI_IMPORT_DIR`, mounted read-only). The database,
     * the network alias and so the agents stay: the new server is "B" with a restored database.
     * Returns the fingerprint of the CA both servers share.
     */
    fun moveServerImportingCa(): String {
        val oldCa = ServerTls.fingerprint(ServerTls.presentedChain(this).last())
        val importVolume = volume()
        copyCaTo(importVolume)
        retiredServerLogs.append(server.logs)
        server.stop()
        pkiVolume = volume()
        server.withEnv("SARD_PKI_IMPORT_DIR", IMPORT_DIR)
        server.withCreateContainerCmdModifier {
            val host = checkNotNull(it.hostConfig)
            host.withBinds(*host.binds, Bind(importVolume, Volume(IMPORT_DIR), AccessMode.ro))
        }
        server.start()
        return oldCa
    }

    /** The old server's `ca` directory in [importVolume], as docs/operator/08 prepares it. */
    private fun copyCaTo(importVolume: String) {
        val copy =
            GenericContainer<Nothing>(DockerImageName.parse(E2e.serverImage)).apply {
                withCreateContainerCmdModifier {
                    it.withUser("0").withEntrypoint("sh")
                    it.hostConfig?.withBinds(Bind(pkiVolume, Volume("/pki"), AccessMode.ro), Bind(importVolume, Volume("/import")))
                }
                withCommand(
                    "-c",
                    "mkdir -p /import/ca && cp /pki/ca/ca.crt /pki/ca/ca.key /import/ca/ && chown -R 10001:10001 /import" +
                        " && chmod 700 /import /import/ca && chmod 600 /import/ca/*",
                )
                withStartupCheckStrategy(OneShotStartupCheckStrategy())
            }
        copy.start()
        copy.stop()
    }

    /**
     * `docker restart` of the server's container: the same container, so the same volumes, alias
     * and log. The host ports may change; [httpBase] and [grpcPort] follow. Returns once the
     * server's health answers 200 again.
     */
    fun restartServer() {
        server.dockerClient.restartContainerCmd(server.containerId).exec()
        val health = HttpClient.newHttpClient()
        Await.until("health of the restarted server", STARTUP_TIMEOUT) {
            runCatching {
                val request = HttpRequest.newBuilder(URI.create("$httpBase/actuator/health")).timeout(Duration.ofSeconds(1)).build()
                health.send(request, HttpResponse.BodyHandlers.discarding()).statusCode()
            }.getOrNull() == 200
        }
    }

    /** Everything every server container of this installation has written so far, unmasked. */
    fun serverLogs(): String = retiredServerLogs.toString() + server.logs

    /** Stops every container, the last added first, then removes the volumes and the network. */
    fun stop() {
        containers.values.reversed().forEach { it.container.stop() }
        val docker = DockerClientFactory.instance().client()
        volumes.forEach { runCatching { docker.removeVolumeCmd(it).exec() } }
        sardNetwork.close()
    }

    /** Writes the masked output of every container to `<logsDir>/<testClass>/<step>/`; returns that directory. */
    fun writeLogs(
        testClass: String,
        step: String,
    ): Path {
        val dir = E2e.logsDir.resolve(testClass).resolve(fileName(step))
        Files.createDirectories(dir)
        containers.forEach { (name, tracked) ->
            Files.writeString(dir.resolve("$name.log"), Redaction.apply(output(tracked), secrets))
        }
        return dir
    }

    // The consumer stops following a container at its restart; `docker logs` has every start of
    // it, as long as the container exists (the server's: every container of it).
    private fun output(tracked: Tracked): String =
        runCatching { if (tracked.container === server) serverLogs() else tracked.container.logs }
            .getOrNull()
            ?.takeIf { it.isNotEmpty() }
            ?: tracked.output.toUtf8String()

    /** A connection to the server's database as its own user, from the host. */
    fun database(): Connection = DriverManager.getConnection(postgres.jdbcUrl, postgres.username, postgres.password)

    override fun beforeAll(context: ExtensionContext) = start(context.requiredTestClass.simpleName)

    override fun afterAll(context: ExtensionContext) = stop()

    override fun testFailed(
        context: ExtensionContext,
        cause: Throwable?,
    ) {
        writeLogs(context.requiredTestClass.simpleName, context.requiredTestMethod.name)
    }

    private fun defaultServerEnv() =
        mapOf(
            "SARD_DB_URL" to "jdbc:postgresql://$POSTGRES_ALIAS:5432/sard",
            "SARD_DB_USER" to "sard",
            "SARD_DB_PASSWORD" to dbPassword,
            // The server certificate's SAN; the endpoint's host must be one of them.
            "SARD_PKI_SERVER_NAMES" to SERVER_ALIAS,
            "SARD_AGENT_ENDPOINT" to AGENT_ENDPOINT,
            // The server refuses to start without it (ADR 0021); 32 hex characters.
            "SARD_ADMIN_PASSWORD" to adminPassword,
        )

    private class Tracked(
        val container: GenericContainer<*>,
        val output: ToStringConsumer,
    )

    companion object {
        const val SERVER_ALIAS = "sard-server"
        const val POSTGRES_ALIAS = "postgres"
        const val HTTP_PORT = 8080
        const val GRPC_PORT = 9090

        /** What agents inside the network dial; also the name the server certificate carries. */
        const val AGENT_ENDPOINT = "$SERVER_ALIAS:$GRPC_PORT"

        /** Where the server keeps its PKI (`SARD_PKI_DIR`, ADR 0014): a volume, as `sard-pki` in deploy/. */
        const val PKI_DIR = "/var/lib/sard/pki"

        /** Where the import source is mounted in the new server (`SARD_PKI_IMPORT_DIR`, F8). */
        const val IMPORT_DIR = "/var/lib/sard/pki-import"

        /** Where the server keeps its CA. */
        const val CA_CERT_PATH = "$PKI_DIR/ca/ca.crt"

        /** The data volume of the postgres:18 image (PGDATA is a directory below it). */
        private const val PG_DATA_DIR = "/var/lib/postgresql"

        private val STARTUP_TIMEOUT = Duration.ofMinutes(3)
        private val random = SecureRandom()

        private fun randomHex() = HexFormat.of().formatHex(ByteArray(16).also(random::nextBytes))

        private fun fileName(step: String) = step.replace(Regex("[^A-Za-z0-9._-]+"), "_")
    }
}
