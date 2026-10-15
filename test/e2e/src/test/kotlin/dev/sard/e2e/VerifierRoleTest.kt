// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.RegisterExtension
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

/**
 * V1, the `@e2e` scenario "Верификатор на стенде виден с ролью и не выполняет бэкап" of
 * docs/specs/server/agent-role.feature: a source agent X and a verifier V in separate containers,
 * V enrolled with the command of a token created with role verifier. The REST API lists both with
 * their roles, refuses a source bound to V, and X's backup runs while V's log never accepts a step.
 */
class VerifierRoleTest {
    @Test
    fun `a verifier on the stand is listed with its role and does not run a backup`() {
        val x = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard, role = "source"), hostname = "role-source", local = LOCAL)
        val v = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard, role = "verifier"), hostname = "role-verifier", local = LOCAL)
        val tree = SourceTree.generate(Chain.SEED)
        x.host.put(Chain.DATA, tree.files())
        val source = Chain.ready(sard, x)
        val verifier = sard.track("agent-role-verifier", AgentContainer.of(v)).apply { start() }
        Await.until("Register of the verifier") { Backups.connected(sard, v.agentId) }
        Await.until("the verifier online") { row(v.agentId)?.field("status") == "online" }

        assertEquals("verifier", row(v.agentId)?.field("role"))
        assertEquals("source", row(x.agentId)?.field("role"))

        val refused =
            SardApi(sard).post(
                "/api/v1/sources",
                """{"name":"on-verifier","agentId":"${v.agentId}","plugin":"files","repositoryName":"${Chain.REPOSITORY}","config":${Chain.files(Chain.DATA)}}""",
            )
        assertEquals(UNPROCESSABLE, refused.status)
        assertEquals("agent_role_mismatch", refused.field("code"))
        assertEquals("agentId", refused.field("field"))

        val started = Backups.start(sard, x.agentId, "role-source", Chain.files(Chain.DATA, tree.excludePatterns(Chain.DATA)))
        val step = Backups.awaitFinished(sard, started.stepId)
        assertEquals("succeeded", step.status, step.message)
        assertTrue("step accepted" in source.logs, "the source agent's log has no accepted step: the check below proves nothing")
        assertFalse("step accepted" in verifier.logs, "the verifier accepted a step")
    }

    /** The agent's object in the list `GET /api/v1/agents` as the administrator sees it; items hold no nested objects. */
    private fun row(agentId: String): SardApi.Answer? {
        val list = SardApi(sard).get("/api/v1/agents")
        assertEquals(SardApi.HTTP_OK, list.status)
        val item = list.body.split("},{").firstOrNull { "\"$agentId\"" in it }
        return item?.let { SardApi.Answer(list.status, it) }.also { assertNotNull(it, "agent $agentId is not in the list") }
    }

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private const val UNPROCESSABLE = 422
        private val LOCAL = Chain.repository("${AgentHost.STATE_DIR}/repo")
    }
}
