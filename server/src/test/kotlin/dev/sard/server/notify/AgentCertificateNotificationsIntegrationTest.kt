// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.api.captureLogs
import dev.sard.server.fleet.AgentCertificateCheck
import dev.sard.server.notify.telegram.FakeBotApi
import dev.sard.server.notify.telegram.TEST_TOKEN
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.Runs
import dev.sard.server.runs.RunsTenant
import dev.sard.server.runs.RunsTestConfiguration
import dev.sard.server.runs.Sources
import dev.sard.server.runs.StepOutcome
import dev.sard.server.runs.StepState
import dev.sard.server.runs.StepTransitions
import org.hibernate.Session
import org.hibernate.exception.JDBCConnectionException
import org.junit.jupiter.api.AfterAll
import org.mockito.ArgumentMatchers
import org.mockito.Mockito
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.test.context.DynamicPropertyRegistry
import org.springframework.test.context.DynamicPropertySource
import org.springframework.test.context.bean.override.mockito.MockitoSpyBean
import java.sql.SQLException
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
import java.util.UUID
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private const val CHAT = "-100778"
private const val CONSOLE = "https://sard.example.com"
private const val HOSTNAME = "db1.example.com"
private val T0: Instant = Instant.parse("2026-10-15T12:00:00Z")
private val DAY: Duration = Duration.ofDays(1)
private val HOUR: Duration = Duration.ofHours(1)
private const val SOON = "⚠️ Сертификат агента скоро истечёт: $HOSTNAME"
private const val EXPIRED = "⛔ Сертификат агента истёк: $HOSTNAME"

private fun days(count: Long): Duration = DAY.multipliedBy(count)

/**
 * C1 phase 3, rules "Уведомление приходит по одному на каждый порог 30, 7 и 1 из 365", "Продление и отзыв
 * прекращают уведомления", "Истечение сертификата уведомляет один раз", "Уведомление доставляется так же надёжно,
 * как прочие уведомления S9a" (docs/specs/server/agent-certificate-expiry.feature). The clock is the server's Clock
 * bean replaced by the test's [MovableClock] (П42); the check is called by the test.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = [
        "spring.grpc.server.port=0",
        "sard.notify.tick-interval=1h",
        "sard.freshness.interval=1h",
        "sard.pki.agent-certificate-check-interval=PT1H",
        "SARD_TELEGRAM_BOT_TOKEN=$TEST_TOKEN",
        "SARD_TELEGRAM_CHAT_ID=$CHAT",
        "SARD_NOTIFY_LANGUAGE=ru",
        "SARD_CONSOLE_PUBLIC_URL=$CONSOLE",
    ],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class AgentCertificateNotificationsIntegrationTest(
    @Autowired private val check: AgentCertificateCheck,
    @Autowired private val service: NotificationService,
    @Autowired private val loop: NotificationLoop,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
    @Autowired private val steps: StepTransitions,
) {
    @MockitoSpyBean
    lateinit var sessions: TenantSessions

    private val tenant = UUID.randomUUID()
    private val agent = UUID.randomUUID()
    private lateinit var serial: String

    companion object {
        private val fake = FakeBotApi()

        @JvmStatic
        @DynamicPropertySource
        fun botApi(registry: DynamicPropertyRegistry) {
            registry.add("sard.notify.telegram.api-url") { fake.uri.toString() }
        }

        @JvmStatic
        @AfterAll
        fun closeFake() {
            fake.close()
        }
    }

    @BeforeTest
    fun `a tenant with the agent X`() {
        loop.stop()
        Mockito.reset(sessions)
        clock.now = T0
        fake.requests.clear()
        fake.fallback = FakeBotApi.ok()
        jdbc.update("insert into tenants (id, name) values (?, ?)", tenant, "cert-$tenant")
        insertAgent(agent)
        serial = certificate(agent)
    }

    @AfterTest
    fun `drop the tenant`() {
        for (table in listOf("notification_deliveries", "agent_certificate_events", "agent_certificates", "agents")) {
            jdbc.update("delete from $table where tenant_id = ?", tenant)
        }
        jdbc.update("delete from tenants where id = ?", tenant)
    }

    private fun insertAgent(
        id: UUID,
        hostname: String = HOSTNAME,
        builtin: Boolean = false,
        lastSeenAt: Instant? = null,
    ) = jdbc.update(
        "insert into agents (id, tenant_id, hostname, registered_at, role, builtin, last_seen_at) " +
            "values (?, ?, ?, ?, 'source', ?, ?)",
        id,
        tenant,
        hostname,
        Timestamp.from(T0),
        builtin,
        lastSeenAt?.let(Timestamp::from),
    )

    /** A record of a certificate of [owner] issued at [issuedAt], valid for [validity], as Enroll writes it. */
    private fun certificate(
        owner: UUID,
        tenant: UUID = this.tenant,
        issuedAt: Instant = T0,
        validity: Duration = days(365),
        predecessor: String? = null,
        serial: String = UUID.randomUUID().toString().replace("-", ""),
    ): String {
        jdbc.update(
            "insert into agent_certificates (serial, tenant_id, agent_id, issued_at, not_after, predecessor_serial) " +
                "values (?, ?, ?, ?, ?, ?)",
            serial,
            tenant,
            owner,
            Timestamp.from(issuedAt),
            Timestamp.from(issuedAt + validity),
            predecessor,
        )
        return serial
    }

    /** X renewed [from] and confirmed the new certificate at the clock's time: [from] is superseded (C1, П9). */
    private fun renewedAndConfirmed(from: String): String {
        val renewed = certificate(agent, issuedAt = clock.now, predecessor = from)
        jdbc.update("update agent_certificates set superseded_at = ? where serial = ?", Timestamp.from(clock.now), from)
        return renewed
    }

    /** The certificate of X lives [validity] from T0 instead of a year. */
    private fun shortLived(validity: Duration) {
        val sql = "update agent_certificates set not_after = ? where serial = ?"
        jdbc.update(sql, Timestamp.from(T0 + validity), serial)
    }

    private fun at(moment: Instant) {
        clock.now = moment
    }

    private fun deliver(ticks: Int = 3) = repeat(ticks) { service.tick() }

    private fun checkAndDeliver(ticks: Int = 3) {
        check.tick()
        deliver(ticks)
    }

    /** One check and delivery every [step] from [from] to [until] inclusive, with the clock showing each moment. */
    private fun sweep(
        from: Instant,
        until: Instant,
        step: Duration = HOUR,
        afterEach: (Instant) -> Unit = {},
    ) {
        var moment = from
        while (!moment.isAfter(until)) {
            at(moment)
            checkAndDeliver(ticks = 1)
            afterEach(moment)
            moment += step
        }
    }

    private fun events(): List<String> =
        jdbc
            .queryForList(
                "select event from agent_certificate_events where tenant_id = ? order by recorded_at, event",
                String::class.java,
                tenant,
            ).filterNotNull()

    private fun eventsOf(serial: String): Set<String> =
        jdbc
            .queryForList(
                "select event from agent_certificate_events where tenant_id = ? and serial = ?",
                String::class.java,
                tenant,
                serial,
            ).filterNotNull()
            .toSet()

    private fun deliveryStatuses(): List<String> =
        jdbc
            .queryForList(
                "select status from notification_deliveries where tenant_id = ? order by created_at, id",
                String::class.java,
                tenant,
            ).filterNotNull()

    private fun messages(): List<String> = List(fake.requests.size) { fake.shownText(it) }

    private fun headlines(): List<String> = messages().map { it.lines().first() }

    @Test
    fun `Уведомление приходит, когда остаток доходит до порога`() {
        at(T0 + days(335) - Duration.ofSeconds(1))
        checkAndDeliver()
        assertEquals(0, fake.requests.size)

        at(T0 + days(335))
        checkAndDeliver()
        assertEquals(listOf(SOON), headlines())
    }

    @Test
    fun `Три порога и истечение дают ровно четыре уведомления за жизнь сертификата`() {
        sweep(T0 + days(330), T0 + days(366))

        assertEquals(listOf(SOON, SOON, SOON, EXPIRED), headlines())
    }

    @Test
    fun `Пороги уведомлений приходят в сутки 335, 358 и 364, истечение в сутки 365`() {
        val arrivals = mutableListOf<Pair<Instant, String>>()
        var seen = 0
        sweep(T0 + days(330), T0 + days(366), afterEach = { moment ->
            (seen until fake.requests.size).forEach { arrivals += moment to fake.shownText(it).lines().first() }
            seen = fake.requests.size
        })
        val expected =
            listOf(
                T0 + days(335) to SOON,
                T0 + days(358) to SOON,
                T0 + days(364) to SOON,
                T0 + days(365) to EXPIRED,
            )
        assertEquals(expected, arrivals)
    }

    @Test
    fun `Пороги уведомлений масштабируются по сроку сертификата`() {
        shortLived(Duration.ofMinutes(10))
        val expected =
            mapOf(
                549L to emptySet(),
                550L to emptySet(),
                551L to setOf("threshold-30"),
                588L to setOf("threshold-30"),
                589L to setOf("threshold-30", "threshold-7"),
                598L to setOf("threshold-30", "threshold-7"),
                599L to setOf("threshold-30", "threshold-7", "threshold-1"),
            )
        for (second in 540L..599L) {
            at(T0 + Duration.ofSeconds(second))
            check.tick()
            expected[second]?.let { assertEquals(it, eventsOf(serial), "after the check at $second s") }
        }
    }

    @Test
    fun `Повторные проверки и heartbeat не повторяют уведомление`() {
        at(T0 + days(335))
        repeat(50) { minute ->
            at(T0 + days(335) + Duration.ofMinutes(minute.toLong()))
            jdbc.update("update agents set last_seen_at = ? where id = ?", Timestamp.from(clock.now), agent)
            checkAndDeliver()
        }
        assertEquals(1, fake.requests.size)
    }

    @Test
    fun `После простоя сервера уведомляется только наименьший достигнутый порог`() {
        at(T0 + days(360))

        checkAndDeliver()

        assertEquals(1, fake.requests.size)
        assertEquals(setOf("threshold-7"), eventsOf(serial))
    }

    @Test
    fun `Пройденный при простое порог не уведомляется и позже`() {
        at(T0 + days(360))
        checkAndDeliver()

        sweep(T0 + days(360) + HOUR, T0 + days(364))

        assertEquals(2, fake.requests.size)
        assertEquals(setOf("threshold-7", "threshold-1"), eventsOf(serial))
    }

    @Test
    fun `Сертификат, оказавшийся за порогом при обновлении сервера, даёт одно уведомление`() {
        at(T0 + days(345))

        checkAndDeliver()

        assertEquals(listOf(SOON), headlines())
    }

    @Test
    fun `Подтверждённое продление прекращает уведомления о старом сертификате`() {
        at(T0 + days(335))
        checkAndDeliver()
        assertEquals(1, fake.requests.size)
        at(T0 + days(340))
        renewedAndConfirmed(serial)

        sweep(T0 + days(340), T0 + days(366))

        assertEquals(1, fake.requests.size)
    }

    @Test
    fun `Новый сертификат получает свои уведомления на своих порогах`() {
        at(T0 + days(335))
        renewedAndConfirmed(serial)
        at(T0 + days(670))

        checkAndDeliver()

        assertEquals(1, fake.requests.size)
        assertEquals("Истекает: 2028-09-14 12:00 UTC", fake.shownText(0).lines()[1])
    }

    @Test
    fun `Выданное, но не подтверждённое продление уведомления не прекращает`() {
        at(T0 + days(300))
        certificate(agent, issuedAt = clock.now, predecessor = serial)
        at(T0 + days(335))

        checkAndDeliver()

        assertEquals(1, fake.requests.size)
        assertEquals(
            "Новый сертификат выдан 2027-08-11 12:00 UTC, но агент его не подтвердил",
            fake.shownText(0).lines()[4],
        )
    }

    @Test
    fun `Уведомление, не отправленное до подтверждения продления, не отправляется`() {
        fake.fallback = FakeBotApi.error(503, "Service Unavailable")
        at(T0 + days(335))
        checkAndDeliver()
        renewedAndConfirmed(serial)
        fake.fallback = FakeBotApi.ok()
        fake.requests.clear()

        at(T0 + days(335) + HOUR)
        deliver()

        assertEquals(0, fake.requests.size)
        assertEquals(listOf("skipped"), deliveryStatuses())
    }

    @Test
    fun `Уведомление, не отправленное до истечения сертификата, не отправляется`() {
        shortLived(Duration.ofMinutes(10))
        fake.fallback = FakeBotApi.error(503, "Service Unavailable")
        at(T0 + Duration.ofSeconds(599))
        checkAndDeliver()
        assertEquals(setOf("threshold-1"), eventsOf(serial))
        fake.fallback = FakeBotApi.ok()
        fake.requests.clear()

        at(T0 + Duration.ofMinutes(11))
        deliver()

        assertEquals(0, fake.requests.size)
        assertEquals(listOf("skipped"), deliveryStatuses())
    }

    @Test
    fun `Отозванный агент не получает уведомлений и событий`() {
        at(T0 + days(10))
        jdbc.update("update agents set revoked_at = ? where id = ?", Timestamp.from(clock.now), agent)
        jdbc.update("update agent_certificates set revoked_at = ? where agent_id = ?", Timestamp.from(clock.now), agent)

        for (day in listOf(335L, 358, 364, 366)) {
            at(T0 + days(day))
            checkAndDeliver()
        }

        assertEquals(0, fake.requests.size)
        assertEquals(emptyList(), events())
    }

    @Test
    fun `Отозванный сертификат неотозванного агента не получает уведомлений и событий`() {
        jdbc.update("update agent_certificates set revoked_at = ? where agent_id = ?", Timestamp.from(T0), agent)

        for (day in listOf(335L, 358, 364, 366)) {
            at(T0 + days(day))
            checkAndDeliver()
        }

        assertEquals(0, fake.requests.size)
        assertEquals(emptyList(), events())
    }

    @Test
    fun `Отзыв агента после записи события отменяет неотправленное уведомление`() {
        fake.fallback = FakeBotApi.error(503, "Service Unavailable")
        at(T0 + days(335))
        checkAndDeliver()
        jdbc.update("update agents set revoked_at = ? where id = ?", Timestamp.from(clock.now), agent)
        jdbc.update("update agent_certificates set revoked_at = ? where agent_id = ?", Timestamp.from(clock.now), agent)
        fake.fallback = FakeBotApi.ok()
        fake.requests.clear()

        at(T0 + days(335) + HOUR)
        deliver()

        assertEquals(0, fake.requests.size)
        assertEquals(listOf("skipped"), deliveryStatuses())
    }

    @Test
    fun `Истечение сертификата даёт уведомление`() {
        at(T0 + days(365))

        checkAndDeliver()

        assertEquals(listOf(EXPIRED), headlines())
    }

    @Test
    fun `Уведомление об истечении приходит один раз на сертификат`() {
        at(T0 + days(365))
        checkAndDeliver()

        sweep(T0 + days(365) + HOUR, T0 + days(400))

        assertEquals(1, fake.requests.size)
    }

    @Test
    fun `Граница истечения проверяется точно`() {
        shortLived(Duration.ofMinutes(10))
        at(T0 + Duration.ofMinutes(10) - Duration.ofSeconds(1))
        check.tick()
        assertFalse("expired" in eventsOf(serial))

        at(T0 + Duration.ofMinutes(10))
        check.tick()

        assertTrue("expired" in eventsOf(serial))
    }

    @Test
    fun `Сертификат, продлённый до конца срока, уведомления об истечении не даёт`() {
        at(T0 + days(360))
        renewedAndConfirmed(serial)

        at(T0 + days(365))
        checkAndDeliver()
        at(T0 + days(366))
        checkAndDeliver()

        assertEquals(0, fake.requests.size)
    }

    @Test
    fun `Сертификат, найденный уже истёкшим, даёт только уведомление об истечении`() {
        at(T0 + days(370))

        checkAndDeliver()

        assertEquals(listOf(EXPIRED), headlines())
        assertEquals(setOf("expired"), eventsOf(serial))
    }

    @Test
    fun `Уведомление об истечении, не отправленное до отзыва агента, не отправляется`() {
        fake.fallback = FakeBotApi.error(503, "Service Unavailable")
        at(T0 + days(365))
        checkAndDeliver()
        jdbc.update("update agents set revoked_at = ? where id = ?", Timestamp.from(clock.now), agent)
        fake.fallback = FakeBotApi.ok()
        fake.requests.clear()

        at(T0 + days(365) + HOUR)
        deliver()

        assertEquals(0, fake.requests.size)
        assertEquals(listOf("skipped"), deliveryStatuses())
    }

    @Test
    fun `Брошенный после перерегистрации агент получает одно уведомление об истечении`() {
        val reenrolled = UUID.randomUUID()
        insertAgent(reenrolled)
        certificate(reenrolled, issuedAt = T0 + days(300))

        at(T0 + days(365))
        checkAndDeliver()

        assertEquals(listOf(EXPIRED), headlines())
    }

    @Test
    fun `Запись события пишется в журнал INFO`() {
        at(T0 + days(335))

        val logs = captureLogs { check.tick() }

        val line = "Agent certificate $serial of agent $agent reached 30/365 of its lifetime left"
        assertEquals(1, logs.count { line in it }, "$logs")
    }

    @Test
    fun `Запись события истечения пишется в журнал INFO`() {
        at(T0 + days(365))

        val logs = captureLogs { check.tick() }

        assertEquals(1, logs.count { "Agent certificate $serial of agent $agent expired" in it }, "$logs")
    }

    @Test
    fun `Неотправленное за срок жизни доставки уведомление пишется в журнал как expired`() {
        fake.fallback = FakeBotApi.error(503, "Service Unavailable")
        at(T0 + days(335))
        checkAndDeliver()
        at(T0 + days(336))

        val logs = captureLogs { deliver() }

        val line = "Notification of agent certificate $serial (30/365) through telegram expired"
        assertEquals(1, logs.count { line in it }, "$logs")
    }

    @Test
    fun `Неотправленное уведомление об истечении пишется в журнал как expired`() {
        fake.fallback = FakeBotApi.error(503, "Service Unavailable")
        at(T0 + days(365))
        checkAndDeliver()
        at(T0 + days(366))

        val logs = captureLogs { deliver() }

        val line = "Notification of agent certificate $serial (expired) through telegram expired"
        assertEquals(1, logs.count { line in it }, "$logs")
    }

    @Test
    fun `Отказ Telegram по уведомлению пишется в журнал с причиной`() {
        fake.fallback = FakeBotApi.error(400, "Bad Request: chat not found")
        at(T0 + days(360))

        val logs = captureLogs { checkAndDeliver() }

        val failed = logs.filter { "Notification of agent certificate $serial (7/365) through telegram failed" in it }
        assertEquals(1, failed.size, "$logs")
        assertTrue("chat not found" in failed.single(), failed.single())
    }

    @Test
    fun `Недоступный Telegram откладывает уведомление до восстановления`() {
        fake.fallback = FakeBotApi.error(503, "Service Unavailable")
        at(T0 + days(335))
        checkAndDeliver()
        fake.fallback = FakeBotApi.ok()
        fake.requests.clear()

        at(T0 + days(335) + HOUR)
        deliver()

        assertEquals(listOf(SOON), headlines())
        assertEquals(setOf("threshold-30"), eventsOf(serial))
    }

    @Test
    fun `Событие, записанное перед остановкой сервера, доставляется после старта`() {
        at(T0 + days(335))
        check.tick()
        assertEquals(0, fake.requests.size)

        deliver()

        assertEquals(listOf(SOON), headlines())
    }

    @Test
    fun `Сбой базы во время проверки срока не теряет и не удваивает уведомление`() {
        at(T0 + days(335))
        val failure = JDBCConnectionException("disk on fire", SQLException("disk on fire"))
        Mockito.doThrow(failure).`when`(sessions).system(anything<(Session) -> Any?> { null })
        val thrown = runCatching { check.tick() }.exceptionOrNull()
        Mockito.reset(sessions)

        checkAndDeliver()

        assertTrue(thrown != null)
        assertEquals(1, fake.requests.size)
    }

    @Test
    fun `Две проверки срока одновременно записывают одно событие`() {
        at(T0 + days(335))
        val start = CountDownLatch(1)
        val pool = Executors.newFixedThreadPool(2)
        val runs =
            List(2) {
                pool.submit {
                    start.await()
                    check.tick()
                }
            }
        start.countDown()
        runs.forEach { it.get(30, TimeUnit.SECONDS) }
        pool.shutdown()

        deliver()

        assertEquals(setOf("threshold-30"), eventsOf(serial))
        assertEquals(1, jdbc.queryForObject("select count(*) from agent_certificate_events", Int::class.java))
        assertEquals(1, fake.requests.size)
    }

    @Test
    fun `Уведомление не содержит материала сертификата`() {
        at(T0 + days(335))
        checkAndDeliver()

        val text = fake.shownText(0)

        assertFalse("BEGIN" in text)
        assertFalse(serial in text)
    }

    @Test
    fun `Агент без единого heartbeat назван агентом, который ни разу не подключался`() {
        at(T0 + days(335))
        checkAndDeliver()

        assertEquals("Агент ни разу не подключался", fake.shownText(0).lines()[4])
    }

    @Test
    fun `Агент с heartbeat, но без потока назван агентом, не бывшим на связи`() {
        jdbc.update("update agents set last_seen_at = ? where id = ?", Timestamp.from(T0 + days(10)), agent)
        at(T0 + days(335))
        checkAndDeliver()

        assertEquals("Агент не на связи с 2026-10-25 12:00 UTC", fake.shownText(0).lines()[4])
    }

    @Test
    fun `Встроенный агент получает совет про журнал контейнера`() {
        val builtin = UUID.randomUUID()
        jdbc.update("update agents set revoked_at = ? where id = ?", Timestamp.from(T0), agent)
        jdbc.update("update agent_certificates set revoked_at = ? where agent_id = ?", Timestamp.from(T0), agent)
        insertAgent(builtin, hostname = "sard-self", builtin = true)
        certificate(builtin)
        at(T0 + days(335))

        checkAndDeliver()

        assertEquals("Встроенный агент: docker compose logs self-agent", fake.shownText(0).lines()[5])
    }

    @Test
    fun `Уведомление о сертификате и сообщение о запуске — разные сообщения`() {
        // Only the agent of the other tenant is in play: the agent X of this class's tenant is revoked.
        jdbc.update("update agents set revoked_at = ? where id = ?", Timestamp.from(T0), agent)
        val other = RunsTenant(jdbc).create()
        try {
            jdbc.update("update agents set hostname = ? where id = ?", HOSTNAME, other.agentId)
            certificate(other.agentId, tenant = other.id)
            at(T0 + days(335))
            val run = runs.start(other.id, sources.create(other.id, other.draft("db-main")).id)
            val step = run.steps.single().id
            assertTrue(steps.claim(other.id, step))
            assertTrue(steps.finished(other.id, step, StepOutcome(StepState.FAILED, "disk full")))

            checkAndDeliver()

            assertEquals(setOf(SOON, "❌ Бэкап завершился ошибкой: db-main"), headlines().toSet())
            assertEquals(2, fake.requests.size)
        } finally {
            for (table in listOf("notification_deliveries", "agent_certificate_events", "agent_certificates")) {
                jdbc.update("delete from $table where tenant_id = ?", other.id)
            }
            other.drop()
        }
    }

    /** A Mockito matcher that Kotlin accepts for a parameter that is not null: [placeholder] is never used. */
    private fun <T : Any> anything(placeholder: T): T {
        ArgumentMatchers.any<Any>()
        return placeholder
    }
}
