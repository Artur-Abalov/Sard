// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.metrics

import com.zaxxer.hikari.HikariDataSource
import dev.sard.server.agents.results.MicrometerResultMetrics
import dev.sard.server.agents.results.ResultsConfiguration
import dev.sard.server.api.ApiClient
import dev.sard.server.notify.MicrometerNotifyMetrics
import dev.sard.server.pki.MovableClock
import dev.sard.server.retention.SceneTenant
import dev.sard.server.retention.T0
import dev.sard.server.runs.RunAnnouncer
import dev.sard.server.runs.RunFinished
import dev.sard.server.runs.RunKind
import dev.sard.server.runs.RunState
import dev.sard.server.runs.Trigger
import dev.sard.server.scheduler.FireKind
import dev.sard.server.scheduler.FireOutcome
import dev.sard.server.scheduler.MicrometerSchedulerMetrics
import io.micrometer.prometheusmetrics.PrometheusMeterRegistry
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.sql.Connection
import java.sql.Timestamp
import java.time.Duration
import java.util.UUID
import java.util.concurrent.Callable
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import javax.sql.DataSource
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private const val LISTENER_THREAD = "sard-metrics"
private const val BACKUP = "sard_source_last_backup_success_timestamp_seconds"
private val ALLOWED_LABELS =
    setOf("source_id", "role", "kind", "status", "outcome", "health", "channel", "reason", "le", "quantile")
private val EXPOSED_PREFIXES = listOf("sard_", "jvm_", "process_")
private val PROBES =
    listOf(
        "/actuator/prometheus",
        "/actuator/metrics",
        "/actuator/metrics/sard.agents.connected",
        "/metrics",
        "/metrics/",
        "//actuator/prometheus",
        "/actuator//prometheus",
        "/;x/actuator/prometheus",
        "/actuator;x/prometheus",
        "/actuator/prometheus;x",
        "/actuator/%70rometheus",
        "/ACTUATOR/PROMETHEUS",
        "/actuator/prometheus/",
        "/api/v1/../../actuator/prometheus",
    )

/** The exposition on the whole server: what a scrape shows, and that it never waits for the database. */
@MetricsServerTest
class MetricsScrapeIntegrationTest(
    @Autowired private val listener: MetricsListener,
    @Autowired private val loop: StateLoop,
    @Autowired private val refresh: StateRefresh,
    @Autowired private val registry: PrometheusMeterRegistry,
    @Autowired private val announcer: RunAnnouncer,
    @Autowired private val statements: StatementLog,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val dataSource: DataSource,
    @Autowired private val mapper: ObjectMapper,
    @LocalServerPort private val httpPort: Int,
) {
    private val http = HttpClient.newHttpClient()
    private val scenes = mutableListOf<SceneTenant>()
    private val startedAt = clock.now

    @BeforeTest
    fun `the clock stands at T0 of the specification`() {
        clock.now = T0
    }

    @AfterTest
    fun `drop what the test made and move the clock back`() {
        scenes.forEach { it.drop() }
        scenes.clear()
        clock.now = startedAt
    }

    private fun get(
        port: Int,
        path: String,
        authorization: String? = null,
    ): HttpResponse<String> {
        val request = HttpRequest.newBuilder(URI.create("http://127.0.0.1:$port$path"))
        authorization?.let { request.header("Authorization", it) }
        return http.send(request.build(), HttpResponse.BodyHandlers.ofString())
    }

    private fun scrape(): Exposition {
        val response = get(listener.boundPort, "/metrics")
        assertEquals(200, response.statusCode())
        return Exposition(response.body())
    }

    private fun tenant() = SceneTenant(jdbc).also { scenes += it }

    // ---- what the exposition holds ------------------------------------------------------------------------------

    @Test
    fun `Слушатель метрик запущен вместе с сервером, тик состояния идёт в фоне`() {
        assertTrue(listener.isRunning)
        assertTrue(loop.isRunning)
    }

    @Test
    fun `Scrape на loopback без токена отдаёт экспозицию с Content-Type text-plain version 0-0-4`() {
        val response = get(listener.boundPort, "/metrics")

        assertEquals(200, response.statusCode())
        assertEquals("text/plain; version=0.0.4; charset=utf-8", response.headers().firstValue("Content-Type").get())
        assertTrue(response.body().lines().any { it.startsWith("sard_agents_connected") }, "no sard_agents_connected")
    }

    @Test
    fun `Каждая метрика Sard из файла имён есть в экспозиции с префиксом sard_ и нет чужих`() {
        MicrometerResultMetrics(registry).recorded("succeeded")
        ResultsConfiguration().runFinishedTrace(registry).runFinished(finished())
        val notify = MicrometerNotifyMetrics(registry)
        notify.sent("telegram")
        notify.retried("telegram")
        notify.undelivered("telegram", "failed")
        MicrometerSchedulerMetrics(registry).fired(FireKind.SCHEDULE, FireOutcome.RUN_CREATED)
        val scene = tenant()
        scene.source()
        jdbc.update(
            "insert into agent_certificates (serial, tenant_id, agent_id, issued_at, not_after) values (?, ?, ?, ?, ?)",
            UUID.randomUUID().toString().replace("-", ""),
            scene.id,
            scene.agentId,
            Timestamp.from(T0.minus(Duration.ofDays(1))),
            Timestamp.from(T0.plus(Duration.ofDays(30))),
        )
        refresh.tick()

        val families = scrape().families()

        // The time of the last self-backup success needs a bound self-backup (MetricsStateIntegrationTest).
        val declared =
            SardMetricNames::class.java.declaredFields
                .filter { it.type == String::class.java }
                .map { it.get(null) as String }
                .filter { it != SardMetricNames.SELF_BACKUP_LAST_SUCCESS }
        assertTrue(declared.size >= 31)
        for (name in declared) {
            val prometheus = name.replace('.', '_')
            assertTrue(families.any { it.startsWith(prometheus) }, "$name is not in the exposition: $families")
        }
        assertTrue(families.all { family -> EXPOSED_PREFIXES.any(family::startsWith) }, families.toString())
    }

    @Test
    fun `Имена меток метрик Sard - только из допустимого набора, значения source_id - UUID`() {
        val scene = tenant()
        repeat(5) { scene.source("name-$it") }
        refresh.tick()

        val sard = scrape().samples.filter { it.name.startsWith("sard_") }

        val labels = sard.flatMap { it.labels.keys }
        assertTrue(labels.all { it in ALLOWED_LABELS }, labels.toString())
        for (id in sard.mapNotNull { it.labels["source_id"] }) UUID.fromString(id)
    }

    @Test
    fun `В экспозиции нет метрик HTTP-запросов с путями после запросов к REST и консоли`() {
        repeat(5) { get(httpPort, "/api/v1/status") }
        repeat(5) { get(httpPort, "/") }

        val now = scrape()

        assertTrue(now.families().none { it.startsWith("http_") || it.startsWith("grpc_") }, now.families().toString())
        assertTrue(now.samples.flatMap { it.labels.values }.none { it.startsWith("/") })
    }

    @Test
    fun `В экспозиции нет имён, путей, адресов и текстов ошибок`() {
        val scene = tenant()
        jdbc.update("update agents set hostname = 'qa-host-7f3a' where id = ?", scene.agentId)
        val source = scene.source("qa-source-7f3a")
        scene.run(source, T0, status = "failed")
        jdbc.update("update runs set message = 'qa-error-7f3a' where source_id = ?", source)
        jdbc.update("update sources set config = '{\"paths\":[\"/srv/qa-path-7f3a\"]}'::jsonb where id = ?", source)
        scene.deliveries(source, 1, T0.minusSeconds(60), "failed")
        jdbc.update("update notification_deliveries set last_error = 'qa-notify-7f3a' where tenant_id = ?", scene.id)
        refresh.tick()

        val text = scrape().text

        val secrets = listOf("qa-host-7f3a", "198.51.100.23", "qa-source-7f3a", "qa-path-7f3a", "qa-error-7f3a")
        for (secret in secrets + "qa-notify-7f3a") assertFalse(secret in text, secret)
    }

    // ---- run durations, on the event ----------------------------------------------------------------------------

    private fun finished(
        trigger: Trigger = Trigger.MANUAL,
        status: RunState = RunState.SUCCEEDED,
    ) = RunFinished(UUID(1, 1), UUID(5, 5), UUID(2, 2), UUID(3, 3), trigger, RunKind.BACKUP, status, null, T0, T0)

    @Test
    fun `Завершение запуска видно в гистограмме сразу, без тика состояния`() {
        val scene = tenant()
        val run = scene.run(scene.source(), T0, status = "failed", steps = 1)
        jdbc.update("update runs set finished_at = ? where id = ?", Timestamp.from(T0.plusSeconds(90)), run.id)
        val labels = arrayOf("kind" to "backup", "status" to "failed")
        val before = scrape()

        fun Exposition.of(
            name: String,
            vararg more: Pair<String, String>,
        ) = value("sard_run_duration_seconds_$name", *labels, *more)

        announcer.announce(scene.id, run.steps.single())

        val now = scrape()
        assertEquals(before.of("count")!! + 1, now.of("count"))
        assertEquals(before.of("sum")!! + 90, now.of("sum"))
        assertEquals(before.of("bucket", "le" to "300.0")!! + 1, now.of("bucket", "le" to "300.0"))
        assertEquals(before.of("bucket", "le" to "60.0"), now.of("bucket", "le" to "60.0"))
    }

    @Test
    fun `Запуск, отменённый до начала, не попадает в гистограмму, а запуск проверки - вид verify`() {
        val scene = tenant()
        val source = scene.source()
        val cancelled = scene.run(source, T0, status = "cancelled")
        jdbc.update("update runs set started_at = null where id = ?", cancelled.id)
        val verification =
            scene.run(scene.source(), T0, status = "succeeded", trigger = "verification", kind = "restore")
        jdbc.update("update runs set finished_at = ? where id = ?", Timestamp.from(T0.plusSeconds(30)), verification.id)
        val before = scrape()

        announcer.announce(scene.id, cancelled.steps.single())
        announcer.announce(scene.id, verification.steps.single())

        val now = scrape()

        fun count(
            kind: String,
            status: String,
        ) = now.value("sard_run_duration_seconds_count", "kind" to kind, "status" to status)

        fun was(
            kind: String,
            status: String,
        ) = before.value("sard_run_duration_seconds_count", "kind" to kind, "status" to status)
        assertEquals(was("backup", "cancelled"), count("backup", "cancelled"))
        assertEquals(was("verify", "succeeded")!! + 1, count("verify", "succeeded"))
        assertEquals(was("restore", "succeeded"), count("restore", "succeeded"))
        val sum = { e: Exposition ->
            e.value("sard_run_duration_seconds_sum", "kind" to "verify", "status" to "succeeded")!!
        }
        assertEquals(30.0, sum(now) - sum(before))
    }

    // ---- a scrape does not touch the database ----------------------------------------------------------------

    @Test
    fun `Сто scrape подряд не выполняют ни одного SQL-запроса на потоках слушателя`() {
        refresh.tick()
        val mark = statements.mark()

        repeat(100) { scrape() }

        assertEquals(emptyList(), statements.onThread(LISTENER_THREAD, mark))
    }

    @Test
    fun `Одновременные scrape отвечают все, значения метрик состояния совпадают`() {
        val scene = tenant()
        scene.run(scene.source(), T0, status = "succeeded")
        refresh.tick()
        val pool = Executors.newFixedThreadPool(50)

        val requests = List(50) { pool.submit(Callable { get(listener.boundPort, "/metrics") }) }
        val bodies = requests.map { it.get(30, TimeUnit.SECONDS) }
        pool.shutdown()

        assertEquals(List(50) { 200 }, bodies.map { it.statusCode() })
        val states =
            bodies.map { response ->
                Exposition(response.body()).samples.filter { it.name == BACKUP || it.name == "sard_agents" }
            }
        assertEquals(1, states.toSet().size)
    }

    @Test
    fun `Scrape отвечает за секунду, пока пул занят и тик ждёт соединения, со значениями прошлого тика`() {
        val scene = tenant()
        val source = scene.source()
        scene.run(source, T0.minus(Duration.ofDays(1)), status = "succeeded")
        refresh.tick()
        val before = scrape().value(BACKUP, "source_id" to "$source")
        val pool = (dataSource as HikariDataSource).maximumPoolSize
        val held = mutableListOf<Connection>()
        val ticking = Executors.newSingleThreadExecutor()
        try {
            repeat(pool) { held += dataSource.connection }
            val blocked = ticking.submit { refresh.tick() }

            val started = System.nanoTime()
            val response = get(listener.boundPort, "/metrics")
            val took = Duration.ofNanos(System.nanoTime() - started)

            assertEquals(200, response.statusCode())
            assertTrue(took < Duration.ofSeconds(1), "the scrape took $took")
            assertEquals(before, Exposition(response.body()).value(BACKUP, "source_id" to "$source"))
            assertFalse(blocked.isDone, "the tick has a connection although the pool is held")
        } finally {
            held.forEach { it.close() }
            ticking.shutdown()
            assertTrue(ticking.awaitTermination(60, TimeUnit.SECONDS))
        }
    }

    // ---- the console's port has no metrics ----------------------------------------------------------------------

    private fun noExposition(response: HttpResponse<String>) {
        val lines = response.body().lines()
        assertTrue(
            lines.none { it.startsWith("sard_") || it.startsWith("# TYPE") },
            "${response.statusCode()} ${response.body().take(200)}",
        )
        val contentType = response.headers().firstValue("Content-Type").orElse("")
        assertFalse(response.statusCode() == 200 && contentType.startsWith("text/plain"), "200 text/plain")
    }

    @Test
    fun `Путь метрик на основном порту не отдаёт экспозицию ни одним из вариантов, с токеном и без cookie`() {
        for (path in PROBES) {
            noExposition(get(httpPort, path, authorization = "Bearer $TOKEN"))
        }
    }

    @Test
    fun `Путь метрик на основном порту не отдаёт экспозицию и с сессией администратора`() {
        clock.now = startedAt
        val api = ApiClient(httpPort, mapper)
        val session = api.signIn()
        for (path in listOf("/actuator/prometheus", "/metrics", "//actuator/prometheus")) {
            val response = api.sendUnchecked("GET", path, session)
            assertTrue(response.body.lines().none { it.startsWith("sard_") || it.startsWith("# TYPE") }, path)
        }
    }

    @Test
    fun `Список actuator на основном порту содержит только health и info, оба без токена`() {
        val list = mapper.readTree(get(httpPort, "/actuator").body())

        assertEquals(setOf("self", "health", "health-path", "info"), list.path("_links").propertyNames().toSet())
        assertEquals(200, get(httpPort, "/actuator/health").statusCode())
        assertTrue(get(httpPort, "/actuator/health").body().contains("UP"))
        assertEquals(200, get(httpPort, "/actuator/info").statusCode())
    }
}
