// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.sample

import org.springframework.http.HttpStatus
import org.springframework.http.MediaType
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.PostMapping
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.ResponseStatus
import org.springframework.web.bind.annotation.RestController

/** One row of sample_events as the endpoint shows it. */
data class SampleEvent(
    val id: Long,
    val type: String,
    val detail: String,
)

/** The sample's REST: under /api/v1, so the session of the core guards it and OpenAPI describes it (CORE1 Р8). */
@RestController
@RequestMapping("/api/v1/sample")
class SampleController(
    private val jdbc: JdbcTemplate,
) {
    @GetMapping("/events", produces = [MediaType.APPLICATION_JSON_VALUE])
    fun events(): List<SampleEvent> =
        jdbc.query("select id, type, detail from sample_events order by id") { rs, _ ->
            SampleEvent(rs.getLong("id"), rs.getString("type"), rs.getString("detail"))
        }

    @PostMapping("/notes")
    @ResponseStatus(HttpStatus.NO_CONTENT)
    fun note() = Unit
}
