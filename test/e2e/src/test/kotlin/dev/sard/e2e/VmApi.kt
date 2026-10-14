// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

/**
 * The REST API of the server that runs inside a [RecoveryVm], as the administrator sees it: `curl` on the machine's
 * loopback with a cookie jar of its own. Request bodies travel on the standard input of `curl`, never in the command,
 * so a password in a body is not a command-line argument.
 */
internal class VmApi(
    private val vm: RecoveryVm,
    private val cookie: String = "/root/.drill-cookie",
) {
    class Answer(
        val status: Int,
        val body: String,
    ) {
        val json: Json by lazy { Json.parse(body) }

        override fun toString() = "Answer(status=$status)"
    }

    fun get(path: String) = send("GET", path, null)

    fun post(
        path: String,
        body: String? = null,
    ) = send("POST", path, body)

    fun put(
        path: String,
        body: String,
    ) = send("PUT", path, body)

    /** Signs in as the administrator; fails unless the server answers 204. */
    fun login(password: String) {
        val answer = post("/api/v1/session", """{"password":"${password.replace("\\", "\\\\").replace("\"", "\\\"")}"}""")
        check(answer.status == NO_CONTENT) { "sign-in answered ${answer.status}" }
    }

    /** [post] that must answer [expected]. */
    fun created(
        path: String,
        body: String? = null,
        expected: Int = CREATED,
    ): Answer = post(path, body).also { check(it.status == expected) { "POST $path answered ${it.status}: ${it.body}" } }

    /** Every item of a list endpoint (`items` and `nextCursor`), page after page. */
    fun items(path: String): List<Json> {
        val items = mutableListOf<Json>()
        var cursor: String? = null
        do {
            val separator = if ('?' in path) '&' else '?'
            val page = get("$path${separator}limit=$PAGE" + (cursor?.let { "&cursor=$it" } ?: ""))
            check(page.status == OK) { "GET $path answered ${page.status}: ${page.body}" }
            items += page.json["items"].items
            cursor = page.json["nextCursor"].text
        } while (cursor != null)
        return items
    }

    private fun send(
        method: String,
        path: String,
        body: String?,
    ): Answer {
        val data = if (body != null) "--data-binary @- " else ""
        val script = "curl -sS -w '\\n%{http_code}' -b $cookie -c $cookie -X $method -H 'Content-Type: application/json' $data'http://localhost:8080$path'"
        val exit = vm.run(script, stdin = listOfNotNull(body))
        check(exit.code == 0) { "curl $method $path exited ${exit.code}: ${exit.stderr}" }
        val out = exit.stdout.trimEnd('\n')
        return Answer(out.substringAfterLast('\n').toInt(), out.substringBeforeLast('\n', ""))
    }

    companion object {
        const val OK = 200
        const val CREATED = 201
        const val NO_CONTENT = 204
        private const val PAGE = 200
    }
}
