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
class DescribeOriginRejectedTest {
    private fun operation() = Operation().responses(ApiResponses())

    @Test
    fun `mutating operations get 403 origin_rejected, a GET does not`() {
        val post = operation()
        val put = operation()
        val patch = operation()
        val delete = operation()
        val get = operation()
        val item =
            PathItem()
                .post(post)
                .put(put)
                .patch(patch)
                .delete(delete)
                .get(get)
        val api = OpenAPI().paths(Paths().addPathItem("/a", item))

        MutFlow.underTest { describeOriginRejected(api) }

        for (op in listOf(post, put, patch, delete)) {
            val response = op.responses["403"]!!
            assertEquals("#/components/schemas/Problem", response.content[PROBLEM_JSON]!!.schema.`$ref`)
        }
        assertNull(get.responses["403"])
    }

    @Test
    fun `a document without paths is left alone`() {
        val api = OpenAPI()
        MutFlow.underTest { describeOriginRejected(api) }
        assertNull(api.paths)
    }

    private companion object {
        const val PROBLEM_JSON = "application/problem+json"
    }
}
