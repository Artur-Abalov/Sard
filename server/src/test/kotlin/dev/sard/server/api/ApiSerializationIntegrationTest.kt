// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.TestcontainersConfiguration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import tools.jackson.databind.JsonNode
import tools.jackson.databind.ObjectMapper
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private const val CA_FINGERPRINT = "8544e2352a80a3d403eed68f8bb4ff271d0ce02c09c1d0faf6f090cea423be9f"

/**
 * The spec is derived by swagger through Jackson 2, the server writes JSON with
 * Jackson 3: every DTO, serialized by the application's mapper, must match its
 * schema in /v3/api-docs — property names, required, null, enums and formats.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class)
class ApiSerializationIntegrationTest(
    @Autowired private val mapper: ObjectMapper,
    @LocalServerPort private val port: Int,
) {
    private val spec: JsonNode by lazy {
        val request = HttpRequest.newBuilder(URI.create("http://localhost:$port/v3/api-docs")).build()
        mapper.readTree(HttpClient.newHttpClient().send(request, HttpResponse.BodyHandlers.ofString()).body())
    }

    private val id = UUID.fromString("0192f7a0-0000-7000-8000-000000000001")
    private val at = Instant.parse("2026-09-27T10:00:00Z")
    private val schema = mapOf("type" to "object", "properties" to mapOf("paths" to mapOf("type" to "array")))

    private val summary =
        AgentSummary(
            id,
            "db1",
            AgentStatus.ONLINE,
            "0.1.0",
            "linux",
            "amd64",
            at,
            at,
            revokedAt = null,
            duplicateSessionAt = at,
            outdated = false,
            builtin = true,
            role = AgentRole.SOURCE,
            certificate = AgentCertificate(at, at.plusSeconds(600), daysLeft = 0, AgentCertificateState.EXPIRING, at),
            repositories = listOf(AgentRepository("local", "local", "r1", "file", RecoveryKeyFlag.ABSENT)),
        )
    private val step =
        RunStep(
            id,
            1,
            StepAction.BACKUP,
            StepStatus.SUCCEEDED,
            StepPhase.UPLOADING,
            id,
            id,
            "files",
            "local",
            10,
            10,
            filesProcessed = 3,
            filesTotal = 4,
            message = null,
            backup =
                BackupOutput(
                    "4f1c",
                    10,
                    2,
                    "a1b2",
                    partial = false,
                    restorable = true,
                    totalFiles = 3,
                    fingerprint =
                        Fingerprint("f".repeat(64), 1, "files", listOf(FingerprintFact("files", "exact", "3")), 2),
                    warnings = listOf("the size fell by 50% since the last snapshot (20 → 10 bytes)"),
                ),
            restore = RestoreOutput("/r/etc", 2, 4, null, null, null, ownersRestored = false, leftover = null),
            RestoreOutcome.RESTORED,
            at,
            at,
            at,
            at,
        )
    private val queuedStep =
        RunStep(
            id,
            1,
            StepAction.RUN,
            StepStatus.QUEUED,
            phase = null,
            id,
            sourceId = null,
            "files",
            repositoryName = null,
            bytesProcessed = null,
            bytesTotal = null,
            filesProcessed = null,
            filesTotal = null,
            message = null,
            backup = null,
            restore = null,
            restoreOutcome = null,
            queuedAt = at,
            dispatchedAt = null,
            startedAt = null,
            finishedAt = null,
        )
    private val source =
        Source(
            id,
            "etc",
            id,
            "files",
            "local",
            mapOf("paths" to listOf("/etc")),
            at,
            at,
            null,
            true,
            null,
            null,
            null,
            emptyList(),
        )
    private val systemSource =
        Source(
            id,
            "Sard: database",
            id,
            "postgresql",
            "offsite",
            mapOf("host" to "postgres"),
            at,
            at,
            SystemSourceRole.SELF_DATABASE,
            verificationEnabled = true,
            verificationStatus = VerificationOutcome.PASSED,
            lastVerifiedAt = at,
            lastVerifiedLevel = 5,
            secretWarnings = listOf("secret \"sard-db\" is not allowed for postgresql at postgres:5432/sard"),
        )
    private val token =
        EnrollmentToken(
            id,
            EnrollmentTokenStatus.USED,
            at,
            at,
            at,
            revokedAt = null,
            agentId = id,
            label = null,
            role = AgentRole.VERIFIER,
        )
    private val snapshot =
        Snapshot(
            id,
            "4f1c",
            id,
            id,
            id,
            id,
            "local",
            "a1b2",
            10,
            2,
            at,
            forgottenAt = null,
            partial = false,
            restorable = true,
            restoreBlockedReason = null,
        )
    private val schedule =
        Schedule(
            id,
            id,
            "30 2 * * *",
            "Europe/Berlin",
            true,
            at,
            null,
            at,
            0,
            notifyOnSuccess = false,
            lastRun = ScheduleLastRun(id, RunTrigger.SCHEDULE, RunStatus.FAILED, at, at),
            createdAt = at,
            updatedAt = at,
        )
    private val fire =
        ScheduleFire(
            id,
            ScheduleFireKind.SCHEDULE,
            at,
            ScheduleFireOutcome.SKIPPED_DOWNTIME,
            runId = null,
            reason = null,
            missedCount = 3,
            missedUntil = at,
            missedCountCapped = false,
            skippedInRow = 0,
            alert = false,
            recordedAt = at,
        )
    private val summaryRun =
        RunSummary(
            id,
            id,
            "etc",
            false,
            id,
            RunTrigger.MANUAL,
            RunStatus.FAILED,
            "boom",
            at,
            at,
            at,
            catchUp = null,
            kind = RunKind.BACKUP,
            requestedBy = "admin",
        )

    private fun onboarding(
        access: OnboardingAccess,
        withCa: Boolean,
    ) = Onboarding(
        listOf(
            OnboardingStep(OnboardingStepId.CA, OnboardingStepState.PENDING),
            OnboardingStep(OnboardingStepId.ADMIN, OnboardingStepState.PENDING),
            OnboardingStep(OnboardingStepId.SELF_BACKUP, OnboardingStepState.UPCOMING),
            OnboardingStep(OnboardingStepId.KEYS_CONFIRMED, OnboardingStepState.UPCOMING),
        ),
        SetupCodeState.ACTIVE,
        access,
        ca =
            if (withCa) {
                CaInfo(CA_FINGERPRINT, CaOrigin.IMPORTED, "/k/ca.key")
            } else {
                null
            },
        caReplaceable = if (withCa) true else null,
        restriction = OnboardingRestriction.FULL,
    )

    private val verifierSample =
        Verifier(id, "verifier1", true, true, null, SandboxRuntime.DOCKER, listOf("e"), listOf("files"), false, 0, null)
    private val verificationJob =
        VerificationJob(id, VerificationJobStatus.QUEUED, VerificationJobReason.CYCLE, at, at, 1, emptyList())
    private val verificationRecord =
        Verification(
            id,
            id,
            id,
            id,
            "4a3b2c1d",
            null,
            null,
            1,
            VerificationOutcome.NO_VERIFIER,
            0,
            "no verifier fits",
            listOf(VerificationStage(0, "fingerprint_tag", null, null, null, 0, "", "")),
            listOf(FingerprintMismatch("tables", "12", "0")),
            listOf(VerifierMisfit(id, "verifier1", listOf(VerifierReason.OFFLINE))),
            null,
            false,
            at,
        )

    private val samples: List<Any> =
        listOf(
            StatusResponse("1.0.0", lastVerifiedRestoreAt = null),
            SourceVerificationInput(enabled = false),
            VerifyNowInput(snapshotId = id),
            VerificationScheduleInput("30 4 * * *", "UTC"),
            VerificationSchedule("30 4 * * *", "UTC", at),
            VerifierMisfit(id, "verifier1", listOf(VerifierReason.SAME_HOST, VerifierReason.SPACE_UNKNOWN)),
            verificationJob,
            SourceVerificationState(true, VerificationOutcome.NO_VERIFIER, at, 5, at, verificationJob),
            VerificationStage(1, "restic_check", VerificationStageStatus.PASSED, at, at, 0, "ok", ""),
            FingerprintMismatch("tables", "12", "0"),
            verificationRecord,
            VerificationPage(listOf(verificationRecord), nextCursor = null),
            verifierSample,
            Overview(1, 3, FirstSteps(true, true, false, false, false, complete = false)),
            CaInfo(CA_FINGERPRINT, CaOrigin.GENERATED, "/var/lib/sard/pki/ca/ca.key"),
            SessionRequest("secret"),
            Session(id, at),
            onboarding(access = OnboardingAccess.SETUP, withCa = true),
            onboarding(access = OnboardingAccess.NONE, withCa = false),
            SetupCodeRequest("ABCD-EFGH-JKMN-PQRS-TVWX-YZ01-2345"),
            AdminStepRequest("correct-horse-battery"),
            PasswordChangeRequest("correct-horse-battery", "new-password-2026"),
            AgentPage(listOf(summary), nextCursor = "c2"),
            AgentDetails(
                id,
                "db1",
                AgentStatus.OFFLINE,
                agentVersion = null,
                os = null,
                arch = null,
                at,
                lastSeenAt = null,
                revokedAt = at,
                duplicateSessionAt = null,
                protocolVersion = 1,
                plugins = listOf(AgentPlugin("files", "0.1.0", listOf(StepAction.BACKUP), schema)),
                repositories =
                    listOf(AgentRepository("local", "local", repositoryId = null, "file", RecoveryKeyFlag.UNKNOWN)),
                secretNames = listOf("pg"),
                scriptNames = listOf("flush"),
                outdated = true,
                builtin = false,
                role = AgentRole.VERIFIER,
                restoreDir = "/var/lib/sard-agent/restore",
                certificate = null,
                verifierSetup =
                    listOf(VerifierSetupEntry("r1", id, "src1", "main", "sudo sard-agent repo key add main", null)),
            ),
            RepositoryList(
                listOf(
                    RepositoryInfo(
                        "r1",
                        RecoveryKeyFlag.ABSENT,
                        at,
                        id,
                        "sudo sard-agent repo key add main",
                        listOf(RepositoryHolderInfo(id, "src1", AgentRole.SOURCE, "main")),
                    ),
                ),
                withoutRecoveryKey = 1,
            ),
            RestoreOptions(
                RestoreSnapshot("4f1c", at, 10, "main", "etc", "files"),
                listOf(RestoreTarget(id, "db1", true, true, "main", "/r", schema, mapOf("destination" to "etc/x"))),
                listOf(IneligibleAgent(id, "db3", IneligibleReason.AGENT_OUTDATED)),
                AttachHint("main", "sudo sard-agent repo add main <address> --password-from-file <file>"),
            ),
            RestoreRequest(id.toString(), mapOf("destination" to "etc/x")),
            RestorePreview(
                "etc",
                "4f1c",
                at,
                10,
                id,
                "db1",
                agentOnline = false,
                "/r/etc/x",
                listOf("/etc"),
                restoreGlobals = false,
                listOf(RestoreWarningCode.AGENT_OFFLINE),
            ),
            AgentInstall(
                downloadsEnabled = true,
                agentVersion = "v1.4.0",
                resticVersion = null,
                InstallArch.ARM64,
                InstallFormat.TAR,
                signed = false,
                ReleaseKey("DF5D5B6DB257DBFA", "RWT621eybVtd38CL7B33xZrcc8ArYiPt3GlKXyJuk9ZQzDnoUV+kLi2d"),
                manualInstallDoc = "https://example.com/doc",
                steps = listOf(InstallStep(StepKind.REPO_ADD, listOf("true"), optional = false)),
            ),
            AgentUpgrade(
                downloadsEnabled = true,
                agentVersion = "v1.4.0",
                resticVersion = "0.19.1",
                arch = null,
                InstallFormat.DEB,
                signed = true,
                ReleaseKey("DF5D5B6DB257DBFA", "RWT621eybVtd38CL7B33xZrcc8ArYiPt3GlKXyJuk9ZQzDnoUV+kLi2d"),
                manualInstallDoc = "https://example.com/doc",
                steps = emptyList(),
                reason = UpgradeReason.ARCH_UNKNOWN,
                keepsConfiguration = false,
            ),
            CreateEnrollmentTokenRequest(ttlSeconds = 3600),
            CreatedEnrollmentToken(id, "sard_x.y", "sard-agent enroll --server s:9090 --token sard_x.y", at, false),
            EnrollmentTokenPage(listOf(token), nextCursor = null),
            SourceInput("etc", id, "files", "local", mapOf("paths" to listOf("/etc"))),
            source,
            SourcePage(listOf(source, systemSource), nextCursor = null),
            SelfBackupRepositoryInput("offsite", confirmLocalStorage = false),
            SelfBackup(
                configured = true,
                agentId = id,
                SelfBackupRepository("offsite", "s3", "a1b2", local = false),
                boundAt = at,
                listOf(
                    SelfBackupSource(
                        SystemSourceRole.SELF_KEYS,
                        id,
                        id,
                        "offsite",
                        lastSuccessAt = at,
                        lastFailureAt = null,
                        SelfBackupLastRun(id, RunStatus.FAILED, at),
                    ),
                    SelfBackupSource(SystemSourceRole.SELF_DATABASE, id, id, "offsite", null, null, null),
                ),
                SelfBackupStatus.OVERDUE,
                listOf(SelfBackupWarningCode.OVERDUE, SelfBackupWarningCode.LOCAL_STORAGE),
                overdueSince = at,
                repositories = listOf(SelfBackupRepository("offsite", "s3", "a1b2", local = false)),
                plan =
                    listOf(
                        SelfBackupPlanItem(
                            SystemSourceRole.SELF_DATABASE,
                            "Sard: database",
                            "postgresql",
                            listOf("sard"),
                            emptyList(),
                        ),
                    ),
                firstBackupDone = true,
                keysConfirmedAt = at,
                passwordRevealCommand = "docker compose exec self-agent sard-agent repo password offsite --reveal",
                recoveryKeyCommand = "docker compose run self-agent repo key add offsite --for recovery --if-missing",
            ),
            SelfBackup(
                configured = false,
                agentId = null,
                repository = null,
                boundAt = null,
                sources = emptyList(),
                SelfBackupStatus.NOT_CONFIGURED,
                warnings = emptyList(),
                overdueSince = null,
                repositories = emptyList(),
                plan = emptyList(),
                firstBackupDone = false,
                keysConfirmedAt = null,
                passwordRevealCommand = null,
                recoveryKeyCommand = null,
            ),
            KeysConfirmationInput(copiedOutsideServer = true),
            RepositoryCommand("self", "/var/lib/sard-self/self", listOf("docker compose restart self-agent")),
            RecoveryCard("self", "s3", null, "a1b2", CA_FINGERPRINT, "0.1.0", at, "https://example.test/doc"),
            SelfBackupRuns(listOf(SelfBackupRunStarted(SystemSourceRole.SELF_DATABASE, id, id, started = false))),
            SnapshotPage(listOf(snapshot), nextCursor = null),
            ScheduleInput("30 2 * * *", "Europe/Berlin", enabled = true),
            schedule,
            ScheduleFirePage(listOf(fire), nextCursor = null),
            SchedulePreview(
                "30 2 * * *",
                "Europe/Berlin",
                "Every day at 02:30",
                listOf(at, at, at),
                tooFrequent = false,
            ),
            Run(
                id,
                id,
                "etc",
                false,
                id,
                RunTrigger.MANUAL,
                RunStatus.RUNNING,
                null,
                at,
                at,
                finishedAt = null,
                listOf(step, queuedStep),
                catchUp = CatchUp(at, at, 4, missedCountCapped = false, timezone = "Europe/Berlin"),
                kind = RunKind.RESTORE,
                requestedBy = "admin",
                restore = RunRestore("4f1c", at, "main", mapOf("destination" to "etc/x"), "/r/etc/x"),
            ),
            RunPage(listOf(summaryRun), nextCursor = null),
            LogPage(
                listOf(LogLine(1, null, LogLevel.INFO, "started")),
                nextAfterSeq = 1,
                hasMore = false,
                truncated = false,
            ),
            Problem("about:blank", "Not Found", 404, detail = null, ErrorCode.NOT_FOUND),
            ValidationProblem(
                "about:blank",
                "Unprocessable",
                422,
                "no such repository",
                ErrorCode.UNKNOWN_REPOSITORY,
                listOf(FieldError("repositoryName", "not in the agent's last Register")),
            ),
            RunActiveProblem("about:blank", "Conflict", 409, detail = null, ErrorCode.RUN_ACTIVE, id),
            TokenConflictProblem("about:blank", "Conflict", 409, null, ErrorCode.TOKEN_EXPIRED, agentId = null),
        )

    @Test
    fun `every DTO serializes as its schema says`() {
        for (sample in samples) {
            val name = sample.javaClass.simpleName
            check(mapper.valueToTree(sample), ref("#/components/schemas/$name"), name)
        }
    }

    @Test
    fun `every schema has a sample here`() {
        val objects =
            spec
                .path("components")
                .path("schemas")
                .properties()
                .filter { it.value.has("properties") }
        assertEquals(objects.map { it.key }.toSet() - NESTED, samples.map { it.javaClass.simpleName }.toSet())
    }

    private fun ref(pointer: String): JsonNode = spec.at(pointer.removePrefix("#"))

    private fun JsonNode.strings(): List<String> = iterator().asSequence().map { it.asString() }.toList()

    private fun types(schema: JsonNode): List<String> =
        if (schema.path("type").isArray) schema.path("type").strings() else listOf(schema.path("type").asString())

    private fun check(
        node: JsonNode,
        schemaNode: JsonNode,
        where: String,
    ) {
        val schema = if (schemaNode.has("\$ref")) ref(schemaNode.path("\$ref").asString()) else schemaNode
        val alternatives = schema.path("oneOf")
        when {
            alternatives.isArray -> checkOneOf(node, alternatives, where)
            node.isNull -> assertTrue("null" in types(schema), "$where is null: $schema")
            else -> checkValue(node, schema, where)
        }
    }

    /** springdoc writes a nullable reference as oneOf [ref, null]. */
    private fun checkOneOf(
        node: JsonNode,
        alternatives: JsonNode,
        where: String,
    ) {
        val nullable = alternatives.any { it.path("type").asString() == "null" }
        if (node.isNull) {
            assertTrue(nullable, "$where is null")
        } else {
            check(node, alternatives.first { it.path("type").asString() != "null" }, where)
        }
    }

    private fun checkValue(
        node: JsonNode,
        schema: JsonNode,
        where: String,
    ) {
        val enum = schema.path("enum")
        if (enum.isArray) assertTrue(enum.any { it == node }, "$where = $node: $enum")
        when (val type = types(schema).first { it != "null" }) {
            "object" -> checkObject(node, schema, where)
            "array" -> node.forEachIndexed { i, item -> check(item, schema.path("items"), "$where[$i]") }
            "string" -> checkString(node, schema.path("format").asString(), where)
            "integer" -> assertTrue(node.isIntegralNumber, "$where = $node")
            "boolean" -> assertTrue(node.isBoolean, "$where = $node")
            else -> error("$where: unexpected type $type")
        }
    }

    private fun checkObject(
        node: JsonNode,
        schema: JsonNode,
        where: String,
    ) {
        assertTrue(node.isObject, "$where = $node")
        val properties = schema.path("properties")
        if (properties.isMissingNode) return // a free-form map: config, configSchema
        assertEquals(properties.propertyNames().toSet(), node.propertyNames().toSet(), where)
        val required = schema.path("required").strings()
        assertTrue(node.propertyNames().containsAll(required), "$where lacks some of $required")
        properties.properties().forEach { (name, property) -> check(node.path(name), property, "$where.$name") }
    }

    private fun checkString(
        node: JsonNode,
        format: String,
        where: String,
    ) {
        assertTrue(node.isString, "$where = $node")
        when (format) {
            "uuid" -> UUID.fromString(node.asString())
            "date-time" -> Instant.parse(node.asString())
        }
    }

    private companion object {
        /** Reached through their parents' samples. */
        val NESTED =
            setOf(
                "OnboardingStep",
                "SelfBackupPlanItem",
                "AgentSummary",
                "InstallStep",
                "ReleaseKey",
                "FirstSteps",
                "AgentCertificate",
                "AgentPlugin",
                "AgentRepository",
                "VerifierSetupEntry",
                "RepositoryInfo",
                "RepositoryHolderInfo",
                "EnrollmentToken",
                "Snapshot",
                "ScheduleFire",
                "ScheduleLastRun",
                "CatchUp",
                "RunStep",
                "BackupOutput",
                "Fingerprint",
                "FingerprintFact",
                "RestoreOutput",
                "RunRestore",
                "RestoreSnapshot",
                "RestoreTarget",
                "IneligibleAgent",
                "AttachHint",
                "RunSummary",
                "LogLine",
                "FieldError",
                "SelfBackupRepository",
                "SelfBackupSource",
                "SelfBackupRunStarted",
                "SelfBackupLastRun",
            )
    }
}
