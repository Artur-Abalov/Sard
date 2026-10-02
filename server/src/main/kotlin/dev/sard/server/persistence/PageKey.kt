// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.persistence

import java.time.Instant
import java.util.UUID

/**
 * The position of a row in a list ordered newest first: by time, then by id, both descending
 * (S8b, "Списки — курсорная пагинация"). A page starts after the key of the last row of the one
 * before it, so rows created meanwhile never shift it: nothing is skipped or repeated.
 */
data class PageKey(
    val at: Instant,
    val id: UUID,
) {
    companion object {
        /** The HQL condition "strictly after [this key]" for [time] and [id] columns, bound to :keyAt and :keyId. */
        fun condition(
            time: String,
            id: String = "id",
        ) = "($time < :keyAt or ($time = :keyAt and $id < :keyId))"
    }

    /** Binds [condition]'s parameters. */
    fun <Q : org.hibernate.query.CommonQueryContract> bind(query: Q): Q {
        query.setParameter("keyAt", at)
        query.setParameter("keyId", id)
        return query
    }
}
