// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private val ISOLATED_PACKAGES =
    listOf(
        "enrollment",
        "persistence",
        "pki",
        "extension",
        "registration",
        "runs",
        "scheduler",
        "fleet",
        "notify",
        "install",
        "downloads",
        "selfagent",
        "selfbackup",
        "retention",
        "metrics",
        "prometheus",
    )

// downloads/ holds both domain types and the HTTP adapter that serves the package files. Only the
// files that actually import org.springframework.web / jakarta.servlet are web by purpose:
// AgentPackagesHandler.kt and AgentDownloadsConfiguration.kt. The rest of the package is scanned.
private val WIRE_EXEMPT_FILES = setOf("AgentPackagesHandler.kt", "AgentDownloadsConfiguration.kt")
private val WIRE_FREE_PACKAGES = ISOLATED_PACKAGES
private val WIRE_ANNOTATION_REFERENCE =
    Regex("""\b(com\.fasterxml\.jackson\.annotation|io\.swagger|org\.springframework\.web|jakarta\.servlet)\b""")
private val FORBIDDEN_FQN_REFERENCE =
    Regex("""\b(io\.grpc|dev\.sard\.proto|com\.google\.rpc|com\.google\.protobuf|dev\.sard\.server\.agents)\.""")
private val OTHER_SERVER_PACKAGE = Regex("""\bdev\.sard\.server\.(?!console\b)\w+""")
private val API_PACKAGE_REFERENCE = Regex("""\bdev\.sard\.server\.api\b""")
private val CONSOLE_PACKAGE_REFERENCE = Regex("""\bdev\.sard\.server\.console\b""")
private val TELEGRAM_REFERENCE = Regex("""\b(dev\.sard\.server\.notify\.telegram|Telegram\w*)\b""")
private const val NOTIFY_COMPOSITION_ROOT = "NotifyConfiguration.kt"
private val AGENT_ENTITY_REFERENCE =
    Regex("""\bfrom\s+Agent\b|\bAgent::class\b|\bdev\.sard\.server\.persistence\.Agent\b""")
private val SELFAGENT_REFERENCE = Regex("""\bdev\.sard\.server\.selfagent\b""")
private val SESSIONS_SYSTEM_CALL = Regex("""\bsessions\.system\s*[({]""")
private val DATABASE_REFERENCE =
    Regex("""\b(org\.springframework\.jdbc|java\.sql|jakarta\.persistence|org\.flywaydb)\b""")
private val SCHEDULER_TABLES_REFERENCE = Regex("""\b(ScheduleRecord|ScheduleFireRecord|schedule_fires|schedules)\b""")
private val STRING_CONCATENATION = Regex(""""\s*\+\s*"""")
private val STRING_LITERAL = Regex("""\"{3}(.*?)\"{3}|"((?:[^"\\]++|\\.)*+)"""", RegexOption.DOT_MATCHES_ALL)
private val NATIVE_SQL_SOURCE =
    Regex("""\bimport org\.springframework\.jdbc\b|\bcreateNative(Mutation)?Query\b|\bprepareStatement\b""")
private val METRIC_NAME_CONSTANT = Regex("""const\s+val\s+(\w+)\s*=\s*"(sard\.[a-z.]+)"""")
private val METRIC_BUILDER_LITERAL =
    Regex(
        """\b(Counter|Gauge|Timer|DistributionSummary|LongTaskTimer|FunctionCounter|TimeGauge)""" +
            """\s*\.builder\(\s*"sard\.|\.(counter|gauge|timer|summary)\(\s*"sard\.""",
    )
private const val EXPECTED_METRIC_NAMES = 31
private val TOKEN_STRING_COMPARISON =
    Regex(
        """\.equals\(|contentEquals|\b(token|offered|expected|authorization)\s*[!=]=\s*+(?!null\b)""" +
            """|[!=]=\s*(token|offered|expected|authorization)\b""",
    )
private val SQL_CONSTANT =
    Regex("""\bconst\s+val\s+\w+\s*=\s*\"{1,3}\s*(select|delete|update|insert|with)\b""", RegexOption.IGNORE_CASE)

/** A file that runs native SQL by a JDBC call, or keeps its queries as constants (retention/RetentionSql.kt). */
private fun isNativeSqlSource(text: String): Boolean {
    val callsJdbc = NATIVE_SQL_SOURCE.containsMatchIn(text)
    return callsJdbc || SQL_CONSTANT.containsMatchIn(text)
}

private val CREATE_TABLE = Regex("""CREATE TABLE (\w+) \((.*?)\n\);""", RegexOption.DOT_MATCHES_ALL)
private val TENANT_ID_COLUMN = Regex("""^\s*tenant_id\s+UUID\b""", RegexOption.MULTILINE)
private val ADD_TENANT_ID = Regex("""ALTER TABLE (\w+)\s+ADD COLUMN tenant_id\b""")
private val NAMED_LITERAL =
    Regex(
        """\bval\s+(\w+)\s*(?::\s*String\s*)?=\s*(?:\"{3}(.*?)\"{3}|"((?:[^"\\]++|\\.)*+)")""",
        RegexOption.DOT_MATCHES_ALL,
    )
private val INTERPOLATION = Regex("""\$\{?(\w+)}?""")
private const val INTERPOLATION_DEPTH = 4
private val SQL_COMMENT = Regex("""--.*$""", RegexOption.MULTILINE)

// ADR 0013, rule 8: installation-wide by design, listed there. Per file and per query (the literal as the
// detector sees it, constants expanded): any other query in the same file is still checked.
private val TENANT_SQL_EXCEPTIONS =
    mapOf(
        // A CA replacement revokes the unused tokens of the whole installation.
        "TokensRevokedOnCaReplacement.kt" to
            setOf(
                "update enrollment_tokens set revoked_at = ? where builtin = false and used_at is null " +
                    "and revoked_at is null and expires_at > ?",
            ),
        // LAST_SLOT: the latest catch-up slot over all tenants, read in the system session (ADR 0013 system callers).
        "Scheduler.kt" to setOf("select max(catch_up_at) from schedules"),
        // "Is any agent certificate issued?" decides whether the installation's CA may be replaced.
        "JdbcCaLedger.kt" to setOf("select 1 from agent_certificates"),
    )
private val LINE_COMMENT = Regex("""//.*$""", RegexOption.MULTILINE)
private val BLOCK_COMMENT = Regex("""/\*.*?\*/""", RegexOption.DOT_MATCHES_ALL)

// W1b (B1): the *Api ports (SessionApi and its siblings) stay implementable and
// testable without a servlet request or response, so an enterprise starter can
// replace one (e.g. SessionApi with SSO) without depending on this module's web layer.
private val API_PORT_FORBIDDEN_TYPE =
    Regex("""\b(HttpServletRequest|HttpServletResponse|ResponseEntity|ResponseCookie)\b""")
private val AUTH_PACKAGE_REFERENCE = Regex("""\bdev\.sard\.server\.auth\b""")
private val SESSION_DOMAIN_FORBIDDEN =
    Regex("""\b(jakarta\.servlet|org\.springframework\.http|org\.springframework\.web)\b""")
private val SESSION_DOMAIN_FILES =
    listOf(
        "onboarding/OnboardingService.kt",
        "onboarding/SetupCodes.kt",
        "onboarding/SetupSessions.kt",
        "onboarding/SetupCodeAnnouncer.kt",
        "auth/SessionStore.kt",
        "auth/LoginAttemptTracker.kt",
        "auth/SessionApiImpl.kt",
        "auth/PasswordHasher.kt",
        "auth/PasswordRules.kt",
        "auth/AdminSetup.kt",
    )

/** Source text with `//` and `/* */` comments stripped, so a comment mentioning a forbidden
 * package (e.g. in a KDoc example) never trips the scan, but any real reference — import or
 * fully-qualified use in a function body — does. */
private fun withoutComments(text: String): String = text.replace(BLOCK_COMMENT, "").replace(LINE_COMMENT, "")

/**
 * A dependency-free source scan, not a JVM classpath/reflection check: greps the .kt sources
 * under `src/main/kotlin`. Keeps two boundaries from ADR 0013 and the S2b review honest:
 *  a) `enrollment/`, `persistence/`, `pki/`, `extension/`, `registration/`, `runs/`, `fleet/` and
 *     `notify/` never import the gRPC/protobuf boundary or the `agents/` package that adapts
 *     domain errors to it — those packages stay usable without a gRPC server, wire format or
 *     the agents' translation layer.
 *  b) `TenantSessions.system` (the one call that bypasses the tenant filter) is used only where
 *     ADR 0013 lists it: `EnrollmentTokens.ownerOf` (a token before its tenant is known) and
 *     `AgentCertificateStandings.of` (a certificate by serial during each agent call, S3; the serials
 *     of all open streams on each stream check, S5a) and `StepCounts.waiting` (the dispatch metric, S6a).
 */
class ArchitectureTest {
    private val mainRoot: File
        get() {
            val dir = File("src/main/kotlin/dev/sard/server")
            check(dir.isDirectory) { "expected to run with the server module as the working directory: $dir" }
            return dir
        }

    private fun ktFiles(root: File): List<File> =
        root
            .walkTopDown()
            .filter { it.isFile && it.extension == "kt" }
            .toList()

    @Test
    fun `the domain packages reference nothing from the gRPC or agents boundary`() {
        val offenders = mutableListOf<String>()
        for (pkg in ISOLATED_PACKAGES) {
            for (file in ktFiles(File(mainRoot, pkg))) {
                val text = withoutComments(file.readText())
                val references = FORBIDDEN_FQN_REFERENCE.findAll(text).map { it.groupValues[1] }.toList()
                for (reference in references) {
                    offenders += "${file.path}: $reference."
                }
            }
        }
        assertTrue(offenders.isEmpty(), "forbidden references:\n${offenders.joinToString("\n")}")
    }

    @Test
    fun `pki knows the database only through the CaLedger port`() {
        val offenders =
            ktFiles(File(mainRoot, "pki")).flatMap { file ->
                DATABASE_REFERENCE
                    .findAll(withoutComments(file.readText()))
                    .map { "${file.path}: ${it.value}" }
                    .toList()
            }
        assertTrue(offenders.isEmpty(), "database in pki:\n${offenders.joinToString("\n")}")
    }

    @Test
    fun `the domain packages never reference the api package`() {
        val offenders =
            ISOLATED_PACKAGES.flatMap { pkg ->
                ktFiles(File(mainRoot, pkg)).filter {
                    API_PACKAGE_REFERENCE.containsMatchIn(withoutComments(it.readText()))
                }
            }
        assertTrue(offenders.isEmpty(), "domain referencing api:\n${offenders.joinToString("\n") { it.path }}")
    }

    @Test
    fun `the domain packages carry no wire or HTTP annotations`() {
        val offenders =
            WIRE_FREE_PACKAGES.flatMap { pkg ->
                ktFiles(File(mainRoot, pkg)).filter { it.name !in WIRE_EXEMPT_FILES }.flatMap { file ->
                    WIRE_ANNOTATION_REFERENCE
                        .findAll(withoutComments(file.readText()))
                        .map { "${file.path}: ${it.value}" }
                        .toList()
                }
            }
        assertTrue(offenders.isEmpty(), "wire concerns in the domain:\n${offenders.joinToString("\n")}")
    }

    @Test
    fun `the channel-neutral notify files reference nothing from Telegram`() {
        val offenders =
            File(mainRoot, "notify")
                .listFiles { f -> f.isFile && f.extension == "kt" && f.name != NOTIFY_COMPOSITION_ROOT }
                .orEmpty()
                .flatMap { file ->
                    TELEGRAM_REFERENCE
                        .findAll(withoutComments(file.readText()))
                        .map { "${file.path}: ${it.value}" }
                        .toList()
                }
        assertTrue(offenders.isEmpty(), "telegram in channel-neutral code:\n${offenders.joinToString("\n")}")
    }

    @Test
    fun `the api package references nothing from the gRPC or agents boundary`() {
        val offenders =
            ktFiles(File(mainRoot, "api")).flatMap { file ->
                FORBIDDEN_FQN_REFERENCE
                    .findAll(withoutComments(file.readText()))
                    .map { "${file.path}: ${it.groupValues[1]}." }
                    .toList()
            }
        assertTrue(offenders.isEmpty(), "api referencing the agent boundary:\n${offenders.joinToString("\n")}")
    }

    @Test
    fun `runs reads nothing of the scheduler's tables, so the journal has one owner`() {
        val offenders =
            ktFiles(File(mainRoot, "runs")).flatMap { file ->
                SCHEDULER_TABLES_REFERENCE
                    .findAll(withoutComments(file.readText()))
                    .map { "${file.path}: ${it.value}" }
                    .toList()
            }
        assertTrue(offenders.isEmpty(), "runs reading the scheduler's tables:\n${offenders.joinToString("\n")}")
    }

    @Test
    fun `sessions system is called only by the callers ADR 0013 lists`() {
        val allowed =
            listOf(
                "agents/AgentCertificateStandings.kt",
                "enrollment/EnrollmentTokens.kt",
                "fleet/AgentCertificateCheck.kt",
                "freshness/FreshnessCheck.kt",
                "notify/Deliveries.kt",
                "prometheus/StateQueries.kt",
                "retention/RetentionPass.kt",
                "runs/StepCounts.kt",
                "runs/StepDeadlines.kt",
                "scheduler/Scheduler.kt",
                "verify/VerificationLoop.kt",
            ).map { File(mainRoot, it) }
        val callers = ktFiles(mainRoot).filter { SESSIONS_SYSTEM_CALL.containsMatchIn(it.readText()) }
        assertEquals(allowed.toSet(), callers.toSet(), "sessions.system callers must match ADR 0013's list exactly")
    }

    @Test
    fun `the retention counter is named in one place of main`() {
        val named = ktFiles(mainRoot).sumOf { Regex("sard\\.retention\\.deleted").findAll(it.readText()).count() }

        assertEquals(1, named, "the name of the counter of deleted rows is written once, in main")
    }

    private fun metricNames(): Map<String, String> {
        val file = File(mainRoot, "metrics/SardMetricNames.kt")
        check(file.isFile) { "the names of the metrics of Sard are declared in $file" }
        val declarations = METRIC_NAME_CONSTANT.findAll(withoutComments(file.readText()))
        return declarations.associate { it.groupValues[1] to it.groupValues[2] }
    }

    @Test
    fun `the names of the metrics of Sard are declared in one file and each is written once in main`() {
        val names = metricNames()
        val sources = ktFiles(mainRoot).associateWith { withoutComments(it.readText()) }

        assertTrue(names.size >= EXPECTED_METRIC_NAMES, "the file declares only ${names.size} names")
        assertEquals(names.size, names.values.toSet().size, "a name is declared twice: ${names.values}")
        for ((constant, name) in names) {
            val written = sources.values.sumOf { Regex(Regex.escape("\"$name\"")).findAll(it).count() }
            assertEquals(1, written, "$constant = \"$name\" is written $written times in main")
        }
        val others = sources.filterKeys { it.name != "SardMetricNames.kt" }
        val outside = others.filterValues { METRIC_BUILDER_LITERAL.containsMatchIn(it) }.keys.map { it.name }
        assertTrue(outside.isEmpty(), "a metric named by a literal outside the names file: $outside")
    }

    @Test
    fun `the token of the metrics listener is compared in constant time over bytes`() {
        val files = ktFiles(File(mainRoot, "prometheus"))
        val token = withoutComments(File(mainRoot, "prometheus/BearerToken.kt").readText())

        assertTrue("MessageDigest.isEqual(" in token, "the token is compared by MessageDigest.isEqual")
        val offenders = files.filter { TOKEN_STRING_COMPARISON.containsMatchIn(withoutComments(it.readText())) }
        assertTrue(offenders.isEmpty(), "string comparison near the token: ${offenders.map { it.name }}")
        assertTrue(TOKEN_STRING_COMPARISON.containsMatchIn("if (token == offered) ok"))
        assertTrue(TOKEN_STRING_COMPARISON.containsMatchIn("header.equals(token)"))
        assertTrue(!TOKEN_STRING_COMPARISON.containsMatchIn("MessageDigest.isEqual(expected, offered)"))
    }

    @Test
    fun `a metric builder given a literal name is found by the scan`() {
        val fixtures =
            listOf(
                "Counter.builder(\"sard.x\").register(m)",
                "Counter\n.builder(\n\"sard.x.y\")",
                "meters.counter(\"sard.x\")",
                "Gauge.builder(\"sard.q\", a) { 1.0 }",
                "Timer.builder(\"sard.q\")",
            )
        for (fixture in fixtures) assertTrue(METRIC_BUILDER_LITERAL.containsMatchIn(fixture), fixture)
        assertTrue(!METRIC_BUILDER_LITERAL.containsMatchIn("Counter.builder(SardMetricNames.RETENTION_DELETED)"))
    }

    @Test
    fun `retention deletes log lines by whole partitions only`() {
        val lineDeletes =
            ktFiles(File(mainRoot, "retention")).filter {
                Regex("""delete\s+from\s+step_logs\b""", RegexOption.IGNORE_CASE)
                    .containsMatchIn(withoutComments(it.readText()))
            }

        assertTrue(lineDeletes.isEmpty(), "DELETE from step_logs: ${lineDeletes.joinToString { it.name }}")
    }

    @Test
    fun `StepResults takes partial from the verdict and does not judge the status itself`() {
        val text = withoutComments(File(mainRoot, "runs/StepResults.kt").readText())

        assertTrue("StepState.SUCCEEDED" !in text, "partial is decided by ResultCheck only (ADR 0034)")
    }

    /** The text of every `interface *Api { ... }` block, braces balanced, in [file]. */
    private fun apiPortBlocks(file: File): List<String> {
        val text = withoutComments(file.readText())
        val results = mutableListOf<String>()
        for (match in Regex("""interface \w*Api\b[^{]*\{""").findAll(text)) {
            var depth = 1
            var i = match.range.last + 1
            while (i < text.length && depth > 0) {
                when (text[i]) {
                    '{' -> depth++
                    '}' -> depth--
                }
                i++
            }
            results += text.substring(match.range.first, i)
        }
        return results
    }

    @Test
    fun `the Api ports declare no HTTP request, response or Spring web types`() {
        val offenders = mutableListOf<String>()
        for (file in ktFiles(File(mainRoot, "api"))) {
            for (block in apiPortBlocks(file)) {
                val hits = API_PORT_FORBIDDEN_TYPE.findAll(block).map { it.value }.toSet()
                if (hits.isNotEmpty()) offenders += "${file.path}: $hits"
            }
        }
        assertTrue(offenders.isEmpty(), "api ports depend on HTTP types:\n${offenders.joinToString("\n")}")
    }

    @Test
    fun `the api package never references the auth package`() {
        val offenders =
            ktFiles(File(mainRoot, "api")).filter {
                AUTH_PACKAGE_REFERENCE.containsMatchIn(withoutComments(it.readText()))
            }
        assertTrue(offenders.isEmpty(), "api referencing auth:\n${offenders.joinToString("\n") { it.path }}")
    }

    @Test
    fun `the session domain classes have no HTTP or Spring web dependency`() {
        val offenders =
            SESSION_DOMAIN_FILES
                .map { File(mainRoot, it) }
                .filter { SESSION_DOMAIN_FORBIDDEN.containsMatchIn(withoutComments(it.readText())) }
        val message = "session domain classes depend on HTTP:\n${offenders.joinToString("\n") { it.path }}"
        assertTrue(offenders.isEmpty(), message)
    }

    @Test
    fun `the console package references no other server package`() {
        val offenders =
            ktFiles(File(mainRoot, "console")).filter {
                OTHER_SERVER_PACKAGE.containsMatchIn(withoutComments(it.readText()))
            }
        assertTrue(offenders.isEmpty(), "console referencing the server:\n${offenders.joinToString("\n") { it.path }}")
    }

    @Test
    fun `no other package references the console package`() {
        val offenders =
            ktFiles(mainRoot).filter { file ->
                file
                    .relativeTo(mainRoot)
                    .path
                    .startsWith("console")
                    .not() &&
                    CONSOLE_PACKAGE_REFERENCE.containsMatchIn(withoutComments(file.readText()))
            }
        assertTrue(offenders.isEmpty(), "references to console:\n${offenders.joinToString("\n") { it.path }}")
    }

    @Test
    fun `selfagent asks fleet about agents and never queries the Agent entity itself`() {
        val offenders =
            ktFiles(File(mainRoot, "selfagent")).filter {
                AGENT_ENTITY_REFERENCE.containsMatchIn(withoutComments(it.readText()))
            }
        val message = "selfagent querying agents directly:\n${offenders.joinToString("\n") { it.path }}"
        assertTrue(offenders.isEmpty(), message)
    }

    @Test
    fun `no package depends on selfagent, so the mechanism stays removable`() {
        val offenders =
            ktFiles(mainRoot).filter { file ->
                !file.relativeTo(mainRoot).path.startsWith("selfagent") &&
                    SELFAGENT_REFERENCE.containsMatchIn(withoutComments(file.readText()))
            }
        assertTrue(offenders.isEmpty(), "references to selfagent:\n${offenders.joinToString("\n") { it.path }}")
    }

    private fun literalText(match: MatchResult): String = match.groupValues[2] + match.groupValues[3]

    /** The literal with the `$NAME` of the constants of the same file replaced by their text. */
    private fun expanded(
        literal: String,
        constants: Map<String, String>,
    ): String =
        (1..INTERPOLATION_DEPTH).fold(literal) { text, _ ->
            INTERPOLATION.replace(text) { constants[it.groupValues[1]] ?: it.value }
        }

    /** Every table with a `tenant_id` column, read from the Flyway migrations, so a new tenant table is
     * covered without anybody remembering to list it here. */
    private fun tenantTables(): Set<String> {
        val dir = File("src/main/resources/db/migration")
        check(dir.isDirectory) { "expected the Flyway migrations at $dir" }
        val sql = dir.listFiles { f -> f.extension == "sql" }!!.map { it.readText().replace(SQL_COMMENT, "") }
        val created =
            sql.flatMap { text ->
                CREATE_TABLE
                    .findAll(text)
                    .filter { TENANT_ID_COLUMN.containsMatchIn(it.groupValues[2]) }
                    .map { it.groupValues[1] }
            }
        val altered = sql.flatMap { text -> ADD_TENANT_ID.findAll(text).map { it.groupValues[1] } }
        return (created + altered).toSet()
    }

    /** Flyway refuses to start on two migrations of one version (DUPLICATE_VERSIONED_MIGRATION); parallel
     * branches picking the same timestamp is how that happens. */
    @Test
    fun `every migration has a version of its own`() {
        val dir = File("src/main/resources/db/migration")
        val versions = dir.listFiles { f -> f.extension == "sql" }!!.groupBy { it.name.substringBefore("__") }
        val shared = versions.filterValues { it.size > 1 }.mapValues { (_, files) -> files.map { it.name }.sorted() }
        assertEquals(emptyMap(), shared)
    }

    /** Scenario "Миграции ядра не содержат переноса объектов между схемами" (DB2): the schema is right from V1. */
    @Test
    fun `Миграции ядра не содержат переноса объектов между схемами`() {
        val dir = File("src/main/resources/db/migration")
        val transfers = Regex("""SET\s+SCHEMA|REASSIGN\s+OWNED|\bpublic\b""", RegexOption.IGNORE_CASE)
        val offenders =
            dir.listFiles { f -> f.extension == "sql" }!!.flatMap { file ->
                transfers
                    .findAll(file.readText().replace(SQL_COMMENT, ""))
                    .map { "${file.name}: ${it.value}" }
                    .toList()
            }
        assertEquals(emptyList(), offenders)
    }

    @Test
    fun `the tenant tables are read from the migrations`() {
        val tables = tenantTables()

        assertTrue(
            tables.containsAll(listOf("agents", "sources", "runs", "enrollment_tokens", "self_backups", "schedules")),
            "tenant tables: $tables",
        )
        val wide = setOf("tenants", "onboarding_steps", "ca_origins")
        assertTrue(tables.none { it in wide }, "installation-wide: $tables")
    }

    private fun tenantTableRegex(): Regex {
        val names = tenantTables().joinToString("|")
        return Regex("""\b(from|update|into|join|delete)\s+($names)\b""", RegexOption.IGNORE_CASE)
    }

    /** The named string literals of [source] (`val NAME = "..."`), by name. */
    private fun namedLiterals(source: String): Map<String, String> {
        val text = withoutComments(source).replace(STRING_CONCATENATION, "")
        return NAMED_LITERAL.findAll(text).associate { it.groupValues[1] to literalText(it) }
    }

    /** The string literals of [source] (constants expanded) that read a tenant table without `tenant_id`. */
    private fun unscopedTenantSql(
        source: String,
        tenantTable: Regex,
        shared: Map<String, String> = emptyMap(),
    ): List<String> {
        val text = withoutComments(source).replace(STRING_CONCATENATION, "")
        val constants = shared + namedLiterals(source)
        return STRING_LITERAL
            .findAll(text)
            .map { expanded(it.groupValues[1] + it.groupValues[2], constants).trim() }
            .filter { tenantTable.containsMatchIn(it) && "tenant_id" !in it }
            .toList()
    }

    @Test
    fun `an exempt file is still checked for every query that is not listed`() {
        val fixture =
            """
            import org.springframework.jdbc.core.JdbcTemplate
            private const val LAST_SLOT = "select max(catch_up_at) from schedules"
            fun a() = jdbc.query(LAST_SLOT)
            fun b() = jdbc.query("select * from runs")
            """.trimIndent()
        val listed = TENANT_SQL_EXCEPTIONS.getValue("Scheduler.kt")

        val reported = unscopedTenantSql(fixture, tenantTableRegex()).filter { it !in listed }

        assertEquals(listOf("select * from runs"), reported)
    }

    @Test
    fun `a file of SQL constants is checked by rule 8 like a file that calls JDBC`() {
        val file = File(mainRoot, "retention/RetentionSql.kt")
        val text = file.readText()

        assertTrue(isNativeSqlSource(text), "RetentionSql.kt holds its queries as constants and must be scanned")
        val tenantTable = tenantTableRegex()
        val literals = STRING_LITERAL.findAll(withoutComments(text))
        val scanned = literals.count { tenantTable.containsMatchIn(it.value) }
        assertTrue(scanned >= 5, "the scan sees only $scanned queries of RetentionSql.kt")
        val quoted = "private const val BAD = \"delete from runs where id = 1\""
        val raw = "private const val BAD = \"\"\"delete from runs where id = 1\"\"\""
        for (fixture in listOf(quoted, raw)) {
            assertTrue(isNativeSqlSource(fixture), fixture)
            assertEquals(listOf("delete from runs where id = 1"), unscopedTenantSql(fixture, tenantTableRegex()))
        }
    }

    @Test
    fun `native SQL on a tenant table names tenant_id (ADR 0013, rule 8)`() {
        val tenantTable = tenantTableRegex()
        // A fragment such as STEP is declared in one file and used in another.
        val shared = ktFiles(mainRoot).flatMap { namedLiterals(it.readText()).toList() }.toMap()
        val found = mutableMapOf<String, Set<String>>()
        val offenders =
            ktFiles(mainRoot)
                .filter { isNativeSqlSource(it.readText()) }
                .flatMap { file ->
                    val unscoped = unscopedTenantSql(file.readText(), tenantTable, shared)
                    val listed = TENANT_SQL_EXCEPTIONS[file.name].orEmpty()
                    found[file.name] = unscoped.filter { it in listed }.toSet()
                    unscoped.filter { it !in listed }.map { "${file.path}: $it" }
                }
        val message = "native SQL on a tenant table without tenant_id:\n${offenders.joinToString("\n")}"
        assertTrue(offenders.isEmpty(), message)
        val stale =
            TENANT_SQL_EXCEPTIONS.flatMap { (file, queries) ->
                (queries - found[file].orEmpty()).map { "$file: $it" }
            }
        assertTrue(stale.isEmpty(), "exceptions that match no query:\n${stale.joinToString("\n")}")
    }
}
