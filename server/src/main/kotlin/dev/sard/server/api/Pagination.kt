// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.persistence.PageKey
import java.time.Instant
import java.util.Base64
import java.util.UUID

private const val MIN_LIMIT = 1
private const val MAX_LIMIT = 200
private const val SEPARATORS = 3

/** Which list a cursor belongs to; a cursor of one list is refused by every other (422 cursor). */
enum class CursorKind(
    val tag: String,
) {
    AGENTS("agents"),
    TOKENS("tokens"),
    SOURCES("sources"),
    RUNS("runs"),
    SNAPSHOTS("snapshots"),
}

/** A page of [items]; [nextCursor] is null on the last one. */
class Slice<T>(
    val items: List<T>,
    val nextCursor: String?,
)

/**
 * Where a list page starts and how many rows it holds. The domain is asked for [fetch] rows, one more
 * than the page, so that [slice] knows whether another page exists without a count.
 */
class PageRequest(
    private val kind: CursorKind,
    val after: PageKey?,
    val limit: Int,
) {
    val fetch: Int get() = limit + 1

    fun <T> slice(
        rows: List<T>,
        key: (T) -> PageKey,
    ): Slice<T> {
        val items = rows.take(limit)
        val next = if (rows.size > limit) items.last().let(key).let { encode(kind, it) } else null
        return Slice(items, next)
    }
}

/** Validates `limit` (1 to 200) and `cursor` of a list of [kind]; 422 with the parameter's name otherwise. */
fun pageRequest(
    kind: CursorKind,
    cursor: String?,
    limit: Int,
): PageRequest {
    if (limit !in MIN_LIMIT..MAX_LIMIT) throw RequestInvalid("limit", "must be between $MIN_LIMIT and $MAX_LIMIT")
    return PageRequest(kind, cursor?.let { decode(kind, it) }, limit)
}

private fun encode(
    kind: CursorKind,
    key: PageKey,
): String =
    Base64
        .getUrlEncoder()
        .withoutPadding()
        .encodeToString("${kind.tag}|${key.at}|${key.id}".toByteArray())

/** Anything that is not a cursor this server issued for this list is one error: the parameter is unusable. */
private fun decode(
    kind: CursorKind,
    cursor: String,
): PageKey {
    val key =
        runCatching {
            val parts = String(Base64.getUrlDecoder().decode(cursor)).split('|')
            if (parts.size == SEPARATORS && parts[0] == kind.tag) PageKey(Instant.parse(parts[1]), UUID.fromString(parts[2])) else null
        }.getOrNull()
    return key ?: throw RequestInvalid("cursor", "is not a cursor of this list")
}
