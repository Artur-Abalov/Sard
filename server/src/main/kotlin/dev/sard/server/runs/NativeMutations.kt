// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import org.hibernate.Session

private val TEXT_VALUES = setOf("message", "output")

/** Binds the [values] that [sql] names; text values are typed so that NULL binds as text. */
internal fun Session.execute(
    sql: String,
    values: Map<String, Any?>,
): Int {
    val query = createNativeMutationQuery(sql)
    for ((name, value) in values.filterKeys { ":$it" in sql }) {
        if (name in TEXT_VALUES) {
            query.setParameter(name, value as String?, String::class.java)
        } else {
            query.setParameter(name, value)
        }
    }
    return query.executeUpdate()
}
