// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import com.github.dockerjava.api.model.Bind
import com.github.dockerjava.api.model.Volume
import org.junit.jupiter.api.extension.RegisterExtension
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * J1f, cancel of a restore verification with real containers (docs/specs/server/run-cancel-deadlines.feature, rule
 * «Сквозная отмена проверки на образах»): the console's `POST /api/v1/verification-jobs/{id}/cancel` stops a
 * verification of a postgresql source while its sandbox runs; the job closes with a record `cancelled`, no sandbox
 * container with the job's label is left (the cleanup runs under its own limit, not the cancelled step's) and no copy
 * stays on the verifier's work volume. Stand as of [VerificationPostgresTest].
 */
class VerificationCancelTest {
    @Test
    fun `cancelling a running verification over REST closes the job and leaves no sandbox`() {
        stand()
        val volumes = verifications.volumes()
        val snapshot = backup("pg-cancel")
        val job = verifications.start(snapshot)
        Await.until("the sandbox of job $job runs") { verifications.sandboxes().isNotEmpty() }

        val answer = SardApi(sard).post("/api/v1/verification-jobs/$job/cancel")

        assertEquals(SardApi.HTTP_OK, answer.status, answer.body)
        val record = Await.value("the record of job $job", Verifications.VERIFY_TIMEOUT) { verifications.recordOf(job) }
        assertEquals("cancelled", record.status, record.reason)
        assertEquals(0, record.level)
        val source = verifications.sourceOf(snapshot)
        val open = SardApi(sard).get("/api/v1/sources/$source/verification")
        assertTrue(Regex("\"openJob\"\\s*:\\s*null").containsMatchIn(open.body), open.body)
        Await.until("no sandbox with the label of the job") { verifications.sandboxes().isEmpty() }
        assertEquals(emptySet(), verifications.volumes() - volumes, "a sandbox left a volume")
        assertEquals(emptyList(), verifier.workVolume(), "the cancelled verification left a copy on the verifier")
    }

    /** Backs up the seeded database as a new source [name] and returns its snapshot's id (snapshots.id). */
    private fun backup(name: String): UUID {
        val started = Backups.start(sard, source.agentId, name, PgAgent.configK(database = "app"), plugin = "postgresql", repository = REPOSITORY)
        // Verification is on by default (V4); «verify now» does not need it, the cycle would race it.
        verifications.enable(started.sourceId, enabled = false)
        val step = Backups.awaitFinished(sard, started.stepId)
        assertEquals("succeeded", step.status, step.message)
        return verifications.snapshotOfStep(started.stepId)
    }

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private val verifications = Verifications(sard)
        private const val REPOSITORY = Chain.REPOSITORY

        private val repository by lazy { verifications.agentsVolume() }

        private val db by lazy { PgServer(sard, 18).start().apply { seed() } }

        private val source: EnrolledAgent by lazy {
            val local = Chain.repository("/srv/$REPOSITORY") + "secrets:\n  pg-app: ${PgAgent.SECRET_FILE}\n"
            val host =
                AgentHost(
                    sard,
                    "pg-source",
                    local,
                    image = E2e.pgAgentImage(18),
                    mounts = listOf(Bind(repository, Volume("/srv/$REPOSITORY"))),
                    bindings = mapOf("pg-app" to AgentHost.binding("postgresql", PgAgent.BOUND_SERVERS)),
                )
            val agent = EnrolledAgent(AgentEnroller.enroll(host, EnrollmentTokens.create(sard, role = "source")), host)
            val init = host.repoInit(REPOSITORY, "--generate-password")
            assertEquals(0, init.code, init.stderr)
            sard.secret(PG_PASSWORD)
            sard.track("agent-pg-source", AgentContainer.of(agent, mapOf(PgAgent.SECRET_FILE to "$PG_PASSWORD\n"))).apply { start() }
            Await.until("repository_id of $REPOSITORY in Register of pg-source") { Backups.repositoryId(sard, agent.agentId, REPOSITORY) != null }
            agent
        }

        /** A key of the repository issued on the source host for the verifier (V3). */
        private fun verifierKey(): String {
            val file = "${AgentHost.STATE_DIR}/issued-$REPOSITORY.pass"
            val add =
                source.host.run(
                    AgentImage.AGENT_BINARY, "repo", "key", "add", "--config", AgentHost.CONFIG, "--for", "verifier",
                    "--to-file", file, "--no-restart", REPOSITORY,
                    asRoot = true,
                )
            assertEquals(0, add.code, add.stderr)
            return String(source.host.read(file)).trimEnd('\n').also(sard::secret)
        }

        private val verifier by lazy {
            VerifierStand(sard, verifications).apply {
                val key = verifierKey()
                enroll(listOf(VerifierStand.Shared(REPOSITORY, VerifierStand.readOnly(repository), "${AgentHost.STATE_DIR}/verifier-main.pass", key)))
                start()
            }
        }

        /** The server, the source and the verifier, once for the class. */
        private fun stand() {
            db
            source
            verifier
        }
    }
}
