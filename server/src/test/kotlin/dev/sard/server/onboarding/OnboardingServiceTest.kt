// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.api.AdminStepResult
import dev.sard.server.api.CodeResult
import dev.sard.server.api.NoSuchSessionException
import dev.sard.server.api.OnboardingAccess
import dev.sard.server.api.OnboardingStep
import dev.sard.server.api.OnboardingStepId
import dev.sard.server.api.OnboardingStepState
import dev.sard.server.api.SetupCodeState
import dev.sard.server.pki.CaUsage
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertIs
import kotlin.test.assertNotEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

private const val PASSWORD = "correct-horse-battery"

/** Docs/specs/server/onboarding-setup.feature at the level of the component: no HTTP, no database. */
@MutFlowTest
class OnboardingServiceTest {
    private val h = OnboardingHarness()
    private val service get() = h.service

    private fun enter(
        code: String? = CODE,
        address: String = ADDRESS,
        previous: String? = null,
    ) = MutFlow.underTest { service.enterCode(code, address, previous) }

    private fun steps(
        ca: OnboardingStepState,
        admin: OnboardingStepState,
    ) = listOf(
        OnboardingStep(OnboardingStepId.CA, ca),
        OnboardingStep(OnboardingStepId.ADMIN, admin),
        OnboardingStep(OnboardingStepId.SELF_BACKUP, OnboardingStepState.UPCOMING),
        OnboardingStep(OnboardingStepId.KEYS_CONFIRMED, OnboardingStepState.UPCOMING),
    )

    // ---- Состояние ----

    @Test
    fun `Состояние чистой установки без сессии перечисляет шаги и не раскрывает CA`() {
        val state = MutFlow.underTest { service.state(false, null) }

        assertEquals(steps(OnboardingStepState.PENDING, OnboardingStepState.PENDING), state.steps)
        assertEquals(SetupCodeState.ACTIVE, state.setupCode)
        assertEquals(OnboardingAccess.NONE, state.access)
        assertNull(state.ca)
        assertNull(state.caReplaceable)
    }

    @Test
    fun `Неверная сессия настройки даёт доступ none`() {
        assertEquals(OnboardingAccess.NONE, service.state(false, "forged-setup-id").access)
    }

    @Test
    fun `Сессия настройки видит CA, и CA ещё заменяем`() {
        val session = h.setupSession()

        val state = service.state(false, session)

        assertEquals(OnboardingAccess.SETUP, state.access)
        assertEquals(h.caInfo, state.ca)
        assertEquals(true, state.caReplaceable)
    }

    @Test
    fun `Сессия администратора видит выполненные шаги и не видит код`() {
        h.steps.ca = true
        h.usage = CaUsage.STEP_CA_COMPLETE
        h.adminSetup.password = PASSWORD

        val state = service.state(true, null)

        assertEquals(steps(OnboardingStepState.DONE, OnboardingStepState.DONE), state.steps)
        assertEquals(SetupCodeState.NOT_ISSUED, state.setupCode)
        assertEquals(OnboardingAccess.ADMIN, state.access)
        assertEquals(false, state.caReplaceable)
    }

    @Test
    fun `Сессия администратора сильнее сессии настройки`() {
        assertEquals(OnboardingAccess.ADMIN, service.state(true, h.setupSession()).access)
    }

    @Test
    fun `CA заменяем ровно пока ни шаг ca, ни сертификаты агентов не заняли его`() {
        val session = h.setupSession()
        for (
        (usage, replaceable) in
        listOf(
            CaUsage.NONE to true,
            CaUsage.AGENT_CERTIFICATES to false,
            CaUsage.STEP_CA_COMPLETE to false,
        )
        ) {
            h.usage = usage
            assertEquals(replaceable, service.state(false, session).caReplaceable, "$usage")
        }
    }

    @Test
    fun `Код истёк — состояние expired, и срок сессий настройки тоже`() {
        val session = h.setupSession()

        h.clock.now = T0 + Duration.ofHours(24)

        val state = service.state(false, session)
        assertEquals(SetupCodeState.EXPIRED, state.setupCode)
        assertEquals(OnboardingAccess.NONE, state.access)
    }

    @Test
    fun `Сессия настройки действует за миллисекунду до срока кода`() {
        val session = h.setupSession()

        h.clock.now = T0 + Duration.ofHours(24) - Duration.ofMillis(1)

        assertEquals(OnboardingAccess.SETUP, service.state(false, session).access)
    }

    @Test
    fun `Без выданного кода и без администратора состояние not_issued`() {
        val bare = OnboardingHarness(issueCode = false)

        assertEquals(SetupCodeState.NOT_ISSUED, bare.service.state(false, null).setupCode)
    }

    @Test
    fun `Шаг admin выполнен при выданном коде — состояние not_issued`() {
        h.adminSetup.password = PASSWORD

        assertEquals(SetupCodeState.NOT_ISSUED, service.state(false, null).setupCode)
    }

    // ---- Ввод кода ----

    @Test
    fun `Верный код выдаёт новую сессию настройки`() {
        val result = enter()

        assertIs<CodeResult.Accepted>(result)
        assertEquals(OnboardingAccess.SETUP, service.state(false, result.setupSessionId).access)
    }

    @Test
    fun `Код принимается в любом из допустимых написаний`() {
        for (
        spelling in
        listOf(
            "abcd-efgh-jkmn-pqrs-tvwx-yz01-2345",
            "ABCDEFGHJKMNPQRSTVWXYZ012345",
            "ABCD EFGH JKMN PQRS TVWX YZ01 2345",
            "  ABCD-EFGH-JKMN-PQRS-TVWX-YZ01-2345\n",
            "ABCD-EFGH-JKMN-PQRS-TVWX-YZO1-2345",
            "ABCD-EFGH-JKMN-PQRS-TVWX-YZ0I-2345",
            "ABCD-EFGH-JKMN-PQRS-TVWX-YZ0l-2345",
        )
        ) {
            assertIs<CodeResult.Accepted>(enter(spelling), spelling)
        }
    }

    @Test
    fun `Неверные коды отклоняются одним ответом`() {
        for (
        code in
        listOf(
            WRONG_CODE,
            "",
            "ABCD",
            null,
            "ABCD-EFGH-JKMN-PQRS-TVWX-YZ01-2346",
            "ABCD-EFGH-JKMN-PQRS-TVWX-YZ01",
            "ABCD-EFGH-JKMN-PQRS-TVWX-YZ01-2345-0000",
            "UBCD-EFGH-JKMN-PQRS-TVWX-YZ01-2345",
            "A".repeat(10_000),
        )
        ) {
            h.attempts.recordSuccess(ADDRESS)
            assertEquals(CodeResult.Rejected, enter(code), "$code")
        }
    }

    @Test
    fun `Повторный ввод кода выдаёт ещё одну сессию, прежние действуют`() {
        val first = (enter() as CodeResult.Accepted).setupSessionId
        val second = (enter(address = "198.51.100.7") as CodeResult.Accepted).setupSessionId

        assertNotEquals(first, second)
        assertEquals(OnboardingAccess.SETUP, service.state(false, first).access)
        assertEquals(OnboardingAccess.SETUP, service.state(false, second).access)
    }

    @Test
    fun `Повторный ввод кода с действующей сессией настройки завершает её`() {
        val first = (enter() as CodeResult.Accepted).setupSessionId

        val second = (enter(previous = first) as CodeResult.Accepted).setupSessionId

        assertEquals(OnboardingAccess.NONE, service.state(false, first).access)
        assertEquals(OnboardingAccess.SETUP, service.state(false, second).access)
    }

    @Test
    fun `Присланная заранее сессия настройки не принимается`() {
        val result = enter(previous = "attacker-chosen") as CodeResult.Accepted

        assertNotEquals("attacker-chosen", result.setupSessionId)
        assertEquals(OnboardingAccess.NONE, service.state(false, "attacker-chosen").access)
    }

    @Test
    fun `Код за миллисекунду до срока принимается, в срок нет`() {
        h.clock.now = T0 + Duration.ofHours(24) - Duration.ofMillis(1)
        assertIs<CodeResult.Accepted>(enter())

        h.clock.now = T0 + Duration.ofHours(24)
        assertEquals(CodeResult.Rejected, enter())
    }

    @Test
    fun `Пока шаг admin не выполнен, код нужен, после него на любой код отвечает Completed`() {
        h.adminSetup.password = PASSWORD

        assertEquals(CodeResult.Completed, enter())
        assertEquals(CodeResult.Completed, enter(WRONG_CODE))
    }

    @Test
    fun `Ответ после шага admin не зависит от кода и не засчитывается в перебор`() {
        h.adminSetup.password = PASSWORD
        repeat(10) { enter(WRONG_CODE) }
        h.adminSetup.password = null

        assertEquals(CodeResult.Rejected, enter(WRONG_CODE))
    }

    @Test
    fun `Код не выдан — любой ввод отклоняется`() {
        val bare = OnboardingHarness(issueCode = false)

        assertEquals(CodeResult.Rejected, bare.service.enterCode(CODE, ADDRESS, null))
    }

    // ---- Перебор ----

    @Test
    fun `Пятая неудача ещё отвечает Rejected, шестой ввод — Locked на 900 секунд`() {
        repeat(5) { assertEquals(CodeResult.Rejected, enter(WRONG_CODE)) }

        assertEquals(CodeResult.Locked(900), enter(WRONG_CODE))
    }

    @Test
    fun `Верный код во время блокировки отвечает Locked`() {
        repeat(5) { enter(WRONG_CODE) }

        assertEquals(CodeResult.Locked(900), enter())
    }

    @Test
    fun `Retry-After показывает остаток, через 15 минут код принимается`() {
        repeat(5) { enter(WRONG_CODE) }

        h.clock.now = T0 + Duration.ofMinutes(10)
        assertEquals(CodeResult.Locked(300), enter())

        h.clock.now = T0 + Duration.ofMinutes(15)
        assertIs<CodeResult.Accepted>(enter())
    }

    @Test
    fun `Успешный ввод обнуляет счётчик адреса`() {
        repeat(4) { enter(WRONG_CODE) }
        enter()
        repeat(4) { enter(WRONG_CODE) }

        assertEquals(CodeResult.Rejected, enter(WRONG_CODE))
    }

    @Test
    fun `Блокировка одного адреса не мешает другому`() {
        repeat(5) { enter(WRONG_CODE) }

        assertIs<CodeResult.Accepted>(enter(address = "198.51.100.7"))
    }

    // ---- Шаг ca ----

    @Test
    fun `Подтверждение CA с сессией настройки выполняет шаг ca и ничего больше`() {
        val session = h.setupSession()

        MutFlow.underTest { service.confirmCa(session, false, ADDRESS) }

        assertTrue(h.steps.ca)
        assertFalse(h.adminSetup.done())
        assertEquals(OnboardingAccess.SETUP, service.state(false, session).access)
    }

    @Test
    fun `Повторное подтверждение CA ничего не меняет`() {
        val session = h.setupSession()
        service.confirmCa(session, false, ADDRESS)

        service.confirmCa(session, false, ADDRESS)

        assertTrue(h.steps.ca)
    }

    @Test
    fun `Подтверждение CA без сессии настройки — 401, шаг не выполнен`() {
        for (id in listOf(null, "forged-setup-id")) {
            assertFailsWith<NoSuchSessionException> { MutFlow.underTest { service.confirmCa(id, false, ADDRESS) } }
        }
        assertFalse(h.steps.ca)
    }

    @Test
    fun `Сессия администратора шаг ca не выполняет, пока вход в ядре`() {
        assertFailsWith<NoSuchSessionException> { service.confirmCa(null, true, ADDRESS) }
        assertFalse(h.steps.ca)
    }

    @Test
    fun `С входом через расширение шаг ca выполняет сессия администратора`() {
        val external = OnboardingHarness(adminSetup = FakeAdminSetup(external = true), issueCode = false)

        external.service.confirmCa(null, true, ADDRESS)

        assertTrue(external.steps.ca)
    }

    @Test
    fun `С входом через расширение без сессии шаг ca не выполняется`() {
        val external = OnboardingHarness(adminSetup = FakeAdminSetup(external = true), issueCode = false)

        assertFailsWith<NoSuchSessionException> { external.service.confirmCa(null, false, ADDRESS) }
    }

    // ---- Шаг admin ----

    private fun readySession(): String {
        val session = h.setupSession()
        service.confirmCa(session, false, ADDRESS)
        return session
    }

    @Test
    fun `Шаг admin задаёт пароль, выдаёт сессию администратора и закрывает код и сессии настройки`() {
        val s1 = readySession()
        val s2 = h.setupSession()

        val result = MutFlow.underTest { service.completeAdmin(s1, PASSWORD, ADDRESS) }

        assertIs<AdminStepResult.Done>(result)
        assertEquals(PASSWORD, h.adminSetup.password)
        assertEquals(h.tenant, h.adminSessions.find(result.sessionId)?.tenantId)
        assertEquals(OnboardingAccess.NONE, service.state(false, s1).access)
        assertEquals(OnboardingAccess.NONE, service.state(false, s2).access)
        assertEquals(SetupCodeState.NOT_ISSUED, service.state(false, null).setupCode)
        assertEquals(CodeResult.Completed, enter())
    }

    @Test
    fun `Шаг admin с устаревшей сессией настройки после шага admin — 401`() {
        val s1 = readySession()
        val s2 = h.setupSession()
        service.completeAdmin(s1, PASSWORD, ADDRESS)

        assertFailsWith<NoSuchSessionException> { service.completeAdmin(s2, "attacker-password-1", ADDRESS) }
        assertEquals(PASSWORD, h.adminSetup.password)
    }

    @Test
    fun `Шаг admin без сессии настройки — 401, пароль не задан`() {
        readySession()

        for (id in listOf(null, "forged-setup-id")) {
            assertFailsWith<NoSuchSessionException> {
                MutFlow.underTest { service.completeAdmin(id, PASSWORD, ADDRESS) }
            }
        }
        assertNull(h.adminSetup.password)
    }

    @Test
    fun `Шаг admin до шага ca — CaPending, пароль не задан, сессия действует`() {
        val session = h.setupSession()

        assertEquals(AdminStepResult.CaPending, MutFlow.underTest { service.completeAdmin(session, PASSWORD, ADDRESS) })

        assertNull(h.adminSetup.password)
        assertEquals(OnboardingAccess.SETUP, service.state(false, session).access)
    }

    @Test
    fun `Недопустимый пароль — InvalidPassword, сессия настройки действует`() {
        val session = readySession()

        for (bad in listOf(null, "", "short-pw-11", "a".repeat(1025))) {
            assertEquals(AdminStepResult.InvalidPassword, service.completeAdmin(session, bad, ADDRESS), "$bad")
        }

        assertNull(h.adminSetup.password)
        assertIs<AdminStepResult.Done>(service.completeAdmin(session, "exactly-12ch", ADDRESS))
    }

    @Test
    fun `Пароль не обрезается`() {
        val session = readySession()

        service.completeAdmin(session, "correct-horse-battery ", ADDRESS)

        assertEquals("correct-horse-battery ", h.adminSetup.password)
    }

    @Test
    fun `Проигравший гонку шаг admin отвечает Completed`() {
        val racing = OnboardingHarness(adminSetup = RacingAdminSetup(), steps = FakeOnboardingSteps(ca = true))
        val raced = racing.setupSession()

        assertEquals(AdminStepResult.Completed, racing.service.completeAdmin(raced, PASSWORD, ADDRESS))
    }

    @Test
    fun `С входом через расширение шаг admin отвечает Completed на любой запрос`() {
        val external = OnboardingHarness(adminSetup = FakeAdminSetup(external = true), issueCode = false)

        assertEquals(AdminStepResult.Completed, external.service.completeAdmin(null, PASSWORD, ADDRESS))
        assertEquals(AdminStepResult.Completed, external.service.completeAdmin("forged", PASSWORD, ADDRESS))
        assertEquals(CodeResult.Completed, external.service.enterCode(CODE, ADDRESS, null))
        assertEquals(SetupCodeState.NOT_ISSUED, external.service.state(false, null).setupCode)
    }

    /** The other session wins between the check and the creation. */
    private class RacingAdminSetup : FakeAdminSetup() {
        override fun done() = false

        override fun create(password: String) = false
    }
}
