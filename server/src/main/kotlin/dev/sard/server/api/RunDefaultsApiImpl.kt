// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.events.SettingsEvents
import dev.sard.server.extension.TenantResolver
import dev.sard.server.runs.TimeoutDefaults
import dev.sard.server.runs.TimeoutSettings
import org.springframework.stereotype.Component

/** The run-defaults endpoints over [TimeoutSettings], in the tenant of the session. */
@Component
class RunDefaultsApiImpl(
    private val settings: TimeoutSettings,
    private val tenants: TenantResolver,
    private val events: SettingsEvents,
) : RunDefaultsApi {
    override fun getRunDefaults(): RunDefaults = of(settings.get(tenants.currentTenantId()))

    override fun setRunDefaults(input: RunDefaults): RunDefaults {
        val backup = checkNotNull(timeoutOf("backupTimeoutMinutes", input.backupTimeoutMinutes))
        val restore = checkNotNull(timeoutOf("restoreTimeoutMinutes", input.restoreTimeoutMinutes))
        val tenant = tenants.currentTenantId()
        val set =
            events.whenChanged(
                "run_defaults",
                null,
                { settings.get(tenant) },
            ) { settings.set(tenant, TimeoutDefaults(backup, restore)) }
        return of(set)
    }

    private fun of(defaults: TimeoutDefaults) = RunDefaults(defaults.backupMinutes, defaults.restoreMinutes)
}
