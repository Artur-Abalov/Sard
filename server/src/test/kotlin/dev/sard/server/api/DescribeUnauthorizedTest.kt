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
class DescribeUnauthorizedTest {
    private fun operation() = Operation().responses(ApiResponses())

    @Test
    fun `an operation inheriting the root security gets 401, a public one does not`() {
        val guarded = operation()
        val public = operation().security(emptyList())
        val api = OpenAPI().paths(Paths().addPathItem("/a", PathItem().get(guarded).post(public)))

        MutFlow.underTest { describeUnauthorized(api) }

        val response = guarded.responses["401"]!!
        assertEquals("#/components/schemas/Problem", response.content[PROBLEM_JSON]!!.schema.`$ref`)
        assertEquals("No session or it expired", response.description)
        assertNull(public.responses["401"])
    }

    @Test
    fun `a document without paths is left alone`() {
        val api = OpenAPI()
        MutFlow.underTest { describeUnauthorized(api) }
        assertNull(api.paths)
    }
}
