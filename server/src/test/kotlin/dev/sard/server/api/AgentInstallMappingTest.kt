// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import kotlin.reflect.KClass
import kotlin.test.Test
import kotlin.test.assertEquals

private fun names(type: KClass<out Enum<*>>) = type.java.enumConstants.map { it.name }

/** The api enums are the wire twins of the install package's; AgentInstallApiImpl maps them by constant name. */
class AgentInstallMappingTest {
    @Test
    fun `every install enum of the domain has a wire twin with the same constants`() {
        val pairs =
            listOf(
                dev.sard.server.install.InstallArch::class to InstallArch::class,
                dev.sard.server.install.InstallFormat::class to InstallFormat::class,
                dev.sard.server.install.FetchTool::class to FetchTool::class,
                dev.sard.server.install.StepKind::class to StepKind::class,
                dev.sard.server.install.UpgradeReason::class to UpgradeReason::class,
            )
        for ((domain, wire) in pairs) assertEquals(names(domain), names(wire), wire.simpleName)
    }
}
