// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

/**
 * A value of a JSON document, read by [parse]: the drill reads a few fields of the REST API's answers and does not
 * add a library for it. A missing key or a null is [isNull]; reading [text], [int] or [bool] of a value of another
 * kind fails.
 */
internal class Json private constructor(
    private val value: Any?,
) {
    val isNull: Boolean get() = value == null
    val text: String? get() = value as String?
    val int: Int get() = (value as String).toInt()
    val bool: Boolean get() = value as Boolean

    /** The number or word as written, for a value that is not a string. */
    val raw: String get() = value.toString()
    val items: List<Json> get() = (value as List<*>).map { Json(it) }
    val keys: List<String> get() = (value as Map<*, *>).keys.map { it as String }

    operator fun get(key: String): Json = Json((value as Map<*, *>)[key])

    override fun toString() = value.toString()

    companion object {
        fun parse(source: String): Json {
            val reader = Reader(source)
            val value = reader.value()
            reader.end()
            return Json(value)
        }
    }

    private class Reader(
        private val s: String,
    ) {
        private var i = 0

        fun end() {
            skip()
            require(i == s.length) { "JSON: text after the value at $i" }
        }

        fun value(): Any? {
            skip()
            require(i < s.length) { "JSON: the text ends where a value should be" }
            return when (val c = s[i]) {
                '{' -> obj()
                '[' -> list()
                '"' -> string()
                else -> if (c == '-' || c.isDigit()) number() else word()
            }
        }

        private fun skip() {
            while (i < s.length && s[i].isWhitespace()) i++
        }

        private fun expect(c: Char) {
            skip()
            require(i < s.length && s[i] == c) { "JSON: expected '$c' at $i" }
            i++
        }

        private fun obj(): Map<String, Any?> {
            expect('{')
            val map = linkedMapOf<String, Any?>()
            skip()
            if (peek('}')) return map.also { i++ }
            do {
                skip()
                val key = string()
                expect(':')
                map[key] = value()
                skip()
            } while (comma())
            expect('}')
            return map
        }

        private fun list(): List<Any?> {
            expect('[')
            val list = mutableListOf<Any?>()
            skip()
            if (peek(']')) return list.also { i++ }
            do {
                list += value()
                skip()
            } while (comma())
            expect(']')
            return list
        }

        private fun peek(c: Char) = i < s.length && s[i] == c

        private fun comma(): Boolean = peek(',').also { if (it) i++ }

        private fun string(): String {
            expect('"')
            val out = StringBuilder()
            while (true) {
                require(i < s.length) { "JSON: unterminated string" }
                val c = s[i++]
                when (c) {
                    '"' -> return out.toString()
                    '\\' -> out.append(escape())
                    else -> out.append(c)
                }
            }
        }

        private fun escape(): Char {
            require(i < s.length) { "JSON: unterminated escape" }
            return when (val e = s[i++]) {
                'n' -> '\n'
                't' -> '\t'
                'r' -> '\r'
                'b' -> '\b'
                'f' -> '\u000C'
                'u' -> s.substring(i, i + 4).toInt(16).toChar().also { i += 4 }
                else -> e
            }
        }

        private fun number(): String {
            val start = i
            while (i < s.length && (s[i].isDigit() || s[i] in "+-.eE")) i++
            return s.substring(start, i)
        }

        private fun word(): Any? {
            val start = i
            while (i < s.length && s[i].isLetter()) i++
            return when (val w = s.substring(start, i)) {
                "true" -> true
                "false" -> false
                "null" -> null
                else -> throw IllegalArgumentException("JSON: unexpected '$w' at $start")
            }
        }
    }
}
