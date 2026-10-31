// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.extension.Operation

/** Gives the public operations of the extensions an empty security requirement; undescribed ones are skipped. */
internal fun describePublicOperations(
    api: io.swagger.v3.oas.models.OpenAPI,
    operations: Set<Operation>,
) {
    for (operation in operations) documented(api, operation)?.security = emptyList()
}

private fun documented(
    api: io.swagger.v3.oas.models.OpenAPI,
    operation: Operation,
): io.swagger.v3.oas.models.Operation? =
    operationsAt(api, operation.pathTemplate).entries.firstOrNull { it.key.name == operation.method }?.value

private fun operationsAt(
    api: io.swagger.v3.oas.models.OpenAPI,
    template: String,
): Map<io.swagger.v3.oas.models.PathItem.HttpMethod, io.swagger.v3.oas.models.Operation> =
    api.paths
        ?.get(template)
        ?.readOperationsMap()
        .orEmpty()
