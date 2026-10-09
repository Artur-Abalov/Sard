// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import ch.qos.logback.classic.Level
import dev.sard.server.api.PasswordChangeResult
import dev.sard.server.api.SignInResult
import dev.sard.server.extension.TenantResolver
import dev.sard.server.pki.MovableClock
import dev.sard.server.selfagent.captureEvents
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.dao.DataAccessResourceFailureException
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertIs
import kotlin.test.assertNotEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val T0: Instant = Instant.parse("2026-10-09T12:00:00Z")
private const val PASSWORD = "correct-horse-battery"
private const val ADDRESS = "203.0.113.10"
private val TENANT = TenantResolver.DEFAULT_TENANT_ID

/** Sign-in and password change against a stored Argon2id hash (F4a); the hasher is the real one. */
@MutFlowTest
class SessionApiImplTest {
    private val hasher = PasswordHasher()
    private val clock = MovableClock(T0)
    private val administrators = FakeAdministrators()
    private val store = SessionStore(clock)
    private val tracker = LoginAttemptTracker(clock)
    private val api = SessionApiImpl(administrators, hasher, store, tracker, { TENANT }, clock)

    private fun withAdministrator(password: String = PASSWORD) {
        administrators.stored = hasher.hash(password)
    }

    private fun signIn(
        password: String,
        address: String = ADDRESS,
    ) = MutFlow.underTest { api.createSession(password, address, null) }

    private fun change(
        session: String,
        current: String?,
        new: String?,
        address: String = ADDRESS,
    ) = MutFlow.underTest { api.changePassword(session, current, new, address) }

    @Test
    fun `Вход до шага admin отвечает setup_required`() {
        assertEquals(SignInResult.SetupRequired, signIn(PASSWORD))
    }

    @Test
    fun `Вход до шага admin не засчитывается в перебор`() {
        repeat(10) { signIn("wrong-password-123") }
        withAdministrator()

        assertEquals(SignInResult.WrongPassword, signIn("wrong-password-123"))
    }

    @Test
    fun `Верный пароль выдаёт сессию тенанта по умолчанию`() {
        withAdministrator()

        val result = signIn(PASSWORD)

        assertIs<SignInResult.SignedIn>(result)
        assertEquals(TENANT, store.find(result.sessionId)?.tenantId)
    }

    @Test
    fun `Неверный пароль отвечает WrongPassword, а шестая попытка после пяти неудач — Locked`() {
        withAdministrator()
        repeat(5) { assertEquals(SignInResult.WrongPassword, signIn("wrong-password-123")) }

        assertEquals(SignInResult.Locked(900), signIn(PASSWORD))
    }

    @Test
    fun `Во время блокировки вход не читает базу`() {
        withAdministrator()
        repeat(5) { signIn("wrong-password-123") }
        val reads = administrators.reads

        signIn(PASSWORD)

        assertEquals(reads, administrators.reads)
    }

    @Test
    fun `Недоступная база — исключение доступа к данным, и попытка не засчитывается`() {
        withAdministrator()
        repeat(4) { signIn("wrong-password-123") }
        administrators.down = true

        repeat(3) { assertFailsWith<DataAccessResourceFailureException> { signIn("wrong-password-123") } }
        administrators.down = false

        assertEquals(SignInResult.WrongPassword, signIn("wrong-password-123"))
    }

    @Test
    fun `Пароль после смены нужен точный, без обрезки и без смены регистра`() {
        administrators.stored = hasher.hash("$PASSWORD ")

        assertEquals(SignInResult.WrongPassword, signIn(PASSWORD))
        assertEquals(SignInResult.WrongPassword, signIn("Correct-Horse-Battery "))
        assertIs<SignInResult.SignedIn>(signIn("$PASSWORD "))
    }

    private fun session(): String {
        withAdministrator()
        return (signIn(PASSWORD) as SignInResult.SignedIn).sessionId
    }

    @Test
    fun `Смена пароля с верным текущим продолжает сессию под новым идентификатором`() {
        val old = session()

        val result = change(old, PASSWORD, "new-password-2026")

        assertIs<PasswordChangeResult.Changed>(result)
        assertNotEquals(old, result.sessionId)
        assertNull(store.find(old))
        assertEquals(TENANT, store.find(result.sessionId)?.tenantId)
    }

    @Test
    fun `Смена пароля завершает все прочие сессии`() {
        val old = session()
        val other = (signIn(PASSWORD) as SignInResult.SignedIn).sessionId

        change(old, PASSWORD, "new-password-2026")

        assertNull(store.find(other))
    }

    @Test
    fun `После смены принимается только новый пароль`() {
        val old = session()

        change(old, PASSWORD, "new-password-2026")

        assertEquals(SignInResult.WrongPassword, signIn(PASSWORD))
        assertIs<SignInResult.SignedIn>(signIn("new-password-2026"))
    }

    @Test
    fun `В базе после смены лежит хэш нового пароля`() {
        val old = session()

        change(old, PASSWORD, "new-password-2026")

        assertTrue(hasher.matches("new-password-2026", checkNotNull(administrators.stored)))
    }

    @Test
    fun `Неверный текущий пароль отвечает WrongPassword и ничего не меняет`() {
        val old = session()
        val stored = administrators.stored

        assertEquals(PasswordChangeResult.WrongPassword, change(old, "wrong-password-123", "new-password-2026"))

        assertNotNull(store.find(old))
        assertEquals(stored, administrators.stored)
    }

    @Test
    fun `Неверный текущий пароль засчитывается в перебор общим со входом счётчиком`() {
        val old = session()
        repeat(4) { signIn("wrong-password-123") }

        change(old, "wrong-password-123", "new-password-2026")

        assertEquals(SignInResult.Locked(900), signIn(PASSWORD))
    }

    @Test
    fun `Во время блокировки смена пароля отвечает Locked и не проверяет пароль`() {
        val old = session()
        repeat(5) { signIn("wrong-password-123") }

        assertEquals(PasswordChangeResult.Locked(900), change(old, PASSWORD, "new-password-2026"))

        assertTrue(hasher.matches(PASSWORD, checkNotNull(administrators.stored)))
    }

    @Test
    fun `Недопустимый новый пароль не засчитывается и называет поле newPassword`() {
        val old = session()
        repeat(4) { signIn("wrong-password-123") }

        for (bad in listOf("", "short-pw-11", "a".repeat(1025), null)) {
            assertEquals(PasswordChangeResult.InvalidField("newPassword"), change(old, PASSWORD, bad))
        }

        assertEquals(SignInResult.WrongPassword, signIn("wrong-password-123"))
    }

    @Test
    fun `Отсутствующий текущий пароль называет поле currentPassword`() {
        val old = session()

        assertEquals(PasswordChangeResult.InvalidField("currentPassword"), change(old, null, "new-password-2026"))
    }

    @Test
    fun `Недопустимый новый пароль отвечает 422 раньше проверки текущего`() {
        val old = session()

        assertEquals(PasswordChangeResult.InvalidField("newPassword"), change(old, "wrong-password-123", "short"))
    }

    @Test
    fun `Успешная смена обнуляет счётчик неудач адреса`() {
        val old = session()
        repeat(4) { signIn("wrong-password-123") }

        change(old, PASSWORD, "new-password-2026")
        repeat(4) { signIn("wrong-password-123") }

        assertEquals(SignInResult.WrongPassword, signIn("wrong-password-123"))
    }

    @Test
    fun `Проигравшая гонку смена отвечает WrongPassword`() {
        val old = session()
        val racing =
            object : Administrators by administrators {
                override fun replaceHash(
                    expected: String,
                    hash: String,
                    now: Instant,
                ): Boolean {
                    administrators.stored = hasher.hash("password-from-a1")
                    return administrators.replaceHash(expected, hash, now)
                }
            }
        val raced = SessionApiImpl(racing, hasher, store, tracker, { TENANT }, clock)

        val result = raced.changePassword(old, PASSWORD, "password-from-b1", ADDRESS)

        assertEquals(PasswordChangeResult.WrongPassword, result)
        assertNotNull(store.find(old))
    }

    @Test
    fun `Смена пароля при сбросе администратора отвечает WrongPassword`() {
        val old = session()
        administrators.stored = null

        assertEquals(PasswordChangeResult.WrongPassword, change(old, PASSWORD, "new-password-2026"))
    }

    @Test
    fun `Смена пароля при недоступной базе — исключение, сессии целы`() {
        val old = session()
        val other = (signIn(PASSWORD) as SignInResult.SignedIn).sessionId
        administrators.down = true

        assertFailsWith<DataAccessResourceFailureException> { change(old, PASSWORD, "new-password-2026") }

        assertNotNull(store.find(old))
        assertNotNull(store.find(other))
    }

    private fun lockWarnings(block: () -> Unit) =
        captureEvents(block).count { it.level == Level.WARN && "Sign-in locked from $ADDRESS" in it.text }

    @Test
    fun `Блокировка пишет предупреждение ровно на пятой неудаче входа`() {
        withAdministrator()

        val warnings = (1..5).map { lockWarnings { signIn("wrong-password-123") } }

        assertEquals(listOf(0, 0, 0, 0, 1), warnings)
        assertEquals(0, lockWarnings { signIn(PASSWORD) })
    }

    @Test
    fun `Блокировка пишет предупреждение ровно на пятой неудаче смены пароля`() {
        val old = session()
        repeat(3) { signIn("wrong-password-123") }

        val warnings = (1..2).map { lockWarnings { change(old, "wrong-password-123", "new-password-2026") } }

        assertEquals(listOf(0, 1), warnings)
    }

    private fun lockOut() = repeat(5) { signIn("wrong-password-123") }

    @Test
    fun `Заблокированный адрес остаётся заблокированным при повторных попытках`() {
        withAdministrator()
        lockOut()

        repeat(3) { assertEquals(SignInResult.Locked(900), signIn(PASSWORD)) }
    }

    @Test
    fun `Остаток блокировки округляется вверх до секунды`() {
        withAdministrator()
        lockOut()

        clock.now = T0 + Duration.ofMillis(1)
        assertEquals(SignInResult.Locked(900), signIn(PASSWORD))

        clock.now = T0 + Duration.ofMinutes(10) + Duration.ofSeconds(1) - Duration.ofNanos(1)
        assertEquals(SignInResult.Locked(300), signIn(PASSWORD))

        clock.now = T0 + Duration.ofMinutes(14) + Duration.ofSeconds(59) + Duration.ofMillis(999)
        assertEquals(SignInResult.Locked(1), signIn(PASSWORD))
    }

    @Test
    fun `Через 15 минут блокировка снимается и тот же адрес блокируется снова после пяти новых неудач`() {
        withAdministrator()
        lockOut()

        clock.now = T0 + Duration.ofMinutes(15)
        repeat(5) { assertEquals(SignInResult.WrongPassword, signIn("wrong-password-123")) }

        assertEquals(SignInResult.Locked(900), signIn(PASSWORD))
    }

    @Test
    fun `Неудача за миллисекунду до конца окна ещё считается`() {
        withAdministrator()
        repeat(4) { signIn("wrong-password-123") }

        clock.now = T0 + Duration.ofMinutes(15) - Duration.ofMillis(1)
        signIn("wrong-password-123")

        assertEquals(SignInResult.Locked(900), signIn(PASSWORD))
    }

    @Test
    fun `Неудачи ровно 15-минутной давности выпадают из окна`() {
        withAdministrator()
        repeat(4) { signIn("wrong-password-123") }

        clock.now = T0 + Duration.ofMinutes(15)
        repeat(4) { assertEquals(SignInResult.WrongPassword, signIn("wrong-password-123")) }

        assertIs<SignInResult.SignedIn>(signIn(PASSWORD))
    }

    @Test
    fun `Вход до шага admin не оставляет адрес в памяти`() {
        signIn(PASSWORD)

        assertEquals(0, tracker.trackedAddresses())
    }

    @Test
    fun `Идентификатор сессии — 64 символа hex, и при входе, и после смены пароля`() {
        val hex = Regex("[0-9a-f]{64}")
        val old = session()

        assertTrue(hex.matches(old), old)
        assertTrue(hex.matches((change(old, PASSWORD, "new-password-2026") as PasswordChangeResult.Changed).sessionId))
    }

    @Test
    fun `Смена пароля оставляет сессию в её тенанте`() {
        val tenant = UUID.randomUUID()
        withAdministrator()
        val old = store.create(tenant).id

        val result = change(old, PASSWORD, "new-password-2026") as PasswordChangeResult.Changed

        assertEquals(tenant, store.find(result.sessionId)?.tenantId)
    }

    @Test
    fun `Смена пароля с истёкшей сессией продолжает её в тенанте по умолчанию`() {
        val tenant = UUID.randomUUID()
        withAdministrator()
        val old = store.create(tenant).id
        clock.now = T0 + Duration.ofHours(13)

        val result = change(old, PASSWORD, "new-password-2026") as PasswordChangeResult.Changed

        assertEquals(TENANT, store.find(result.sessionId)?.tenantId)
    }

    @Test
    fun `Выдача новой сессии стирает истёкшие`() {
        withAdministrator()
        store.create(TENANT)
        clock.now = T0 + Duration.ofHours(13)

        signIn(PASSWORD)

        assertEquals(1, store.trackedSessions())
    }
}
