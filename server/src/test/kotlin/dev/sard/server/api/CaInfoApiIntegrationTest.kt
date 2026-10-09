// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.pki.CertificateAuthority
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.web.server.LocalServerPort
import tools.jackson.databind.ObjectMapper
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse

/** Rule "Отпечаток CA виден в логе при старте и в консоли" (F8, Р7): API only with an administrator session. */
@RestApiTest
class CaInfoApiIntegrationTest(
    @Autowired private val ca: CertificateAuthority,
    @Autowired mapper: ObjectMapper,
    @LocalServerPort port: Int,
) {
    private val api = ApiClient(port, mapper)

    @Test
    fun `the API gives the administrator the fingerprint of the CA`() {
        val response = api.get("/api/v1/ca", api.signIn())
        assertEquals(200, response.status)
        assertEquals(ca.fingerprint().hex, response.json.path("fingerprint").asString())
    }

    @Test
    fun `without a session the API refuses and does not give the fingerprint`() {
        val response = api.get("/api/v1/ca", null)
        assertEquals(401, response.status)
        assertEquals("unauthenticated", response.code)
        assertFalse(ca.fingerprint().hex in response.body)
    }

    @Test
    fun `the public status does not carry the fingerprint`() {
        assertFalse(ca.fingerprint().hex in api.get("/api/v1/status", null).body)
    }
}
