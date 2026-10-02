// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.persistence.PageKey
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNotNull
import kotlin.test.assertNull

private val AT = Instant.parse("2026-10-01T12:00:00.123456Z")
private val ID = UUID.fromString("0192f7a0-0000-7000-8000-000000000001")

/** Lists are paged by keyset with an opaque cursor (S8b, "Списки — курсорная пагинация"). */
@MutFlowTest
class PaginationTest {
    private fun request(
        cursor: String? = null,
        limit: Int = 50,
        kind: CursorKind = CursorKind.RUNS,
    ) = MutFlow.underTest { pageRequest(kind, cursor, limit) }

    private fun nextCursor(
        kind: CursorKind,
        rows: List<Int>,
        limit: Int,
    ): String? = request(null, limit, kind).slice(rows) { PageKey(AT.plusSeconds(it.toLong()), ID) }.nextCursor

    @Test
    fun `a limit from 1 to 200 is accepted`() {
        assertEquals(1, request(limit = 1).limit)
        assertEquals(200, request(limit = 200).limit)
    }

    @Test
    fun `a limit outside 1 to 200 is an error at limit`() {
        for (limit in listOf(0, -1, 201)) {
            val error = assertFailsWith<RequestInvalid> { request(limit = limit) }
            assertEquals("limit", error.field)
        }
    }

    @Test
    fun `the domain is asked for one row more than the page`() {
        assertEquals(51, request().fetch)
        assertEquals(2, request(limit = 1).fetch)
    }

    @Test
    fun `a page with more rows than its limit ends with a cursor that continues after its last item`() {
        val page = request(null, 2).slice(listOf(3, 2, 1)) { PageKey(AT.plusSeconds(it.toLong()), ID) }

        assertEquals(listOf(3, 2), page.items)
        val next = request(page.nextCursor, 2)
        assertEquals(PageKey(AT.plusSeconds(2), ID), next.after)
    }

    @Test
    fun `a page with exactly its limit of rows is the last one`() {
        assertNull(nextCursor(CursorKind.RUNS, listOf(2, 1), 2))
        assertNull(nextCursor(CursorKind.RUNS, emptyList(), 2))
        assertNotNull(nextCursor(CursorKind.RUNS, listOf(3, 2, 1), 2))
    }

    @Test
    fun `microseconds of the time survive the cursor`() {
        val cursor = request(null, 1).slice(listOf(1, 2)) { PageKey(AT, ID) }.nextCursor

        assertEquals(PageKey(AT, ID), request(cursor, 1).after)
    }

    @Test
    fun `a cursor of another list, or none at all, is an error at cursor`() {
        val tokens = nextCursor(CursorKind.TOKENS, listOf(3, 2, 1), 2)
        for (cursor in listOf(tokens, "garbage", "", "!!!")) {
            val error = assertFailsWith<RequestInvalid> { request(cursor, 2, CursorKind.RUNS) }
            assertEquals("cursor", error.field)
        }
    }

    @Test
    fun `a cursor of a list is accepted by that list`() {
        for (kind in CursorKind.entries) {
            val cursor = nextCursor(kind, listOf(3, 2, 1), 2)

            assertEquals(PageKey(AT.plusSeconds(2), ID), request(cursor, 2, kind).after, kind.name)
        }
    }

    @Test
    fun `the condition of a key is strictly after it in time, then in id`() {
        assertEquals("(r.at < :keyAt or (r.at = :keyAt and r.id < :keyId))", PageKey.condition("r.at", "r.id"))
        assertEquals("(created < :keyAt or (created = :keyAt and id < :keyId))", PageKey.condition("created"))
    }
}
