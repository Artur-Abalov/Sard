// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import io.swagger.v3.oas.models.Components
import io.swagger.v3.oas.models.OpenAPI
import io.swagger.v3.oas.models.media.ObjectSchema
import io.swagger.v3.oas.models.media.StringSchema
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

@MutFlowTest
class RequireConstructorParametersTest {
    private fun objectSchema(vararg properties: String): ObjectSchema {
        val schema = ObjectSchema()
        properties.forEach { schema.addProperty(it, StringSchema()) }
        return schema
    }

    @Test
    fun `parameters without a default are required, nullable or not`() {
        val tokens = objectSchema("ttlSeconds")
        val step = objectSchema("snapshotId", "totalBytes", "addedBytes")
        val status = objectSchema("version", "lastVerifiedRestoreAt")
        val api =
            OpenAPI().components(
                Components()
                    .addSchemas("CreateEnrollmentTokenRequest", tokens)
                    .addSchemas("BackupOutput", step)
                    .addSchemas("StatusResponse", status),
            )

        MutFlow.underTest { requireConstructorParameters(api) }

        assertNull(tokens.required)
        assertEquals(listOf("addedBytes", "snapshotId", "totalBytes"), step.required) // swagger sorts required
        assertEquals(listOf("lastVerifiedRestoreAt", "version"), status.required)
    }

    @Test
    fun `schemas that are not classes of the api package are left alone`() {
        val unknown = objectSchema("a")
        val enum = StringSchema()
        val api = OpenAPI().components(Components().addSchemas("Unknown", unknown).addSchemas("StepStatus", enum))

        MutFlow.underTest { requireConstructorParameters(api) }

        assertNull(unknown.required)
        assertNull(enum.required)
    }

    @Test
    fun `a document without components is left alone`() {
        val api = OpenAPI()
        MutFlow.underTest { requireConstructorParameters(api) }
        assertNull(api.components)
    }
}
