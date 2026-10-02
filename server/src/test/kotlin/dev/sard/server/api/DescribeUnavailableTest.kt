// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import io.swagger.v3.oas.models.OpenAPI
import io.swagger.v3.oas.models.Operation
import io.swagger.v3.oas.models.PathItem
import io.swagger.v3.oas.models.Paths
import io.swagger.v3.oas.models.responses.ApiResponses
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

@MutFlowTest
class DescribeUnavailableTest {
    private fun operation() = Operation().responses(ApiResponses())

    @Test
    fun `every operation under api v1, of any method, gets 503 unavailable`() {
        val get = operation()
        val post = operation()
        val delete = operation()
        val api =
            OpenAPI().paths(
                Paths()
                    .addPathItem("/api/v1/agents", PathItem().get(get).post(post))
                    .addPathItem("/api/v1/sources/{id}", PathItem().delete(delete)),
            )

        MutFlow.underTest { describeUnavailable(api) }

        for (op in listOf(get, post, delete)) {
            val response = op.responses["503"]!!
            assertEquals("#/components/schemas/Problem", response.content["application/problem+json"]!!.schema.`$ref`)
        }
    }

    @Test
    fun `a path outside the API is left alone`() {
        val health = operation()
        val api = OpenAPI().paths(Paths().addPathItem("/actuator/health", PathItem().get(health)))

        MutFlow.underTest { describeUnavailable(api) }

        assertNull(health.responses["503"])
    }

    @Test
    fun `a document without paths is left alone`() {
        val api = OpenAPI()
        MutFlow.underTest { describeUnavailable(api) }
        assertNull(api.paths)
    }
}
