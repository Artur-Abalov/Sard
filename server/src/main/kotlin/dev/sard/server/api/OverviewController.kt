// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import io.swagger.v3.oas.annotations.Operation
import io.swagger.v3.oas.annotations.media.Schema
import io.swagger.v3.oas.annotations.tags.Tag
import org.springframework.http.HttpStatus
import org.springframework.http.MediaType
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.ResponseStatus
import org.springframework.web.bind.annotation.RestController

@Schema(description = "The first steps of a tenant, each marked by the server by its own rule; recomputed per request")
data class FirstSteps(
    @field:Schema(description = "At least one enrollment token was created, in any status")
    val tokenIssued: Boolean,
    @field:Schema(description = "A not revoked agent has sent Hello at least once")
    val agentConnected: Boolean,
    @field:Schema(description = "A not revoked agent's last Register has a repository with a repositoryId")
    val repositoryInitialized: Boolean,
    @field:Schema(description = "At least one source is not deleted")
    val sourceCreated: Boolean,
    @field:Schema(description = "At least one run, of any source or agent, has succeeded")
    val backupSucceeded: Boolean,
    @field:Schema(description = "All five steps are done")
    val complete: Boolean,
)

@Schema(description = "What the console's overview page shows besides the lists")
data class Overview(
    @field:Schema(description = "Not revoked agents that are online, as in the agent list")
    val agentsOnline: Int,
    @field:Schema(description = "Not revoked agents")
    val agentsTotal: Int,
    val firstSteps: FirstSteps,
)

/** What the overview endpoint does; W2 implements it. */
interface OverviewApi {
    fun overview(): Overview
}

@RestController
@RequestMapping("/api/v1/overview", produces = [MediaType.APPLICATION_JSON_VALUE])
@Tag(name = "overview")
class OverviewController(
    private val api: OverviewApi,
) {
    @GetMapping
    @ResponseStatus(HttpStatus.OK)
    @Operation(
        summary = "Overview",
        description = "Agent counts of the tenant and the state of the five first steps.",
    )
    fun overview(): Overview = api.overview()
}
