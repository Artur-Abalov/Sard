// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import com.fasterxml.jackson.annotation.JsonProperty
import dev.sard.server.extension.TenantResolver
import dev.sard.server.persistence.PageKey
import dev.sard.server.scheduler.FireView
import dev.sard.server.scheduler.LastRun
import dev.sard.server.scheduler.ScheduleDraft
import dev.sard.server.scheduler.ScheduleView
import dev.sard.server.scheduler.Schedules
import io.swagger.v3.oas.annotations.Operation
import io.swagger.v3.oas.annotations.media.Schema
import io.swagger.v3.oas.annotations.tags.Tag
import org.springframework.http.HttpStatus
import org.springframework.http.MediaType
import org.springframework.stereotype.Component
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.PathVariable
import org.springframework.web.bind.annotation.PutMapping
import org.springframework.web.bind.annotation.RequestBody
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.RequestParam
import org.springframework.web.bind.annotation.ResponseStatus
import org.springframework.web.bind.annotation.RestController
import java.time.Instant
import java.util.UUID

private const val CRON = "Standard cron: five fields (minute hour day-of-month month day-of-week), names allowed"
private const val TIMEZONE = "An IANA time zone, such as Europe/Berlin"

@Schema(description = "A source's schedule as set")
data class ScheduleInput(
    @field:Schema(description = CRON, example = "30 2 * * *")
    val cron: String,
    @field:Schema(description = TIMEZONE, example = "Europe/Berlin")
    val timezone: String,
    @field:Schema(description = "A disabled schedule keeps its cron and zone and does not fire")
    val enabled: Boolean,
    @field:Schema(description = NOTIFY_ON_SUCCESS)
    val notifyOnSuccess: Boolean = false,
)

private const val NOTIFY_ON_SUCCESS =
    "Tell of every successful scheduled run too; failures and recoveries are always told. Off by default"

@Schema(description = "The latest run the schedule created, scheduled or catch-up")
data class ScheduleLastRun(
    val id: UUID,
    val trigger: RunTrigger,
    val status: RunStatus,
    val queuedAt: Instant,
    val finishedAt: Instant?,
)

@Schema(description = "A source's schedule")
data class Schedule(
    val id: UUID,
    val sourceId: UUID,
    @field:Schema(description = CRON)
    val cron: String,
    @field:Schema(description = TIMEZONE)
    val timezone: String,
    val enabled: Boolean,
    @field:Schema(description = "The next fire by cron; null while disabled")
    val nextRunAt: Instant?,
    @field:Schema(description = "When the catch-up owed after a server downtime runs; null when none is owed")
    val catchUpAt: Instant?,
    val lastFiredAt: Instant?,
    @field:Schema(description = "Fires skipped in a row: an active run, a revoked agent, a refusal")
    val skippedInRow: Int,
    @field:Schema(description = NOTIFY_ON_SUCCESS)
    val notifyOnSuccess: Boolean,
    @field:Schema(description = "The latest run created by this schedule (schedule or catch_up); null if none yet")
    val lastRun: ScheduleLastRun?,
    val createdAt: Instant,
    val updatedAt: Instant,
)

/** What fired (schedule_fires.kind). */
@Schema(enumAsRef = true)
enum class ScheduleFireKind {
    @JsonProperty("schedule")
    SCHEDULE,

    @JsonProperty("catch_up")
    CATCH_UP,
}

/** What came of a fire (schedule_fires.outcome). */
@Schema(enumAsRef = true)
enum class ScheduleFireOutcome {
    @JsonProperty("run_created")
    RUN_CREATED,

    @JsonProperty("skipped_active")
    SKIPPED_ACTIVE,

    @JsonProperty("skipped_gone")
    SKIPPED_GONE,

    @JsonProperty("refused")
    REFUSED,

    @JsonProperty("skipped_downtime")
    SKIPPED_DOWNTIME,
}

/** Why a fire was gone or refused (schedule_fires.reason). */
@Schema(enumAsRef = true)
enum class ScheduleFireReason {
    @JsonProperty("source_deleted")
    SOURCE_DELETED,

    @JsonProperty("agent_revoked")
    AGENT_REVOKED,

    @JsonProperty("unknown_plugin")
    UNKNOWN_PLUGIN,

    @JsonProperty("unknown_repository")
    UNKNOWN_REPOSITORY,
}

@Schema(description = "One fire of a schedule and what came of it")
data class ScheduleFire(
    val id: UUID,
    val kind: ScheduleFireKind,
    @field:Schema(description = "The cron moment; for a downtime the first fire missed; for a catch-up its slot")
    val scheduledFor: Instant,
    val outcome: ScheduleFireOutcome,
    @field:Schema(description = "The run created, or for skipped_active the run that was active")
    val runId: UUID?,
    val reason: ScheduleFireReason?,
    @field:Schema(description = "skipped_downtime: how many fires were missed (counting stops at 10 000)")
    val missedCount: Int?,
    @field:Schema(description = "skipped_downtime: the last fire missed")
    val missedUntil: Instant?,
    @field:Schema(description = "skipped_downtime: more fires were missed than counted; missedCount is a lower bound")
    val missedCountCapped: Boolean,
    @field:Schema(description = "Fires skipped in a row after this one")
    val skippedInRow: Int,
    @field:Schema(description = "This skip brought skippedInRow to the alert threshold")
    val alert: Boolean,
    val recordedAt: Instant,
)

@Schema(description = "A page of a schedule's fires")
data class ScheduleFirePage(
    val items: List<ScheduleFire>,
    @field:Schema(description = CURSOR_NEXT)
    val nextCursor: String?,
)

/** What the schedule endpoints do (F3a). */
interface SchedulesApi {
    fun getSchedule(sourceId: UUID): Schedule

    fun setSchedule(
        sourceId: UUID,
        schedule: ScheduleInput,
    ): Schedule

    fun listScheduleFires(
        sourceId: UUID,
        cursor: String?,
        limit: Int,
    ): ScheduleFirePage
}

@RestController
@RequestMapping("/api/v1/sources/{sourceId}/schedule", produces = [MediaType.APPLICATION_JSON_VALUE])
@Tag(name = "sources")
class SchedulesController(
    private val api: SchedulesApi,
) {
    @GetMapping
    @ResponseStatus(HttpStatus.OK)
    @Operation(summary = "The source's schedule", description = "404 when the source has none.")
    @NotFound
    fun getSchedule(
        @PathVariable sourceId: UUID,
    ): Schedule = api.getSchedule(sourceId)

    @PutMapping(consumes = [MediaType.APPLICATION_JSON_VALUE])
    @ResponseStatus(HttpStatus.OK)
    @Operation(
        summary = "Set the source's schedule",
        description =
            "Creates or replaces it; enabled=false disables it. Setting it unchanged keeps its next fire; a change " +
                "starts over from now, without a catch-up. 422 validation_failed names cron or timezone.",
    )
    @NotFound
    @Unprocessable
    fun setSchedule(
        @PathVariable sourceId: UUID,
        @RequestBody schedule: ScheduleInput,
    ): Schedule = api.setSchedule(sourceId, schedule)

    @GetMapping("/fires")
    @ResponseStatus(HttpStatus.OK)
    @Operation(
        summary = "The journal of the source's schedule",
        description = "Newest recorded first; empty for a source without a schedule.",
    )
    @NotFound
    @Unprocessable
    fun listScheduleFires(
        @PathVariable sourceId: UUID,
        @PageCursor @RequestParam(required = false) cursor: String?,
        @PageLimit @RequestParam(defaultValue = DEFAULT_LIMIT) limit: Int,
    ): ScheduleFirePage = api.listScheduleFires(sourceId, cursor, limit)
}

/** The schedule endpoints over [Schedules], in the tenant of the session. */
@Component
class SchedulesApiImpl(
    private val schedules: Schedules,
    private val tenants: TenantResolver,
) : SchedulesApi {
    override fun getSchedule(sourceId: UUID): Schedule =
        schedules.get(tenants.currentTenantId(), sourceId)?.let(::scheduleOf) ?: throw ResourceNotFound()

    override fun setSchedule(
        sourceId: UUID,
        schedule: ScheduleInput,
    ): Schedule {
        val draft = ScheduleDraft(schedule.cron, schedule.timezone, schedule.enabled, schedule.notifyOnSuccess)
        return scheduleOf(schedules.set(tenants.currentTenantId(), sourceId, draft))
    }

    override fun listScheduleFires(
        sourceId: UUID,
        cursor: String?,
        limit: Int,
    ): ScheduleFirePage {
        val page = pageRequest(CursorKind.FIRES, cursor, limit)
        val rows = schedules.fires(tenants.currentTenantId(), sourceId, page.after, page.fetch)
        val slice = page.slice(rows) { PageKey(it.recordedAt, it.id) }
        return ScheduleFirePage(slice.items.map(::fireOf), slice.nextCursor)
    }

    private fun scheduleOf(view: ScheduleView) =
        Schedule(
            view.id,
            view.sourceId,
            view.cron,
            view.timezone,
            view.enabled,
            view.nextRunAt,
            view.catchUpAt,
            view.lastFiredAt,
            view.skippedInRow,
            view.notifyOnSuccess,
            view.lastRun?.let(::lastRunOf),
            view.createdAt,
            view.updatedAt,
        )

    private fun lastRunOf(run: LastRun) =
        ScheduleLastRun(
            run.id,
            RunTrigger.valueOf(run.trigger.name),
            RunStatus.valueOf(run.status.name),
            run.queuedAt,
            run.finishedAt,
        )

    private fun fireOf(view: FireView) =
        ScheduleFire(
            view.id,
            ScheduleFireKind.valueOf(view.kind.name),
            view.scheduledFor,
            ScheduleFireOutcome.valueOf(view.outcome.name),
            view.runId,
            view.reason?.let { ScheduleFireReason.valueOf(it.name) },
            view.missedCount,
            view.missedUntil,
            view.missedCountCapped,
            view.skippedInRow,
            view.alert,
            view.recordedAt,
        )
}
