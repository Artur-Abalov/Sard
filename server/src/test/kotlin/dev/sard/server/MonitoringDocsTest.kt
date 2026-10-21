// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import dev.sard.server.retention.RetentionProperties
import tools.jackson.databind.JsonNode
import tools.jackson.dataformat.yaml.YAMLMapper
import java.io.File
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private val FENCE = Regex("""```yaml\n(.*?)\n\s*```""", RegexOption.DOT_MATCHES_ALL)
private val SARD_NAME = Regex("""\bsard_[a-z_]+""")
private val RELATIVE_LINK = Regex("""\]\(((?!https?:|#|mailto:)[^)#\s]+)(?:#[^)]*)?\)""")
private val SUFFIXES = listOf("", "_total", "_count", "_sum", "_bucket", "_max")

private fun JsonNode.items(): List<JsonNode> = iterator().asSequence().toList()

/**
 * The scenarios tagged `@doc` of docs/specs/server/server-metrics.feature: the operator's pages "Мониторинг" and
 * "Хранение данных сервера" say what the server does. The test reads files of the repository, no Spring context.
 */
class MonitoringDocsTest {
    private val root = File("..").canonicalFile
    private val yaml = YAMLMapper()

    private fun text(path: String): String {
        val file = File(root, path)
        check(file.isFile) { "expected to run with the server module as the working directory: $file" }
        return file.readText()
    }

    private fun yamlBlocks(page: String): List<String> = FENCE.findAll(text(page)).map { it.groupValues[1] }.toList()

    /** The names of the metrics of Sard as the exposition writes them, from the one file that declares them. */
    private fun declaredNames(): Set<String> {
        val declarations = Regex("""const val \w+ = "(sard\.[a-z.]+)"""")
        val names = declarations.findAll(text("server/src/main/kotlin/dev/sard/server/metrics/SardMetricNames.kt"))
        return names.map { it.groupValues[1].replace('.', '_') }.toSet()
    }

    private fun scrapeConfig(): JsonNode {
        val block = yamlBlocks("docs/operator/14-monitoring.md").single { "scrape_configs" in it }
        return yaml.readTree(block)
    }

    private fun rules(): List<JsonNode> {
        val block = yamlBlocks("docs/operator/14-monitoring.md").single { "groups:" in it }
        return yaml
            .readTree(block)
            .path("groups")
            .items()
            .flatMap { it.path("rules").items() }
    }

    @Test
    fun `Страницы Мониторинг и Хранение данных есть и включены в оглавление`() {
        val readme = text("docs/operator/README.md")
        assertTrue("| 14 | [Мониторинг](14-monitoring.md)" in readme)
        assertTrue("| 15 | [Хранение данных сервера](15-data-retention.md)" in readme)
        val page = text("docs/operator/14-monitoring.md")
        for (variable in METRICS_VARIABLES) assertTrue(variable in page, variable)
    }

    @Test
    fun `Страница Мониторинг предупреждает об открытии порта наружу и даёт пример override`() {
        val page = text("docs/operator/14-monitoring.md")
        assertTrue("HTTP без TLS" in page && "только с токеном" in page && "docker inspect" in page)
        val override = yamlBlocks("docs/operator/14-monitoring.md").single { "services:" in it }
        val ports =
            yaml
                .readTree(override)
                .path("services")
                .path("server")
                .path("ports")
                .items()
                .map { it.asString() }
        assertEquals(listOf("<10.0.0.5>:8081:8081"), ports)
    }

    @Test
    fun `Пример конфигурации Prometheus берёт токен из файла и не содержит его значения`() {
        val job = scrapeConfig().path("scrape_configs").items().single()

        assertEquals("sard", job.path("job_name").asString())
        assertEquals("/metrics", job.path("metrics_path").asString())
        val authorization = job.path("authorization")
        assertTrue(authorization.has("credentials_file"))
        assertFalse(authorization.has("credentials"))
    }

    @Test
    fun `Правила алертов покрывают нужное и называют только метрики спецификации`() {
        val rules = rules()
        val names = rules.map { it.path("alert").asString() }.toSet()
        val required =
            setOf(
                "SardBackupTooOld",
                "SardSourceNeverBackedUp",
                "SardVerificationTooOld",
                "SardAgentCertificateExpires",
                "SardCaExpires",
                "SardSelfBackupFailing",
                "SardAgentOffline",
                "SardMetricsStateStale",
            )
        assertEquals(required, names)
        val allowed = declaredNames().flatMap { name -> SUFFIXES.map { name + it } }.toSet()
        for (rule in rules) {
            val expression = rule.path("expr").asString()
            val used = SARD_NAME.findAll(expression).map { it.value }.toList()
            assertTrue(used.isNotEmpty(), expression)
            assertTrue(used.all { it in allowed }, "unknown metric in: $expression")
            assertEquals(expression.count { it == '(' }, expression.count { it == ')' }, expression)
        }
    }

    @Test
    fun `Страница Хранение данных называет свойства и переменные с умолчаниями сервера`() {
        val page = text("docs/operator/15-data-retention.md")
        val defaults = RetentionProperties()
        val expected =
            mapOf(
                "step-logs" to days(defaults.stepLogs),
                "runs" to days(defaults.runs),
                "verifications" to days(defaults.verifications),
                "schedule-fires" to days(defaults.scheduleFires),
                "notification-deliveries" to days(defaults.notificationDeliveries),
                "interval" to "${defaults.interval.toHours()}h",
                "batch-size" to "${defaults.batchSize}",
            )
        for ((property, default) in expected) {
            val variable = "SARD_RETENTION_" + property.replace("-", "").uppercase()
            val row = page.lines().single { "`sard.retention.$property`" in it }
            assertTrue("`$variable`" in row, "$property: $variable")
            assertTrue(row.trimEnd().endsWith("| `$default` |"), "$property: default $default in $row")
        }
    }

    private fun days(term: Duration) = "${term.toDays()}d"

    @Test
    fun `Страница Хранение данных называет то, что не удаляется никогда, ограничения и требование к L1`() {
        val page = text("docs/operator/15-data-retention.md")
        assertTrue("## Что не удаляется никогда" in page)
        for (part in listOf("Снимки", "Последний успешный запуск", "Активные запуски", "Последние проверки")) {
            assertTrue(part in page, part)
        }
        for (limit in listOf("**R2.**", "**R3.**", "**В9.**")) assertTrue(limit in page, limit)
        assertTrue("запуск удаляется или\nпомечается, когда L1 забыл его снимок" in page)
        assertTrue("sard_retention_deleted_total" in page)
    }

    @Test
    fun `Раздел безопасности и справочник конфигурации называют порт и переменные метрик`() {
        val security = text("docs/operator/10-security.md")
        val row = security.lines().single { it.startsWith("| порт метрик") }
        assertTrue("только loopback внутри контейнера, не опубликован" in row && "токен" in row)
        val configuration = text("docs/operator/03-configuration.md")
        for (variable in METRICS_VARIABLES) assertTrue("`$variable`" in configuration, variable)
    }

    @Test
    fun `Относительные ссылки страниц оператора ведут на существующие файлы`() {
        val directory = File(root, "docs/operator")
        val broken =
            directory.listFiles { f -> f.extension == "md" }.orEmpty().flatMap { page ->
                RELATIVE_LINK
                    .findAll(page.readText())
                    .map { it.groupValues[1] }
                    .filterNot { File(page.parentFile, it).exists() }
                    .map { "${page.name}: $it" }
                    .toList()
            }
        assertEquals(emptyList(), broken)
    }
}
