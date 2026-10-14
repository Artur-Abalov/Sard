// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

/** The small JSON reader of the drill: the answers of the REST API of the machines, no library. */
class JsonTest {
    @Test
    fun `objects, arrays, strings, numbers, booleans and null`() {
        val json = Json.parse("""{"a":"x\ny\"\\ A","n":-12,"f":1.5,"t":true,"z":null,"list":[1,{"k":"v"}],"empty":{}}""")

        assertEquals("x\ny\"\\ A", json["a"].text)
        assertEquals(-12, json["n"].int)
        assertEquals(true, json["t"].bool)
        assertTrue(json["z"].isNull)
        assertTrue(json["missing"].isNull)
        assertEquals(listOf("1", "v"), listOf(json["list"].items[0].raw, json["list"].items[1]["k"].text))
        assertEquals(0, json["empty"].keys.size)
        assertEquals(listOf("a", "n", "f", "t", "z", "list", "empty"), json.keys)
    }

    @Test
    fun `a text that is not JSON is refused`() {
        assertFailsWith<IllegalArgumentException> { Json.parse("{\"a\":") }
        assertFailsWith<IllegalArgumentException> { Json.parse("[1,]") }
        assertFailsWith<IllegalArgumentException> { Json.parse("{} x") }
    }
}
