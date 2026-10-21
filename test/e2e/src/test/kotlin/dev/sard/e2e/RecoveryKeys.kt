// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

/** What the stand asks of the server about recovery keys (V3, `GET /api/v1/repositories`) and of `repo key list`. */
internal object RecoveryKeys {
    /** The password of a key as the agent generates it: 32 bytes, 43 characters of base64url. */
    val PASSWORD = Regex("(?<![A-Za-z0-9_-])[A-Za-z0-9_-]{43}(?![A-Za-z0-9_-])")

    /** The `recoveryKey` the server shows for the repository [name] that [agentId] holds; null while it lists none. */
    fun flag(
        api: SardApi,
        agentId: String,
        name: String,
    ): String? {
        val list = api.get("/api/v1/repositories")
        check(list.status == SardApi.HTTP_OK) { "GET /api/v1/repositories answered ${list.status}" }
        return Json
            .parse(list.body)["items"]
            .items
            .firstOrNull { item -> item["holders"].items.any { it["agentId"].text == agentId && it["repositoryName"].text == name } }
            ?.get("recoveryKey")
            ?.text
    }

    /** Waits until the server shows [expected] for the repository: the agent tells it at the next Register (within 60 s). */
    fun awaitFlag(
        api: SardApi,
        agentId: String,
        name: String,
        expected: String,
    ) = Await.until("recoveryKey $expected of $name on the server") { flag(api, agentId, name) == expected }

    /** The ids of the keys of [keys] (`repo key list --json`) whose purpose is recovery. */
    fun recoveryIds(keys: Json): List<String> = keys.items.filter { it["purpose"].text == "recovery" }.map { it["id"].text!! }
}
