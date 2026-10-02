// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.persistence

/** The `where` clause of [conditions] joined by `and`; nothing when there are none. */
fun hqlWhere(conditions: List<String>): String = if (conditions.isEmpty()) "" else conditions.joinToString(AND, WHERE)

private const val AND = " and "
private const val WHERE = "where "
