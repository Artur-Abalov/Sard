// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import org.yaml.snakeyaml.Yaml
import java.io.File
import kotlin.io.path.createTempDirectory
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/**
 * What `deploy/docker-compose.yml` says about the roles and passwords of the database (DB2, docs/specs/server/
 * database-roles.feature): who mounts which password, what waits for what, that the files db-init and the database
 * need are files next to it (no copy of them inside), and that a release ships every one of them. The running stack
 * is checked by scripts/test-self-agent.sh.
 */
class DeployDatabaseComposeTest {
    private val compose: Map<String, Any?> by lazy {
        val file = File("../deploy/docker-compose.yml")
        check(file.isFile) { "expected to run with the server module as the working directory: $file" }
        @Suppress("UNCHECKED_CAST")
        Yaml().load<Map<String, Any?>>(file.readText())
    }

    @Suppress("UNCHECKED_CAST")
    private fun map(value: Any?): Map<String, Any?> = value as Map<String, Any?>

    private fun service(name: String): Map<String, Any?> = map(map(compose["services"])[name])

    @Suppress("UNCHECKED_CAST")
    private fun volumes(service: String): List<Pair<String, String>> =
        ((service(service)["volumes"] as? List<Any?>).orEmpty()).map { entry ->
            when (entry) {
                is String -> entry.substringBefore(":") to entry.substringAfter(":")
                else -> map(entry).let { it["source"].toString() to it["target"].toString() }
            }
        }

    private fun services() = map(compose["services"]).keys

    private fun mounting(volume: String) =
        services()
            .filter { service ->
                volumes(service).any {
                    it
                        .first == volume
                }
            }.toSet()

    @Suppress("UNCHECKED_CAST")
    private fun dependsOn(service: String): Map<String, String> =
        map(service(service)["depends_on"]).mapValues { map(it.value)["condition"].toString() }

    private fun config(name: String) = map(map(compose["configs"])[name])["content"].toString().replace("$$", "$")

    /** The `configs: <name>: file:` paths, as written in the compose file. */
    private fun configFiles(): Map<String, String> =
        map(compose["configs"])
            .mapNotNull { (name, value) -> map(value)["file"]?.let { name to it.toString() } }
            .toMap()

    private fun configFile(name: String) = File("../deploy", configFiles().getValue(name)).readText()

    private fun releaseFiles(): File {
        val dest = createTempDirectory("release-files").toFile()
        val process =
            ProcessBuilder("../scripts/release-server-files.sh", dest.path, "v1.2.3")
                .redirectErrorStream(true)
                .start()
        val output = process.inputStream.bufferedReader().readText()
        assertEquals(0, process.waitFor(), output)
        return dest
    }

    @Test
    fun `Каждый файл, на который ссылается compose-файл релиза, является файлом релиза`() {
        val release = releaseFiles()
        val referenced = configFiles().values
        assertTrue(referenced.isNotEmpty())

        for (path in referenced) {
            assertTrue(path.startsWith("./db/"), "$path: a file of deploy/db/ under a relative path")
            assertTrue(File(release, path.removePrefix("./db/")).isFile, "$path is not among the files of the release")
            assertEquals(File("../deploy", path).readText(), File(release, path.removePrefix("./db/")).readText())
        }
        assertTrue(File(release, "docker-compose.yml").isFile)
        assertTrue("SARD_VERSION=1.2.3" in File(release, "sard.env.example").readLines())
    }

    @Test
    fun `Compose-файл не содержит копий db-init, bootstrap и конфигурации pg_hba`() {
        val text = File("../deploy/docker-compose.yml").readText()

        for (name in listOf("db-init-script", "bootstrap-sql", "postgresql-hba", "postgres-entrypoint")) {
            assertTrue(name in configFiles(), "$name is a file")
            assertFalse("content" in map(map(compose["configs"])[name]), name)
        }
        for (file in listOf("db-init.sh", "bootstrap.sql", "pg_hba.conf", "postgres-entrypoint.sh")) {
            val body = File("../deploy/db", file).readLines().filter { it.isNotBlank() && !it.startsWith("#") }
            assertTrue(body.none { it.trim() in text.lines().map(String::trim) }, "$file is inlined")
        }
        assertFalse("CREATE ROLE" in text)
        assertFalse("host all all all scram-sha-256" in text)
        assertTrue("/dev/urandom" !in text.lines().filterNot { it.trim().startsWith("#") }.joinToString("\n"))
    }

    @Test
    fun `Пароль суперпользователя делает файл postgres-entrypoint, и нигде не сохраняет`() {
        val entrypoint = configFile("postgres-entrypoint")

        assertEquals(listOf("sh", "/etc/sard/postgres-entrypoint.sh"), service("postgres")["entrypoint"])
        assertTrue("/dev/urandom" in entrypoint)
        assertTrue("exec docker-entrypoint.sh \"\$@\"" in entrypoint)
        assertTrue(entrypoint.lines().none { ">" in it && "POSTGRES_PASSWORD" in it })
    }

    @Test
    fun `Файл пароля роли виден только своим службам`() {
        assertEquals(setOf("db-init", "migrate"), mounting("sard-db-migrate"))
        assertEquals(setOf("db-init", "server"), mounting("sard-db-app"))
        assertEquals(setOf("db-init", "self-agent"), mounting("sard-db-backup"))
        assertTrue(mounting("sard-db-app").intersect(mounting("sard-db-migrate")) == setOf("db-init"))
    }

    @Test
    fun `Долгоживущие службы монтируют пароль только на чтение, а postgres не монтирует ни одного`() {
        for (service in listOf("server", "self-agent", "migrate")) {
            val passwords = volumes(service).filter { it.first.startsWith("sard-db-") }
            assertTrue(passwords.isNotEmpty(), service)
            val raw = (service(service)["volumes"] as List<*>).map { it.toString() }
            assertTrue(raw.filter { "sard-db-" in it }.all { "ro" in it || "read_only=true" in it }, "$service $raw")
        }
        assertEquals(emptyList(), volumes("postgres").filter { it.first.startsWith("sard-db-") })
    }

    @Test
    fun `Сервер получает только пароль sard_app и только файлом`() {
        val environment = map(service("server")["environment"])

        assertEquals("sard_app", environment["SARD_DB_USER"])
        assertEquals("/run/sard-db/password", environment["SARD_DB_PASSWORD_FILE"])
        assertFalse("SARD_DB_PASSWORD" in environment, environment.toString())
        assertFalse(environment.keys.any { it.startsWith("SARD_MIGRATE") }, environment.toString())
    }

    @Test
    fun `migrate запускает команду migrate образа сервера с паролем sard_migrator из файла`() {
        val migrate = service("migrate")

        assertEquals(service("server")["image"], migrate["image"])
        assertEquals(listOf("migrate"), migrate["command"])
        assertEquals("/run/sard-db/password", map(migrate["environment"])["SARD_MIGRATE_PASSWORD_FILE"])
        assertEquals("sard_migrator", map(migrate["environment"])["SARD_MIGRATE_USER"])
    }

    @Test
    fun `Сервер стартует только после db-init и migrate, а migrate после db-init`() {
        assertEquals("service_completed_successfully", dependsOn("server")["db-init"])
        assertEquals("service_completed_successfully", dependsOn("server")["migrate"])
        assertEquals("service_healthy", dependsOn("server")["postgres"])
        assertEquals("service_completed_successfully", dependsOn("migrate")["db-init"])
        assertEquals("service_healthy", dependsOn("db-init")["postgres"])
    }

    @Test
    fun `Пароль суперпользователя нигде не задан, вход суперпользователя по сети закрыт`() {
        val text = File("../deploy/docker-compose.yml").readText()

        assertFalse(
            services().any { "POSTGRES_PASSWORD" in (service(it)["environment"] as? Map<*, *>).orEmpty() },
            "POSTGRES_PASSWORD is set",
        )
        assertTrue(text.lines().none { it.trim().startsWith("POSTGRES_PASSWORD:") })
        val hba = configFile("postgresql-hba").lines()
        assertTrue("host all sard all reject" in hba, hba.toString())
        assertTrue(hba.indexOf("host all sard all reject") < hba.indexOf("host all all all scram-sha-256"))
        assertTrue("hba_file = '/etc/postgresql/pg_hba.conf'" in config("postgresql-conf"))
    }

    @Test
    fun `db-init идёт образом базы, restart no и локальным сокетом`() {
        val init = service("db-init")

        assertEquals(service("postgres")["image"], init["image"])
        assertEquals("no", init["restart"].toString())
        assertTrue(volumes("db-init").contains("postgres-socket" to "/var/run/postgresql"))
        assertTrue(volumes("postgres").contains("postgres-socket" to "/var/run/postgresql"))
    }

    @Test
    fun `В образце файла окружения нет переменных паролей базы`() {
        val names =
            File("../deploy/.env.example").readLines().filter {
                "DB_PASSWORD" in it &&
                    !it.trimStart().startsWith("# There")
            }

        assertEquals(emptyList(), names.filter { it.matches(Regex("""^\s*#?\s*[A-Z_]*DB_PASSWORD[A-Z_]*\s*=.*""")) })
        assertFalse(
            File("../deploy/docker-compose.yml").readLines().any {
                "SARD_DB_PASSWORD}" in it ||
                    "SARD_DB_PASSWORD:?" in it
            },
        )
    }
}
